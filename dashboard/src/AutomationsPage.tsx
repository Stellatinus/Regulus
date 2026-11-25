import { useT } from "./i18n";

// AutomationsPage holds the place of the Automations section. Automations will
// be a plugin; until it exists the page says so. The dock shows it only while
// the setting is on (SettingsPage).
export function AutomationsPage() {
  const t = useT();
  return (
    <section aria-labelledby="automations-heading">
      <h2 id="automations-heading">{t("settings.automationsTitle")}</h2>
      <div className="placeholder-pane">
        <p>{t("settings.automationsBody")}</p>
      </div>
    </section>
  );
}
