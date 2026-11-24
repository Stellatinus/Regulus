#!/bin/sh
# Runs before the files are unpacked. Called by dpkg with "install" or
# "upgrade [old version]" and by rpm with 1 for a first install and 2 for an
# upgrade. It keeps the dashboard address of an upgraded hub, then creates the
# system user and group the hub runs as. That user has no login shell, no home
# directory and no sudo rights.
set -e

# old_has_loopback_default succeeds when version $1 (1.2.3, 0.3.0-alpha.1,
# 0.3.0~alpha.1, with an optional epoch or revision) is 0.3.0 or newer, that is
# when 127.0.0.1:8080 was already its default. Only the numbers before any
# pre-release text are compared: 0.3.0-alpha.1 is the first release with it.
# An empty or unreadable version fails, so the caller acts.
old_has_loopback_default() {
	v=${1#*:} # drop a Debian epoch
	v=$(printf '%s' "$v" | sed -n 's/^\([0-9][0-9]*\)\.\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1 \2 \3/p')
	[ -n "$v" ] || return 1
	# shellcheck disable=SC2086 # three numbers
	set -- $v
	[ "$1" -gt 0 ] || [ "$2" -ge 3 ]
}

# keep_old_listen keeps an upgraded hub on the address it used before. Up to
# the version that made 127.0.0.1:8080 the default (#197), the packaged
# config.yaml said listen: ":8080", every interface. dpkg replaces a
# configuration file that was never edited, and the new built-in default
# applies when the line is missing, so without this an upgrade would quietly
# take the dashboard off the network. The old address is written to the
# environment file (it wins over config.yaml), with a note on how to drop it.
# It acts only when the package being replaced is older than 0.3.0, the first
# release with the loopback default (#397): a later upgrade or a reinstall must
# not read a deleted or commented "listen" line as the old default and reopen
# the dashboard to the network. $1 is the configuration directory, $2 the
# version being replaced (empty when it is not known: then it acts, as it did
# before this check existed).
keep_old_listen() {
	dir=$1
	if old_has_loopback_default "${2:-}"; then
		return 0
	fi
	[ -f "$dir/config.yaml" ] || return 0
	if [ -r "$dir/role" ] && [ "$(head -n 1 "$dir/role")" = agent ]; then
		return 0
	fi
	if [ -f "$dir/regulus.env" ] && grep -q '^REGULUS_LISTEN=' "$dir/regulus.env"; then
		return 0
	fi
	listen=$(sed -n "s/^listen: *[\"']\{0,1\}\([^\"' #]*\).*/\1/p" "$dir/config.yaml" | head -n 1)
	case "$listen" in
		"" | ":8080") ;;
		*) return 0 ;; # an address chosen on purpose: config.yaml was edited, so dpkg keeps it
	esac
	{
		echo "# Kept by the upgrade that made 127.0.0.1:8080 the default (#197): this hub"
		echo "# served the dashboard on every interface, over plain HTTP. To answer on this"
		echo "# machine only, delete the next line and restart the service; to keep it"
		echo "# reachable, prefer HTTPS (tls_cert and tls_key, or a reverse proxy)."
		echo "REGULUS_LISTEN=:8080"
	} >>"$dir/regulus.env"
	chmod 0640 "$dir/regulus.env"
	if getent group regulus >/dev/null 2>&1; then
		chown root:regulus "$dir/regulus.env" 2>/dev/null || true
	fi
	echo "Regulus: the dashboard keeps listening on :8080 (every interface, plain HTTP), as before this upgrade."
	echo "New installations listen on 127.0.0.1:8080 only. See REGULUS_LISTEN in $dir/regulus.env and docs/installation.md."
}

# Sourced by scripts/test-install.sh to test the function alone.
if [ "${REGULUS_PREINSTALL_SOURCED:-}" = 1 ]; then
	return 0
fi

# The version being replaced: dpkg passes it as $2; rpm has no argument for it,
# but the old package is still installed while %pre runs.
case "${1:-}" in
	upgrade | install) old_version=${2:-} ;;
	2) old_version=$(rpm -q --qf '%{VERSION}' regulus 2>/dev/null || true) ;;
	*) old_version= ;;
esac
case "${1:-}" in
	upgrade | 2) keep_old_listen /etc/regulus "$old_version" ;;
	# A package that was removed but not purged left its configuration behind.
	install) [ -z "${2:-}" ] || keep_old_listen /etc/regulus "$old_version" ;;
esac

if getent passwd regulus >/dev/null 2>&1; then
	exit 0
fi

if [ -f /etc/debian_version ] && command -v adduser >/dev/null 2>&1; then
	adduser --system --group --no-create-home --home /var/lib/regulus --gecos "Regulus hub" regulus
else
	getent group regulus >/dev/null 2>&1 || groupadd --system regulus
	useradd --system --gid regulus --no-create-home --home-dir /var/lib/regulus --shell /usr/sbin/nologin --comment "Regulus hub" regulus
fi
