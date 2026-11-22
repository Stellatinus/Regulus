package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The signed release index (#263). The release workflow publishes
// latest.json, signed with the same Sigstore identity as checksums.txt, as an
// asset of a release. It names the newest release of each channel and says
// when it was issued and when it stops being believed. Signatures protect what
// is installed; this protects the knowledge that something newer exists: a
// server that answers and leaves new releases out (a lying mirror or proxy, a
// compromised account that hides a release) cannot be told from "no news", so
// every hub would stay on an old version with no warning (a freeze attack).
const (
	// IndexName and IndexBundleName are the assets of the index.
	IndexName       = "latest.json"
	IndexBundleName = "latest.json.sigstore.json"

	// maxIndexLife bounds how long an index may claim to be valid, so that a
	// signature made once cannot hide releases for ever.
	maxIndexLife = 90 * 24 * time.Hour
)

// The problems found with the index, as Status reports them (IndexProblem).
// The dashboard has a text for each in `updates.index.<code>`.
const (
	// IndexExpired: the newest index the hub could verify has expired.
	IndexExpired = "expired"
	// IndexReplayed: the index offered is older than one the hub already
	// verified, so an older answer is being replayed.
	IndexReplayed = "replayed"
	// IndexHidden: the index names a newer release than the list of releases
	// holds (detail: its version).
	IndexHidden = "hidden"
	// IndexMissing: the hub verified an index before and the release server
	// now offers none.
	IndexMissing = "missing"
	// IndexInvalid: the index could not be fetched or its signature or
	// contents were refused (detail: why).
	IndexInvalid = "invalid"
)

// Index is the content of latest.json.
type Index struct {
	// Tag is the release the file is attached to and the one whose workflow run
	// signed it.
	Tag string `json:"tag"`
	// IssuedAt is when the index was made; a hub never accepts one older than
	// the last it verified. Expires is when it stops being believed.
	IssuedAt time.Time `json:"issued_at"`
	Expires  time.Time `json:"expires"`
	// Channels is the newest release tag of each channel; a channel with no
	// release is left out.
	Channels map[Channel]string `json:"channels"`
}

// ParseIndex reads and checks latest.json. Unknown fields are ignored, so a
// newer release workflow can add some.
func ParseIndex(data []byte) (Index, error) {
	var ix Index
	if err := json.Unmarshal(data, &ix); err != nil {
		return Index{}, fmt.Errorf("the release index is not valid JSON: %w", err)
	}
	if _, err := ParseVersion(ix.Tag); err != nil || !tagRE.MatchString(ix.Tag) {
		return Index{}, fmt.Errorf("the release index names an invalid tag %q", ix.Tag)
	}
	if ix.IssuedAt.IsZero() || ix.Expires.IsZero() || !ix.Expires.After(ix.IssuedAt) {
		return Index{}, errors.New("the release index has no valid issued_at and expires")
	}
	if ix.Expires.Sub(ix.IssuedAt) > maxIndexLife {
		return Index{}, errors.New("the release index is valid for longer than any index should be")
	}
	for c, tag := range ix.Channels {
		v, err := ParseVersion(tag)
		if !c.Valid() || err != nil || !c.Offers(v) || v.Channel() != c {
			return Index{}, fmt.Errorf("the release index lists %q as the newest of channel %q", tag, c)
		}
	}
	return ix, nil
}

// Newest is the newest release the index names on a channel that offers it
// (a beta hub also learns of the stable releases), or false when it names
// none.
func (ix Index) Newest(channel Channel) (Version, bool) {
	var best Version
	var ok bool
	for _, tag := range ix.Channels {
		v, err := ParseVersion(tag)
		if err != nil || !channel.Offers(v) {
			continue
		}
		if !ok || v.Compare(best) > 0 {
			best, ok = v, true
		}
	}
	return best, ok
}

// IndexInfo is the verified index, as shown.
type IndexInfo struct {
	Tag      string    `json:"tag"`
	IssuedAt time.Time `json:"issued_at"`
	Expires  time.Time `json:"expires"`
}

// indexResult is what one look at the index found.
type indexResult struct {
	info    *IndexInfo
	problem string
	detail  string
	// issuedAt is the newest issue time ever verified (the replay mark).
	issuedAt time.Time
}

// checkIndex looks for the signed index among the releases, verifies it and
// compares it with what the list of releases says. seen is the issue time of
// the newest index verified before (zero: none yet). A hub that never saw one
// is told nothing when there is none, because releases made before the index
// existed carry none; after it has seen one, its absence is a problem.
func (m *Manager) checkIndex(ctx context.Context, releases []Release, channel Channel, seen time.Time) indexResult {
	res := indexResult{issuedAt: seen}
	var holder *Release
	for i := range releases {
		r := &releases[i]
		if _, ok := r.Asset(IndexName); !ok {
			continue
		}
		if _, ok := r.Asset(IndexBundleName); !ok {
			continue
		}
		if holder == nil || r.Version.Compare(holder.Version) > 0 {
			holder = r
		}
	}
	if holder == nil {
		if !seen.IsZero() {
			res.problem = IndexMissing
		}
		return res
	}
	fail := func(err error) indexResult {
		res.problem, res.detail = IndexInvalid, err.Error()
		return res
	}
	file, _ := holder.Asset(IndexName)
	sig, _ := holder.Asset(IndexBundleName)
	data, err := m.fetchSmall(ctx, file.URL)
	if err != nil {
		return fail(fmt.Errorf("download %s: %w", IndexName, err))
	}
	bundle, err := m.fetchSmall(ctx, sig.URL)
	if err != nil {
		return fail(fmt.Errorf("download %s: %w", IndexBundleName, err))
	}
	if err := m.opts.Verifier.Verify(ctx, holder.Tag, data, bundle); err != nil {
		return fail(err)
	}
	ix, err := ParseIndex(data)
	if err != nil {
		return fail(err)
	}
	if ix.Tag != holder.Tag {
		return fail(fmt.Errorf("the release index says it belongs to %s but is attached to %s", ix.Tag, holder.Tag))
	}
	if ix.IssuedAt.Before(seen) {
		res.problem = IndexReplayed
		res.detail = ix.IssuedAt.UTC().Format(time.RFC3339)
		return res
	}
	res.issuedAt = ix.IssuedAt
	res.info = &IndexInfo{Tag: ix.Tag, IssuedAt: ix.IssuedAt.UTC(), Expires: ix.Expires.UTC()}
	if newest, ok := ix.Newest(channel); ok {
		if listed, ok := Latest(releases, channel); !ok || newest.Compare(listed.Version) > 0 {
			res.problem, res.detail = IndexHidden, newest.String()
			return res
		}
	}
	if !m.opts.Now().Before(ix.Expires) {
		res.problem, res.detail = IndexExpired, ix.Expires.UTC().Format(time.RFC3339)
	}
	return res
}

func (m *Manager) fetchSmall(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	client := m.opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %s", strings.TrimSpace(res.Status))
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxSmallFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSmallFile {
		return nil, errors.New("file is unexpectedly large")
	}
	return data, nil
}

// MakeIndex builds the index for the release workflow (cmd/make-latest) from
// the JSON the GitHub releases API returns (one array, or several concatenated
// as `gh api --paginate` prints them). tag is the release the index is made
// for: it is counted even when the list does not hold it yet (the release is
// created after the index), and it is the tag the file belongs to and is
// signed at. life is how long the index is believed.
func MakeIndex(releasesJSON []byte, tag string, now time.Time, life time.Duration) (Index, error) {
	if _, err := ParseVersion(tag); err != nil || !tagRE.MatchString(tag) {
		return Index{}, fmt.Errorf("%q is not a release tag", tag)
	}
	if life <= 0 || life > maxIndexLife {
		return Index{}, fmt.Errorf("the index must be valid for more than nothing and at most %s", maxIndexLife)
	}
	var raw []ghRelease
	dec := json.NewDecoder(bytes.NewReader(releasesJSON))
	for dec.More() {
		var page []ghRelease
		if err := dec.Decode(&page); err != nil {
			return Index{}, fmt.Errorf("the list of releases is not valid: %w", err)
		}
		raw = append(raw, page...)
	}
	v, _ := ParseVersion(tag)
	raw = append(raw, ghRelease{TagName: tag, Prerelease: v.Prerelease()})
	best := map[Channel]Version{}
	for _, r := range (&GitHubSource{}).convert(raw) {
		c := r.Version.Channel()
		if cur, ok := best[c]; !ok || r.Version.Compare(cur) > 0 {
			best[c] = r.Version
		}
	}
	ix := Index{Tag: tag, IssuedAt: now.UTC().Truncate(time.Second), Channels: map[Channel]string{}}
	ix.Expires = ix.IssuedAt.Add(life)
	for c, v := range best {
		ix.Channels[c] = "v" + v.String()
	}
	return ix, nil
}
