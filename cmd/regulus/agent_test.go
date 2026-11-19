package main

import (
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/plugin"
)

func TestPluginReport(t *testing.T) {
	since := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := pluginReport([]plugin.Status{
		{ID: "a", Version: "1.0.0", State: plugin.StateRunning, PID: 42, Since: since},
		{ID: "b", Version: "2.0.0", State: plugin.StateFailed, Restarts: 6, LastError: "exit status 1"},
	})
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if a := got[0]; a.GetId() != "a" || a.GetState() != "running" || !a.GetSince().AsTime().Equal(since) {
		t.Errorf("a = %v", a)
	}
	if b := got[1]; b.GetRestarts() != 6 || b.GetLastError() != "exit status 1" || b.GetSince() != nil {
		t.Errorf("b = %v", b)
	}
}
