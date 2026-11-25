import { useEffect, useState, type ReactNode } from "react";
import { ApiError, fetchAgents, type Agent } from "./api";
import { serverHref } from "./route";
import { formatAgo, formatDuration } from "./format";
import { useSession } from "./session";
import { useT, type MessageKey } from "./i18n";
import type { Translator } from "./widgets/registry";
import { isLimited } from "./limits";

const REFRESH_MS = 5000;

type State =
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "ready"; agents: Agent[] };

interface Props {
  refreshMs?: number;
  // Show the "Servers" heading; a widget has its own title.
  heading?: boolean;
  // Called when the hub answers 401: the session has ended.
  onUnauthorized?: () => void;
  // List every enrolled server, also those that are offline or revoked.
  all?: boolean;
  // Called after the list changes; the page uses it to refresh when an agent is
  // added, renamed or revoked.
  reloadKey?: number;
  // Shown at the far right of the title row (the Servers page puts its "+"
  // button there, #428).
  actions?: ReactNode;
  // Shown between the title row and the list (the open "Add a server" panel).
  belowTitle?: ReactNode;
}

export function AgentList({ refreshMs = REFRESH_MS, heading = true, onUnauthorized: onUnauthorizedProp, all = false, reloadKey = 0, actions, belowTitle }: Props) {
  const t = useT();
  const session = useSession();
  const onUnauthorized = onUnauthorizedProp ?? session?.expired;
  const [state, setState] = useState<State>({ kind: "loading" });

  useEffect(() => {
    const controller = new AbortController();
    const load = () => {
      fetchAgents(controller.signal, all).then(
        (agents) => setState({ kind: "ready", agents }),
        (err: unknown) => {
          if (controller.signal.aborted) return;
          if (err instanceof ApiError && err.status === 401) {
            onUnauthorized?.();
            return;
          }
          setState({ kind: "error", message: err instanceof Error ? err.message : String(err) });
        },
      );
    };
    load();
    const timer = setInterval(load, refreshMs);
    return () => {
      controller.abort();
      clearInterval(timer);
    };
  }, [refreshMs, onUnauthorized, all, reloadKey]);

  return (
    <section aria-labelledby="agents-heading">
      {heading ? (
        <div className="page-title">
          <h2 id="agents-heading" style={{ margin: 0 }}>{t("servers.list.heading")}</h2>
          {state.kind === "ready" && <StatusCounts agents={state.agents} />}
          {actions && <span className="title-actions">{actions}</span>}
        </div>
      ) : (
        <h2 id="agents-heading" className="visually-hidden">{t("servers.list.heading")}</h2>
      )}
      {belowTitle}
      {state.kind === "loading" && <p>{t("servers.loading")}</p>}
      {state.kind === "error" && <p role="alert">{t("servers.list.loadError", { message: state.message })}</p>}
      {state.kind === "ready" && state.agents.length === 0 && <p>{all ? t("servers.list.noServers") : t("servers.list.noneConnected")}</p>}
      {state.kind === "ready" && state.agents.length > 0 && <AgentTable agents={state.agents} counts={!heading} />}
    </section>
  );
}

// displayName is what to call a server: the name an admin gave it, else its
// host name, else its id.
export const displayName = (a: Agent) => a.name || a.hostname || a.id;

const STATUS_LABEL: Record<Agent["status"], MessageKey> = {
  connected: "servers.status.online",
  stale: "servers.status.stale",
  offline: "servers.status.offline",
  revoked: "servers.status.revoked",
  incompatible: "servers.status.incompatible",
};

// versionNote says, in a few words, what to do about an agent whose release
// differs from the hub's, or "" when there is nothing to do.
export function versionNote(a: Agent, t: Translator): string {
  if (a.status === "incompatible") return t("servers.list.noteTooOld");
  if (a.update_only) return t("servers.list.noteUpdateOnly");
  if (a.compatibility === "behind") return t("servers.list.noteBehind");
  if (a.compatibility === "ahead") return t("servers.list.noteAhead");
  return "";
}

