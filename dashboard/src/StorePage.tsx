import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError,
  can,
  fetchStore,
  installStorePlugin,
  type StoreCatalog,
  type StorePlugin,
  type StoreSource,
  type StoreVersion,
} from "./api";
import { formatAgo } from "./format";
import { useT, type MessageKey } from "./i18n";
import { useSession } from "./session";
import { errorMessage } from "./useApiList";

// The permission that installs plugins. The store backend (internal/store in
// Regulus) defines it; this is the one place the dashboard names it.
export const STORE_INSTALL_PERMISSION = "plugins.manage";

type Filter = "all" | "installed" | "updates";
type State =
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "ready"; data: StoreCatalog };

const KINDS = ["manager", "integration", "feature"] as const;
const kindOf = (p: StorePlugin) => (p.kind && (KINDS as readonly string[]).includes(p.kind) ? p.kind : "other");

// The versions a hub may install: they run here and are not yanked.
const installable = (p: StorePlugin) => p.versions.filter((v) => v.compatible && !v.yanked);

// The version offered: the newest the index calls latest if it is installable,
// else the first installable one (the hub lists versions newest first).
export function offered(p: StorePlugin): StoreVersion | undefined {
  const ok = installable(p);
  return ok.find((v) => v.version === p.latest) ?? ok[0];
}

export const hasUpdate = (p: StorePlugin) => {
  const v = offered(p);
  return !!p.installed && !!v && v.version !== p.installed.version;
};

const sourceClass = { ok: "connected", error: "offline", expired: "stale" } as const;

// StorePage is the Regulus Store: the plugins the hub's sources offer, read
// from GET /api/store. It shows only what the hub returns. Installing asks the
// hub, which says today that installation is not built; that answer is shown as
// it is, never as a success.
export function StorePage() {
  const t = useT();
  const session = useSession();
  const expired = session?.expired;
  const canInstall = can(session?.user, STORE_INSTALL_PERMISSION);
  const [state, setState] = useState<State>({ kind: "loading" });
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [kind, setKind] = useState("all");
  const [open, setOpen] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    fetchStore(controller.signal).then(
      (data) => setState({ kind: "ready", data }),
      (err: unknown) => {
        if (controller.signal.aborted) return;
        if (err instanceof ApiError && err.status === 401) return expired?.();
        setState({ kind: "error", message: errorMessage(err) });
      },
    );
    return () => controller.abort();
  }, [expired]);

  const plugins = useMemo(() => (state.kind === "ready" ? state.data.plugins : []), [state]);
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return plugins.filter(
      (p) =>
        (q === "" || `${p.name} ${p.id} ${p.description ?? ""}`.toLowerCase().includes(q)) &&
        (kind === "all" || kindOf(p) === kind) &&
        (filter === "all" || (filter === "installed" ? !!p.installed : hasUpdate(p))),
    );
  }, [plugins, query, filter, kind]);

  if (state.kind === "loading") return <p role="status">{t("store.loading")}</p>;
  if (state.kind === "error")
    return (
      <p role="alert" className="error">
        {t("store.error", { message: state.message })}
      </p>
    );

  const { sources } = state.data;
  const detail = plugins.find((p) => p.id === open);
  const filterLabel: Record<Filter, MessageKey> = {
    all: "store.filter.all",
    installed: "store.filter.installed",
    updates: "store.filter.updates",
  };

  return (
    <div className="store">
      <SourceList sources={sources} />
      {!canInstall && <p className="store-note">{t("store.noPermission", { permission: STORE_INSTALL_PERMISSION })}</p>}
      {sources.length === 0 ? (
        <Empty title={t("store.empty.noSources.title")} body={t("store.empty.noSources.body")} />
      ) : plugins.length === 0 ? (
        <Empty title={t("store.empty.noPlugins.title")} body={t("store.empty.noPlugins.body")} />
      ) : (
        <>
          <div className="store-toolbar">
            <input
              type="search"
              className="search-input store-search"
              aria-label={t("store.search")}
              placeholder={t("store.search")}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            <div className="store-cat-pills" role="group" aria-label={t("store.filters")}>
              {(Object.keys(filterLabel) as Filter[]).map((f) => (
                <button key={f} type="button" aria-pressed={filter === f} onClick={() => setFilter(f)}>
                  {t(filterLabel[f])}
                </button>
              ))}
            </div>
            <select aria-label={t("store.kind.all")} value={kind} onChange={(e) => setKind(e.target.value)}>
              <option value="all">{t("store.kind.all")}</option>
              {[...KINDS, "other"].map((k) => (
                <option key={k} value={k}>
                  {t(`store.kind.${k}` as MessageKey)}
                </option>
              ))}
            </select>
          </div>
          {shown.length === 0 ? (
            <p className="store-note">{t("store.empty.noMatch")}</p>
          ) : (
            <ul className="catalog-grid">
              {shown.map((p) => (
                <PluginCard key={p.id} plugin={p} onOpen={() => setOpen(p.id)} />
              ))}
            </ul>
          )}
        </>
      )}
      {detail && <Detail plugin={detail} canInstall={canInstall} onClose={() => setOpen(null)} />}
    </div>
  );
}

function Empty({ title, body }: { title: string; body: string }) {
  return (
    <div className="placeholder-pane">
      <h3>{title}</h3>
      <p>{body}</p>
    </div>
  );
}

