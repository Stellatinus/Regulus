import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import {
  ApiError,
  apiErrorText,
  can,
  fetchStore,
  fetchStoreProgress,
  installStorePlugin,
  notifyPluginsChanged,
  removeStorePlugin,
  updateStorePlugin,
  type StoreCatalog,
  type StoreProgress,
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

// The "Downloaded" switch (#534) is remembered in this browser. Off shows the
// whole catalogue, on only what is downloaded on the hub, which is what is
// installed: the hub's API knows no plugin that is downloaded and not installed.
const DOWNLOADED_KEY = "regulus.store.downloaded";
const DOWNLOADED_CHANGED = "regulus:store-downloaded";

function readDownloaded(): boolean {
  try {
    return window.localStorage.getItem(DOWNLOADED_KEY) === "on";
  } catch {
    return false; // no storage
  }
}

function useDownloaded(): [boolean, (on: boolean) => void] {
  const on = useSyncExternalStore(
    (cb) => {
      window.addEventListener(DOWNLOADED_CHANGED, cb);
      window.addEventListener("storage", cb);
      return () => {
        window.removeEventListener(DOWNLOADED_CHANGED, cb);
        window.removeEventListener("storage", cb);
      };
    },
    readDownloaded,
    () => false,
  );
  const set = useCallback((next: boolean) => {
    try {
      window.localStorage.setItem(DOWNLOADED_KEY, next ? "on" : "off");
    } catch {
      // not saved
    }
    window.dispatchEvent(new Event(DOWNLOADED_CHANGED));
  }, []);
  return [on, set];
}

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

// What a version needs from the hub, as a list (a plain string is a required
// capability).
export const needsOf = (v?: StoreVersion) =>
  (v?.needs ?? []).map((n) => (typeof n === "string" ? { capability: n, optional: false } : { capability: n.capability, optional: !!n.optional }));

// A version that asks for permissions or needs other capabilities is not
// installed from a card without showing them first (#532): the card's Install
// never skips what the description page shows.
export const needsApproval = (v?: StoreVersion) => !!v && ((v.permissions?.length ?? 0) > 0 || needsOf(v).length > 0);

type Action = "install" | "update" | "remove";
type Working = { id: string; name: string; action: Action };
type Notice = { kind: "done" | "error"; text: string };

const STORE_CODES = new Set([
  "plugin_not_found",
  "not_installed",
  "source_unknown",
  "already_installed",
  "up_to_date",
  "source_ambiguous",
  "no_source",
  "plugins_off",
  "busy",
  "no_compatible_version",
  "version_not_offered",
  "source_unavailable",
  "download_failed",
  "helper_timeout",
]);
const HELPER_CODES = new Set([
  "not_verified",
  "checksum",
  "incompatible",
  "yanked",
  "unsafe_package",
  "bad_manifest",
  "already_installed",
  "not_installed",
  "source_mismatch",
  "hand_installed",
  "plugins_dir",
  "internal",
]);

// storeErrorText is the text of a refused install, update or removal in the
// language of the page. The store's codes have texts of their own ("busy" and
// "plugin_not_found" mean other things elsewhere), so they are not in the hub's
// general list; anything else (a lost session, no permission, the network)
// falls back to the usual text of the error.
export function storeErrorText(t: ReturnType<typeof useT>, err: unknown): string {
  if (err instanceof ApiError && err.code) {
    const text = storeCodeText(t, err.code, String(err.args?.helper_code ?? ""));
    if (text) return text;
  }
  return apiErrorText(t, err);
}

// storeCodeText is the text of a store code (and of the helper's own code), or
// "" when the code is not one of the store's. A failed operation that another
// administrator started (GET /api/store/progress) carries the same codes.
function storeCodeText(t: ReturnType<typeof useT>, code: string, helper: string): string {
  if (code === "helper_failed")
    return HELPER_CODES.has(helper) ? t(`store.error.helper.${helper}` as MessageKey) : t("store.error.helper_failed", { code: helper || "?" });
  return STORE_CODES.has(code) ? t(`store.error.${code}` as MessageKey) : "";
}

// How often the page asks the hub where an install, update or removal is: fast
// while one is running (here or started elsewhere), slowly otherwise, so a
// reload, or another administrator's operation, shows up.
export const PROGRESS_POLL_MS = 1000;
export const PROGRESS_IDLE_POLL_MS = 5000;

const STEPS = ["downloading", "verifying", "installing", "removing", "testing", "rolling_back"];

// stepText is the text of the current step, in the language of the page.
function stepText(t: ReturnType<typeof useT>, p: StoreProgress | null): string {
  if (p?.state === "failed")
    return t("store.step.failed", { reason: storeCodeText(t, p.code ?? "", p.helper_code ?? "") || p.message || t("store.step.unknown") });
  if (!p || !p.step || !STEPS.includes(p.step)) return t("store.step.starting");
  if (p.step === "downloading" && p.percent >= 0) return t("store.step.downloadingPercent", { percent: p.percent });
  return t(`store.step.${p.step}` as MessageKey);
}

const sourceClass = { ok: "connected", error: "offline", expired: "stale" } as const;

// StorePage is the Regulus Store: the plugins the hub's sources offer, read
// from GET /api/store. It shows only what the hub returns. Install, update and
// remove ask the hub, which runs them through its root helper; what it answers
// is shown as it is, a refusal never as a success.
export function StorePage() {
  const t = useT();
  const session = useSession();
  const expired = session?.expired;
  const canInstall = can(session?.user, STORE_INSTALL_PERMISSION);
  const [state, setState] = useState<State>({ kind: "loading" });
  const [query, setQuery] = useState("");
  const [downloaded, setDownloaded] = useDownloaded();
  const [kind, setKind] = useState("all");
  const [open, setOpen] = useState<string | null>(null);
  // An install, update or removal blocks for up to two minutes. While one runs,
  // every action is off; the state lives here, not in the detail dialog, so
  // closing the dialog does not forget it.
  const [working, setWorking] = useState<Working | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);
  const [reload, setReload] = useState(0);
  // What the hub says the running (or last) operation is doing (#530); it is
  // what survives a reload and what another administrator started.
  const [remote, setRemote] = useState<StoreProgress | null>(null);
  // The confirmation a card's Install, Update or Remove asks first (#532).
  const [confirm, setConfirm] = useState<{ plugin: StorePlugin; action: Action } | null>(null);

  const run = useCallback(
    async (p: StorePlugin, action: Action, version?: string) => {
      setWorking({ id: p.id, name: p.name, action });
      setNotice(null);
      setRemote(null);
      try {
        const r =
          action === "install"
            ? await installStorePlugin(p.id, { version, source: p.source })
            : action === "update"
              ? await updateStorePlugin(p.id, version)
              : await removeStorePlugin(p.id);
        setNotice({ kind: "done", text: t(`store.done.${action}` as MessageKey, { name: p.name, version: r.version ?? version ?? "" }) });
        // What shows plugins (the node window's tabs) reads them again, and so does the catalog.
        notifyPluginsChanged();
        setReload((n) => n + 1);
      } catch (err) {
        const failed: Record<Action, MessageKey> = { install: "store.installFailed", update: "store.updateFailed", remove: "store.removeFailed" };
        setNotice({ kind: "error", text: t(failed[action], { message: storeErrorText(t, err) }) });
      } finally {
        setWorking(null);
        setRemote(null);
      }
    },
    [t],
  );

  // Ask the hub where the operation is: quickly while one runs, slowly
  // otherwise (a reload, or another administrator's operation, shows up).
  const polling = working !== null || remote?.state === "running";
  const wasRunning = useRef(false);
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      try {
        const r = await fetchStoreProgress();
        const p = r && typeof r === "object" && r.progress ? r.progress : null;
        if (!stopped) setRemote(p);
      } catch {
        // The page keeps what it knew: a refusal or a lost network is not progress.
      }
      if (!stopped) timer = setTimeout(() => void tick(), polling ? PROGRESS_POLL_MS : PROGRESS_IDLE_POLL_MS);
    };
    void tick();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [polling]);
  // An operation that someone else started has ended: read the catalog and the
  // plugin tabs again, as after our own.
  useEffect(() => {
    const running = remote?.state === "running";
    if (wasRunning.current && !running && working === null) {
      notifyPluginsChanged();
      setReload((n) => n + 1);
    }
    wasRunning.current = running;
  }, [remote, working]);

  // A card's button: remove always asks; install and update ask when the
  // version has permissions or needs to show, and go straight on otherwise.
  const act = (p: StorePlugin, action: Action) => {
    const v = offered(p);
    if (action === "remove" || needsApproval(v)) setConfirm({ plugin: p, action });
    else void run(p, action, v?.version);
  };

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
  }, [expired, reload]);

  const plugins = useMemo(() => (state.kind === "ready" ? state.data.plugins : []), [state]);
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    // The Updates view is gone (#534): a plugin with an update stays reachable
    // on its card, and the updatable ones come first.
    return plugins
      .filter(
        (p) =>
          (q === "" || `${p.name} ${p.id} ${p.description ?? ""}`.toLowerCase().includes(q)) &&
          (kind === "all" || kindOf(p) === kind) &&
          (!downloaded || !!p.installed),
      )
      .sort((a, b) => Number(hasUpdate(b)) - Number(hasUpdate(a)));
  }, [plugins, query, downloaded, kind]);
  const updates = useMemo(() => plugins.filter(hasUpdate).length, [plugins]);

  // What is busy: our own action, or an operation the hub says is running
  // (started in another tab or by another administrator, or before a reload).
  const running = remote?.state === "running" ? remote : null;
  const current: Working | null =
    working ?? (running ? { id: running.plugin, name: plugins.find((p) => p.id === running.plugin)?.name ?? running.plugin, action: running.action } : null);
  const shownProgress = current ? (running && running.plugin === current.id ? running : null) : remote?.state === "failed" ? remote : null;

  if (state.kind === "loading") return <p role="status">{t("store.loading")}</p>;
  if (state.kind === "error")
    return (
      <p role="alert" className="error">
        {t("store.error", { message: state.message })}
      </p>
    );

  const { sources } = state.data;
  const detail = plugins.find((p) => p.id === open);

  return (
    <div className="store">
      {!detail && <ActionStatus working={current} notice={notice} progress={shownProgress} />}
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
            <label className="store-switch" htmlFor="store-downloaded">
              <input id="store-downloaded" type="checkbox" role="switch" className="switch" checked={downloaded} onChange={(e) => setDownloaded(e.target.checked)} />
              {t("store.downloaded")}
              {updates > 0 && <span className="badge-role store-updates-count">{t("store.updatesAvailable", { count: updates })}</span>}
            </label>
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
            <p className="store-note">
              {downloaded && query.trim() === "" && kind === "all" ? t("store.empty.nothingDownloaded") : t("store.empty.noMatch")}
            </p>
          ) : (
            <ul className="catalog-grid">
              {shown.map((p) => (
                <PluginCard key={p.id} plugin={p} canInstall={canInstall} working={current} onOpen={() => setOpen(p.id)} onAct={act} />
              ))}
            </ul>
          )}
        </>
      )}
      {confirm && (
        <ActionConfirm
          plugin={confirm.plugin}
          action={confirm.action}
          onCancel={() => setConfirm(null)}
          onConfirm={() => {
            const { plugin, action } = confirm;
            setConfirm(null);
            void run(plugin, action, action === "remove" ? undefined : offered(plugin)?.version);
          }}
        />
      )}
      {detail && (
        <Detail plugin={detail} canInstall={canInstall} working={current} notice={notice} progress={shownProgress} onRun={run} onClose={() => setOpen(null)} />
      )}
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

