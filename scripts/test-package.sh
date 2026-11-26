#!/bin/sh
# Installs, upgrades, removes and purges a .deb of Regulus in the machine it
# runs on, and checks what packaging/scripts/* and docs/installation.md promise
# (#400, #398, #397). It changes the system (a user, /etc/regulus, /usr/bin), so
# it refuses to run outside a container unless REGULUS_TEST_PACKAGE_HOST=1:
#
#   docker run --rm -v "$PWD":/src:ro ubuntu:24.04 sh /src/scripts/test-package.sh [PACKAGE.deb]
#
# With a path it installs that package (the one GoReleaser built, for the
# architecture of the machine) and also checks its contents. Without one it
# builds a stand-in with dpkg-deb from the same maintainer scripts, units and
# configuration, with a shell script for the binary. The older version that the
# upgrade checks need is always a stand-in. systemd does not run in a container,
# and the scripts skip it when /run/systemd/system is missing; what they do with
# systemd is for the [Test] issues (#273, #81).
set -eu

src=$(cd "$(dirname "$0")/.." && pwd)
pkg=${1:-}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fails=0

ok() { echo "ok   $1"; }
fail() {
	echo "FAIL $1" >&2
	fails=$((fails + 1))
}
equal() { # equal NAME GOT WANT
	if [ "$2" = "$3" ]; then ok "$1"; else fail "$1: got '$2', want '$3'"; fi
}
check() { # check NAME COMMAND...
	name=$1
	shift
	if "$@" >/dev/null 2>&1; then ok "$name"; else fail "$name"; fi
}
check_not() {
	name=$1
	shift
	if "$@" >/dev/null 2>&1; then fail "$name"; else ok "$name"; fi
}
mode_of() { stat -c '%a %U:%G' "$1"; }

if [ "$(id -u)" != 0 ]; then
	echo "run as root, in a container" >&2
	exit 2
fi
if [ ! -e /.dockerenv ] && [ ! -e /run/.containerenv ] && [ "${REGULUS_TEST_PACKAGE_HOST:-}" != 1 ]; then
	echo "this changes the system: run it in a container (see the top of the file)" >&2
	exit 2
fi
if ! command -v dpkg >/dev/null 2>&1; then
	echo "dpkg is needed" >&2
	exit 2
fi
export DEBIAN_FRONTEND=noninteractive
# adduser is a dependency of the package; a minimal image may not have it.
command -v adduser >/dev/null 2>&1 || {
	apt-get update -qq >/dev/null && apt-get install -y -qq adduser >/dev/null
}

