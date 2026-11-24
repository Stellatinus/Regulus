package console

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/localstatus"
	"github.com/CodexCircle/Regulus/internal/plugin"
	"github.com/CodexCircle/Regulus/internal/storage"
)

// ServiceUnit is the systemd unit of the hub.
const ServiceUnit = "regulus"

// Check statuses.
const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
	Info = "info"
)

// Check is one health check. Name and Detail are always English, because
// --json and --plain are for programs; Title and Text are the same in the
// language of the console (T), for people.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	// Key names the check in --plain (check.<key>); it never changes.
	Key   string `json:"-"`
	Title string `json:"-"`
	Text  string `json:"-"`
}

// Agent is a connected agent as the hub reports it.
type Agent = localstatus.Agent

// Plugin is a plugin of the hub as the hub reports it.
type Plugin = localstatus.Plugin

// Snapshot is everything the console shows about the hub at one moment.
type Snapshot struct {
	Version string `json:"version"`
	// ConfigFile is the file the settings are read from and written to, or
	// "" when there is none.
	ConfigFile string `json:"config_file"`
	// Problem is set when the configuration cannot be loaded.
	Problem string `json:"problem,omitempty"`

	// Service is the state of the systemd unit: active, inactive, failed,
	// or unknown when systemd does not know it or is not there.
	Service string `json:"service"`
	// Running is true when the hub answered on its local socket.
	Running bool `json:"running"`

	Listen      string   `json:"listen"`
	Port        string   `json:"port"`
	URL         string   `json:"url"`
	LANURLs     []string `json:"lan_urls,omitempty"`
	AgentListen string   `json:"agent_listen,omitempty"`
	// CAFingerprint is what a new agent pins, from the running hub.
	CAFingerprint string `json:"ca_fingerprint,omitempty"`
	DataDir       string `json:"data_dir"`
	DatabaseSize  int64  `json:"database_bytes"`
	// Database names the backend (never with a password) and DatabaseProblem
	// is what the running hub reports when it cannot reach PostgreSQL.
	Database        string `json:"database"`
	DatabaseProblem string `json:"database_problem,omitempty"`
	BuiltinAgent    bool   `json:"builtin_agent"`
	// LogLevel and Format are shown in the header of the logs.
	LogLevel string `json:"log_level"`

	StartedAt time.Time `json:"started_at,omitzero"`
	// Uptime is in English, for programs; UptimeText in the console language.
	Uptime     string  `json:"uptime,omitempty"`
	UptimeText string  `json:"-"`
	SetupToken string  `json:"setup_token,omitempty"`
	Agents     []Agent `json:"agents"`
	// CertSoonest is the hub's certificate that ends first, and CertWarnings
	// the CA and agent certificates that end within 90 days (see certs.go).
	CertSoonest  *Certificate  `json:"cert_soonest,omitempty"`
	CertWarnings []Certificate `json:"cert_warnings,omitempty"`
	// UpdateAvailable is the version of a newer release found at the last
	// check of the running hub (#71), empty when there is none.
	UpdateAvailable string `json:"update_available,omitempty"`
	// PluginsEnabled is true when the running hub has a plugins directory;
	// Plugins is the state of its plugins and PluginsProblem why none runs.
	PluginsEnabled bool     `json:"plugins_enabled,omitempty"`
	PluginsProblem string   `json:"plugins_problem,omitempty"`
	Plugins        []Plugin `json:"plugins,omitempty"`

	Settings []config.Entry `json:"-"`
	Health   []Check        `json:"health"`
}

// System is how the console looks at the machine. Tests replace it.
type System struct {
	Getenv func(string) string
	Now    func() time.Time
	// ServiceState returns the systemd state of the hub's unit.
	ServiceState func(ctx context.Context) string
	// Fetch asks the running hub for its status.
	Fetch func(ctx context.Context, dataDir string) (localstatus.Status, error)
	// Addrs lists the IPv4 addresses of this machine, besides loopback.
	Addrs func() []string
	// Logs returns the last lines of the hub's log.
	Logs func(ctx context.Context, n int) ([]string, error)
	// Follow streams the hub's log like `journalctl -f`: the last n lines,
	// then each new line, until ctx ends or the source does (#369). Nil
	// means the console reloads Logs every two seconds instead.
	Follow func(ctx context.Context, n int) (<-chan string, error)
	// Dial opens a TCP connection to addr, to see whether the hub answers
	// on an address of this machine (#369). Nil skips that check.
	Dial func(ctx context.Context, addr string) error
}

