// Package alerts decides when something that the hub can see is a problem
// somebody should hear about (docs/alerts.md, #386, #404). It holds the rules
// (one per condition, with the quiet defaults of the design), evaluates them
// against a Snapshot of what the hub knows, keeps the alerts in the database
// (open, acknowledged, resolved) and tells a Notifier once for each change of
// state: when an alert opens, when its severity goes up, when it resolves, and
// for a reminder when one is asked for.
//
// It does not collect anything: the hub builds the Snapshot from the registry,
// the latest reports, the certificate dates, the rollout and the active-hub
// lock, and runs Evaluate on a timer. It does not deliver: the transports
// (email, webhook) are internal/notify, a later step, and until then the hub
// gives it a Notifier that only logs. It has no HTTP, no permissions and no
// audit events: those are the API step of the design, on top of Engine.List,
// Engine.Acknowledge and Engine.Rules.
package alerts
