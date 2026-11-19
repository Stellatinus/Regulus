# Writing a plugin

A plugin is a program that serves the plugin contract on the Unix socket Regulus gives it, plus a `plugin.yaml`. This page starts from the example in this repository.

## The example: `plugins/example-hello`

```
plugins/example-hello/
  plugin.yaml   the manifest
  main.go       the program
```

It serves the contract, reports itself healthy and does nothing else. It is the starting point for a new plugin.

`main.go`, in full:

```go
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
```

## Try it on your machine

Build Regulus and the example, then run an agent with the plugins directory set. The agent needs a hub to connect to; see [Development](../development.md) for running one locally.

```bash
go build -o plugins/example-hello/bin/example-hello ./plugins/example-hello
```

```bash
regulus agent --hub localhost:8443 --data-dir /tmp/agent --plugins-dir "$PWD/plugins"
```

The agent's log shows `plugin running plugin=example-hello`, then the line the example prints. Stop the agent and the plugin stops with it. `plugins/example-hello/bin/` is ignored by git.

## The SDK for Go: `internal/plugin/sdk`

```go
sdk.Serve(ctx, sdk.Plugin{
	ID:         "my-plugin",         // = id in plugin.yaml
	Version:    "0.1.0",             // = version
	APIVersion: 1,                   // = api
	Health: func(ctx context.Context) error {
		return nil // or an error: reported as NOT_SERVING with its text
	},
}, func(s *grpc.Server) {
	// register your own services on the same socket here
})
```

`Serve` listens on `REGULUS_PLUGIN_SOCKET` and returns when the process gets `SIGTERM` (what Regulus sends to stop it) or `SIGINT`. It stops gracefully. Use `sdk.EnvDataDir` (`REGULUS_PLUGIN_DATA_DIR`) for the plugin's own files.

The SDK lives under `internal/`, so only plugins in this repository can import it. That is enough while plugins are internal (#22). A public module comes with third-party plugins (#23).

## Other languages

A plugin does not have to be written in Go. It must:

1. listen on the Unix socket named by `REGULUS_PLUGIN_SOCKET`, within 10 seconds of starting;
2. serve `regulus.plugin.v1.PluginService` from `proto/regulus/plugin/v1/plugin.proto` (generate the code with your language's gRPC tools);
3. answer `Describe` with the `id`, `version` and `api` of its manifest, and `Health` with `STATUS_SERVING` while it works;
4. exit within 10 seconds of `SIGTERM`.

## Checklist for a new internal plugin

- [ ] `plugins/<id>/plugin.yaml`, with the directory named after the id (see [Manifest](manifest.md)).
- [ ] The constants in the program match the manifest. A test that reads the manifest and compares is cheap.
- [ ] `requires` names the Regulus releases you tested with, and `api` is the current plugin API.
- [ ] Every permission has a description an administrator can understand.
- [ ] The plugin keeps its files in `REGULUS_PLUGIN_DATA_DIR` and nowhere else.
- [ ] It handles `SIGTERM`, and the processes it starts are in its process group (the default), so they stop with it.
- [ ] Its output is useful in the agent's log, without secrets.
- [ ] The package comment says what it does and what it does not (CI's linter asks for one).
