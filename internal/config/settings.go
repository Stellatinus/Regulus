package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/CodexCircle/Regulus/internal/storage"
)

// DefaultFile is where the package installs the hub configuration. The hub
// itself only reads a file it is told about (--config); the console and the
// `regulus config` commands fall back to this one when it exists.
const DefaultFile = "/etc/regulus/config.yaml"

// Setting describes one hub setting that people can read and change with the
// console or `regulus config`.
type Setting struct {
	// Key is the dotted YAML path, such as log.level.
	Key string
	// Env is the environment variable that overrides it, if any.
	Env         string
	Description string
	// Choices lists the accepted values when there is a fixed set.
	Choices []string
	// Restart is true when the hub reads the setting only at start.
	Restart bool
	// Secret is true for a value that must never be shown or written to the
	// configuration file; it comes from the environment only.
	Secret bool
	// quote is true for values that are written as YAML strings.
	quote bool
}

// Settings returns the settings in the order they are shown.
func Settings() []Setting {
	return []Setting{
		{Key: "listen", Env: "REGULUS_LISTEN", Description: "Address of the dashboard and API: host:port, or only the port (8080). The default, 127.0.0.1:8080, is reachable from this machine only; a port alone listens on every interface", Restart: true, quote: true},
		{Key: "agent_listen", Env: "REGULUS_AGENT_LISTEN", Description: "Address where remote agents connect (gRPC over TLS); empty keeps remote agents off", Restart: true, quote: true},
		{Key: "data_dir", Env: "REGULUS_DATA_DIR", Description: "Directory where the hub keeps its database and keys", Restart: true, quote: true},
		{Key: "database.type", Env: "REGULUS_DATABASE_TYPE", Description: "Where the hub keeps its data: sqlite (a file in data_dir, nothing to set up) or postgres (a server you prepared)", Choices: []string{"sqlite", "postgres"}, Restart: true},
		{Key: "database.host", Env: "REGULUS_DATABASE_HOST", Description: "PostgreSQL host name or address (database.type postgres)", Restart: true, quote: true},
		{Key: "database.port", Env: "REGULUS_DATABASE_PORT", Description: "PostgreSQL port", Restart: true},
		{Key: "database.name", Env: "REGULUS_DATABASE_NAME", Description: "PostgreSQL database name", Restart: true, quote: true},
		{Key: "database.user", Env: "REGULUS_DATABASE_USER", Description: "PostgreSQL user", Restart: true, quote: true},
		{Key: "database.password", Env: "REGULUS_DATABASE_PASSWORD", Description: "PostgreSQL password; read only from the environment (/etc/regulus/regulus.env), never shown or written to the configuration file", Restart: true, Secret: true},
		{Key: "database.sslmode", Env: "REGULUS_DATABASE_SSLMODE", Description: "TLS to PostgreSQL", Choices: []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}, Restart: true},
		{Key: "database.sslrootcert", Env: "REGULUS_DATABASE_SSLROOTCERT", Description: "File with the CA certificate that signed the PostgreSQL server certificate (for verify-ca and verify-full)", Restart: true, quote: true},
		{Key: "database.schema", Env: "REGULUS_DATABASE_SCHEMA", Description: "PostgreSQL schema that holds the tables; empty means public", Restart: true, quote: true},
		{Key: "builtin_agent", Env: "REGULUS_BUILTIN_AGENT", Description: "Run an agent inside the hub so its own server is managed too", Choices: []string{"true", "false"}, Restart: true},
		{Key: "metrics_interval", Env: "REGULUS_METRICS_INTERVAL", Description: "How often agents report metrics, such as 10s (at least 1s)", Restart: true},
		{Key: "metrics_budget", Env: "REGULUS_METRICS_BUDGET", Description: "Memory for the metrics history of all agents together, such as 256MiB (at least 4MiB); the oldest samples go first when it is full", Restart: true},
		{Key: "metrics_retention_1m", Env: "REGULUS_METRICS_RETENTION_1M", Description: "How long the stored 1-minute history of the metrics is kept, such as 3d; 0 turns the tier off", Restart: true},
		{Key: "metrics_retention_15m", Env: "REGULUS_METRICS_RETENTION_15M", Description: "How long the stored 15-minute history of the metrics is kept, such as 90d; 0 turns the tier off", Restart: true},
		{Key: "plugins_dir", Env: "REGULUS_PLUGINS_DIR", Description: "Directory of the plugins the hub runs, each as its own process; empty runs none. Keep it owned by root: whatever is in it runs", Restart: true, quote: true},
		{Key: "log.level", Env: "REGULUS_LOG_LEVEL", Description: "How much the hub logs", Choices: []string{"debug", "info", "warn", "error"}, Restart: true},
		{Key: "log.format", Env: "REGULUS_LOG_FORMAT", Description: "Log format", Choices: []string{"text", "json"}, Restart: true},
		{Key: "updates.check", Env: "REGULUS_UPDATES_CHECK", Description: "Look for new releases on GitHub every few hours; false keeps the hub from contacting any server for updates", Choices: []string{"true", "false"}, Restart: true},
		{Key: "updates.apply", Env: "REGULUS_UPDATES_APPLY", Description: "Let the dashboard install downloaded updates through the root helper (regulus-update.path, enabled by the installer)", Choices: []string{"true", "false"}, Restart: true},
		{Key: "prometheus.enabled", Env: "REGULUS_PROMETHEUS_ENABLED", Description: "Serve GET /metrics in the Prometheus text format (the hub's figures and each server's latest); needs prometheus.token", Choices: []string{"true", "false"}, Restart: true},
		{Key: "prometheus.token", Env: "REGULUS_PROMETHEUS_TOKEN", Description: "Bearer token a Prometheus scrape sends, at least 32 characters; read only from the environment (/etc/regulus/regulus.env), never shown or written to the configuration file", Restart: true, Secret: true},
		{Key: "console.tui", Description: "Open the full-screen console when running `regulus` in a terminal", Choices: []string{"true", "false"}},
	}
}

