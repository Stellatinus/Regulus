#!/bin/sh
# Tests scripts/package-plugin.sh on the example plugin.
#
# Usage: scripts/test-package-plugin.sh
set -eu

here=$(cd "$(dirname "$0")" && pwd)
cd "$here/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
failures=0

check() {
	if eval "$2"; then
		printf 'ok   %s\n' "$1"
	else
		printf 'FAIL %s\n' "$1"
		failures=$((failures + 1))
	fi
}

archive=$(scripts/package-plugin.sh plugins/example-hello "$tmp/out")
check "the archive has the documented name" '[ "$(basename "$archive")" = "example-hello_0.1.0_$(go env GOOS)_$(go env GOARCH).tar.gz" ]'
check "the checksum file verifies" '(cd "$tmp/out" && sha256sum -c "$(basename "$archive").sha256" >/dev/null)'

mkdir "$tmp/plugins"
tar -xzf "$archive" -C "$tmp/plugins"
check "plugin.yaml is in the plugin directory" '[ -f "$tmp/plugins/example-hello/plugin.yaml" ]'
check "the program is executable" '[ -x "$tmp/plugins/example-hello/bin/example-hello" ]'
check "the widget module is in" '[ -f "$tmp/plugins/example-hello/ui/widgets.js" ]'
check "nothing else is in" '[ "$(find "$tmp/plugins/example-hello" -type f | wc -l)" -eq 3 ]'

# The same source gives the same archive.
again=$(scripts/package-plugin.sh plugins/example-hello "$tmp/out2")
check "the archive is reproducible" 'cmp -s "$archive" "$again"'

# Refusals.
check "a directory without a manifest is refused" '! scripts/package-plugin.sh plugins/nothing "$tmp/out3" 2>/dev/null'
mkdir -p "$tmp/wrong/other"
printf 'id: example\nversion: 1.0.0\n' >"$tmp/wrong/other/plugin.yaml"
check "a directory not named after the id is refused" '! scripts/package-plugin.sh "$tmp/wrong/other" "$tmp/out4" 2>/dev/null'

# A path that leaves the plugin directory is refused (#402).
for bad in /etc/passwd ../outside ui/../../outside; do
	mkdir -p "$tmp/bad/example"
	printf 'id: example\nversion: 1.0.0\nui:\n  module: %s\n' "$bad" >"$tmp/bad/example/plugin.yaml"
	check "the path $bad in the manifest is refused" '! scripts/package-plugin.sh "$tmp/bad/example" "$tmp/out5" 2>"$tmp/err" && grep -q "leaves the plugin directory" "$tmp/err"'
done

[ "$failures" -eq 0 ] || { echo "$failures failed"; exit 1; }
