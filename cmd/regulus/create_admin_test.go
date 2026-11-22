package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/storage/storagetest"
)

// emptyHub makes a migrated data directory with no account and returns it.
func emptyHub(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db := storagetest.Open(t, dir)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeTTY answers the prompts with the given passwords in order.
func fakeTTY(passwords ...string) func(*os.File, string, io.Writer) (string, error) {
	return func(_ *os.File, _ string, _ io.Writer) (string, error) {
		if len(passwords) == 0 {
			return "", errors.New("not a terminal")
		}
		p := passwords[0]
		passwords = passwords[1:]
		return p, nil
	}
}

func TestCreateAdminMakesAnAccountThatCanSignIn(t *testing.T) {
	dir := emptyHub(t)
	var out, errOut bytes.Buffer
	code := createAdmin([]string{"--data-dir", dir, "--user", "Boss"}, storagetest.Getenv(t, dir), nil, &out, &errOut,
		fakeTTY("a long enough password", "a long enough password"))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, errOut.String())
	}
	if strings.Contains(out.String()+errOut.String(), "a long enough password") {
		t.Error("the password was printed")
	}
	ctx := context.Background()
	db := storagetest.Open(t, dir)
	defer func() { _ = db.Close() }()
	s, err := auth.New(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if s.SetupRequired() {
		t.Error("the hub still asks for the setup")
	}
	u, _, _, err := s.Login(ctx, "127.0.0.1", "boss", "a long enough password")
	if err != nil || u.Role != auth.RoleAdmin {
		t.Fatalf("login = %+v, %v; want the admin", u, err)
	}
}

func TestCreateAdminRefusesWhenAnAccountExists(t *testing.T) {
	dir := hubWithAdmin(t)
	var out, errOut bytes.Buffer
	code := createAdmin([]string{"--data-dir", dir}, storagetest.Getenv(t, dir), nil, &out, &errOut,
		fakeTTY("another long password", "another long password"))
	if code != 1 || !strings.Contains(errOut.String(), "reset-admin") {
		t.Fatalf("exit code = %d, stderr = %q; want 1 and the advice to use reset-admin", code, errOut.String())
	}
}

func TestCreateAdminPasswordRules(t *testing.T) {
	dir := emptyHub(t)
	for name, tty := range map[string]func(*os.File, string, io.Writer) (string, error){
		"the two passwords differ": fakeTTY("a long enough password", "another long password"),
		"not a terminal":           fakeTTY(),
	} {
		var out, errOut bytes.Buffer
		code := createAdmin([]string{"--data-dir", dir}, storagetest.Getenv(t, dir), nil, &out, &errOut, tty)
		if code != 1 || !strings.Contains(errOut.String(), name) {
			t.Errorf("%s: exit code = %d, stderr = %q", name, code, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	code := createAdmin([]string{"--data-dir", dir}, storagetest.Getenv(t, dir), nil, &out, &errOut, fakeTTY("short", "short"))
	if code != 1 || errOut.Len() == 0 {
		t.Errorf("a short password: exit code = %d, stderr = %q", code, errOut.String())
	}
}

func TestCreateAdminPasswordStdin(t *testing.T) {
	dir := emptyHub(t)
	in, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	if _, err := in.WriteString("a long enough password\nignored\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := createAdmin([]string{"--data-dir", dir, "--password-stdin"}, storagetest.Getenv(t, dir), in, &out, &errOut, fakeTTY())
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, errOut.String())
	}
}
