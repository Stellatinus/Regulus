package main

import (
	"bytes"
	"testing"
)

// The three spellings print the same line (#441).
func TestVersionSpellings(t *testing.T) {
	for _, a := range []string{"version", "--version", "-v"} {
		if !isVersionArg(a) {
			t.Errorf("%q should ask for the version", a)
		}
	}
	for _, a := range []string{"-h", "--no-tui", "status", "-version", "-V"} {
		if isVersionArg(a) {
			t.Errorf("%q should not ask for the version", a)
		}
	}
	old := version
	version = "1.2.3"
	defer func() { version = old }()
	var b bytes.Buffer
	printVersion(&b)
	if b.String() != "1.2.3\n" {
		t.Errorf("printVersion = %q", b.String())
	}
}