// Lookup returns the setting with the given key.
func Lookup(key string) (Setting, bool) {
	for _, s := range Settings() {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// Keys lists the keys of the settings.
func Keys() []string {
	var keys []string
	for _, s := range Settings() {
		keys = append(keys, s.Key)
	}
	return keys
}

// Get returns the value of a setting in the form Set accepts.
func (c Config) Get(key string) (string, bool) {
	switch key {
	case "listen":
		return c.Listen, true
	case "agent_listen":
		return c.AgentListen, true
	case "data_dir":
		return c.DataDir, true
	case "database.type":
		return c.Database.Type, true
	case "database.host":
		return c.Database.Host, true
	case "database.port":
		return strconv.Itoa(c.Database.Port), true
	case "database.name":
		return c.Database.Name, true
	case "database.user":
		return c.Database.User, true
	case "database.password":
		// Only whether it is set, never the value.
		if c.Database.Password != "" {
			return Masked, true
		}
		return "", true
	case "database.sslmode":
		return c.Database.SSLMode, true
	case "database.sslrootcert":
		return c.Database.SSLRootCert, true
	case "database.schema":
		return c.Database.Schema, true
	case "builtin_agent":
		return strconv.FormatBool(c.BuiltinAgent), true
	case "metrics_interval":
		return c.MetricsInterval.String(), true
	case "metrics_budget":
		return c.MetricsBudget.String(), true
	case "metrics_retention_1m":
		return c.MetricsRetention1m.String(), true
	case "metrics_retention_15m":
		return c.MetricsRetention15m.String(), true
	case "plugins_dir":
		return c.PluginsDir, true
	case "log.level":
		return c.Log.Level, true
	case "log.format":
		return c.Log.Format, true
	case "updates.check":
		return strconv.FormatBool(c.Updates.Check), true
	case "updates.apply":
		return strconv.FormatBool(c.Updates.Apply), true
	case "prometheus.enabled":
		return strconv.FormatBool(c.Prometheus.Enabled), true
	case "prometheus.token":
		if c.Prometheus.Token != "" {
			return Masked, true
		}
		return "", true
	case "console.tui":
		return strconv.FormatBool(c.Console.TUI), true
	}
	return "", false
}

// Normalize checks a value typed for a setting and returns it in canonical
// form (for example "9090" becomes ":9090"). The error is a *SettingError
// that says what is wrong in words fit for a person typing it.
func Normalize(key, raw string) (string, error) {
	if _, ok := Lookup(key); !ok {
		return "", settingErr("", ErrUnknownSetting, key, strings.Join(Keys(), ", "))
	}
	v, err := normalize(key, raw)
	if err != nil {
		err.Key = key
		return "", err
	}
	return v, nil
}

func normalize(key, raw string) (string, *SettingError) {
	v := strings.TrimSpace(raw)
	switch key {
	case "listen":
		return normalizeAddress(v)
	case "agent_listen":
		if v == "" || strings.EqualFold(v, "off") {
			return "", nil
		}
		return normalizeAddress(v)
	case "data_dir":
		if v == "" {
			return "", settingErr("", ErrEmpty)
		}
		return v, nil
	case "database.type":
		return oneOf(v, "sqlite", "postgres")
	case "database.sslmode":
		return oneOf(v, "disable", "allow", "prefer", "require", "verify-ca", "verify-full")
	case "database.host", "database.sslrootcert", "plugins_dir":
		return v, nil
	case "database.name", "database.user":
		if v == "" {
			return "", settingErr("", ErrEmpty)
		}
		return v, nil
	case "database.port":
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return "", settingErr("", ErrPort)
		}
		return strconv.Itoa(n), nil
	case "database.schema":
		if !storage.ValidSchema(v) {
			return "", settingErr("", ErrSchema)
		}
		return v, nil
	case "database.password":
		return "", settingErr("", ErrPasswordInFile, "REGULUS_DATABASE_PASSWORD")
	case "prometheus.token":
		return "", settingErr("", ErrPasswordInFile, "REGULUS_PROMETHEUS_TOKEN")
	case "builtin_agent", "console.tui", "updates.check", "updates.apply", "prometheus.enabled":
		b, err := strconv.ParseBool(v)
		if err != nil {
			return "", settingErr("", ErrBool)
		}
		return strconv.FormatBool(b), nil
	case "metrics_interval":
		d, err := time.ParseDuration(v)
		if err != nil {
			return "", settingErr("", ErrDuration)
		}
		if d < MinMetricsInterval {
			return "", settingErr("", ErrTooShort, MinMetricsInterval.String())
		}
		return d.String(), nil
	case "metrics_budget":
		n, err := ParseSize(v)
		if err != nil {
			return "", settingErr("", ErrSize)
		}
		if n < MinMetricsBudget {
			return "", settingErr("", ErrTooShort, MinMetricsBudget.String())
		}
		return n.String(), nil
	case "metrics_retention_1m", "metrics_retention_15m":
		r, err := ParseRetention(v)
		if err != nil {
			return "", settingErr("", ErrRetention)
		}
		return r.String(), nil
	case "log.level":
		return oneOf(v, "debug", "info", "warn", "error")
	case "log.format":
		return oneOf(v, "text", "json")
	}
	return "", settingErr("", ErrUnknownSetting, key, strings.Join(Keys(), ", "))
}

func oneOf(v string, choices ...string) (string, *SettingError) {
	v = strings.ToLower(v)
	for _, c := range choices {
		if v == c {
			return v, nil
		}
	}
	return "", settingErr("", ErrChoice, strings.Join(choices, ", "))
}

// normalizeAddress accepts host:port or a bare port.
func normalizeAddress(v string) (string, *SettingError) {
	if v == "" {
		return "", settingErr("", ErrEmpty)
	}
	if _, err := strconv.Atoi(v); err == nil {
		v = ":" + v
	}
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return "", settingErr("", ErrAddress)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", settingErr("", ErrPort)
	}
	if host == "" {
		return ":" + port, nil
	}
	return net.JoinHostPort(host, port), nil
}

