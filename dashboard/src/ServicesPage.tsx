import { useFeature } from "./features";
import { useT } from "./i18n";
import { PluginsPage } from "./PluginsPage";
import { useTab } from "./route";

// ServicesPage is the Services section of the dock: what runs on the mesh for
// you (plugins, and later apps) and the store to get more. Two tabs, in the
// address: #/plugins (installed) and #/plugins/store. The store is not built:
// the plugin store has its own repository and design, and apps have theirs
// (#475), so the tab says so instead of showing a catalogue that does not exist.
export function ServicesPage() {
  const t = useT();
  const tab = useTab();
  const [plugins] = useFeature("plugins");
  const [apps] = useFeature("apps");
  const store = tab === "store";
  return (
    <section aria-labelledby="services-heading">
      <div className="section-header">
        <div>
          <h2 id="services-heading">{t("services.title")}</h2>
          <p>{t("services.subtitle")}</p>
        </div>
        <nav className="admin-tabs" aria-label={t("services.tabs")}>
          <a href="#/plugins" aria-current={store ? undefined : "page"}>
            {t("services.tab.installed")}
          </a>
          <a href="#/plugins/store" aria-current={store ? "page" : undefined}>
            {t("services.tab.store")}
          </a>
        </nav>
      </div>
      {store ? (
        <div className="placeholder-pane">
          <h3>{t("services.store.title")}</h3>
          <p>{t("services.store.body")}</p>
        </div>
      ) : (
        <>
          {plugins ? (
            <PluginsPage />
          ) : (
            <div className="placeholder-pane">
              <p>{t("services.pluginsOff")}</p>
              <a href="#/settings/features">{t("services.openFeatures")}</a>
            </div>
          )}
          {apps && (
            <div className="placeholder-pane" style={{ marginTop: 16, minHeight: 0 }}>
              <h3>{t("services.apps.title")}</h3>
              <p>{t("services.apps.body")}</p>
            </div>
          )}
        </>
      )}
    </section>
  );
}
