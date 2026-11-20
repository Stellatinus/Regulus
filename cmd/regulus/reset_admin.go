package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/logging"
	"github.com/CodexCircle/Regulus/internal/storage"
)

// runResetAdmin implements `regulus hub reset-admin`: it gives back access to
// a hub whose admin cannot log in. Whoever can read and write the data
// directory owns the hub, so no further proof is asked.
func runResetAdmin(args []string) int {
	return resetAdmin(args, os.Getenv, os.Stdout, os.Stderr)
}

func resetAdmin(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	var user string
	cfg, err := config.LoadWith("hub reset-admin", args, getenv, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&user, "user", "", "the admin to recover (default: the only admin)")
		fs.Usage = func() {
			_, _ = fmt.Fprint(fs.Output(), `Usage: regulus hub reset-admin [--config FILE] [--data-dir DIR] [--user NAME]

Makes a one-time link, valid for one hour, to choose a new password for an
admin account, enables the account if it was disabled, and ends all its
sessions. The password is chosen in the dashboard, so it never appears in a
terminal. Run it as the user that owns the data directory (for the package
install: sudo -u regulus regulus hub reset-admin --config
/etc/regulus/config.yaml), on the machine where the
hub runs; the hub may keep running. It never changes the database schema:
after a package upgrade, restart the hub first.

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

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rec, link, err := recoverAdmin(ctx, cfg, user)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}

	if rec.TwoFactorCleared {
		_, _ = fmt.Fprintf(stderr, "Two-factor authentication of %q was removed; set it up again from the Account page.\n", rec.Username)
	}
	if rec.Reenabled {
		_, _ = fmt.Fprintf(stderr, "The account %q was disabled and is enabled again.\n", rec.Username)
	}
	_, _ = fmt.Fprintf(stderr, "Open this link to choose a new password for %q. It works once and expires at %s.\nAll sessions of the account were ended. Replace the host if you reach the dashboard under another name.\n\n",
		rec.Username, rec.Expires.Local().Format(time.RFC1123))
	_, _ = fmt.Fprintln(stdout, link)
	return 0
}

// recoverAdmin opens the database of the hub in cfg and makes the one-time
// reset link of an admin (see auth.RecoverAdmin).
func recoverAdmin(ctx context.Context, cfg config.Config, user string) (auth.Recovery, string, error) {
	// Do not create an empty database where there is none (that would be a
	// wrong data directory, not a recovery), and do not migrate one: the hub
	// that uses it may be an older version still running after a package
	// upgrade, and migrating is the job of the hub that starts next.
	db, err := storage.OpenExisting(ctx, cfg.Storage())
	if err != nil {
		return auth.Recovery{}, "", err
	}
	defer func() { _ = db.Close() }()

	log, err := logging.New(cfg.Log, io.Discard)
	if err != nil {
		return auth.Recovery{}, "", err
	}
	rec, err := auth.RecoverAdmin(ctx, db, user, log)
	if err != nil {
		return auth.Recovery{}, "", err
	}
	return rec, fmt.Sprintf("%s/#/reset/%s", cfg.DashboardURL(), rec.Token), nil
}
