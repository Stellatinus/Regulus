package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/storage"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

func noEnv(string) string { return "" }

// hubWithAdmin makes a data directory with one admin and returns it.
func hubWithAdmin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db := storagetest.Open(t, dir)
	s, err := auth.New(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Setup(ctx, s.SetupToken(), "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResetAdminPrintsALinkThatWorks(t *testing.T) {
	dir := hubWithAdmin(t)
	var out, errOut bytes.Buffer
	if code := resetAdmin([]string{"--data-dir", dir, "--listen", "0.0.0.0:9000"}, storagetest.Getenv(t, dir), &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, errOut.String())
	}
	link := strings.TrimSpace(out.String())
	const prefix = "http://localhost:9000/#/reset/"
	if !strings.HasPrefix(link, prefix) || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("stdout = %q, want one line starting with %s", out.String(), prefix)
	}

	// The token in the link is accepted by the hub's service.
	ctx := context.Background()
	db := storagetest.Open(t, dir)
	defer func() { _ = db.Close() }()
	s, err := auth.New(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPassword(ctx, "r", strings.TrimPrefix(link, prefix), "a brand new password"); err != nil {
		t.Errorf("the printed token does not work: %v", err)
	}
}

func TestResetAdminRefusesToInventADatabase(t *testing.T) {
	if storagetest.Postgres() {
		t.Skip("about the SQLite file; PostgreSQL is covered by the checks in storage")
	}
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	if code := resetAdmin([]string{"--data-dir", dir}, noEnv, &out, &errOut); code == 0 {
		t.Fatal("a missing database must fail")
	}
	if !strings.Contains(errOut.String(), "no hub database") || out.Len() != 0 {
		t.Errorf("stderr = %q, stdout = %q", errOut.String(), out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, storage.FileName)); err == nil {
		t.Error("the command created a database")
	}
}

func TestResetAdminRejectsAWrongAccount(t *testing.T) {
	dir := hubWithAdmin(t)
	var out, errOut bytes.Buffer
	if code := resetAdmin([]string{"--data-dir", dir, "--user", "ghost"}, storagetest.Getenv(t, dir), &out, &errOut); code == 0 {
		t.Fatal("an unknown account must fail")
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), "not an admin account") {
		t.Errorf("stderr = %q, stdout = %q", errOut.String(), out.String())
	}
}

// reset-admin runs beside a hub that may be older than the binary (a package
// upgrade before the service restart): it must not migrate (#217).
func TestResetAdminDoesNotMigrate(t *testing.T) {
	dir := hubWithAdmin(t)
	db := storagetest.Open(t, dir)
	var last int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, last); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if code := resetAdmin([]string{"--data-dir", dir}, storagetest.Getenv(t, dir), &out, &errOut); code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "restart the hub") || out.Len() != 0 {
		t.Errorf("stderr = %q, stdout = %q; want the advice to restart the hub and no link", errOut.String(), out.String())
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, last).Scan(&n); err != nil || n != 0 {
		t.Fatalf("migration %d was applied by reset-admin (count %d, err %v)", last, n, err)
	}
}
