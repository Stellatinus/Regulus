import type { services } from "./services.en";

// Italian draft, to be read by the maintainer.
export const servicesIt: Record<keyof typeof services, string> = {
  "services.title": "Servizi e plugin",
  "services.subtitle": "I plugin attivi nel tuo gruppo Regulus, e lo store per averne altri.",
  "services.tabs": "Servizi",
  "services.tab.installed": "Installati",
  "services.tab.store": "Regulus Store",
  "services.store.title": "Lo store non è ancora disponibile",
  "services.store.body": "I plugin arriveranno dal loro store (il repository regulus-plugins), e le app e le automazioni da store propri (#475). Finché non esistono, i plugin si aggiungono a mano alla directory dei plugin di un hub o di un server.",
  "services.pluginsOff": "I plugin sono disattivati nelle Impostazioni.",
  "services.openFeatures": "Apri le impostazioni",
  "services.apps.title": "App",
  "services.apps.body": "Nessuna app installata. Le app non sono ancora disponibili (#475).",
};
