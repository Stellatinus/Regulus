# Hub backup and restore

**Status: design (#253), not implemented.** Today a backup is a copy of `data_dir` and of the database by hand ([Database](database.md)), and with PostgreSQL a database restored without `secret.key` cannot decrypt the two-factor secrets. The only copy Regulus makes is the SQLite snapshot (`pre-update.db`) that the update helper takes before it swaps the executable and removes when the update ends.

The goal is "install once" ([Design decisions](decisions.md)): a hub that has to be reinstalled, or that lost its disk, must come back with its users, servers, enrolled agents and settings **without enrolling every agent again**. This page is about the hub's own state. Backups of *other* servers are the backup node (#166), and moving data between database backends is #124; both can reuse the format below.

## What is in a backup

| Part | How it is taken | Why it is needed |
|---|---|---|
| The database | SQLite: a consistent copy with `VACUUM INTO`, while the hub runs. PostgreSQL: a **logical export through the hub's own connection**, table by table in one `REPEATABLE READ` transaction (see the decision below). | users, roles, sessions' hashes, agents and their certificate serials, settings, audit log |
| `secret.key` | a copy of the file | without it the two-factor secrets in the database cannot be read |
| `pki/` (`ca.crt`, `ca.key`, the listener certificate) | a copy | every enrolled agent pins the CA; a new CA means enrolling everything again |
| The manifest | generated | hub version, schema version, database type, creation time, the list of parts with their SHA-256 |
| The metrics history tables (#387) | **left out by default** (`--with-metrics` includes them) | the largest table, and it can be rebuilt by waiting |

**Not included:** `/etc/regulus/config.yaml`, `regulus.env` and `agent.yaml`. They are owned by root and the hub runs as `regulus`, so the hub cannot read `regulus.env` (it can hold the database password), and a backup that contains it would put a root secret into a file the hub's user can write. `regulus backup` run by an administrator on the machine can add them with `--with-config` (it reads them as root, and says so); the dashboard never does. The page for restore lists them as the files to copy by hand otherwise. Also not included: `updates/`, `tuf/`, `update-apply/`, `regulus.sock`, `hub.lock` and `running.json`: they are caches or live state.

## Format

One file, `regulus-backup-<hub id>-<UTC time>.rgb`, which is a `tar` stream of the parts above plus `manifest.json` (first), **encrypted with a passphrase** the administrator gives. It holds the CA key and the key that decrypts the two-factor secrets, so a file that is not encrypted would be a copy of the hub's whole authority.

- **Encryption: `age` in passphrase mode** (scrypt work factor, ChaCha20-Poly1305), through the Go library `filippo.io/age`. No home-made cryptography, no key management to design, and the file can be opened with the `age` tool when everything else is lost. The passphrase is never stored; there is no recovery without it, and the dashboard says so before it makes the first backup.
- The manifest has `format_version` (an integer; the restore refuses a version it does not know), the hub version and the schema version. It is inside the encrypted stream, so a damaged or swapped part is found by its checksum after decryption.
- The database part is a **portable export**: one JSON-lines file per table in schema order, with the migration number. It is the same for SQLite and PostgreSQL, so a backup of one can be restored on the other. That is what #124 needs, and it is why `pg_dump` was not chosen.

## Where it goes

- **Download** from the dashboard (**Settings → Backup**): the hub builds the file, the browser saves it. Needs `backup.create` and step-up (see [Authentication](authentication.md)), because the file contains every secret.
- **A directory on the hub**: `data_dir/backups/` (mode `0700`), written by **scheduled backups**: a time of day (default 03:30, off until the administrator sets a passphrase) and a retention count (default 7), older files deleted after a new one succeeded. The passphrase for scheduled backups is stored encrypted with `secret.key`, so a scheduled backup protects against losing the *disk*, not against whoever reads the whole data directory; the dashboard says that, and recommends copying the files off the machine (a later backup node, #166, does it for them).
- **Before every update** the hub takes a backup called `pre-update` (kept for the last 3), as the SQLite snapshot of the update helper does today. For **PostgreSQL** this is the rollback that is missing today (#247): the helper cannot copy a database server, the portable export can be restored by hand after a rollback.
- `regulus backup FILE` on the machine does the same from the console, with a passphrase read from a prompt or a file.

## Restore

- **From the console, on a fresh installation:** `regulus restore FILE`, as the user that owns the data directory, with the hub **stopped** (it holds the instance lock of #217 while it runs, and restore takes the same lock, so two cannot run at once). It asks for the passphrase, verifies every checksum **before it changes anything**, then writes into an empty data directory. A data directory that already holds a hub's data is refused unless `--force` is given, which moves the old files into `data_dir/restore-old-<time>/` instead of deleting them.
- **From the dashboard:** not in the first version. Replacing the running database from a browser needs the hub to stop serving, restart in a restore mode and come back; that has its own failure cases and is built after the console restore works (issue 5 below). Until then the dashboard offers **download** only.
- **A backup made by an older version** is restored and then migrated forward by the normal migrations. **A backup made by a newer version** (a schema this hub does not know) is refused, with the version that made it.
- **Agents enrolled after the backup was made** are not in the restored database, so their certificate serial is unknown and the hub refuses them; they enroll again with a new token. **Agents revoked after the backup** are connectable again, because the revocation was made after the copy: the restore prints the date of the backup so the administrator can revoke them again. Agents in the backup keep working without a new token, because the CA, the listener certificate and the serials came back.
- **Sessions** are not restored: every user signs in again. A user who turned two-factor on after the backup is asked to set it up again (the secret is not in the file).
- The audit log gets `backup_restored` as its first entry, with the manifest's time and hub version.

## Permissions and audit

- `backup.create`: make and download a backup, set the schedule (admin only; step-up).
- `backup.restore`: reserved for the dashboard restore; the console restore needs the machine, not a permission.
- Audit events with a code and arguments: `backup_created`, `backup_downloaded`, `backup_failed`, `backup_deleted` (retention), `backup_restored`. They never carry a passphrase.

## What is decided, and why

- **Logical export, not `pg_dump`.** `pg_dump` needs the binary on the hub, its major version must match the server, and its output restores only to PostgreSQL. A table-by-table export through the hub's connection has no dependency, restores to either backend and is tested the same way on both.
- **One encrypted file, no plain option.** A plain backup would be one `cp` away from taking over the hub's agents; a flag that disables encryption would be used.
- **No config files by default** (above), and **no metrics history by default**.
- **Restore is offline.** A restore that runs under a live hub can leave half of the old state; stopping the hub is the simplest rule that is always correct.
- **Console first.** The case that matters is a fresh machine with no dashboard.

## Implementation issues

1. Create a backup: the export of both backends, the format, `age` encryption, `regulus backup`, the manifest and its tests (M4).
2. Restore on a clean installation: `regulus restore`, verification before writing, forward migration, refusal of a newer schema, the instance lock (M4).
3. The dashboard: **Settings → Backup**, download, the passphrase dialog, English and Italian (M4).
4. Scheduled backups and the automatic `pre-update` backup with retention (M5).
5. Restore from the dashboard (after 2).
6. `[Test]`: a restore on a real machine, with an enrolled agent that keeps working, and a PostgreSQL hub (M4).
