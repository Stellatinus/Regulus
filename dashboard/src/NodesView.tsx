import { useEffect, useRef, useState, type ReactNode } from "react";
import type { Agent } from "./api";
import { agentRole, displayName, lastContact, needsUpdate, statusLabel, versionNote } from "./AgentList";
import { useT, type MessageKey } from "./i18n";
import { isLimited } from "./limits";
import { NODE_TYPES, NodeIcon, nodeTypeDef, useNodeTypes, type NodeTypeId } from "./nodeTypes";
import { serverHref } from "./route";
import { icons } from "./icons";
import { Topology } from "./NodesTopology";

// The Nodes page of the design system v3: one control bar (search, how many
// nodes are shown, filter, three views) and the nodes as cards, as a compact
// list or as the map of the mesh. A node opens in a window over the page.

export type ViewMode = "grid" | "compact" | "topology";

const VIEW_KEY = "regulus.nodesView";

function savedView(): ViewMode {
  try {
    const v = window.localStorage.getItem(VIEW_KEY);
    if (v === "grid" || v === "compact" || v === "topology") return v;
  } catch {
    // no storage
  }
  return "grid";
}

type StatusFilter = "all" | Agent["status"] | "update";

const STATUS_OPTIONS: { id: StatusFilter; label: MessageKey; dot?: string }[] = [
  { id: "all", label: "nodes.filter.all" },
  { id: "connected", label: "servers.status.online" },
  { id: "stale", label: "servers.status.stale" },
  { id: "offline", label: "servers.status.offline" },
  { id: "incompatible", label: "servers.status.incompatible" },
  { id: "revoked", label: "servers.status.revoked" },
  { id: "update", label: "nodes.filter.update", dot: "var(--accent-mesh)" },
];

interface Filters {
  search: string;
  status: StatusFilter;
  type: NodeTypeId | "all";
}

const NO_FILTERS: Filters = { search: "", status: "all", type: "all" };

// Pills about the state of the node's software: what to do, in a word; the
// whole sentence is the tooltip and is written in the node's window.
function UpdateBadges({ agent }: { agent: Agent }) {
  const t = useT();
  const pills: ReactNode[] = [];
  const note = versionNote(agent, t);
  if (needsUpdate(agent)) pills.push(<span key="u" className="upd-pill mesh" title={note}>{t("nodes.badge.update")}</span>);
  else if (agent.compatibility === "ahead") pills.push(<span key="a" className="upd-pill warn" title={note}>{t("nodes.badge.ahead")}</span>);
  if (agent.status === "incompatible") pills.push(<span key="i" className="upd-pill warn" title={note}>{t("nodes.badge.incompatible")}</span>);
  if (isLimited(agent)) pills.push(<span key="l" className="upd-pill warn">{t("servers.metrics.limited")}</span>);
  if (pills.length === 0) {
    const live = agent.status === "connected" || agent.status === "stale";
    pills.push(
      <span key="ok" className="upd-pill ok">
        {live ? t("nodes.badge.upToDate") : agent.version || t("nodes.badge.noVersion")}
      </span>,
    );
  }
  return <div className="update-badges">{pills}</div>;
}

function StatusPill({ agent }: { agent: Agent }) {
  const t = useT();
  return (
    <span className={`status-pill ${agent.status}`}>
      <span className={`status-dot ${agent.status}`} aria-hidden="true" />
      {statusLabel(agent, t)}
    </span>
  );
}

// What sits under a node's name: its host name when that is not the name
// shown, else its id.
const subtitle = (a: Agent) => (a.hostname && a.hostname !== displayName(a) ? a.hostname : a.id === "local" ? "" : a.id);

function Arrow() {
  return (
    <svg className="card-arrow" width="16" height="16" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true" focusable="false">
      <path strokeLinecap="round" strokeLinejoin="round" d="M7.5 5l5 5-5 5" />
    </svg>
  );
}

