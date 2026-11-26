package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "regulus.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	cfg, err := Load(nil, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("got %+v, want defaults %+v", cfg, Default())
	}
}

func TestPrecedence(t *testing.T) {
	file := writeFile(t, "listen: ':1111'\ndata_dir: /from/file\nlog:\n  level: debug\n")

	// file only
	cfg, err := Load([]string{"-config", file}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":1111" || cfg.DataDir != "/from/file" || cfg.Log.Level != "debug" || cfg.Log.Format != "text" {
		t.Fatalf("file values not applied correctly: %+v", cfg)
	}

	// environment overrides the file
	e := env(map[string]string{"REGULUS_LISTEN": ":2222", "REGULUS_LOG_FORMAT": "JSON"})
	cfg, err = Load([]string{"-config", file}, e, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":2222" || cfg.DataDir != "/from/file" || cfg.Log.Format != "json" {
		t.Fatalf("env values not applied correctly: %+v", cfg)
	}

	// flags override the environment
	cfg, err = Load([]string{"-config", file, "-listen", ":3333"}, e, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":3333" {
		t.Fatalf("flag not applied: %+v", cfg)
	}
}

func TestBuiltinAgent(t *testing.T) {
	file := writeFile(t, "builtin_agent: false\n")

	cfg, err := Load(nil, env(nil), io.Discard)
	if err != nil || !cfg.BuiltinAgent {
		t.Fatalf("default: cfg = %+v, err = %v; want the built-in agent enabled", cfg, err)
	}

	cfg, err = Load([]string{"-config", file}, env(nil), io.Discard)
	if err != nil || cfg.BuiltinAgent {
		t.Fatalf("file: cfg = %+v, err = %v; want the built-in agent disabled", cfg, err)
	}

	// environment overrides the file, flags override the environment
	cfg, err = Load([]string{"-config", file}, env(map[string]string{"REGULUS_BUILTIN_AGENT": "true"}), io.Discard)
	if err != nil || !cfg.BuiltinAgent {
		t.Fatalf("env: cfg = %+v, err = %v; want the built-in agent enabled", cfg, err)
	}
	cfg, err = Load([]string{"-builtin-agent=false"}, env(map[string]string{"REGULUS_BUILTIN_AGENT": "true"}), io.Discard)
	if err != nil || cfg.BuiltinAgent {
		t.Fatalf("flag: cfg = %+v, err = %v; want the built-in agent disabled", cfg, err)
	}

	if _, err := Load(nil, env(map[string]string{"REGULUS_BUILTIN_AGENT": "maybe"}), io.Discard); err == nil {
		t.Fatal("expected an error for a value that is not a boolean")
	}
}

func TestMetricsInterval(t *testing.T) {
	file := writeFile(t, "metrics_interval: 30s\n")

	cfg, err := Load(nil, env(nil), io.Discard)
	if err != nil || cfg.MetricsInterval != 10*time.Second {
		t.Fatalf("default: cfg = %+v, err = %v; want 10s", cfg, err)
	}
	cfg, err = Load([]string{"-config", file}, env(nil), io.Discard)
	if err != nil || cfg.MetricsInterval != 30*time.Second {
		t.Fatalf("file: cfg = %+v, err = %v; want 30s", cfg, err)
	}
	cfg, err = Load([]string{"-config", file}, env(map[string]string{"REGULUS_METRICS_INTERVAL": "20s"}), io.Discard)
	if err != nil || cfg.MetricsInterval != 20*time.Second {
		t.Fatalf("env: cfg = %+v, err = %v; want 20s", cfg, err)
	}
	cfg, err = Load([]string{"-metrics-interval", "5s"}, env(map[string]string{"REGULUS_METRICS_INTERVAL": "20s"}), io.Discard)
	if err != nil || cfg.MetricsInterval != 5*time.Second {
		t.Fatalf("flag: cfg = %+v, err = %v; want 5s", cfg, err)
	}

	for name, args := range map[string][]string{"too short": {"-metrics-interval", "100ms"}, "zero": {"-metrics-interval", "0"}} {
		if _, err := Load(args, env(nil), io.Discard); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := Load(nil, env(map[string]string{"REGULUS_METRICS_INTERVAL": "soon"}), io.Discard); err == nil {
		t.Error("expected an error for a value that is not a duration")
	}
}

func TestConfigPathFromEnv(t *testing.T) {
	file := writeFile(t, "data_dir: /from/env-file\n")
	cfg, err := Load(nil, env(map[string]string{"REGULUS_CONFIG": file}), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "/from/env-file" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		file string
		want string
	}{
		{"unknown key", []string{"-config", "@"}, nil, "listn: ':1'\n", "listn"},
		{"missing file", []string{"-config", "/does/not/exist.yaml"}, nil, "", "read config file"},
		{"bad level", []string{"-log-level", "loud"}, nil, "", "log.level"},
		{"bad format", nil, map[string]string{"REGULUS_LOG_FORMAT": "xml"}, "", "log.format"},
		{"unknown flag", []string{"-nope"}, nil, "", "nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.args
			if tt.file != "" {
				p := writeFile(t, tt.file)
				for i, a := range args {
					if a == "@" {
						args[i] = p
					}
				}
			}
			_, err := Load(args, env(tt.env), io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestPackagedConfigIsValid(t *testing.T) {
	// The file installed by the .deb (packaging/config.yaml) must load.
	path := filepath.Join("..", "..", "packaging", "config.yaml")
	cfg, err := Load([]string{"--config", path}, func(string) string { return "" }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "/var/lib/regulus" || !cfg.BuiltinAgent || cfg.Listen != "127.0.0.1:8080" || cfg.TLS() {
		t.Errorf("unexpected packaged defaults: %+v", cfg)
	}
}

func TestAgentListen(t *testing.T) {
	cfg, err := Load(nil, env(nil), io.Discard)
	if err != nil || cfg.AgentListen != "" {
		t.Fatalf("default = %q, %v; want remote agents off", cfg.AgentListen, err)
	}
	cfg, err = Load(nil, env(map[string]string{"REGULUS_AGENT_LISTEN": ":8443"}), io.Discard)
	if err != nil || cfg.AgentListen != ":8443" {
		t.Errorf("from env = %q, %v", cfg.AgentListen, err)
	}
	cfg, err = Load([]string{"--agent-listen", "127.0.0.1:9000"}, env(map[string]string{"REGULUS_AGENT_LISTEN": ":8443"}), io.Discard)
	if err != nil || cfg.AgentListen != "127.0.0.1:9000" {
		t.Errorf("flag over env = %q, %v", cfg.AgentListen, err)
	}
}

func TestLoadAgent(t *testing.T) {
	cfg, err := LoadAgent([]string{"--hub", "hub.example:8443"}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hub != "hub.example:8443" || cfg.DataDir != "agent-data" || cfg.Log.Level != "info" {
		t.Errorf("defaults = %+v", cfg)
	}

	file := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(file, []byte("hub: file.example:1\ndata_dir: /var/lib/regulus-agent\nca_fingerprint: sha256:abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadAgent([]string{"--config", file, "--token", "from-flag"}, env(map[string]string{
		"REGULUS_AGENT_HUB":   "env.example:2",
		"REGULUS_AGENT_TOKEN": "from-env",
	}), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	// file < env < flag
	if cfg.Hub != "env.example:2" || cfg.Token != "from-flag" || cfg.DataDir != "/var/lib/regulus-agent" || cfg.CAFingerprint != "sha256:abc" {
		t.Errorf("precedence = %+v", cfg)
	}
}

func TestLoadAgentPluginsDir(t *testing.T) {
	cfg, err := LoadAgent([]string{"--hub", "h:1"}, env(nil), io.Discard)
	if err != nil || cfg.PluginsDir != "" {
		t.Fatalf("plugins are off by default: %+v, %v", cfg, err)
	}
	file := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(file, []byte("hub: h:1\nplugins_dir: /from/file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		env  map[string]string
		want string
	}{
		{[]string{"--config", file}, nil, "/from/file"},
		{[]string{"--config", file}, map[string]string{"REGULUS_AGENT_PLUGINS_DIR": "/from/env"}, "/from/env"},
		{[]string{"--config", file, "--plugins-dir", "/from/flag"}, map[string]string{"REGULUS_AGENT_PLUGINS_DIR": "/from/env"}, "/from/flag"},
	} {
		cfg, err := LoadAgent(tc.args, env(tc.env), io.Discard)
		if err != nil || cfg.PluginsDir != tc.want {
			t.Errorf("%v %v: %q, %v; want %q", tc.args, tc.env, cfg.PluginsDir, err, tc.want)
		}
	}
}

func TestLoadHubPluginsDir(t *testing.T) {
	cfg, err := Load(nil, env(nil), io.Discard)
	if err != nil || cfg.PluginsDir != "" {
		t.Fatalf("plugins are off by default: %q, %v", cfg.PluginsDir, err)
	}
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte("plugins_dir: /from/file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		env  map[string]string
		want string
	}{
		{[]string{"--config", file}, nil, "/from/file"},
		{[]string{"--config", file}, map[string]string{"REGULUS_PLUGINS_DIR": "/from/env"}, "/from/env"},
		{[]string{"--config", file, "--plugins-dir", "/from/flag"}, map[string]string{"REGULUS_PLUGINS_DIR": "/from/env"}, "/from/flag"},
	} {
		cfg, err := Load(tc.args, env(tc.env), io.Discard)
		if err != nil || cfg.PluginsDir != tc.want {
			t.Errorf("%v %v: %q, %v; want %q", tc.args, tc.env, cfg.PluginsDir, err, tc.want)
		}
	}
}

func TestLoadAgentEnrollOnly(t *testing.T) {
	cfg, err := LoadAgent([]string{"--hub", "hub.example:8443"}, env(nil), io.Discard)
	if err != nil || cfg.EnrollOnly {
		t.Fatalf("by default the agent also runs: %+v, %v", cfg, err)
	}
	cfg, err = LoadAgent([]string{"--hub", "hub.example:8443", "--enroll-only"}, env(nil), io.Discard)
	if err != nil || !cfg.EnrollOnly {
		t.Fatalf("--enroll-only: %+v, %v", cfg, err)
	}
}

func TestLoadAgentErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no hub":           {},
		"hub without port": {"--hub", "hub.example"},
		"empty data dir":   {"--hub", "h:1", "--data-dir", ""},
		"bad log level":    {"--hub", "h:1", "--log-level", "loud"},
		"unknown flag":     {"--hub", "h:1", "--nope"},
	} {
		if _, err := LoadAgent(args, env(nil), io.Discard); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("hub: h:1\ntypo: x\n"), 0o600)
	if _, err := LoadAgent([]string{"--config", bad}, env(nil), io.Discard); err == nil {
		t.Error("an unknown key in the file must be rejected")
	}
}

func TestAgentAndHubUpdateSettings(t *testing.T) {
	cfg, err := LoadAgent([]string{"--hub", "h:1"}, env(nil), io.Discard)
	if err != nil || cfg.Updates {
		t.Fatalf("by default the agent does not update itself: %+v, %v", cfg, err)
	}
	file := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(file, []byte("hub: h:1\nupdates: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadAgent([]string{"--config", file}, env(nil), io.Discard); err != nil || !cfg.Updates {
		t.Errorf("updates: true in the file: %+v, %v", cfg, err)
	}
	if cfg, err = LoadAgent([]string{"--config", file}, env(map[string]string{"REGULUS_AGENT_UPDATES": "false"}), io.Discard); err != nil || cfg.Updates {
		t.Errorf("the environment wins: %+v, %v", cfg, err)
	}
	if _, err = LoadAgent([]string{"--hub", "h:1"}, env(map[string]string{"REGULUS_AGENT_UPDATES": "maybe"}), io.Discard); err == nil {
		t.Error("accepted a value that is not a boolean")
	}

	hub, err := Load(nil, env(map[string]string{"REGULUS_UPDATES_APPLY": "true"}), io.Discard)
	if err != nil || !hub.Updates.Apply {
		t.Errorf("hub: %+v, %v", hub.Updates, err)
	}
	if hub, _ = Load(nil, env(nil), io.Discard); hub.Updates.Apply {
		t.Error("the hub installs updates by default")
	}
}

func TestTrustedProxies(t *testing.T) {
	file := writeFile(t, "trusted_proxies:\n  - 127.0.0.1\n  - 10.0.0.0/8\n")
	cfg, err := Load([]string{"-config", file}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.TrustedProxies; len(got) != 2 || got[0] != "127.0.0.1" || got[1] != "10.0.0.0/8" {
		t.Fatalf("from the file: %v", got)
	}
	cfg, err = Load([]string{"-config", file}, env(map[string]string{"REGULUS_TRUSTED_PROXIES": "::1, 192.168.0.0/16"}), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.TrustedProxies; len(got) != 2 || got[0] != "::1" || got[1] != "192.168.0.0/16" {
		t.Fatalf("the environment must replace the file: %v", got)
	}
	prefixes, err := ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil || prefixes[0].Bits() != 128 || prefixes[1].Bits() != 16 {
		t.Fatalf("prefixes = %v, %v", prefixes, err)
	}
	if _, err := Load(nil, env(map[string]string{"REGULUS_TRUSTED_PROXIES": "proxy.example"}), io.Discard); err == nil || !strings.Contains(err.Error(), "trusted_proxies") {
		t.Fatalf("a host name must be refused, got %v", err)
	}
}

func TestTheDashboardListensOnLoopbackByDefault(t *testing.T) {
	cfg := Default()
	if cfg.Listen != "127.0.0.1:8080" || !LoopbackOnly(cfg.Listen) || cfg.TLS() {
		t.Errorf("default listen %q, TLS %v: want 127.0.0.1:8080 without TLS", cfg.Listen, cfg.TLS())
	}
	if got := cfg.DashboardURL(); got != "http://127.0.0.1:8080" {
		t.Errorf("DashboardURL = %q", got)
	}
}

func TestLoopbackOnly(t *testing.T) {
	for listen, want := range map[string]bool{
		"127.0.0.1:8080": true,
		"127.0.0.2:8080": true,
		"[::1]:8080":     true,
		"localhost:8080": true,
		":8080":          false,
		"0.0.0.0:8080":   false,
		"[::]:8080":      false,
		"10.0.0.5:8080":  false,
		"hub.lan:8080":   false,
		"nonsense":       false,
	} {
		if got := LoopbackOnly(listen); got != want {
			t.Errorf("LoopbackOnly(%q) = %v, want %v", listen, got, want)
		}
	}
}

func TestTLSSettings(t *testing.T) {
	file := writeFile(t, "tls_cert: /from/file.pem\ntls_key: /from/file.key\nlisten: \":8443\"\n")
	cfg, err := Load([]string{"--config", file}, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TLS() || cfg.TLSCert != "/from/file.pem" || cfg.TLSKey != "/from/file.key" {
		t.Errorf("from the file: %+v", cfg)
	}
	if got := cfg.DashboardURL(); got != "https://localhost:8443" {
		t.Errorf("DashboardURL = %q", got)
	}
	cfg, err = Load([]string{"--config", file, "--tls-key", "/flag.key"}, env(map[string]string{"REGULUS_TLS_CERT": "/env.pem", "REGULUS_TLS_KEY": "/env.key"}), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLSCert != "/env.pem" || cfg.TLSKey != "/flag.key" {
		t.Errorf("environment and flags: %+v", cfg)
	}
}

func TestMetricsBudget(t *testing.T) {
	file := writeFile(t, "metrics_budget: 64MiB\n")

	cfg, err := Load(nil, env(nil), io.Discard)
	if err != nil || cfg.MetricsBudget != DefaultMetricsBudget {
		t.Fatalf("default: budget = %v, err = %v; want %v", cfg.MetricsBudget, err, DefaultMetricsBudget)
	}
	cfg, err = Load([]string{"-config", file}, env(nil), io.Discard)
	if err != nil || cfg.MetricsBudget != 64<<20 {
		t.Fatalf("file: budget = %v, err = %v; want 64MiB", cfg.MetricsBudget, err)
	}
	cfg, err = Load([]string{"-config", file}, env(map[string]string{"REGULUS_METRICS_BUDGET": "1GiB"}), io.Discard)
	if err != nil || cfg.MetricsBudget != 1<<30 {
		t.Fatalf("env: budget = %v, err = %v; want 1GiB", cfg.MetricsBudget, err)
	}
	if _, err := Load(nil, env(map[string]string{"REGULUS_METRICS_BUDGET": "lots"}), io.Discard); err == nil {
		t.Fatal("expected an error for a budget that is not a size")
	}
	if _, err := Load(nil, env(map[string]string{"REGULUS_METRICS_BUDGET": "1MiB"}), io.Discard); err == nil {
		t.Fatal("expected an error for a budget under the minimum")
	}
	if _, err := Load([]string{"-config", writeFile(t, "metrics_budget: lots\n")}, env(nil), io.Discard); err == nil {
		t.Fatal("expected an error for a bad size in the file")
	}
}

func TestParseSizeRoundTrip(t *testing.T) {
	for in, want := range map[string]ByteSize{"256MiB": 256 << 20, "2gib": 2 << 30, "512k": 512 << 10, "100": 100, "4 MB": 4 << 20} {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %v, %v; want %v", in, got, err, want)
		}
		if back, err := ParseSize(got.String()); err != nil || back != got {
			t.Errorf("%v does not round trip: %v, %v", got, back, err)
		}
	}
	for _, bad := range []string{"", "MiB", "0", "-1MiB", "1.5GiB", "9999999999999GiB"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) succeeded, want an error", bad)
		}
	}
}

func TestPrometheusNeedsALongToken(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if cfg, err := Load(nil, env(nil), io.Discard); err != nil || cfg.Prometheus.Enabled {
		t.Fatalf("default: %+v, %v", cfg.Prometheus, err)
	}
	if _, err := Load(nil, env(map[string]string{"REGULUS_PROMETHEUS_ENABLED": "true"}), io.Discard); err == nil || !strings.Contains(err.Error(), "REGULUS_PROMETHEUS_TOKEN") {
		t.Fatalf("enabled without a token: %v", err)
	}
	if _, err := Load(nil, env(map[string]string{"REGULUS_PROMETHEUS_ENABLED": "true", "REGULUS_PROMETHEUS_TOKEN": "short"}), io.Discard); err == nil {
		t.Fatal("a short token was accepted")
	}
	long := strings.Repeat("a", MinMetricsTokenLength)
	cfg, err := Load(nil, env(map[string]string{"REGULUS_PROMETHEUS_ENABLED": "true", "REGULUS_PROMETHEUS_TOKEN": long}), io.Discard)
	if err != nil || !cfg.Prometheus.Enabled || cfg.Prometheus.Token != long {
		t.Fatalf("enabled with a token: %+v, %v", cfg.Prometheus, err)
	}
	if v, _ := cfg.Get("prometheus.token"); v != Masked {
		t.Errorf("the token is shown as %q", v)
	}
	if _, err := Load(nil, env(map[string]string{"REGULUS_PROMETHEUS_ENABLED": "maybe"}), io.Discard); err == nil {
		t.Error("a value that is not a boolean was accepted")
	}
}

func TestPrometheusTokenIsNeverWrittenToTheFile(t *testing.T) {
	_, err := Normalize("prometheus.token", strings.Repeat("a", 40))
	var se *SettingError
	if !errors.As(err, &se) || se.Code != ErrPasswordInFile || se.Args[0] != "REGULUS_PROMETHEUS_TOKEN" {
		t.Fatalf("Normalize: %v", err)
	}
}

func TestLoadHubPluginSources(t *testing.T) {
	load := func(yaml string) (Config, error) {
		file := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load([]string{"--config", file}, env(nil), io.Discard)
	}
	cfg, err := load("plugin_sources:\n  - name: mine\n    url: https://example.org/index.json\n    trust: custom\n    signer: https://github.com/me/p/.github/workflows/x.yml@refs/heads/main\n    channel: beta\n")
	if err != nil || len(cfg.PluginSources) != 1 || cfg.PluginSources[0].Channel != "beta" {
		t.Fatalf("%+v, %v", cfg.PluginSources, err)
	}
	if _, err := load("plugin_sources:\n  - name: mine\n    url: https://example.org/index.json\n    trust: custom\n"); err == nil || !strings.Contains(err.Error(), "signing identity") {
		t.Errorf("a source with no signer must be refused: %v", err)
	}
}