// A node that runs an older Regulus than the hub (or one so old that it is
// connected only to be updated) has an update to make.
export const needsUpdate = (a: Agent) => a.compatibility === "behind" || a.compatibility === "too_old" || a.update_only === true;

export const statusLabel = (a: Agent, t: Translator) => t(STATUS_LABEL[a.status]);

// agentRole is the node role of a server. A hub older than 0.3.0-alpha.2 does
// not send it: the built-in agent ("local") is then the hub, the rest agents.
export const agentRole = (a: Agent) => a.role ?? (a.id === "local" ? "hub" : "agent");

// roleLabel is the word for the role; a role the dashboard does not know is
// shown as the hub names it.
export function roleLabel(a: Agent, t: Translator): string {
  const role = agentRole(a);
  if (role === "hub") return t("servers.role.hub");
  if (role === "agent") return t("servers.role.agent");
  return role;
}

// lastContact says when the hub last heard from a server.
export function lastContact(a: Agent, t: Translator): string {
  if (a.status === "connected" || a.status === "stale") return formatAgo(a.last_heartbeat, undefined, t);
  return a.last_seen_at ? formatAgo(a.last_seen_at, undefined, t) : t("servers.list.neverConnected");
}

// StatusCounts says how many servers are in each state ("3 online, 1 offline").
function StatusCounts({ agents }: { agents: Agent[] }) {
  const t = useT();
  const order: Agent["status"][] = ["connected", "stale", "offline", "incompatible", "revoked"];
  const counts = order.map((s) => [s, agents.filter((a) => a.status === s).length] as const).filter(([, n]) => n > 0);
  if (agents.length < 2) return null;
  return (
    <span className="row-actions" aria-label={t("servers.list.counts")}>
      {counts.map(([s, n]) => (
        <span key={s} className={`status ${s}`}>
          {n} {t(STATUS_LABEL[s])}
        </span>
      ))}
    </span>
  );
}

function AgentTable({ agents, counts }: { agents: Agent[]; counts: boolean }) {
  const t = useT();
  return (
    <>
    {counts && <StatusCounts agents={agents} />}
    <table className="cards">
      <thead>
        <tr>
          <th>{t("servers.fact.server")}</th>
          <th>{t("servers.fact.role")}</th>
          <th>{t("servers.fact.system")}</th>
          <th>{t("servers.list.version")}</th>
          <th>{t("servers.fact.status")}</th>
          <th>{t("servers.fact.lastContact")}</th>
          <th>{t("servers.fact.uptime")}</th>
        </tr>
      </thead>
      <tbody>
        {agents.map((a) => (
          <tr key={a.id}>
            <td data-label={t("servers.fact.server")}>
              <span>
                <a href={serverHref(a.id)} className="mono">
                  {displayName(a)}
                </a>
                {a.name && a.hostname && a.name !== a.hostname ? <span className="muted"> {a.hostname}</span> : null}
              </span>
            </td>
            <td data-label={t("servers.fact.role")}>
              <span className={agentRole(a) === "hub" ? "status hub" : "muted"}>
                {roleLabel(a, t)}
              </span>
            </td>
            <td data-label={t("servers.fact.system")}>{[a.os_version ?? a.os, a.arch].filter(Boolean).join(" ") || "-"}</td>
            <td data-label={t("servers.list.version")}>
              <span>
                <span className="mono">{a.version || "-"}</span>
                {versionNote(a, t) && <span className="muted"> {versionNote(a, t)}</span>}
              </span>
            </td>
            <td data-label={t("servers.fact.status")}>
              <span>
                <span className={`status ${a.status}`}>{statusLabel(a, t)}</span>
                {isLimited(a) && <span className="muted"> {t("servers.metrics.limited")}</span>}
              </span>
            </td>
            <td data-label={t("servers.fact.lastContact")}>{lastContact(a, t)}</td>
            <td data-label={t("servers.fact.uptime")}>{a.uptime_seconds === null ? "-" : formatDuration(a.uptime_seconds, t)}</td>
          </tr>
        ))}
      </tbody>
    </table>
    </>
  );
}
