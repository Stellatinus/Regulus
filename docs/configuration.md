# Configuration

The hub reads, in increasing priority: defaults, a YAML file (`--config` or `REGULUS_CONFIG`), environment variables, flags.

| Setting (YAML) | Flag | Environment | Default |
|---|---|---|---|
| `listen` | `--listen` | `REGULUS_LISTEN` | `127.0.0.1:8080` (this machine only; see below) |
| `tls_cert`, `tls_key` (PEM files) | `--tls-cert`, `--tls-key` | `REGULUS_TLS_CERT`, `REGULUS_TLS_KEY` | empty: plain HTTP |
| `trusted_proxies` (a list of addresses or CIDR blocks) | - | `REGULUS_TRUSTED_PROXIES` (comma separated) | empty: forwarding headers are ignored |
| `data_dir` | `--data-dir` | `REGULUS_DATA_DIR` | `data` |
| `builtin_agent` (`true`, `false`) | `--builtin-agent` | `REGULUS_BUILTIN_AGENT` | `true` |
| `metrics_interval` (for example `10s`, minimum `1s`) | `--metrics-interval` | `REGULUS_METRICS_INTERVAL` | `10s` |
| `metrics_budget` (a size such as `256MiB`, minimum `4MiB`) | - | `REGULUS_METRICS_BUDGET` | `256MiB` |
| `metrics_retention_1m`, `metrics_retention_15m` (days such as `3d`, or hours; `0` turns a tier off) | - | `REGULUS_METRICS_RETENTION_1M`, `REGULUS_METRICS_RETENTION_15M` | `3d`, `90d` |
| `plugins_dir` | `--plugins-dir` | `REGULUS_PLUGINS_DIR` | empty: the hub runs no plugin |
| `log.level` (`debug`, `info`, `warn`, `error`) | `--log-level` | `REGULUS_LOG_LEVEL` | `info` |
| `log.format` (`text`, `json`) | `--log-format` | `REGULUS_LOG_FORMAT` | `text` |
| `updates.check` (`true`, `false`) | - | `REGULUS_UPDATES_CHECK` | `true` |
| `updates.apply` (`true`, `false`) | - | `REGULUS_UPDATES_APPLY` | `false` (the installer sets it) |
| `prometheus.enabled` (`true`, `false`) | - | `REGULUS_PROMETHEUS_ENABLED` | `false` |
| `prometheus.token` (at least 32 characters) | - | `REGULUS_PROMETHEUS_TOKEN` (only; never in the file) | empty |
| `console.tui` (`true`, `false`) | `--tui`, `--no-tui` (console commands only) | `REGULUS_NO_TUI=1` (the opposite) | `true` |

