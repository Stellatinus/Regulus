#!/bin/sh
# Started by regulus-update-recover.service when the machine starts with an
# update unsettled (a power cut between replacing the executable and confirming
# the new version), before Regulus itself starts.
#
# The work is done by "regulus update-apply --recover --before-start". This
# script exists for the one case that command cannot handle: the executable
# itself cannot run (a damaged file system after the power cut). Then the copy
# of the previous version, which the update helper keeps in root's own
# directory, is put back with nothing but the shell, and the command is run
# from it.
#
# Usage: update-recover [STATE_DIR [EXECUTABLE]]   (the arguments are for tests)
set -u

state=${1:-/var/lib/regulus-update}
bin=${2:-/usr/bin/regulus}
marker=$state/pending.json
backup=$state/regulus.previous

[ -e "$marker" ] || exit 0

settle() {
	exec "$bin" update-apply --recover --before-start --state-dir "$state"
}

# runs FILE: true when FILE is a program that answers "version" with something.
# An empty file is not one, although a shell would "run" it without an error.
runs() {
	[ -s "$1" ] && [ -n "$("$1" version 2>/dev/null)" ]
}

if runs "$bin"; then
	settle
fi

echo "regulus: $bin cannot run after an interrupted update; putting the previous version back" >&2
if [ ! -f "$backup" ] || [ -L "$backup" ] || [ ! -s "$backup" ]; then
	echo "regulus: there is no usable copy of the previous version in $state; install Regulus again with install.sh (the data is kept)" >&2
	exit 1
fi

# The copy is tried from the directory of the executable, not from the state
# directory, which may be on a file system that does not allow running programs.
tmp=$(dirname "$bin")/.regulus.rollback
rm -f "$tmp"
if ! cp "$backup" "$tmp" || ! chmod 0755 "$tmp"; then
	rm -f "$tmp"
	echo "regulus: the previous version could not be copied next to $bin" >&2
	exit 1
fi
if ! runs "$tmp"; then
	rm -f "$tmp"
	echo "regulus: the copy of the previous version cannot run either; install Regulus again with install.sh (the data is kept)" >&2
	exit 1
fi
sync
if ! mv -f "$tmp" "$bin"; then
	rm -f "$tmp"
	echo "regulus: the previous version could not be put in place" >&2
	exit 1
fi
sync
echo "regulus: the previous version is back in place" >&2
settle
