package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/CodexCircle/Regulus/internal/plugin"
	"github.com/CodexCircle/Regulus/internal/update"
)

// Audit events of the lifecycle.
const (
	EventInstalled     = "plugin_installed"
	EventUpdated       = "plugin_updated"
	EventRemoved       = "plugin_removed"
	EventInstallFailed = "plugin_change_failed"
)

// Installer is the hub's half of installing, updating and removing plugins:
// it decides which version, downloads the package, checks it against the
// signed index, stages it, asks the root helper and waits for its answer. It
// does nothing the helper does not check again. Rollback, pinning, the
// requirements between plugins and the automatic mode are not built.
type Installer struct {
	Service *Service
	// DataDir is the hub's data directory; the staged files and the request
	// are in it (StageDir, ApplyDir).
	DataDir string
	// PluginsDir is plugins_dir; empty means plugins are off.
	PluginsDir string
	// Client downloads packages; nil uses a client with a long timeout.
	Client *http.Client
	// Wait is how long to wait for the helper (default 2 minutes) and Poll how
	// often to look (default 200 ms).
	Wait, Poll time.Duration
	// Reload makes the hub read the plugins directory again after a change.
	Reload func()
	// Audit records an event; may be nil.
	Audit func(ctx context.Context, actor, event, target, detail string)

	mu sync.Mutex
}

// Outcome is what a successful request did.
type Outcome struct {
	Plugin  string `json:"plugin"`
	Action  string `json:"action"`
	Version string `json:"version,omitempty"`
	From    string `json:"from,omitempty"`
	Source  string `json:"source,omitempty"`
}

func (i *Installer) installed() []plugin.Installed { return plugin.ListInstalled(i.PluginsDir) }

func (i *Installer) find(id string) (plugin.Installed, bool) {
	for _, p := range i.installed() {
		if p.ID == id {
			return p, true
		}
	}
	return plugin.Installed{}, false
}

// Install installs a plugin that is not installed. version and source may be
// empty (the newest offered version; the only source that offers it).
func (i *Installer) Install(ctx context.Context, by, id, version, source string) (Outcome, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.ready(); err != nil {
		return Outcome{}, err
	}
	if _, ok := i.find(id); ok {
		return Outcome{}, errf(ErrCodeAlreadyInstalled, "%s is installed already: update it, or remove it first", id)
	}
	offer, err := i.Service.Resolve(ctx, id, version, source)
	if err != nil {
		return Outcome{}, err
	}
	return i.run(ctx, by, EventInstalled, ActionInstall, offer, "")
}

// Update installs a newer version of an installed plugin, from the source it
// was installed from and no other. version may be empty.
func (i *Installer) Update(ctx context.Context, by, id, version string) (Outcome, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.ready(); err != nil {
		return Outcome{}, err
	}
	cur, ok := i.find(id)
	if !ok {
		return Outcome{}, errf(ErrCodeNotInstalled, "%s is not installed", id)
	}
	if cur.Source == "" {
		return Outcome{}, errf(ErrCodeNoSource, "%s was put in the plugins directory by hand, so no source updates it: remove it and install it from a source", id)
	}
	offer, err := i.Service.Resolve(ctx, id, version, cur.Source)
	if err != nil {
		return Outcome{}, err
	}
	have, _ := update.ParseVersion(cur.Version)
	want, _ := update.ParseVersion(offer.Version.Version)
	if want.Compare(have) <= 0 {
		return Outcome{}, errf(ErrCodeUpToDate, "%s %s is the newest version %s offers on this channel", id, cur.Version, cur.Source)
	}
	return i.run(ctx, by, EventUpdated, ActionInstall, offer, cur.Version)
}

// Remove removes an installed plugin: its directory, with every version kept.
// Its data directory (plugins-data/<id>, owned by the hub) is left alone, so
// installing it again finds its state.
func (i *Installer) Remove(ctx context.Context, by, id string) (Outcome, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.ready(); err != nil {
		return Outcome{}, err
	}
	cur, ok := i.find(id)
	if !ok {
		return Outcome{}, errf(ErrCodeNotInstalled, "%s is not installed", id)
	}
	req := Request{Action: ActionRemove, Plugin: id}
	res, err := i.ask(ctx, by, req)
	if err != nil {
		i.audit(ctx, by, EventInstallFailed, id, "remove: "+err.Error())
		return Outcome{}, err
	}
	i.reload()
	i.audit(ctx, by, EventRemoved, id, "version "+cur.Version)
	return Outcome{Plugin: id, Action: "remove", From: res.From}, nil
}

func (i *Installer) ready() error {
	if i.PluginsDir == "" {
		return errf(ErrCodePluginsOff, "plugins are off: set plugins_dir (a directory owned by root) in the hub's configuration")
	}
	if RequestPending(ApplyDir(i.DataDir)) {
		return errf(ErrCodeBusy, "another change of plugins is waiting for the helper")
	}
	return nil
}

