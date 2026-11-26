import { useCallback, useEffect, useRef, useState } from "react";
import {
  displayName,
  lastContact,
  roleLabel,
  statusLabel,
  versionNote,
} from "./AgentList";
import {
  ApiError,
  can,
  fetchAgent,
  fetchPluginUIs,
  PLUGINS_CHANGED_EVENT,
  renameAgent,
  revokeAgent,
  type Agent,
  type PluginUI,
} from "./api";
import { serverIdFromHash } from "./route";
import { useSession } from "./session";
import { errorMessage } from "./useApiList";
import { ConfirmButton, PanelBoundary } from "./ui";
import { daysLeft } from "./CertificateBanner";
import { useLanguage, useT, type MessageKey } from "./i18n";
import { PluginFrame } from "./PluginFrame";
import { registry } from "./widgets";
import { widgetTitle } from "./widgets/registry";
import { metricsSummary } from "./limits";
import { DisksPanel } from "./DisksPanel";
import { NodeIcon, NODE_TYPES, nodeTypeDef, naturalType, savedNodeType, setNodeType, useNodeTypes, type NodeTypeId } from "./nodeTypes";

// The widgets of the page of a server, in order.
const WIDGETS = ["system", "cpu", "memory", "network", "disk"];

const REFRESH_MS = 5000;

type State =
  | { kind: "loading" }
  | { kind: "missing" }
  | { kind: "error"; message: string }
  | { kind: "ready"; agent: Agent };

// ServerPage is the window of one server, over the Nodes page: a header with
// the node's name, type, state and actions, a three-way switch (General, SSH
// terminal, File editor) and the part chosen. It loads the server itself and
// reads it again every few seconds.
export function ServerPage({
  id = serverIdFromHash(window.location.hash),
}: {
  id?: string;
}) {
  const t = useT();
  const session = useSession();
  const expired = session?.expired;
  const [state, setState] = useState<State>({ kind: "loading" });
  const [round, setRound] = useState(0);
  const reload = useCallback(() => setRound((n) => n + 1), []);

  useEffect(() => {
    const controller = new AbortController();
    const load = () =>
      fetchAgent(id, controller.signal).then(
        (agent) => setState({ kind: "ready", agent }),
        (err: unknown) => {
          if (controller.signal.aborted) return;
          if (err instanceof ApiError && err.status === 401) return expired?.();
          if (err instanceof ApiError && err.status === 404)
            return setState({ kind: "missing" });
          setState({ kind: "error", message: errorMessage(err) });
        },
      );
    void load();
    const timer = setInterval(() => void load(), REFRESH_MS);
    return () => {
      controller.abort();
      clearInterval(timer);
    };
  }, [id, round, expired]);

  const close = useCallback(() => {
    window.location.hash = "#/servers";
  }, []);
  const windowRef = useRef<HTMLDivElement>(null);

  // The window takes the keyboard: Escape closes it, the page behind does not
  // scroll, and focus goes back to where it was when it closes.
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    windowRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !document.querySelector(".edit-backdrop")) close();
    };
    window.addEventListener("keydown", onKey);
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = overflow;
      previous?.focus?.();
    };
  }, [close]);

  return (
    <div
      className="node-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) close();
      }}
    >
      <div ref={windowRef} className="node-window" role="dialog" aria-modal="true" aria-labelledby="server-heading" tabIndex={-1}>
        {state.kind === "ready" ? (
          <NodeWindow
            agent={state.agent}
            canManage={can(session?.user, "servers.manage")}
            canSeeDisks={can(session?.user, "inventory.read")}
            onChanged={reload}
            onClose={close}
          />
        ) : (
          <>
            <div className="node-head">
              <h2 id="server-heading" className="node-name">
                {state.kind === "missing" ? t("servers.server.notFound") : t("nodes.window.loading")}
              </h2>
              <button type="button" className="node-close" aria-label={t("nodes.window.close")} title={t("nodes.window.close")} onClick={close}>
                <CloseIcon />
              </button>
            </div>
            <div className="node-body single">
              {state.kind === "loading" && <p>{t("servers.loading")}</p>}
              {state.kind === "error" && <p role="alert">{t("servers.server.loadError", { message: state.message })}</p>}
              {state.kind === "missing" && <p>{t("servers.server.notFoundBody")}</p>}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function CloseIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true" focusable="false">
      <path strokeLinecap="round" d="M5 5l10 10M15 5L5 15" />
    </svg>
  );
}