// Masked is shown instead of a secret value that is set.
const Masked = "********"

// Origins of a value.
const (
	OriginDefault = "default"
	OriginFile    = "file"
	OriginEnv     = "environment"
	// OriginDashboard is a value the running hub reports that is in none of
	// the others: today only the agent listener turned on from the dashboard,
	// which the hub keeps in its database (#423). Resolve never returns it;
	// the console sets it from what the hub says about itself.
	OriginDashboard = "dashboard"
)

// Entry is a setting with its current value and where the value comes from.
type Entry struct {
	Setting
	Value  string
	Origin string
}

// FindFile returns the configuration file to use: the explicit path, else
// REGULUS_CONFIG, else DefaultFile when it exists, else "".
func FindFile(explicit string, getenv func(string) string) string {
	if explicit != "" {
		return explicit
	}
	if v := getenv("REGULUS_CONFIG"); v != "" {
		return v
	}
	if _, err := os.Stat(DefaultFile); err == nil {
		return DefaultFile
	}
	return ""
}

// Resolve loads the configuration the way the hub would (defaults, file,
// environment) and returns it with every setting's value and origin.
func Resolve(file string, getenv func(string) string) (Config, []Entry, error) {
	var args []string
	if file != "" {
		args = []string{"--config", file}
	}
	cfg, err := Load(args, getenv, io.Discard)
	if err != nil {
		return Config{}, nil, err
	}
	inFile := map[string]bool{}
	if file != "" {
		if data, err := os.ReadFile(file); err == nil {
			var doc map[string]any
			if yaml.Unmarshal(data, &doc) == nil {
				collectKeys("", doc, inFile)
			}
		}
	}
	var entries []Entry
	for _, s := range Settings() {
		v, _ := cfg.Get(s.Key)
		e := Entry{Setting: s, Value: v, Origin: OriginDefault}
		switch {
		case s.Env != "" && getenv(s.Env) != "":
			e.Origin = OriginEnv
		case inFile[s.Key]:
			e.Origin = OriginFile
		}
		entries = append(entries, e)
	}
	return cfg, entries, nil
}

