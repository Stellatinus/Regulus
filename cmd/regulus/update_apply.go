package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/CodexCircle/Regulus/internal/update"
)

// runUpdateApply is the root-owned half of the updates: systemd starts it
// (regulus-update.service) when the hub leaves a request. It checks the
// downloaded release again, installs it, restarts the service and puts the
// previous version back if the new one does not come up healthy.
func runUpdateApply(args []string) int {
	fs := flag.NewFlagSet("update-apply", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "/var/lib/regulus", "the hub's data directory, where the request and the downloaded release are")
	stateDir := fs.String("state-dir", "/var/lib/regulus-update", "directory owned by root for the previous binary and the signature trust cache")
	unit := fs.String("unit", "regulus.service", "the service to restart")
	binary := fs.String("binary", "", "the executable to replace (default: this one)")
	isAgent := fs.Bool("agent", false, "this server runs the agent: healthy means the agent connected to the hub again")
	recoverOnly := fs.Bool("recover", false, "only settle an update that an earlier run left unfinished (the marker in the state directory), then exit")
	beforeStart := fs.Bool("before-start", false, "with --recover, when the machine starts and before the service does: only what needs no running service (see regulus-update-recover.service)")
	policyFile := fs.String("policy", update.PolicyFile, "file with the local limits on what is installed (max_channel, max_major); read only here, as root")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *beforeStart && !*recoverOnly {
		fmt.Fprintln(os.Stderr, "regulus: --before-start is only for --recover")
		return 2
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "regulus: update-apply installs files owned by root; systemd runs it as root")
		return 1
	}
	if *binary == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "regulus: %v\n", err)
			return 1
		}
		if *binary, err = filepath.EvalSymlinks(exe); err != nil {
			fmt.Fprintf(os.Stderr, "regulus: %v\n", err)
			return 1
		}
	}
	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "regulus: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	policy, policyErr := update.LoadPolicy(*policyFile, true)
	in := &update.Installer{
		Policy:    policy,
		PolicyErr: policyErr,
		DataDir:   *dataDir,
		StateDir:  *stateDir,
		Binary:    *binary,
		Unit:      *unit,
		Agent:     *isAgent,
		Verifier:  &update.SigstoreVerifier{Trust: update.PublicTrust(filepath.Join(*stateDir, "tuf"))},
	}
	// One helper at a time: an update that was asked for and the confirm step
	// after a start of the machine must not work on the executable together.
	unlock, err := update.LockState(ctx, *stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "regulus: %v\n", err)
		return 1
	}
	defer unlock()
	if *beforeStart {
		rres, settled, rerr := in.RecoverBeforeStart(ctx)
		switch {
		case rerr != nil:
			fmt.Fprintf(os.Stderr, "regulus: interrupted update from %s to %s: %s: %v\n", rres.From, rres.Version, rres.Outcome, rerr)
			if rres.Outcome == update.OutcomeFailed {
				return 1
			}
		case !settled && rres.Version != "":
			fmt.Printf("regulus: interrupted update from %s to %s: the new version is in place and is checked once the service has started\n", rres.From, rres.Version)
		}
		return 0
	}
	// An earlier run that was killed between replacing the executable and
	// confirming the result left a marker: settle it first, whatever else is
	// asked now (systemd also runs this after the helper stops, see the unit).
	if rres, did, rerr := in.Recover(ctx); did {
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "regulus: interrupted update from %s to %s: %s: %v\n", rres.From, rres.Version, rres.Outcome, rerr)
		} else {
			fmt.Printf("regulus: interrupted update from %s to %s: %s\n", rres.From, rres.Version, rres.Outcome)
		}
		if *recoverOnly {
			if rerr != nil {
				return 1
			}
			return 0
		}
	}
	if *recoverOnly {
		return 0
	}
	res, err := in.Apply(ctx)
	switch {
	case errors.Is(err, update.ErrNoRequest):
		fmt.Fprintln(os.Stderr, "regulus: there is no update request")
		return 0
	case err != nil:
		fmt.Fprintf(os.Stderr, "regulus: update to %s: %s: %v\n", res.Version, res.Outcome, err)
		return 1
	}
	fmt.Printf("regulus: updated from %s to %s\n", res.From, res.Version)
	return 0
}
