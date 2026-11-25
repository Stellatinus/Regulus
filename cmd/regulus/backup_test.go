package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CodexCircle/Regulus/internal/storage"
)

func TestBackupAndRestoreCommands(t *testing.T) {
	src := t.TempDir()
	db, err := storage.Open(context.Background(), storage.Config{DataDir: src})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (username, password_hash, role, created_at) VALUES ('admin', 'h', 'admin', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_ = os.WriteFile(filepath.Join(src, "secret.key"), []byte("k"), 0o600)

	pp := filepath.Join(t.TempDir(), "pp")
	_ = os.WriteFile(pp, []byte("a long passphrase\n"), 0o600)
	out := filepath.Join(t.TempDir(), "hub.rgb")
	noEnv := func(string) string { return "" }
	var so, se bytes.Buffer
	if code := backupCmd([]string{out, "--data-dir", src, "--passphrase-file", pp}, noEnv, nil, &so, &se, nil, "1.2.3"); code != 0 {
		t.Fatalf("backup: %d %s", code, se.String())
	}
	if info, err := os.Stat(out); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", info, err)
	}
	// An existing file is never replaced.
	se.Reset()
	if code := backupCmd([]string{out, "--data-dir", src, "--passphrase-file", pp}, noEnv, nil, &so, &se, nil, "1.2.3"); code == 0 || !strings.Contains(se.String(), "exists") {
		t.Errorf("second backup: %d %s", code, se.String())
	}

	dst := t.TempDir()
	so.Reset()
	se.Reset()
	if code := restoreCmd([]string{out, "--data-dir", dst, "--passphrase-file", pp}, noEnv, nil, &so, &se, nil); code != 0 {
		t.Fatalf("restore: %d %s", code, se.String())
	}
	if !strings.Contains(so.String(), "Restored the backup") {
		t.Errorf("output = %s", so.String())
	}
	// Into a directory that has a hub: refused, nothing changed.
	se.Reset()
	if code := restoreCmd([]string{out, "--data-dir", dst, "--passphrase-file", pp}, noEnv, nil, &so, &se, nil); code == 0 || !strings.Contains(se.String(), "already holds") {
		t.Errorf("restore over a hub: %d %s", code, se.String())
	}
	// A wrong passphrase.
	bad := filepath.Join(t.TempDir(), "bad")
	_ = os.WriteFile(bad, []byte("nope\n"), 0o600)
	se.Reset()
	if code := restoreCmd([]string{out, "--data-dir", t.TempDir(), "--passphrase-file", bad}, noEnv, nil, &so, &se, nil); code == 0 || !strings.Contains(se.String(), "passphrase") {
		t.Errorf("wrong passphrase: %d %s", code, se.String())
	}
}