function GridView({ agents, typeOf }: { agents: Agent[]; typeOf: (a: Agent) => NodeTypeId }) {
  const t = useT();
  return (
    <div className="servers-grid">
      {agents.map((a) => {
        const type = typeOf(a);
        const online = a.status === "connected" || a.status === "stale";
        return (
          <a key={a.id} className={`server-card${online ? "" : " offline-card"}`} href={serverHref(a.id)} aria-label={t("nodes.open", { name: displayName(a) })}>
            <div>
              <div className="card-top">
                <div className="card-identity">
                  <div className={`card-logo role-icon-box role-${type}`}>
                    <NodeIcon type={type} size={23} />
                  </div>
                  <div style={{ minWidth: 0 }}>
                    <div className="card-name">{displayName(a)}</div>
                    <div className="card-sub">{subtitle(a) || " "}</div>
                  </div>
                </div>
                <StatusPill agent={a} />
              </div>
              <div className="card-meta-row">
                <span className={`role-pill role-${type}`}>{t(nodeTypeDef(type).label)}</span>
                <span className="last-contact" title={t("servers.fact.lastContact")}>
                  {lastContact(a, t)}
                </span>
              </div>
            </div>
            <div className="card-footer">
              <UpdateBadges agent={a} />
              <Arrow />
            </div>
          </a>
        );
      })}
    </div>
  );
}

function CompactView({ agents, typeOf }: { agents: Agent[]; typeOf: (a: Agent) => NodeTypeId }) {
  const t = useT();
  return (
    <div className="servers-list">
      {agents.map((a) => {
        const type = typeOf(a);
        const online = a.status === "connected" || a.status === "stale";
        return (
          <a key={a.id} className={`server-row${online ? "" : " offline-card"}`} href={serverHref(a.id)} aria-label={t("nodes.open", { name: displayName(a) })}>
            <div className="card-identity">
              <div className={`row-logo role-icon-box role-${type}`}>
                <NodeIcon type={type} size={19} />
              </div>
              <div style={{ minWidth: 0 }}>
                <div className="card-name">{displayName(a)}</div>
                <div className="card-sub">{subtitle(a) || " "}</div>
              </div>
            </div>
            <div>
              <span className={`role-pill role-${type}`}>{t(nodeTypeDef(type).label)}</span>
            </div>
            <div className="row-status-col">
              <StatusPill agent={a} />
              <span className="last-contact">{lastContact(a, t)}</span>
            </div>
            <UpdateBadges agent={a} />
            <Arrow />
          </a>
        );
      })}
    </div>
  );
}

