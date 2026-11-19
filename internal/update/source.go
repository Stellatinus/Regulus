package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Release is a published version of the core.
type Release struct {
	Version     Version
	Tag         string
	URL         string // page of the release, for the release notes
	Notes       string
	PublishedAt time.Time
	Assets      []Asset
}

// Asset is a file attached to a release.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// Asset returns the asset with the given name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Source lists the releases of the core, in any order.
type Source interface {
	Releases(ctx context.Context) ([]Release, error)
}

// Channel decides which releases are offered.
type Channel string

const (
	// Stable offers releases only.
	Stable Channel = "stable"
	// Beta offers releases and beta pre-releases, whichever is newest.
	Beta Channel = "beta"
	// Alpha offers every release, alpha pre-releases included.
	Alpha Channel = "alpha"
)

// Valid reports whether c is a known channel.
func (c Channel) Valid() bool { return c == Stable || c == Beta || c == Alpha }

// rank orders the channels from the most conservative: a channel offers the
// releases whose own channel ranks at most as high.
func (c Channel) rank() int {
	switch c {
	case Stable:
		return 0
	case Beta:
		return 1
	default:
		return 2
	}
}

// Offers reports whether a release of version v is offered on channel c.
func (c Channel) Offers(v Version) bool { return v.Channel().rank() <= c.rank() }

// Latest returns the newest release on the channel, or ok = false when there
// is none.
func Latest(releases []Release, channel Channel) (best Release, ok bool) {
	for _, r := range releases {
		if !channel.Offers(r.Version) {
			continue
		}
		if !ok || r.Version.Compare(best.Version) > 0 {
			best, ok = r, true
		}
	}
	return best, ok
}

// GitHubSource reads the releases of a repository from the GitHub API. The
// repository is public, so no token is needed; the unauthenticated limit
// (60 requests an hour per address) is respected with conditional requests
// and by the caller checking only every few hours.
type GitHubSource struct {
	// Repo is "owner/name".
	Repo string
	// BaseURL is the API address; empty means https://api.github.com.
	BaseURL string
	// UserAgent identifies the hub to GitHub.
	UserAgent string
	Client    *http.Client

	// allowHTTP lets tests use a plain HTTP server.
	allowHTTP bool

	mu       sync.Mutex
	etag     string
	cached   []Release
	cachedOK bool
}

// maxListBytes bounds the answer of the releases API.
const maxListBytes = 8 << 20

// tagRE selects the tags of the core ("v1.4.0"); plugin releases such as
// "plugin/docker/v0.3.1" are not the core's.
var tagRE = regexp.MustCompile(`^v[0-9]`)

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// maxNotes bounds the release notes kept and shown.
const maxNotes = 8 << 10

// Releases fetches the recent releases of the core. GitHub lists releases
// newest first, plugin releases and drafts included, so one page can be
// filled with releases that are not the core's (#213): pages are read until
// one holds a stable release of the core (the newest stable one is then
// known, and every pre-release newer than it was on that page or before),
// the list ends, or maxPages were read. Only the first page is asked
// conditionally: when it did not change, nothing did, and the whole cached
// list is reused.
func (g *GitHubSource) Releases(ctx context.Context) ([]Release, error) {
	g.mu.Lock()
	etag := ""
	if g.cachedOK {
		etag = g.etag
	}
	g.mu.Unlock()

	var all []Release
	newETag := ""
	for page := 1; page <= maxPages; page++ {
		raw, tag, notModified, err := g.page(ctx, page, etag)
		if err != nil {
			return nil, err
		}
		if notModified {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.cachedOK {
				return g.cached, nil
			}
			return nil, errors.New("the release server answered 304 without a previous answer")
		}
		if page == 1 {
			newETag, etag = tag, "" // later pages are always read in full
		}
		got := g.convert(raw)
		all = append(all, got...)
		if len(raw) < perPage || hasStable(got) {
			break
		}
	}
	g.mu.Lock()
	g.etag, g.cached, g.cachedOK = newETag, all, true
	g.mu.Unlock()
	return all, nil
}

const (
	// perPage is the most the releases API returns at once.
	perPage = 100
	// maxPages bounds a check to a few requests of the 60 an hour GitHub
	// allows without a token: 500 releases with no stable core release among
	// them is not a list to keep reading.
	maxPages = 5
)

func hasStable(releases []Release) bool {
	for _, r := range releases {
		if !r.Version.Prerelease() {
			return true
		}
	}
	return false
}

// page reads one page of the list. With etag set, a 304 answer is reported
// as notModified.
func (g *GitHubSource) page(ctx context.Context, n int, etag string) (raw []ghRelease, tag string, notModified bool, err error) {
	base := strings.TrimRight(g.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	u := fmt.Sprintf("%s/repos/%s/releases?per_page=%d&page=%d", base, g.Repo, perPage, n)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.UserAgent != "" {
		req.Header.Set("User-Agent", g.UserAgent)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	client := g.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("cannot reach the release server: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	switch {
	case res.StatusCode == http.StatusNotModified && etag != "":
		return nil, "", true, nil
	case res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusTooManyRequests:
		return nil, "", false, rateLimited(res)
	case res.StatusCode != http.StatusOK:
		return nil, "", false, fmt.Errorf("the release server answered %s", res.Status)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxListBytes)).Decode(&raw); err != nil {
		return nil, "", false, fmt.Errorf("cannot read the list of releases: %w", err)
	}
	return raw, res.Header.Get("ETag"), false, nil
}

func (g *GitHubSource) convert(raw []ghRelease) []Release {
	var out []Release
	for _, r := range raw {
		if r.Draft || !tagRE.MatchString(r.TagName) {
			continue
		}
		v, err := ParseVersion(r.TagName)
		if err != nil || v.Prerelease() != r.Prerelease {
			// A tag that is not a version, or a release whose flag disagrees
			// with its tag, is not something to install by itself.
			continue
		}
		rel := Release{Version: v, Tag: r.TagName, URL: r.HTMLURL, Notes: clip(r.Body, maxNotes), PublishedAt: r.PublishedAt.UTC()}
		for _, a := range r.Assets {
			if !g.safeURL(a.URL) {
				continue
			}
			rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
		}
		out = append(out, rel)
	}
	return out
}

// safeURL accepts only HTTPS addresses (HTTP in tests), so that an answer
// cannot send the downloader to a plain-text address.
func (g *GitHubSource) safeURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "https" || (g.allowHTTP && u.Scheme == "http")
}

func rateLimited(res *http.Response) error {
	if res.Header.Get("X-RateLimit-Remaining") != "0" && res.StatusCode != http.StatusTooManyRequests {
		return fmt.Errorf("the release server refused the request: %s", res.Status)
	}
	if reset, err := strconv.ParseInt(res.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		return fmt.Errorf("the release server's request limit is used up until %s", time.Unix(reset, 0).UTC().Format(time.RFC3339))
	}
	return errors.New("the release server's request limit is used up; try again later")
}

// clip shortens s to at most n bytes without cutting a character in two.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}
