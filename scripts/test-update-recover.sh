#!/bin/sh
# Tests packaging/update-recover.sh, the script that settles an interrupted
# update when the machine starts, without touching the system: a directory
# stands for /usr/bin and one for /var/lib/regulus-update, and small shell
# scripts stand for the executable.
#
# Usage: scripts/test-update-recover.sh
set -u

here=$(cd "$(dirname "$0")" && pwd)
script=$here/../packaging/update-recover.sh
failures=0
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

pass() { printf 'ok   %s\n' "$1"; }
fail() {
	printf 'FAIL %s\n' "$1"
	failures=$((failures + 1))
}

# program FILE VERSION writes a stand-in for the executable: it answers
# "version" and records any other call in $tmp/calls.
program() {
	cat >"$1" <<PROG
#!/bin/sh
if [ "\$1" = version ]; then echo "$2"; exit 0; fi
echo "$2: \$*" >>"$tmp/calls"
PROG
	chmod 0755 "$1"
}

setup() {
	rm -rf "$tmp/bin" "$tmp/state" "$tmp/calls"
	mkdir -p "$tmp/bin" "$tmp/state"
	: >"$tmp/calls"
}

run() { sh "$script" "$tmp/state" "$tmp/bin/regulus" >"$tmp/out" 2>&1; }

# 1. No marker: nothing is done, even with a broken executable.
setup
: >"$tmp/bin/regulus"
if run && [ ! -s "$tmp/calls" ]; then pass "no marker, nothing done"; else fail "no marker, nothing done"; fi

# 2. The executable runs: the work is handed to it.
setup
echo '{}' >"$tmp/state/pending.json"
program "$tmp/bin/regulus" 0.2.0
program "$tmp/state/regulus.previous" 0.1.0
if run && grep -q "^0.2.0: update-apply --recover --before-start --state-dir $tmp/state\$" "$tmp/calls"; then
	pass "a working executable settles it itself"
else
	fail "a working executable settles it itself"
fi

# 3. The executable is empty (damaged file system): the copy is put back and
#    the work is handed to the copy.
setup
echo '{}' >"$tmp/state/pending.json"
: >"$tmp/bin/regulus"
chmod 0755 "$tmp/bin/regulus"
program "$tmp/state/regulus.previous" 0.1.0
if run && [ "$("$tmp/bin/regulus" version)" = 0.1.0 ] && grep -q "^0.1.0: update-apply --recover --before-start" "$tmp/calls" && [ ! -e "$tmp/bin/.regulus.rollback" ]; then
	pass "an empty executable is replaced by the previous version"
else
	fail "an empty executable is replaced by the previous version"
fi

# 4. The executable is missing.
setup
echo '{}' >"$tmp/state/pending.json"
program "$tmp/state/regulus.previous" 0.1.0
if run && [ "$("$tmp/bin/regulus" version)" = 0.1.0 ]; then pass "a missing executable is replaced"; else fail "a missing executable is replaced"; fi

# 5. The executable is garbage that cannot run.
setup
echo '{}' >"$tmp/state/pending.json"
printf '\177ELF garbage' >"$tmp/bin/regulus"
chmod 0755 "$tmp/bin/regulus"
program "$tmp/state/regulus.previous" 0.1.0
if run && [ "$("$tmp/bin/regulus" version)" = 0.1.0 ]; then pass "a damaged executable is replaced"; else fail "a damaged executable is replaced"; fi

# 6. No copy: it fails, says how to get out, and leaves the executable alone.
setup
echo '{}' >"$tmp/state/pending.json"
: >"$tmp/bin/regulus"
if ! run && grep -q "install.sh" "$tmp/out" && [ ! -s "$tmp/bin/regulus" ]; then pass "without a copy it fails and says so"; else fail "without a copy it fails and says so"; fi

# 7. The copy cannot run either: the executable is not replaced by it.
setup
echo '{}' >"$tmp/state/pending.json"
echo original >"$tmp/bin/regulus"
printf '\177ELF garbage' >"$tmp/state/regulus.previous"
if ! run && [ "$(cat "$tmp/bin/regulus")" = original ] && [ ! -e "$tmp/bin/.regulus.rollback" ]; then
	pass "a copy that cannot run is not installed"
else
	fail "a copy that cannot run is not installed"
fi

# 8. The copy is a symbolic link: never followed.
setup
echo '{}' >"$tmp/state/pending.json"
: >"$tmp/bin/regulus"
program "$tmp/elsewhere" 6.6.6
ln -s "$tmp/elsewhere" "$tmp/state/regulus.previous"
if ! run && [ ! -s "$tmp/bin/regulus" ]; then pass "a link in place of the copy is refused"; else fail "a link in place of the copy is refused"; fi

if [ "$failures" -gt 0 ]; then
	printf '%s test(s) failed\n' "$failures"
	exit 1
fi
echo "all update-recover tests passed"