// A tab is one of the node window's own parts, or a plugin's ("plugin:<id>", #21).
type Part = "general" | "terminal" | "files" | `plugin:${string}`;

// The plugins that add a tab to the node window. A failed or odd answer is no
// plugin: the window must work without them.
function usePluginTabs(): PluginUI[] {
  const [plugins, setPlugins] = useState<PluginUI[]>([]);
  // The store sends PLUGINS_CHANGED_EVENT after an install, update or removal:
  // the tabs follow without a reload.
  const [version, setVersion] = useState(0);
  useEffect(() => {
    const again = () => setVersion((n) => n + 1);
    window.addEventListener(PLUGINS_CHANGED_EVENT, again);
    return () => window.removeEventListener(PLUGINS_CHANGED_EVENT, again);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    fetchPluginUIs(controller.signal).then(
      (list) => setPlugins(Array.isArray(list) ? list.filter((p) => p.node_tab) : []),
      () => undefined,
    );
    return () => controller.abort();
  }, [version]);
  return plugins;
}

// The Disks plugin replaces the core's Physical disks panel (#497): with it
// installed the panel is not drawn, so the same disks are not shown twice.
const hasDisksPlugin = (plugins: PluginUI[]) => plugins.some((p) => p.id === "disks" && p.node_tab?.id === "disks");

function NodeWindow({
  agent,
  canManage,
  canSeeDisks,
  onChanged,
  onClose,
}: {
  agent: Agent;
  canManage: boolean;
  canSeeDisks: boolean;
  onChanged: () => void;
  onClose: () => void;
}) {
  const t = useT();
  const typeOf = useNodeTypes();
  const type = typeOf(agent);
  const { lang } = useLanguage();
  const pluginTabs = usePluginTabs();
  const [part, setPart] = useState<Part>("general");
  const [editing, setEditing] = useState(false);
  const parts: { id: Part; label?: MessageKey; text?: string; icon: React.ReactNode }[] = [
    { id: "general", label: "nodes.part.general", icon: <path d="M3 3h6v8H3zM11 3h6v5h-6zM11 10h6v7h-6zM3 13h6v4H3z" /> },
    ...pluginTabs.map((p) => ({
      id: `plugin:${p.id}` as Part,
      text: (lang === "it" ? p.node_tab?.title.it : p.node_tab?.title.en) ?? p.id,
      icon: <path strokeLinejoin="round" d="M4 7.5 10 4l6 3.5v5L10 16l-6-3.5zM4 7.5l6 3.5 6-3.5M10 11v5" />,
    })),
    { id: "terminal", label: "nodes.part.terminal", icon: <><rect x="3" y="4" width="14" height="12" rx="2" /><path strokeLinecap="round" strokeLinejoin="round" d="M6.5 8.5L9 10.5 6.5 12.5M10.5 12.5h3" /></> },
    { id: "files", label: "nodes.part.files", icon: <path strokeLinejoin="round" d="M3 5.5A1.5 1.5 0 0 1 4.5 4h3.2l1.6 1.8h6.2A1.5 1.5 0 0 1 17 7.3v7.2a1.5 1.5 0 0 1-1.5 1.5h-11A1.5 1.5 0 0 1 3 14.5z" /> },
  ];
  return (
    <>
      <div className="node-head">
        <div className="node-head-left">
          <div className={`node-logo role-icon-box role-${type}`}>
            <NodeIcon type={type} size={28} />
          </div>
          <div style={{ minWidth: 0 }}>
            <div className="node-title-row">
              <h2 id="server-heading" className="node-name">
                {displayName(agent)}
              </h2>
              <span className={`role-pill role-${type}`}>{t(nodeTypeDef(type).label)}</span>
              <span className={`status-pill ${agent.status}`}>
                <span className={`status-dot ${agent.status}`} aria-hidden="true" />
                {statusLabel(agent, t)}
              </span>
            </div>
            <div className="node-sub-row">
              {agent.hostname && (
                <>
                  <span className="mono">{agent.hostname}</span>
                  <span aria-hidden="true">·</span>
                </>
              )}
              <span>
                {t("servers.fact.lastContact")}: <strong style={{ color: "var(--ink)", fontWeight: 500 }}>{lastContact(agent, t)}</strong>
              </span>
            </div>
          </div>
        </div>
        <div className="node-head-right">
          <div className="node-switch" role="radiogroup" aria-label={t("nodes.part.label")}>
            {parts.map((p) => (
              <button key={p.id} type="button" role="radio" aria-checked={part === p.id} onClick={() => setPart(p.id)}>
                <svg width="14" height="14" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true" focusable="false">
                  {p.icon}
                </svg>
                <span>{p.text ?? (p.label ? t(p.label) : "")}</span>
              </button>
            ))}
          </div>
          <button type="button" className="node-edit" aria-label={t("nodes.edit.open")} title={t("nodes.edit.open")} onClick={() => setEditing(true)}>
            <svg width="16" height="16" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
              <path d="M12.5 4.5l3 3M4 16l.8-3.2L13.6 4a1.4 1.4 0 0 1 2 0l.4.4a1.4 1.4 0 0 1 0 2L7.2 15.2 4 16z" />
            </svg>
          </button>
          <button type="button" className="node-close" aria-label={t("nodes.window.close")} title={t("nodes.window.close")} onClick={onClose}>
            <CloseIcon />
          </button>
        </div>
      </div>
      {part === "general" && (
        <General agent={agent} canManage={canManage} canSeeDisks={canSeeDisks && !hasDisksPlugin(pluginTabs)} onChanged={onChanged} />
      )}
      {pluginTabs.map((p) => part === `plugin:${p.id}` && <PluginFrame key={p.id} plugin={p} node={agent.id} />)}
      {part === "terminal" && (
        <div className="node-body single">
          <div className="placeholder-pane">
            <h3>{t("nodes.terminal.title")}</h3>
            <p>{t("nodes.terminal.body")}</p>
          </div>
        </div>
      )}
      {part === "files" && (
        <div className="node-body single">
          <div className="placeholder-pane">
            <h3>{t("nodes.files.title")}</h3>
            <p>{t("nodes.files.body")}</p>
          </div>
        </div>
      )}
      {editing && <EditNode agent={agent} canManage={canManage} onClose={() => setEditing(false)} onSaved={onChanged} />}
    </>
  );
}

