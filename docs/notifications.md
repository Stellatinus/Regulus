# Notification panel (design)

**Status: design for #533, proposed, waiting for the maintainer's agreement. Nothing here is built, and no code is written until it is agreed** ([AGENTS.md](../AGENTS.md): a new area starts with a design). The bell is **not in design v3.2** ([design-system.md](design-system.md)); this page starts from the issue.

**The idea (#533).** A **bell** to the left of the account menu in the notch opens a **panel**: one place for what needs an administrator's attention, with an unread count on the bell.

## What exists today, and what does not

| Thing to show | Where the data is today | State |
|---|---|---|
| A new release of the core (hub and agents) | the update state of the hub (`core.update`, the Updates page) | exists |
| A plugin install, update or removal finished, failed | the store's progress record (#530), kept **in memory for a minute** | exists, not kept |
| A plugin update is available | `GET /api/store` (`latest` against `installed`) | exists |
| A server went offline, a disk is full, a certificate ends | the alert rules of [alerts.md](alerts.md): the evaluator runs on the hub (#404); the dashboard list and badge, the API and delivery are **not built** | half |
| A failed backup | [backup.md](backup.md) is a design | no data yet |
| A message from an installed app or plugin | no door exists; it needs the host side of plugins (#261, #287, #288) | not designed |
| Other system events | the audit log has events, but it is a log, not a list of things to act on | exists as a log |

The panel must not invent a source that does not exist: what is "no data yet" does not appear until its feature does.

## One decision first: a view, plus one table

Two kinds of item behave differently, and the design follows that:

- **State-based:** true until the cause is gone: *a core update is available*, *a plugin update is available*, *an alert is open*. Copying them into a table would let the panel disagree with its source (an update installed, a notification still there). So they are **not stored**: the hub **derives them when the panel is read**, from the source of truth, and they disappear when the cause does. A user can still mark one **read** (and, if it is dismissible, hide it until it changes: a newer release is a new item).
- **Events:** one-off, true once: *plugin X was installed*, *X failed and was rolled back*, *a backup failed*, and every message an app or plugin sends. These are **rows in a table** with a retention.

The panel is the two merged and sorted by time, newest first, unread first within a level.

## Data model (proposal)

A notification, in the API and in the table for events:

| Field | Meaning |
|---|---|
| `id` | stable; for a derived one built from its source (`update:core:1.2.0`, `alert:<id>`, `update:plugin:disks:0.2.0`) so a read mark survives reads |
| `source` | `core`, `plugin:<id>` or `app:<id>` |
| `level` | `info`, `warning` or `error` |
| `title`, `text` | English and Italian (`{en, it}`); core items come from the dashboard's catalogs by `code` and `args` (as the audit log's details, #368), so a text follows the language of the page; a plugin or app gives both texts |
| `link` | a route inside Regulus (`#/plugins/store`, `#/settings/updates`, an alert); nothing else unless the manifest declares the host |
| `created_at` | when it began |
| `requires` | the permission that lets someone see it (below) |
| `dismissible` | an event can be cleared; a state-based item goes by itself |

**Where it is stored.** The table of events, `notifications`, in the hub's database (both SQLite and PostgreSQL, expand-only migration, [updates.md](updates.md#rollback-and-the-schema-range)); **read marks per user** in `notification_reads(user, id, read_at)` and **dismissals per user** in the same way, so one administrator clearing an item does not hide it from another. **Retention:** events for 30 days, and at most 500 per source (the oldest go first); a plugin that floods loses its own oldest, not others'. These numbers are proposals.

## Delivery

- **To the dashboard:** the live stream already carries `plugins.changed`; add `notifications.changed` (no payload: the page reads the count and, when the panel is open, the list again), as the Plugins page does. The unread count on the bell comes from `GET /api/notifications?summary=1`. No polling.
- **Relation to #414 (email and webhooks):** one event source, several channels. [alerts.md](alerts.md) already names the dashboard as the channel that is always on; **the panel is that channel**. An alert opening writes one event for the channels it is configured for (email, webhook) and shows in the panel from the same state; the panel adds nothing to what a channel receives. The word *notification* in alerts.md means a message to a channel; in the panel it means an item. The glossary says both (below).
- **Alerts:** the bell takes the place of "a badge in the header" in alerts.md; the Alerts page stays the full list with acknowledge, and the panel links to it.

## Who sees what

Each item carries the permission needed to see it, and the panel lists **only what the viewer holds**: core updates need `core.update`; plugin operations and plugin updates need `plugins.manage`; alerts need `servers.read`; app and plugin messages need the permission their manifest names, and no more than the viewer's own. A role with nothing to see gets a bell with no count (and no panel). What an app or plugin may send is limited by the host, never by the sender: **a rate limit** (proposal: 20 per hour per source), **a size limit** (title 80 characters, text 400), **no HTML or markup** (text only), **no link outside Regulus** unless the manifest declares it and an administrator approved the plugin, and **a level cap** (`error` only for sources with a manifest key that asks for it). A source that breaks a limit is dropped and the drop is counted in the audit log.

## Behaviour

- **Mark as read** one, or **mark all**; opening the panel does not mark anything.
- **Filter** by source and by level; **clear** the dismissible ones (events) for the viewer.
- Items that **stay until the cause is gone** (an update available, an open alert) show a **Resolve in** link instead of a clear button, and cannot be cleared.
- The bell shows the unread count (`9+` above nine); an `error` unread turns it to the warning colour.

## Accessibility

The bell is a button with an accessible name that includes the count (*Notifications, 3 unread*), the count is in a **polite live region** (announced when it grows, not on every read), the panel is a labelled popover that opens and closes with the keyboard (Escape returns the focus to the bell), every item is reachable by Tab, and the unread state is **not** colour alone. It works in every theme and at a phone width (the panel becomes a full-width sheet).

## The plugin and app side

A manifest key (proposal: `notifications: {permission, max_per_hour}`) and an SDK call, behind the permissioned door of the plugin host. It depends on #287 and #288 and on the host side of plugins (#261), none of which exists, so **it comes last** and nothing about it is promised before they are designed.

## Build order, once agreed

1. **The core panel and the bell**: the endpoint, the stream event, the read marks, with the two state-based sources that exist today (core update, plugin update) and the plugin operations (which need the store's progress record kept, #530).
2. **Alerts in the panel**, when the alert API of [alerts.md](alerts.md) exists.
3. **Apps and plugins**, after #287 and #288.

Each is its own issue with a "Done when" line; the permissions, the audit events and the strings (English and Italian) are part of each, and the lines for [verified-systems.md](verified-systems.md) too.

## Open questions for the maintainer

1. **View plus table** as above, or one table for everything (simpler, but it can disagree with its source)?
2. **Per-user read and dismissal** as proposed, or shared for the whole hub?
3. **Retention:** 30 days and 500 per source?
4. Is a core-update notification shown to everyone with `core.update`, or only to administrators?
5. **The bell for roles with nothing to see:** hidden, or shown empty?
