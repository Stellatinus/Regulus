// Texts of the Updates page (UpdatesPage).
// Keys start with "updates.". Add the Italian text in updates.it.ts.
export const updates = {
  "updates.title": "Updates",
  "updates.loading": "Loading…",
  "updates.unknown": "unknown",

  "updates.channel.stable": "stable",
  "updates.channel.beta": "beta",
  "updates.channel.alpha": "alpha",

  "updates.version.title": "Version",
  "updates.version.runs": "This hub runs",
  "updates.version.platform": "({platform}).",
  "updates.version.devBuild":
    "This is a development build. It does not compare itself with releases, so it is never offered an update.",
  "updates.version.checksOffBefore":
    "Update checks are turned off in the configuration (",
  "updates.version.checksOffAfter":
    "), so the hub does not contact any server.",
  "updates.version.availableLabel": "Update available",
  "updates.version.availableVersion": "Version {version}",
  "updates.version.isAvailable": "is available.",
  "updates.version.releasePage": "Release page",
  "updates.version.upToDate":
    "Regulus is up to date: the newest {channel} release is {version}.",
  "updates.version.noRelease": "The release server lists no {channel} release.",
  "updates.version.checked": "Checked {ago}.",
  "updates.version.notChecked": "Not checked yet.",
  "updates.version.checkFailed": "The last check failed: {error}",
  "updates.version.stale":
    "This hub has not been able to ask for new releases since {when}. It may not know about an update, a security fix included. Check that it can reach api.github.com.",
  "updates.version.staleNever":
    "This hub has never been able to ask for new releases. It may not know about an update, a security fix included. Check that it can reach api.github.com.",
  "updates.version.lastSuccess": "Last successful check: {ago}.",

  "updates.staged.label": "Downloaded update",
  "updates.staged.version": "Version",
  "updates.staged.downloaded": "is downloaded ({size}, {archive}).",
  "updates.staged.signature":
    "Its signature was checked: it was made by this project's release workflow for this version, and the file matches it.",
  "updates.staged.checksum": "Its SHA-256 matches the release's checksum list.",
  "updates.staged.itIs": "It is",
  "updates.staged.notInstalled": "not installed",
  "updates.staged.applyOn": ".",
  "updates.staged.applyOff":
    ": this hub was not set up to install updates itself (updates.apply, and the regulus-update.path unit of the installer).",
  "updates.staged.checksumNote":
    "The checksum list comes from the same place as the file, so it catches a damaged download but does not prove who published it.",

  "updates.downloading": "Downloading…",
  "updates.applying.head": "Installing {what}…",
  "updates.applying.theUpdate": "the update",
  "updates.applying.rest":
    "The hub restarts in a moment and this page may stop answering for a short while; it checks again by itself. If the new version does not start properly, the previous one is put back.",
  "updates.stalled":
    "Nothing has picked up the request for a few minutes. The unit that installs updates is probably not running:",
  "updates.noStatusSocket":
    "This hub has no local status socket, so `regulus status` shows no live data, and an update from a release that does not yet write running.json (made before the fix of #312) rolls back on it. Updates from this version on work. Reason:",
  "updates.trustFallback":
    "The hub could not fetch the current Sigstore trusted root, so it checks releases with the one built into this version. That one does not know about signing keys Sigstore has revoked since. It tries again by itself. Reason:",
  "updates.last.applied": "Updated to {version} {ago}.",
  "updates.last.appliedFrom": "Updated to {version} from {from} {ago}.",
  "updates.last.rolledBack":
    "Version {version} did not start properly, so the previous version was put back.",
  "updates.last.notInstalled": "Version {version} was not installed.",
  "updates.downloadFailed": "The download failed: {error}",

  "updates.action.checkNow": "Check now",
  "updates.action.checking": "Checking…",
  "updates.action.download": "Download {version}",
  "updates.action.install": "Install {version}",
  "updates.action.installNow": "Install now",
  "updates.action.cancel": "Cancel",
  "updates.action.save": "Save",
  "updates.action.stop": "Stop",
  "updates.confirm.label": "Confirm installation",
  "updates.confirm.text":
    "The hub is checked and replaced by version {version} and restarts. Agents reconnect by themselves. If the new version does not start, the previous one is put back.",

  "updates.settings.title": "Settings",
  "updates.settings.when": "When a new version is out",
  "updates.mode.manual": "Only tell me",
  "updates.mode.download": "Download and check it, then wait for me",
  "updates.mode.automatic":
    "Download it and install it by itself, inside a maintenance window",
  "updates.settings.applyOff":
    "Installing is not set up on this hub (updates.apply and the regulus-update.path unit, which the installer enables).",
  "updates.settings.windowStarts": "Window starts",
  "updates.settings.windowEnds": "Window ends",
  "updates.settings.windowNote":
    "Times are those of the hub's server. The hub restarts during the update, and a version that had to be rolled back is not tried again by itself.",
  "updates.settings.channel": "Channel",
  "updates.settings.channelStable": "Stable",
  "updates.settings.channelBeta": "Beta (releases and betas)",
  "updates.settings.channelAlpha": "Alpha (every release, alphas included)",

  "updates.rollout.title": "Servers",
  "updates.rollout.intro":
    "The servers this hub manages are updated to the version the hub runs (",
  "updates.rollout.introAfter":
    "). They fetch it from the hub, so they need no internet access, and a server whose new version does not reconnect goes back to the previous one by itself.",
  "updates.rollout.running":
    "Updating the servers, {size} at a time. It stops at the first failure.",
  "updates.rollout.progress": "Update progress",
  "updates.rollout.allCurrent": "All servers are up to date ({target}).",
  "updates.rollout.exceptions": "Except these, which cannot be updated from here:",
  "updates.rollout.exceptionsLabel": "Servers that cannot be updated from here",
  "updates.rollout.behindOne": "1 server runs an older version.",
  "updates.rollout.behindMany": "{count} servers run an older version.",
  "updates.rollout.behindLabel": "Servers behind",
  "updates.rollout.runs": "{name} runs {version}",
  "updates.rollout.atATime": "At a time",
  "updates.rollout.all": "All",
  "updates.rollout.updateOne": "Update this server to {target}",
  "updates.rollout.updateMany": "Update {count} servers to {target}",
  "updates.rollout.lastDone": "Last update of the servers: done.",
  "updates.rollout.lastDoneAgo": "Last update of the servers: done, {ago}.",
  "updates.rollout.lastStopped": "Last update of the servers: stopped.",
  "updates.rollout.lastStoppedAgo":
    "Last update of the servers: stopped, {ago}.",
  "updates.rollout.lastFailed":
    "Last update of the servers: stopped by a failure.",
  "updates.rollout.lastFailedAgo":
    "Last update of the servers: stopped by a failure, {ago}.",
  "updates.rollout.lastLabel": "Last update",

  "updates.agent.pending": "waiting",
  "updates.agent.offered": "fetching the update",
  "updates.agent.installing": "installing, waiting for it to come back",
  "updates.agent.updated": "updated",
  "updates.agent.failed": "failed",
  "updates.agent.skipped": "not tried",
  "updates.agent.unknown": "result unknown",
  "updates.agent.row": "{name} ({from}): {status}",
  "updates.agent.rowMessage": "{name} ({from}): {status} — {message}",

  // #368: what the installer on a server wrote (internal/update/applycode.go).
  "updates.applyMsg.request_unreadable": "The update request could not be read (it was not a request, was too large or was not a regular file). It was removed.",
  "updates.applyMsg.invalid_version": "The request names an invalid version: {error}",
  "updates.applyMsg.not_newer": "Version {version} is not newer than the installed {installed}. Updates never go backwards.",
  "updates.applyMsg.rolled_back": "{error}. Version {version} was put back.{note}",
  "updates.applyMsg.rollback_failed": "{error}. Putting {version} back failed: {rollback_error}",
  "updates.applyMsg.interrupted_stands": "The installer was interrupted. The new version was checked afterwards and stands.",
  "updates.applyMsg.interrupted_not_installed": "The installer was interrupted before the new version was installed. {version} is still in place.",
  "updates.applyMsg.interrupted_rolled_back": "The installer was interrupted. Version {version} was put back.{note}",
  "updates.applyMsg.interrupted_rollback_fail": "The installer was interrupted and putting {version} back failed: {error}",

  // #372: the steps that fail with the text of a system error ({error} is that text, in English).
  "updates.applyMsg.version_unreadable": "The installed version could not be read: {error}",
  "updates.applyMsg.work_dir_failed": "The installer could not make its working directory: {error}",
  "updates.applyMsg.fetch_failed": "The downloaded release could not be used: {error}",
  "updates.applyMsg.extract_failed": "The program could not be taken out of the archive: {error}",
  "updates.applyMsg.new_binary_mismatch": "The new program does not report version {version} (it reported: {got}).",
  "updates.applyMsg.backup_failed": "The current program could not be kept for a rollback: {error}",
  "updates.applyMsg.database_copy_failed": "A copy of the database could not be kept for a rollback: {error}. Nothing was changed.",
  "updates.applyMsg.pending_write_failed": "The update in progress could not be recorded: {error}",
  "updates.applyMsg.swap_failed": "The new program could not be put in place: {error}",
  "updates.applyMsg.marker_unreadable": "The note of an interrupted update could not be read. It was removed.",
  "updates.applyMsg.marker_outside": "The note of an interrupted update names a file outside the state directory. It was removed.",
  "updates.applyMsg.boot_not_confirmed": "Version {version} was not confirmed after the machine started {count} times with it. Version {from} was put back.{note}",
  "updates.applyMsg.boot_not_confirmed_bad": "Version {version} was not confirmed after the machine started {count} times with it, and putting {from} back failed: {error}",
  "updates.applyMsg.boot_unusable": "The update was interrupted and the program left in place cannot be used. Version {from} was put back.{note}",
  "updates.applyMsg.boot_unusable_bad": "The update was interrupted and the program left in place cannot be used, and putting {from} back failed: {error}",
  "updates.applyMsg.boot_database_bad": "The update was interrupted. {from} is in place, but the database could not be restored: {error}",
  "updates.applyMsg.boot_database_back": "The update was interrupted. Version {from} is in place.{note}",
  "updates.applyMsg.policy_channel": "Version {version} is on the {channel} channel, and this server's update policy allows at most {max}.",
  "updates.applyMsg.policy_major": "Version {version} is a new major version, and this server's update policy allows at most {max}.",
  "updates.applyMsg.policy_unreadable": "The local update policy cannot be used, so nothing is installed: {error}",

  // #368: the message of a server's step in an update of the servers
  // (internal/hub/rollout.go).
  "updates.rolloutMsg.not_connected": "The server is not connected.",
  "updates.rolloutMsg.no_archive": "The hub has no archive for this server: {error}",
  "updates.rolloutMsg.stopped": "Stopped.",
  "updates.rolloutMsg.agent_too_old": "This agent is too old to update itself: update it once with install.sh.",
  "updates.rolloutMsg.result_unknown": "The update of the servers was stopped before this server's result was known.",
  "updates.rolloutMsg.rolled_back": "The server connected with version {version}, then came back with version {back}: the installer rolled the update back.",
  "updates.rolloutMsg.did_not_take": "The server came back with version {back} and not {version} within {wait}: the update did not take or was rolled back.",
  "updates.rolloutMsg.did_not_stay": "The server connected with version {version} but did not stay connected.",
  "updates.rolloutMsg.did_not_return": "The server did not connect again within {wait}.",
} as const;