// NodesView is the control bar and the three views. It does not load
// anything: the page gives it the servers.
export function NodesView({ agents, actions, belowControls }: { agents: Agent[]; actions?: ReactNode; belowControls?: ReactNode }) {
  const t = useT();
  const typeOf = useNodeTypes();
  const [view, setViewState] = useState<ViewMode>(savedView);
  const [filters, setFilters] = useState<Filters>(NO_FILTERS);
  const [filterOpen, setFilterOpen] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);
  const filterWrapper = useRef<HTMLDivElement>(null);

  const setView = (v: ViewMode) => {
    setViewState(v);
    try {
      window.localStorage.setItem(VIEW_KEY, v);
    } catch {
      // not saved
    }
  };

  // "/" jumps to the search box, as the design says.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = document.activeElement;
      const typing = el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement;
      if (e.key === "/" && !typing && !document.querySelector("[role='dialog']")) {
        e.preventDefault();
        searchRef.current?.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (!filterOpen) return;
    const onDown = (e: MouseEvent) => {
      if (filterWrapper.current && !filterWrapper.current.contains(e.target as Node)) setFilterOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setFilterOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [filterOpen]);

  const matches = (a: Agent): boolean => {
    const q = filters.search.trim().toLowerCase();
    if (q) {
      const type = typeOf(a);
      const hay = [displayName(a), a.id, a.hostname, a.os_version ?? a.os, t(nodeTypeDef(type).label), agentRole(a)].join(" ").toLowerCase();
      if (!hay.includes(q)) return false;
    }
    if (filters.status === "update") {
      if (!needsUpdate(a)) return false;
    } else if (filters.status !== "all" && a.status !== filters.status) return false;
    if (filters.type !== "all" && typeOf(a) !== filters.type) return false;
    return true;
  };

  const shown = agents.filter(matches);
  const activeFilters = (filters.status !== "all" ? 1 : 0) + (filters.type !== "all" ? 1 : 0);
  const typeCounts = new Map<NodeTypeId, number>();
  for (const a of agents) typeCounts.set(typeOf(a), (typeCounts.get(typeOf(a)) ?? 0) + 1);

  const viewButton = (id: ViewMode, label: MessageKey, svg: ReactNode) => (
    <button type="button" className="view-btn" aria-pressed={view === id} aria-label={t(label)} title={t(label)} onClick={() => setView(id)}>
      {svg}
    </button>
  );
  const viewIcon = (d: ReactNode) => (
    <svg width="16" height="16" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" aria-hidden="true" focusable="false">
      {d}
    </svg>
  );

  return (
    <>
      <section className="control-bar" aria-label={t("nodes.controls")}>
        <div className="search-box">
          <span className="search-icon" aria-hidden="true">
            {viewIcon(
              <>
                <circle cx="9" cy="9" r="5.5" />
                <path d="M13.5 13.5L17 17" />
              </>,
            )}
          </span>
          <input
            ref={searchRef}
            className="search-input"
            type="search"
            role="searchbox"
            aria-label={t("nodes.search")}
            placeholder={t("nodes.search")}
            autoComplete="off"
            value={filters.search}
            onChange={(e) => setFilters({ ...filters, search: e.target.value })}
          />
          {filters.search ? (
            <button type="button" className="clear-search-btn" aria-label={t("nodes.clearSearch")} title={t("nodes.clearSearch")} onClick={() => setFilters({ ...filters, search: "" })}>
              {viewIcon(<path d="M6 6l8 8M14 6l-8 8" />)}
            </button>
          ) : (
            <kbd className="shortcut-kbd" aria-hidden="true">
              /
            </kbd>
          )}
        </div>

        <div className="control-actions">
          <div className="counter-chip" title={t("nodes.counterTitle")} role="status" aria-label={t("nodes.counter", { shown: shown.length, total: agents.length })}>
            <strong>{shown.length}</strong>
            <span aria-hidden="true">/</span>
            <span aria-hidden="true">{agents.length}</span>
            <span className="counter-label" aria-hidden="true">
              {t("nodes.counterLabel")}
            </span>
          </div>

          <div className="filter-wrapper" ref={filterWrapper}>
            <button
              type="button"
              className={`icon-btn${filterOpen || activeFilters > 0 ? " active" : ""}`}
              aria-haspopup="true"
              aria-expanded={filterOpen}
              aria-controls={filterOpen ? "nodes-filter" : undefined}
              title={t("nodes.filter.title")}
              onClick={() => setFilterOpen((v) => !v)}
            >
              {viewIcon(<path d="M3.5 5.5h13M6 10h8M8.5 14.5h3" />)}
              <span className="filter-btn-label">{t("nodes.filter.button")}</span>
              {activeFilters > 0 && <span className="filter-badge">{activeFilters}</span>}
            </button>
            {filterOpen && (
            <div id="nodes-filter" className="floating-menu filter-popover open" role="group" aria-label={t("nodes.filter.title")}>
              <span className="popover-title">{t("nodes.filter.status")}</span>
              <div className="status-chips-grid">
                {STATUS_OPTIONS.map((o) => (
                  <button key={o.id} type="button" className="filter-chip" aria-pressed={filters.status === o.id} onClick={() => setFilters({ ...filters, status: o.id })}>
                    {o.dot && <span className="dot-indicator" style={{ background: o.dot }} />}
                    <span>{t(o.label)}</span>
                  </button>
                ))}
              </div>
              <div className="menu-divider" style={{ margin: "14px 0" }} />
              <span className="popover-title">{t("nodes.filter.type")}</span>
              <div className="role-list">
                <button type="button" className="role-option" aria-pressed={filters.type === "all"} onClick={() => setFilters({ ...filters, type: "all" })}>
                  <span className="role-option-left">
                    <span className="role-mini-icon role-icon-box" style={{ ["--role-bg" as string]: "var(--hover)", ["--role-fg" as string]: "var(--muted)", ["--role-bd" as string]: "var(--line)" }}>
                      {icons.nodes}
                    </span>
                    <span>{t("nodes.filter.allTypes")}</span>
                  </span>
                  <span className="count">{agents.length}</span>
                </button>
                {NODE_TYPES.map((n) => (
                  <button key={n.id} type="button" className="role-option" aria-pressed={filters.type === n.id} onClick={() => setFilters({ ...filters, type: n.id })}>
                    <span className="role-option-left">
                      <span className={`role-mini-icon role-icon-box role-${n.id}`}>
                        <NodeIcon type={n.id} size={15} />
                      </span>
                      <span>{t(n.label)}</span>
                    </span>
                    <span className="count">{typeCounts.get(n.id) ?? 0}</span>
                  </button>
                ))}
              </div>
              {activeFilters > 0 && (
                <button type="button" className="popover-reset-btn" onClick={() => setFilters({ ...filters, status: "all", type: "all" })}>
                  {t("nodes.filter.reset")}
                </button>
              )}
            </div>
            )}
          </div>

          <div className="view-switcher" role="group" aria-label={t("nodes.view")}>
            {viewButton(
              "grid",
              "nodes.view.grid",
              viewIcon(
                <>
                  <rect x="3" y="3" width="6" height="6" rx="1.3" />
                  <rect x="11" y="3" width="6" height="6" rx="1.3" />
                  <rect x="3" y="11" width="6" height="6" rx="1.3" />
                  <rect x="11" y="11" width="6" height="6" rx="1.3" />
                </>,
              ),
            )}
            {viewButton("compact", "nodes.view.compact", viewIcon(<path d="M3.5 5.5h13M3.5 10h13M3.5 14.5h13" />))}
            {viewButton(
              "topology",
              "nodes.view.topology",
              viewIcon(
                <>
                  <circle cx="4.5" cy="10" r="2" />
                  <circle cx="15" cy="5" r="2" />
                  <circle cx="15" cy="15" r="2" />
                  <path d="M6.4 9.1L13.1 5.9M6.4 10.9L13.1 14.1" />
                </>,
              ),
            )}
          </div>
          {actions}
        </div>
      </section>
      {belowControls}

      <section id="nodes-container" aria-live="polite" aria-label={t("nodes.title")}>
        {agents.length === 0 ? (
          <p>{t("servers.list.noServers")}</p>
        ) : view === "topology" ? (
          <Topology agents={agents} matches={activeFilters === 0 && !filters.search.trim() ? null : matches} typeOf={typeOf} />
        ) : shown.length === 0 && agents.length > 0 ? (
          <div className="empty-state">
            <div className="empty-icon">{viewIcon(<><circle cx="9" cy="9" r="5.5" /><path d="M13.5 13.5L17 17" /></>)}</div>
            <div style={{ fontSize: 14, fontWeight: 600 }}>{t("nodes.empty.title")}</div>
            <div style={{ fontSize: 12.5, color: "var(--muted)", marginTop: 4 }}>{t("nodes.empty.body")}</div>
            <button type="button" onClick={() => setFilters(NO_FILTERS)}>
              {t("nodes.filter.reset")}
            </button>
          </div>
        ) : view === "compact" ? (
          <CompactView agents={shown} typeOf={typeOf} />
        ) : (
          <GridView agents={shown} typeOf={typeOf} />
        )}
      </section>
    </>
  );
}
