package plugin

import (
	"strings"
	"testing"

	"github.com/CodexCircle/Regulus/internal/update"
)

const goodManifest = `
id: docker
version: 0.3.1
name: Docker
description: Containers of each server.
api: 1
requires: ">=0.3.0 <0.5.0"
components:
  agent:
    command: bin/docker-agent
    args: [--quiet]
  hub:
    command: bin/docker-hub
  ui:
    module: ui/widgets.js
permissions:
  - id: docker.containers.read
    description: See the containers.
  - id: docker.containers.restart
    description: Restart a container.
`

func TestParseManifest(t *testing.T) {
	m, err := ParseManifest(strings.NewReader(goodManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "docker" || m.Version != "0.3.1" || m.API != 1 || m.Program(RoleAgent).Command != "bin/docker-agent" ||
		m.Program(RoleAgent).Args[0] != "--quiet" || m.Program(RoleHub) == nil || m.Components.UI.Module != "ui/widgets.js" || len(m.Permissions) != 2 {
		t.Errorf("manifest = %+v", m)
	}
}

func TestManifestRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ from, to, want string }{
		"unknown key":             {"api: 1", "api: 1\nautostart: true", "autostart"},
		"upper-case id":           {"id: docker", "id: Docker", "id"},
		"id with two hyphens":     {"id: docker", "id: dock--er", "id"},
		"version with a v":        {"version: 0.3.1", "version: v0.3.1", "version"},
		"version not semver":      {"version: 0.3.1", "version: three", "version"},
		"no name":                 {"name: Docker", "name: ' '", "name"},
		"no api":                  {"api: 1", "api: 0", "api"},
		"bad range":               {`requires: ">=0.3.0 <0.5.0"`, `requires: "0.3"`, "requires"},
		"absolute command":        {"command: bin/docker-agent", "command: /usr/bin/docker", "relative"},
		"command leaves the dir":  {"command: bin/docker-agent", "command: ../other/bin", "inside"},
		"unclean command":         {"command: bin/docker-agent", "command: bin/../bin/docker-agent", "clean"},
		"ui not js":               {"module: ui/widgets.js", "module: ui/widgets.html", ".js"},
		"permission of another":   {"id: docker.containers.read", "id: other.containers.read", "start with the plugin id"},
		"permission twice":        {"id: docker.containers.restart", "id: docker.containers.read", "twice"},
		"permission without text": {"description: Restart a container.", "description: ''", "description"},
	} {
		t.Run(name, func(t *testing.T) {
			src := strings.Replace(goodManifest, tc.from, tc.to, 1)
			if src == goodManifest {
				t.Fatal("the test did not change the manifest")
			}
			_, err := ParseManifest(strings.NewReader(src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
	if _, err := ParseManifest(strings.NewReader("id: x1\nversion: 0.1.0\nname: X\napi: 1\n")); err == nil || !strings.Contains(err.Error(), "components") {
		t.Errorf("no component: %v", err)
	}
	if _, err := ParseManifest(strings.NewReader("")); err == nil {
		t.Error("an empty manifest was accepted")
	}
	if _, err := ParseManifest(strings.NewReader(strings.Repeat("#", maxManifest+1))); err == nil {
		t.Error("a huge manifest was accepted")
	}
}

func TestRange(t *testing.T) {
	r, err := ParseRange(">=0.3.0, <0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	for v, want := range map[string]bool{"0.2.9": false, "0.3.0": true, "0.4.7": true, "0.5.0-alpha.1": false, "0.5.0": false} {
		if got := r.Contains(mustVersion(t, v)); got != want {
			t.Errorf("%s: %v, want %v", v, got, want)
		}
	}
	if r, _ := ParseRange(""); !r.Contains(mustVersion(t, "9.9.9")) {
		t.Error("an empty range must contain every version")
	}
	if r, _ := ParseRange("=0.3.0"); r.Contains(mustVersion(t, "0.3.1")) || !r.Contains(mustVersion(t, "0.3.0")) {
		t.Error("=")
	}
}

func TestCompatible(t *testing.T) {
	m, _ := ParseManifest(strings.NewReader(goodManifest))
	if err := m.Compatible("0.4.0"); err != nil {
		t.Errorf("0.4.0: %v", err)
	}
	if err := m.Compatible("0.5.0"); err == nil || !strings.Contains(err.Error(), "needs Regulus") {
		t.Errorf("0.5.0: %v", err)
	}
	if err := m.Compatible("dev"); err != nil {
		t.Errorf("a development build runs any plugin of its API: %v", err)
	}
	m.API = APIVersion + 1
	if err := m.Compatible("0.4.0"); err == nil || !strings.Contains(err.Error(), "plugin API") {
		t.Errorf("newer API: %v", err)
	}
}

func mustVersion(t *testing.T, s string) update.Version {
	t.Helper()
	v, err := update.ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
