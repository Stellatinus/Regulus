package console

import "strings"

// Every word the console shows that is not data lives here, so that a
// translation replaces one value and touches no other file.
type strs struct {
	Title string

	TabStatus, TabSettings, TabLogs, TabHealth, TabActions string

	Running, Stopped string
	Service          string

	Hub, Version, Dashboard, AlsoOn, AgentPort, BuiltinAgent, DataDir, Database, ConfigFile, Uptime string
	On, Off, None                                                                                   string
	Agents, NoAgents                                                                                string
	SetupTitle, SetupText, SetupCLI                                                                 string

	SettingHeader  [3]string
	Default        string
	AppliesNow     string
	AppliesRestart string
	EnvOverride    string
	// AgentListenDashboard is shown under agent_listen when its value comes
	// from the dashboard (#423).
	AgentListenDashboard string
	Editing              string
	Valid                string
	Choices              string

	LogsEmpty, LogsUnavailable string
	LogsNoJournal              string
	LogFilters                 [4]string
	Following                  string

	Loading string

	// The help line of the main screen is HelpMain, then HelpReset when the
	// reset action is there, then HelpQuit.
	HelpMain, HelpReset, HelpQuit string
	HelpEdit, HelpConfirm         string
	// TabLabel is a tab: %d its number (the key that opens it), %s its name.
	TabLabel string

	RestartAsk, ResetAsk string
	RestartHint          string

	ActionsIntro                         string
	ActRestart, ActRestartInfo           string
	ActReset, ActResetInfo               string
	ActAgents, ActAgentsInfo             string
	ActAgentsOff, ActAgentsOffInfo       string
	ActUnavailable                       string
	AgentsAsk                            string
	AgentsOffAsk                         string
	NewAgentTitle, NewAgentText          string
	NewAgentAddress, NewAgentFingerprint string
	NewAgentToken                        string

	// Messages shown after an action. Most are fmt templates: the verbs are
	// documented where they are used (internal/console/tui.go).
	MsgAgentsFailed, MsgAgentsSaved, MsgAgentsOn string
	MsgAgentsOffFailed, MsgAgentsOffSaved        string
	MsgAgentsOff, MsgTurningOff                  string
	MsgRestartFailed, MsgRestartUnavailable      string
	MsgRestarting, MsgRestarted                  string
	MsgLinkFailed, MsgLinkReady, MsgMakingLink   string
	MsgTurningOn, MsgSaved, ResetLinkTitle       string

	// Small words of the status and settings screens and of the text output.
	Problem       string // a configuration that cannot be loaded
	Empty         string // an empty setting value
	RunningUnsure string // the service is active but the hub does not answer
	Ago           string // %s: a duration
	// Durations: %d days, hours, minutes and seconds, largest unit first.
	DurDays, DurHours, DurMinutes, DurSeconds string
	// Where the value of a setting comes from (config.Origin*).
	OriginDefault, OriginFile, OriginEnv, OriginDashboard string
	// States are the words for the systemd state of the service and the
	// state of an agent, by the value systemd or the hub reports. A value
	// that is missing here is shown as it is.
	States map[string]string
	// SettingInfo describes the settings, by key. English has none: the
	// descriptions in internal/config are the English text, and a key missing
	// here falls back to them.
	SettingInfo map[string]string

	// Why an action of the Actions tab cannot run here.
	WhyRestart         string // %s: the systemd unit
	WhyReset           string
	WhyNoConfig        string
	WhyEnvAgents       string // %s: the environment variable
	WhyDashboardAgents string
	NoConfigToChange   string
	FingerprintLater   string

	// Health checks: the status words of the text output, the names of the
	// checks and their details (the %s are documented where they are used,
	// in internal/console/snapshot.go). --plain and --json always use English.
	StatusOK, StatusWarn, StatusFail, StatusInfo                               string
	ChkConfig, ChkHub, ChkDashboardPort, ChkAgentPort, ChkDataDir, ChkDatabase string
	CfgMissing, CfgValid                                                       string
	HubAnswers, HubNoPermission, HubSilent                                     string
	PortServed, PortInUse, PortPrivileged, PortError, PortFree                 string
	DataMissing, DataNotWritable, DataWritable                                 string
	DBProblem, DBAnswers, DBLater, DBFile, DBNone                              string

	// The view of a server that runs only the agent (#434), in a block of its
	// own. AgentLink* say how the agent stands with the hub, read from its log.
	AgentTitle, AgentRole, AgentLink, AgentID, AgentHost, AgentCert string
	AgentLinkConnected, AgentLinkRetrying, AgentLinkRejected        string
	AgentLinkUnknown, AgentLinkStopped, AgentNotEnrolled            string
	AgentHintRejected, AgentHintNotEnrolled, AgentHintLog           string
	AgentHelp, AgentRestartAsk                                      string
	AgentMsgRestarting, AgentMsgRestarted, AgentMsgFailed           string

	// Certificate end dates (#245), kept in a block of their own. CertAgent
	// takes the agent; CertEnds takes the certificate, the date and the days
	// left; CertEnded the certificate and the date.
	Certificates, CertNoneSoon      string
	CertCA, CertListener, CertAgent string
	CertEnds, CertEnded             string
	CertRenewAgent, CertRenewCA     string

	// --- Plugins of the hub, on the Status tab (#262). Kept together so
	// that other changes to this table do not touch these lines.
	Plugins, NoPlugins, PluginRestarts              string
	PluginStarting, PluginRunning, PluginRestarting string
	PluginFailed, PluginIncompatible, PluginStopped string
	// --- end of the plugins block.

	// --- Plugins in the Health tab and the invalid state (#319). PlgOK
	// takes the number of plugins; PlgProblem why none runs; PlgBad the
	// plugins that need a look, each "id (state)".
	PluginInvalid, ChkPlugins, PlgOK, PlgProblem, PlgBad string
	// --- end of the plugin health block.

	// --- Pending updates in the Health tab (#71). UpdPending takes the
	// version of the newer release.
	ChkUpdates, UpdPending, UpdNone string
	// --- end of the updates health block.

	// --- Reachability from the network in the Health tab (#369).
	// LANLocalOnly takes the listen address and the setting to use;
	// LANTry, LANLater and LANOK a dashboard URL; LANFail the addresses that
	// did not answer.
	ChkLAN                                                    string
	LANLocalOnly, LANTry, LANNoAddr, LANLater, LANOK, LANFail string
	// --- end of the network reachability block.

	// --- Setting errors (#318): why config.Normalize or config.SetFile
	// refused a value, by config.ErrorCode. Each %s takes the arguments of
	// config.SettingError.Args, in order (documented in internal/config).
	SettingErrors map[string]string
	// --- end of the setting errors block.
}

