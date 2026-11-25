package console

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/CodexCircle/Regulus/internal/config"
	"github.com/CodexCircle/Regulus/internal/plugin"
)

// Format is how a command prints.
type Format int

const (
	// FormatText is for people: aligned, with headings.
	FormatText Format = iota
	// FormatPlain is stable, one fact per line (key=value), for grep and awk.
	FormatPlain
	// FormatJSON is for programs.
	FormatJSON
)

// WriteStatus prints the snapshot.
func WriteStatus(w io.Writer, snap Snapshot, f Format, now time.Time) error {
	switch f {
	case FormatJSON:
		return writeJSON(w, snap)
	case FormatPlain:
		return writeStatusPlain(w, snap, now)
	}
	return writeStatusText(w, snap, now)
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func state(snap Snapshot) string {
	if snap.Running {
		return T.Running
	}
	return T.Stopped
}

func writeStatusText(w io.Writer, snap Snapshot, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	row := func(k, v string) { _, _ = fmt.Fprintf(tw, "%s\t%s\n", k, v) }

	hub := state(snap)
	if snap.Running && snap.UptimeText != "" {
		hub += ", " + T.Uptime + " " + snap.UptimeText
	}
	row(T.Hub, hub+" ("+T.Service+": "+stateWord(snap.Service)+")")
	row(T.Version, snap.Version)
	row(T.Dashboard, snap.URL)
	for _, u := range snap.LANURLs {
		row("", T.AlsoOn+" "+u)
	}
	row(T.AgentPort, orDash(snap.AgentListen, T.Off))
	row(T.BuiltinAgent, onOff(snap.BuiltinAgent))
	data := snap.DataDir
	if snap.DatabaseSize > 0 {
		data += " (" + T.Database + " " + FormatBytes(snap.DatabaseSize) + ")"
	}
	row(T.DataDir, data)
	if strings.HasPrefix(snap.Database, "PostgreSQL") {
		row(T.Database, snap.Database)
	}
	row(T.ConfigFile, orDash(snap.ConfigFile, T.None))
	if snap.CertSoonest != nil {
		row(T.Certificates, describeCert(T, *snap.CertSoonest, now))
	}
	if snap.Problem != "" {
		row(T.Problem, snap.Problem)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if snap.SetupToken != "" {
		_, _ = fmt.Fprintf(w, "\n%s: %s\n  %s\n", T.SetupTitle, T.SetupText, snap.SetupToken)
		_, _ = fmt.Fprintf(w, "  %s\n", T.SetupCLI)
	}
	if snap.Running {
		_, _ = fmt.Fprintf(w, "\n%s (%d)\n", T.Agents, len(snap.Agents))
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, a := range snap.Agents {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", a.ID, stateWord(a.Status), heartbeat(a, now, T), a.Hostname)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if snap.Running && snap.PluginsEnabled {
		_, _ = fmt.Fprintf(w, "\n%s (%d)\n", T.Plugins, len(snap.Plugins))
		for _, line := range pluginLines(snap) {
			_, _ = fmt.Fprintln(w, "  "+line)
		}
	}

	_, _ = fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range snap.Health {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", statusWord(c.Status), c.Title, c.Text)
	}
	return tw.Flush()
}

func writeStatusPlain(w io.Writer, snap Snapshot, now time.Time) error {
	p := func(k string, v any) { _, _ = fmt.Fprintf(w, "%s=%v\n", k, v) }
	p("version", snap.Version)
	p("service", snap.Service)
	p("running", snap.Running)
	p("listen", snap.Listen)
	p("port", snap.Port)
	p("url", snap.URL)
	for _, u := range snap.LANURLs {
		p("lan_url", u)
	}
	p("agent_listen", snap.AgentListen)
	p("data_dir", snap.DataDir)
	p("database_bytes", snap.DatabaseSize)
	p("builtin_agent", snap.BuiltinAgent)
	p("config_file", snap.ConfigFile)
	if snap.Problem != "" {
		p("problem", snap.Problem)
	}
	if snap.Running {
		p("uptime", snap.Uptime)
	}
	if snap.SetupToken != "" {
		p("setup_token", snap.SetupToken)
	}
	if c := snap.CertSoonest; c != nil {
		p("cert_soonest", strings.TrimSpace(c.Kind+" "+c.AgentID)+" "+c.NotAfter.UTC().Format(time.RFC3339))
	}
	for _, c := range snap.CertWarnings {
		p("cert_warning", strings.TrimSpace(c.Kind+" "+c.AgentID)+" "+c.NotAfter.UTC().Format(time.RFC3339))
	}
	p("agents", len(snap.Agents))
	for _, a := range snap.Agents {
		p("agent."+a.ID, a.Status+" "+heartbeat(a, now, en))
	}
	if snap.PluginsEnabled {
		p("plugins", len(snap.Plugins))
		if snap.PluginsProblem != "" {
			p("plugins_problem", snap.PluginsProblem)
		}
		for _, pl := range snap.Plugins {
			p("plugin."+pl.ID, fmt.Sprintf("%s %s restarts=%d", pl.State, pl.Version, pl.Restarts))
		}
	}
	for _, c := range snap.Health {
		p("check."+c.Key, c.Status+" "+c.Detail)
	}
	return nil
}

// WriteSettings prints the settings with their values and sources.
func WriteSettings(w io.Writer, entries []config.Entry, f Format) error {
	switch f {
	case FormatJSON:
		type item struct {
			Key         string   `json:"key"`
			Value       string   `json:"value"`
			Source      string   `json:"source"`
			Default     string   `json:"default"`
			Description string   `json:"description"`
			Choices     []string `json:"choices,omitempty"`
			Restart     bool     `json:"restart_required"`
		}
		def := config.Default()
		out := make([]item, 0, len(entries))
		for _, e := range entries {
			d, _ := def.Get(e.Key)
			out = append(out, item{e.Key, e.Value, e.Origin, d, e.Description, e.Choices, e.Restart})
		}
		return writeJSON(w, out)
	case FormatPlain:
		for _, e := range entries {
			_, _ = fmt.Fprintf(w, "%s=%s\n", e.Key, e.Value)
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", strings.ToUpper(T.SettingHeader[0]), strings.ToUpper(T.SettingHeader[1]), strings.ToUpper(T.SettingHeader[2]))
	for _, e := range entries {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Key, displayValue(e.Value), originWord(e.Origin))
	}
	return tw.Flush()
}

func displayValue(v string) string {
	if v == "" {
		return T.Empty
	}
	return v
}

func orDash(v, empty string) string {
	if v == "" {
		return empty
	}
	return v
}

func onOff(b bool) string {
	if b {
		return T.On
	}
	return T.Off
}

// pluginState is a plugin state in the console's language; a state this
// console does not know is shown as it is.
func pluginState(s string) string { return pluginStateIn(T, s) }

// pluginStateIn is a plugin state in the language of t.
func pluginStateIn(t strs, s string) string {
	switch s {
	case plugin.StateStarting:
		return t.PluginStarting
	case plugin.StateRunning:
		return t.PluginRunning
	case plugin.StateRestarting:
		return t.PluginRestarting
	case plugin.StateFailed:
		return t.PluginFailed
	case plugin.StateIncompatible:
		return t.PluginIncompatible
	case plugin.StateStopped:
		return t.PluginStopped
	case plugin.StateInvalid:
		return t.PluginInvalid
	}
	return s
}

// pluginLines describes the hub's plugins, one line each (and one for a
// problem that keeps them all from running), for the text output and the TUI.
func pluginLines(snap Snapshot) []string {
	var out []string
	if snap.PluginsProblem != "" {
		out = append(out, snap.PluginsProblem)
	}
	if len(snap.Plugins) == 0 && snap.PluginsProblem == "" {
		out = append(out, T.NoPlugins)
	}
	for _, p := range snap.Plugins {
		line := fmt.Sprintf("%-20s %-10s %-14s %s %d", p.ID, p.Version, pluginState(p.State), T.PluginRestarts, p.Restarts)
		if p.LastError != "" {
			line += "  " + p.LastError
		}
		out = append(out, line)
	}
	return out
}

// heartbeat describes when an agent was last heard from, in the language of s.
func heartbeat(a Agent, now time.Time, s strs) string {
	if a.LastHeartbeat.IsZero() {
		return "-"
	}
	return fmt.Sprintf(s.Ago, formatDuration(now.Sub(a.LastHeartbeat), s))
}

// stateWord is a state of the service or of an agent in the console language.
func stateWord(v string) string {
	if w := T.States[v]; w != "" {
		return w
	}
	return v
}

// statusWord is the status of a health check in the console language.
func statusWord(status string) string {
	switch status {
	case OK:
		return T.StatusOK
	case Warn:
		return T.StatusWarn
	case Fail:
		return T.StatusFail
	case Info:
		return T.StatusInfo
	}
	return status
}

// originWord says where the value of a setting comes from.
func originWord(origin string) string {
	switch origin {
	case config.OriginDefault:
		return T.OriginDefault
	case config.OriginFile:
		return T.OriginFile
	case config.OriginEnv:
		return T.OriginEnv
	case config.OriginDashboard:
		return T.OriginDashboard
	}
	return origin
}

// describe is the description of a setting in the console language.
func describe(e config.Entry) string {
	if d := T.SettingInfo[e.Key]; d != "" {
		return d
	}
	return e.Description
}