# standin VERSION OUT: a package made of the repository's files.
standin() {
	root=$work/root-$1
	rm -rf "$root"
	mkdir -p "$root/DEBIAN" "$root/usr/bin" "$root/usr/lib/systemd/system" "$root/usr/lib/regulus" "$root/etc/regulus"
	printf '#!/bin/sh\necho regulus %s\n' "$1" >"$root/usr/bin/regulus"
	chmod 0755 "$root/usr/bin/regulus"
	cp "$src"/packaging/*.service "$src"/packaging/*.path "$root/usr/lib/systemd/system/"
	cp "$src/packaging/update-recover.sh" "$root/usr/lib/regulus/update-recover"
	chmod 0755 "$root/usr/lib/regulus/update-recover"
	cp "$src/packaging/config.yaml" "$root/etc/regulus/config.yaml"
	chmod 0640 "$root/etc/regulus/config.yaml"
	echo /etc/regulus/config.yaml >"$root/DEBIAN/conffiles"
	for s in preinst:preinstall postinst:postinstall prerm:preremove postrm:postremove; do
		cp "$src/packaging/scripts/${s#*:}.sh" "$root/DEBIAN/${s%%:*}"
		chmod 0755 "$root/DEBIAN/${s%%:*}"
	done
	cat >"$root/DEBIAN/control" <<EOF
Package: regulus
Version: $1
Architecture: all
Maintainer: test <test@example.invalid>
Depends: adduser
Description: stand-in for the Regulus package
EOF
	dpkg-deb --root-owner-group -b "$root" "$2" >/dev/null
}

real=false
if [ -n "$pkg" ]; then
	real=true
else
	pkg=$work/regulus_0.3.0.deb
	standin 0.3.0 "$pkg"
fi
standin 0.2.0 "$work/regulus_0.2.0.deb"
cfg=/etc/regulus

# --- contents of the package under test
if [ "$real" = true ]; then
	contents=$(dpkg-deb -c "$pkg")
	for f in usr/bin/regulus usr/lib/systemd/system/regulus.service usr/lib/systemd/system/regulus-agent.service \
		usr/lib/systemd/system/regulus-update.path usr/lib/systemd/system/regulus-update.service \
		usr/lib/systemd/system/regulus-update-recover.service usr/lib/systemd/system/regulus-plugin-apply.path usr/lib/systemd/system/regulus-plugin-apply.service usr/lib/regulus/update-recover etc/regulus/config.yaml; do
		case "$contents" in *" ./$f"*) ok "the package carries $f" ;; *) fail "the package carries $f" ;; esac
	done
	case "$(printf '%s\n' "$contents" | grep ' ./etc/regulus/config.yaml$')" in
		-rw-r-----\ root/regulus*) ok "config.yaml is 0640 root:regulus in the package" ;;
		*) fail "config.yaml is 0640 root:regulus in the package" ;;
	esac
fi

# --- fresh install
dpkg -i "$pkg" >/dev/null
check "the user regulus exists" getent passwd regulus
check "the group regulus exists" getent group regulus
equal "config.yaml mode and owner" "$(mode_of $cfg/config.yaml)" "640 root:regulus"
check "the executable is installed" test -x /usr/bin/regulus
check "the update units are installed" test -f /usr/lib/systemd/system/regulus-update.path
check "the plugin store's helper units are installed" test -f /usr/lib/systemd/system/regulus-plugin-apply.path

# --- reinstall keeps an edited configuration, and keeps the loopback default
echo '# edited by the administrator' >>$cfg/config.yaml
sed -i '/^listen:/d' $cfg/config.yaml
dpkg -i --force-confold "$pkg" >/dev/null
check "an edited config.yaml is kept" grep -q 'edited by the administrator' $cfg/config.yaml
check_not "a removed listen line stays removed on a reinstall (#397)" test -e $cfg/regulus.env

# --- an upgrade across the loopback change keeps :8080, once
dpkg -P regulus >/dev/null
dpkg -i "$work/regulus_0.2.0.deb" >/dev/null
printf 'listen: ":8080"\n' >$cfg/config.yaml
dpkg -i --force-confold "$pkg" >/dev/null
equal "an upgrade from 0.2.0 keeps the old listen address" "$(grep -c '^REGULUS_LISTEN=:8080$' $cfg/regulus.env)" 1
dpkg -i --force-confold "$pkg" >/dev/null
equal "and writes it once" "$(grep -c '^REGULUS_LISTEN=' $cfg/regulus.env)" 1
equal "regulus.env mode and owner" "$(mode_of $cfg/regulus.env)" "640 root:regulus"

# --- remove keeps data and user, leaves a marker that the next install reads
mkdir -p /var/lib/regulus
echo data >/var/lib/regulus/keep
dpkg -r regulus >/dev/null
check "remove keeps /var/lib/regulus" test -f /var/lib/regulus/keep
check "remove keeps the user" getent passwd regulus
check "remove keeps the configuration" test -f $cfg/config.yaml
check "remove leaves the marker for the next install (#398)" test -e $cfg/.removed
check_not "remove deletes the executable" test -e /usr/bin/regulus
dpkg -i --force-confold "$pkg" >/dev/null
check_not "the next install consumed the marker (#398)" test -e $cfg/.removed

# --- a group left without its user (#398): the install still creates the user
dpkg -r regulus >/dev/null
userdel regulus >/dev/null 2>&1 || true
getent group regulus >/dev/null 2>&1 || groupadd --system regulus
dpkg -i --force-confold "$pkg" >/dev/null
check "a user is created next to an existing group (#398)" getent passwd regulus

# --- purge removes what the installer wrote, keeps data and user
printf 'REGULUS_DATABASE_TYPE=postgres\n' >>$cfg/regulus.env
echo hub >$cfg/role
mkdir -p /var/lib/regulus-update
dpkg -P regulus >/dev/null
check_not "purge removes regulus.env" test -e $cfg/regulus.env
check_not "purge removes role" test -e $cfg/role
check_not "purge removes the marker" test -e $cfg/.removed
check_not "purge removes /etc/regulus" test -d $cfg
check_not "purge removes /var/lib/regulus-update" test -e /var/lib/regulus-update
check "purge keeps the data" test -f /var/lib/regulus/keep
check "purge keeps the user" getent passwd regulus

# --- the agent role: the agent directory is made
mkdir -p $cfg
echo agent >$cfg/role
dpkg -i "$pkg" >/dev/null
equal "an agent gets its own data directory" "$(mode_of /var/lib/regulus-agent)" "750 regulus:regulus"
dpkg -P regulus >/dev/null

if [ "$fails" -gt 0 ]; then
	echo "$fails check(s) failed" >&2
	exit 1
fi
echo "package tests passed"