var en = strs{
	Title: "Regulus",

	TabStatus:   "Status",
	TabSettings: "Settings",
	TabLogs:     "Logs",
	TabHealth:   "Health",
	TabActions:  "Actions",

	Running: "running",
	Stopped: "not running",
	Service: "service",

	Hub:          "Hub",
	Version:      "Version",
	Dashboard:    "Dashboard",
	AlsoOn:       "also on",
	AgentPort:    "Agent port",
	BuiltinAgent: "Built-in agent",
	DataDir:      "Data directory",
	Database:     "Database",
	ConfigFile:   "Configuration",
	Uptime:       "up",
	On:           "on",
	Off:          "off",
	None:         "none",
	Agents:       "Connected agents",
	NoAgents:     "No agent is connected.",
	SetupTitle:   "First run",
	SetupText:    "No administrator exists yet. Open the dashboard and enter this one-time setup token:",
	SetupCLI:     "No browser? Create the administrator here: sudo -u regulus regulus hub create-admin -h",

	SettingHeader:        [3]string{"Setting", "Value", "Source"},
	Default:              "default",
	AppliesNow:           "Applies as soon as it is saved.",
	AppliesRestart:       "The hub reads it at start: restart it to apply.",
	EnvOverride:          "An environment variable overrides the file: saving here will not change what the hub uses.",
	AgentListenDashboard: "Set from the dashboard (kept in the database). A value in the file or environment wins over it.",
	Editing:              "Editing",
	Valid:                "will be saved as",
	Choices:              "choices",

	LogsEmpty:       "No log lines.",
	LogsUnavailable: "Cannot read the log",
	LogsNoJournal:   "journald is not available on this machine. The hub writes its log to standard error: read it where whatever started it keeps it (docker logs, the terminal of a hub started by hand, the supervisor of your init system).",
	LogFilters:      [4]string{"all levels", "info and above", "warnings and errors", "errors only"},
	Following:       "following",

	Loading: "Loading…",

	HelpMain:    "tab/←/→ section  ↑/↓ move  enter edit  r restart",
	HelpReset:   "p reset admin password",
	HelpQuit:    "q quit",
	HelpEdit:    "enter save  esc cancel  tab next choice",
	HelpConfirm: "y yes  n no",
	TabLabel:    "%d %s",

	RestartAsk:  "Restart the regulus service now?",
	ResetAsk:    "Make a one-time link to choose a new administrator password?",
	RestartHint: "Restart the hub to apply: press r, or run: sudo systemctl restart regulus",

	ActionsIntro:        "Press the key of an action, or select it with ↑/↓ and press enter. Each asks before doing anything.",
	ActRestart:          "Restart the service",
	ActRestartInfo:      "Restarts regulus.service, to apply settings read at start. Needs root or sudo without a password.",
	ActReset:            "Reset the administrator password",
	ActResetInfo:        "Makes a one-time link to choose a new password, for when nobody can sign in. Needs root or the hub's user.",
	ActAgents:           "Turn on remote agents",
	ActAgentsInfo:       "Sets agent_listen to :8443 and restarts the hub, so other servers can connect. Needs root.",
	ActAgentsOff:        "Turn off remote agents",
	ActAgentsOffInfo:    "Empties agent_listen and restarts the hub: the remote servers disconnect and stay enrolled. Needs root.",
	ActUnavailable:      "not available",
	AgentsAsk:           "Turn on remote agents on port 8443 and restart the hub?",
	AgentsOffAsk:        "Turn off remote agents and restart the hub? The remote servers disconnect until they are turned on again.",
	NewAgentTitle:       "Adding a server",
	NewAgentText:        "Remote agents are on. On the new server, the installer needs:",
	NewAgentAddress:     "Hub address",
	NewAgentFingerprint: "CA fingerprint",
	NewAgentToken:       "Token: make it in the dashboard, Servers → Add a server (it shows the whole command).",

	MsgAgentsFailed:       "Could not turn on remote agents: %s. Open the console with sudo, or set agent_listen by hand.",
	MsgAgentsSaved:        "Saved agent_listen = %s.",
	MsgAgentsOn:           "Remote agents are on, port 8443. Open it in the firewall towards the servers you manage.",
	MsgRestartFailed:      "Could not restart: %s. Run: sudo systemctl restart %s",
	MsgRestartUnavailable: "Restarting is not available here. Run: sudo systemctl restart %s",
	MsgRestarting:         "Restarting…",
	MsgRestarted:          "The service was restarted.",
	MsgLinkFailed:         "Could not make the link: %s",
	MsgLinkReady:          "Open the link below to choose a new password. It works once.",
	MsgMakingLink:         "Making the link…",
	MsgTurningOn:          "Turning on remote agents…",
	MsgTurningOff:         "Turning off remote agents…",
	MsgAgentsOff:          "Remote agents are off.",
	MsgAgentsOffSaved:     "Saved: agent_listen is empty.",
	MsgAgentsOffFailed:    "Could not turn off remote agents: %s. Open the console with sudo, or set agent_listen by hand.",
	MsgSaved:              "Saved %s = %s.",
	ResetLinkTitle:        "Password reset link",

	Problem:         "Problem",
	Empty:           "(empty)",
	RunningUnsure:   "running?",
	Ago:             "%s ago",
	DurDays:         "%dd %dh %dm",
	DurHours:        "%dh %dm",
	DurMinutes:      "%dm %ds",
	DurSeconds:      "%ds",
	OriginDefault:   "default",
	OriginFile:      "file",
	OriginEnv:       "environment",
	OriginDashboard: "dashboard",
	States: map[string]string{
		"active": "active", "inactive": "inactive", "failed": "failed", "unknown": "unknown",
		"activating": "starting", "deactivating": "stopping", "reloading": "reloading",
		"connected": "connected", "stale": "stale", "offline": "offline", "revoked": "revoked",
	},

	WhyRestart:         "run: sudo systemctl restart %s",
	WhyReset:           "run the console as root, or run: sudo regulus hub reset-admin",
	WhyNoConfig:        "there is no configuration file: start the console with --config FILE",
	WhyEnvAgents:       "%s overrides the configuration file",
	WhyDashboardAgents: "they were turned on from the dashboard, which keeps the address in the hub's database: they cannot be turned off from here yet",
	NoConfigToChange:   "there is no configuration file to change: start the console with --config FILE",
	FingerprintLater:   "shown while the hub runs",

	StatusOK:   "ok",
	StatusWarn: "warn",
	StatusFail: "fail",
	StatusInfo: "info",

	ChkConfig:        "Configuration",
	ChkHub:           "Hub",
	ChkDashboardPort: "Dashboard port",
	ChkAgentPort:     "Agent port",
	ChkDataDir:       "Data directory",
	ChkDatabase:      "Database",

	CfgMissing:      "no configuration file: showing the defaults; pass --config FILE to read and change one",
	CfgValid:        "%s is valid",
	HubAnswers:      "answers on its local socket",
	HubNoPermission: "cannot read its local socket: run as the hub's user (for example sudo -u regulus regulus)",
	HubSilent:       "the service is active but does not answer on %s",
	PortServed:      "%s is served by the hub",
	PortInUse:       "%s is in use by another program",
	PortPrivileged:  "%s needs more privileges than yours (ports below 1024)",
	PortError:       "%s: %s",
	PortFree:        "%s is free",
	DataMissing:     "%s does not exist yet (created at the first start)",
	DataNotWritable: "you cannot write to %s (fine if the hub runs as another user)",
	DataWritable:    "%s is writable",
	DBProblem:       "%s: %s",
	DBAnswers:       "%s answers",
	DBLater:         "%s (checked while the hub runs; `regulus db check` tries it now)",
	DBFile:          "%s, %s",
	DBNone:          "none yet",

	// The agent view (#434).
	AgentTitle:           "Regulus agent",
	AgentRole:            "Role",
	AgentLink:            "Link to the hub",
	AgentID:              "Agent id",
	AgentHost:            "Host name",
	AgentCert:            "Certificate",
	AgentLinkConnected:   "connected",
	AgentLinkRetrying:    "not connected, trying again",
	AgentLinkRejected:    "the hub does not accept this agent (revoked or removed)",
	AgentLinkUnknown:     "not known (no connection line in the recent log)",
	AgentLinkStopped:     "the agent is not running",
	AgentNotEnrolled:     "not enrolled",
	AgentHintRejected:    "To use this server again: remove the agent data directory and enroll it with a new token (docs/remote-agents.md).",
	AgentHintNotEnrolled: "Make a token in the hub's dashboard (Servers, Add a server) and run its install command on this server.",
	AgentHintLog:         "The link is read from the agent's log; the hub's own list of servers is the authority.",
	AgentHelp:            "tab/←/→ section  r restart the agent  q quit",
	AgentRestartAsk:      "Restart the regulus-agent service now?",
	AgentMsgRestarting:   "Restarting the agent…",
	AgentMsgRestarted:    "The agent service was restarted.",
	AgentMsgFailed:       "Could not restart: %s. Run: sudo systemctl restart regulus-agent",

	// Certificate end dates (#245).
	Certificates:   "Certificates",
	CertNoneSoon:   "none ends within 90 days; the first to end:",
	CertCA:         "CA certificate",
	CertListener:   "agent listener certificate (renewed by itself)",
	CertAgent:      "certificate of agent %s",
	CertEnds:       "%s: ends on %s, in %s days",
	CertEnded:      "%s: ended on %s",
	CertRenewAgent: "Agent certificates are not renewed automatically yet (#128): revoke the server and enroll it again with a new token.",
	CertRenewCA:    "The CA is not renewed automatically yet (#128): a new CA means every agent must enroll again.",

	// --- Plugins of the hub (#262).
	Plugins:            "Plugins of the hub",
	NoPlugins:          "The plugins directory holds no plugin for the hub.",
	PluginRestarts:     "restarts",
	PluginStarting:     "starting",
	PluginRunning:      "running",
	PluginRestarting:   "restarting",
	PluginFailed:       "failed",
	PluginIncompatible: "incompatible",
	PluginStopped:      "stopped",
	// --- end of the plugins block.

	// --- Plugins in the Health tab (#319).
	PluginInvalid: "invalid",
	ChkUpdates:    "Updates",
	UpdPending:    "Version %s is available: install it from the Updates page of the dashboard",
	UpdNone:       "no newer release was found at the last check",

	// --- Reachability from the network (#369).
	ChkLAN:       "Network access",
	LANLocalOnly: "the dashboard listens on %s: only this machine can open it; to reach it from the network set listen to \"0.0.0.0:%s\"",
	LANTry:       "listens on a single address: from another host open %s (if it does not load, check the firewall)",
	LANNoAddr:    "no network address of this machine was found; check the network and the listen setting",
	LANLater:     "start the hub, then from another host open %s",
	LANOK:        "the hub answers on this machine's network addresses; the firewall is not checked: from another host open %s",
	LANFail:      "the hub does not answer on %s: check the listen setting and that the service runs",
	// --- end of the network reachability block.
	ChkPlugins: "Plugins",
	PlgOK:      "%d plugins of the hub, none failed or invalid",
	PlgProblem: "no plugin runs: %s",
	PlgBad:     "need a look (see the Status tab): %s",
	// --- end of the plugin health block.

	// --- Setting errors (#318).
	SettingErrors: map[string]string{
		"unknown_setting":  "unknown setting %s (known: %s)",
		"empty":            "must not be empty",
		"address":          "use host:port, such as :8080 or 127.0.0.1:8080, or only the port",
		"port":             "the port must be a number from 1 to 65535",
		"schema":           "use letters, digits and underscore, not starting with a digit",
		"password_in_file": "the secret is never stored in the configuration file: put %s=... in /etc/regulus/regulus.env (mode 0640, root:regulus) and restart the service",
		"bool":             "use true or false",
		"duration":         "use a duration such as 10s or 1m",
		"size":             "use a size such as 256MiB or 1GiB",
		"retention":        "use days such as 3d or 90d (or a duration such as 36h), or 0 to turn the tier off",
		"too_short":        "too short (minimum %s)",
		"choice":           "use one of: %s",
		"read_file":        "cannot read the configuration file: %s",
		"data_dir_move":    "%s holds the hub's data and %s holds none. Changing the setting does not move it: the hub would start with an empty database, a new secret key and a new CA. Stop the hub, copy the directory to the new place, then change the setting. To start fresh on purpose, run: sudo regulus config set data_dir PATH --force",
		"unusual_line":     "cannot change this line automatically (%s): edit the file by hand",
		"unreadable":       "the change would leave %s unreadable: %s",
		"hand_edit":        "the setting is written in %s in a form the console does not change: edit the file by hand",
		"postgres_host":    "set database.host first: PostgreSQL needs a host",
		"invalid":          "the configuration would not be valid: %s",
		"write_permission": "the configuration file belongs to another user: open the console with sudo (%s)",
		"write_file":       "cannot write the configuration file: %s",
	},
	// --- end of the setting errors block.
}

// T is the language in use. It is English until UseLocale picks another.
var T = en

// UseLocale picks the console language from the environment, the way the C
// library does: the first of LC_ALL, LC_MESSAGES and LANG that is set decides,
// and a value that starts with "it" (it_IT.UTF-8, it) is Italian. Anything
// else, and no value, is English. It sets the package-wide T, so call it once
// at start (or again in tests: it always resets).
func UseLocale(getenv func(string) string) {
	T = en
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := strings.ToLower(strings.TrimSpace(getenv(k))); v != "" {
			if v == "it" || strings.HasPrefix(v, "it_") || strings.HasPrefix(v, "it.") || strings.HasPrefix(v, "it@") {
				T = it
			}
			return
		}
	}
}
