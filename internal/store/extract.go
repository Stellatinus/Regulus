package store

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Limits on a package. They are generous for a plugin and far below what could
// fill a disk.
const (
	maxPackageFile  = 256 << 20 // one file of the package (a program)
	maxPackageTotal = 512 << 20 // everything unpacked
	maxPackageFiles = 4096
	maxPackageDepth = 16
	// MaxPackage bounds the compressed package the hub downloads and the
	// helper copies.
	MaxPackage = 256 << 20
)

// unpack extracts a package (a gzip-compressed tar) into dest, which must not
// exist. The package holds one directory named id, whose content becomes
// dest; everything else is refused. It is the policy of the root helper, so it is strict: only regular
// files and directories (no links, devices, FIFOs), names that are clean,
// relative and inside id, no duplicates, no setuid, setgid, sticky or
// group/world-writable bits (modes become 0755 for a program and a directory,
// 0644 for any other file), and bounds on size, count and depth. ownership
// is the caller's: it runs as root, so what it creates belongs to root, and
// chown, if set, is called on each path it creates (tests use it to check
// that every path is reached).
func unpack(r io.Reader, dest, id string, chown func(string) error) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("the package is not gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	if err := os.Mkdir(dest, 0o755); err != nil {
		return err
	}
	mark := func(p string) error {
		if chown == nil {
			return nil
		}
		return chown(p)
	}
	if err := mark(dest); err != nil {
		return err
	}
	tr := tar.NewReader(&capReader{r: gz, left: maxPackageTotal + 1})
	seen := map[string]bool{}
	var total int64
	n := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading the package: %w", err)
		}
		n++
		if n > maxPackageFiles {
			return fmt.Errorf("the package has more than %d entries", maxPackageFiles)
		}
		name, err := cleanEntry(h.Name, id)
		if err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("the package lists %s twice", name)
		}
		seen[name] = true
		// The package's directory <id> is dest itself.
		target := filepath.Join(dest, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(name, id), "/")))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			if err := mark(target); err != nil {
				return err
			}
		case tar.TypeReg:
			if h.Size < 0 || h.Size > maxPackageFile {
				return fmt.Errorf("%s is larger than %d MiB", name, maxPackageFile>>20)
			}
			total += h.Size
			if total > maxPackageTotal {
				return fmt.Errorf("the package unpacks to more than %d MiB", maxPackageTotal>>20)
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, h.Size)); err != nil {
				_ = f.Close()
				return fmt.Errorf("unpacking %s: %w", name, err)
			}
			if err := f.Chmod(mode); err != nil { // not the umask's
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			if err := mark(target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s is not a regular file or a directory (a package may hold nothing else)", name)
		}
	}
	if !seen[id+"/plugin.yaml"] {
		return fmt.Errorf("the package has no %s/plugin.yaml", id)
	}
	return nil
}

// cleanEntry checks the name of a tar entry and returns it without a trailing
// slash. It must be id or inside id, relative and already clean.
func cleanEntry(name, id string) (string, error) {
	bad := func() (string, error) {
		return "", fmt.Errorf("the package has an entry with an unsafe name: %q", name)
	}
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
		return bad()
	}
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" || path.Clean(trimmed) != trimmed {
		return bad()
	}
	if trimmed != id && !strings.HasPrefix(trimmed, id+"/") {
		return "", fmt.Errorf("the package has %q outside the directory %s", name, id)
	}
	if strings.Count(trimmed, "/") >= maxPackageDepth {
		return bad()
	}
	return trimmed, nil
}

// capReader fails once more than the allowance has been read: it bounds the
// uncompressed size whatever the headers say (a compression bomb).
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errors.New("the package unpacks to too much data")
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}
