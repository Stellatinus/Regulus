# Design drafts

The shell, the Nodes page and the node window of the dashboard follow the maintainer's design system v3 instead ([docs/design-system.md](../docs/design-system.md)); the drafts below are older explorations.

Temporary home for dashboard design explorations. Each draft is a single self-contained HTML file: open it in a browser, no build step. They are **prototypes with example data**, not part of the dashboard (`dashboard/`), and they are not wired to the hub API.

When a direction is agreed, it goes through a design issue (see AGENTS.md, "New areas start with a design issue") and is rebuilt in the React dashboard. Then this folder can be removed.

| File | What it is | Status |
|---|---|---|
| `machine-room-v1.html` | The "machine room": an isometric room that shows each node where it physically is (rack, desk, cloud), cabled to the hub; a queue of what needs attention; an inspector with the node's front panel; a per-minute CPU load wall. | Chosen direction (first draft). |
| `machine-room-v2.html` | Second iteration of the machine room: left sidebar navigation, alerts moved onto the nodes and into a drawer, uniform 2U nodes named only by their node name. | Work in progress. |
| `node-types-v2.html` | Node types (#164, #165) in the v2 language: the inspector's "Type and capabilities" section with change-type actions and why each is unavailable, the confirmation with the two keys (hub and node), and the guided "Add a node" wizard with copyable commands and per-step verification. | Proposal (design not agreed). |
| `node-access-v2.html` | Web terminal and file browser of one node (#236, #237) in the v2 language: the two-keys states, step-up and local login, the live session bar with idle timer, the file browser with root banner, uploads and delete confirmation, the live sessions list with the kill switch. Deep links (`#live`, `#files`, `#sessions`…) open each state. | Proposal (design not agreed). |
| `network-v2.html` | Network pages in the v2 language: the hub-wide Open ports table (filters, group by port, change log; #282) and the Traffic map (links between nodes, hub and outside by kind and state, with a table alternative and outbound destinations; #283). Design: `docs/network-visibility.md`. | Proposal (design not agreed). |

## Notes for whoever turns this into the real dashboard

- Nodes are named only by their node name (for example `ceres`), never by a role such as `nas-` or `web-`: a role is not part of the name.
- The room needs data Regulus does not have yet: the **physical location** of a node (rack, unit, desk, cloud) and the **temperature**. Both need a design issue before code.
- The GUI strings in the drafts already exist in English and Italian.
