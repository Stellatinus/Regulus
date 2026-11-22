// Command example-hello is the example plugin (plugins/example-hello): it
// serves the plugin contract on the socket Regulus gives it, keeps a small
// state of its own in the data directory Regulus gives it (how many times it
// has been started) and reports itself healthy while that directory is
// writable. It is the starting point for a new plugin; see
// docs/plugins/writing-a-plugin.md. It does not send anything to the
// dashboard: the host side of plugin features is not designed yet (#19), and
// the widget in ui/ is not loaded by anything yet (#21). It is not a part of
// Regulus that runs by itself.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/CodexCircle/Regulus/internal/plugin/sdk"
)

// These must match plugin.yaml: Regulus checks them when it starts the plugin,
// and main_test.go checks them against the file.
const (
	id      = "example-hello"
	version = "0.1.0"
	api     = 1
)

// startsFile is the plugin's only file, in its data directory.
const startsFile = "starts"

func main() {
	dir := os.Getenv(sdk.EnvDataDir)
	n, err := countStart(dir)
	if err != nil {
		// Not fatal: a plugin that cannot write its data directory says so in
		// its health, which Regulus shows, and keeps serving.
		fmt.Fprintln(os.Stderr, "example-hello: cannot record the start:", err)
	}
	fmt.Printf("example-hello %s started as the %s component, start number %d\n", version, os.Getenv(sdk.EnvRole), n)

	p := sdk.Plugin{ID: id, Version: version, APIVersion: api, Health: func(context.Context) error { return writable(dir) }}
	if err := sdk.Serve(context.Background(), p, nil); err != nil {
		fmt.Fprintln(os.Stderr, "example-hello:", err)
		os.Exit(1)
	}
}

// countStart adds one to the number of starts kept in dir and returns it. An
// empty dir (a plugin run by hand) keeps nothing and returns 1.
func countStart(dir string) (int, error) {
	if dir == "" {
		return 1, nil
	}
	path := filepath.Join(dir, startsFile)
	n := 0
	if data, err := os.ReadFile(path); err == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}
	n++
	return n, os.WriteFile(path, []byte(strconv.Itoa(n)+"\n"), 0o600)
}

// writable reports whether dir can be written: the plugin's one health check.
func writable(dir string) error {
	if dir == "" {
		return nil
	}
	f, err := os.CreateTemp(dir, ".health-*")
	if err != nil {
		return fmt.Errorf("the data directory is not writable: %w", err)
	}
	_ = f.Close()
	return os.Remove(f.Name())
}
