# Documentation index

Find what you need by what you want to do. For what Regulus is, see the [README](../README.md).

| I want to… | Go to |
|---|---|
| install Regulus on a server | [Installation](installation.md) |
| configure the hub | [Configuration](configuration.md) |
| choose and prepare the database (SQLite or PostgreSQL) | [Database](database.md) |
| use the console or the command line | [Console and command line](console-and-cli.md) |
| manage users, roles and two-factor authentication, or recover lost credentials | [Authentication](authentication.md) |
| add other servers (remote agents) | [Remote agents](remote-agents.md), and **Servers → Add a server** in the dashboard |
| update the hub and the servers | [Updates](updates.md) |
| know how releases and channels work | [Releases](releases.md) |
| know which versions of hub, agents and helper work together, and what may change in the protocol | [Versioning](versioning.md) |
| scrape the hub and its servers with Prometheus | [Metrics for Prometheus](metrics.md) |
| use the API | [HTTP API](http-api.md) and `internal/hub/openapi.yaml` (also served at `/api/openapi.yaml`) |
| learn what the dashboard offers | [Dashboard](dashboard.md) |
| see the look and the structure the dashboard is built to (notch, dock, nodes, tokens) | [Design system v3](design-system.md), with the reference in `design-system/regulus-console-v3.html` |
| understand how the parts fit together | [Architecture](architecture.md), with diagrams and the code map |
| find my way in the biggest source files | [Code map](code-map.md) |
| see how a disk keeps one identity between servers (disk identity, `inventory.read`) | [Disks](disks.md) |
| look up a term | [Glossary](glossary.md) |
| read the guide to hub promotion, redundancy and failover (skeleton, not implemented) | [Hub redundancy](hub-redundancy.md) |
| see the kinds of machine in a group (hub, redundant hub, backup node, access gateway…) | [Node types](node-types.md) (design draft) |
| read the design of the Android access app and the access gateway (skeleton, not implemented) | [Regulus Access](regulus-access.md) |
| understand why a choice was made | [Design decisions](decisions.md) |
| understand plugins: the manifest, how they run, how to write one | [Plugins](plugins/README.md), [Manifest](plugins/manifest.md), [Runtime](plugins/runtime.md), [Writing a plugin](plugins/writing-a-plugin.md) |
| understand managers, backends and apps (RAID, firewall, containers, VPN, Plex and the like; design only) | [Managers, backends and apps](managers-and-apps.md) |
| read the proposed design for changing a node's type and the guided installation (not implemented) | [Node type changes](node-type-changes.md) |
| read the proposed design of the web terminal and the file browser of a node (not implemented) | [Web terminal and file browser](node-access.md) |
| see open ports and the traffic map of the group (design only) | [Network visibility](network-visibility.md) |
| know how alerts will tell an administrator that a server is offline or a disk is full (design only) | [Alerts](alerts.md) |
| know how a hub will be backed up and restored (design only) | [Hub backup and restore](backup.md) |
| read the design of apps: the manifest, the volume layout, the catalog and the lifecycle (design only) | [Apps](apps.md) |
| build and test Regulus | [Development](development.md) and [CONTRIBUTING](../CONTRIBUTING.md) |
| work on Regulus as an AI coding agent | [AGENTS.md](../AGENTS.md) |
| report a security problem | [SECURITY](../SECURITY.md) |
| see what is open | the [issues](https://github.com/Stellatinus/Regulus/issues) and the roadmap in the [README](../README.md#roadmap) |

Every new page under `docs/` is added to this table in the same pull request. Documents are in English; only the GUI is bilingual (see [CONTRIBUTING](../CONTRIBUTING.md)).
