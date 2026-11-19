// Texts of the plugins view (GET /api/plugins, #262). Keys start with
// "plugins.". Add the Italian text in plugins.it.ts.
export const plugins = {
  "plugins.title": "Plugins",
  "plugins.loading": "Loading…",
  "plugins.loadError": "Could not load the plugins: {message}",

  "plugins.hub.title": "Hub",
  "plugins.hub.off": "The hub runs no plugins: set plugins_dir in its configuration and restart it.",
  "plugins.hub.problem": "No plugin runs on the hub: {problem}",
  "plugins.hub.none": "The plugins directory holds no plugin for the hub.",

  "plugins.agents.title": "Servers",
  "plugins.agents.none": "No connected server reports plugins.",

  "plugins.column.server": "Server",
  "plugins.column.plugin": "Plugin",
  "plugins.column.version": "Version",
  "plugins.column.state": "State",
  "plugins.column.restarts": "Restarts",
  "plugins.column.lastError": "Last error",
  "plugins.column.since": "Since",

  "plugins.state.starting": "starting",
  "plugins.state.running": "running",
  "plugins.state.restarting": "restarting",
  "plugins.state.failed": "failed",
  "plugins.state.incompatible": "incompatible",
  "plugins.state.stopped": "stopped",

  "plugins.state.failedHint": "It stopped too often and is not started again until Regulus restarts.",
  "plugins.state.incompatibleHint": "It does not work with this version of Regulus and is never started.",
} as const;
