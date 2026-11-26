package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CodexCircle/Regulus/internal/hostsync"
	"github.com/CodexCircle/Regulus/packaging"
)

// runHostSync makes the machine match what this version needs outside the
// executable (units, directories, settings). An update from the dashboard
// replaces only /usr/bin/regulus, so without this an installation made before a
// feature existed would never get what the feature needs.
func runHostSync(args []string) int {
	fs := flag.NewFlagSet("host-sync", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "regulus: host-sync writes units and settings owned by root; run it with sudo")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := syncHost(ctx); err != nil {
		return 1
	}
	return 0
}

// syncHost runs the sync and says what it did. A failure is reported and
// returned, and the callers that run it after an update ignore it: the
// update has succeeded whatever happens here.
func syncHost(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	changes, err := hostsync.Sync(ctx, hostsync.Defaults(packaging.Units))
	for _, c := range changes {
		fmt.Printf("regulus: host-sync: %s\n", c)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "regulus: host-sync: %v\n", err)
	}
	return err
}
