# Metrics for Prometheus

The hub can serve its own figures and the latest figures of each server in the [Prometheus text format](https://prometheus.io/docs/instrumenting/exposition_formats/) (version 0.0.4), so an existing Prometheus, VictoriaMetrics, Grafana Agent or similar can scrape it (#389). It is **off by default**.

## Turn it on

1. Make a token of at least 32 characters, for example `openssl rand -hex 32`.
2. Put it in `/etc/regulus/regulus.env` (mode `0640`, `root:regulus`): `REGULUS_PROMETHEUS_TOKEN=...`. The token is never read from the configuration file and never shown by `regulus config` or the console (it appears as `********`).
3. Set `prometheus.enabled: true` in the configuration file (or `REGULUS_PROMETHEUS_ENABLED=true`) and restart the hub. With the endpoint on and no token, or a token that is too short, the hub does not start.

The endpoint is `GET /metrics` on the dashboard address (`listen`). It is **not** behind the dashboard login, because a scraper cannot sign in: it answers only a request with `Authorization: Bearer <token>` and refuses every other with `401`. Off, the path does not exist (`404`).

```yaml
scrape_configs:
  - job_name: regulus
    metrics_path: /metrics
    authorization:
      credentials_file: /etc/prometheus/regulus.token   # a file that holds the token
    static_configs:
      - targets: ["hub.example:8080"]
```

**Do not expose the port.** The default `listen` is loopback only: scrape from the same machine, over an SSH tunnel, or through a reverse proxy that you control. With the dashboard behind a reverse proxy, scraping through it works as for any other path; if you do not want `/metrics` reachable from the internet, block it at the proxy for outside clients and let Prometheus reach it directly or from an internal address. The token travels in a header: use HTTPS (`tls_cert`, `tls_key`, or the proxy's) once the scrape crosses a network.

## Metrics of the hub

| Metric | Type | Meaning |
|---|---|---|
| `regulus_build_info{version}` | gauge | always 1; the version of the hub |
| `regulus_agents_connected` | gauge | agents connected now (the built-in agent included) |
| `regulus_agents_enrolled` | gauge | agents enrolled, revoked ones not counted |
| `regulus_agents_revoked` | gauge | agents that were revoked |
| `regulus_stream_clients` | gauge | dashboard streams (WebSockets) open now |
| `regulus_reports_received_total` | counter | reports accepted from agents (use `rate()` for the report rate) |
| `regulus_reports_rejected_total` | counter | reports dropped because the agent sent them faster than the interval |
| `regulus_database_up` | gauge | 1 while the database answers |
| `regulus_database_read_seconds` | gauge | duration of the last database check (a ping, every 15 s); absent until measured |
| `regulus_database_write_seconds` | gauge | duration of the last write of agent presence (about once a minute per connected agent); absent until a write happened |
| `regulus_update_available`, `regulus_update_staged`, `regulus_update_check_stale` | gauge | a newer release is known; a verified release waits in the cache; the last successful check is too old |
| `regulus_rollout_state{state}` | gauge | 1 for the state of the last update of the servers (`idle`, `running`, `done`, `failed`, `stopped`), 0 for the others |
| `regulus_rollout_candidates` | gauge | connected servers that run an older version than the hub |
| `regulus_certificate_days_left{kind}` | gauge | days until a certificate ends, negative after: `ca`, `listener` (only while remote agents are on) and `agent` (the agent whose certificate ends first) |
| `regulus_certificate_agents_unknown` | gauge | agents whose certificate end the hub does not know yet |
| `regulus_certificate_legacy_agents` | gauge | connected agents on a certificate without a kind (#322) |

The counters live as long as the hub process: they start from 0 at every restart, which Prometheus handles as a counter reset.

## Metrics of each server

From the latest report of each **connected** server, with the labels `id` (never changes) and `server` (the display name, else the host name, else the id). A server that is not connected has no series, so its series end when it goes away. A part the agent could not read (`unavailable` in its report, for example `cpu` in a container without `/proc/stat`) is left out, not exported as 0.

| Metric | Extra labels | Meaning |
|---|---|---|
| `regulus_server_up` | | 1 connected, 0 connected but silent (stale) |
| `regulus_server_cpu_usage_percent` | | CPU use, 0 to 100 across all cores |
| `regulus_server_load1`, `_load5`, `_load15` | | load averages |
| `regulus_server_memory_total_bytes`, `_used_bytes`, `_available_bytes` | | memory |
| `regulus_server_swap_total_bytes`, `_used_bytes` | | swap |
| `regulus_server_uptime_seconds` | | time since boot |
| `regulus_server_disk_total_bytes`, `_used_bytes` | `mount`, `filesystem` | one pair per mounted filesystem |
| `regulus_server_network_receive_bytes_total`, `_transmit_bytes_total` | `interface` | counters since boot |
| `regulus_server_network_receive_bytes_per_second`, `_transmit_bytes_per_second` | `interface` | rate between the last two reports, as in the dashboard; absent for the first report and after a counter reset |
| `regulus_server_series_cut` | | disks and interfaces of the last report that were left out (see below) |

### Cardinality

Every label combination is a series. To keep a server with many mounts or virtual interfaces (containers, snapshots) from multiplying the series of the whole fleet, the hub exports at most **32 filesystems and 16 interfaces per server**: the first ones of the report, in the order the agent sent them. `regulus_server_series_cut` says how many were left out, so a server that goes over the limit can be seen. With the limits, a server adds at most about 100 series. The labels `mount`, `filesystem` and `interface` come from the agent: a mount point that changes often makes a series that comes and goes.

## What it does not do

- It is not a history: the hub keeps no more than the last hour in memory, and Prometheus keeps what it scrapes.
- Plugin metrics are not exported.
- There is no per-user or per-token scrape permission: one shared token. A scoped API token could replace it later (decision open in #394).

See also [Configuration](configuration.md), [HTTP API](http-api.md) and the choices in [Design decisions](decisions.md#metrics-for-prometheus-389).
