// Command make-latest writes latest.json, the index of the newest release of
// each channel that the release workflow signs and publishes (#263). It reads
// the list of releases (the JSON of `gh api repos/OWNER/REPO/releases
// --paginate`), counts the release being made, and sets when the index was
// issued and when it expires. It does not sign or upload anything and does not
// touch a hub: .github/workflows/release.yml runs it in the build job, which
// has no signing identity, and signs the file in the publish job. Only
// cmd/regulus is shipped.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/CodexCircle/Regulus/internal/update"
)

func main() {
	releases := flag.String("releases", "", "file with the JSON list of releases from the GitHub API")
	tag := flag.String("tag", "", "the release the index is made for, such as v1.4.0")
	life := flag.Duration("valid-for", 21*24*time.Hour, "how long the index is believed")
	out := flag.String("o", "latest.json", "the file to write")
	flag.Parse()
	if err := run(*releases, *tag, *life, *out, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "make-latest: %v\n", err)
		os.Exit(1)
	}
}

func run(releasesFile, tag string, life time.Duration, out string, now time.Time) error {
	data, err := os.ReadFile(releasesFile)
	if err != nil {
		return err
	}
	ix, err := update.MakeIndex(data, tag, now, life)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return err
	}
	// The hub parses what it reads with ParseIndex: write nothing it would refuse.
	if _, err := update.ParseIndex(b); err != nil {
		return err
	}
	return os.WriteFile(out, append(b, '\n'), 0o644)
}