// HostSystem is the real machine.
func HostSystem() System {
	return System{
		Getenv:       os.Getenv,
		Now:          time.Now,
		ServiceState: systemdState,
		Fetch:        localstatus.Fetch,
		Addrs:        lanAddrs,
		Logs:         journalLogs,
		Follow:       journalFollow,
		Dial:         dialTCP,
	}
}

func systemdState(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "is-active", ServiceUnit).Output()
	state := strings.TrimSpace(string(out))
	if state == "" || (err != nil && state == "") {
		return "unknown"
	}
	return state
}

// dialTCP connects to addr and closes the connection at once.
func dialTCP(ctx context.Context, addr string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

// ErrNoJournal is what Logs and Follow return when journalctl is not on this
// machine (a container, OpenRC or runit, a hub started by hand). The hub has no
// log file: it writes its log to standard error and leaves it to whatever
// started it, so the Logs tab says where to look instead (#375, decisions.md).
var ErrNoJournal = errors.New("journalctl not found")

// journalFollow runs `journalctl -f` and sends its lines on the channel,
// which is closed when the command ends or ctx is done. An error is returned
// only when the command cannot start.
func journalFollow(ctx context.Context, n int) (<-chan string, error) {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return nil, ErrNoJournal
	}
	cmd := exec.CommandContext(ctx, "journalctl", "-u", ServiceUnit, "-n", fmt.Sprint(n), "-f", "--no-pager", "-o", "cat")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	ch := make(chan string, 256)
	go func() {
		defer close(ch)
		defer func() { _ = cmd.Wait() }()
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			select {
			case ch <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func journalLogs(ctx context.Context, n int) ([]string, error) {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return nil, ErrNoJournal
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", "-u", ServiceUnit, "-n", fmt.Sprint(n), "--no-pager", "-o", "cat").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	text := strings.TrimRight(string(out), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

func lanAddrs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, ipn.IP.String())
	}
	return out
}

// Collect gathers the snapshot: the configuration (from configFile, the
// environment and the defaults), the state of the service and, when the hub
// runs, what it says about itself.
func Collect(ctx context.Context, sys System, configFile, version string) Snapshot {
	snap := Snapshot{Version: version, ConfigFile: configFile, Agents: []Agent{}}
	cfg, entries, err := config.Resolve(configFile, sys.Getenv)
	if err != nil {
		snap.Problem = err.Error()
		cfg = config.Default()
		_, entries, _ = config.Resolve("", func(string) string { return "" })
	}
	snap.Settings = entries
	snap.Listen = cfg.Listen
	snap.Port = portOf(cfg.Listen)
	snap.AgentListen = cfg.AgentListen
	snap.DataDir = cfg.DataDir
	snap.Database = cfg.Storage().Describe()
	snap.BuiltinAgent = cfg.BuiltinAgent
	snap.LogLevel = cfg.Log.Level
	snap.URL = cfg.DashboardURL()
	if hostOf(cfg.Listen) == "" || isWildcard(hostOf(cfg.Listen)) {
		for _, a := range sys.Addrs() {
			snap.LANURLs = append(snap.LANURLs, cfg.Scheme()+"://"+net.JoinHostPort(a, snap.Port))
		}
	}
	if fi, err := os.Stat(filepath.Join(cfg.DataDir, storage.FileName)); err == nil {
		snap.DatabaseSize = fi.Size()
	}

	snap.Service = sys.ServiceState(ctx)
	live, fetchErr := sys.Fetch(ctx, cfg.DataDir)
	if fetchErr == nil {
		snap.Running = true
		snap.StartedAt = live.StartedAt
		snap.Uptime = formatDuration(sys.Now().Sub(live.StartedAt), en)
		snap.UptimeText = formatDuration(sys.Now().Sub(live.StartedAt), T)
		snap.SetupToken = live.SetupToken
		snap.Agents = live.Agents
		if live.Version != "" {
			snap.Version = live.Version
		}
		if live.AgentAddr != "" {
			snap.AgentListen = live.AgentAddr
		}
		snap.CAFingerprint = live.CAFingerprint
		snap.DatabaseProblem = live.DatabaseProblem
		snap.PluginsEnabled, snap.PluginsProblem, snap.Plugins = live.PluginsEnabled, live.PluginsProblem, live.Plugins
		snap.CertSoonest, snap.CertWarnings = live.CertSoonest, live.CertWarnings
		snap.UpdateAvailable = live.UpdateAvailable
	}
	snap.Health = healthChecks(snap, cfg, fetchErr)
	if c, ok := certCheck(snap, sys.Now()); ok {
		snap.Health = append(snap.Health, c)
	}
	if snap.Running {
		snap.Health = append(snap.Health, updatesCheck(snap))
	}
	snap.Health = append(snap.Health, lanCheck(ctx, sys, snap, cfg))
	return snap
}

// text is a text of the table, filled in, so that a check can be written in
// English (for --json and --plain) and in the console language.
type text func(strs) string

// tr fills the template that pick chooses with args.
func tr(pick func(strs) string, args ...any) text {
	return func(s strs) string {
		if len(args) == 0 {
			return pick(s)
		}
		return fmt.Sprintf(pick(s), args...)
	}
}

// verbatim is data that is the same in every language (an error message).
func verbatim(v string) text { return func(strs) string { return v } }

func healthChecks(snap Snapshot, cfg config.Config, fetchErr error) []Check {
	var checks []Check
	add := func(key string, name func(strs) string, status string, detail text) {
		checks = append(checks, Check{
			Name: name(en), Status: status, Detail: detail(en),
			Key: key, Title: name(T), Text: detail(T),
		})
	}
	cfgName := func(s strs) string { return s.ChkConfig }
	hubName := func(s strs) string { return s.ChkHub }
	dataName := func(s strs) string { return s.ChkDataDir }
	dbName := func(s strs) string { return s.ChkDatabase }

	switch {
	case snap.Problem != "":
		add("configuration", cfgName, Fail, verbatim(snap.Problem))
	case snap.ConfigFile == "":
		add("configuration", cfgName, Warn, tr(func(s strs) string { return s.CfgMissing }))
	default:
		// %s: the configuration file.
		add("configuration", cfgName, OK, tr(func(s strs) string { return s.CfgValid }, snap.ConfigFile))
	}

	switch {
	case snap.Running:
		add("hub", hubName, OK, tr(func(s strs) string { return s.HubAnswers }))
	case fetchErr != nil && errors.Is(fetchErr, fs.ErrPermission):
		add("hub", hubName, Warn, tr(func(s strs) string { return s.HubNoPermission }))
	case snap.Service == "active":
		// %s: the path of the local socket.
		add("hub", hubName, Warn, tr(func(s strs) string { return s.HubSilent }, localstatus.SocketPath(snap.DataDir)))
	default:
		add("hub", hubName, Warn, tr(func(s strs) string { return s.Stopped }))
	}

	st, detail := portCheck(cfg.Listen, snap.Running)
	add("dashboard_port", func(s strs) string { return s.ChkDashboardPort }, st, detail)
	if cfg.AgentListen != "" {
		st, detail = portCheck(cfg.AgentListen, snap.Running)
		add("agent_port", func(s strs) string { return s.ChkAgentPort }, st, detail)
	}

	// %s: the data directory.
	switch err := writable(snap.DataDir); {
	case errors.Is(err, fs.ErrNotExist):
		add("data_directory", dataName, Warn, tr(func(s strs) string { return s.DataMissing }, snap.DataDir))
	case err != nil:
		add("data_directory", dataName, Warn, tr(func(s strs) string { return s.DataNotWritable }, snap.DataDir))
	default:
		add("data_directory", dataName, OK, tr(func(s strs) string { return s.DataWritable }, snap.DataDir))
	}

	// The first %s is the database (PostgreSQL at host:port/name), then the
	// problem; for SQLite the file name and its size.
	switch {
	case cfg.Storage().IsPostgres():
		switch {
		case snap.DatabaseProblem != "":
			add("database", dbName, Fail, tr(func(s strs) string { return s.DBProblem }, snap.Database, snap.DatabaseProblem))
		case snap.Running:
			add("database", dbName, OK, tr(func(s strs) string { return s.DBAnswers }, snap.Database))
		default:
			add("database", dbName, Info, tr(func(s strs) string { return s.DBLater }, snap.Database))
		}
	case fileExists(filepath.Join(snap.DataDir, storage.FileName)):
		add("database", dbName, OK, tr(func(s strs) string { return s.DBFile }, storage.FileName, FormatBytes(snap.DatabaseSize)))
	default:
		add("database", dbName, Info, tr(func(s strs) string { return s.DBNone }))
	}

	// The hub's plugins, while it runs with a plugins directory (#319): a
	// plugin that failed for good, cannot run here or is not a usable plugin
	// needs the administrator; one that is starting or restarting does not
	// yet.
	if snap.Running && snap.PluginsEnabled {
		plgName := func(s strs) string { return s.ChkPlugins }
		var bad []Plugin
		for _, p := range snap.Plugins {
			switch p.State {
			case plugin.StateFailed, plugin.StateIncompatible, plugin.StateInvalid:
				bad = append(bad, p)
			}
		}
		switch {
		case snap.PluginsProblem != "":
			add("plugins", plgName, Warn, tr(func(s strs) string { return s.PlgProblem }, snap.PluginsProblem))
		case len(bad) > 0:
			add("plugins", plgName, Warn, func(s strs) string {
				parts := make([]string, len(bad))
				for i, p := range bad {
					parts[i] = p.ID + " (" + pluginStateIn(s, p.State) + ")"
				}
				return fmt.Sprintf(s.PlgBad, strings.Join(parts, ", "))
			})
		case len(snap.Plugins) == 0:
			add("plugins", plgName, Info, tr(func(s strs) string { return s.NoPlugins }))
		default:
			add("plugins", plgName, OK, tr(func(s strs) string { return s.PlgOK }, len(snap.Plugins)))
		}
	}
	return checks
}

// portCheck says whether the address can be used. While the hub runs it is
// the hub that holds it. The %s of the details is the address (and the error).
func portCheck(addr string, hubRunning bool) (string, text) {
	if hubRunning {
		return OK, tr(func(s strs) string { return s.PortServed }, addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return Fail, tr(func(s strs) string { return s.PortInUse }, addr)
		}
		if errors.Is(err, fs.ErrPermission) {
			return Warn, tr(func(s strs) string { return s.PortPrivileged }, addr)
		}
		return Warn, tr(func(s strs) string { return s.PortError }, addr, err.Error())
	}
	_ = ln.Close()
	return OK, tr(func(s strs) string { return s.PortFree }, addr)
}

func writable(dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".regulus-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

func hostOf(listen string) string {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	return host
}

func portOf(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	return port
}

func isWildcard(host string) bool { return host == "0.0.0.0" || host == "::" }

// FormatBytes writes a size for people: 1.5 MiB.
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// formatDuration writes a duration for people, in the language of s: 3h 12m.
func formatDuration(d time.Duration, s strs) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	h := int(d / time.Hour)
	d -= time.Duration(h) * time.Hour
	m := int(d / time.Minute)
	sec := int((d - time.Duration(m)*time.Minute) / time.Second)
	switch {
	case days > 0:
		return fmt.Sprintf(s.DurDays, days, h, m)
	case h > 0:
		return fmt.Sprintf(s.DurHours, h, m)
	case m > 0:
		return fmt.Sprintf(s.DurMinutes, m, sec)
	}
	return fmt.Sprintf(s.DurSeconds, sec)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// updatesCheck says whether the running hub found a newer release (#71). It is
// a warning, not a failure: the server works, it only is not the newest.
func updatesCheck(snap Snapshot) Check {
	name := func(s strs) string { return s.ChkUpdates }
	if snap.UpdateAvailable != "" {
		d := tr(func(s strs) string { return s.UpdPending }, snap.UpdateAvailable)
		return Check{Name: name(en), Status: Warn, Detail: d(en), Key: "updates", Title: name(T), Text: d(T)}
	}
	d := tr(func(s strs) string { return s.UpdNone })
	return Check{Name: name(en), Status: OK, Detail: d(en), Key: "updates", Title: name(T), Text: d(T)}
}

// lanCheck says whether the dashboard can be reached from the network (#369).
// A program on this machine cannot prove that another host gets through (a
// firewall or a router may stop it), so it checks what it can: that the hub
// listens on an address other hosts could use, and that it answers there. The
// text then tells the person how to try from another host.
func lanCheck(ctx context.Context, sys System, snap Snapshot, cfg config.Config) Check {
	name := func(s strs) string { return s.ChkLAN }
	mk := func(status string, d text) Check {
		return Check{Name: name(en), Status: status, Detail: d(en), Key: "lan", Title: name(T), Text: d(T)}
	}
	host := hostOf(cfg.Listen)
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		// %s: the listen address.
		return mk(Info, tr(func(s strs) string { return s.LANLocalOnly }, cfg.Listen, snap.Port))
	}
	if len(snap.LANURLs) == 0 {
		if !isWildcard(host) && host != "" {
			// A specific address: it is the one to try.
			return mk(Info, tr(func(s strs) string { return s.LANTry }, snap.URL))
		}
		return mk(Info, tr(func(s strs) string { return s.LANNoAddr }))
	}
	if !snap.Running || sys.Dial == nil {
		return mk(Info, tr(func(s strs) string { return s.LANLater }, snap.LANURLs[0]))
	}
	var bad []string
	for _, u := range snap.LANURLs {
		pu, err := url.Parse(u)
		if err != nil {
			continue
		}
		if err := sys.Dial(ctx, pu.Host); err != nil {
			bad = append(bad, pu.Host)
		}
	}
	if len(bad) > 0 {
		return mk(Warn, tr(func(s strs) string { return s.LANFail }, strings.Join(bad, ", ")))
	}
	return mk(OK, tr(func(s strs) string { return s.LANOK }, snap.LANURLs[0]))
}
