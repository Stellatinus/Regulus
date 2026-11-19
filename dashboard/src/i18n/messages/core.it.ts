import type { core } from "./core.en";

export const coreIt: Record<keyof typeof core, string> = {
  "app.brand": "Regulus",
  "app.languageBrowser": "Lingua del browser",
  "app.theme": "Tema",
  "theme.system": "Sistema",
  "theme.light": "Chiaro",
  "theme.dark": "Scuro",
  "app.language": "Lingua",

  "nav.main": "Principale",
  "nav.dashboard": "Dashboard",
  "nav.servers": "Server",
  "nav.users": "Utenti",
  "nav.updates": "Aggiornamenti",
  "nav.account": "Account",
  "nav.accountOf": "Account: {name} ({role})",
  "nav.sidebar": "Barra laterale",
  "nav.menu": "Menu",
  "nav.collapse": "Riduci la barra laterale",
  "nav.expand": "Espandi la barra laterale",
  "nav.sectionOverview": "Panoramica",
  "nav.sectionAdmin": "Amministrazione",
  "nav.signOut": "Esci",

  "ui.cancel": "Annulla",
  "ui.actions": "Azioni",
  "ui.actionsFor": "Azioni per {name}",
  "ui.copied": "Copiato",
  "ui.copyManual": "Qui il browser non può copiare (l'hub non è in HTTPS): seleziona il testo e premi Ctrl+C.",

  "time.day": "g",
  "time.hour": "h",
  "time.minute": "m",
  "time.second": "s",
  "time.ago": "{duration} fa",
  "time.never": "mai",

  "database.down":
    "Il database non risponde. Accesso e gran parte delle pagine non funzioneranno finché non torna disponibile. L'hub resta in funzione e si riconnette da solo.",
  "database.recovered": "Il database risponde di nuovo.",
};
