# Design drafts

Dashboard design explorations. Each draft is a single self-contained HTML file: open it in a browser, no build step. They are **prototypes with example data**, not part of the dashboard (`dashboard/`), and they are not wired to the hub API.

## The design reference: `machine-room.html`

`machine-room.html` is the **reference for the dashboard's design** (agreed on 2026-11-19, tag `design-reference-machine-room`). Design issues start from it; a change to the design language changes this file in the same pull request. It was the second iteration of the machine room; the first one and a starry "constellation" draft were dropped.

What it fixes:

- **Layout.** Left sidebar (group, sections Operations / Manage / Administration, active hub, user; it collapses to icons), a header (breadcrumb, search ⌘K, alerts bell, theme, language), the machine room in the middle, the inspector on the right, the load wall below.
- **The machine room.** An isometric room that shows every node where it physically is: racks, desk, cloud. Cables run on the floor to the hub, traffic moves toward the hub (agents connect out). A location filter (All / Rack / Desk / Cloud) applies to the room and the load wall.
- **Nodes.** Named only by their node name (for example `ceres`), never by a role such as `nas-` or `web-`. Every node is drawn as the same 2U machine, cloud nodes included (no virtual machines drawn inside); a node may have more drive bays (for example 4).
- **Alerts belong to a node.** They show as pins on the node in the room, as a count by severity over the room, in a drawer (bell or sidebar), and at the top of the inspector with their actions. There is no permanent alerts column.
- **Inspector.** The node's front panel (drive bays with their state, LEDs, vents, power), CPU, memory, temperature and disk, managers with their backend, apps, node facts, actions.
- **Load wall.** One row per node, one cell per minute of the last hour, a four-step colour scale; a row selects the node.
- **Visual language.** Concrete neutrals, graphite hardware, one safety yellow for the hub and for primary actions only; semantic colours (ok, warning, critical, info) are separate from it. Archivo (wide for headings), IBM Plex Mono for data and names. Light and dark themes. GUI strings in English and Italian.

## Not settled yet

- How nodes are placed in the room and how a user describes it (which rack, which unit, desk, cloud): the room needs a **physical location** per node, which Regulus does not store. It needs a design issue.
- **Temperature** is not collected by the agent today.
- Managers and apps follow epic #286, which is design only.
