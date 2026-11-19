import type { plugins } from "./plugins.en";

// Italian draft, to be read by the maintainer.
export const pluginsIt: Record<keyof typeof plugins, string> = {
  "plugins.title": "Plugin",
  "plugins.loading": "Caricamento…",
  "plugins.loadError": "Impossibile caricare i plugin: {message}",

  "plugins.hub.title": "Hub",
  "plugins.hub.off": "L'hub non esegue plugin: imposta plugins_dir nella sua configurazione e riavvialo.",
  "plugins.hub.problem": "Nessun plugin è in esecuzione sull'hub: {problem}",
  "plugins.hub.none": "La directory dei plugin non contiene plugin per l'hub.",

  "plugins.agents.title": "Server",
  "plugins.agents.none": "Nessun server connesso segnala plugin.",

  "plugins.column.server": "Server",
  "plugins.column.plugin": "Plugin",
  "plugins.column.version": "Versione",
  "plugins.column.state": "Stato",
  "plugins.column.restarts": "Riavvii",
  "plugins.column.lastError": "Ultimo errore",
  "plugins.column.since": "Da",

  "plugins.state.starting": "in avvio",
  "plugins.state.running": "in esecuzione",
  "plugins.state.restarting": "in riavvio",
  "plugins.state.failed": "fallito",
  "plugins.state.incompatible": "incompatibile",
  "plugins.state.stopped": "fermato",

  "plugins.state.failedHint": "Si è fermato troppe volte e non viene riavviato finché Regulus non si riavvia.",
  "plugins.state.incompatibleHint": "Non funziona con questa versione di Regulus e non viene mai avviato.",
};