function SourceList({ sources }: { sources: StoreSource[] }) {
  const t = useT();
  if (sources.length === 0) return null;
  return (
    <section className="store-sources" aria-label={t("store.sources")}>
      <ul>
        {sources.map((s) => (
          <li key={s.name} className={`store-source ${s.state}`}>
            <span className={`status-pill ${sourceClass[s.state] ?? "offline"}`}>
              {t(`store.source.${s.state}` as MessageKey)}
            </span>{" "}
            <strong>{s.name}</strong> ({t(`store.source.trust.${s.trust === "official" ? "official" : "custom"}` as MessageKey)})
            {s.state === "ok" && s.issued_at && <> · {t("store.source.issued", { when: formatAgo(s.issued_at) })}</>}
            {s.state !== "ok" && (
              <p role="alert">
                {s.error && <>{s.error} </>}
                {t(s.state === "expired" ? "store.source.expiredHint" : "store.source.errorHint")}
              </p>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Channel({ channel }: { channel: string }) {
  const t = useT();
  const known = channel === "stable" || channel === "beta" || channel === "alpha";
  return <span className={`badge-role store-channel ${channel}`}>{known ? t(`store.channel.${channel}` as MessageKey) : channel}</span>;
}

function PluginCard({ plugin: p, onOpen }: { plugin: StorePlugin; onOpen: () => void }) {
  const t = useT();
  const v = offered(p);
  return (
    <li className="catalog-card">
      <div className="catalog-head">
        <h3>{p.name}</h3>
        {v && <Channel channel={v.channel} />}
      </div>
      {p.description && <p>{p.description}</p>}
      <p className="store-meta">
        {v ? t("store.card.version", { version: v.version }) : t("store.card.noVersion")} · {t("store.card.from", { source: p.source })}
        {p.audience === "admin" && <> · {t("store.card.admin")}</>}
      </p>
      <div className="store-actions">
        <span>
          {p.installed ? t("store.card.installed", { version: p.installed.version }) : t("store.card.notInstalled")}
          {hasUpdate(p) && v && <> · {t("store.card.update", { version: v.version })}</>}
        </span>
        <button type="button" onClick={onOpen} aria-label={`${t("store.card.details")}: ${p.name}`}>
          {t("store.card.details")}
        </button>
      </div>
    </li>
  );
}

function Detail({ plugin: p, canInstall, onClose }: { plugin: StorePlugin; canInstall: boolean; onClose: () => void }) {
  const t = useT();
  const ref = useRef<HTMLDialogElement>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const v = offered(p);
  useEffect(() => {
    const d = ref.current;
    if (d && typeof d.showModal === "function" && !d.open) d.showModal();
    else if (d && !d.open) d.setAttribute("open", "");
  }, []);

  const install = useCallback(async () => {
    if (!v) return;
    setBusy(true);
    setFailure(null);
    try {
      await installStorePlugin(p.id, v.version);
    } catch (err) {
      setFailure(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }, [p.id, v]);

  const needs = (v?.needs ?? []).map((n) => (typeof n === "string" ? { capability: n, optional: false } : { capability: n.capability, optional: !!n.optional }));
  return (
    <dialog ref={ref} className="edit-dialog store-detail" aria-labelledby="store-detail-title" onClose={onClose} onCancel={onClose}>
      <h3 id="store-detail-title">{p.name}</h3>
      {p.description && <p>{p.description}</p>}
      <p className="store-meta">
        {t("store.card.from", { source: p.source })} · <code>{p.id}</code>
        {p.homepage && (
          <>
            {" · "}
            <a href={p.homepage} target="_blank" rel="noreferrer noopener">
              {t("store.detail.homepage")}
            </a>
          </>
        )}
      </p>

      <h4>{t("store.detail.permissions")}</h4>
      <p className="store-note">{t("store.detail.permissionsNote")}</p>
      {v?.permissions && v.permissions.length > 0 ? (
        <ul>
          {v.permissions.map((perm) => (
            <li key={perm.id}>
              <code>{perm.id}</code>: {perm.description}
            </li>
          ))}
        </ul>
      ) : (
        <p>{t("store.detail.noPermissions")}</p>
      )}

      <h4>{t("store.detail.needs")}</h4>
      <p>
        {needs.length === 0
          ? t("store.detail.none")
          : needs.map((n) => (n.optional ? t("store.detail.needsOptional", { capability: n.capability }) : n.capability)).join(", ")}
      </p>
      <h4>{t("store.detail.provides")}</h4>
      <p>{v?.provides && v.provides.length > 0 ? v.provides.join(", ") : t("store.detail.none")}</p>

      <h4>{t("store.detail.versions")}</h4>
      <ul className="store-versions">
        {p.versions.map((ver) => (
          <li key={ver.version} className={ver.compatible && !ver.yanked ? undefined : "incompatible"} aria-disabled={!ver.compatible || ver.yanked}>
            <strong>{ver.version}</strong> <Channel channel={ver.channel} />
            {ver.yanked && <> · {t("store.detail.yanked")}</>}
            {!ver.compatible && (
              <>
                {" · "}
                {ver.incompatible_reason ? t("store.detail.incompatible", { reason: ver.incompatible_reason }) : t("store.detail.incompatibleNoReason")}
              </>
            )}
          </li>
        ))}
      </ul>

      {failure && (
        <p role="alert" className="error">
          {t("store.installFailed", { message: failure })}
        </p>
      )}
      <div className="store-actions">
        <button type="button" onClick={onClose}>
          {t("store.detail.close")}
        </button>
        <button type="button" className="primary" disabled={!canInstall || !v || busy} aria-busy={busy} onClick={() => void install()}>
          {busy ? t("store.installing") : v ? t("store.installVersion", { version: v.version }) : t("store.install")}
        </button>
      </div>
    </dialog>
  );
}