func collectKeys(prefix string, m map[string]any, out map[string]bool) {
	for k, v := range m {
		key := prefix + k
		if sub, ok := v.(map[string]any); ok {
			collectKeys(key+".", sub, out)
			continue
		}
		out[key] = true
	}
}

// SetFile changes one setting in the YAML file at path, keeping comments and
// the rest of the file as they are, and returns the canonical value written.
// The new file is checked as a whole before anything is written, and it
// replaces the old one in one step (see writeConfig), so a crash or a full
// disk leaves the old file or the new one, never half of one.
//
// Changing data_dir away from a directory that holds the hub's data is
// refused; SetFileForce does it.
func SetFile(path, key, raw string) (string, error) { return setFile(path, key, raw, false) }

// SetFileForce is SetFile without the refusal to move data_dir off a
// directory that holds the hub's data.
func SetFileForce(path, key, raw string) (string, error) { return setFile(path, key, raw, true) }

func setFile(path, key, raw string, force bool) (string, error) {
	value, err := Normalize(key, raw)
	if err != nil {
		return "", err
	}
	// Nobody else edits the file between the read and the write.
	defer lockDir(filepath.Dir(path))()
	old, err := os.ReadFile(path)
	if err != nil {
		return "", &SettingError{Code: ErrReadFile, Args: []string{err.Error()}, Err: err}
	}
	if key == "data_dir" && !force {
		if err := checkDataDirChange(string(old), value); err != nil {
			return "", err
		}
	}
	updated, err := editYAML(string(old), key, value)
	if err != nil {
		return "", err
	}

	check := Default()
	dec := yaml.NewDecoder(strings.NewReader(updated))
	dec.KnownFields(true)
	if err := dec.Decode(&check); err != nil && !errors.Is(err, io.EOF) {
		return "", &SettingError{Code: ErrUnreadable, Args: []string{path, err.Error()}, Err: err}
	}
	if got, _ := check.Get(key); got != value {
		return "", settingErr(key, ErrHandEdit, path)
	}
	if check.Database.Type == storage.TypePostgres && check.Database.Host == "" {
		// The one way a valid value makes the file invalid as a whole.
		return "", settingErr("", ErrPostgresHost)
	}
	if err := check.Validate(); err != nil {
		return "", &SettingError{Code: ErrInvalid, Args: []string{err.Error()}, Err: err}
	}

	if updated == string(old) {
		return value, nil
	}
	if err := writeConfig(path, []byte(updated)); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return "", &SettingError{Code: ErrWritePermission, Args: []string{err.Error()}, Err: err}
		}
		return "", &SettingError{Code: ErrWriteFile, Args: []string{err.Error()}, Err: err}
	}
	return value, nil
}

