package update

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// VerifiedSignature: the checksums file carries a valid Sigstore signature
// made by the release workflow of this repository for this very tag, and the
// archive matches it.
const VerifiedSignature = "signature"

// The identity that signs releases: the GitHub Actions run of the release
// workflow, at the tag of the release (see .goreleaser.yaml and release.yml).
const (
	signerIssuer   = "https://token.actions.githubusercontent.com"
	signerWorkflow = "https://github.com/" + Repository + "/.github/workflows/release.yml@refs/tags/"
)

// Repository is the GitHub repository ("owner/name") that publishes releases.
// It is the one place that names it in Go code: the release feed, the
// signature identity and the links shown in the dashboard follow it. After a
// move of the repository, change this, packaging/install.sh (REPO) and the
// links in the documents (TestNoStaleRepositoryName finds the ones left
// behind). A signature made from any other repository is refused, so
// binaries built before a move cannot install releases published after it.
const Repository = "Stellatinus/Regulus"

// Verifier checks the signature of a release's checksums.txt.
type Verifier interface {
	// Verify checks the Sigstore bundle of checksums for the release at tag.
	Verify(ctx context.Context, tag string, checksums, bundleJSON []byte) error
}

// trustedRootJSON is a snapshot of the public Sigstore trusted root, used when
// the up-to-date one cannot be fetched. It is **not** harmless when stale: a
// root that is out of date keeps trusting keys and certificate authorities
// that Sigstore has retired or revoked, so a signature that the live root
// would refuse can pass (#214). That is why the use of this snapshot is logged
// and shown on the Updates page (TrustState), and why a scheduled workflow
// keeps the file current (.github/workflows/trusted-root.yml). A hub that can
// reach the Sigstore TUF repository follows rotations by itself (see
// docs/updates.md).
//
//go:embed trusted_root.json
var trustedRootJSON []byte

// TrustSource provides the Sigstore trusted material.
type TrustSource func(ctx context.Context) (root.TrustedMaterial, error)

// SigstoreVerifier verifies bundles made by cosign's keyless signing.
type SigstoreVerifier struct {
	// Trust provides the trusted root; nil uses PublicTrust with no cache.
	Trust TrustSource
	// Signer is the expected identity (the certificate's subject alternative
	// name) for a tag; nil expects the release workflow of this repository.
	Signer func(tag string) string
	// Issuer is the expected OIDC issuer; empty means GitHub Actions.
	Issuer string

	// withoutSCT drops the certificate timestamp requirement; the virtual
	// Sigstore of the tests does not issue them.
	withoutSCT bool
}

// Verify implements Verifier.
func (v *SigstoreVerifier) Verify(ctx context.Context, tag string, checksums, bundleJSON []byte) error {
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return fmt.Errorf("the signature file is not a Sigstore bundle: %w", err)
	}
	trust := v.Trust
	if trust == nil {
		trust = PublicTrust("")
	}
	material, err := trust(ctx)
	if err != nil {
		return err
	}
	return v.verifyEntity(material, &b, tag, checksums)
}

func (v *SigstoreVerifier) verifyEntity(material root.TrustedMaterial, entity verify.SignedEntity, tag string, checksums []byte) error {
	opts := []verify.VerifierOption{verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1)}
	if !v.withoutSCT {
		opts = append(opts, verify.WithSignedCertificateTimestamps(1))
	}
	verifier, err := verify.NewVerifier(material, opts...)
	if err != nil {
		return fmt.Errorf("signature verifier: %w", err)
	}
	signer := signerWorkflow + tag
	if v.Signer != nil {
		signer = v.Signer(tag)
	}
	issuer := v.Issuer
	if issuer == "" {
		issuer = signerIssuer
	}
	identity, err := verify.NewShortCertificateIdentity(issuer, "", signer, "")
	if err != nil {
		return err
	}
	policy := verify.NewPolicy(verify.WithArtifact(bytes.NewReader(checksums)), verify.WithCertificateIdentity(identity))
	if _, err := verifier.Verify(entity, policy); err != nil {
		return fmt.Errorf("the signature of the release checksums is not valid for %s: %w", signer, err)
	}
	return nil
}