// ActionStatus says what the running action is, with a progress bar and the
// current step (#530), or how the last one ended.
function ActionStatus({ working, notice, progress }: { working: Working | null; notice: Notice | null; progress: StoreProgress | null }) {
  const t = useT();
  if (working)
    return (
      <div>
        <p role="status" aria-busy="true" className="store-note">
          {t(`store.working.${working.action}` as MessageKey, { name: working.name })}
        </p>
        <StepBar progress={progress} label={working.name} />
      </div>
    );
  // Another administrator's operation failed and nothing of ours says otherwise.
  if (!notice && progress?.state === "failed")
    return (
      <div role="alert" className="error">
        <StepBar progress={progress} label={progress.plugin} />
      </div>
    );
  if (!notice) return null;
  return notice.kind === "error" ? (
    <p role="alert" className="error">
      {notice.text}
    </p>
  ) : (
    <p role="status" className="store-note">
      {notice.text}
    </p>
  );
}

// StepBar is the progress bar and the text of the step. The download has a
// real percentage; the other steps, an indeterminate bar with their name. The
// step is in a polite live region, so it is announced as it changes.
function StepBar({ progress, label }: { progress: StoreProgress | null; label: string }) {
  const t = useT();
  const text = stepText(t, progress);
  const failed = progress?.state === "failed";
  const known = !!progress && !failed && progress.percent >= 0;
  return (
    <div className="store-progress">
      <div
        role="progressbar"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={known ? progress.percent : undefined}
        aria-valuetext={text}
        className={`store-progress-track${failed ? " failed" : known ? "" : " indeterminate"}`}
      >
        <div className="store-progress-fill" style={failed ? { width: "100%" } : known ? { width: `${progress.percent}%` } : undefined} />
      </div>
      <p className="store-note" aria-live="polite">
        {text}
      </p>
    </div>
  );
}

