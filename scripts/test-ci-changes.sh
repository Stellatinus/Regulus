#!/bin/sh
# Tests scripts/ci-changes.sh on a throw-away repository (#488). Run from
# anywhere; needs git. Part of `make test` and of the Scripts job of CI.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
script=$here/ci-changes.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"
git init -q .
git config user.email test@example.invalid
git config user.name test
git config commit.gpgsign false

fail=0
commit() { git add -A && git commit -q -m "$1"; }
expect() { # expect WANT DESCRIPTION [ARGS...]
	want=$1 what=$2
	shift 2
	got=$(sh "$script" "$@")
	if [ "$got" = "docs_only=$want" ]; then
		echo "ok   $what"
	else
		echo "FAIL $what: got '$got', want 'docs_only=$want'"
		fail=1
	fi
}

mkdir -p docs cmd .github/ISSUE_TEMPLATE .github/workflows
echo a >docs/a.md
echo a >README.md
echo a >cmd/main.go
echo a >.github/workflows/ci.yml
commit base
git tag t-base

echo b >>docs/a.md
commit "a document"
git tag t-doc
expect true "a document only"

echo b >>README.md
echo b >>docs/a.md
echo x >docs/diagram.svg
commit "documents, one of them not markdown"
expect true "everything under docs/ and any markdown file count"

echo b >>cmd/main.go
commit "code"
expect false "code"

echo b >>docs/a.md
echo b >>.github/workflows/ci.yml
commit "a document and a workflow"
expect false "a document with a workflow"

echo b >.github/ISSUE_TEMPLATE/bug.yml
echo c >LICENSE
commit "templates and licence"
expect true "issue templates and the licence"

git mv cmd/main.go docs/main.md
commit "a code file renamed into docs"
expect false "a rename out of code is a code change"

git commit -q --allow-empty -m "nothing"
expect false "an empty change is not known to be documents only"

expect false "a commit that does not exist" does-not-exist HEAD

printf 'weird\n' >'docs/with"quote.md'
commit "a path that git quotes"
expect false "a quoted path"

expect true "an explicit range" t-base t-doc

[ "$fail" = 0 ] && echo "all ci-changes checks passed"
exit "$fail"
