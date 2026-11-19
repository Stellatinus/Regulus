import type { core } from "./core.en";

export const coreIt: Record<keyof typeof core, string> = {
  "app.brand": "Regulus",
  "app.language": "Lingua",
  "app.crashed.title": "Qualcosa è andato storto",
  "app.crashed.body": "La dashboard ha incontrato un errore che non è riuscita a gestire. Di solito basta ricaricare la pagina; i tuoi server non ne risentono.",
  "app.crashed.reload": "Ricarica la pagina",
  "app.crashed.details": "Dettagli tecnici",

  "nav.main": "Principale",
  "nav.dashboard": "Dashboard",
  "nav.servers": "Server",
  "nav.users": "Utenti",
  "nav.updates": "Aggiornamenti",
  "nav.signOut": "Esci",

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
