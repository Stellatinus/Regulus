package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CodexCircle/Regulus/internal/update"
)

var t0 = time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)

func indexJSON(t *testing.T, mutate func(*Index)) []byte {
	t.Helper()
	ver := func(v, req string, api uint32) IndexVersion {
		return IndexVersion{Version: v, ReleasedAt: "2026-12-01T08:00:00Z", API: api, Requires: req,
			Artifacts:   []Artifact{{OS: "linux", Arch: "amd64", URL: "https://x/" + v, SHA256: "ab", Size: 1}},
			Permissions: []Permission{{ID: "disks.read", Description: "See disks."}}}
	}
	ix := Index{Format: 1, Source: "regulus-plugins", IssuedAt: t0.Add(-time.Hour), Expires: t0.Add(20 * 24 * time.Hour),
		Plugins: []IndexPlugin{
			{ID: "disks", Name: "Disks", Versions: []IndexVersion{ver("0.1.0", ">=0.4.0", 1), ver("0.2.0-alpha.1", "", 1), ver("0.3.0-beta.1", "", 1)}},
			{ID: "alpha-only", Name: "Alpha only", Versions: []IndexVersion{ver("0.1.0-alpha.1", "", 1)}},
		}}
	if mutate != nil {
		mutate(&ix)
	}
	b, err := json.Marshal(ix)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseIndex(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Index)
		raw    string
		want   string // substring of the error, "" for none
		is     error
	}{
		"good":           {},
		"unknown format": {mutate: func(i *Index) { i.Format = 2 }, want: "format", is: ErrFormat},
		"expired":        {mutate: func(i *Index) { i.Expires = t0.Add(-time.Minute) }, want: "expired", is: ErrExpired},
		"future":         {mutate: func(i *Index) { i.IssuedAt = t0.Add(time.Hour) }, want: "future"},
		"no times":       {mutate: func(i *Index) { i.IssuedAt = time.Time{} }, want: "issued_at"},
		"bad id":         {mutate: func(i *Index) { i.Plugins[0].ID = "Bad_ID" }, want: "bad id"},
		"duplicate id":   {mutate: func(i *Index) { i.Plugins[1].ID = "disks" }, want: "twice"},
		"bad version":    {mutate: func(i *Index) { i.Plugins[0].Versions[0].Version = "one" }, want: "not a version"},
		"not json":       {raw: "{", want: "not valid JSON"},
		"unknown fields": {raw: `{"format":1,"issued_at":"2026-12-01T08:00:00Z","expires":"2026-12-20T08:00:00Z","plugins":[],"future":{"x":1}}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			data := []byte(c.raw)
			if c.raw == "" {
				data = indexJSON(t, c.mutate)
			}
			_, err := ParseIndex(data, t0)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if c.is != nil && !errors.Is(err, c.is) {
				t.Errorf("err = %v, want %v", err, c.is)
			}
		})
	}
}

func TestAnOlderIndexIsRefused(t *testing.T) {
	ix, _ := ParseIndex(indexJSON(t, nil), t0)
	if err := CheckNotOlder(ix, ix.IssuedAt.Add(time.Second)); !errors.Is(err, ErrOlder) {
		t.Fatalf("err = %v", err)
	}
	if err := CheckNotOlder(ix, ix.IssuedAt); err != nil {
		t.Fatalf("the same index again: %v", err)
	}
	if err := CheckNotOlder(ix, time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func hub(t *testing.T, v string) Env {
	t.Helper()
	pv, ok := update.ParseBuildVersion(v)
	return Env{Hub: pv, HubKnown: ok, Machine: Machine{"linux", "amd64"}}
}

func TestCheckVersion(t *testing.T) {
	art := []Artifact{{OS: "linux", Arch: "amd64"}}
	cases := map[string]struct {
		v      IndexVersion
		env    Env
		ok     bool
		reason string
	}{
		"fine":                   {IndexVersion{API: 1, Requires: ">=0.4.0", Artifacts: art}, hub(t, "0.4.1"), true, ""},
		"hub too old":            {IndexVersion{API: 1, Requires: ">=0.5.0", Artifacts: art}, hub(t, "0.4.1"), false, "needs Regulus"},
		"requires excludes pre":  {IndexVersion{API: 1, Requires: "<0.5.0", Artifacts: art}, hub(t, "0.5.0-alpha.1"), false, "needs Regulus"},
		"dev build runs any":     {IndexVersion{API: 1, Requires: ">=9.0.0", Artifacts: art}, hub(t, "dev"), true, ""},
		"api too new":            {IndexVersion{API: 2, Artifacts: art}, hub(t, "0.4.1"), false, "plugin API 2"},
		"api zero":               {IndexVersion{API: 0, Artifacts: art}, hub(t, "0.4.1"), false, "plugin API 0"},
		"no artifact for arch":   {IndexVersion{API: 1, Artifacts: []Artifact{{OS: "linux", Arch: "arm64"}}}, hub(t, "0.4.1"), false, "no package for linux/amd64"},
		"unreadable requirement": {IndexVersion{API: 1, Requires: "soon", Artifacts: art}, hub(t, "0.4.1"), false, "cannot be read"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ok, why := CheckVersion(c.v, c.env)
			if ok != c.ok || !strings.Contains(why, c.reason) {
				t.Fatalf("ok=%v why=%q, want ok=%v containing %q", ok, why, c.ok, c.reason)
			}
		})
	}
}

func TestCatalogueFollowsTheChannel(t *testing.T) {
	ix, _ := ParseIndex(indexJSON(t, nil), t0)
	env := hub(t, "0.4.1")
	env.Installed = map[string]string{"disks": "0.1.0"}
	versions := func(ps []PluginJSON, id string) (vs []string) {
		for _, p := range ps {
			if p.ID == id {
				for _, v := range p.Versions {
					vs = append(vs, v.Version)
				}
			}
		}
		return
	}
	stable := Catalogue(ix, "regulus-plugins", update.Stable, env)
	if len(stable) != 1 || strings.Join(versions(stable, "disks"), ",") != "0.1.0" {
		t.Fatalf("stable = %+v: a plugin with only alphas, and pre-releases, must not show", stable)
	}
	if stable[0].Latest == nil || *stable[0].Latest != "0.1.0" || stable[0].Installed == nil || stable[0].Installed.Version != "0.1.0" || stable[0].Audience != "admin" {
		t.Errorf("plugin = %+v", stable[0])
	}
	beta := Catalogue(ix, "regulus-plugins", update.Beta, env)
	if got := strings.Join(versions(beta, "disks"), ","); got != "0.3.0-beta.1,0.1.0" {
		t.Errorf("beta = %s (newest first, no alpha)", got)
	}
	alpha := Catalogue(ix, "regulus-plugins", update.Alpha, env)
	if len(alpha) != 2 || alpha[0].ID != "alpha-only" || strings.Join(versions(alpha, "disks"), ",") != "0.3.0-beta.1,0.2.0-alpha.1,0.1.0" {
		t.Errorf("alpha = %+v", alpha)
	}
}

func TestAnIncompatibleOrYankedVersionIsNeverLatest(t *testing.T) {
	ix, _ := ParseIndex(indexJSON(t, func(i *Index) {
		i.Plugins[0].Versions[0].Yanked = true
		i.Plugins[0].Versions = i.Plugins[0].Versions[:1]
	}), t0)
	got := Catalogue(ix, "s", update.Stable, hub(t, "0.4.1"))
	if len(got) != 1 || got[0].Latest != nil || got[0].Versions[0].Compatible || !got[0].Versions[0].Yanked || got[0].Versions[0].IncompatibleReason == "" {
		t.Fatalf("got %+v", got)
	}
	got = Catalogue(ix, "s", update.Stable, hub(t, "0.3.0"))
	if got[0].Latest != nil {
		t.Errorf("latest = %v", *got[0].Latest)
	}
}

type fakeVerifier struct {
	err    error
	signer string
	calls  int
}

func (f *fakeVerifier) Verify(_ context.Context, _, _ []byte, signer, _ string) error {
	f.calls++
	f.signer = signer
	return f.err
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newService(t *testing.T, files map[string][]byte, v Verifier, c *clock, sources ...Source) *Service {
	t.Helper()
	return New(Options{
		Sources: sources, Verifier: v, Now: c.Now,
		Fetch: func(_ context.Context, url string) ([]byte, error) {
			if b, ok := files[url]; ok {
				return b, nil
			}
			return nil, errors.New("404 " + url)
		},
		Env:       func() Env { return hub(t, "0.4.1") },
		Installed: func() map[string]InstalledInfo { return map[string]InstalledInfo{} },
	})
}

func TestServiceReadsVerifiesAndKeepsTheLastGoodIndex(t *testing.T) {
	src := DefaultSources()[0]
	c := &clock{t0}
	files := map[string][]byte{src.URL: indexJSON(t, nil), src.URL + bundleSuffix: []byte("bundle")}
	v := &fakeVerifier{}
	s := newService(t, files, v, c)

	cat := s.Catalogue(context.Background())
	if len(cat.Sources) != 1 || cat.Sources[0].State != StateOK || cat.Sources[0].Trust != TrustOfficial || len(cat.Plugins) != 1 {
		t.Fatalf("cat = %+v", cat)
	}
	if !strings.Contains(v.signer, "publish-index.yml@refs/heads/main") {
		t.Errorf("verified against %q", v.signer)
	}
	s.Catalogue(context.Background())
	if v.calls != 1 {
		t.Errorf("the index was read %d times inside the TTL", v.calls)
	}

	// The source goes down: the last good index is still offered, with the error.
	c.now = t0.Add(time.Hour)
	delete(files, src.URL)
	cat = s.Catalogue(context.Background())
	if cat.Sources[0].State != StateError || cat.Sources[0].Error == "" || len(cat.Plugins) != 1 {
		t.Fatalf("down: %+v", cat)
	}

	// Past its expiry nothing of it is offered.
	c.now = t0.Add(30 * 24 * time.Hour)
	cat = s.Catalogue(context.Background())
	if cat.Sources[0].State != StateExpired || len(cat.Plugins) != 0 {
		t.Fatalf("expired: %+v", cat)
	}
}

func TestServiceRefusesWhatItCannotVerify(t *testing.T) {
	src := DefaultSources()[0]
	good := map[string][]byte{src.URL: indexJSON(t, nil), src.URL + bundleSuffix: []byte("b")}
	cases := map[string]struct {
		files map[string][]byte
		v     Verifier
		want  string
	}{
		"bad signature": {good, &fakeVerifier{err: errors.New("no match")}, "signature of the index is not valid"},
		"no bundle":     {map[string][]byte{src.URL: good[src.URL]}, &fakeVerifier{}, "signature of the index"},
		"no verifier":   {good, nil, "never offered unverified"},
		"bad format":    {map[string][]byte{src.URL: indexJSON(t, func(i *Index) { i.Format = 9 }), src.URL + bundleSuffix: []byte("b")}, &fakeVerifier{}, "format"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var v Verifier = c.v
			if c.v == nil {
				v = nil
			}
			cat := newService(t, c.files, v, &clock{t0}).Catalogue(context.Background())
			if cat.Sources[0].State != StateError || !strings.Contains(cat.Sources[0].Error, c.want) || len(cat.Plugins) != 0 {
				t.Fatalf("cat = %+v", cat)
			}
		})
	}
}

func TestServiceNeverGoesBackToAnOlderIndex(t *testing.T) {
	src := DefaultSources()[0]
	c := &clock{t0}
	files := map[string][]byte{src.URL: indexJSON(t, nil), src.URL + bundleSuffix: []byte("b")}
	s := newService(t, files, &fakeVerifier{}, c)
	s.Catalogue(context.Background())
	files[src.URL] = indexJSON(t, func(i *Index) { i.IssuedAt = i.IssuedAt.Add(-24 * time.Hour) })
	c.now = t0.Add(time.Hour)
	cat := s.Catalogue(context.Background())
	if cat.Sources[0].State != StateError || !strings.Contains(cat.Sources[0].Error, "older") || len(cat.Plugins) != 1 {
		t.Fatalf("cat = %+v", cat)
	}
}

func TestSamePluginInTwoSourcesIsInstalledFromOneOnly(t *testing.T) {
	off := DefaultSources()[0]
	custom := Source{Name: "mine", URL: "https://mine/index.json", Trust: TrustCustom, Signer: "me"}
	files := map[string][]byte{
		off.URL: indexJSON(t, nil), off.URL + bundleSuffix: []byte("b"),
		custom.URL: indexJSON(t, nil), custom.URL + bundleSuffix: []byte("b"),
	}
	s := newService(t, files, &fakeVerifier{}, &clock{t0}, off, custom)
	s.o.Installed = func() map[string]InstalledInfo { return map[string]InstalledInfo{"disks": {Version: "0.1.0"}} }
	var installedIn []string
	for _, p := range s.Catalogue(context.Background()).Plugins {
		if p.ID == "disks" && p.Installed != nil {
			installedIn = append(installedIn, p.Source)
		}
	}
	if len(installedIn) != 1 || installedIn[0] != OfficialName {
		t.Fatalf("installed shown in %v", installedIn)
	}
}

func TestValidateSources(t *testing.T) {
	ok := Source{Name: "mine", URL: "https://x/index.json", Trust: TrustCustom, Signer: "id"}
	with := func(f func(*Source)) Source { s := ok; f(&s); return s }
	cases := map[string]struct {
		list []Source
		want string
	}{
		"empty is fine":   {nil, ""},
		"good":            {[]Source{ok}, ""},
		"no signer":       {[]Source{with(func(s *Source) { s.Signer = "" })}, "pin the signing identity"},
		"official name":   {[]Source{with(func(s *Source) { s.Name = OfficialName })}, "used already"},
		"claims official": {[]Source{with(func(s *Source) { s.Trust = TrustOfficial })}, "trust must be"},
		"no url":          {[]Source{with(func(s *Source) { s.URL = "" })}, "url is empty"},
		"bad channel":     {[]Source{with(func(s *Source) { s.Channel = "nightly" })}, "unknown channel"},
		"duplicate":       {[]Source{ok, ok}, "used already"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateSources(c.list)
			if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}
