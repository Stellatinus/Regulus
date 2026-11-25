package backup

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

// ErrWrongPassphrase is the answer to a passphrase that does not open the file.
var ErrWrongPassphrase = errors.New("the passphrase is wrong, or the file is not a Regulus backup")

// maxManifest bounds the manifest, which is read into memory.
const maxManifest = 1 << 20

// Extract decrypts a backup, writes its parts under dir (which must exist and
// be private) and checks them: the first entry is the manifest, the format is
// one this version knows, every part is listed once with the size and SHA-256
// it has, every listed part is there, and no entry has a name outside
// manifest.json, secret.key, pki/ and db/. Nothing of the installation is
// touched, so a restore verifies first and changes things after.
func Extract(r io.Reader, passphrase, dir string) (Manifest, error) {
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return Manifest{}, err
	}
	dr, err := age.Decrypt(r, id)
	if err != nil {
		var nm *age.NoIdentityMatchError
		if errors.As(err, &nm) {
			return Manifest{}, ErrWrongPassphrase
		}
		return Manifest{}, fmt.Errorf("%w (%v)", ErrWrongPassphrase, err)
	}
	tr := tar.NewReader(dr)
	hdr, err := tr.Next()
	if err != nil || hdr.Name != manifestName || hdr.Size > maxManifest {
		return Manifest{}, errors.New("the backup does not start with its manifest")
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(tr, maxManifest)).Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("read the manifest: %w", err)
	}
	if m.FormatVersion > FormatVersion {
		return Manifest{}, fmt.Errorf("%w (format %d, this version reads up to %d)", ErrFormatNewer, m.FormatVersion, FormatVersion)
	}
	if m.FormatVersion < 1 {
		return Manifest{}, errors.New("the manifest has no format version")
	}
	listed := map[string]Part{}
	for _, p := range m.Parts {
		if !validName(p.Name) {
			return Manifest{}, fmt.Errorf("the manifest lists a part with an invalid name %q", p.Name)
		}
		if _, dup := listed[p.Name]; dup {
			return Manifest{}, fmt.Errorf("the manifest lists %s twice", p.Name)
		}
		listed[p.Name] = p
	}
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("read the backup: %w", err)
		}
		p, ok := listed[hdr.Name]
		if !ok || hdr.Typeflag != tar.TypeReg || seen[hdr.Name] {
			return Manifest{}, fmt.Errorf("the backup has an entry %q that its manifest does not list", hdr.Name)
		}
		seen[hdr.Name] = true
		path := filepath.Join(dir, filepath.FromSlash(hdr.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return Manifest{}, err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return Manifest{}, err
		}
		h := sha256.New()
		n, cerr := io.Copy(io.MultiWriter(f, h), io.LimitReader(tr, p.Size+1))
		if err := errors.Join(cerr, f.Close()); err != nil {
			return Manifest{}, fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		if n != p.Size || hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
			return Manifest{}, fmt.Errorf("%s is damaged: its size or checksum is not the one in the manifest", hdr.Name)
		}
	}
	for name := range listed {
		if !seen[name] {
			return Manifest{}, fmt.Errorf("the backup lacks %s, which its manifest lists", name)
		}
	}
	for _, t := range m.Tables {
		if _, ok := listed[dbPrefix+t+tableSuffix]; !ok {
			return Manifest{}, fmt.Errorf("the manifest lists table %s but no file for it", t)
		}
	}
	return m, nil
}

// validName allows only the names Create writes.
func validName(name string) bool {
	if name != path.Clean(name) || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return false
	}
	switch {
	case name == secretName:
		return true
	case strings.HasPrefix(name, pkiPrefix):
		return !strings.Contains(strings.TrimPrefix(name, pkiPrefix), "/")
	case strings.HasPrefix(name, dbPrefix) && strings.HasSuffix(name, tableSuffix):
		return !strings.Contains(strings.TrimPrefix(name, dbPrefix), "/")
	}
	return false
}
