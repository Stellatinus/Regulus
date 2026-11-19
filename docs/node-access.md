# Web terminal and file browser (design, proposed)

**Status: proposal, waiting for the maintainer's agreement. Nothing on this page is implemented and no code may be written until the "decide" points below are agreed** (AGENTS.md, "New areas start with a design issue"). It answers the design issues [#236](https://github.com/Stellatinus/Regulus/issues/236) (web terminal) and [#237](https://github.com/Stellatinus/Regulus/issues/237) (file browser), which share one transport, one sandbox problem and one answer to "as which user". The visual prototype is [`design-drafts/node-access-v2.html`](../design-drafts/node-access-v2.html) (example data, not wired to anything).

"Node" is used as in [node types](node-types.md): any enrolled machine, including the hub's own server through the built-in agent. **Node access** is the name of this feature as a whole (terminal plus file browser); it has nothing to do with **Regulus Access**, the phone app ([regulus-access.md](regulus-access.md)).

## What is wanted, and why it is dangerous

From the dashboard, in the browser, open a shell on **one** node, or browse and manage the files of **one** node, with no inbound port and no SSH setup on the node. The maintainer wants it in the core, possible to disable, and not necessarily installed for everyone.

Today the hub can only ask agents for metrics, intervals and signed updates. Node access is the **first channel that runs arbitrary code or changes arbitrary files**. The hub holds the credentials that reach every node, so without limits a compromised hub, or a stolen admin session, would become a shell on every node. The design below is mostly about bounding that.

## The answers in one table

