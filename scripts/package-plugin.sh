#!/bin/sh
# Builds a plugin from source and packs it into one archive, the layout a plugin
# directory has when Regulus runs it (docs/plugins/writing-a-plugin.md).
#
# Usage: scripts/package-plugin.sh plugins/<id> [output-directory]
#
# It writes <id>_<version>_<os>_<arch>.tar.gz and its .sha256 in the output
# directory (default: dist/plugins). The archive holds one directory named
# after the plugin id with plugin.yaml, the program of each component that has
# one (built for GOOS and GOARCH, default: this machine) and the ui module. To
# install it, unpack it in the plugins directory.
#
# The name and layout are provisional: the plugin index and the installation
# from the dashboard are #20, which decides the final ones. Nothing signs the
# archive yet.
set -eu

src=${1:?usage: scripts/package-plugin.sh plugins/<id> [output-directory]}
out=${2:-dist/plugins}
src=${src%/}
[ -f "$src/plugin.yaml" ] || { echo "no $src/plugin.yaml" >&2; exit 1; }

# Read the few keys we need. The manifest is strict YAML written by hand, one
# key per line; Regulus itself validates it when the plugin starts.
id=$(sed -n 's/^id:[[:space:]]*//p' "$src/plugin.yaml" | head -n 1)
version=$(sed -n 's/^version:[[:space:]]*//p' "$src/plugin.yaml" | head -n 1)
[ -n "$id" ] && [ -n "$version" ] || { echo "$src/plugin.yaml has no id or version" >&2; exit 1; }
[ "$(basename "$src")" = "$id" ] || { echo "the directory $src is not named after the id $id" >&2; exit 1; }

goos=${GOOS:-$(go env GOOS)}
goarch=${GOARCH:-$(go env GOARCH)}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
stage="$work/$id"
mkdir -p "$stage"
cp "$src/plugin.yaml" "$stage/"

# Every "command: <path>" of a component is a program to build: bin/<name>
# comes from the main package of the plugin's directory (one program, as in the
# example) or of ./<src>/cmd/<name> when that exists.
for cmd in $(sed -n 's/^[[:space:]]*command:[[:space:]]*//p' "$src/plugin.yaml" | sort -u); do
	name=$(basename "$cmd")
	pkg="./$src"
	[ -d "$src/cmd/$name" ] && pkg="./$src/cmd/$name"
	mkdir -p "$stage/$(dirname "$cmd")"
	CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -o "$stage/$cmd" "$pkg"
done

module=$(sed -n 's/^[[:space:]]*module:[[:space:]]*//p' "$src/plugin.yaml" | head -n 1)
if [ -n "$module" ]; then
	[ -f "$src/$module" ] || { echo "the manifest names $module, which is not in $src" >&2; exit 1; }
	mkdir -p "$stage/$(dirname "$module")"
	cp "$src/$module" "$stage/$module"
fi

mkdir -p "$out"
archive="$out/${id}_${version}_${goos}_${goarch}.tar.gz"
# A fixed order and time make the same source give the same archive.
(cd "$work" && find "$id" | LC_ALL=C sort | tar --no-recursion --mtime='UTC 1970-01-01' --owner=0 --group=0 --numeric-owner -czf - -T -) >"$archive"
(cd "$out" && sha256sum "$(basename "$archive")" >"$(basename "$archive").sha256")
echo "$archive"
