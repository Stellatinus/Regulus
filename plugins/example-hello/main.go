// Command example-hello is the example plugin (plugins/example-hello): it
// serves the plugin contract on the socket Regulus gives it, reports itself
// healthy and does nothing else. It is the starting point for a new plugin;
// see docs/plugins/writing-a-plugin.md. It is not a part of Regulus that runs
// by itself.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/CodexCircle/Regulus/internal/plugin/sdk"
)

// These must match plugin.yaml: Regulus checks them when it starts the plugin.
const (
	id      = "example-hello"
	version = "0.1.0"
	api     = 1
)

func main() {
	fmt.Printf("example-hello %s started as the %s component\n", version, os.Getenv(sdk.EnvRole))
	err := sdk.Serve(context.Background(), sdk.Plugin{ID: id, Version: version, APIVersion: api}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "example-hello:", err)
		os.Exit(1)
	}
}
