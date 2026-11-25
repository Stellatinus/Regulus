# Design system v3 (the console)

The visual and structural language of the dashboard, designed by the maintainer. **The reference is `docs/design-system/regulus-console-v3.html`**: one self-contained HTML file, open it in a browser, no build step. It is the source of truth: it is not an inspiration, and the dashboard is rebuilt to look and behave like it. Where the dashboard differs, this page says why. If the file and the dashboard disagree and this page does not say why, the dashboard is wrong.

The file has example data (eight made-up nodes, a fake ping, fake updates). It is not wired to the hub. It replaces the "machine room" direction of `design-drafts/` for the dashboard shell (see [decisions.md](decisions.md), "Web UI: design system v3").

## What it is made of

| Piece | What it is | Code |
|---|---|---|
| **Tokens** | Graphite surfaces (`--bg #0e1014`, `--surface #161920`, `--surface-2 #1d212b`), text `#eef0f5`, white as the primary action, violet (`#8b5cf6`) for the mesh, green/amber/red for state. Inter and JetBrains Mono, bundled (the CSP allows no outside fonts). | `dashboard/src/styles.css` (top) |
| **Notch** | The island at the top, joined to the page edge by two concave fillets: the brand, the *n/m Online* pill, the live state of the stream, the settings cog and the account menu (account page, sign out). | `Shell.tsx`, `shell.css` |
| **Dock** | The floating bar at the bottom with the sections: Dashboard, Nodes, Services, Automations (only when switched on) and, after a divider, Admin. A section that needs a permission shows only to those who hold it. Below 680 px only the icons show. | `Shell.tsx` (`Dock`) |
| **Nodes page** | One control bar (search with `/`, how many nodes are shown, filter popover, three views) and the nodes as cards, a compact list or the map of the mesh. | `NodesView.tsx`, `NodesTopology.tsx`, `topology.ts` |
| **Node window** | A node opens in a window of 90 vw x 90 vh over the page (the address `#/servers/<id>` still works and is what the cards link to). Header: logo, name, type, state, host name, last contact, a three-way switch **General / SSH terminal / File editor** (one is always chosen; General is the default), an edit button (pencil) and close. | `ServerPage.tsx` |
| **Logo library** | Seven node-type logos, drawn for Regulus: primary hub, secondary hub, compute, generic, proxy/gateway, storage/NAS, dev and AI. Each is an SVG in `currentColor`, tinted by a `role-*` class. The reference's "role library" window is not part of the dashboard: the logos are used where a node is drawn, and picked in the edit dialog. | `nodeTypes.tsx` |
| **Settings** | The cog of the notch: appearance (theme, language) and features. Automations are a feature that can be switched on or off. | `SettingsPage.tsx`, `features.ts` |

## Decisions made while building it

These follow what the maintainer said when handing over the design; the rest of the reference is copied as it is.

- **No sidebar.** The old sidebar is split in two: the notch (brand, online count, account, settings) and the dock (the views).
- **The cog is a cog.** The reference draws a sun for Settings; the dashboard draws a gear, and Settings moved to the notch (the dock has no Settings item).
- **Dashboard stays, in the dock.** The new dashboard is not designed yet. The existing widget dashboard is the first item of the dock until then.
- **Services = plugins and apps.** One section for both (today it shows the plugins page). **Automations** are a plugin that does not exist yet; the dock shows the section only while the *Automations* setting is on (off by default, kept in this browser). **Admin** groups Users and Updates as tabs; the Admin section of the reference is not designed yet, so these two pages are all it holds.
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

Admin (its sections), the new Dashboard, and the screens the maintainer is drawing next. When one arrives, add it to the table above and to the reference file or a new file next to it.