// The steps of writeConfig, replaceable by a test that makes one fail.
var (
	createTemp = os.CreateTemp
	renameFile = os.Rename
)

// writeConfig replaces the file with data in one step: the new text goes to a
// temporary file in the same directory, is synced, gets the owner and mode of
// the old file and is renamed over it. Truncating the file in place (what
// os.WriteFile does) leaves it empty or cut short if the process dies in the
// middle, and an empty file loads as "all defaults": a hub started on it
// would use a new database, a new secret key and a new CA.
//
// When the directory cannot take a new file (a file that the user may write
// in a directory that they may not) it falls back to writing in place, the
// only way left to change it.
func writeConfig(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	removeStaleTemps(filepath.Dir(path), info)
	tmp, err := createTemp(filepath.Dir(path), ".config-")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return os.WriteFile(path, data, info.Mode().Perm())
		}
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after a successful rename, cleans up after a failure
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if uid, gid, ok := ownerOf(info); ok {
		_ = tmp.Chown(uid, gid) // only root can give a file away; without it the file keeps the owner of whoever edits
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := renameFile(tmp.Name(), path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync() // the rename itself must survive a power cut
		_ = d.Close()
	}
	return nil
}

// staleTempAge is how old a ".config-*" file must be before writeConfig
// deletes it. A writer that is still running holds a younger one for a few
// milliseconds, so ten minutes cannot touch a live write.
const staleTempAge = 10 * time.Minute

// removeStaleTemps deletes the ".config-*" files that a writer killed between
// the creation of its temporary file and the rename left behind (SIGKILL and a
// power cut skip the deferred remove). Each is a copy of the whole
// configuration, which can hold database.password or prometheus.token. Only
// regular files older than staleTempAge, with the owner of the configuration
// file, go; errors are ignored because this is housekeeping.
func removeStaleTemps(dir string, cfg os.FileInfo) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cfgUID, _, haveOwner := ownerOf(cfg)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".config-") || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < staleTempAge {
			continue
		}
		if uid, _, ok := ownerOf(info); haveOwner && ok && uid != cfgUID {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// stateFiles are what a hub keeps in its data directory: the database (a file
// with SQLite), the key that protects the two-factor secrets and the CA that
// every enrolled agent pins. The names are those of storage.FileName,
// auth's secret.key and pki's ca.key.
var stateFiles = []string{storage.FileName, "secret.key", "ca.key"}

func holdsHubData(dir string) bool {
	for _, f := range stateFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

// checkDataDirChange refuses to point data_dir away from a directory that
// holds the hub's data to one that holds none: the hub would start with an
// empty database, a new secret key and a new CA, every enrolled agent would
// distrust it and the two-factor secrets would be lost. The data is not moved
// by changing the setting.
func checkDataDirChange(oldYAML, newDir string) error {
	cur := Default()
	dec := yaml.NewDecoder(strings.NewReader(oldYAML))
	dec.KnownFields(true)
	if err := dec.Decode(&cur); err != nil && !errors.Is(err, io.EOF) {
		return nil // the edit itself reports an unreadable file
	}
	oldDir, newAbs := absPath(cur.DataDir), absPath(newDir)
	if oldDir == newAbs || !holdsHubData(oldDir) || holdsHubData(newAbs) {
		return nil
	}
	return settingErr("data_dir", ErrDataDirMove, oldDir, newAbs)
}

func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

var commentRE = regexp.MustCompile(`^(\s*[A-Za-z_]+:)[ \t]*("[^"]*"|'[^']*'|[^#\s][^#]*?)?([ \t]+#.*)?[ \t]*$`)

// editYAML sets key (a dotted path of at most two names) in the text of a
// YAML file by changing the one line, so comments and layout survive.
func editYAML(text, key, value string) (string, error) {
	setting, _ := Lookup(key)
	rendered := value
	if setting.quote {
		rendered = strconv.Quote(value)
	}
	if rendered == "" {
		rendered = `""`
	}

	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	parent, name, nested := strings.Cut(key, ".")
	if !nested {
		name, parent = parent, ""
	}

	// Find the line to change within the right block.
	start, end := 0, len(lines)
	if parent != "" {
		idx := -1
		for i, l := range lines {
			if regexp.MustCompile(`^` + regexp.QuoteMeta(parent) + `:[ \t]*(#.*)?$`).MatchString(l) {
				idx = i
				break
			}
		}
		if idx < 0 {
			lines = append(lines, parent+":", "  "+name+": "+rendered)
			return strings.Join(lines, "\n") + "\n", nil
		}
		start = idx + 1
		end = start
		for end < len(lines) && (lines[end] == "" || strings.HasPrefix(lines[end], " ") || strings.HasPrefix(lines[end], "\t")) {
			end++
		}
		// Trailing blank lines belong to what follows.
		for end > start && lines[end-1] == "" {
			end--
		}
	}
	keyRE := regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(name) + `:(\s|$)`)
	commented := regexp.MustCompile(`^(\s*)#[ \t]*` + regexp.QuoteMeta(name) + `:(\s|$)`)
	for i := start; i < end; i++ {
		l := lines[i]
		if parent == "" && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) {
			continue
		}
		if keyRE.MatchString(l) {
			m := commentRE.FindStringSubmatch(l)
			if m == nil {
				return "", settingErr(key, ErrUnusualLine, l)
			}
			lines[i] = m[1] + " " + rendered + m[3]
			return strings.Join(lines, "\n") + "\n", nil
		}
	}
	// An example left in a comment, such as `# agent_listen: ":8443"`, is
	// replaced by the real setting.
	for i := start; i < end; i++ {
		if m := commented.FindStringSubmatch(lines[i]); m != nil && (parent != "" || !strings.HasPrefix(lines[i], " ")) {
			indent := ""
			if parent != "" {
				indent = "  "
			}
			lines[i] = indent + name + ": " + rendered
			return strings.Join(lines, "\n") + "\n", nil
		}
	}
	// Not there: add it at the end of its block.
	line := name + ": " + rendered
	if parent != "" {
		line = "  " + line
	}
	lines = append(lines[:end], append([]string{line}, lines[end:]...)...)
	return strings.Join(lines, "\n") + "\n", nil
}

