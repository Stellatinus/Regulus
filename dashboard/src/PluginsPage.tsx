import { useEffect, useState } from "react";
import { ApiError, fetchPlugins, type PluginsStatus, type PluginStatus } from "./api";
import { formatAgo } from "./format";
import { useT, type MessageKey } from "./i18n";
import { useSession } from "./session";
import { errorMessage } from "./useApiList";

const REFRESH_MS = 10_000;

type State = { kind: "loading" } | { kind: "error"; message: string } | { kind: "ready"; data: PluginsStatus };

const STATE_LABEL: Record<string, MessageKey> = {
  starting: "plugins.state.starting",
  running: "plugins.state.running",
  restarting: "plugins.state.restarting",
  failed: "plugins.state.failed",
  incompatible: "plugins.state.incompatible",
  stopped: "plugins.state.stopped",
};

// The badge colour of a state (styles.css .status.*).
const STATE_CLASS: Record<string, string> = {
  starting: "warn",
  running: "ok",
  restarting: "warn",
  failed: "failed",
  incompatible: "failed",
  stopped: "off",
};

// PluginsPage shows the state of the plugins of the hub and of every
// connected server that runs plugins (#262). The hub does not push plugin
// changes on the live stream, so the page asks again every 10 seconds.
export function PluginsPage() {
  const t = useT();
  const session = useSession();
  const expired = session?.expired;
  const [state, setState] = useState<State>({ kind: "loading" });

  useEffect(() => {
    const controller = new AbortController();
    const load = () =>
      fetchPlugins(controller.signal).then(
        (data) => setState({ kind: "ready", data }),
        (err: unknown) => {
          if (controller.signal.aborted) return;
          if (err instanceof ApiError && err.status === 401) return expired?.();
          setState({ kind: "error", message: errorMessage(err) });
        },
      );
    void load();
    const timer = setInterval(() => void load(), REFRESH_MS);
    return () => {
      controller.abort();
      clearInterval(timer);
    };
  }, [expired]);

  return (
    <section aria-labelledby="plugins-heading">
      <h2 id="plugins-heading">{t("plugins.title")}</h2>
      {state.kind === "loading" && <p>{t("plugins.loading")}</p>}
      {state.kind === "error" && <p role="alert">{t("plugins.loadError", { message: state.message })}</p>}
      {state.kind === "ready" && (
        <>
          <section className="panel" aria-labelledby="plugins-hub-heading">
            <h3 id="plugins-hub-heading">{t("plugins.hub.title")}</h3>
            {!state.data.hub.enabled ? (
              <p>{t("plugins.hub.off")}</p>
            ) : state.data.hub.problem ? (
              <p className="notice error">{t("plugins.hub.problem", { problem: state.data.hub.problem })}</p>
            ) : state.data.hub.plugins.length === 0 ? (
              <p>{t("plugins.hub.none")}</p>
            ) : (
              <PluginTable plugins={state.data.hub.plugins} />
            )}
          </section>
          <section className="panel" aria-labelledby="plugins-agents-heading">
            <h3 id="plugins-agents-heading">{t("plugins.agents.title")}</h3>
            {state.data.agents.length === 0 ? (
              <p>{t("plugins.agents.none")}</p>
            ) : (
              state.data.agents.map((a) => (
                <div key={a.id}>
                  <h4 className="mono">{a.name || a.id}</h4>
                  <PluginTable plugins={a.plugins} />
                </div>
              ))
            )}
          </section>
        </>
      )}
    </section>
  );
}

function PluginTable({ plugins }: { plugins: PluginStatus[] }) {
  const t = useT();
  return (
    <table className="cards">
      <thead>
        <tr>
          <th>{t("plugins.column.plugin")}</th>
          <th>{t("plugins.column.version")}</th>
          <th>{t("plugins.column.state")}</th>
          <th>{t("plugins.column.restarts")}</th>
          <th>{t("plugins.column.since")}</th>
          <th>{t("plugins.column.lastError")}</th>
        </tr>
      </thead>
      <tbody>
        {plugins.map((p) => {
          const label = STATE_LABEL[p.state];
          const hint = p.state === "failed" ? t("plugins.state.failedHint") : p.state === "incompatible" ? t("plugins.state.incompatibleHint") : "";
          return (
            <tr key={p.id}>
              <td data-label={t("plugins.column.plugin")} className="mono">
                {p.id}
              </td>
              <td data-label={t("plugins.column.version")} className="mono">
                {p.version}
              </td>
              <td data-label={t("plugins.column.state")}>
                <span className={`status ${STATE_CLASS[p.state] ?? "off"}`} title={hint || undefined}>
                  {label ? t(label) : p.state}
                </span>
              </td>
              <td data-label={t("plugins.column.restarts")}>{p.restarts}</td>
              <td data-label={t("plugins.column.since")}>{p.since ? formatAgo(p.since, undefined, t) : "–"}</td>
              <td data-label={t("plugins.column.lastError")} className="wrap">
                {p.last_error || "–"}
                {hint && <div className="hint">{hint}</div>}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
