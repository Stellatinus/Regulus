// tui.go is the Bubble Tea model of the interactive console (tabs Status,
// Settings, Logs, Health). Reading order: Options and Model, New and Run, Update
// with its key, mouse and edit handlers, View with its section bodies
// (statusBody, settingsBody, logsBody, healthBody). The strings are in
// strings.go and the data it shows is built in snapshot.go. See
// docs/code-map.md.

package console

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/plugin"
)

type tab int

const (
	tabStatus tab = iota
	tabSettings
	tabLogs
	tabHealth
	tabActions
	tabCount
)

// remoteAgentsListen is what the "turn on remote agents" action writes.
const remoteAgentsListen = ":8443"

// Hooks are the actions that need more than the console knows how to do; the
// command supplies them. A nil hook hides its key.
type Hooks struct {
	// ResetAdmin makes the one-time password reset link of the administrator.
	ResetAdmin func(ctx context.Context) (link string, err error)
	// Restart restarts the hub's service.
	Restart func(ctx context.Context) error
}

// Options configures the console.
type Options struct {
	System     System
	ConfigFile string
	Version    string
	Hooks      Hooks
	// Unicode is false for terminals that cannot draw ✓ and the like.
	Unicode bool
	// Refresh is how often the screen reloads; zero turns it off (tests).
	Refresh time.Duration
}

// Model is the Bubble Tea model of the console.
type Model struct {
	opt Options

	width, height int
	tab           tab
	snap          Snapshot
	loaded        bool

	// settings tab
	sel     int
	editing bool
	input   textinput.Model
	editErr string
	editVal string // the canonical value, when the text is valid

	// logs tab
	logs      []string
	logErr    string
	logLevel  int
	logScroll int // lines up from the end; 0 follows the log
	// follow is the live stream of the log while the Logs tab is open
	// (#369); nil when there is none and the tab reloads every tick.
	follow *logStream
	gen    int // tells a stream from an older one that ended

	// body scroll of the status and health tabs
	scroll int

	// actions tab
	actSel int

	notice       string
	noticeIsErr  bool
	needsRestart bool
	confirm      string // "restart", "reset", "agents" or ""
	resetLink    string
}

// New returns the console model.
func New(opt Options) Model {
	in := textinput.New()
	in.Prompt = "> "
	in.Cursor.SetMode(cursor.CursorStatic)
	in.CharLimit = 200
	return Model{opt: opt, input: in}
}

// Run opens the full-screen console and returns when the person quits.
func Run(opt Options) error {
	_, err := tea.NewProgram(New(opt), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

// messages
type (
	tickMsg     time.Time
	snapshotMsg Snapshot
	logsMsg     struct {
		lines []string
		err   error
	}
	// logLinesMsg carries lines of stream gen; closed is true when it ended.
	logLinesMsg struct {
		gen    int
		lines  []string
		closed bool
	}
	opMsg struct {
		kind string
		link string
		err  error
	}
)

// Init loads the first snapshot.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.collect(), m.tick())
}

func (m Model) tick() tea.Cmd {
	if m.opt.Refresh <= 0 {
		return nil
	}
	return tea.Tick(m.opt.Refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) collect() tea.Cmd {
	opt := m.opt
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return snapshotMsg(Collect(ctx, opt.System, opt.ConfigFile, opt.Version))
	}
}

func (m Model) loadLogs() tea.Cmd {
	logs := m.opt.System.Logs
	if logs == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		lines, err := logs(ctx, 500)
		return logsMsg{lines, err}
	}
}

// maxLogLines bounds the lines kept while following.
const maxLogLines = 2000

// logStream is a running Follow: cancel stops it, ch delivers its lines.
type logStream struct {
	cancel context.CancelFunc
	ch     <-chan string
	gen    int
}