// EditNode changes how a node is shown: its name (kept on the hub, for those
// who may manage servers) and its type (a look kept in this browser until the
// hub knows node types, #164). The name of the machine itself does not change.
function EditNode({ agent, canManage, onClose, onSaved }: { agent: Agent; canManage: boolean; onClose: () => void; onSaved: () => void }) {
  const t = useT();
  const natural = naturalType(agent);
  const fixed = natural === "hub" || natural === "hub-backup";
  const nameEditable = canManage && agent.id !== "local";
  const [name, setName] = useState(agent.name ?? "");
  const [type, setType] = useState<NodeTypeId | "auto">(savedNodeType(agent.id) ?? "auto");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      if (nameEditable && name.trim() !== (agent.name ?? "")) await renameAgent(agent.id, name.trim());
      if (!fixed) setNodeType(agent.id, type);
      onSaved();
      onClose();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="node-backdrop edit-backdrop"
      style={{ zIndex: 90 }}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape") onClose();
      }}
    >
      <form className="edit-dialog" role="dialog" aria-modal="true" aria-labelledby="edit-node-heading" onSubmit={(e) => void save(e)}>
        <h3 id="edit-node-heading">{t("nodes.edit.title", { name: displayName(agent) })}</h3>
        <label>
          {t("servers.server.nameLabel")}
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={64} placeholder={agent.hostname || agent.id} disabled={!nameEditable} autoFocus />
        </label>
        <p className="muted" style={{ marginTop: -6 }}>
          {nameEditable ? t("nodes.edit.nameHint") : t("nodes.edit.nameLocked")}
        </p>
        <span className="ws-section-title">{t("nodes.edit.type")}</span>
        {fixed ? (
          <p className="muted">{t("nodes.edit.typeFixed")}</p>
        ) : (
          <>
            <div className="type-grid" role="group" aria-label={t("nodes.edit.type")}>
              <button type="button" className="type-option" aria-pressed={type === "auto"} onClick={() => setType("auto")}>
                <span className={`role-icon-box role-${natural}`}>
                  <NodeIcon type={natural} size={20} />
                </span>
                <span>{t("nodes.edit.typeAuto")}</span>
              </button>
              {NODE_TYPES.filter((n) => n.id !== "hub" && n.id !== "hub-backup").map((n) => (
                <button key={n.id} type="button" className="type-option" aria-pressed={type === n.id} onClick={() => setType(n.id)}>
                  <span className={`role-icon-box role-${n.id}`}>
                    <NodeIcon type={n.id} size={20} />
                  </span>
                  <span>{t(n.label)}</span>
                </button>
              ))}
            </div>
            <p className="muted">{t("nodes.edit.typeLocal")}</p>
          </>
        )}
        {error && <p role="alert">{error}</p>}
        <div className="dialog-actions">
          <button type="button" onClick={onClose}>
            {t("ui.cancel")}
          </button>
          <button type="submit" disabled={busy}>
            {t("nodes.edit.save")}
          </button>
        </div>
      </form>
    </div>
  );
}

