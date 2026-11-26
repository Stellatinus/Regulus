#!/bin/sh
# Decides whether a change touches only documents, so that CI can skip what a
# document cannot break (#488).
#
# Usage: scripts/ci-changes.sh [BASE [HEAD]]
#
# Prints `docs_only=true` or `docs_only=false` (the form $GITHUB_OUTPUT takes).
# With no arguments it compares HEAD^1 with HEAD: for a pull request CI checks
# out the merge of the pull request into its base, whose first parent is the
# base, so that is the change of the pull request.
#
# Only these paths count as documents: docs/, any *.md, LICENSE, and the issue
# and pull request templates. Everything else (code, scripts, workflows, the
# dashboard, packaging, go.mod) is not, and the Go tests that read documents
# (TestNoStaleRepositoryName) still run for a documents-only change.
#
# Whatever it cannot decide counts as `false`, so CI then runs everything: an
# empty list of files, a commit it cannot read, a path with an odd name. The
# exit status is 0 in every case, so a failure here never fails CI by itself.
set -u

base=${1:-HEAD^1}
head=${2:-HEAD}

if ! files=$(git -c core.quotepath=off diff --name-only --no-renames "$base" "$head" 2>/dev/null); then
	echo "docs_only=false"
	exit 0
fi
if [ -z "$files" ]; then
	echo "docs_only=false"
	exit 0
fi

only_docs=true
# One path per line; a path that is quoted by git (it holds a newline or a quote) is not a document.
while IFS= read -r f; do
	case $f in
	'"'*) only_docs=false ;;
	docs/* | *.md | LICENSE | .github/ISSUE_TEMPLATE/*) ;;
	*) only_docs=false ;;
	esac
done <<EOF2
$files
EOF2
echo "docs_only=$only_docs"
