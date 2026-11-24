#!/bin/sh
# On "purge" (dpkg) the settings written by the installer go away. The data in
# /var/lib/regulus and /var/lib/regulus-agent and the system user are left alone
# on purpose: they hold the accounts, the layouts and the agent's credentials,
# and deleting them is the administrator's decision. What the update helper
# keeps for itself (the previous binary and the signature trust cache in
# /var/lib/regulus-update, and the files a killed update can leave next to the
# executable) is not data: a purge removes it (#215). rpm has no purge: after
# "rpm -e" the files listed here stay, see docs/installation.md (#398).
set -e

if [ -d /run/systemd/system ]; then
	systemctl daemon-reload || true
fi

case "$1" in
	purge)
		rm -f /etc/regulus/regulus.env /etc/regulus/role /etc/regulus/agent.yaml /etc/regulus/agent.env
		rm -f /etc/regulus/update-policy.yaml /etc/regulus/.removed
		rmdir /etc/regulus 2>/dev/null || true
		rm -rf /var/lib/regulus-update
		rm -f /usr/bin/.regulus.update /usr/bin/.regulus.rollback
		echo "Regulus: the data in /var/lib/regulus and /var/lib/regulus-agent and the user 'regulus' were kept."
		echo "To delete them: rm -rf /var/lib/regulus /var/lib/regulus-agent && deluser regulus && delgroup regulus"
		;;
esac