const (
	trustRefreshEvery = 12 * time.Hour
	trustFetchTimeout = 20 * time.Second
)

// trustRetryEmbedded is how soon the TUF repository is asked again while the
// embedded snapshot is in use, so that the warning goes away soon after the
// network is back.
var trustRetryEmbedded = time.Hour

// TrustState says where the trusted root of the last verification came from.
type TrustState struct {
	// Embedded is true while the snapshot compiled into the binary is in use
	// because the Sigstore TUF repository could not be reached and no earlier
	// fetch worked (a stale snapshot does not know about key revocations).
	Embedded bool
	// Reason is why the fetch failed; empty unless Embedded.
	Reason string
}

// PublicTrust returns the trust source for the public Sigstore instance. It
// asks the Sigstore TUF repository for the current trusted root (so a key
// rotation reaches a hub that nobody updated), keeping the answer in cacheDir
// (empty: not cached on disk), and falls back to the snapshot embedded in the
// binary when the repository cannot be reached.
func PublicTrust(cacheDir string) TrustSource {
	return PublicTrustReporting(cacheDir, nil)
}

// PublicTrustReporting is PublicTrust that calls report whenever the source of
// the root changes: with Embedded set when it falls back to the snapshot, and
// with the zero state when a fetch works again. report may be nil; it is not
// called with the lock held.
func PublicTrustReporting(cacheDir string, report func(TrustState)) TrustSource {
	var (
		mu       sync.Mutex
		current  root.TrustedMaterial
		fetched  time.Time
		embedded bool
	)
	// get returns the root, and the new state when the source changed.
	get := func(ctx context.Context) (root.TrustedMaterial, *TrustState, error) {
		mu.Lock()
		defer mu.Unlock()
		ttl := trustRefreshEvery
		if embedded {
			ttl = trustRetryEmbedded
		}
		if current != nil && time.Since(fetched) < ttl {
			return current, nil, nil
		}
		tr, fetchErr := fetchTrustedRootFunc(ctx, cacheDir)
		if fetchErr != nil {
			if current != nil {
				fetched = time.Now() // keep the last good one, ask again later
				return current, nil, nil
			}
			emb, err := root.NewTrustedRootFromJSON(trustedRootJSON)
			if err != nil {
				return nil, nil, fmt.Errorf("embedded Sigstore trusted root: %w", err)
			}
			embedded = true
			current, fetched = emb, time.Now()
			return emb, &TrustState{Embedded: true, Reason: fetchErr.Error()}, nil
		}
		var state *TrustState
		if embedded {
			embedded = false
			state = &TrustState{}
		}
		current, fetched = tr, time.Now()
		return tr, state, nil
	}
	return func(ctx context.Context) (root.TrustedMaterial, error) {
		material, state, err := get(ctx)
		if state != nil && report != nil {
			report(*state)
		}
		return material, err
	}
}

// fetchTrustedRootFunc is replaced in tests.
var fetchTrustedRootFunc = fetchTrustedRoot

func fetchTrustedRoot(ctx context.Context, cacheDir string) (*root.TrustedRoot, error) {
	opts := tuf.DefaultOptions()
	if cacheDir != "" {
		opts = opts.WithCachePath(cacheDir)
	} else {
		opts = opts.WithDisableLocalCache()
	}
	type result struct {
		tr  *root.TrustedRoot
		err error
	}
	done := make(chan result, 1)
	go func() {
		tr, err := root.FetchTrustedRootWithOptions(opts)
		done <- result{tr, err}
	}()
	select {
	case r := <-done:
		return r.tr, r.err
	case <-time.After(trustFetchTimeout):
		return nil, errors.New("timeout fetching the Sigstore trusted root")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
