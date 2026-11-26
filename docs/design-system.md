# Design system v3 (the console)

The visual and structural language of the dashboard, designed by the maintainer. **The reference is `docs/design-system/regulus-console-v3.1.html`** (the first version, `regulus-console-v3.html`, is kept: it has the screens the second does not repeat): one self-contained HTML file, open it in a browser, no build step. It is the source of truth: it is not an inspiration, and the dashboard is rebuilt to look and behave like it. Where the dashboard differs, this page says why. If the file and the dashboard disagree and this page does not say why, the dashboard is wrong.

The file has example data (eight made-up nodes, a fake ping, fake updates). It is not wired to the hub. It replaces the "machine room" direction of `design-drafts/` for the dashboard shell (see [decisions.md](decisions.md), "Web UI: design system v3").

## What it is made of

| Piece | What it is | Code |
|---|---|---|
| **Tokens** | Graphite surfaces (`--bg #0e1014`, `--surface #161920`, `--surface-2 #1d212b`), text `#eef0f5`, white as the primary action, violet (`#8b5cf6`) for the mesh, green/amber/red for state. Inter and JetBrains Mono, bundled (the CSP allows no outside fonts). | `dashboard/src/styles.css` (top) |
| **Notch** | The island at the top, joined to the page edge by two concave fillets: the brand, the *n/m Online* pill and the account menu (Settings, sign out). The pill is also the connection indicator: while the live stream to the hub flows it is green and says *6/8 Online* (the hub is online, the nodes in part); while it does not it turns orange and says *Reconnecting…* (red after 60 seconds). There is no separate live-state text and no cog: Settings is in the account menu. | `Shell.tsx`, `shell.css` |
| **Dock** | The floating bar at the bottom with the sections: Dashboard, Nodes, Services, Automations (only when switched on) and, after a divider, Admin. A section that needs a permission shows only to those who hold it. Below 680 px only the icons show. | `Shell.tsx` (`Dock`) |
| **Nodes page** | One control bar (search with `/`, how many nodes are shown, filter popover, three views) and the nodes as cards, a compact list or the map of the mesh. | `NodesView.tsx`, `NodesTopology.tsx`, `topology.ts` |
| **Node window** | A node opens in a window of 90 vw x 90 vh over the page (the address `#/servers/<id>` still works and is what the cards link to). Header: logo, name, type, state, host name, last contact, a three-way switch **General / SSH terminal / File editor** (one is always chosen; General is the default), an edit button (pencil) and close. | `ServerPage.tsx` |
| **Logo library** | Seven node-type logos, drawn for Regulus: primary hub, secondary hub, compute, generic, proxy/gateway, storage/NAS, dev and AI. Each is an SVG in `currentColor`, tinted by a `role-*` class. The reference's "role library" window is not part of the dashboard: the logos are used where a node is drawn, and picked in the edit dialog. | `nodeTypes.tsx` |
| **Settings** | Behind *Settings* in the account menu (`#/settings/<category>`): a list of categories and the chosen one. Personal: *Account and security* (password, two-factor, sessions), *Appearance and language*. System: *Plugins, apps, automations* (three switches), *Network and core*, *Database*, *Metrics and telemetry* (what the hub's configuration file holds; not editable yet, #483) and *Mesh updates* (the Updates page, with `core.update`). | `SettingsPage.tsx`, `features.ts` |
| **Services** | *Installed* (the plugins of the hub and the servers; apps later) and *Regulus Store* (`StorePage.tsx`, built against `GET /api/store`: sources and the state of their index, search, filters, cards, a detail with the permissions shown before installing, versions that cannot run greyed out with the reason; **Install, Update and Remove** against the hub's lifecycle routes, with step-up, a busy state and translated refusals. **Not built:** rollback, pin, requirements between plugins, the automatic mode and "needs approval", adding a source from the page). Hidden when Plugins and Apps are both off. | `ServicesPage.tsx` |
| **Automations** | A list of automations and an editor (trigger, nodes to watch, condition, actions). For now drafts kept in this browser that do not run (#476). | `AutomationsPage.tsx` |
| **Admin** | Tabs: *Accounts*, *Roles and permissions*, *Recent activity* (each with its permission). Updates is no longer a tab: it is in Settings. | `Shell.tsx`, `UsersPage.tsx` |

## Decisions made while building it

These follow what the maintainer said when handing over the design; the rest of the reference is copied as it is.

- **No sidebar.** The old sidebar is split in two: the notch (brand, online count, account, settings) and the dock (the views).
- **Settings is in the account menu** (second version of the design). The first version had a cog in the notch; it is gone, and so is the live-state text.
- **Dashboard stays, in the dock.** The new dashboard is not designed yet. The existing widget dashboard is the first item of the dock until then.
- **Services = plugins and apps; Plugins, Apps and Automations are switches in Settings.** The switches are kept in this browser until the hub can keep them (#474); Plugins start on, Apps and Automations off. The store of the reference is built for **plugins only**, from what the hub's API returns: its example catalogue (Docker apps, automations, Telegram bot) is not copied. Apps and automations will have repositories of their own (#475) and are not in the store page. Not verified: the page was run in a browser against a mocked `/api/store`, never against a real hub (the backend and the signed index have not run together), and the install, update and removal buttons have been exercised only against mocked answers, never against a real hub or the root helper. The install permission is assumed to be `plugins.manage` (`STORE_INSTALL_PERMISSION` in `StorePage.tsx`, the one place that names it); it must match what the hub defines.
- **Automations are drafts.** The designer is built; saving keeps the draft in this browser and nothing runs (#476). The reference's triggers and actions are examples (a Telegram bot, Plex, Stirling-PDF); the editor offers generic ones (notify, e-mail, chat, package updates, restart).
- **Admin** shows accounts, roles and activity, as in the reference; Mesh updates moved to Settings.
- **Languages** are English and Italian built in; the design wants every language as a download (#482).
- **The window of a node has three parts, not three buttons.** SSH terminal and File editor are placeholders that say they are designed ([node-access.md](node-access.md), #236, #237) and not built. Ping and Restart of the reference are not in the window: no command can be sent to a server from the dashboard today ([dashboard.md](dashboard.md)); they come back when there is a command to send.
- **The edit button.** It changes the name shown in Regulus (kept on the hub, `servers.manage`, not for the hub's own server; the name of the machine does not change) and the node type.
- **Names:** the nodes are called by the name an administrator gave them, else their host name, else their id. The long internal name of the reference (`prometheus.ai.regulus.internal`) is shown only as the host name inside the window; names for mentioning nodes (for example `@name` for AI support) will be the node name.
- **Dark is the design.** It is the default theme; light and system stay as choices, with a light set derived from the same tokens (not designed by the maintainer).

## What the data does not give yet

The reference shows facts Regulus does not have. The dashboard leaves them out instead of inventing them:

| In the reference | In the dashboard | What is needed |
|---|---|---|
| IP address of a node | the host name, else the id | the agent reports its addresses (issue) |
| Seven node types per node | the hub is the hub; every other server is *generic* unless the type picked in the edit dialog, which is a look kept **in this browser** | node types on the hub ([#164](https://github.com/Stellatinus/Regulus/issues/164), [node-types.md](node-types.md)) |
| Updates by APT, Ubuntu and Regulus Mesh | one pill: *Update* when the agent runs an older Regulus than the hub (the Updates page does the update) | package and OS updates per node (issue) |
| Services and containers of a node, a log console | not shown | node services and logs (issue) |
| Ping, restart | not shown | node commands (issue) |
| Where the map puts a node (proxy left, secondary hub below) | by node type; with no types on the hub, every server hangs from the hub on the right | node types |

## Screens still to come

The new Dashboard, the corrected Settings screens the maintainer is still testing, and whatever he draws next. Parts of the second version are drafts of future features; they are built only where the hub has the data. When one arrives, add it to the table above and to the reference file or a new file next to it.
