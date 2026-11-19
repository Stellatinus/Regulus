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
