package store

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnpackPolicy(t *testing.T) {
	ok := entry{name: "disks/plugin.yaml", mode: 0o644, body: "id: disks"}
	cases := []struct {
		name    string
		entries []entry
		want    string // "" = accepted; else a fragment of the error
	}{
		{"fine", []entry{ok, {name: "disks/bin/x", mode: 0o755, body: "x"}}, ""},
		{"no manifest", []entry{{name: "disks/other", mode: 0o644, body: "x"}}, "no disks/plugin.yaml"},
		{"parent directory", []entry{ok, {name: "disks/../evil", mode: 0o644, body: "x"}}, "unsafe name"},
		{"absolute", []entry{ok, {name: "/etc/passwd", mode: 0o644, body: "x"}}, "unsafe name"},
		{"outside the plugin directory", []entry{ok, {name: "other/x", mode: 0o644, body: "x"}}, "outside"},
		{"not clean", []entry{ok, {name: "disks//x", mode: 0o644, body: "x"}}, "unsafe name"},
		{"dot segments", []entry{ok, {name: "disks/./x", mode: 0o644, body: "x"}}, "unsafe name"},
		{"backslash", []entry{ok, {name: `disks\x`, mode: 0o644, body: "x"}}, "unsafe name"},
		{"symlink", []entry{ok, {name: "disks/l", typ: tar.TypeSymlink, link: "/etc"}}, "not a regular file"},
		{"hard link", []entry{ok, {name: "disks/l", typ: tar.TypeLink, link: "disks/plugin.yaml"}}, "not a regular file"},
		{"device", []entry{ok, {name: "disks/d", typ: tar.TypeChar}}, "not a regular file"},
		{"fifo", []entry{ok, {name: "disks/f", typ: tar.TypeFifo}}, "not a regular file"},
		{"duplicate", []entry{ok, ok}, "twice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "disks")
			err := unpack(bytes.NewReader(pkg(t, c.entries...)), dest, "disks", nil)
			switch {
			case c.want == "" && err != nil:
				t.Fatal(err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestUnpackModesAndOwnership(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "disks")
	var touched []string
	pk := pkg(t,
		entry{name: "disks/", typ: tar.TypeDir, mode: 0o777},
		entry{name: "disks/plugin.yaml", mode: 0o4666, body: "id: disks"}, // setuid, world-writable
		entry{name: "disks/bin/run", mode: 0o2777, body: "x"},             // setgid, executable
	)
	err := unpack(bytes.NewReader(pk), dest, "disks", func(p string) error { touched = append(touched, p); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{"plugin.yaml": 0o644, "bin/run": 0o755} {
		info, err := os.Stat(filepath.Join(dest, p))
		if err != nil || info.Mode() != want {
			t.Errorf("%s: %v %v, want %v", p, info.Mode(), err, want)
		}
	}
	if info, _ := os.Stat(dest); info.Mode().Perm() != 0o755 {
		t.Errorf("directory mode %v", info.Mode())
	}
	if len(touched) < 3 {
		t.Errorf("chown reached only %v", touched)
	}
}

func TestUnpackRefusesTooMuch(t *testing.T) {
	// A compression bomb: a header that lies about the size is stopped by the
	// cap on what is read, whatever the header says.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "disks/plugin.yaml", Typeflag: tar.TypeReg, Mode: 0o644, Size: maxPackageFile + 1})
	_ = tw.Flush()
	_ = gz.Close()
	if err := unpack(&buf, filepath.Join(t.TempDir(), "disks"), "disks", nil); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("err = %v", err)
	}
	if err := unpack(strings.NewReader("not gzip"), filepath.Join(t.TempDir(), "disks"), "disks", nil); err == nil {
		t.Fatal("garbage accepted")
	}
	// More entries than allowed.
	var many []entry
	many = append(many, entry{name: "disks/plugin.yaml", mode: 0o644, body: "x"})
	for i := 0; i < maxPackageFiles+1; i++ {
		many = append(many, entry{name: "disks/f" + string(rune('a'+i%26)) + strings.Repeat("x", i/26), mode: 0o644, body: "x"})
	}
	if err := unpack(bytes.NewReader(pkg(t, many...)), filepath.Join(t.TempDir(), "disks"), "disks", nil); err == nil {
		t.Fatal("too many entries accepted")
	}
}
