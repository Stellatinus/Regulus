package e2e

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/update"
)

// TestAgentOfAnOlderRelease runs the agent of the previous published release
// (a real binary, downloaded from the project's releases) against the hub
// built from this source (#341). The hub is built with a release version one
// minor ahead of the old agent, and two ahead, so that both sides of the
// support rule are met by a real old binary and not only by a simulated one:
//
//   - one minor behind: the agent is admitted as a normal agent, reports
//     metrics, and the API says its release is "behind";
//   - two minors behind: it is admitted to an update-only session (#212): the
//     API says "too_old" and update_only, and it sends no metrics.
//
// It skips, with the reason, when there is no release to download (a
// repository without releases, no network, or GitHub's rate limit): set
// REGULUS_E2E_OLD_BIN and REGULUS_E2E_OLD_VERSION to use a binary of your own.
// The download is checked against the release's checksums.txt, but not against
// its Sigstore signature: this only decides what the test runs, not what any
// hub installs.
func TestAgentOfAnOlderRelease(t *testing.T) {
	if os.Getenv("REGULUS_E2E") != "1" {
		t.Skip("set REGULUS_E2E=1 to run the end-to-end test (make e2e)")
	}
	old, version := olderRelease(t)
	v, err := update.ParseVersion(version)
	if err != nil {
		t.Fatalf("the version %q of the old release cannot be read: %v", version, err)
	}
	t.Logf("old agent: release %s, %s", version, old)

	for _, tc := range []struct {
		name       string
		minorsUp   uint64
		compat     string
		updateOnly bool
	}{
		{"one minor behind", 1, "behind", false},
		{"two minors behind", 2, "too_old", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hubVersion := fmt.Sprintf("%d.%d.0", v.Major, v.Minor+tc.minorsUp)
			bin := hubBinaryOf(t, hubVersion)
			dir := t.TempDir()
			hub := startHub(t, bin, filepath.Join(dir, "hub.log"),
				"hub", "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:"+freePort(t),
				"--data-dir", filepath.Join(dir, "hub"), "--builtin-agent=false", "--metrics-interval", "1s")
			api := newAPI(t, hub.httpAddr)
			api.setup(hub.setupToken)
			token, fingerprint := api.newToken("old")

			ag := start(t, old, filepath.Join(dir, "agent.log"),
				"agent", "--hub", hub.agentAddr, "--token", token, "--ca-fingerprint", fingerprint, "--data-dir", filepath.Join(dir, "agent"))
			var got struct {
				ID            string     `json:"id"`
				Status        string     `json:"status"`
				Version       string     `json:"version"`
				Compatibility string     `json:"compatibility"`
				UpdateOnly    bool       `json:"update_only"`
				LastHeartbeat *time.Time `json:"last_heartbeat"`
			}
			deadline := time.Now().Add(60 * time.Second)
			for {
				var agents []json.RawMessage
				api.getJSON("/api/agents", &agents)
				if len(agents) == 1 {
					if err := json.Unmarshal(agents[0], &got); err != nil {
						t.Fatal(err)
					}
					if got.Status == "connected" && got.Version != "" {
						break
					}
				}
				if ag.exited() {
					t.Fatalf("the old agent exited with %d:\n%s", ag.code, ag.log())
				}
				if time.Now().After(deadline) {
					t.Fatalf("the old agent was not connected within 60s: %s\n%s", agents, ag.log())
				}
				time.Sleep(200 * time.Millisecond)
			}
			if got.Compatibility != tc.compat || got.UpdateOnly != tc.updateOnly {
				t.Errorf("hub %s, agent %s: compatibility %q update_only %v, want %q %v", hubVersion, got.Version, got.Compatibility, got.UpdateOnly, tc.compat, tc.updateOnly)
			}

			if tc.updateOnly {
				// Admitted to be updated, and nothing else: no metrics are kept.
				time.Sleep(3 * time.Second)
				var m struct {
					Points []json.RawMessage `json:"points"`
				}
				api.getJSON("/api/agents/"+got.ID+"/metrics", &m)
				if len(m.Points) != 0 {
					t.Errorf("an update-only agent has %d metrics points, want none", len(m.Points))
				}
				return
			}
			api.waitMetrics(t, got.ID, 2)
		})
	}
}

