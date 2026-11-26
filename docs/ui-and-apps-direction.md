# Direction of 2026-11-26: the dashboard, the first plugin, apps, widgets and Docker

**Status: the maintainer's direction, written down so it is not lost. Nothing here is designed in detail or built.** Each point has an issue that holds the design work; no code is written before the design is agreed (`AGENTS.md`). Where this page contradicts another page of the documents, it says so and the issue to decide it is named: the other page is **not** changed until then (except for a short note on it).

It collects what the maintainer asked for on 2026-11-26 after using the design system v3 ([Design system](design-system.md)) and while the plugin store was being designed ([The plugin store](plugins/store.md)).

## What was asked, and where it is tracked

| What | Issue |
|---|---|
| The **top bar (notch)** must look like **one piece**: the two curves and the centre have no border where they meet | #491 |
| The **node page** redesigned: better CPU, memory, network and disk-use views; **Identity and System** rewritten so each fact has one place; **"File editor" renamed "File manager"** (it does not edit files) | #492 |
| **Better Graph**: the **Dashboard as one icon in the dock** (no visible text), **customization in a popup** (drag and drop, then apply, with no edit mode left on the page), better charts | #493 |
| A node whose type was changed to **compute node** still shows as a server with role `agent` | #494 |
| **Install an app on a node chosen from the hub** | #495 |
| **Plugins are installed on the hubs only**; the redundant hub has the same plugins; both answer at one address | #496 |
| **Disks becomes the first real plugin** (a **Disks** / "Dischi" tab in the node window, with the moves of disks); Regulus side and plugin side | #497, regulus-plugins#10 |
| **Widgets** from apps, plugins and the core, described in YAML, drawn from templates (future) | #498 |
| **Docker management**: an epic with ten design issues (plugin or Portainer app, lifecycle, image updates, statistics, a shell in a container, logs, recreate, volumes, networks across machines, images) | #499, #503 to #512 |
| The gaps found in the plugin repository's CI: `regulus plugin check`, an index fixture, the pinned signing identity, the test plugins, a first real run, an index heartbeat, parallel builds | #500, #501, #502, regulus-plugins#11 to #14 |

Already open before this page and related: #470 (what the node window needs from the hub), #474 (turn plugins, apps and automations on and off), #475 (the repositories of Regulus Apps and Regulus Automations), #480 (services, containers and logs of a node), #486 (the Settings screens), #432 (the front-end redesign), #467 (Admin and Standard views).

## The node window and the dashboard

- The node window's parts become **General | the plugins' parts (Disks first) | SSH terminal | File manager**, in that order. In Italian the Disks tab is "Dischi". The current names are in [Design system](design-system.md) and [Dashboard](dashboard.md) and change when #492 is built.
- The **Dashboard** is a single icon in the bottom dock with no visible label: everyone sees it first and does not need the word. Customizing opens a **popup**; what is chosen shows on the page when applied. The old on-page "Customize" mode goes.
- **Charts** are redrawn once and used by both the dashboard and the node page.
- **Node type** is today a look kept in the browser, and the hub reports only the role `hub` or `agent` ([Dashboard](dashboard.md), #164, [Node types](node-types.md)). The place where the maintainer saw `agent` after changing a node's type is the **"Server" widget of the Dashboard** (`widgets/AgentsWidget.tsx`, which renders `AgentList.tsx`): its Role column prints the hub's role and **never reads the node type** that the Nodes page and the node window do read, so one node shows two different things on two pages. #494 says what to do now and when the hub stores types.
- **The screenshot the maintainer sent** (Dashboard in Italian: the Network widget, the Disk use widget, the Server widget, the top bar and the dock) is a reference of what is poor today, not a mock-up; #493 describes what it shows and is where the new charts start from.

## Plugins: hubs only, and the first plugin is Disks

- **Disks** (the **Physical disks** panel: device, kind, size, serial, identity, and the moves of disks between servers) becomes the first real plugin. The agent's collection of disk identities stays in the core; whether the plugin only presents what the core stores or owns its storage is open (#497). It needs the SDK to be importable (#463), the host side of plugins (#261) and a way for a plugin to add a tab to the node window (#21).
- **Plugins are installed on the hubs, not on other machines**, and the **redundant hub has the same plugins** as the primary, or the interface would differ depending on which hub answers. Both hubs answer at **one address**; the maintainer first said "IP" and then preferred **URL**.

**This contradicts what is written today.** [Runtime](plugins/runtime.md), [Manifest](plugins/manifest.md) and [The plugin store](plugins/store.md) describe a plugin that can have an **agent component** started by the agent, and the store design has the hub **send plugins to agents**. Both the Disks plugin and Docker management need per-node data and actions, so hub-only plugins need that to come through **core commands and reports**, which is the host side of plugins (#261). #496 decides what becomes of the agent component and how the redundant hub gets the plugins; until it is decided, those pages describe the earlier design and carry a note.

## Apps, widgets and the repository of apps

- Apps are installed **on a node chosen from the hub** (#495). The maintainer first said "plugins" and corrected it: plugins go on the hubs.
- The repository of apps is separate and named **Regulus Apps** (`regulus-apps`; "Apps" is preferred to "Applications"), like `regulus-plugins` (#475 decides names, layout and trust). Each app there has a **`widgets/` folder** that attaches its widgets; it is not mandatory.
- **Widgets** come from three sources, the core's defaults, plugins and apps, and appear in the list when a person clicks **Customize** then **Add**. Since the maintainer does not write the apps, a widget is **a descriptor** (YAML, perhaps JSON: to decide) that names a **ready-made template** and says where its data comes from. The reference is Homepage's service widgets (<https://gethomepage.dev/widgets/>): same logic, not the same format. This is complex and **left for the future** (#498); the points to design (format, templates, data sources and their security, audience) are in the issue.

## Docker management

Most apps are containers, so the maintainer wants to see and manage them from the administrative side: stop, restart, **pause**, check for **image updates**, **statistics**, a **console** in a container, **logs**, **recreate** an application, **volumes**, **networks that can be shared between machines** (which matters beyond Docker: it is how apps talk inside the mesh) and **images**. The first decision is whether this is **a plugin of our own** or **Portainer as an app**; the maintainer's concern with Portainer is that what is not created through it is not manageable (to be checked against the real product, not verified here), and that it brings authentication, accounts, notifications and settings Regulus does not need.

What is already agreed ([Managers, backends and apps](managers-and-apps.md), "Containers"): the runtime is a Docker or Podman **backend**; **the agent never gets the Docker socket**; operations are a **closed list** in the root helper; and **"no shell into a container in this series"**. The wish for a console (#507) **changes that last rule** and the design must say what it costs. The epic is #499.

## What is not decided

The format of widget descriptors; the plugin-or-Portainer decision; what happens to the agent component of plugins; whether the Disks plugin owns its storage; the name of the Italian "File manager"; where the type of a node is stored (#164). Each is in its issue. Nothing on this page has been checked on a real machine.
