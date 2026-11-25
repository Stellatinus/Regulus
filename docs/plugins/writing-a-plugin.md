# Writing a plugin

A plugin is a program that serves the plugin contract on the Unix socket Regulus gives it, plus a `plugin.yaml`. This page starts from the example in this repository.

## The example: `plugins/example-hello`

```
plugins/example-hello/
  plugin.yaml    the manifest: id, version, components, permission
  main.go        the program: serves the contract, keeps a count in its data directory
  main_test.go   checks the program against the manifest, and its two small behaviours
  ui/widgets.js  the widget (provisional, nothing loads it yet)
```

It serves the contract, counts its starts in its data directory (`REGULUS_PLUGIN_DATA_DIR`) and is healthy while that directory is writable. It does nothing the dashboard can see: **today a plugin can only run, be healthy and keep its own files**. What a plugin can offer the dashboard and what it may ask of the host is not designed (see [Plugins](README.md), "Open questions"), and the widget is only a file in the right place. The example shows every part a plugin has so far and no more.

`main.go`, the part that matters:

```go
// These must match plugin.yaml: Regulus checks them when it starts the plugin.
const (
	id      = "example-hello"
	version = "0.1.0"
	api     = 1
)

func main() {
	dir := os.Getenv(sdk.EnvDataDir)
	n, _ := countStart(dir) // the plugin's own state, in its own directory
	fmt.Printf("example-hello %s started as the %s component, start number %d\n", version, os.Getenv(sdk.EnvRole), n)

	p := sdk.Plugin{ID: id, Version: version, APIVersion: api, Health: func(context.Context) error { return writable(dir) }}
	if err := sdk.Serve(context.Background(), p, nil); err != nil {
		fmt.Fprintln(os.Stderr, "example-hello:", err)
		os.Exit(1)
	}
}
```

## Make your own plugin, step by step

This is the path the example was checked with. Replace `my-plugin` with your id (2 to 40 characters: lower-case letters, digits, single hyphens, starting with a letter).

1. **Copy the example.** `cp -r plugins/example-hello plugins/my-plugin` (not the `bin/` directory).
2. **Edit the manifest** (`plugins/my-plugin/plugin.yaml`, see [Manifest](manifest.md)): the `id`, `version`, `name`, `description`, `requires` (the Regulus releases you tested with) and `api: 1`. Keep `components.agent`, `components.hub` or both, whichever your plugin has; a component whose command is missing is simply not started. Keep or drop `components.ui`.
3. **Declare the permissions** it will need, each starting with the id and a dot (`my-plugin.read`) and with a description an administrator can read. They are a *request*: nothing enforces them yet ([Manifest](manifest.md), "Permissions").
4. **Change the program** (`main.go`): the constants `id`, `version` and `api` must equal the manifest, and the package comment must say what it does and what it does not. The contract the host speaks to you is `Describe` and `Health` ([the SDK](#the-sdk-for-go-internalpluginsdk) serves both). Keep files only in `REGULUS_PLUGIN_DATA_DIR`.
5. **Change the test** (`main_test.go`): `TestTheProgramMatchesItsManifest` reads `plugin.yaml` with the same parser Regulus uses and fails when the constants, the directory name, the permissions or the widget path are wrong. Run `go test ./plugins/my-plugin/`.
6. **The widget** (`ui/widgets.js`, named by `components.ui.module`): a JavaScript module inside the plugin directory. Regulus checks the name and that it is inside the plugin; it does **not** load or run it (#21). The file in the example shows the shape the design proposes and will change with that design.
7. **Build it into `bin/`:** `go build -o plugins/my-plugin/bin/my-plugin ./plugins/my-plugin` (the path of `components.<role>.command`). `plugins/*/bin/` is ignored by git.
8. **Run it.** Start a hub (or an agent) with the plugins directory: `regulus hub --plugins-dir "$PWD/plugins"` (an agent: `regulus agent --hub HOST:PORT --plugins-dir "$PWD/plugins"`, after enrolling, see [Remote agents](../remote-agents.md)). The log shows `plugin running plugin=my-plugin`, then what your program printed. `regulus status` and `GET /api/plugins` show its state; a plugin that is not valid is listed as `invalid` with the reason. Stop the hub and the plugin stops with it. Its data directory is `<data_dir>/plugins-data/my-plugin/` on the hub.
9. **Package it:** `scripts/package-plugin.sh plugins/my-plugin` writes `dist/plugins/my-plugin_<version>_<os>_<arch>.tar.gz` and a `.sha256`, with the program built for this machine (`GOOS` and `GOARCH` change that). It holds one directory with `plugin.yaml`, the programs and the widget module. **To install it, unpack it in the plugins directory** (`tar -xzf my-plugin_0.1.0_linux_amd64.tar.gz -C /etc/regulus/plugins`, or wherever `plugins_dir` points, owned by root). The archive name and layout are provisional until the plugin index and installation (regulus-plugins#1); nothing signs the archive yet.

What was checked: the example builds, is found, validated and run by the supervisor (`TestTheExamplePluginRuns`); it was started by a real hub with `--plugins-dir` and counted its start in the hub's plugins data directory; the packaging script and its archive are tested (`scripts/test-package-plugin.sh`, part of `make test`). **Not checked:** an installed hub or agent under systemd (the unit's hardening may need relaxing for some plugins, see [Runtime](runtime.md)), and the widget, which nothing loads.

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

The SDK lives under `internal/`, so only plugins in this repository can import it. That is enough while plugins are internal (regulus-plugins#2). A public module comes with third-party plugins (regulus-plugins#3).

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
