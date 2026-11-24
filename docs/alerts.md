# Alerts

**Status: design (#386), not implemented.** This page is the agreed design; the implementation issues listed at the end build it. Nothing here exists in the hub yet. Today Regulus shows the state of every server, but nobody is told when something goes wrong: the dashboard has to be open and looked at. The certificate warning of #245 is a log line and a field of the API, not a message to a person.

## What an alert is

An **alert** is a record on the hub that says one **condition** is true for one **subject** (a server, the hub, a certificate). It has a **state**, and **notifications** go to **channels** when the state changes. Rules are evaluated **on the hub, from the data it already has** (the registry, the metrics history, the certificate dates, the update state): no new data is collected for alerts, and nothing runs on the servers.

| Term | Meaning |
|---|---|
| Condition | A kind of problem the hub can see, with a threshold and a duration (table below). |
| Rule | A condition with its settings (on or off, threshold, duration, severity). One rule per condition at first; per-server overrides later. |
| Alert | The record of a rule being true for a subject: `open`, `acknowledged`, `resolved`. |
| Channel | Where a message goes: the dashboard list (always), email (SMTP) or a webhook. |

## Conditions and defaults

Defaults are **quiet**: a condition fires only after it has held for a while, and a threshold is set where a person should act, not where something is merely busy. Every number is a setting of the rule.

| Condition | Fires when | Default | Severity | Data it reads |
|---|---|---|---|---|
| `agent_offline` | an enrolled agent has no connection | 5 minutes | critical | registry presence (`last_seen`) |
| `disk_full` | a mounted file system is fuller than the threshold | 90% for 10 minutes; 97% is critical | warning, critical | the disks of the latest report |
| `memory_high` | memory in use (without cache) is above the threshold | off by default | warning | the metrics history |
| `cpu_high` | CPU is above the threshold | off by default | warning | the metrics history |
| `certificate_ending` | the CA, the listener or an agent certificate ends within the period | 30 days; 7 days is critical | warning, critical | the dates of #245 |
| `update_failed` | a rollout failed for a server, or an update was rolled back | at once | warning | rollout state and helper result |
| `hub_lock_lost` | this hub lost the active-hub lock to another | at once | critical | the PostgreSQL lock (#217) |

CPU and memory are off by default because a busy server is not a broken one; an administrator who wants them turns them on with a threshold and a period (for example 95% for 30 minutes). A server that is offline raises only `agent_offline`: the other conditions of a server that is not reporting are not evaluated, so one outage is one alert.

## State and no repeat storm

```
          condition true for the whole duration
   (none) ───────────────────────────────────────► open ──── acknowledge ───► acknowledged
                                                     │                             │
                          condition false for        │      condition false        │
                          the whole recovery time    ▼      for the recovery time  ▼
                                                  resolved ◄───────────────────────┘
```

- **One message per change of state**: when an alert opens, when its severity goes up, and when it resolves. A flapping condition cannot send a message per sample: it opens only after its duration, and it resolves only after it has been false for a **recovery time** (default the same as the duration, at least one minute).
- An **acknowledged** alert stays on the list and sends nothing more until it resolves; acknowledging records who and when.
- An optional **reminder** (off by default; hours) repeats a message for an alert that is open and not acknowledged.
- When the hub starts it does not announce again the alerts that were open before it stopped; their state is in the database. An alert is not resolved only because the hub restarted: after a restart every condition needs a fresh evaluation window of one report before it can resolve.
- A server that is revoked or removed resolves its alerts with the reason `subject_gone`.

## Where messages go

- **The dashboard** always shows the list of open and acknowledged alerts (a badge in the header, a page **Alerts**). This needs no setup.
- **Email (SMTP)** with a host, a port, STARTTLS or TLS, a user and a password, a sender and a list of recipients. The hub sends plain text.
- **A webhook**: an HTTPS URL that receives a JSON `POST` for each change of state, with a `HMAC-SHA256` signature header made with a per-channel secret, so the receiver can check who sent it. The URL is a secret (it often carries a token) and is stored like one. A webhook is retried a few times with a delay and then gives up with a log line and an `alert_delivery_failed` audit event; delivery never blocks the evaluation.
- **Shared delivery code.** Email and webhooks are written once, in one package (`internal/notify`), because #112 (the out-of-band channel for failover) and #157 (Regulus Access notifications) need the same two transports. Alerts are the first user. What differs is *which events* a channel receives: a channel lists the kinds of event it takes (`alerts`, and later `failover`, `access`), so an administrator can send alerts to a mailing list and failover messages only to a phone.

## While the hub is down

Alerts are evaluated by the hub, so a hub that is down raises none: that is exactly the case of #112 (out-of-band channel and manual override), which belongs to the redundant hub design and is not solved here. What alerts do cover: a redundant hub that takes over (#103) evaluates the same rules from the same database, so a failover loses at most the time of the takeover; and `hub_lock_lost` tells the administrator that the active hub changed. For a single hub, the advice in this page is an external check of `/healthz` (any uptime monitor) or a Prometheus alert on `up` (see [Metrics for Prometheus](metrics.md)).

## Permissions and audit

- `alerts.read`: see the alerts and the rules (viewer, operator and admin by default).
- `alerts.acknowledge`: acknowledge an alert (operator and admin).
- `alerts.manage`: change rules and channels, send a test message (admin only). Changing a channel's address or secret needs step-up (see [Authentication](authentication.md)), because it decides where information about the servers is sent.

Audit events: `alert_opened`, `alert_resolved` (actor `system`), `alert_acknowledged`, `alert_rule_changed`, `alert_channel_changed`, `alert_delivery_failed`. Each carries a code and arguments, so the dashboard shows it in the language of the page (see [Design decisions](decisions.md)).

## Secrets

The SMTP password, the webhook URL and the webhook signing secret are **not** in `config.yaml` or the environment file: an administrator sets them in the dashboard, and the hub keeps them in the database **encrypted with the same key as the two-factor secrets** (`secret.key`). The API never returns them: it returns `set: true` (and a `********` placeholder in the form), like the database password. A backup (#253) includes `secret.key`, so the channels survive a restore.

## Texts

Every text of the alerts feature is in English and Italian from the first version: the condition names and descriptions, the dashboard page, the audit events and the messages the hub sends. A message is built from a code and arguments, so an email can be written in the language chosen for the channel (a setting of the channel, default the hub's language).

## What is not in the first version

Per-server and per-group rules, maintenance windows, escalation chains, chat integrations other than a webhook, and alerts on the logs. Trend alerts (disk full in N days) need the persistent metrics history of #387.

## Implementation issues

1. Rules, alert state and storage: the tables, the evaluator on the hub, the defaults above, with unit tests on a fake clock (M4).
2. Delivery: `internal/notify` with SMTP and signed webhooks, retries, the secrets in the database (M4).
3. Dashboard: the **Alerts** page, the header badge, the rule and channel forms, in English and Italian (M4).
4. API, permissions, audit events and `docs/http-api.md` (M4).
5. `[Test]`: email through a real mail server and a webhook receiver, a server that goes offline and comes back, a flapping disk (M4).
