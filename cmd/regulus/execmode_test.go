package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFixExecModeRepairsAnExecutableOnlyRootCanRun(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"regulus", ".regulus.update", ".regulus.rollback"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if !fixExecMode(p, dir) {
			t.Errorf("%s: not repaired", name)
		}
		fi, _ := os.Stat(p)
		if got := fi.Mode().Perm(); got != 0o755 {
			t.Errorf("%s: mode %o, want 755", name, got)
		}
		if fixExecMode(p, dir) {
			t.Errorf("%s: changed again although it was already 755", name)
		}
	}
}

func TestFixExecModeLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "something-else")
	if err := os.WriteFile(other, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(other, 0o700)
	if fixExecMode(other, dir) {
		t.Error("a file with another name was changed")
	}
	// The right name in another directory is not touched either.
	elsewhere := filepath.Join(t.TempDir(), "regulus")
	if err := os.WriteFile(elsewhere, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(elsewhere, 0o700)
	if fixExecMode(elsewhere, dir) {
		t.Error("a file outside the directory was changed")
	}
	link := filepath.Join(dir, "regulus")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if fixExecMode(link, dir) {
		t.Error("a symbolic link was followed")
	}
}
