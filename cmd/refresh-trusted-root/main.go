// Command refresh-trusted-root downloads the current Sigstore trusted root
// from the Sigstore TUF repository and writes it to
// internal/update/trusted_root.json, the snapshot compiled into the binary
// (the fallback of update.PublicTrust, #214). The scheduled workflow
// .github/workflows/trusted-root.yml runs it and opens a pull request when
// the file changes. It does not edit anything else, does not touch a hub, and
// is not part of the release: only cmd/regulus is shipped.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
)

func main() {
	out := flag.String("o", "internal/update/trusted_root.json", "the file to write")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintf(os.Stderr, "refresh-trusted-root: %v\n", err)
		os.Exit(1)
	}
}

func run(path string) error {
	tr, err := root.FetchTrustedRootWithOptions(tuf.DefaultOptions().WithDisableLocalCache())
	if err != nil {
		return fmt.Errorf("fetch the trusted root: %w", err)
	}
	raw, err := tr.MarshalJSON()
	if err != nil {
		return err
	}
	fresh, err := canonical(raw)
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		same, err := sameRoot(old, fresh)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if same {
			fmt.Println("unchanged")
			return nil
		}
	}
	if err := os.WriteFile(path, fresh, 0o644); err != nil { //nolint:gosec // a public file of the repository
		return err
	}
	fmt.Println("updated")
	return nil
}

// canonical re-encodes JSON with sorted keys and two-space indentation: the
// protobuf encoder does not promise one exact layout, and a diff of a
// reviewed file must show only real changes.
func canonical(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sameRoot says whether two JSON documents hold the same data, whatever their
// layout.
func sameRoot(a, b []byte) (bool, error) {
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false, err
	}
	return reflect.DeepEqual(x, y), nil
}
