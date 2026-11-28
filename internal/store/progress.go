package store

import (
	"path/filepath"
	"sync"
	"time"
)

// Steps of an install, update or removal, in the order the administrator sees
// them (#530). The dashboard has a text for each, in English and Italian.
//
// StepTesting and StepRollingBack are in the vocabulary so the dashboard has
// their texts, but nothing reports them yet: the hub does not test a plugin
// after installing it and there is no rollback (#517). A step is reported only
// when the code really is in it.
const (
	StepDownloading = "downloading"  // the hub fetches the package; Percent is real
	StepVerifying   = "verifying"    // the root helper checks the signed index and the package
	StepInstalling  = "installing"   // the helper unpacks and puts the version in place
	StepRemoving    = "removing"     // the helper deletes the plugin
	StepTesting     = "testing"      // not reported yet
	StepRollingBack = "rolling_back" // not reported yet (#517)
)

// States of a Progress.
const (
	ProgressRunning = "running"
	ProgressDone    = "done"
	ProgressFailed  = "failed"
)

// progressName is the file in ApplyDir where the helper says which step it is
// in. The hub reads it while it waits and republishes it with its own steps.
const progressName = "progress.json"

// finishedKeep is how long a finished operation stays readable, so an
// administrator who opens the Store a moment later still sees how it ended.
const finishedKeep = time.Minute

// Progress is where an install, update or removal is. The hub keeps the one
// in progress (or the last one, for a minute) and GET /api/store/progress
// returns it; the helper writes the part it knows to progress.json.
type Progress struct {
	// RequestID ties the helper's file to the request it is for.
	RequestID string `json:"request_id,omitempty"`
	Plugin    string `json:"plugin"`
	Action    string `json:"action"`
	Version   string `json:"version,omitempty"`
	State     string `json:"state"`
	Step      string `json:"step,omitempty"`
	// Percent is 0 to 100 while the size is known (the download) and -1 when
	// it is not: the dashboard then shows an indeterminate bar.
	Percent int `json:"percent"`
	// Code and Helper say why it failed (the codes of the answer, never text
	// to show); Message is English for the log and for people.
	Code    string    `json:"code,omitempty"`
	Helper  string    `json:"helper_code,omitempty"`
	Message string    `json:"message,omitempty"`
	Started time.Time `json:"started_at"`
	At      time.Time `json:"at"`
}

// writeProgress is the helper's: readable by the hub's user.
func writeProgress(dir string, p Progress) error {
	return writeJSON(filepath.Join(dir, progressName), p, 0o644)
}

// ReadProgress returns what the helper last wrote.
func ReadProgress(dir string) (Progress, bool) {
	var p Progress
	return p, readJSON(filepath.Join(dir, progressName), &p)
}

// tracker holds the hub's view of the operation in progress.
type tracker struct {
	mu  sync.Mutex
	cur *Progress
	now func() time.Time
}

func (t *tracker) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *tracker) begin(plugin, action, version string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock().UTC()
	t.cur = &Progress{Plugin: plugin, Action: action, Version: version, State: ProgressRunning, Percent: -1, Started: now, At: now}
}

// step moves to a step; percent is -1 when unknown.
func (t *tracker) step(step string, percent int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur == nil || t.cur.State != ProgressRunning {
		return
	}
	if t.cur.Step == step && t.cur.Percent == percent {
		return
	}
	t.cur.Step, t.cur.Percent, t.cur.At = step, percent, t.clock().UTC()
}

func (t *tracker) finish(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur == nil {
		return
	}
	t.cur.At, t.cur.Step, t.cur.Percent = t.clock().UTC(), "", -1
	if err == nil {
		t.cur.State = ProgressDone
		return
	}
	t.cur.State = ProgressFailed
	t.cur.Message = err.Error()
	t.cur.Code = ErrCodeHelperFailed
	if e, ok := err.(*Error); ok {
		t.cur.Code, t.cur.Helper = e.CodeStr, e.Helper
	}
}

// get returns the operation in progress, or the last one for a minute after it
// ended, or false.
func (t *tracker) get() (Progress, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur == nil {
		return Progress{}, false
	}
	if t.cur.State != ProgressRunning && t.clock().Sub(t.cur.At) > finishedKeep {
		t.cur = nil
		return Progress{}, false
	}
	return *t.cur, true
}
