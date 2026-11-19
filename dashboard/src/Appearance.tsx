import { LANGUAGES, useLanguage, useT, type LanguageChoice, type MessageKey } from "./i18n";
import { THEMES, useTheme, type Theme } from "./theme";

// The theme and language switches, shown in the top bar and on the Account
// page.

// ThemeSwitch picks light, dark or the system's theme.
export function ThemeSwitch() {
  const t = useT();
  const { theme, setTheme } = useTheme();
  const names: Record<Theme, MessageKey> = { system: "theme.system", light: "theme.light", dark: "theme.dark" };
  return (
    <div className="seg" role="group" aria-label={t("app.theme")}>
      {THEMES.map((th) => (
        <button key={th} type="button" aria-pressed={theme === th} onClick={() => setTheme(th)}>
          {t(names[th])}
        </button>
      ))}
    </div>
  );
}

// LanguageSelect picks English, Italian or the browser's language.
export function LanguageSelect({ id }: { id?: string }) {
  const t = useT();
  const { choice, setLang } = useLanguage();
  return (
    <select id={id} aria-label={id ? undefined : t("app.language")} value={choice} onChange={(e) => setLang(e.target.value as LanguageChoice)}>
      <option value="auto">{t("app.languageBrowser")}</option>
      {LANGUAGES.map((l) => (
        <option key={l.code} value={l.code} lang={l.code}>
          {l.name}
        </option>
      ))}
    </select>
  );
}