// startFollow opens the stream of the log when the system can, and else
// falls back to reloading the last lines. It cancels any earlier stream.
func (m *Model) startFollow() tea.Cmd {
	m.stopFollow()
	m.logs, m.logErr = nil, ""
	if m.opt.System.Follow == nil {
		return m.loadLogs()
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := m.opt.System.Follow(ctx, 500)
	if err != nil {
		cancel()
		return m.loadLogs() // shows the reason, and keeps reloading
	}
	m.gen++
	m.follow = &logStream{cancel: cancel, ch: ch, gen: m.gen}
	return m.waitLines()
}

// stopFollow ends the stream, if any.
func (m *Model) stopFollow() {
	if m.follow != nil {
		m.follow.cancel()
		m.follow = nil
	}
}

// waitLines waits for a line of the stream, then takes what else is ready.
func (m Model) waitLines() tea.Cmd {
	st := m.follow
	if st == nil {
		return nil
	}
	return func() tea.Msg {
		line, ok := <-st.ch
		if !ok {
			return logLinesMsg{gen: st.gen, closed: true}
		}
		lines := []string{line}
		for len(lines) < 500 {
			select {
			case l, ok := <-st.ch:
				if !ok {
					return logLinesMsg{gen: st.gen, lines: lines, closed: true}
				}
				lines = append(lines, l)
			default:
				return logLinesMsg{gen: st.gen, lines: lines}
			}
		}
		return logLinesMsg{gen: st.gen, lines: lines}
	}
}

// Update handles input and results.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		cmds := []tea.Cmd{m.collect(), m.tick()}
		if m.tab == tabLogs && m.follow == nil {
			cmds = append(cmds, m.loadLogs())
		}
		return m, tea.Batch(cmds...)
	case snapshotMsg:
		m.snap = Snapshot(msg)
		m.loaded = true
		if m.sel >= len(m.snap.Settings) {
			m.sel = 0
		}
	case logLinesMsg:
		if m.follow == nil || m.follow.gen != msg.gen {
			return m, nil
		}
		m.logs = append(m.logs, msg.lines...)
		if len(m.logs) > maxLogLines {
			m.logs = append([]string(nil), m.logs[len(m.logs)-maxLogLines:]...)
		}
		if msg.closed {
			// journalctl ended: keep the lines and reload them every tick.
			m.stopFollow()
			return m, nil
		}
		return m, m.waitLines()
	case logsMsg:
		if m.follow != nil {
			return m, nil
		}
		m.logs, m.logErr = msg.lines, ""
		if msg.err != nil {
			m.logErr = msg.err.Error()
		}
	case opMsg:
		return m.operationDone(msg)
	case tea.MouseMsg:
		return m.mouse(msg)
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) operationDone(msg opMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.err != nil && msg.kind == "agents":
		m.say(fmt.Sprintf(T.MsgAgentsFailed, settingError(msg.err)), true)
	case msg.kind == "agents-saved":
		m.needsRestart = true
		m.say(fmt.Sprintf(T.MsgAgentsSaved, remoteAgentsListen)+" "+T.RestartHint, false)
		return m, m.collect()
	case msg.kind == "agents":
		m.needsRestart = false
		m.say(T.MsgAgentsOn, false)
		return m, m.collect()
	case msg.err != nil && msg.kind == "restart":
		m.say(fmt.Sprintf(T.MsgRestartFailed, msg.err.Error(), ServiceUnit), true)
	case msg.err != nil:
		m.say(fmt.Sprintf(T.MsgLinkFailed, msg.err.Error()), true)
	case msg.kind == "restart":
		m.needsRestart = false
		m.say(T.MsgRestarted, false)
		return m, m.collect()
	default:
		m.resetLink = msg.link
		m.say(T.MsgLinkReady, false)
	}
	return m, nil
}

func (m *Model) say(text string, isErr bool) {
	m.notice, m.noticeIsErr = text, isErr
}

