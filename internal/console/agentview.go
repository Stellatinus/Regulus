package console

// agentview.go: the console of a server that runs only the agent (#434). The
// hub's screens (dashboard address, setup token, hub settings, the agents
// list) mean nothing there, and the hub's unit is not enabled, so the hub view
// would say "inactive" about a server that works. The role is what
// `install.sh --role agent` wrote in /etc/regulus/role; with it the commands
// `regulus`, `regulus console` and `regulus status` show this view instead.
//
// The agent has no local socket like the hub's: what the view shows is read
// from the machine (the unit, agent.yaml, the credentials in the data
// directory, the agent's log). In particular the link to the hub is read from
// the last connection line of the log, and says "not known" when the log has
// none; it is not the hub's own record.

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/CodexCircle/Regulus/internal/agent"
	"github.com/CodexCircle/Regulus/internal/config"
)

// Where the packages and the installer put the role and the agent's files.
const (
	// AgentUnit is the systemd unit of the agent.
	AgentUnit = "regulus-agent"
	// RoleFile holds "agent" on a server installed with --role agent; it is
	// absent on a hub.
	RoleFile = "/etc/regulus/role"
	// DefaultAgentConfig is the agent's configuration file.
	DefaultAgentConfig = "/etc/regulus/agent.yaml"
)

// The role of a server.
const (
	RoleHub   = "hub"
	RoleAgent = "agent"
)

// How the agent stands with the hub, as the log says.
const (
	LinkConnected = "connected"
	LinkRetrying  = "retrying"
	LinkRejected  = "rejected"
	LinkUnknown   = "unknown"
	LinkStopped   = "stopped"
)

// Role returns RoleAgent when the role file says so, RoleHub otherwise.
func Role(readFile func(string) ([]byte, error)) string {
	data, err := readFile(RoleFile)
	if err != nil {
		return RoleHub
	}
	if strings.TrimSpace(string(data)) == RoleAgent {
		return RoleAgent
	}
	return RoleHub
}

