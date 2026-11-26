// Package hostsync makes a machine match what this version of Regulus needs
// outside its executable: systemd units, directories and settings.
//
// An update from the dashboard replaces only /usr/bin/regulus. Whatever a
// newer version adds around it (a unit, a directory, a setting) is put there
// by the package or by install.sh, which such an installation never runs
// again, so Sync does it, as root, after every update. Every step checks
// first and changes nothing that is already right, and never overrides a
// value an administrator chose.
//
// It does not run the update, restart the hub for any other reason than a
// setting it just added, download anything, or decide what a version needs
// beyond the list in Sync. Adding to that list is part of any change that adds
// something outside the executable (AGENTS.md).
package hostsync