func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.editing || m.confirm != "" {
		return m, nil
	}
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		return m.move(-1)
	case msg.Button == tea.MouseButtonWheelDown:
		return m.move(1)
	case msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft && msg.Y == 2:
		// The tab bar is the third line.
		x := 1
		for t := tab(0); t < tabCount; t++ {
			w := lipgloss.Width(m.tabLabel(t)) + 1
			if msg.X >= x && msg.X < x+w {
				return m.switchTo(t)
			}
			x += w
		}
	}
	return m, nil
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		m.stopFollow()
		return m, tea.Quit
	}
	switch {
	case m.confirm != "":
		return m.answer(k)
	case m.editing:
		return m.edit(msg)
	}
	switch k {
	case "q":
		m.stopFollow()
		return m, tea.Quit
	case "tab", "right", "l":
		return m.switchTo((m.tab + 1) % tabCount)
	case "shift+tab", "left", "h":
		return m.switchTo((m.tab + tabCount - 1) % tabCount)
	case "1", "2", "3", "4", "5":
		return m.switchTo(tab(k[0] - '1'))
	case "up", "k":
		return m.move(-1)
	case "down", "j":
		return m.move(1)
	case "pgup":
		return m.move(-10)
	case "pgdown":
		return m.move(10)
	case "enter":
		if m.tab == tabSettings && m.loaded && len(m.snap.Settings) > 0 {
			return m.startEdit()
		}
		if m.tab == tabActions && m.loaded {
			return m.ask(m.actions()[m.actSel].key)
		}
	case "f":
		if m.tab == tabLogs {
			m.logLevel = (m.logLevel + 1) % len(T.LogFilters)
			m.logScroll = 0
		}
	case "r", "p", "a":
		return m.ask(k)
	case "esc":
		m.notice, m.resetLink = "", ""
	}
	return m, nil
}

func (m Model) switchTo(t tab) (tea.Model, tea.Cmd) {
	m.tab, m.scroll = t, 0
	if t == tabLogs {
		m.logScroll = 0
		cmd := m.startFollow()
		return m, cmd
	}
	m.stopFollow()
	return m, nil
}

func (m Model) move(delta int) (tea.Model, tea.Cmd) {
	switch m.tab {
	case tabSettings:
		n := len(m.snap.Settings)
		switch {
		case n == 0:
		case delta == 1 || delta == -1:
			m.sel = ((m.sel+delta)%n + n) % n
		default: // page keys stop at the ends
			m.sel = clamp(m.sel+delta, 0, n-1)
		}
	case tabLogs:
		m.logScroll = max(0, m.logScroll-delta)
	case tabActions:
		n := len(m.actions())
		m.actSel = clamp(m.actSel+delta, 0, n-1)
	default:
		m.scroll = max(0, m.scroll+delta)
	}
	return m, nil
}

func (m Model) answer(k string) (tea.Model, tea.Cmd) {
	what := m.confirm
	m.confirm = ""
	if k != "y" && k != "Y" {
		return m, nil
	}
	hooks := m.opt.Hooks
	switch what {
	case "restart":
		if hooks.Restart == nil {
			m.say(fmt.Sprintf(T.MsgRestartUnavailable, ServiceUnit), true)
			return m, nil
		}
		m.say(T.MsgRestarting, false)
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return opMsg{kind: "restart", err: hooks.Restart(ctx)}
		}
	case "agents":
		file := m.snap.ConfigFile
		m.say(T.MsgTurningOn, false)
		return m, func() tea.Msg {
			if _, err := config.SetFile(file, "agent_listen", remoteAgentsListen); err != nil {
				return opMsg{kind: "agents", err: err}
			}
			if hooks.Restart == nil {
				return opMsg{kind: "agents-saved"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := hooks.Restart(ctx); err != nil {
				return opMsg{kind: "agents-saved"}
			}
			return opMsg{kind: "agents"}
		}
	case "reset":
		m.say(T.MsgMakingLink, false)
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			link, err := hooks.ResetAdmin(ctx)
			return opMsg{kind: "reset", link: link, err: err}
		}
	}
	return m, nil
}