function Channel({ channel }: { channel: string }) {
  const t = useT();
  const known = channel === "stable" || channel === "beta" || channel === "alpha";
  return <span className={`badge-role store-channel ${channel}`}>{known ? t(`store.channel.${channel}` as MessageKey) : channel}</span>;
}

function PluginCard({
  plugin: p,
  canInstall,
  working,
  onOpen,
  onAct,
}: {
  plugin: StorePlugin;
  canInstall: boolean;
  working: Working | null;
  onOpen: () => void;
  onAct: (p: StorePlugin, action: Action) => void;
}) {
  const t = useT();
  const v = offered(p);
  const busy = working !== null;
  const mine = working?.id === p.id ? working.action : null;
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
        {/* [Install] [i]: the action first, the description page last (#532). An installed plugin shows Update, or Remove, in the same place. */}
        <div className="store-card-buttons">
          {!p.installed ? (
            <button type="button" className="primary" disabled={!canInstall || !v || busy} aria-busy={mine === "install"} aria-label={`${t("store.install")}: ${p.name}`} onClick={() => onAct(p, "install")}>
              {mine === "install" ? t("store.installing") : t("store.install")}
            </button>
          ) : hasUpdate(p) && v ? (
            <button type="button" className="primary" disabled={!canInstall || busy} aria-busy={mine === "update"} aria-label={`${t("store.update")}: ${p.name}`} onClick={() => onAct(p, "update")}>
              {mine === "update" ? t("store.updating") : t("store.update")}
            </button>
          ) : (
            <button type="button" disabled={!canInstall || busy} aria-busy={mine === "remove"} aria-label={`${t("store.remove")}: ${p.name}`} onClick={() => onAct(p, "remove")}>
              {mine === "remove" ? t("store.removing") : t("store.remove")}
            </button>
          )}
          <button type="button" className="store-info" onClick={onOpen} aria-label={t("store.card.info", { name: p.name })} title={t("store.card.info", { name: p.name })}>
            <span aria-hidden="true">i</span>
          </button>
        </div>
      </div>
    </li>
  );
}