| Question | Recommended answer | Section |
|---|---|---|
| As which user? (shared, Decide 1 of #236) | A **local account of the node**, chosen from a list the node keeps, **authenticated by the node itself (PAM)** through a small root **session helper**. Never root. Regulus is the transport, not the authority. | [Decide 1](#decide-1-as-which-user-shared) |
| Core or plugin? (shared, Decide 2 of both) | **In the single binary, off by default on both sides** ("two keys"). The helper unit is installed but not enabled. | [Decide 2](#decide-2-core-optional-package-or-plugin-shared) |
| Which files? (Decide 1 of #237) | Only beneath **roots listed on the node**, none by default, each `ro` or `rw`. Nothing outside them is visible, even to a hub administrator. | [Decide 3](#decide-3-scope-of-the-file-browser) |
| Which permissions? | `terminal.open`, `files.read`, `files.write`, `files.delete`, `files.chmod`, `sessions.manage`: **explicit** permissions that the built-in `admin` role does **not** get through "every permission". | [Controls](#controls) |
| Second check at the hub? | Step-up (password, plus the two-factor code) on every new session; **two-factor enabled is required** to use node access. | [Controls](#controls) |
| Recording? | No session recording in the first version; audit of every session and every file change, never content. | [Decide 4](#decide-4-what-is-recorded) |

## Decide 1: as which user (shared)

The agent runs as the unprivileged `regulus` user with `NoNewPrivileges`, an empty capability set, `ProtectSystem=strict`, `ProtectHome=read-only`, `SystemCallFilter=@system-service` and `MemoryDenyWriteExecute` (`packaging/regulus-agent.service`). A shell or a file operation started by the agent inherits all of that.

| Option | How | Verdict |
|---|---|---|
| A. The agent's own user | spawn `$SHELL` as `regulus` inside its sandbox | **Rejected.** Read-only system, no `sudo`, no `/home`: safe and almost useless, and a second, weaker mode would confuse more than it helps. |
| **B. Session helper + local account (PAM)** | a root unit, separate from the agent, started per connection; it asks **PAM** to authenticate a local account the node allows, opens a real login session for it, then drops to that account before starting the shell or the file worker | **Recommended.** The node's own authentication stays in charge; `sudo` inside the shell follows the node's own `sudoers`, so Regulus never grants root by itself. A compromised hub still needs a password the node checks. |
| C. Embedded SSH client to the node's `sshd` | browser terminal → hub → agent → `localhost:22` | **Rejected for the first version.** Needs `sshd` on every node and either keys held by the hub (a compromised hub then has them all) or the same password relay as B with more moving parts. It can come back later as a second backend of the same session channel. |
| D. Root shell on a hub grant | the helper starts `root` when the hub asks | **Rejected.** A hub compromise would be root everywhere, with no second check. |

Rules of option B:

- **The list of local accounts lives on the node** (`/etc/regulus/node-access.yaml`, owned by root, see Decide 2), empty by default. `root` and any account with UID 0 are refused even if listed: use `sudo` inside the session.
- **The helper enforces the list, not the agent.** The helper reads the root-owned file itself; what the agent sends is treated as untrusted (only a user name, a terminal size, a request). A bug in the agent cannot widen access.
- **Authentication is the node's PAM stack** (a new `/etc/pam.d/regulus-session` including the distribution's `common-auth`, `common-account` and `common-session`, or `system-auth` on RHEL). Prompts are relayed to the browser and shown in a **form above the terminal, not inside it**; answers to hidden prompts are never logged. A node that adds TOTP to PAM (for example `pam_google_authenticator`) gets it for free.
- **A real login session**: `pam_open_session`, so `systemd-logind`, limits and the node's own `auth.log` or journal see a login like any other. This is the node's own record of who logged in, which a compromised hub cannot erase.
- **The file browser uses the same login**: the file worker runs as the chosen local account, so the operating system's permissions apply on top of the roots. A file session is one helper connection like a terminal: one local login, then any number of operations until it ends (idle timeout, End session, revocation). Each session logs in on its own; reusing one local login for several sessions would mean the hub or the agent keeping a credential, which this design avoids.
- **Changing the owner of a file to another user** (`chown`) needs root, so it is **out of the first version**; `files.chmod` covers the mode and the group among the account's own groups.

## Decide 2: core, optional package or plugin (shared)

| Option | Verdict |
|---|---|
| **Core, off by default on both sides** | **Recommended.** A PTY, a file worker and a relay are small; the plugin runtime (M6) cannot yet host something that needs a root helper, and isolation of plugins (#267) is not designed. "Core" does not mean "every node is remotely shell-able out of the box". |
| A separate optional package | Rejected: one binary is a decision (#227, [versioning](versioning.md)); a second package means a second install and update path. The same effect comes from the helper unit not being enabled. |
| A plugin, later | Rejected for now, kept as a possibility: when #261 and #267 exist, the file browser could move behind the plugin API with the same rules. #68 must be updated: the "file manager plugin" becomes this core feature, offered to the AI assistant only through its own approval model (#63). |

**Two keys** ([node types](node-types.md), "Two keys for exposure"):

1. **The hub key.** An administrator grants the node the capability `terminal` and/or `files` (new names for the capability list of #169) on the node's page. Granting is audited and needs step-up.
2. **The node key.** On the node, as root, the operator turns it on locally. Nothing the hub sends can change it:
   - at install time: `install.sh --allow terminal,files --access-user alice` (extends the `--allow` list proposed in #164);
   - later: `regulus node-access enable terminal --user alice`, `regulus node-access add-root /srv --rw`, `regulus node-access disable` (all write `/etc/regulus/node-access.yaml` and enable or disable `regulus-session.socket`).

The package installs `regulus-session.socket` and `regulus-session@.service` **disabled**. A node that never opts in has no listening helper at all: even a fully compromised hub cannot open anything on it. The agent reports the node key's state in `Hello` (new optional field) for display only; the dashboard shows "not allowed on this node" with the command to run.

Example of the node file:

```yaml
# /etc/regulus/node-access.yaml — root:root 0644. The hub cannot change this file.
terminal:
  enabled: true
  users: [alice]            # never root; empty = nobody
files:
  enabled: true
  users: [alice]
  roots:
    - { path: /srv/media, mode: rw }
    - { path: /etc/nginx, mode: ro }
limits:
  max_sessions: 4           # on this node, all users
  upload_max_bytes: 2147483648
```

**The hub's own server.** The built-in agent has no certificate identity; it reaches the helper of the hub machine the same way. The UI warns that the hub machine is the most valuable one and suggests leaving it off there.

## Decide 3: scope of the file browser

- **Roots are listed on the node**, none by default; each is `ro` or `rw`. A root must be an absolute path, not `/`, not under `/proc`, `/sys`, `/dev` or `/run`; it is resolved once when the file is loaded (a root that is a symlink is resolved then, and a root that disappears is shown as unavailable).
- **Hub permissions are separate**: `files.read` (list, open, download), `files.write` (upload, edit, create folder, rename, move, copy), `files.delete`, `files.chmod`. The effective right is the **intersection** of the hub permission, the root's mode and the local account's own file permissions. A `ro` root refuses writes even to a user with `files.write`.
- **Deletion has no trash** in the first version (decide): a trash would need a writable place per root and a cleanup policy. Recursive delete asks for confirmation, naming the node and the full path, and the number of entries when known.
- **Listings are not audited one by one** (decide): they would flood the log; **opening or downloading a file is**.

## Decide 4: what is recorded

- **Audit (hub):** `node_session_opened` (user, node, kind, local account, source address), `node_session_closed` (reason, duration, bytes in and out), `node_session_denied` (reason: permission, step-up, node key, local authentication, limit), `node_session_terminated` (by whom), `node_access_switch` (kill switch on or off), `node_access_granted` / `node_access_revoked` (hub key), and for files `file_opened`, `file_downloaded`, `file_written`, `file_uploaded`, `folder_created`, `file_renamed`, `file_copied`, `file_deleted`, `file_mode_changed` with node, root, path, size and result. **Never content.** These need a login, so they are in the 100,000-row class of the audit log, not the class anyone can flood (#200).
- **Node:** the helper logs every login and end through PAM and to the journal (`regulus-session`), so the node keeps its own record.
- **Session recording: not in the first version** (decide). It would store everything typed and shown, including secrets that are not behind a hidden prompt, and its size is unbounded. If wanted later it is opt-in per node, kept on the hub with its own retention, and announced in the session bar.

## Threat model

| Attacker | Can | Cannot |
|---|---|---|
| **Stolen session cookie** (no password) | see which nodes allow node access; see their own live sessions | open a terminal or file session (step-up needs the password and the code); grant a node; see other users' sessions without `sessions.manage` |
| **Stolen admin credentials** (password and two-factor, for example phished) | step-up; give themselves `terminal.open` by creating a role (audited); try to open sessions | reach a node that did not opt in; use an account not in the node's list; use `root`; get in without the **local account's password**, which the node checks |
| **Compromised hub** (code running as `regulus` on the hub, or its database and `secret.key`) | bypass every hub-side control (permissions, step-up, limits, audit); serve modified JavaScript to administrators; **capture local passwords typed after the compromise** and then use them on the nodes and accounts that allow it | reach nodes that did not opt in (no helper listening); choose an account outside the node's list; become root through Regulus; push code to agents (updates are verified by the root helper against the Sigstore identity of the release workflow, not trusted from the hub); erase the node's own login records |
| **Compromised agent process** (a bug in the agent) | talk to the helper socket, like the hub | the same limits as the compromised hub: the helper trusts only PAM and its own file |
| **Hostile node** (someone controls the node) | send huge outputs, hostile file names, terminal escape sequences, files that look like HTML | exhaust the hub (frame cap, bounded buffers, backpressure, per-session limits); write the browser's clipboard (OSC 52 off) or open links without a click; run script on the dashboard origin (downloads are attachments with `nosniff` and a sandboxing CSP, file names are text, never HTML) |
| **Network attacker** between browser and hub | nothing useful | read or change a session: node access is **refused over plain HTTP** unless the browser is on the hub machine itself (loopback) |

**Residual risk, said plainly:** after a hub compromise, every local password typed into the dashboard for a node is lost, and those nodes and accounts are then reachable. Mitigations are on the node side: a dedicated account per node with a unique password, TOTP in PAM (it limits reuse, not a live relay), a short user list, and turning the node key off. A node that must survive a hub compromise unharmed simply does not opt in.

## Controls

- **Permissions** (`internal/auth`): `terminal.open`, `files.read`, `files.write`, `files.delete`, `files.chmod`, `sessions.manage` (see every live session, terminate any, use the kill switch). They are **explicit permissions**: a new flag in the catalogue that excludes them from a role with "every permission" (`all: true`), so the built-in `admin` does not get them by upgrading Regulus. An administrator gives them through a custom role; the roles page shows them in their own group, marked dangerous. Per-node scoping of user permissions waits for node groups; in the first version the per-node control is the hub key.
- **Two-factor required:** a user without two-factor enabled cannot open node access, whatever their role (decide; the alternative is to follow the global two-factor policy).
- **Step-up on every new session** (needs #279): password plus the two-factor code; the hub returns a **one-time ticket** valid 60 seconds and bound to user, node and kind, which the WebSocket upgrade must present. A step-up stays valid 5 minutes for opening *another* session (a second tab or the file browser), never for more than that.
- **Limits** (hub settings with these defaults, the node's own `limits` can only lower them): terminal idle timeout 15 minutes (no input), maximum session length 8 hours, 3 concurrent sessions per user, 4 per node, 20 on the hub; data frames at most 32 KiB; at most 1 MiB buffered per session on the hub; at most 5,000 entries per directory page; text editing up to 1 MiB; uploads up to 2 GiB, in chunks of 256 KiB; 2 concurrent transfers per user and per node.
- **Revocation within seconds**: the hub keeps a registry of live sessions and closes the affected ones **when the event happens** (logout, account disabled, password changed, role losing the permission, node revoked, hub key removed, kill switch), not on the 30-second recheck of the live stream (#198), which stays as a backstop.
- **Live sessions page** (Administration): who, node, kind, local account, since, idle, bytes; a **Terminate** button per row; the **kill switch** closes every session and refuses new ones until turned back on. The kill switch is also `regulus config set node_access.enabled false` on the hub machine. The strongest switch is on the node: `regulus node-access disable`, or `systemctl disable --now regulus-session.socket`.
- **Ending a session**: closing the tab closes the WebSocket and the session (no detach in the first version). The helper sends `SIGHUP` to the session's process group, then `SIGKILL` after 5 seconds, and closes the PAM session. Processes the user detached on purpose (`nohup`, `setsid`, `tmux`) survive, as after any SSH logout.
- **Browser side**: WebSocket with the session cookie and an `Origin` check; downloads with `Content-Disposition: attachment`, `Content-Type: application/octet-stream`, `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`; xterm.js with OSC 52 clipboard writes off and links opened only on a click with a confirmation.

## Protocol sketch

A new **agent protocol version** (2), negotiated as today; an agent that speaks only 1 shows "agent too old for node access". The frozen update contract (`protocol.UpdateContract`) is not touched.

Why not put the bytes on `Connect`: one HTTP/2 stream shares one flow-control window, so a download would delay heartbeats and a slow browser would block metrics. Each session gets **its own stream on the same mTLS connection**, like `FetchUpdate` already does. The agent stays the client: the hub never connects to a node.

```protobuf
// New command kind (Command.kind):
message OpenSession {
  string session_id = 1;     // random, chosen by the hub, single use
  SessionKind kind = 2;      // TERMINAL or FILES
  string local_user = 3;     // checked again by the helper against its own list
  uint32 cols = 4; uint32 rows = 5;
}

// New RPC on AgentService; the agent calls it after OpenSession:
rpc Session(stream SessionUp) returns (stream SessionDown);
// SessionUp   (agent -> hub): attach{session_id} first, then auth_prompt, data, exit, file_response, chunk, closed{reason}
// SessionDown (hub -> agent): auth_answer, data, resize, file_request, chunk, close{reason}
```

- The hub accepts `attach` only for a pending `session_id` issued to **the agent id in the client certificate**, within 30 seconds.
- **Limits:** every message at most 64 KiB (well under today's 256 KiB `maxAgentMessageBytes`); per-stream HTTP/2 flow control is the backpressure: the hub reads from the agent only as fast as the browser takes the bytes, so a noisy shell blocks on its own PTY instead of filling the hub's memory.
- **Agent ↔ helper:** a Unix socket `/run/regulus-session.sock`, socket-activated (`Accept=yes`, owner root, group `regulus`, mode `0660`), one helper process per connection. The same frames, length-prefixed. The helper does PAM, opens the session, then `setgid`/`initgroups`/`setuid` before it starts the shell on a PTY or the file worker. The helper unit runs **without** `NoNewPrivileges` (so `sudo` works in the shell) but with the rest of the sandbox that does not get in the way of a login.
- **Browser ↔ hub:** `GET /api/agents/{id}/terminal` (WebSocket: binary frames for data, JSON text frames for resize, auth prompts and close); `/api/agents/{id}/files/...` for file operations, with downloads and uploads streamed over HTTP (no whole file in memory on the hub).

## Path confinement rules (file worker)

The worker runs as the local account, opens each root once as a directory file descriptor, and resolves everything relative to it.

1. A request names a **root id and a relative path**, never an absolute path. Reject: absolute paths, `..` components, NUL bytes, empty components after cleaning, a path longer than 4096 bytes or a component longer than 255.
2. Resolve with **`openat2` and `RESOLVE_BENEATH | RESOLVE_NO_MAGICLINKS | RESOLVE_NO_XDEV`**: symlinks that stay inside the root work, a symlink that points outside fails, `/proc` magic links fail, crossing into another mount fails. `openat2` needs Linux 5.6: Ubuntu 22.04 and later and Debian 12 have it; **RHEL 8 (4.18) does not, and the file browser is refused there** rather than emulated in user space.
3. **Act on file descriptors, never on paths again**: operations use the parent directory's fd and the last component (`unlinkat`, `renameat2`, `mkdirat`, `fchmodat` on the opened fd), so a path swapped between check and use cannot redirect them.
4. The last component is never followed when it is a symlink, for `delete`, `rename`, `chmod` (a symlink is deleted or renamed as a link; `chmod` on a link is refused).
5. **Special files** (devices, FIFOs, sockets) are listed with their type and never opened: files are opened with `O_NONBLOCK | O_NOFOLLOW`, then `fstat` must say "regular file" before any byte is read.
6. **Recursive delete and copy** walk with directory fds, do not follow symlinks, stay on the same device, and stop at a limit of entries, reporting what was done.
7. **Uploads** go to a temporary file created in the target directory (`O_TMPFILE` when the filesystem supports it, otherwise `.regulus-upload-<random>` with `O_EXCL | O_NOFOLLOW`), are `fsync`ed, then renamed with `renameat2(RENAME_NOREPLACE)`; overwriting needs an explicit "replace" and uses a plain rename. The mode is set explicitly (default `0644` for files, `0755` for folders, minus the account's umask). An optional SHA-256 from the browser is checked before the rename.
8. **Editing** sends the file's size, modification time and SHA-256 with the content; saving sends them back and the worker refuses if they changed (conflict shown to the user). Binary files (a NUL in the first 8 KiB, or invalid UTF-8) and files over 1 MiB open read-only.

The path resolver gets a **fuzz test and a property test** (no accepted request ever resolves outside its root, with random symlink trees), on top of unit tests for each rule.

## User interface

The prototype shows each state (open it with `#notallowed`, `#notgranted`, `#noperm`, `#step`, `#live`, `#ended`, `#files`, `#ro`, `#delete`, `#sessions` or `#kill` after the file name to jump to one); texts in English and Italian. The UI word is "node", as in the v2 drafts (today's dashboard still says "server"; the change of UI word is the naming plan's, not this design's).

- **Node page** gets two tabs, **Terminal** and **Files**, shown only to users with the permission. Before a session: a state card says exactly which key is missing — *not allowed on this node* (with the command to run on the node), *not granted by the hub* (with the grant button for those who may), *agent too old*, *two-factor required*.
- **Opening**: step-up dialog (Regulus password and code), then the local login form (account from the node's list, the node's password, any further PAM prompt).
- **Live terminal**: a **session bar** always visible above the terminal, in the signal colour: node name, local account, elapsed time, idle countdown (warns in the last 2 minutes), **End session** button. xterm.js (pinned, bundled, no CDN), resize, UTF-8, copy and paste, 5,000 lines of scrollback. After the end: a closed banner with the reason and **Open again**.
- **File browser**: a **root banner** (node, root, `rw` or `read-only`, local account), breadcrumb, a table (name, size, modified, mode, owner) with sort and filter, keyboard navigation, upload by button or drag and drop with a progress row per file, rename, new folder, download, and a **delete confirmation** that names the node and the full path and asks to type the last component for recursive deletes.
- **Administration → Live sessions**: the list with Terminate, and the kill switch with a confirmation.

### Key texts (English / Italian)

| Key | English | Italian |
|---|---|---|
| tab terminal | Terminal | Terminale |
| tab files | Files | File |
| not allowed | Node access is not allowed on this node. An operator must turn it on locally. | L'accesso al nodo non è consentito su questo nodo. Deve abilitarlo localmente un operatore. |
| command hint | Run on the node as root: | Esegui sul nodo come root: |
| not granted | The hub has not granted terminal access to this node. | Il hub non ha concesso l'accesso al terminale per questo nodo. |
| grant | Grant on this node | Concedi su questo nodo |
| no permission | Your role does not include "Open a terminal". | Il tuo ruolo non comprende "Apri un terminale". |
| need 2FA | Turn on two-factor authentication to use node access. | Attiva l'autenticazione a due fattori per usare l'accesso al nodo. |
| step-up title | Confirm it is you | Conferma la tua identità |
| step-up body | Enter your Regulus password and a code from your app to open a session on {node}. | Inserisci la password di Regulus e un codice dell'app per aprire una sessione su {node}. |
| local login | Log in to {node} | Accedi a {node} |
| local account | Local account | Account locale |
| local password | Password of {user} on {node} | Password di {user} su {node} |
| session bar | Live session on {node} as {user} | Sessione attiva su {node} come {user} |
| idle | Closes after {t} without input | Si chiude dopo {t} senza input |
| end | End session | Termina sessione |
| ended | Session ended: {reason} | Sessione terminata: {reason} |
| root banner | {node} · root {root} · as {user} | {node} · radice {root} · come {user} |
| read-only | Read-only root | Radice in sola lettura |
| upload | Upload | Carica |
| new folder | New folder | Nuova cartella |
| delete title | Delete for good? | Eliminare definitivamente? |
| delete body | This deletes {path} on {node}, with {n} items inside. There is no trash. | Elimina {path} su {node}, con {n} elementi al suo interno. Non c'è un cestino. |
| type to confirm | Type {name} to confirm | Scrivi {name} per confermare |
| live sessions | Live sessions | Sessioni attive |
| terminate | Terminate | Termina |
| kill switch | Close all sessions and block new ones | Chiudi tutte le sessioni e blocca le nuove |
| kill on | Node access is off for the whole group | L'accesso ai nodi è disattivato per tutto il gruppo |

## Prerequisites

| Issue | What | State | Needed because |
|---|---|---|---|
| #279 | Step-up re-authentication | **open** | step-up on every session and on the hub grant |
| #198 | End the WebSocket when its session ends or loses permission | closed (30-second recheck) | the base; node access adds closing **on the event** |
| #199 | Bound what an enrolled agent can send | closed | the base; session streams get their own caps (above) |
| #200 | Audit log budgets | closed | node-access events must not be flushed by failures anyone can cause |
| #201 | Browser security headers | closed | the CSP; xterm.js must work under it (see open points) |
| #197 | Plain HTTP on all interfaces by default | **open** | mitigated here by refusing node access over plain HTTP; should still be fixed |
| #169, #170 | Capability and permission lists for nodes | **open** | the names `terminal` and `files` of the hub key |
| #164 | `--allow` list on the node | **open** | the node key at install time |
| #60 | sudoers or root helper for privileged actions | **open** | the session helper is a root helper; this design does not settle #60 for other actions |

## Implementation issues to open after agreement

1. Session helper and the node key: `node-access.yaml`, `regulus node-access`, `regulus-session.socket`/`@.service`, PAM file, installer `--allow terminal,files`.
2. Agent protocol 2: `OpenSession`, the `Session` stream, caps, golden files.
3. Agent: terminal sessions through the helper (PTY, resize, signals, end).
4. Agent: file worker with root confinement, fuzz and property tests.
5. Hub: explicit permissions, hub key per node, step-up ticket, live-session registry with revocation on event, limits, kill switch, audit events.
6. Hub: relay endpoints (WebSocket terminal, file API with streamed transfers and download headers).
7. Dashboard: terminal tab (xterm.js under the CSP) and session bar.
8. Dashboard: file browser.
9. Dashboard: live sessions page and kill switch.
10. `[Test]` terminal on real machines; `[Test]` file browser escape attempts and large files.

**Proposed milestone: M4** (phase 3, with privileged actions #60, which shares the root-helper question, and the protocol versioning this needs), and only once this design, #169/#170 and #279 are agreed. The `[Test]` issues go with them.

Then: update #68 (the file manager is core, not a plugin example) and #63 (the assistant never uses node access without its own approval model).

## Open points

- **xterm.js and the CSP.** The hub forbids inline `<style>` (#201). xterm.js adds style elements for its DOM renderer; the canvas or WebGL renderer, or a nonce for that one element, must be checked before choosing the version.
- **`connect()` on the helper socket from the agent's sandbox** (`ProtectSystem=strict`): expected to work (a socket is not a file write), to verify on a real machine.
- **SELinux on RHEL-like systems** may need a policy for a root helper that starts logins; not designed.
- **Detach and reattach** a terminal after a dropped connection: out of the first version.
- **Per-node scoping of user permissions** (user X may open terminals only on node Y): waits for node groups.
- **Sharing a terminal, transfers between nodes, search in contents, scripted or AI use**: out of scope (#236, #237).
