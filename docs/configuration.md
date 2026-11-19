# Configuration

The hub reads, in increasing priority: defaults, a YAML file (`--config` or `REGULUS_CONFIG`), environment variables, flags.

| Setting (YAML) | Flag | Environment | Default |
|---|---|---|---|
| `listen` | `--listen` | `REGULUS_LISTEN` | `:8080` |
| `trusted_proxies` (a list of addresses or CIDR blocks) | - | `REGULUS_TRUSTED_PROXIES` (comma separated) | empty: forwarding headers are ignored |
| `data_dir` | `--data-dir` | `REGULUS_DATA_DIR` | `data` |
| `builtin_agent` (`true`, `false`) | `--builtin-agent` | `REGULUS_BUILTIN_AGENT` | `true` |
| `metrics_interval` (for example `10s`, minimum `1s`) | `--metrics-interval` | `REGULUS_METRICS_INTERVAL` | `10s` |
| `plugins_dir` | `--plugins-dir` | `REGULUS_PLUGINS_DIR` | empty: the hub runs no plugin |
| `log.level` (`debug`, `info`, `warn`, `error`) | `--log-level` | `REGULUS_LOG_LEVEL` | `info` |
| `log.format` (`text`, `json`) | `--log-format` | `REGULUS_LOG_FORMAT` | `text` |
| `updates.check` (`true`, `false`) | - | `REGULUS_UPDATES_CHECK` | `true` |
| `updates.apply` (`true`, `false`) | - | `REGULUS_UPDATES_APPLY` | `false` (the installer sets it) |
| `console.tui` (`true`, `false`) | `--tui`, `--no-tui` (console commands only) | `REGULUS_NO_TUI=1` (the opposite) | `true` |

With `builtin_agent` enabled (the default) the hub runs an agent for its own server, which connects through the same contract as any other agent (agent id `local`).

`plugins_dir` names the directory of the plugins the hub runs, each as its own process with the role `hub` (see [Plugin runtime](plugins/runtime.md)). It is read at start: restart the hub after changing it or the plugins in it. A missing directory means no plugins. Whatever is in it runs as the hub's user, so keep it owned by root and not writable by `regulus`. A plugin never sees the hub's environment (the database password included). The state of the plugins is shown by `regulus status`, the console and `GET /api/plugins`. Agents have their own setting (`plugins_dir` in `agent.yaml`, `REGULUS_AGENT_PLUGINS_DIR`, see [Remote agents](remote-agents.md)).

The hub stores its data in a SQLite database (`regulus.db`) inside `data_dir`, or in a PostgreSQL database when `database.type` is `postgres`, and applies schema migrations automatically at startup. The `database.*` settings (host, port, name, user, `sslmode`, `sslrootcert`, `schema`, and the password, which comes **only** from `REGULUS_DATABASE_PASSWORD` in `/etc/regulus/regulus.env`) are described in [Database](database.md); `regulus db check` tests them.
