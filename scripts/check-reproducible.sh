#!/bin/sh
# Builds the archives and packages twice, from a clean build cache each time,
# and fails when a file differs between the two builds (#215). It uses
# GoReleaser's snapshot mode (no tag, nothing signed or published), so it
# checks that the build is a function of the commit: `mod_timestamp` and the
# archive and package times in .goreleaser.yaml are the time of the commit, and
# the binary is built with -trimpath.
#
# Usage: scripts/check-reproducible.sh   (needs goreleaser, go, node and git
# with the history of HEAD; run it in a clean checkout)
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)
cd "$here"
# The default directory is removed on exit; one given by REPRODUCIBLE_DIR is kept.
if [ -n "${REPRODUCIBLE_DIR:-}" ]; then
	out=$REPRODUCIBLE_DIR
	mkdir -p "$out"
else
	out=$(mktemp -d)
	trap 'rm -rf "$out"' EXIT
fi
# A build makes at least the archives and the packages of two architectures;
# fewer files means that GoReleaser produced nothing, not that it is reproducible.
min=${REPRODUCIBLE_MIN_FILES:-4}

build() {
	go clean -cache
	rm -rf dist dashboard/dist
	if ! goreleaser release --snapshot --clean --skip=publish,sign,sbom,announce >"$out/goreleaser.log" 2>&1; then
		cat "$out/goreleaser.log" >&2
		echo "goreleaser failed" >&2
		exit 1
	fi
	(cd dist && for f in *.tar.gz *.deb *.rpm; do [ -e "$f" ] && sha256sum "$f"; done) | sort -k2 >"$out/$1"
}

build first
build second

count=$(wc -l <"$out/first")
if [ "$count" -lt "$min" ]; then
	echo "only $count files were built (at least $min expected): nothing to compare" >&2
	exit 1
fi

if ! diff -u "$out/first" "$out/second"; then
	echo "not reproducible: the files above differ between two builds of $(git rev-parse --short HEAD)" >&2
	exit 1
fi
echo "reproducible: $(wc -l <"$out/first") files are identical in two builds of $(git rev-parse --short HEAD)"