func (i *Installer) run(ctx context.Context, by, event, action string, o Offer, from string) (Outcome, error) {
	id, version := o.Plugin.ID, o.Version.Version
	fail := func(err error) (Outcome, error) {
		i.audit(ctx, by, EventInstallFailed, id, fmt.Sprintf("%s %s: %v", action, version, err))
		return Outcome{}, err
	}
	sdir := stagedDir(i.DataDir, id, version)
	_ = os.RemoveAll(sdir)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		return fail(errf(ErrCodeDownload, "cannot stage the package: %v", err))
	}
	defer func() { _ = os.RemoveAll(sdir) }()
	if err := os.MkdirAll(ApplyDir(i.DataDir), 0o750); err != nil {
		return fail(errf(ErrCodeHelperFailed, "cannot create the request directory: %v", err))
	}
	if err := i.download(ctx, o.Artifact, filepath.Join(sdir, stagedPackage)); err != nil {
		return fail(err)
	}
	if err := writeFile(filepath.Join(sdir, stagedIndex), o.Index); err != nil {
		return fail(errf(ErrCodeDownload, "cannot stage the index: %v", err))
	}
	if err := writeFile(filepath.Join(sdir, stagedBundle), o.Bundle); err != nil {
		return fail(errf(ErrCodeDownload, "cannot stage the signature: %v", err))
	}
	res, err := i.ask(ctx, by, Request{Action: action, Plugin: id, Version: version, Source: o.Source.Name})
	if err != nil {
		return fail(err)
	}
	i.reload()
	i.audit(ctx, by, event, id, fmt.Sprintf("version %s from %s", version, o.Source.Name))
	if res.From != "" {
		from = res.From
	}
	return Outcome{Plugin: id, Action: action, Version: version, From: from, Source: o.Source.Name}, nil
}

func (i *Installer) reload() {
	if i.Reload != nil {
		i.Reload()
	}
}

func (i *Installer) audit(ctx context.Context, by, event, target, detail string) {
	if i.Audit != nil {
		i.Audit(ctx, by, event, target, detail)
	}
}

// ask leaves the request, waits for the helper's result and turns a failure
// into an Error with the helper's code.
func (i *Installer) ask(ctx context.Context, by string, req Request) (Result, error) {
	dir := ApplyDir(i.DataDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Result{}, errf(ErrCodeHelperFailed, "cannot create the request directory: %v", err)
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Result{}, err
	}
	req.ID, req.RequestedBy, req.RequestedAt = hex.EncodeToString(nonce[:]), by, time.Now().UTC()
	if err := WriteRequest(dir, req); err != nil {
		return Result{}, errf(ErrCodeHelperFailed, "cannot leave the request: %v", err)
	}
	wait, poll := i.Wait, i.Poll
	if wait == 0 {
		wait = 2 * time.Minute
	}
	if poll == 0 {
		poll = 200 * time.Millisecond
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		if res, ok := ReadResult(dir); ok && res.RequestID == req.ID {
			if res.Outcome == OutcomeApplied {
				return res, nil
			}
			return res, &Error{CodeStr: ErrCodeHelperFailed, Msg: res.Message, Helper: res.Code}
		}
		select {
		case <-ctx.Done():
			i.withdraw(dir)
			return Result{}, ctx.Err()
		case <-deadline.C:
			i.withdraw(dir)
			return Result{}, errf(ErrCodeHelperTimeout, "the helper did not answer in %s: is regulus-plugin-apply.path enabled? (the request was withdrawn)", wait)
		case <-tick.C:
		}
	}
}

// withdraw removes a request nobody took, so it cannot run later when the
// helper is switched on.
func (i *Installer) withdraw(dir string) { _ = os.Remove(filepath.Join(dir, requestName)) }

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

// download fetches the package of a and writes it to dst, checking the size
// and the SHA-256 that the signed index gives. HTTPS only, except for a
// loopback address (tests, a local mirror).
func (i *Installer) download(ctx context.Context, a Artifact, dst string) error {
	if a.Size <= 0 || a.Size > MaxPackage || len(a.SHA256) != 64 {
		return errf(ErrCodeDownload, "the index gives no usable size and checksum for the package")
	}
	u, err := url.Parse(a.URL)
	if err != nil || u.Host == "" {
		return errf(ErrCodeDownload, "the package address is not a URL")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u.Hostname())) {
		return errf(ErrCodeDownload, "the package address is not https")
	}
	c := i.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return errf(ErrCodeDownload, "%v", err)
	}
	req.Header.Set("User-Agent", "regulus-hub-store")
	resp, err := c.Do(req)
	if err != nil {
		return errf(ErrCodeDownload, "downloading the package: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errf(ErrCodeDownload, "the package server answered %s", resp.Status)
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errf(ErrCodeDownload, "%v", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, a.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return errf(ErrCodeDownload, "downloading the package: %v", err)
	}
	if n != a.Size {
		return errf(ErrCodeDownload, "the package is not %d bytes, as the signed index says", a.Size)
	}
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(h.Sum(nil))), []byte(strings.ToLower(a.SHA256))) != 1 {
		return errf(ErrCodeDownload, "the package does not match the checksum of the signed index")
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
