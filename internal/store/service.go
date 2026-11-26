package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/CodexCircle/Regulus/internal/update"
)

// Trust levels of a source.
const (
	TrustOfficial = "official"
	TrustCustom   = "custom"
)

// OfficialName and OfficialURL name the default source, regulus-plugins. The
// URL is where its publish-index workflow is meant to serve the index (GitHub
// Pages); that workflow has not run yet, so nothing answers there today.
const (
	OfficialName = "regulus-plugins"
	OfficialURL  = "https://stellatinus.github.io/Regulus-Plugins/index.json"
	// officialSigner is the identity of the workflow that signs the official
	// index: publish-index.yml at main (docs/plugins/store.md, "The index").
	officialSigner = "https://github.com/Stellatinus/Regulus-Plugins/.github/workflows/publish-index.yml@refs/heads/main"
	officialIssuer = "https://token.actions.githubusercontent.com"
	// bundleSuffix: the Sigstore bundle of index.json is index.json.sigstore.json.
	bundleSuffix = ".sigstore.json"
)

// Source is one place plugins come from: an index address and who must have
// signed it.
type Source struct {
	Name  string `yaml:"name" json:"name"`
	URL   string `yaml:"url" json:"url"`
	Trust string `yaml:"trust" json:"trust"`
	// Channel is the channel followed for this source; empty follows the hub's.
	Channel string `yaml:"channel" json:"channel"`
	// Signer and Issuer pin the signing identity of a custom source (the
	// certificate's subject and the OIDC issuer). A custom source with no
	// Signer is refused: an unverifiable index is never offered.
	Signer string `yaml:"signer" json:"signer"`
	Issuer string `yaml:"issuer" json:"issuer"`
}

// DefaultSources is what a hub with no configuration uses: the official one.
func DefaultSources() []Source {
	return []Source{{Name: OfficialName, URL: OfficialURL, Trust: TrustOfficial, Issuer: officialIssuer, Signer: officialSigner}}
}

// ValidateSources checks a configured list; the official source is not a
// configuration entry and cannot be shadowed.
func ValidateSources(list []Source) error {
	names := map[string]bool{OfficialName: true}
	for i, s := range list {
		switch {
		case s.Name == "" || !idRE.MatchString(s.Name):
			return fmt.Errorf("plugin_sources[%d]: name %q must be lower-case letters, digits and hyphens", i, s.Name)
		case names[s.Name]:
			return fmt.Errorf("plugin_sources[%d]: name %q is used already", i, s.Name)
		case s.URL == "":
			return fmt.Errorf("plugin_sources[%d]: url is empty", i)
		case s.Trust != TrustCustom:
			return fmt.Errorf("plugin_sources[%d]: trust must be %q (the official source is built in)", i, TrustCustom)
		case s.Signer == "":
			return fmt.Errorf("plugin_sources[%d]: a custom source must pin the signing identity (signer)", i)
		case s.Channel != "" && !update.Channel(s.Channel).Valid():
			return fmt.Errorf("plugin_sources[%d]: unknown channel %q", i, s.Channel)
		}
		names[s.Name] = true
	}
	return nil
}

// Verifier checks the Sigstore bundle of an index, made by signer.
type Verifier interface {
	Verify(ctx context.Context, index, bundle []byte, signer, issuer string) error
}

// SigstoreVerifier is the real Verifier: the code of the core's releases
// (update.SigstoreVerifier), with the identity of the source in place of the
// release workflow's. It is **not verified against a real signed index**: no
// index has been published yet.
type SigstoreVerifier struct{ Trust update.TrustSource }

// Verify implements Verifier.
func (v SigstoreVerifier) Verify(ctx context.Context, index, bundle []byte, signer, issuer string) error {
	sv := &update.SigstoreVerifier{Trust: v.Trust, Issuer: issuer, Signer: func(string) string { return signer }}
	return sv.Verify(ctx, "", index, bundle)
}

// Fetcher reads a URL.
type Fetcher func(ctx context.Context, url string) ([]byte, error)

// HTTPFetcher reads over HTTPS (HTTP only for a loopback address, for tests
// and a local mirror) with a size limit.
func HTTPFetcher(c *http.Client) Fetcher {
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	return func(ctx context.Context, url string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "regulus-hub-store")
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s answered %s", url, resp.Status)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, MaxIndexSize+1))
		if err != nil {
			return nil, err
		}
		if len(data) > MaxIndexSize {
			return nil, errors.New("the index is too large")
		}
		return data, nil
	}
}

// Source states of the contract.
const (
	StateOK      = "ok"
	StateError   = "error"
	StateExpired = "expired"
)