func (m Model) startEdit() (tea.Model, tea.Cmd) {
	e := m.snap.Settings[m.sel]
	m.editing = true
	m.input.SetValue(e.Value)
	m.input.CursorEnd()
	m.input.Focus()
	m.validate()
	return m, nil
}

func (m *Model) validate() {
	e := m.snap.Settings[m.sel]
	v, err := config.Normalize(e.Key, m.input.Value())
	if err != nil {
		m.editErr, m.editVal = settingError(err), ""
		return
	}
	m.editErr, m.editVal = "", v
}

// settingError is the text of an error of config.Normalize or config.SetFile
// in the console language: the template of its code in T.SettingErrors. An
// error without a code, or a code the table lacks, is shown as it is.
func settingError(err error) string {
	var se *config.SettingError
	if !errors.As(err, &se) {
		return err.Error()
	}
	tpl, ok := T.SettingErrors[string(se.Code)]
	if !ok {
		return err.Error()
	}
	args := make([]any, len(se.Args))
	for i, a := range se.Args {
		args[i] = a
	}
	return fmt.Sprintf(tpl, args...)
}

func (m Model) edit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.snap.Settings[m.sel]
	switch msg.String() {
	case "esc":
		m.editing = false
		m.input.Blur()
		return m, nil
	case "tab":
		if len(e.Choices) > 0 {
			next := 0
			for i, c := range e.Choices {
				if c == m.editVal || c == m.input.Value() {
					next = (i + 1) % len(e.Choices)
				}
			}
			m.input.SetValue(e.Choices[next])
			m.input.CursorEnd()
			m.validate()
		}
		return m, nil
	case "enter":
		m.validate()
		if m.editErr != "" {
			return m, nil
		}
		if m.snap.ConfigFile == "" {
			m.editErr = T.NoConfigToChange
			return m, nil
		}
		value, err := config.SetFile(m.snap.ConfigFile, e.Key, m.editVal)
		if err != nil {
			m.editErr = settingError(err)
			return m, nil
		}
		m.editing = false
		m.input.Blur()
		text := fmt.Sprintf(T.MsgSaved, e.Key, displayValue(value))
		if e.Restart && value != e.Value {
			m.needsRestart = true
			text += " " + T.RestartHint
		}
		m.say(text, false)
		return m, m.collect()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.validate()
	return m, cmd
}

// --- view ---

var (
	accent  = lipgloss.AdaptiveColor{Light: "#5B3FD9", Dark: "#A78BFA"}
	muted   = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}
	good    = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"}
	warning = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
	bad     = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}

	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	mutedStyle  = lipgloss.NewStyle().Foreground(muted)
	headStyle   = lipgloss.NewStyle().Bold(true)
	goodStyle   = lipgloss.NewStyle().Foreground(good)
	warnStyle   = lipgloss.NewStyle().Foreground(warning)
	badStyle    = lipgloss.NewStyle().Foreground(bad)
	selectStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	tabOn       = lipgloss.NewStyle().Bold(true).Foreground(accent).Underline(true)
)

func (m Model) width0() int {
	if m.width <= 0 {
		return 80
	}
	return m.width
}

func (m Model) height0() int {
	if m.height <= 0 {
		return 24
	}
	return m.height
}

func (m Model) tabLabel(t tab) string {
	name := [...]string{T.TabStatus, T.TabSettings, T.TabLogs, T.TabHealth, T.TabActions}[t]
	label := " " + fmt.Sprintf(T.TabLabel, int(t)+1, name) + " "
	if t == m.tab {
		return tabOn.Render(label)
	}
	return mutedStyle.Render(label)
}

