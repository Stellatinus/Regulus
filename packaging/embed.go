// Package packaging holds the files that the packages and the installer put on
// a machine. The ones `regulus host-sync` may have to put there later are
// embedded, so an installation that was updated from the dashboard (which
// replaces only the executable) gets exactly the files a package would have
// given it. It does not install anything by itself.
package packaging

import "embed"

// Units are the systemd units of the plugin store's root helper.
//
//go:embed regulus-plugin-apply.path regulus-plugin-apply.service
var Units embed.FS