// SetAgentHub sets hub in the agent's configuration file (agent.yaml): the
// address the agent connects to. The comments and the order of the file are
// kept and the file is replaced in one step, like SetFile does for the hub's
// configuration. The address must be host:port.
func SetAgentHub(path, hub string) error {
	host, port, err := net.SplitHostPort(hub)
	if err != nil || host == "" {
		return &SettingError{Code: ErrInvalid, Args: []string{fmt.Sprintf("hub %q must be host:port, such as hub.example:8443", hub)}, Err: fmt.Errorf("hub %q must be host:port", hub)}
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return &SettingError{Code: ErrInvalid, Args: []string{fmt.Sprintf("the port of hub %q must be a number from 1 to 65535", hub)}, Err: fmt.Errorf("hub %q has an invalid port", hub)}
	}
	defer lockDir(filepath.Dir(path))()
	old, err := os.ReadFile(path)
	if err != nil {
		return &SettingError{Code: ErrReadFile, Args: []string{err.Error()}, Err: err}
	}
	// Quoted: an IPv6 address such as [::1]:8443 starts like a YAML list.
	updated, err := editYAML(string(old), "hub", strconv.Quote(hub))
	if err != nil {
		return err
	}
	check := DefaultAgent()
	dec := yaml.NewDecoder(strings.NewReader(updated))
	dec.KnownFields(true)
	if err := dec.Decode(&check); err != nil && !errors.Is(err, io.EOF) {
		return &SettingError{Code: ErrUnreadable, Args: []string{path, err.Error()}, Err: err}
	}
	if check.Hub != hub {
		return settingErr("hub", ErrHandEdit, path)
	}
	if updated == string(old) {
		return nil
	}
	if err := writeConfig(path, []byte(updated)); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return &SettingError{Code: ErrWritePermission, Args: []string{err.Error()}, Err: err}
		}
		return &SettingError{Code: ErrWriteFile, Args: []string{err.Error()}, Err: err}
	}
	return nil
}