// View draws the screen: header, section tabs, the section, then the help.
func (m Model) View() string {
	w, h := m.width0(), m.height0()

	var header strings.Builder
	header.WriteString(titleStyle.Render(T.Title))
	if m.snap.Version != "" {
		header.WriteString(" " + mutedStyle.Render(m.snap.Version))
	}
	if m.loaded {
		dot, style := m.sym("●", "*"), badStyle
		label := T.Stopped
		if m.snap.Running {
			style, label = goodStyle, T.Running
		} else if m.snap.Service == "active" {
			style, label = warnStyle, T.RunningUnsure
		}
		header.WriteString("  " + T.Hub + " " + style.Render(dot+" "+label))
	}

	var tabs []string
	for t := tab(0); t < tabCount; t++ {
		tabs = append(tabs, m.tabLabel(t))
	}

	lines := []string{header.String(), "", strings.Join(tabs, " ")}
	rule := m.sym("─", "-")
	lines = append(lines, mutedStyle.Render(strings.Repeat(rule, w)))

	bodyH := max(h-len(lines)-3, 3)
	body := m.body(w)
	body = m.window(body, bodyH)
	for len(body) < bodyH {
		body = append(body, "")
	}
	lines = append(lines, body...)

	lines = append(lines, m.footer(w)...)
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "…")
	}
	return strings.Join(lines, "\n")
}

// window picks the lines of the body that fit, honouring the scroll offset.
func (m Model) window(body []string, height int) []string {
	if len(body) <= height {
		return body
	}
	switch m.tab {
	case tabLogs:
		end := len(body) - m.logScroll
		end = clamp(end, height, len(body))
		return body[end-height : end]
	case tabSettings:
		// Keep the selected row and the lines that describe it visible.
		const details = 7
		start := 0
		if row := 1 + m.sel; row+details >= height {
			start = min(row+details-height+1, len(body)-height)
		}
		return body[start : start+height]
	}
	start := clamp(m.scroll, 0, len(body)-height)
	return body[start : start+height]
}

func (m Model) footer(w int) []string {
	var status string
	switch {
	case m.confirm == "restart":
		status = warnStyle.Render(T.RestartAsk + " " + T.HelpConfirm)
	case m.confirm == "reset":
		status = warnStyle.Render(T.ResetAsk + " " + T.HelpConfirm)
	case m.confirm == "agents":
		status = warnStyle.Render(T.AgentsAsk + " " + T.HelpConfirm)
	case m.notice != "":
		style := goodStyle
		if m.noticeIsErr {
			style = badStyle
		}
		status = style.Render(m.notice)
	case m.needsRestart:
		status = warnStyle.Render(T.RestartHint)
	}
	help := T.HelpMain
	if m.opt.Hooks.ResetAdmin != nil {
		help += "  " + T.HelpReset
	}
	help += "  " + T.HelpQuit
	if m.editing {
		help = T.HelpEdit
	} else if m.confirm != "" {
		help = T.HelpConfirm
	}
	if !m.opt.Unicode {
		help = strings.NewReplacer("↑/↓", "up/down", "←/→", "left/right").Replace(help)
	}
	rule := mutedStyle.Render(strings.Repeat(m.sym("─", "-"), w))
	return []string{rule, status, mutedStyle.Render(help)}
}

func (m Model) sym(unicode, ascii string) string {
	if m.opt.Unicode {
		return unicode
	}
	return ascii
}

func (m Model) body(w int) []string {
	if !m.loaded {
		return []string{T.Loading}
	}
	switch m.tab {
	case tabSettings:
		return m.settingsBody(w)
	case tabLogs:
		return m.logsBody()
	case tabHealth:
		return m.healthBody()
	case tabActions:
		return m.actionsBody()
	}
	return m.statusBody()
}

// labelWidth is the width of the labels of kv: 16, or more when a label of the
// console language is longer.
func labelWidth() int {
	w := 16
	for _, l := range []string{T.Hub, T.Version, T.Dashboard, T.AgentPort, T.BuiltinAgent, T.DataDir, T.Database, T.ConfigFile, T.Problem, T.NewAgentAddress, T.NewAgentFingerprint} {
		w = max(w, lipgloss.Width(l)+2)
	}
	return w
}