`listen` is the address of the dashboard and the API. The default, `127.0.0.1:8080`, answers on the loopback interface only (#197): the port speaks plain HTTP unless `tls_cert` and `tls_key` are set, and a login, a session cookie or an enrollment token must not cross a network in the clear by default. A port alone (`:8080`) or `0.0.0.0:8080` listens on every interface; the hub then logs a warning at every start unless TLS is on. How to reach the dashboard from other machines (an SSH tunnel, a reverse proxy, or TLS here) is in [Installation](installation.md#reaching-the-dashboard). The agent port (`agent_listen`) is not affected: it is always mutual TLS.

`tls_cert` and `tls_key` make the dashboard port serve **HTTPS only** (TLS 1.2 or later, HTTP/1.1) with that certificate and key; set both or neither, or the hub refuses to start. The certificate file holds the server certificate followed by its chain. The key must be readable by the hub's user (`regulus`), for example `/etc/regulus/tls/key.pem` owned by `root:regulus` with mode `0640`; the service unit can read `/etc` but not write it. The files are looked at again at most once a minute, and a renewed pair is used without a restart; a pair that cannot be loaded keeps the previous certificate and logs a warning. The session cookie is then `Secure` and HSTS is sent. They are a pair of paths, so they are set in the file or the environment, not with `regulus config set` (like `trusted_proxies`).

`metrics_budget` bounds the memory of the hub's in-memory metrics history for all agents together (#333). A typical report costs about 1.3 KB, so the default keeps a full hour at the 10 s interval for about 500 agents; when the estimate goes over the budget the oldest samples of any agent are dropped first, and every agent keeps at least its newest sample. Raise it for a large fleet, lower it on a small VPS. Units are binary (`MB` means MiB). It is read at start.

`metrics_retention_1m` and `metrics_retention_15m` are how long the hub keeps the **stored** metrics history (#405, [Architecture](architecture.md#persistent-metrics-history)): one value a minute for 3 days and one every 15 minutes for 90 days by default, written to the database, so that a restart of the hub does not lose them. A value is days (`3d`) or a duration (`36h`); `0` turns the tier off, and rows an earlier setting left are then deleted by the hourly pruning. While the 15-minute tier is on, the 1-minute rows are kept for at least one hour whatever their retention, because a 15-minute bucket is made from them. Both are read at start. About 100 bytes a row: ten servers use about 13 MB with the defaults (see [Database](database.md)).

`prometheus.enabled` turns on `GET /metrics` (#389, [Metrics for Prometheus](metrics.md)). It needs `prometheus.token`, a secret of at least 32 characters that Prometheus sends as a bearer token; like the database password it is read only from the environment (`REGULUS_PROMETHEUS_TOKEN` in `/etc/regulus/regulus.env`) and never shown by `regulus config` or the console, and the hub refuses to start with the endpoint on and no token. Both are read at start.

With `builtin_agent` enabled (the default) the hub runs an agent for its own server, which connects through the same contract as any other agent (agent id `local`).

`plugins_dir` names the directory of the plugins the hub runs, each as its own process with the role `hub` (see [Plugin runtime](plugins/runtime.md)). It is read at start: restart the hub after changing it or the plugins in it. A missing directory means no plugins. It is also where the Store installs plugins (through the root helper `regulus plugin-apply`, which must own the directory: [The plugin store](plugins/store.md)); the installer sets it to `/usr/local/lib/regulus/plugins` on a hub that installs its own updates, and an installation made earlier needs the line added. If you use another directory, change the helper's unit with a drop-in (`ExecStart=` with `--plugins-dir`, and `ReadWritePaths=`). Whatever is in it runs as the hub's user, so keep it owned by root and not writable by `regulus`. A plugin never sees the hub's environment (the database password included). The state of the plugins is shown by `regulus status`, the console and `GET /api/plugins`. Agents have their own setting (`plugins_dir` in `agent.yaml`, `REGULUS_AGENT_PLUGINS_DIR`, see [Remote agents](remote-agents.md)).

`plugin_sources` (a list in the configuration file only) adds sources to the plugin store besides the official one (regulus-plugins), which is built in with its signing identity. Each entry has `name`, `url` (the address of its `index.json`; its Sigstore bundle must be at the same address plus `.sigstore.json`), `trust: custom`, `signer` (the identity, the certificate's subject, that must have signed the index) and optionally `issuer` (default GitHub Actions) and `channel` (default: the hub's). An entry with no `signer` is refused when the hub starts: an index nobody vouches for is never offered. **Adding a source is an act of trust**: whoever signs its index decides what can be installed on your servers ([The plugin store](plugins/store.md)). Reading sources does not install anything.

The hub stores its data in a SQLite database (`regulus.db`) inside `data_dir`, or in a PostgreSQL database when `database.type` is `postgres`, and applies schema migrations automatically at startup. The `database.*` settings (host, port, name, user, `sslmode`, `sslrootcert`, `schema`, and the password, which comes **only** from `REGULUS_DATABASE_PASSWORD` in `/etc/regulus/regulus.env`) are described in [Database](database.md); `regulus db check` tests them.