// ActionConfirm is what a card's button asks before it acts: the permissions
// and the needs of the version about to be installed, or the removal. It
// shows what the description page shows, so the card is never a way around it.
function ActionConfirm({ plugin: p, action, onCancel, onConfirm }: { plugin: StorePlugin; action: Action; onCancel: () => void; onConfirm: () => void }) {
  const t = useT();
  const ref = useRef<HTMLDialogElement>(null);
  const v = offered(p);
  useEffect(() => {
    const d = ref.current;
    if (d && typeof d.showModal === "function" && !d.open) d.showModal();
    else if (d && !d.open) d.setAttribute("open", "");
  }, []);
  const needs = needsOf(v);
  const remove = action === "remove";
  return (
    <dialog ref={ref} className="edit-dialog store-detail" role="alertdialog" aria-labelledby="store-action-title" onClose={onCancel} onCancel={onCancel}>
      {remove ? (
        <>
          <h3 id="store-action-title">{t("store.confirmRemove.title", { name: p.name })}</h3>
          <p>{t("store.confirmRemove.body", { name: p.name })}</p>
        </>
      ) : (
        <>
          <h3 id="store-action-title">{t(action === "update" ? "store.confirmUpdate.title" : "store.confirmInstall.title", { name: p.name, version: v?.version ?? "" })}</h3>
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
          <p>{needs.length === 0 ? t("store.detail.none") : needs.map((n) => (n.optional ? t("store.detail.needsOptional", { capability: n.capability }) : n.capability)).join(", ")}</p>
        </>
      )}
      <div className="store-actions">
        <button type="button" onClick={onCancel}>
          {t("store.confirmRemove.no")}
        </button>
        <button type="button" className={remove ? "danger" : "primary"} onClick={onConfirm}>
          {remove ? t("store.confirmRemove.yes", { name: p.name }) : t(action === "update" ? "store.confirmUpdate.yes" : "store.confirmInstall.yes", { name: p.name })}
        </button>
      </div>
    </dialog>
  );
}

