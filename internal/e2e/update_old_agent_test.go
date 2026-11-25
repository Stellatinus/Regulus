package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/update"
)

// TestUpdateOfAnOlderAgentOverMTLS applies a real update to the real agent of
// an older release, over the real mTLS listener (#384, left over from #341).
// It needs two published releases:
//
//   - the agent is the binary of the older one;
//   - the hub is built from this source with the version of the newer one, so
//     it is the hub of that release as far as the update goes: a rollout makes
//     it download the newer release from the project's releases and check its
//     Sigstore signature against the embedded trust root (a hub built with any
//     other version would serve nothing it had verified);
//   - the old agent is told to update over the stream, fetches the signed
//     archive from the hub, verifies it, and leaves its request for the helper
//     (update.ApplyDir of its data directory). Nothing installs it: the helper
//     is a systemd unit, which this test does not have, so the agent stays at
//     the old version and the rollout stays "running" until the test ends.
//
// It skips, with the reason, while there is only one release, no network, or
// GitHub's rate limit; CI treats the skip as a warning, like the other tests of
// older releases. It also skips when the old agent does not trust the identity
// that signed the newer release (the repository moved from one organization to
// another between v0.1.0-alpha.1 and v0.2.0-alpha.1: issue #392). REGULUS_E2E_OLD_BIN
// does not apply: the test needs both releases to exist.
//
// Not verified: the hand-over itself (the request for the helper) has not run
// against two real releases, because the only two published cannot update one
// another; the same flow runs in process in TestRolloutUpdatesAnAgentBehind.
func TestUpdateOfAnOlderAgentOverMTLS(t *testing.T) {
	if os.Getenv("REGULUS_E2E") != "1" {
		t.Skip("set REGULUS_E2E=1 to run the end-to-end test (make e2e)")
	}
	rels, err := publishedReleases()
	if err != nil {
		t.Skipf("no releases to test with (%v)", err)
	}
	if len(rels) < 2 {
		t.Skipf("only %d release is published: an update needs an older agent and a newer release to apply", len(rels))
	}
	newer, older := rels[0], rels[1]
	newerVersion := strings.TrimPrefix(newer.Tag, "v")
	nv, err1 := update.ParseVersion(newerVersion)
	ov, err2 := update.ParseVersion(strings.TrimPrefix(older.Tag, "v"))
	if err1 != nil || err2 != nil || nv.Compare(ov) <= 0 {
		t.Skipf("the two newest releases (%s, %s) are not an older and a newer version", newer.Tag, older.Tag)
	}
	oldBin, oldVersion := downloadRelease(t, older)
	t.Logf("old agent: release %s; hub: this source as %s", oldVersion, newerVersion)

	hubBin := hubBinaryOf(t, newerVersion)
	dir := t.TempDir()
	hub := startHubEnv(t, hubBin, filepath.Join(dir, "hub.log"),
		// The hub must be allowed to look for releases: this one downloads.
		[]string{"REGULUS_UPDATES_CHECK=true"},
		"hub", "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:"+freePort(t),
		"--data-dir", filepath.Join(dir, "hub"), "--builtin-agent=false", "--metrics-interval", "1s")
	api := newAPI(t, hub.httpAddr)
	api.setup(hub.setupToken)
	token, fingerprint := api.newToken("old")

	agentDir := filepath.Join(dir, "agent")
	ag := startEnv(t, oldBin, filepath.Join(dir, "agent.log"), []string{"REGULUS_AGENT_UPDATES=true"},
		"agent", "--hub", hub.agentAddr, "--token", token, "--ca-fingerprint", fingerprint, "--data-dir", agentDir)
	id := api.waitConnected(t, "", ag)

	var status struct {
		Target     string `json:"target"`
		State      string `json:"state"`
		Candidates []struct {
			ID   string `json:"id"`
			From string `json:"from"`
		} `json:"candidates"`
	}
	api.getJSON("/api/updates/rollout", &status)
	if status.Target != newerVersion || len(status.Candidates) != 1 || status.Candidates[0].ID != id {
		t.Fatalf("the old agent is not a candidate of the rollout: %+v (agent %s)", status, id)
	}

	// Starting a rollout needs a recent proof of the password (step-up).
	api.do("POST", "/api/account/reauth", map[string]string{"password": password}, 200)
	// The hub asks GitHub for the release without a token, so it can find the
	// request limit used up even when this test could still download: that is
	// the same skip as above, not a failure.
	if code, out := api.send("POST", "/api/updates/rollout", map[string]any{"wave_size": 1}); code != 202 {
		if code == 502 && strings.Contains(string(out), `"update_upstream"`) && strings.Contains(string(out), "request limit") {
			t.Skipf("GitHub's rate limit: %s", out)
		}
		t.Fatalf("POST /api/updates/rollout answered %d, want 202: %s", code, out)
	}

	// The agent has fetched the signed archive over mTLS and verified it, and
	// has left its request for the helper.
	applyDir := update.ApplyDir(agentDir)
	deadline := time.Now().Add(3 * time.Minute)
	var req update.ApplyRequest
	for {
		if r, ok := update.ReadRequest(applyDir); ok {
			req = r
			break
		}
		if ag.exited() {
			t.Fatalf("the old agent exited with %d:\n%s", ag.code, ag.log())
		}
		// The rollout stops at the first failure: do not wait for a request
		// that will not come.
		api.getJSON("/api/updates/rollout", &status)
		if status.State == "failed" {
			log := ag.log()
			if strings.Contains(log, "no matching CertificateIdentity") {
				// The old agent trusts the signing identity of the release
				// workflow of its own time. When the repository moved (the
				// organization changed between the two releases) it cannot
				// verify the newer release, and no update can reach it.
				t.Skipf("the old agent %s does not trust the identity that signed %s, so it cannot apply it (issue #392); the update itself is not tested until two releases signed by the same identity exist:\n%s", oldVersion, newerVersion, lastLine(log, "cannot update"))
			}
			t.Fatalf("the rollout failed:\n%s\nagent log:\n%s", hub.log(), log)
		}
		if hub.exited() {
			t.Fatalf("the hub exited with %d:\n%s", hub.code, hub.log())
		}
		if time.Now().After(deadline) {
			api.getJSON("/api/updates/rollout", &status)
			t.Fatalf("no request for the helper within 3 minutes; rollout: %+v\nagent log:\n%s", status, ag.log())
		}
		time.Sleep(500 * time.Millisecond)
	}
	if req.Version != newerVersion || req.RequestedBy != "hub" {
		t.Errorf("the request is %+v, want version %s requested by the hub", req, newerVersion)
	}
	archive := "regulus_" + newerVersion + "_linux_" + runtime.GOARCH + ".tar.gz"
	if fi, err := os.Stat(filepath.Join(agentDir, "updates", newerVersion, archive)); err != nil || fi.Size() == 0 {
		t.Errorf("the agent did not keep the verified archive %s: %v", archive, err)
	}

	// The hub says the server is being updated, not done: the helper that
	// installs it does not exist here.
	var after struct {
		State  string `json:"state"`
		Agents []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"agents"`
	}
	raw := api.do("GET", "/api/updates/rollout", nil, 200)
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if after.State != "running" || len(after.Agents) != 1 || after.Agents[0].Status == "updated" || after.Agents[0].Status == "failed" {
		t.Errorf("rollout after the hand-over: %s", raw)
	}
	api.do("POST", "/api/updates/rollout/stop", struct{}{}, 200)
	hub.stop(t)
}

// lastLine is the last line of text that holds substr.
func lastLine(text, substr string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], substr) {
			return lines[i]
		}
	}
	return ""
}