func kv(k, v string) string {
	return " " + headStyle.Render(fmt.Sprintf("%-*s", labelWidth(), k)) + v
}

func (m Model) statusBody() []string {
	s := m.snap
	var out []string
	hub := T.Stopped
	if s.Running {
		hub = T.Running
		if s.UptimeText != "" {
			hub += ", " + T.Uptime + " " + s.UptimeText
		}
	}
	out = append(out, kv(T.Hub, hub+mutedStyle.Render("  ("+T.Service+": "+stateWord(s.Service)+")")))
	out = append(out, kv(T.Version, s.Version))
	out = append(out, kv(T.Dashboard, s.URL))
	for _, u := range s.LANURLs {
		out = append(out, kv("", mutedStyle.Render(T.AlsoOn+" ")+u))
	}
	out = append(out, kv(T.AgentPort, orDash(s.AgentListen, T.Off)))
	out = append(out, kv(T.BuiltinAgent, onOff(s.BuiltinAgent)))
	data := s.DataDir
	if s.DatabaseSize > 0 {
		data += mutedStyle.Render("  (" + T.Database + " " + FormatBytes(s.DatabaseSize) + ")")
	}
	out = append(out, kv(T.DataDir, data))
	if strings.HasPrefix(s.Database, "PostgreSQL") {
		out = append(out, kv(T.Database, s.Database))
	}
	out = append(out, kv(T.ConfigFile, orDash(s.ConfigFile, T.None)))
	if s.Problem != "" {
		out = append(out, kv(T.Problem, badStyle.Render(s.Problem)))
	}

	if s.SetupToken != "" {
		out = append(out, "", " "+headStyle.Render(T.SetupTitle), " "+T.SetupText, "   "+selectStyle.Render(s.SetupToken), " "+mutedStyle.Render(T.SetupCLI))
	}
	if m.resetLink != "" {
		out = append(out, "", " "+headStyle.Render(T.ResetLinkTitle), "   "+selectStyle.Render(m.resetLink))
	}

	out = append(out, "", " "+headStyle.Render(fmt.Sprintf("%s (%d)", T.Agents, len(s.Agents))))
	if len(s.Agents) == 0 {
		out = append(out, "   "+mutedStyle.Render(T.NoAgents))
	}
	now := m.opt.System.Now
	stateW := 9
	for _, a := range s.Agents {
		stateW = max(stateW, lipgloss.Width(stateWord(a.Status)))
	}
	for _, a := range s.Agents {
		at := time.Time{}
		if now != nil {
			at = now()
		}
		style := goodStyle
		if a.Status != "connected" {
			style = warnStyle
		}
		out = append(out, fmt.Sprintf("   %-14s %s  %-10s %s", a.ID, style.Render(fmt.Sprintf("%-*s", stateW, stateWord(a.Status))), heartbeat(a, at, T), mutedStyle.Render(a.Hostname)))
	}
	if s.Running && s.PluginsEnabled {
		out = append(out, m.pluginsBody()...)
	}
	return out
}

// pluginsBody is the section of the Status tab about the hub's plugins,
// shown only while the hub runs with a plugins directory.
func (m Model) pluginsBody() []string {
	s := m.snap
	out := []string{"", " " + headStyle.Render(fmt.Sprintf("%s (%d)", T.Plugins, len(s.Plugins)))}
	if s.PluginsProblem != "" {
		out = append(out, "   "+badStyle.Render(s.PluginsProblem))
	} else if len(s.Plugins) == 0 {
		out = append(out, "   "+mutedStyle.Render(T.NoPlugins))
	}
	for _, p := range s.Plugins {
		style := warnStyle
		switch p.State {
		case plugin.StateRunning:
			style = goodStyle
		case plugin.StateFailed, plugin.StateIncompatible, plugin.StateInvalid:
			style = badStyle
		}
		line := fmt.Sprintf("   %-20s %-10s %s  %s %d", p.ID, p.Version, style.Render(fmt.Sprintf("%-14s", pluginState(p.State))), T.PluginRestarts, p.Restarts)
		if p.LastError != "" {
			line += "  " + mutedStyle.Render(p.LastError)
		}
		out = append(out, line)
	}
	return out
}