function Detail({
  plugin: p,
  canInstall,
  working,
  notice,
  progress,
  onRun,
  onClose,
}: {
  plugin: StorePlugin;
  canInstall: boolean;
  working: Working | null;
  notice: Notice | null;
  progress: StoreProgress | null;
  onRun: (p: StorePlugin, action: Action, version?: string) => Promise<void>;
  onClose: () => void;
}) {
  const t = useT();
  const ref = useRef<HTMLDialogElement>(null);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const v = offered(p);
  const busy = working !== null;
  useEffect(() => {
    const d = ref.current;
    if (d && typeof d.showModal === "function" && !d.open) d.showModal();
    else if (d && !d.open) d.setAttribute("open", "");
  }, []);

  const needs = needsOf(v);
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

      <ActionStatus working={working} notice={notice} progress={progress} />
      {confirmRemove && p.installed && (
        <div role="alertdialog" aria-labelledby="store-remove-title" className="store-confirm">
          <h4 id="store-remove-title">{t("store.confirmRemove.title", { name: p.name })}</h4>
          <p>{t("store.confirmRemove.body", { name: p.name })}</p>
          <div className="store-actions">
            <button type="button" onClick={() => setConfirmRemove(false)} disabled={busy}>
              {t("store.confirmRemove.no")}
            </button>
            <button
              type="button"
              className="danger"
              disabled={busy}
              onClick={() => {
                setConfirmRemove(false);
                void onRun(p, "remove");
              }}
            >
              {t("store.confirmRemove.yes", { name: p.name })}
            </button>
          </div>
        </div>
      )}
      <div className="store-actions">
        <button type="button" onClick={onClose}>
          {t("store.detail.close")}
        </button>
        {p.installed && (
          <button type="button" disabled={!canInstall || busy} onClick={() => setConfirmRemove(true)}>
            {working?.action === "remove" && working.id === p.id ? t("store.removing") : t("store.remove")}
          </button>
        )}
        {p.installed ? (
          hasUpdate(p) && v ? (
            <button type="button" className="primary" disabled={!canInstall || busy} aria-busy={busy} onClick={() => void onRun(p, "update", v.version)}>
              {working?.action === "update" && working.id === p.id ? t("store.updating") : t("store.updateTo", { version: v.version })}
            </button>
          ) : null
        ) : (
          <button type="button" className="primary" disabled={!canInstall || !v || busy} aria-busy={busy} onClick={() => v && void onRun(p, "install", v.version)}>
            {working?.action === "install" && working.id === p.id ? t("store.installing") : v ? t("store.installVersion", { version: v.version }) : t("store.install")}
          </button>
        )}
      </div>
    </dialog>
  );
}
