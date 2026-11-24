# Installation (Ubuntu Server)

The hub installs on Ubuntu Server 22.04, 24.04 and 26.04 (amd64 and arm64; other Debian-based systems with systemd should work too). One command downloads the `.deb` of the latest release, verifies its checksum and signature, installs it, starts the service and prints where to open the dashboard and the one-time setup token:

```sh
curl -fsSLO https://github.com/Stellatinus/Regulus/releases/latest/download/install.sh
sh install.sh
```

The same in one line is `curl -fsSL …/install.sh | sh`, with options after `sh -s --`; if the `-s` gets lost, `sh` takes the first option for a file name ("cannot open --version"), so the two steps are safer. Until the first stable release is out, `latest` has nothing to serve: use the command at the top of the notes of a [pre-release](https://github.com/Stellatinus/Regulus/releases).

The installer asks whether to include the **built-in agent** (so the hub's own server is managed like the others). To answer in advance, or in a script, add options after `sh install.sh` (or `sh -s --` in the one-line form): `--builtin-agent`, `--no-builtin-agent`, `--yes` (no questions), `--channel beta` or `--channel alpha` (latest release of that channel; also `--beta`, `--alpha`), `--version 0.2.0`, `--allow-unsigned` (see below). The installer **requires a valid signature** (#208): it downloads `checksums.txt.sigstore.json` and checks that `checksums.txt` was signed by the release workflow of this repository at exactly the tag of the version (`release.yml@refs/tags/v<version>`, not any ref), with [cosign](https://docs.sigstore.dev/cosign/) when it is installed, otherwise with `regulus verify-release` from the package itself, which it unpacks without installing (`dpkg-deb -x`). A missing or invalid signature stops the installation; `--allow-unsigned` (or `REGULUS_ALLOW_UNSIGNED=1`) goes on with the checksum only and says that this proves nothing about who built the package. `--require-signature` is still accepted and does nothing. Every download uses https only (`--proto '=https' --tlsv1.2`); `--base-url` must be an `https://` address unless `--insecure-base-url` is given for a test server. **What this does not do:** without cosign the package verifies itself, so a hijacked origin could ship a package that says yes; install cosign, or verify by hand as in [Releases](releases.md), to be independent of the origin. The very first install trusts GitHub and TLS either way. If you prefer not to pipe a script into a shell, download the `.deb` from the [releases](https://github.com/Stellatinus/Regulus/releases), verify it as shown under [Releases](releases.md) and run `sudo apt install ./regulus_*.deb`.

## Choosing the database

The database is chosen **once, at install time**. SQLite is the default and needs nothing. PostgreSQL is either a server **you prepare first**, which the installer connects to (see [Database](database.md) for the three SQL statements and the `pg_hba.conf` and firewall notes), or one the installer sets up **on this server** with `--db-local` (below).

- On a terminal, without `--database`, the installer asks `SQLite or PostgreSQL?`, and for PostgreSQL the host, port, database, user, TLS mode and the password (typed without echo).
- With `--yes`, or without a terminal (the one-line `curl ... | sh` form), the answer is **SQLite**; nothing new is asked, so existing commands keep working.
- Non-interactively: `--database postgres --db-host HOST` (the rest defaults to port `5432`, database `regulus`, user `regulus`, `--db-sslmode prefer`; also `--db-port`, `--db-name`, `--db-user`, `--db-sslmode`, `--db-sslrootcert FILE`) and the password from a file or the environment:

```sh
sh install.sh --yes --database postgres --db-host db.example --db-sslmode verify-full \
  --db-sslrootcert /etc/ssl/db-ca.pem --database-password-file /root/regulus-db-password
# or: REGULUS_DATABASE_PASSWORD=... sh install.sh --yes --database postgres --db-host db.example
```

**The password is never an option**: an option would show in the process list and in the shell history, and this script is usually piped. It is read from the first line of `--database-password-file`, from `REGULUS_DATABASE_PASSWORD`, or typed without echo, and it is written only to `/etc/regulus/regulus.env` (`0640`, `root:regulus`) — never to `config.yaml`, to a log or to the screen; `set -x` is turned off while it is handled.

**One command with a local PostgreSQL (`--db-local`).** `install.sh --db-local` (it implies `--database postgres`) installs the distribution's PostgreSQL with `apt` if it is not there (14 on Ubuntu 22.04, 16 on 24.04), starts it, creates the role (`regulus`) and the database (`regulus`, owned by that role) with a **generated password of 32 letters and digits**, enables `citext`, writes the settings to `/etc/regulus/regulus.env` (`0640`, `root:regulus`) and then runs the usual check. It connects to `127.0.0.1` and creates only that role and that database, so other databases of an existing PostgreSQL are not touched. The password is sent to PostgreSQL on standard input, is never an argument of any command, and is never printed; it exists only in the environment file. `--db-name`, `--db-user`, `--db-port` and `--db-sslmode` still apply; `--db-host`, `--db-sslrootcert` and `--database-password-file` do not (it is this server, and the password is generated).

- **If the role or the database already exists**, the installer stops and changes nothing; choose other names, or pass **`--db-reuse`**: it then uses the existing role and database, does not create or change them (it only makes sure `citext` is enabled) and needs the role's password from `--database-password-file` or `REGULUS_DATABASE_PASSWORD`.
- **Running it again** on a server that already has Regulus keeps the database it has: `--db-local` is ignored with a note, nothing is created twice. If the check fails after the role and database were created, the settings are already saved, so fix the cause and run the installer again.
- Not covered: tuning or hardening PostgreSQL, a non-default cluster layout (it uses the `postgres` system user over the local socket and the port you give, 5432 by default), and anything but `apt`.

**The connection is checked before anything is installed.** The installer takes the new binary out of the verified package and runs `regulus db check` with your settings. A wrong password, an unreachable host, a missing database, a missing privilege or a PostgreSQL older than 14 stops the installer with the cause, before any file of the system or `/etc/regulus` was changed. After the install the same check runs again with the installed binary and the saved settings. If the package is installed by `apt` alone, its post-install step prints the result of the check but never fails the package.

**Running the installer again, or upgrading the package, keeps the database.** Without `--database` the choice that is there stays and nothing is asked. `--database` naming the *other* backend stops with an explanation and touches nothing: moving data between SQLite and PostgreSQL is a separate feature (#124). On an existing PostgreSQL installation you may pass `--db-*` options or a new password file to change the connection (for example after rotating the password); only what you give changes.

What the package sets up:

| Path | Purpose |
|---|---|
| `/usr/bin/regulus` | the executable |
| `/usr/lib/systemd/system/regulus.service` | the service, enabled and started at install |
| `/etc/regulus/config.yaml` | configuration (kept on upgrade) |
| `/usr/lib/systemd/system/regulus-agent.service` | the agent service, used only on servers installed with `--role agent` |
| `/etc/regulus/regulus.env` | settings chosen by the installer, such as `REGULUS_BUILTIN_AGENT` and, with PostgreSQL, `REGULUS_DATABASE_*` including the password (`0640`, `root:regulus`); they override `config.yaml` |
| `/etc/regulus/agent.yaml`, `/etc/regulus/role` | agent role only: the hub address, and a marker that keeps the package from starting a hub |
| `/var/lib/regulus` | the database; kept when the package is removed or purged |
| `/var/lib/regulus-agent` | agent role only: the agent's key and certificates; kept on removal |

**Remove, purge and the `.rpm`.** `apt remove regulus` stops and disables the units and keeps the configuration, the data, the user and the group; installing the package again starts the hub like a first install (`remove` leaves the marker `/etc/regulus/.removed`, which the next install reads, because dpkg calls that install an upgrade). `apt purge regulus` also deletes `regulus.env`, `role`, `agent.yaml`, `agent.env`, `update-policy.yaml`, `/var/lib/regulus-update` and the update helper's files next to the executable, but never the data or the user (`rm -rf /var/lib/regulus /var/lib/regulus-agent && deluser regulus && delgroup regulus`). **rpm has no purge**: after `rpm -e` these files stay, `/etc/regulus/regulus.env` among them (it can hold the database password), and a reinstall reuses them; delete them by hand if that is not wanted. The `.rpm` is built and published but the supported platform is Ubuntu Server, and nobody has run it on a RHEL-like system yet (#401). The update helper replaces `/usr/bin/regulus` outside dpkg and rpm, so a later `apt upgrade` to an older packaged version would put the older program back and `dpkg --verify` reports the file as changed (see [Updates](updates.md)).

**Privilege model.** The hub runs as the system user `regulus`, which has no login shell, no home directory and no `sudo` rights, and the unit is hardened (no capabilities, read-only system, private `/tmp` and devices, system call filter). Monitoring needs no privileges, so this is enough for now. Actions that do need them (restarting services, package updates, Docker) will be added as explicit, opt-in grants rather than by running as root; see issue #46. Check the unit with `systemd-analyze security regulus`.

**Installing an agent on another server.** The same installer sets up the agent role. In the dashboard open **Servers → Add a server**, which shows the command with a one-time token and the hub's CA fingerprint, and run it on the new server (Ubuntu or Debian, as a user that can use `sudo`):

```sh
curl -fsSLO https://github.com/Stellatinus/Regulus/releases/download/vVERSION/install.sh && sh install.sh --version VERSION --role agent --hub HUB_HOST:8443 --token TOKEN --ca-fingerprint sha256:...
```

It verifies and installs the same package, enrolls the server as the `regulus` user (the token is sent only to a hub whose CA has that fingerprint), then enables and starts `regulus-agent.service`; the server shows as online within seconds. The hub must have the agent listener on (see [Remote agents](remote-agents.md)). `VERSION` is the version the hub runs: the dashboard fills it in, so the agent matches the hub even when only pre-releases exist. Keep the token off the command line with `REGULUS_AGENT_TOKEN=... sh install.sh --role agent ...`. If the hub revokes the agent, the service stops and is not restarted (exit status 3).

## Reaching the dashboard

A new installation serves the dashboard on **`127.0.0.1:8080`, this server only** (#197). The port speaks plain HTTP unless the hub has a certificate, and a password, a session cookie or an enrollment token should not cross the network in the clear because nobody changed a default. The installer prints the address, the setup token and the tunnel command below. Choose one of these:

- **SSH tunnel (nothing to change).** From your computer run `ssh -L 8080:127.0.0.1:8080 you@hub-server` and open `http://localhost:8080`. This is enough for the first admin (setup token) and for occasional use. When you add a server from the dashboard opened this way, the install command shows `localhost` as the hub: replace it with the name or address the new server uses to reach the hub (the dashboard says so under the command). The agents themselves never use this port; they connect to `agent_listen` (mutual TLS), see [Remote agents](remote-agents.md).
- **HTTPS from the hub itself.** Put a certificate and its key where the `regulus` user can read them, and set in `/etc/regulus/config.yaml`:

  ```yaml
  listen: ":8443"
  tls_cert: /etc/regulus/tls/cert.pem   # certificate followed by its chain
  tls_key: /etc/regulus/tls/key.pem     # root:regulus, mode 0640
  ```

  then `sudo systemctl restart regulus` and open `https://hub-server:8443`. A renewed certificate is picked up within a minute, without a restart (for example a certbot `--deploy-hook` that copies the files and fixes their owner and mode). Details in [Configuration](configuration.md).
- **A reverse proxy with TLS** (nginx, Caddy, Traefik) on the same machine: keep `listen: "127.0.0.1:8080"` and set `trusted_proxies: ["127.0.0.1"]` in `/etc/regulus/config.yaml`, so that the hub sees the real client address behind the proxy (the failed-login limit, the audit log and the session list use it; see [Authentication](authentication.md)). A proxy on another machine needs `listen` on an address it can reach, and its address in `trusted_proxies`; the hop between them is then plain HTTP.
- **Plain HTTP on every interface**, only on a network you trust: `sudo regulus config set listen 8080` (that is `:8080`) and restart the service. The hub logs a warning at every start while it serves plain HTTP beyond the loopback interface.

**Upgrading an installation from before this default.** Earlier packages listened on `:8080`, every interface. So that an upgrade does not take the dashboard off the network without warning, the package keeps that address when it was the one in use (the `listen: ":8080"` of the old packaged file, or no `listen` line at all): it adds `REGULUS_LISTEN=:8080`, with a comment, to `/etc/regulus/regulus.env` and says so during the upgrade. An address you chose yourself, or a `REGULUS_LISTEN` already in that file, is left alone. To move such a hub to one of the choices above, delete that line (the environment file wins over `config.yaml`), make the change and restart the service. The hub's log warns at every start while the old address serves plain HTTP. Agents are not affected.
