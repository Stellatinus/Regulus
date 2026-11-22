import { useEffect, useRef, useState } from "react";
import { ApiError, fetchPlugins, type PluginsStatus, type PluginStatus } from "./api";
import { formatAgo } from "./format";
import { useT, type MessageKey } from "./i18n";
import { usePluginsVersion } from "./LiveProvider";
import { useSession } from "./session";
import { errorMessage } from "./useApiList";

// A burst of changes (a plugin that keeps crashing at start) is read once:
// the page waits this long after a change before asking the hub.
export const RELOAD_DELAY_MS = 250;

type State = { kind: "loading" } | { kind: "error"; message: string } | { kind: "ready"; data: PluginsStatus };

const STATE_LABEL: Record<string, MessageKey> = {
  starting: "plugins.state.starting",
  running: "plugins.state.running",
  restarting: "plugins.state.restarting",
  failed: "plugins.state.failed",
  incompatible: "plugins.state.incompatible",
  stopped: "plugins.state.stopped",
  invalid: "plugins.state.invalid",
};

const STATE_HINT: Record<string, MessageKey> = {
  failed: "plugins.state.failedHint",
  incompatible: "plugins.state.incompatibleHint",
  invalid: "plugins.state.invalidHint",
};

// The badge colour of a state (styles.css .status.*).
const STATE_CLASS: Record<string, string> = {
  starting: "warn",
  running: "ok",
  restarting: "warn",
  failed: "failed",
  incompatible: "failed",
  stopped: "off",
  invalid: "failed",
};

// PluginsPage shows the state of the plugins of the hub and of every
// connected server that runs plugins (#262), and the directories that are not
// usable plugins (state "invalid", #319). It loads them once, then again
// whenever the live stream says they may have changed ("plugins.changed", a
// snapshot after a reconnection, a server that comes or goes): no polling.
export function PluginsPage() {
  const t = useT();
  const session = useSession();
  const expired = session?.expired;
  const version = usePluginsVersion();
  const [state, setState] = useState<State>({ kind: "loading" });
  const loadedOnce = useRef(false);

  useEffect(() => {
    const controller = new AbortController();
    const load = () =>
      fetchPlugins(controller.signal).then(
        (data) => {
          loadedOnce.current = true;
          setState({ kind: "ready", data });
        },
        (err: unknown) => {
          if (controller.signal.aborted) return;
          if (err instanceof ApiError && err.status === 401) return expired?.();
          setState({ kind: "error", message: errorMessage(err) });
        },
      );
    // The first load is at once; a reload waits for the burst to end. A newer
    // change cancels a reload that has not answered yet.
    const timer = setTimeout(() => void load(), loadedOnce.current ? RELOAD_DELAY_MS : 0);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [expired, version]);

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
          const hintKey = STATE_HINT[p.state];
          const hint = hintKey ? t(hintKey) : "";
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
                {p.last_error && <div className="hint">{t("plugins.ownWords")}</div>}
                {hint && <div className="hint">{hint}</div>}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
