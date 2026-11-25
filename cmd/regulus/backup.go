package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/backup"
	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/hub"
	"github.com/CodexCircle/Regulus/internal/logging"
	"github.com/CodexCircle/Regulus/internal/storage"
)

// runBackup implements `regulus backup FILE` and runRestore `regulus restore
// FILE` (docs/backup.md, #407, #408).
func runBackup(args []string) int {
	return backupCmd(args, os.Getenv, os.Stdin, os.Stdout, os.Stderr, readPasswordTTY, version)
}

func runRestore(args []string) int {
	return restoreCmd(args, os.Getenv, os.Stdin, os.Stdout, os.Stderr, readPasswordTTY)
}

type passwordReader = func(in *os.File, prompt string, out io.Writer) (string, error)

// fileArg takes FILE from the arguments: the first one when it is not a flag,
// otherwise the last (flags first, then FILE, is the usual form).
func fileArg(args []string) (file string, rest []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

// passphrase reads the passphrase of a backup: from a file (--passphrase-file,
// one line) or at the terminal without echo, twice when it is a new one. It is
// never a flag or an environment variable: those end up in process lists and
// shell histories.
func passphrase(file string, confirm bool, stdin *os.File, stderr io.Writer, read passwordReader) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		line, _, _ := strings.Cut(string(b), "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			return "", fmt.Errorf("%s has no passphrase on its first line", file)
		}
		return line, nil
	}
	first, err := read(stdin, "Passphrase: ", stderr)
	if err != nil {
		return "", fmt.Errorf("cannot read the passphrase: %w (run it in a terminal, or use --passphrase-file)", err)
	}
	if first == "" {
		return "", errors.New("the passphrase is empty")
	}
	if confirm {
		second, err := read(stdin, "Repeat the passphrase: ", stderr)
		if err != nil {
			return "", err
		}
		if first != second {
			return "", errors.New("the two passphrases differ")
		}
	}
	return first, nil
}

func backupCmd(args []string, getenv func(string) string, stdin *os.File, stdout, stderr io.Writer, read passwordReader, ver string) int {
	file, rest := fileArg(args)
	var ppFile string
	var withMetrics bool
	cfg, err := config.LoadWith("backup", rest, getenv, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&ppFile, "passphrase-file", "", "read the passphrase from the first line of this file instead of asking")
		fs.BoolVar(&withMetrics, "with-metrics", false, "include the metrics history, the largest table (left out by default)")
		fs.Usage = func() {
			_, _ = fmt.Fprint(fs.Output(), `Usage: regulus backup FILE [--config FILE] [--data-dir DIR] [--passphrase-file FILE] [--with-metrics]

Writes an encrypted backup of the hub: the database (SQLite or PostgreSQL, as
a portable export), secret.key and the CA in pki/. The file is encrypted with
a passphrase you type twice (it is never stored: without it the backup cannot
be opened). Run it as the user that owns the data directory (for the package
install: sudo -u regulus regulus backup FILE --config /etc/regulus/config.yaml);
the hub may keep running. The configuration files, which root owns, are not in
it: copy /etc/regulus/ by hand. FILE must not exist.

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
	if file == "" {
		_, _ = fmt.Fprintln(stderr, "regulus: backup needs the name of the file to write: regulus backup FILE")
		return 2
	}
	pp, err := passphrase(ppFile, true, stdin, stderr, read)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	// The database must exist and be the schema of this binary, as for the
	// other commands that run beside the hub.
	db, err := storage.OpenExisting(ctx, cfg.Storage())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}
	_ = db.Close()

	tmp, err := os.CreateTemp(filepath.Dir(file), ".regulus-backup-")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	m, err := backup.Create(ctx, backup.CreateOptions{Storage: cfg.Storage(), HubVersion: ver, Passphrase: pp, WithMetrics: withMetrics}, tmp)
	if err == nil {
		err = errors.Join(tmp.Chmod(0o600), tmp.Sync())
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		// Link, not rename: an existing file is never replaced.
		if err = os.Link(tmp.Name(), file); errors.Is(err, os.ErrExist) {
			err = fmt.Errorf("%s exists already: choose another name", file)
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Backup written to %s (%d tables, schema %d, %s).\nKeep the passphrase: there is no way to open the file without it. Copy the file off this machine.\n",
		file, len(m.Tables), m.SchemaVersion, m.DatabaseType)
	return 0
}

func restoreCmd(args []string, getenv func(string) string, stdin *os.File, stdout, stderr io.Writer, read passwordReader) int {
	file, rest := fileArg(args)
	var ppFile string
	var force bool
	cfg, err := config.LoadWith("restore", rest, getenv, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&ppFile, "passphrase-file", "", "read the passphrase from the first line of this file instead of asking")
		fs.BoolVar(&force, "force", false, "move the data already in the data directory into restore-old-<time>/ instead of refusing")
		fs.Usage = func() {
			_, _ = fmt.Fprint(fs.Output(), `Usage: regulus restore FILE [--config FILE] [--data-dir DIR] [--passphrase-file FILE] [--force]

Restores a backup made by 'regulus backup' into an empty data directory (and,
with PostgreSQL, an empty database). Stop the hub first: restore takes the
same lock and fails while a hub runs. Run it as the user that owns the data
directory. The whole file is decrypted and checked before anything is
written. A backup of an older version is migrated forward; one of a newer
version is refused. Sessions are not restored: everybody signs in again.
Servers enrolled after the backup must enroll again, and servers revoked
after it can connect again: revoke them again.

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
	if file == "" {
		_, _ = fmt.Fprintln(stderr, "regulus: restore needs the backup file: regulus restore FILE")
		return 2
	}
	in, err := os.Open(file)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}
	defer func() { _ = in.Close() }()
	pp, err := passphrase(ppFile, false, stdin, stderr, read)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}

	release, err := hub.LockDataDir(cfg.DataDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := backup.Restore(ctx, in, backup.RestoreOptions{Storage: cfg.Storage(), Passphrase: pp, Force: force})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", explainRestore(err))
		return 1
	}
	if db, err := storage.OpenExisting(ctx, cfg.Storage()); err == nil {
		if log, lerr := logging.New(cfg.Log, io.Discard); lerr == nil {
			auth.RecordRestore(ctx, db, log, res.Manifest.CreatedAt, res.Manifest.HubVersion)
		}
		_ = db.Close()
	}
	m := res.Manifest
	_, _ = fmt.Fprintf(stdout, "Restored the backup made %s by Regulus %s (%d tables, schema %d).\n",
		m.CreatedAt.Local().Format(time.RFC1123), m.HubVersion, len(m.Tables), m.SchemaVersion)
	if res.MovedTo != "" {
		_, _ = fmt.Fprintf(stdout, "The files that were here are in %s.\n", res.MovedTo)
	}
	_, _ = fmt.Fprintf(stdout, "Agents enrolled after %s must enroll again; agents revoked after it can connect again, so revoke them again.\n"+
		"Copy /etc/regulus/ (config.yaml, regulus.env) by hand if this is a new machine, then start the hub.\n", m.CreatedAt.Local().Format("2006-01-02 15:04"))
	return 0
}

func explainRestore(err error) error {
	switch {
	case errors.Is(err, backup.ErrWrongPassphrase):
		return fmt.Errorf("%w: nothing was changed", err)
	case errors.Is(err, storage.ErrNotEmpty):
		return fmt.Errorf("%w: restore into an empty database", err)
	}
	return err
}
