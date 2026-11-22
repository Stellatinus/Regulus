package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeVerifier struct {
	tag       string
	checksums string
	bundle    string
	err       error
}

func (f *fakeVerifier) Verify(_ context.Context, tag string, checksums, bundle []byte) error {
	f.tag, f.checksums, f.bundle = tag, string(checksums), string(bundle)
	return f.err
}

func TestVerifyReleaseCommand(t *testing.T) {
	dir := t.TempDir()
	sums, bundle := filepath.Join(dir, "checksums.txt"), filepath.Join(dir, "checksums.txt.sigstore.json")
	if err := os.WriteFile(sums, []byte("sums"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, []byte("bundle"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(v *fakeVerifier, args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := verifyReleaseCmd(args, v, &out, &errOut)
		return code, out.String(), errOut.String()
	}

	v := &fakeVerifier{}
	if code, out, _ := run(v, "--tag", "v0.2.0", sums, bundle); code != 0 || !strings.Contains(out, "verified") {
		t.Fatalf("a valid signature: exit %d, %q", code, out)
	}
	if v.tag != "v0.2.0" || v.checksums != "sums" || v.bundle != "bundle" {
		t.Errorf("the verifier got tag %q, checksums %q, bundle %q", v.tag, v.checksums, v.bundle)
	}

	if code, _, errOut := run(&fakeVerifier{err: errors.New("wrong tag")}, "--tag", "v0.2.0", sums, bundle); code != 1 || !strings.Contains(errOut, "wrong tag") {
		t.Errorf("an invalid signature: exit %d, %q; want 1 and the reason", code, errOut)
	}
	if code, _, _ := run(&fakeVerifier{}, "--tag", "v0.2.0", sums, filepath.Join(dir, "missing")); code != 1 {
		t.Errorf("a missing bundle: exit %d, want 1", code)
	}
	if code, _, _ := run(&fakeVerifier{}, sums, bundle); code != 2 {
		t.Errorf("no tag: exit %d, want 2", code)
	}
	if code, _, _ := run(&fakeVerifier{}, "--tag", "v0.2.0", sums); code != 2 {
		t.Errorf("one file: exit %d, want 2", code)
	}
}
