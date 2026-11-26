# Plugin manifest (`plugin.yaml`)

Every plugin has a `plugin.yaml` at the top of its directory. Regulus reads it strictly (`internal/plugin/manifest.go`): an unknown key is an error, so a misspelt key is never silently ignored.

```yaml
id: docker
version: 0.3.1
name: Docker
description: Containers of each server.
api: 1
requires: ">=0.3.0 <0.5.0"
components:
  agent:
    command: bin/docker-agent
    args: [--quiet]
  hub:
    command: bin/docker-hub
  ui:
    module: ui/widgets.js
permissions:
  - id: docker.containers.read
    description: See the containers.
  - id: docker.containers.restart
    description: Restart a container.
```

## Keys

| Key | Required | Meaning and rules |
|---|---|---|
| `id` | yes | Names the plugin everywhere: its directory, its permissions, its release tags. 2 to 40 characters: lower-case letters, digits and single hyphens, starting with a letter (`docker`, `smart-disks`). It never changes. |
| `version` | yes | The plugin's own version, SemVer, without a leading `v` (`0.3.1`, `1.0.0-beta.2`). |
| `name` | yes | Shown to the administrator. |
| `description` | no | Shown to the administrator. |
| `api` | yes | The plugin API version the plugin speaks. Regulus runs it only if it speaks that version (today: `1`). |
| `requires` | no | The Regulus releases the plugin works with: constraints separated by spaces or commas, all of which must hold. Operators `>=`, `>`, `<=`, `<`, `=`. Empty means any. A development build of Regulus (not a release) runs any plugin whose `api` it speaks. |
| `components` | yes, at least one | The parts of the plugin, below. |
| `permissions` | no | What the plugin asks to be allowed, below. |
| `provides` | no | Names of integration points for other plugins. Reserved; not used yet. |

### Version ranges

Versions are compared as SemVer, with **one exception**: `<X.Y.Z` also leaves out the pre-releases of `X.Y.Z`. In plain SemVer `0.5.0-alpha.1` comes before `0.5.0`, so `<0.5.0` would let the first alpha of 0.5 run a plugin that says it does not work with 0.5. A plugin author who writes `<0.5.0` means "not the 0.5.0 release line", so that is what it means here.

| `requires` | 0.2.9 | 0.3.0 | 0.4.7 | 0.5.0-alpha.1 | 0.5.0 |
|---|---|---|---|---|---|
| `>=0.3.0 <0.5.0` | no | yes | yes | no | no |
| `>=0.3.0` | no | yes | yes | yes | yes |
| `=0.3.0` | no | yes | no | no | no |

### Components

| Component | Keys | What it is |
|---|---|---|
| `agent` | `command`, `args` | A program started on every server whose agent runs plugins. |
| `hub` | `command`, `args` | A program started on the hub. *The hub does not start plugins yet.* |
| `ui` | `module`, `node_tab` | A JavaScript module for the dashboard (#21). It is served to the dashboard and run in a sandboxed frame ([Runtime](runtime.md#the-page-of-a-plugin-in-the-dashboard-21-261)). Must end in `.js`, be **one self-contained file** (no `import` of other files: the frame has no session to fetch them with) and not contain `</script` or `<!--`. At most 2 MiB. |

`node_tab` (optional) adds a tab to the node window that shows the module for the node:

```yaml
components:
  ui:
    module: ui/disks.js
    node_tab:
      id: disks          # lower-case letters, digits, single hyphens, 2 to 40
      title:
        en: Disks        # required, at most 24 characters
        it: Dischi       # optional; English is shown when it is missing
```

A plugin has at most one tab. The tab is placed after **General**. Other keys under `node_tab` or `title` are refused. Adding it is compatible (an optional key), so `api` stays 1.

`command` and `module` are paths **relative to the plugin directory and inside it**: no leading `/`, no `..`, no `\`, already clean (`bin/x`, not `./bin/x` or `bin//x`). When the plugin starts, symbolic links are followed and the result must still be inside the plugin directory and be an executable regular file. `args` are passed as they are; no shell is involved.

### Permissions

Each permission has an `id` that starts with the plugin id and a dot (`docker.containers.read`), and a `description`, which is what an administrator reads before allowing it. An id may appear only once.

Permissions in the manifest are a **request**. They will become permissions that an administrator can give to roles, like the core's (#61), when plugins are installed from the dashboard (regulus-plugins#1). **Nothing enforces them yet:** a plugin can do whatever the user it runs as can do. See [Runtime](runtime.md), "Security".

## Checking a manifest

`plugin.ParseManifest` returns the first problem it finds, with the key in the message. The tests in `internal/plugin/manifest_test.go` list what is refused. When the agent starts, a plugin with an invalid manifest is logged and skipped. The others still run.

## Changing the format

The manifest is part of the plugin API (`api`). Adding an optional key is compatible. A key whose meaning changes, or a new required key, needs a new `api` version. Regulus keeps accepting the old one until `MinAPIVersion` is raised. See [Versioning](../versioning.md).