// General is the main part of the window: what the server is doing now (the
// widgets bound to it), and what it is.
function General({
  agent,
  canManage,
  canSeeDisks,
  onChanged,
}: {
  agent: Agent;
  canManage: boolean;
  canSeeDisks: boolean;
  onChanged: () => void;
}) {
  const t = useT();
  // An agent connected only to be updated sends no metrics: the widgets would
  // stay empty, so the page says why instead.
  const online =
    (agent.status === "connected" || agent.status === "stale") &&
    !agent.update_only;
  return (
    <div className="node-body">
      <div className="ws-column">
        <ConnectionNotice agent={agent} />
        {online && (
          <div>
            <span className="ws-section-title">{t("nodes.window.live")}</span>
            <div className="node-widgets">
              {WIDGETS.map((w) => {
                const def = registry.get(w);
                if (!def) return null;
                const Widget = def.component;
                return (
                  <section key={w} className="widget" aria-label={widgetTitle(def, t)} style={w === "system" ? { gridColumn: "1 / -1" } : undefined}>
                    <header>
                      <h3 className="widget-title">{widgetTitle(def, t)}</h3>
                    </header>
                    <div className="widget-body">
                      <Widget instanceId={`server-${w}`} config={{ agentId: agent.id, fixed: true }} setConfig={() => undefined} />
                    </div>
                  </section>
                );
              })}
            </div>
          </div>
        )}
      </div>
      <div className="ws-column">
        <div>
          <span className="ws-section-title">{t("nodes.window.identity")}</span>
          <Facts agent={agent} online={online} />
        </div>
        {canSeeDisks && (
          <PanelBoundary name="disks">
            <DisksPanel serverId={agent.id} />
          </PanelBoundary>
        )}
        {canManage && agent.id !== "local" && <Manage agent={agent} onChanged={onChanged} />}
      </div>
    </div>
  );
}

function Facts({ agent, online }: { agent: Agent; online: boolean }) {
  const t = useT();
  return (
    <dl className="facts server-facts">
      <div>
        <dt>{t("servers.fact.id")}</dt>
        <dd>{agent.id}</dd>
      </div>
      <div>
        <dt>{t("servers.fact.role")}</dt>
        <dd>{roleLabel(agent, t)}</dd>
      </div>
      <div>
        <dt>{t("servers.fact.host")}</dt>
        <dd>{agent.hostname || "-"}</dd>
      </div>
      <div>
        <dt>{t("servers.fact.system")}</dt>
        <dd>{[agent.os_version ?? agent.os, agent.arch].filter(Boolean).join(" ") || "-"}</dd>
      </div>
      <div>
        <dt>{t("servers.fact.agentVersion")}</dt>
        <dd>
          {agent.version || "-"}
          {versionNote(agent, t) && <span className="muted"> ({versionNote(agent, t)})</span>}
        </dd>
      </div>
      <div>
        <dt>{t("servers.fact.lastContact")}</dt>
        <dd>{lastContact(agent, t)}</dd>
      </div>
      {agent.container && (
        <div>
          <dt>{t("servers.fact.container")}</dt>
          <dd>{agent.container}</dd>
        </div>
      )}
      {online && metricsSummary(agent.collector, agent.unavailable, t) && (
        <div>
          <dt>{t("servers.fact.metrics")}</dt>
          <dd>{metricsSummary(agent.collector, agent.unavailable, t)}</dd>
        </div>
      )}
      {agent.id !== "local" && (
        <div>
          <dt>{t("servers.fact.certificate")}</dt>
          <dd>{certificateEnd(agent.cert_not_after, t)}</dd>
        </div>
      )}
    </dl>
  );
}

