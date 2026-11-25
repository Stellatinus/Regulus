import { useCallback, useEffect, useState } from "react";
import {
  agentRole,
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
  renameAgent,
  revokeAgent,
  type Agent,
} from "./api";
import { serverIdFromHash } from "./route";
import { useSession } from "./session";
import { errorMessage } from "./useApiList";
import { ConfirmButton, PanelBoundary } from "./ui";
import { daysLeft } from "./CertificateBanner";
import { useT } from "./i18n";
import { registry } from "./widgets";
import { widgetTitle } from "./widgets/registry";
import { metricsSummary } from "./limits";
import { DisksPanel } from "./DisksPanel";

// The widgets of the page of a server, in order.
const WIDGETS = ["system", "cpu", "memory", "network", "disk"];

const REFRESH_MS = 5000;

type State =
  | { kind: "loading" }
  | { kind: "missing" }
  | { kind: "error"; message: string }
  | { kind: "ready"; agent: Agent };

// ServerPage shows one server: its state and the widgets bound to it.
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

  return (
    <section aria-labelledby="server-heading">
      <p>
        <a href="#/servers">{t("servers.server.back")}</a>
      </p>
      {state.kind === "loading" && <p>{t("servers.loading")}</p>}
      {state.kind === "error" && (
        <p role="alert">
          {t("servers.server.loadError", { message: state.message })}
        </p>
      )}
      {state.kind === "missing" && (
        <>
          <h2 id="server-heading">{t("servers.server.notFound")}</h2>
          <p>{t("servers.server.notFoundBody")}</p>
        </>
      )}
      {state.kind === "ready" && (
        <Details
          agent={state.agent}
          canManage={can(session?.user, "servers.manage")}
          canSeeDisks={can(session?.user, "inventory.read")}
          onChanged={reload}
        />
      )}
    </section>
  );
}

function Details({
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
    <>
      <div className="page-title">
        <h2 id="server-heading" className="mono" style={{ margin: 0 }}>
          {displayName(agent)}
        </h2>
        <span className={`status ${agent.status}`}>
          {statusLabel(agent, t)}
        </span>
        {agentRole(agent) === "hub" && (
          <span className="status hub">{roleLabel(agent, t)}</span>
        )}
      </div>
      <ConnectionNotice agent={agent} />
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
          <dd>
            {[agent.os_version ?? agent.os, agent.arch]
              .filter(Boolean)
              .join(" ") || "-"}
          </dd>
        </div>
        <div>
          <dt>{t("servers.fact.agentVersion")}</dt>
          <dd>
            {agent.version || "-"}
            {versionNote(agent, t) && (
              <span className="muted"> ({versionNote(agent, t)})</span>
            )}
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
      {canManage && agent.id !== "local" && (
        <Manage agent={agent} onChanged={onChanged} />
      )}
      {canSeeDisks && (
        <PanelBoundary name="disks">
          <DisksPanel serverId={agent.id} />
        </PanelBoundary>
      )}
      {online ? (
        <div className="server-grid">
          {WIDGETS.map((w) => {
            const def = registry.get(w);
            if (!def) return null;
            const Widget = def.component;
            return (
              <section
                key={w}
                className="widget"
                aria-label={widgetTitle(def, t)}
              >
                <header>
                  <h3 className="widget-title">{widgetTitle(def, t)}</h3>
                </header>
                <div className="widget-body">
                  <Widget
                    instanceId={`server-${w}`}
                    config={{ agentId: agent.id, fixed: true }}
                    setConfig={() => undefined}
                  />
                </div>
              </section>
            );
          })}
        </div>
      ) : null}
    </>
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
