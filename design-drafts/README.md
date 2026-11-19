# Design drafts

Temporary home for dashboard design explorations. Each draft is a single self-contained HTML file: open it in a browser, no build step. They are **prototypes with example data**, not part of the dashboard (`dashboard/`), and they are not wired to the hub API.

When a direction is agreed, it goes through a design issue (see AGENTS.md, "New areas start with a design issue") and is rebuilt in the React dashboard. Then this folder can be removed.

| File | What it is | Status |
|---|---|---|
| `machine-room-v1.html` | The "machine room": an isometric room that shows each node where it physically is (rack, desk, cloud), cabled to the hub; a queue of what needs attention; an inspector with the node's front panel; a per-minute CPU load wall. | Chosen direction (first draft). |
| `machine-room-v2.html` | Second iteration of the machine room: left sidebar navigation, alerts moved onto the nodes and into a drawer, uniform 2U nodes named only by their node name. | Work in progress. |

## Notes for whoever turns this into the real dashboard

- Nodes are named only by their node name (for example `ceres`), never by a role such as `nas-` or `web-`: a role is not part of the name.
- The room needs data Regulus does not have yet: the **physical location** of a node (rack, unit, desk, cloud) and the **temperature**. Both need a design issue before code.
- The GUI strings in the drafts already exist in English and Italian.
