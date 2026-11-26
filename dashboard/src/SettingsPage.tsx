import type { ReactNode } from "react";
import { AccountSecurity } from "./AccountPage";
import { LanguageSelect, ThemeSwitch } from "./Appearance";
import { can } from "./api";
import { FEATURES, useFeature, type Feature } from "./features";
import { useT, type MessageKey } from "./i18n";
import { useTab } from "./route";
import { useSession } from "./session";
import { UpdatesPage } from "./UpdatesPage";

// SettingsPage is behind "Settings" in the account menu: a list of categories
// on the left and the chosen one on the right (design system v3). The address
// keeps the category: #/settings/<category>.
//
// Personal: account and security (password, two-factor, sessions), appearance
// and language. System: the features (Plugins, Apps, Automations), and what
// the hub reads from its configuration file today (network, database,
// metrics), which the dashboard cannot edit yet (#483); the mesh updates are
// the Updates page.

type Category = "account" | "appearance" | "features" | "network" | "database" | "metrics" | "updates";

interface Item {
  id: Category;
  label: MessageKey;
  group: "personal" | "system";
  icon: ReactNode;
  // A category with a permission shows only to those who hold it.
  permission?: string;
}

const svg = (d: ReactNode) => (
  <svg width="16" height="16" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.65" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
    {d}
  </svg>
);

const ITEMS: Item[] = [
  { id: "account", label: "settings.cat.account", group: "personal", icon: svg(<><circle cx="10" cy="7" r="3.2" /><path d="M4.2 16.5c1-2.8 3.2-4.2 5.8-4.2s4.8 1.4 5.8 4.2" /></>) },
  { id: "appearance", label: "settings.cat.appearance", group: "personal", icon: svg(<><circle cx="10" cy="10" r="6.5" /><path d="M10 3.5v13M3.5 10h13" strokeOpacity="0.55" /></>) },
  { id: "features", label: "settings.cat.features", group: "system", icon: svg(<path d="M10 3L16.5 6.75V13.25L10 17L3.5 13.25V6.75L10 3ZM3.5 6.75L10 10.5L16.5 6.75M10 10.5V17" />) },
  { id: "network", label: "settings.cat.network", group: "system", icon: svg(<><rect x="3" y="4" width="14" height="12" rx="2" /><path d="M6.5 8.5L9 10.5 6.5 12.5M10.5 12.5h3" /></>) },
  { id: "database", label: "settings.cat.database", group: "system", icon: svg(<><ellipse cx="10" cy="5.5" rx="6" ry="2.4" /><path d="M4 5.5v4.5c0 1.3 2.7 2.4 6 2.4s6-1.1 6-2.4V5.5M4 10v4.5c0 1.3 2.7 2.4 6 2.4s6-1.1 6-2.4V10" /></>) },
  { id: "metrics", label: "settings.cat.metrics", group: "system", icon: svg(<path d="M3 13l3.5-4 3 2.5L14 6l3 2" />) },
  { id: "updates", label: "settings.cat.updates", group: "system", permission: "core.update", icon: svg(<path d="M15.5 10a5.5 5.5 0 1 1-1.6-3.9M15.5 4.5v3h-3" />) },
];

const isCategory = (v: string): v is Category => ITEMS.some((i) => i.id === v);

