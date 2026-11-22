# Releases

There are three channels: **stable** (`v0.3.0`), **beta** (`v0.3.0-beta.2`, or `-rc.1`) and **alpha** (`v0.3.0-alpha.1`). To publish, a maintainer runs **Publish release** in the Actions tab on `main`, picking the channel (and, when a new version starts, whether to raise the patch, minor or major number). It refuses a commit on which CI has not passed, works out the next version (`scripts/next-version.sh`: a version goes through its alphas, then its betas, then the release; pre-releases are numbered per channel), tags the commit and starts the release workflow on the tag. Pushing a `v*` tag by hand does the same.

The release workflow has two jobs (#206): **build** (GoReleaser, `npm ci`, Go; read-only token, no `id-token`) makes the archives, packages, SBOMs and `checksums.txt`; **publish** (no install, no build) checks the files against `checksums.txt`, signs it with Sigstore and creates the release with `gh release create`. Only the second job can ask Sigstore for a certificate. GoReleaser (v2.12.0), cosign (v2.6.1) and syft (v1.57.2) are pinned to a version in `release.yml`; the actions are still referenced by tag (see `docs/decisions.md`). The release carries a GitHub Release with `tar.gz` archives, `.deb` and `.rpm` packages (linux amd64 and arm64), SBOMs, `install.sh` and `checksums.txt`, whose Sigstore signature is `checksums.txt.sigstore.json`. Alphas and betas are marked as pre-releases, so GitHub's **Latest** (and `releases/latest/download/install.sh`) is always the newest stable release; each release's notes start with the install command for that exact version. To verify a download:

```sh
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity 'https://github.com/Stellatinus/Regulus/.github/workflows/release.yml@refs/tags/vX.Y.Z' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
sha256sum --check --ignore-missing checksums.txt
```
