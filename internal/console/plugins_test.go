package console

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CodexCircle/Regulus/internal/localstatus"
)

// pluginSystem is fakeSystem with a hub that runs two plugins (#262).
func pluginSystem() System {
	sys := fakeSystem()
	fetch := sys.Fetch
	sys.Fetch = func(ctx context.Context, dir string) (localstatus.Status, error) {
		st, err := fetch(ctx, dir)
		st.PluginsEnabled = true
		st.Plugins = []Plugin{
			{ID: "example-hello", Version: "0.1.0", State: "running", Since: fixedNow.Add(-time.Hour)},
			{ID: "crasher", Version: "0.0.1", State: "failed", Restarts: 5, LastError: "exit status 7"},
		}
		return st, err
	}
	return sys
}

func TestStatusShowsTheHubsPlugins(t *testing.T) {
	defer func() { T = en }()
	snap := Collect(context.Background(), pluginSystem(), "testdata/config.yaml", "v0.3.0")
	if !snap.PluginsEnabled || len(snap.Plugins) != 2 {
		t.Fatalf("snapshot plugins = %v %+v", snap.PluginsEnabled, snap.Plugins)
	}

	var text bytes.Buffer
	if err := WriteStatus(&text, snap, FormatText, fixedNow); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Plugins of the hub (2)", "example-hello", "running", "failed", "restarts 5", "exit status 7"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text output lacks %q:\n%s", want, text.String())
		}
	}

	var plain bytes.Buffer
	if err := WriteStatus(&plain, snap, FormatPlain, fixedNow); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"plugins=2\n", "plugin.crasher=failed 0.0.1 restarts=5\n", "plugin.example-hello=running 0.1.0 restarts=0\n"} {
		if !strings.Contains(plain.String(), want) {
			t.Errorf("plain output lacks %q:\n%s", want, plain.String())
		}
	}

	T = it
	text.Reset()
	if err := WriteStatus(&text, snap, FormatText, fixedNow); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Plugin dell'hub (2)", "in esecuzione", "fallito", "riavvii 5"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("Italian text output lacks %q:\n%s", want, text.String())
		}
	}
}

func TestTheStatusTabShowsTheHubsPlugins(t *testing.T) {
	m := New(Options{System: pluginSystem(), ConfigFile: "testdata/config.yaml", Version: "v0.3.0", Unicode: true})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 60})
	m = settle(t, next.(Model), next.(Model).collect())
	view := m.View()
	for _, want := range []string{"Plugins of the hub (2)", "example-hello", "crasher", "exit status 7"} {
		if !strings.Contains(view, want) {
			t.Errorf("the Status tab lacks %q:\n%s", want, view)
		}
	}
}

// Without a plugins directory the section is not shown at all.
func TestNoPluginSectionWithoutAPluginsDirectory(t *testing.T) {
	snap := Collect(context.Background(), fakeSystem(), "testdata/config.yaml", "v0.3.0")
	var text bytes.Buffer
	if err := WriteStatus(&text, snap, FormatText, fixedNow); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text.String(), T.Plugins) {
		t.Errorf("a plugin section without plugins_dir:\n%s", text.String())
	}
}

func pluginCheck(snap Snapshot) (Check, bool) {
	for _, c := range snap.Health {
		if c.Key == "plugins" {
			return c, true
		}
	}
	return Check{}, false
}

// A failed or invalid plugin of the hub makes the Health tab warn, in English
// and Italian; --json and --plain keep English (#319).
func TestHealthWarnsAboutAFailedHubPlugin(t *testing.T) {
	defer func() { T = en }()
	sys := pluginSystem()
	fetch := sys.Fetch
	sys.Fetch = func(ctx context.Context, dir string) (localstatus.Status, error) {
		st, err := fetch(ctx, dir)
		st.Plugins = append(st.Plugins, Plugin{ID: "broken", State: "invalid", LastError: "plugin.yaml: version is required"})
		return st, err
	}
	for _, tc := range []struct {
		table      strs
		title, txt string
	}{
		{en, "Plugins", "need a look (see the Status tab): crasher (failed), broken (invalid)"},
		{it, "Plugin", "da controllare (vedi la scheda Stato): crasher (fallito), broken (non valido)"},
	} {
		T = tc.table
		snap := Collect(context.Background(), sys, "testdata/config.yaml", "v0.3.0")
		c, ok := pluginCheck(snap)
		if !ok || c.Status != Warn || c.Title != tc.title || c.Text != tc.txt {
			t.Errorf("check = %+v", c)
		}
		if c.Name != "Plugins" || c.Detail != "need a look (see the Status tab): crasher (failed), broken (invalid)" {
			t.Errorf("the English fields changed with the language: %+v", c)
		}
	}

	// All running: ok. No plugin: info. The directory cannot be read: warn.
	T = en
	ok := fakeSystem()
	fetchOK := ok.Fetch
	ok.Fetch = func(ctx context.Context, dir string) (localstatus.Status, error) {
		st, err := fetchOK(ctx, dir)
		st.PluginsEnabled = true
		st.Plugins = []Plugin{{ID: "example-hello", Version: "0.1.0", State: "running"}, {ID: "x", State: "restarting"}}
		return st, err
	}
	if c, _ := pluginCheck(Collect(context.Background(), ok, "testdata/config.yaml", "v0.3.0")); c.Status != OK {
		t.Errorf("running plugins: %+v", c)
	}
	ok.Fetch = func(ctx context.Context, dir string) (localstatus.Status, error) {
		st, err := fetchOK(ctx, dir)
		st.PluginsEnabled, st.PluginsProblem = true, "reading the plugins directory: not a directory"
		return st, err
	}
	if c, _ := pluginCheck(Collect(context.Background(), ok, "testdata/config.yaml", "v0.3.0")); c.Status != Warn || !strings.Contains(c.Text, "not a directory") {
		t.Errorf("unreadable directory: %+v", c)
	}
	// No plugins directory: no check at all.
	if _, found := pluginCheck(Collect(context.Background(), fakeSystem(), "testdata/config.yaml", "v0.3.0")); found {
		t.Error("a plugins check without plugins_dir")
	}
}