// AgentSnapshot is everything the agent view shows at one moment.
type AgentSnapshot struct {
	Role       string `json:"role"`
	Version    string `json:"version"`
	ConfigFile string `json:"config_file"`
	// Problem is set when the configuration cannot be loaded.
	Problem string `json:"problem,omitempty"`
	// Service is the state of regulus-agent.service.
	Service string `json:"service"`
	// Running is true when the unit is active.
	Running bool `json:"running"`
	// Hub is the address of the hub's agent listener the agent connects to.
	Hub     string `json:"hub"`
	DataDir string `json:"data_dir"`
	// Enrolled is false until the agent has its credentials.
	Enrolled bool   `json:"enrolled"`
	AgentID  string `json:"agent_id,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	// CertNotAfter is when the agent's certificate ends.
	CertNotAfter time.Time `json:"cert_not_after,omitzero"`
	// Link is connected, retrying, rejected, unknown or stopped.
	Link    string `json:"link"`
	Updates bool   `json:"updates"`
}

// AgentSystem is how the agent view looks at the machine. Tests replace it.
type AgentSystem struct {
	Getenv       func(string) string
	Now          func() time.Time
	Hostname     func() (string, error)
	ServiceState func(ctx context.Context) string
	// Logs returns the last lines of the agent's log.
	Logs func(ctx context.Context, n int) ([]string, error)
}

// HostAgentSystem is the real machine.
func HostAgentSystem() AgentSystem {
	return AgentSystem{
		Getenv:       os.Getenv,
		Now:          time.Now,
		Hostname:     os.Hostname,
		ServiceState: func(ctx context.Context) string { return unitState(ctx, AgentUnit) },
		Logs:         func(ctx context.Context, n int) ([]string, error) { return unitLogs(ctx, AgentUnit, n) },
	}
}

// CollectAgent gathers the snapshot of the agent view from the agent's
// configuration file (configFile), its credentials and the machine.
func CollectAgent(ctx context.Context, sys AgentSystem, configFile, version string) AgentSnapshot {
	snap := AgentSnapshot{Role: RoleAgent, Version: version, ConfigFile: configFile, Link: LinkUnknown}
	var args []string
	if configFile != "" {
		args = []string{"--config", configFile}
	}
	cfg, err := config.LoadAgent(args, sys.Getenv, io.Discard)
	if err != nil {
		snap.Problem = err.Error()
		cfg = config.DefaultAgent()
	}
	snap.Hub, snap.DataDir, snap.Updates = cfg.Hub, cfg.DataDir, cfg.Updates
	if h, err := sys.Hostname(); err == nil {
		snap.Hostname = h
	}
	creds, err := agent.LoadCredentials(cfg.DataDir)
	switch {
	case err == nil:
		snap.Enrolled, snap.AgentID = true, creds.AgentID
		if cert, err := x509.ParseCertificate(creds.CertDER); err == nil {
			snap.CertNotAfter = cert.NotAfter
		}
	case errors.Is(err, agent.ErrNotEnrolled):
	default:
		if snap.Problem == "" {
			snap.Problem = err.Error()
		}
	}

	snap.Service = sys.ServiceState(ctx)
	snap.Running = snap.Service == "active"
	if !snap.Running {
		snap.Link = LinkStopped
	} else if lines, err := sys.Logs(ctx, 200); err == nil {
		snap.Link = linkFromLog(lines)
	}
	return snap
}

// linkFromLog reads how the agent stands with the hub from the last line of
// its log that says so. These are the messages of internal/agent and of
// cmd/regulus/agent.go; a message that changes there must change here.
func linkFromLog(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		switch {
		case strings.Contains(l, "the hub does not accept this agent"):
			return LinkRejected
		case strings.Contains(l, "connected to the hub"):
			return LinkConnected
		case strings.Contains(l, "connection to the hub lost"),
			strings.Contains(l, "another connection with this agent's identity"):
			return LinkRetrying
		}
	}
	return LinkUnknown
}

func linkWord(link string) string {
	switch link {
	case LinkConnected:
		return T.AgentLinkConnected
	case LinkRetrying:
		return T.AgentLinkRetrying
	case LinkRejected:
		return T.AgentLinkRejected
	case LinkStopped:
		return T.AgentLinkStopped
	}
	return T.AgentLinkUnknown
}

// agentHint is the line that tells what to do next, or "".
func agentHint(snap AgentSnapshot) string {
	switch {
	case !snap.Enrolled:
		return T.AgentHintNotEnrolled
	case snap.Link == LinkRejected:
		return T.AgentHintRejected
	}
	return ""
}

func agentCert(snap AgentSnapshot, now time.Time) string {
	if snap.CertNotAfter.IsZero() {
		return "-"
	}
	days := int(snap.CertNotAfter.Sub(now).Hours() / 24)
	date := snap.CertNotAfter.UTC().Format(time.DateOnly)
	// The row is already named Certificate: drop the name the template adds.
	if snap.CertNotAfter.Before(now) {
		return strings.TrimPrefix(fmt.Sprintf(T.CertEnded, T.AgentCert, date), T.AgentCert+": ")
	}
	return strings.TrimPrefix(fmt.Sprintf(T.CertEnds, T.AgentCert, date, fmt.Sprint(days)), T.AgentCert+": ")
}

// agentRows are the rows of the status, in the console language.
func agentRows(snap AgentSnapshot, now time.Time) [][2]string {
	id := snap.AgentID
	if !snap.Enrolled {
		id = T.AgentNotEnrolled
	}
	rows := [][2]string{
		{T.AgentRole, RoleAgent},
		{capFirst(T.Service), AgentUnit + ": " + stateWord(snap.Service)},
		{T.Version, snap.Version},
		{T.Hub, orDash(snap.Hub, "-")},
		{T.AgentLink, linkWord(snap.Link)},
		{T.AgentID, id},
		{T.AgentHost, orDash(snap.Hostname, "-")},
		{T.AgentCert, agentCert(snap, now)},
		{T.DataDir, snap.DataDir},
		{T.ConfigFile, orDash(snap.ConfigFile, T.None)},
	}
	if snap.Problem != "" {
		rows = append(rows, [2]string{T.Problem, snap.Problem})
	}
	return rows
}

// WriteAgentStatus prints the agent view as text, --plain or --json.
func WriteAgentStatus(w io.Writer, snap AgentSnapshot, f Format, now time.Time) error {
	switch f {
	case FormatJSON:
		return writeJSON(w, snap)
	case FormatPlain:
		p := func(k string, v any) { _, _ = fmt.Fprintf(w, "%s=%v\n", k, v) }
		p("role", snap.Role)
		p("version", snap.Version)
		p("service", snap.Service)
		p("running", snap.Running)
		p("hub", snap.Hub)
		p("link", snap.Link)
		p("enrolled", snap.Enrolled)
		p("agent_id", snap.AgentID)
		p("hostname", snap.Hostname)
		if !snap.CertNotAfter.IsZero() {
			p("cert_not_after", snap.CertNotAfter.UTC().Format(time.RFC3339))
		}
		p("data_dir", snap.DataDir)
		p("config_file", snap.ConfigFile)
		if snap.Problem != "" {
			p("problem", snap.Problem)
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range agentRows(snap, now) {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", r[0], r[1])
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if h := agentHint(snap); h != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", h)
	}
	return nil
}

// unitState is `systemctl is-active` of a unit, or "unknown".
func unitState(ctx context.Context, unit string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output()
	if state := strings.TrimSpace(string(out)); state != "" {
		return state
	}
	return "unknown"
}

// unitLogs returns the last n lines of the journal of a unit.
func unitLogs(ctx context.Context, unit string, n int) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", "-u", unit, "-n", fmt.Sprint(n), "--no-pager", "-o", "cat").CombinedOutput()
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

// ---------------------------------------------------------------- the screen

// AgentOptions are what the agent console needs from outside.
type AgentOptions struct {
	System     AgentSystem
	ConfigFile string
	Version    string
	Unicode    bool
	// Refresh is how often the snapshot is read again; 0 never.
	Refresh time.Duration
	// Restart restarts the agent's unit; nil means the action is not offered.
	Restart func(ctx context.Context) error
}

// RunAgent opens the full-screen agent console.
func RunAgent(opt AgentOptions) error {
	_, err := tea.NewProgram(NewAgent(opt), tea.WithAltScreen()).Run()
	return err
}

// AgentModel is the agent console: a Status section and a Logs section.
type AgentModel struct {
	opt           AgentOptions
	snap          AgentSnapshot
	logs          []string
	logErr        string
	loaded        bool
	logsTab       bool
	confirm       bool
	notice        string
	noticeIsErr   bool
	width, height int
}

// NewAgent returns the agent console model.
func NewAgent(opt AgentOptions) AgentModel { return AgentModel{opt: opt} }

type (
	agentSnapMsg struct {
		snap AgentSnapshot
		logs []string
		err  error
	}
	agentTickMsg  time.Time
	agentRestartM struct{ err error }
)

// Init loads the first snapshot.
func (m AgentModel) Init() tea.Cmd { return tea.Batch(m.collect(), m.tick()) }

func (m AgentModel) tick() tea.Cmd {
	if m.opt.Refresh <= 0 {
		return nil
	}
	return tea.Tick(m.opt.Refresh, func(t time.Time) tea.Msg { return agentTickMsg(t) })
}

func (m AgentModel) collect() tea.Cmd {
	opt := m.opt
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		snap := CollectAgent(ctx, opt.System, opt.ConfigFile, opt.Version)
		logs, err := opt.System.Logs(ctx, 200)
		return agentSnapMsg{snap, logs, err}
	}
}

// Update handles keys, the refresh and the results of the work.
func (m AgentModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case agentTickMsg:
		return m, tea.Batch(m.collect(), m.tick())
	case agentSnapMsg:
		m.snap, m.logs, m.loaded = msg.snap, msg.logs, true
		m.logErr = ""
		if msg.err != nil {
			m.logErr = msg.err.Error()
		}
	case agentRestartM:
		if msg.err != nil {
			m.notice, m.noticeIsErr = fmt.Sprintf(T.AgentMsgFailed, msg.err), true
		} else {
			m.notice, m.noticeIsErr = T.AgentMsgRestarted, false
		}
		return m, m.collect()
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m AgentModel) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm {
		m.confirm = false
		if k.String() == "y" && m.opt.Restart != nil {
			m.notice, m.noticeIsErr = T.AgentMsgRestarting, false
			restart := m.opt.Restart
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				return agentRestartM{restart(ctx)}
			}
		}
		return m, nil
	}
	switch k.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "tab", "right", "left", "1", "2":
		switch k.String() {
		case "1":
			m.logsTab = false
		case "2":
			m.logsTab = true
		default:
			m.logsTab = !m.logsTab
		}
	case "r":
		if m.opt.Restart != nil {
			m.confirm = true
		}
	}
	return m, nil
}

func (m AgentModel) sym(unicode, ascii string) string {
	if m.opt.Unicode {
		return unicode
	}
	return ascii
}

// View draws the header, the tabs, the section and the help.
func (m AgentModel) View() string {
	w, h := m.width, m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	head := titleStyle.Render(T.AgentTitle)
	if m.snap.Version != "" {
		head += " " + mutedStyle.Render(m.snap.Version)
	}
	if m.loaded {
		dot, style := m.sym("●", "*"), badStyle
		switch {
		case m.snap.Link == LinkConnected:
			style = goodStyle
		case m.snap.Running:
			style = warnStyle
		}
		head += "  " + style.Render(dot+" "+linkWord(m.snap.Link))
	}
	tab := func(n int, name string, on bool) string {
		label := " " + fmt.Sprintf(T.TabLabel, n, name) + " "
		if on {
			return tabOn.Render(label)
		}
		return mutedStyle.Render(label)
	}
	lines := []string{head, "", tab(1, T.TabStatus, !m.logsTab) + " " + tab(2, T.TabLogs, m.logsTab),
		mutedStyle.Render(strings.Repeat(m.sym("─", "-"), w))}

	var body []string
	switch {
	case !m.loaded:
		body = []string{" " + T.Loading}
	case m.logsTab:
		body = m.logsBody(max(h-len(lines)-3, 3))
	default:
		body = m.statusBody()
	}
	bodyH := max(h-len(lines)-3, 3)
	for len(body) < bodyH {
		body = append(body, "")
	}
	lines = append(lines, body[:min(len(body), bodyH)]...)

	status := ""
	switch {
	case m.confirm:
		status = warnStyle.Render(T.AgentRestartAsk + " " + T.HelpConfirm)
	case m.notice != "":
		style := goodStyle
		if m.noticeIsErr {
			style = badStyle
		}
		status = style.Render(m.notice)
	}
	help := T.AgentHelp
	if m.opt.Restart == nil {
		help = T.HelpQuit
	}
	if m.confirm {
		help = T.HelpConfirm
	}
	if !m.opt.Unicode {
		help = strings.NewReplacer("↑/↓", "up/down", "←/→", "left/right").Replace(help)
	}
	lines = append(lines, "", " "+status, " "+mutedStyle.Render(help))
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "…")
	}
	return strings.Join(lines, "\n")
}

func (m AgentModel) statusBody() []string {
	var out []string
	rows := agentRows(m.snap, m.opt.System.Now())
	width := 0
	for _, r := range rows {
		width = max(width, len([]rune(r[0])))
	}
	for _, r := range rows {
		out = append(out, fmt.Sprintf(" %-*s  %s", width, r[0], r[1]))
	}
	if h := agentHint(m.snap); h != "" {
		out = append(out, "", " "+warnStyle.Render(h))
	}
	out = append(out, "", " "+mutedStyle.Render(T.AgentHintLog))
	return out
}

func (m AgentModel) logsBody(height int) []string {
	if m.logErr != "" {
		return []string{" " + warnStyle.Render(T.LogsUnavailable+": "+m.logErr)}
	}
	if len(m.logs) == 0 {
		return []string{" " + mutedStyle.Render(T.LogsEmpty)}
	}
	lines := m.logs
	if len(lines) > height {
		lines = lines[len(lines)-height:] // the newest lines
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		style := lipgloss.NewStyle()
		switch logSeverity(l) {
		case 2:
			style = warnStyle
		case 3:
			style = badStyle
		}
		out = append(out, " "+style.Render(l))
	}
	return out
}

// capFirst upper-cases the first letter of a label.
func capFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}