// ConnectionNotice sits at the top of the page of a server that is not sending
// live data (#436). It says the state and since when, and that what follows
// is what the hub last knew; the live widgets are not drawn for a server that
// is offline, so nothing looks alive.
function ConnectionNotice({ agent }: { agent: Agent }) {
  const t = useT();
  const stale = agent.status === "stale";
  const online = agent.status === "connected" && !agent.update_only;
  if (online) return null;

  const when = stale ? agent.last_heartbeat : (agent.last_seen_at ?? null);
  const since = when
    ? t("servers.server.lastContactAt", {
        date: new Date(when).toLocaleString(),
        ago: lastContact(agent, t),
      })
    : t("servers.list.neverConnected");

  let detail: string;
  if (agent.update_only) {
    detail = t("servers.server.updateOnlyNote", { version: agent.version });
  } else if (agent.status === "revoked") {
    detail = t("servers.server.revokedNote");
  } else if (agent.status === "incompatible") {
    detail = t("servers.server.incompatibleNote", {
      reason:
        agent.incompatible_reason ?? t("servers.server.incompatibleFallback"),
    });
  } else if (stale) {
    detail = t("servers.server.staleNote");
  } else {
    detail = t("servers.server.offlineNote");
  }

  const lastKnown =
    agent.status === "stale" ||
    agent.status === "offline" ||
    agent.status === "revoked";
  return (
    <div role="status" className="notice warn">
      <p>
        <strong>{statusLabel(agent, t)}</strong>
        {lastKnown ? `: ${since}` : null}
      </p>
      <p>{detail}</p>
      {lastKnown && <p className="muted">{t("servers.server.lastKnown")}</p>}
    </div>
  );
}

// certificateEnd says when an agent's certificate ends (#245).
function certificateEnd(
  notAfter: string | undefined,
  t: ReturnType<typeof useT>,
): string {
  if (!notAfter) return t("servers.fact.certificateUnknown");
  const date = new Date(notAfter).toLocaleDateString();
  const days = daysLeft(notAfter);
  return days < 0
    ? t("servers.fact.certificateEnded", { date })
    : t("servers.fact.certificateIn", { date, days });
}

function Manage({ agent, onChanged }: { agent: Agent; onChanged: () => void }) {
  const t = useT();
  const [name, setName] = useState(agent.name ?? "");
  const [error, setError] = useState("");

  const run = async (action: () => Promise<unknown>) => {
    setError("");
    try {
      await action();
      onChanged();
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  return (
    <section className="panel" aria-labelledby="manage-heading">
      <h3 id="manage-heading">{t("servers.server.manage")}</h3>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void run(() => renameAgent(agent.id, name.trim()));
        }}
      >
        <label>
          {t("servers.server.nameLabel")}
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={64}
            placeholder={agent.hostname || agent.id}
          />
        </label>
        <button type="submit">{t("servers.server.rename")}</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {agent.status !== "revoked" && (
        <div style={{ marginTop: 12 }}>
          <ConfirmButton
            label={t("servers.server.revoke")}
            message={t("servers.server.revokeConfirm", {
              name: displayName(agent),
            })}
            confirmLabel={t("servers.server.revokeYes")}
            onConfirm={() => void run(() => revokeAgent(agent.id))}
          />
        </div>
      )}
    </section>
  );
}