// SourceJSON is a source's state in GET /api/store.
type SourceJSON struct {
	Name     string `json:"name"`
	Trust    string `json:"trust"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
	IssuedAt string `json:"issued_at,omitempty"`
	Expires  string `json:"expires,omitempty"`
}

// CatalogueJSON is the answer of GET /api/store.
type CatalogueJSON struct {
	Sources []SourceJSON `json:"sources"`
	Plugins []PluginJSON `json:"plugins"`
}

// Options of a Service.
type Options struct {
	Sources  []Source
	Verifier Verifier
	Fetch    Fetcher
	Now      func() time.Time
	// HubChannel returns the channel the hub follows.
	HubChannel func(context.Context) update.Channel
	// Env returns what versions are checked against; Installed is filled per call.
	Env func() Env
	// Installed returns the installed plugins by id: their version and the
	// source they came from ("" for one put there by hand).
	Installed func() map[string]InstalledInfo
	// TTL is how long a read index is reused; default 15 minutes.
	TTL time.Duration
}

// InstalledInfo is an installed plugin as the catalogue needs it.
type InstalledInfo struct {
	Version string
	Source  string
}

type sourceState struct {
	index     Index
	raw       []byte // the signed bytes of index, and its signature bundle,
	bundle    []byte // kept for the helper, which verifies them again
	have      bool
	lastIssue time.Time // issued_at of the last verified index: never go back
	err       string
	tried     time.Time
}

// Service reads the indexes of its sources and keeps the last good one.
type Service struct {
	o  Options
	mu sync.Mutex
	st map[string]*sourceState
}

// New returns a Service. Sources empty means DefaultSources.
func New(o Options) *Service {
	if len(o.Sources) == 0 {
		o.Sources = DefaultSources()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.TTL == 0 {
		o.TTL = 15 * time.Minute
	}
	if o.Fetch == nil {
		o.Fetch = HTTPFetcher(nil)
	}
	if o.HubChannel == nil {
		o.HubChannel = func(context.Context) update.Channel { return update.Stable }
	}
	return &Service{o: o, st: map[string]*sourceState{}}
}

// refresh reads one source; it keeps the previous good index on failure.
func (s *Service) refresh(ctx context.Context, src Source, st *sourceState) {
	now := s.o.Now()
	st.tried = now
	fail := func(err error) { st.err = err.Error() }
	if s.o.Verifier == nil {
		fail(errors.New("no signature verifier is configured: an index is never offered unverified"))
		return
	}
	data, err := s.o.Fetch(ctx, src.URL)
	if err != nil {
		fail(err)
		return
	}
	bundle, err := s.o.Fetch(ctx, src.URL+bundleSuffix)
	if err != nil {
		fail(fmt.Errorf("the signature of the index: %w", err))
		return
	}
	if err := s.o.Verifier.Verify(ctx, data, bundle, src.Signer, src.Issuer); err != nil {
		fail(fmt.Errorf("the signature of the index is not valid: %w", err))
		return
	}
	ix, err := ParseIndex(data, now)
	if err == nil {
		err = CheckNotOlder(ix, st.lastIssue)
	}
	if err != nil {
		fail(err)
		return
	}
	st.index, st.raw, st.bundle, st.have, st.lastIssue, st.err = ix, data, bundle, true, ix.IssuedAt, ""
}

// Catalogue returns the plugins of every source, reading a source again when
// its last read is older than the TTL. A source that cannot be read is shown
// with its error; its last good index is still used while it has not expired.
func (s *Service) Catalogue(ctx context.Context) CatalogueJSON {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.o.Now()
	hub := s.o.HubChannel(ctx)
	out := CatalogueJSON{Sources: []SourceJSON{}, Plugins: []PluginJSON{}}
	installed := map[string]string{}
	from := map[string]string{}
	if s.o.Installed != nil {
		for id, in := range s.o.Installed() {
			installed[id], from[id] = in.Version, in.Source
		}
	}
	taken := map[string]bool{}
	for _, src := range s.o.Sources {
		st := s.st[src.Name]
		if st == nil {
			st = &sourceState{}
			s.st[src.Name] = st
		}
		if st.tried.IsZero() || now.Sub(st.tried) >= s.o.TTL {
			s.refresh(ctx, src, st)
		}
		sj := SourceJSON{Name: src.Name, Trust: src.Trust, State: StateOK, Error: st.err}
		if st.have {
			sj.IssuedAt, sj.Expires = st.index.IssuedAt.UTC().Format(time.RFC3339), st.index.Expires.UTC().Format(time.RFC3339)
			if !now.Before(st.index.Expires) {
				sj.State = StateExpired
				if sj.Error == "" {
					sj.Error = ErrExpired.Error()
				}
			}
		}
		if st.err != "" && sj.State == StateOK {
			sj.State = StateError
		}
		out.Sources = append(out.Sources, sj)
		if !st.have || sj.State == StateExpired {
			continue
		}
		ch := hub
		if src.Channel != "" {
			ch = update.Channel(src.Channel)
		}
		env := s.o.Env()
		env.Installed, env.InstalledFrom = installed, from
		for _, p := range Catalogue(st.index, src.Name, ch, env) {
			// An installed plugin remembers the source it came from and is
			// shown as installed only there. One put there by hand has no
			// source: the first source in the list that offers the id owns it.
			if taken[p.ID] && from[p.ID] == "" {
				p.Installed = nil
			}
			taken[p.ID] = true
			out.Plugins = append(out.Plugins, p)
		}
	}
	return out
}