// hubBinaryOf builds the hub of this source with a release version, so that
// compatibility with the agents is judged as for a release.
func hubBinaryOf(t *testing.T, version string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "regulus")
	cmd := exec.Command("go", "build", "-race", "-ldflags", "-X main.version="+version, "-o", out, "../../cmd/regulus")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build the hub %s: %v", version, err)
	}
	return out
}

// olderRelease returns the binary of the newest published release and its
// version, or skips the test. REGULUS_E2E_OLD_BIN and REGULUS_E2E_OLD_VERSION
// name one instead.
func olderRelease(t *testing.T) (bin, version string) {
	t.Helper()
	if b := os.Getenv("REGULUS_E2E_OLD_BIN"); b != "" {
		version = strings.TrimPrefix(os.Getenv("REGULUS_E2E_OLD_VERSION"), "v")
		if version == "" {
			t.Fatal("REGULUS_E2E_OLD_BIN needs REGULUS_E2E_OLD_VERSION (for example 0.2.0-alpha.1)")
		}
		abs, err := filepath.Abs(b)
		if err != nil {
			t.Fatal(err)
		}
		return abs, version
	}
	rel, err := newestRelease()
	if err != nil {
		t.Skipf("no previous release to test against (%v); set REGULUS_E2E_OLD_BIN and REGULUS_E2E_OLD_VERSION to use your own", err)
	}
	version = strings.TrimPrefix(rel.Tag, "v")
	archive := fmt.Sprintf("regulus_%s_linux_%s.tar.gz", version, runtime.GOARCH)
	assets := map[string]string{}
	for _, a := range rel.Assets {
		assets[a.Name] = a.URL
	}
	if assets[archive] == "" || assets["checksums.txt"] == "" {
		t.Skipf("the release %s has no %s with a checksums.txt", rel.Tag, archive)
	}
	sums, err := download(assets["checksums.txt"])
	if err != nil {
		t.Skipf("cannot download the checksums of %s: %v", rel.Tag, err)
	}
	tgz, err := download(assets[archive])
	if err != nil {
		t.Skipf("cannot download %s: %v", archive, err)
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == archive {
			want = f[0]
		}
	}
	sum := sha256.Sum256(tgz)
	if want == "" || hex.EncodeToString(sum[:]) != want {
		t.Fatalf("%s does not match its checksum in checksums.txt (want %q, got %x): a corrupted or replaced download is not run", archive, want, sum)
	}
	bin = filepath.Join(t.TempDir(), "regulus-"+version)
	if err := extractBinary(tgz, bin); err != nil {
		t.Fatalf("unpack %s: %v", archive, err)
	}
	return bin, version
}

type release struct {
	Tag    string `json:"tag_name"`
	Draft  bool   `json:"draft"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// newestRelease is the newest published release of the project, a
// pre-release included: the previous release of any channel is what an agent
// of an older install runs.
func newestRelease() (release, error) {
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+update.Repository+"/releases?per_page=10", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return release{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("GitHub answered %d", res.StatusCode)
	}
	var list []release
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		return release{}, err
	}
	for _, r := range list {
		if !r.Draft && r.Tag != "" {
			return r, nil
		}
	}
	return release{}, fmt.Errorf("%s has no published release", update.Repository)
}

func download(url string) ([]byte, error) {
	res, err := (&http.Client{Timeout: 3 * time.Minute}).Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", url, res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 200<<20))
}

// extractBinary writes the file named regulus of a .tar.gz to dest.
func extractBinary(tgz []byte, dest string) error {
	gz, err := gzip.NewReader(strings.NewReader(string(tgz)))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("no regulus in the archive")
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != "regulus" {
			continue
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(tr, 500<<20))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	}
}
