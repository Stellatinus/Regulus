package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/CodexCircle/Regulus/internal/update"
)

// runVerifyRelease is `regulus verify-release --tag vX.Y.Z checksums.txt
// checksums.txt.sigstore.json`: it checks the Sigstore signature of a
// release's checksums with the same verifier the hub uses, for the exact
// identity of the release workflow at that tag. install.sh runs it before
// it installs a package (#208). It exits 0 when the signature is valid, 1
// when it is not or cannot be checked, 2 on a wrong command line. It touches
// nothing and needs no privileges.
func runVerifyRelease(args []string) int {
	return verifyReleaseCmd(args, &update.SigstoreVerifier{}, os.Stdout, os.Stderr)
}

func verifyReleaseCmd(args []string, v update.Verifier, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify-release", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tag := fs.String("tag", "", "the release tag the files belong to, for example v0.2.0 (required)")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: regulus verify-release --tag vX.Y.Z checksums.txt checksums.txt.sigstore.json\n\n"+
			"Checks that the Sigstore bundle is a valid signature of checksums.txt made by the\n"+
			"release workflow of this repository at exactly that tag.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *tag == "" || fs.NArg() != 2 {
		fs.Usage()
		return 2
	}
	checksums, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}
	bundle, err := os.ReadFile(fs.Arg(1))
	if err != nil {
		fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := v.Verify(ctx, *tag, checksums, bundle); err != nil {
		fmt.Fprintf(stderr, "regulus: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Signature of %s verified for the release workflow at %s.\n", fs.Arg(0), *tag)
	return 0
}