func (m Model) settingsBody(w int) []string {
	entries := m.snap.Settings
	hdr := T.SettingHeader
	out := []string{" " + mutedStyle.Render(fmt.Sprintf("  %-22s %-26s %s", hdr[0], hdr[1], hdr[2]))}
	for i, e := range entries {
		marker := "  "
		line := fmt.Sprintf("%-22s %-26s %s", e.Key, ansi.Truncate(displayValue(e.Value), 26, "…"), originWord(e.Origin))
		if i == m.sel {
			marker = m.sym("▸ ", "> ")
			line = selectStyle.Render(line)
		}
		out = append(out, " "+marker+line)
	}
	if len(entries) == 0 {
		return out
	}

	e := entries[m.sel]
	def, _ := config.Default().Get(e.Key)
	out = append(out, "", " "+headStyle.Render(e.Key)+mutedStyle.Render("  "+T.Default+": "+displayValue(def)))
	out = append(out, " "+describe(e))
	apply := T.AppliesNow
	if e.Restart {
		apply = T.AppliesRestart
	}
	out = append(out, " "+mutedStyle.Render(apply))
	if len(e.Choices) > 0 {
		out = append(out, " "+mutedStyle.Render(T.Choices+": "+strings.Join(e.Choices, ", ")))
	}
	if e.Origin == config.OriginEnv {
		out = append(out, " "+warnStyle.Render(T.EnvOverride))
	}
	if e.Origin == config.OriginDashboard {
		out = append(out, " "+mutedStyle.Render(T.AgentListenDashboard))
	}
	if m.editing {
		out = append(out, "", " "+headStyle.Render(T.Editing+" "+e.Key), " "+m.input.View())
		if m.editErr != "" {
			out = append(out, " "+badStyle.Render(m.sym("✗ ", "x ")+m.editErr))
		} else {
			out = append(out, " "+goodStyle.Render(m.sym("✓ ", "ok ")+T.Valid+" "+displayValue(m.editVal)))
		}
	}
	_ = w
	return out
}

var levelRE = regexp.MustCompile(`(?i)(?:level=|"level":\s*")(debug|info|warn|warning|error)`)

// logSeverity ranks the level of a log line: 0 debug, 1 info, 2 warn, 3 error,
// -1 when the line has none.
func logSeverity(line string) int {
	m := levelRE.FindStringSubmatch(line)
	if m == nil {
		return -1
	}
	switch strings.ToLower(m[1]) {
	case "debug":
		return 0
	case "info":
		return 1
	case "warn", "warning":
		return 2
	}
	return 3
}

func filterLogs(lines []string, level int) []string {
	if level == 0 {
		return lines
	}
	var out []string
	for _, l := range lines {
		if s := logSeverity(l); s >= level {
			out = append(out, l)
		}
	}
	return out
}