export function SettingsPage({ category }: { category?: Category }) {
  const t = useT();
  const session = useSession();
  const tab = useTab();
  const visible = ITEMS.filter((i) => !i.permission || can(session?.user, i.permission));
  const wanted = category ?? (isCategory(tab) ? tab : "account");
  const current = visible.find((i) => i.id === wanted) ?? visible[0];
  const groups: { id: Item["group"]; label: MessageKey }[] = [
    { id: "personal", label: "settings.group.personal" },
    { id: "system", label: "settings.group.system" },
  ];

  return (
    <section aria-labelledby="settings-heading">
      <div className="section-header">
        <div>
          <h2 id="settings-heading">{t("settings.title")}</h2>
          <p>{t("settings.subtitle")}</p>
        </div>
      </div>
      <div className="settings-shell">
        <nav className="settings-sidebar" aria-label={t("settings.categories")}>
          {groups.map((g) => (
            <div key={g.id}>
              <div className="settings-sidebar-group">{t(g.label)}</div>
              {visible
                .filter((i) => i.group === g.id)
                .map((i) => (
                  <a key={i.id} className="settings-nav-btn" href={`#/settings/${i.id}`} aria-current={current.id === i.id ? "page" : undefined}>
                    {i.icon}
                    <span>{t(i.label)}</span>
                  </a>
                ))}
            </div>
          ))}
        </nav>
        <div className="settings-content">
          {current.id === "account" && <AccountSecurity />}
          {current.id === "appearance" && <AppearanceTab />}
          {current.id === "features" && <FeaturesTab />}
          {current.id === "network" && (
            <ConfigTab
              title="settings.network.title"
              desc="settings.network.desc"
              keys={[
                ["listen", "settings.key.listen"],
                ["tls_cert, tls_key", "settings.key.tls"],
                ["trusted_proxies", "settings.key.proxies"],
                ["builtin_agent", "settings.key.builtinAgent"],
                ["data_dir", "settings.key.dataDir"],
              ]}
            />
          )}
          {current.id === "database" && (
            <ConfigTab title="settings.database.title" desc="settings.database.desc" keys={[["database.*", "settings.key.database"]]} />
          )}
          {current.id === "metrics" && (
            <ConfigTab
              title="settings.metrics.title"
              desc="settings.metrics.desc"
              keys={[
                ["metrics_interval", "settings.key.interval"],
                ["metrics_budget", "settings.key.budget"],
                ["metrics_retention_1m, metrics_retention_15m", "settings.key.retention"],
                ["prometheus.enabled, prometheus.token", "settings.key.prometheus"],
              ]}
            />
          )}
          {current.id === "updates" && <UpdatesPage />}
        </div>
      </div>
    </section>
  );
}

function AppearanceTab() {
  const t = useT();
  return (
    <>
      <div className="settings-card">
        <div className="settings-card-title">{t("settings.theme")}</div>
        <p className="settings-card-desc">{t("settings.theme.desc")}</p>
        <ThemeSwitch />
      </div>
      <div className="settings-card">
        <label htmlFor="settings-language" className="settings-card-title" style={{ color: "var(--ink)", margin: 0 }}>
          {t("settings.language")}
        </label>
        <p className="settings-card-desc">{t("settings.language.desc")}</p>
        <div style={{ maxWidth: "16rem" }}>
          <LanguageSelect id="settings-language" />
        </div>
        <p className="hint">{t("settings.language.hint")}</p>
      </div>
    </>
  );
}

const FEATURE_TEXT: Record<Feature, { label: MessageKey; hint: MessageKey }> = {
  plugins: { label: "settings.feature.plugins", hint: "settings.feature.pluginsHint" },
  apps: { label: "settings.feature.apps", hint: "settings.feature.appsHint" },
  automations: { label: "settings.feature.automations", hint: "settings.feature.automationsHint" },
};

function FeatureRow({ feature }: { feature: Feature }) {
  const t = useT();
  const [on, set] = useFeature(feature);
  const id = `feature-${feature}`;
  return (
    <div className="settings-row">
      <div>
        <label htmlFor={id}>{t(FEATURE_TEXT[feature].label)}</label>
        <p>{t(FEATURE_TEXT[feature].hint)}</p>
      </div>
      <input id={id} type="checkbox" role="switch" className="switch" checked={on} onChange={(e) => set(e.target.checked)} />
    </div>
  );
}

function FeaturesTab() {
  const t = useT();
  return (
    <div className="settings-card">
      <div className="settings-card-title">{t("settings.features")}</div>
      <p className="settings-card-desc">{t("settings.features.desc")}</p>
      {FEATURES.map((f) => (
        <FeatureRow key={f} feature={f} />
      ))}
    </div>
  );
}

// ConfigTab says what the hub reads from its configuration file for this
// category. Editing it from the dashboard is not built (#483).
function ConfigTab({ title, desc, keys }: { title: MessageKey; desc: MessageKey; keys: [string, MessageKey][] }) {
  const t = useT();
  return (
    <div className="settings-card">
      <div className="settings-card-title">{t(title)}</div>
      <p className="settings-card-desc">{t(desc)}</p>
      <dl className="facts">
        {keys.map(([k, text]) => (
          <div key={k}>
            <dt className="mono">{k}</dt>
            <dd style={{ fontFamily: "var(--f-body)", textAlign: "right" }}>{t(text)}</dd>
          </div>
        ))}
      </dl>
      <p className="hint">{t("settings.config.notEditable")}</p>
    </div>
  );
}
