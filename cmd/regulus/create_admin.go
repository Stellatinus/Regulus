package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/logging"
	"github.com/CodexCircle/Regulus/internal/storage"
)

// runCreateAdmin implements `regulus hub create-admin`: it creates the first
// admin of a hub from the terminal, for a server that no browser can reach.
func runCreateAdmin(args []string) int {
	return createAdmin(args, os.Getenv, os.Stdin, os.Stdout, os.Stderr, readPasswordTTY)
}

// readPasswordTTY reads a password without echo from the terminal behind fd.
// It returns an error when fd is not a terminal.
func readPasswordTTY(in *os.File, prompt string, out io.Writer) (string, error) {
	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("not a terminal")
	}
	_, _ = fmt.Fprint(out, prompt)
	b, err := term.ReadPassword(fd)
	_, _ = fmt.Fprintln(out)
	return string(b), err
}

// createAdmin is runCreateAdmin with its inputs: readPassword reads one
// password without echo and fails when stdin is not a terminal.
func createAdmin(args []string, getenv func(string) string, stdin *os.File, stdout, stderr io.Writer,
	readPassword func(in *os.File, prompt string, out io.Writer) (string, error)) int {
	var user string
	var fromStdin bool
	cfg, err := config.LoadWith("hub create-admin", args, getenv, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&user, "user", "admin", "the name of the admin account")
		fs.BoolVar(&fromStdin, "password-stdin", false, "read the password, one line, from standard input (for scripts; never put it on the command line)")
		fs.Usage = func() {
			_, _ = fmt.Fprint(fs.Output(), `Usage: regulus hub create-admin [--config FILE] [--data-dir DIR] [--user NAME] [--password-stdin]

Creates the first admin account of a hub that has no account yet, for a server
where no browser can reach the dashboard. The password is typed twice at a
prompt without echo, so it never appears in the shell history; with
--password-stdin it is read as one line from standard input instead. Run it as
the user that owns the data directory (for the package install: sudo -u
regulus regulus hub create-admin --config /etc/regulus/config.yaml), on the
machine where the hub runs. The hub must have started once, so that its
database exists. It refuses when any account exists: use 'regulus hub
reset-admin' to regain access to an existing one.

Flags:
`)
			fs.PrintDefaults()
		}
	})
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 2
	}
	if os.Geteuid() == 0 {
		_, _ = fmt.Fprintln(stderr, "warning: running as root; if the hub runs as another user, run this as that user (for example sudo -u regulus ...) so the database files keep their owner")
	}

	password, err := readNewPassword(stdin, stderr, fromStdin, readPassword)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, err := storage.OpenExisting(ctx, cfg.Storage())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()
	log, err := logging.New(cfg.Log, io.Discard)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}
	u, err := auth.CreateFirstAdmin(ctx, db, user, password, log)
	if errors.Is(err, auth.ErrSetupDone) {
		_, _ = fmt.Fprintln(stderr, "regulus: this hub already has an account; use 'regulus hub reset-admin' to regain access to it")
		return 1
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Admin account %q created. Sign in at %s\n", u.Username, cfg.DashboardURL())
	return 0
}

// readNewPassword gets the password: one line from stdin with fromStdin,
// otherwise typed twice at the terminal.
func readNewPassword(stdin *os.File, stderr io.Writer, fromStdin bool,
	readPassword func(in *os.File, prompt string, out io.Writer) (string, error)) (string, error) {
	if fromStdin {
		b, err := io.ReadAll(io.LimitReader(stdin, 4096))
		if err != nil {
			return "", err
		}
		line, _, _ := strings.Cut(string(b), "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			return "", errors.New("no password on standard input")
		}
		return line, nil
	}
	first, err := readPassword(stdin, "Password: ", stderr)
	if err != nil {
		return "", fmt.Errorf("cannot read the password: %w (run it in a terminal, or use --password-stdin)", err)
	}
	second, err := readPassword(stdin, "Repeat the password: ", stderr)
	if err != nil {
		return "", fmt.Errorf("cannot read the password: %w", err)
	}
	if first != second {
		return "", errors.New("the two passwords differ")
	}
	return first, nil
}
