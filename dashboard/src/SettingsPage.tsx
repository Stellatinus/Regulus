import { LanguageSelect, ThemeSwitch } from "./Appearance";
import { useFeature } from "./features";
import { useT } from "./i18n";

// SettingsPage is behind the cog of the notch. Today it holds the appearance
// (theme and language) and the features that can be switched on or off.
export function SettingsPage() {
  const t = useT();
  const [automations, setAutomations] = useFeature("automations");
  return (
    <section aria-labelledby="settings-heading">
      <h2 id="settings-heading" className="page-title">
        {t("settings.title")}
      </h2>
      <div className="settings-section">
        <h3>{t("settings.appearance")}</h3>
        <div className="settings-row">
          <span>{t("settings.theme")}</span>
          <ThemeSwitch />
        </div>
        <div className="settings-row">
          <label htmlFor="settings-language">{t("settings.language")}</label>
          <div style={{ minWidth: 180 }}>
            <LanguageSelect id="settings-language" />
          </div>
        </div>
      </div>
      <div className="settings-section">
        <h3>{t("settings.features")}</h3>
        <div className="settings-row">
          <div>
            <label htmlFor="settings-automations">{t("settings.automationsLabel")}</label>
            <p>{t("settings.automationsHint")}</p>
          </div>
          <input id="settings-automations" type="checkbox" role="switch" className="switch" checked={automations} onChange={(e) => setAutomations(e.target.checked)} />
        </div>
      </div>
    </section>
  );
}