func (m Model) logsBody() []string {
	head := " " + mutedStyle.Render(T.LogFilters[m.logLevel]+"  (f)")
	if m.logScroll == 0 {
		head += mutedStyle.Render("  " + T.Following)
	}
	out := []string{head}
	if m.logErr != "" {
		return append(out, " "+warnStyle.Render(T.LogsUnavailable+": "+m.logErr))
	}
	lines := filterLogs(m.logs, m.logLevel)
	if len(lines) == 0 {
		return append(out, " "+mutedStyle.Render(T.LogsEmpty))
	}
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

func (m Model) healthBody() []string {
	var out []string
	width := 16
	for _, c := range m.snap.Health {
		width = max(width, lipgloss.Width(c.Title)+1)
	}
	for _, c := range m.snap.Health {
		var mark string
		switch c.Status {
		case OK:
			mark = goodStyle.Render(m.sym("✓", "ok"))
		case Warn:
			mark = warnStyle.Render(m.sym("!", "!!"))
		case Fail:
			mark = badStyle.Render(m.sym("✗", "xx"))
		default:
			mark = mutedStyle.Render(m.sym("·", "--"))
		}
		out = append(out, fmt.Sprintf(" %s  %s  %s", mark, headStyle.Render(fmt.Sprintf("%-*s", width, c.Title)), c.Text))
	}
	return out
}

func clamp(v, lo, hi int) int {
	return max(lo, min(v, hi))
}

// action is a line of the Actions tab.
type action struct {
	key, title, info string
	// why is set when the action cannot run here, and says why.
	why string
}

// actions lists what the console can do, in the order of the Actions tab.
func (m Model) actions() []action {
	restart := action{key: "r", title: T.ActRestart, info: T.ActRestartInfo}
	if m.opt.Hooks.Restart == nil {
		restart.why = fmt.Sprintf(T.WhyRestart, ServiceUnit)
	}
	reset := action{key: "p", title: T.ActReset, info: T.ActResetInfo}
	if m.opt.Hooks.ResetAdmin == nil {
		reset.why = T.WhyReset
	}
	agents := action{key: "a", title: T.ActAgents, info: T.ActAgentsInfo, why: m.agentsBlocker()}
	return []action{restart, reset, agents}
}

// agentsBlocker says why remote agents cannot be turned on from here, or ""
// when they can.
func (m Model) agentsBlocker() string {
	switch {
	case !m.loaded:
		return T.Loading
	case m.snap.AgentListen != "":
		return fmt.Sprintf(T.WhyAgentsOn, m.snap.AgentListen)
	case m.snap.ConfigFile == "":
		return T.WhyNoConfig
	}
	for _, e := range m.snap.Settings {
		if e.Key == "agent_listen" && e.Origin == config.OriginEnv {
			return fmt.Sprintf(T.WhyEnvAgents, e.Env)
		}
	}
	return ""
}

// ask asks to confirm the action with key k, or says why it cannot run.
func (m Model) ask(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "r":
		m.confirm = "restart"
	case "p":
		if m.opt.Hooks.ResetAdmin != nil {
			m.confirm = "reset"
		}
	case "a":
		if why := m.agentsBlocker(); why != "" {
			m.say(T.ActAgents+": "+why+".", true)
			return m, nil
		}
		m.confirm = "agents"
	}
	return m, nil
}

func (m Model) actionsBody() []string {
	out := []string{" " + mutedStyle.Render(T.ActionsIntro), ""}
	for i, a := range m.actions() {
		marker := "  "
		title := fmt.Sprintf("%s  %s", a.key, a.title)
		if i == m.actSel {
			marker = m.sym("▸ ", "> ")
			title = selectStyle.Render(title)
		}
		out = append(out, " "+marker+title)
		out = append(out, "      "+mutedStyle.Render(a.info))
		if a.why != "" {
			out = append(out, "      "+warnStyle.Render(T.ActUnavailable+": "+a.why))
		}
	}

	if m.snap.AgentListen == "" {
		return out
	}
	out = append(out, "", " "+headStyle.Render(T.NewAgentTitle), " "+T.NewAgentText)
	_, port, err := net.SplitHostPort(m.snap.AgentListen)
	if err != nil {
		port = strings.TrimPrefix(m.snap.AgentListen, ":")
	}
	for _, u := range m.snap.LANURLs {
		if p, err := url.Parse(u); err == nil && p.Hostname() != "" {
			out = append(out, kv(T.NewAgentAddress, net.JoinHostPort(p.Hostname(), port)))
		}
	}
	fp := m.snap.CAFingerprint
	if fp == "" {
		fp = mutedStyle.Render(T.FingerprintLater)
	}
	out = append(out, kv(T.NewAgentFingerprint, fp))
	out = append(out, " "+mutedStyle.Render(T.NewAgentToken))
	return out
}
