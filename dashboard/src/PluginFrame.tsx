import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, callPluginOp, type PluginUI } from "./api";
import { useLanguage, useT } from "./i18n";
import { clampHeight, parseFrameMessage, PROTOCOL, readTheme } from "./pluginFrame";

// How long a page has to say "ready" before the tab says it failed.
const READY_TIMEOUT_MS = 10000;

type State = { kind: "loading" } | { kind: "ready" } | { kind: "error"; message: string };

// PluginFrame shows the page of a plugin for one node (#21). The page is a
// frame with sandbox="allow-scripts" and NOT allow-same-origin (#315): an
// opaque origin, no cookie, no storage, no access to this page. The hub serves
// it as one HTML page with the module inlined and a policy that allows no
// network; the only door is postMessage (pluginFrame.ts), and every request is
// forwarded by this component to the hub's bridge, as the logged-in person.
// Messages count only when they come from this frame's window.
export function PluginFrame({ plugin, node }: { plugin: PluginUI; node: string }) {
  const t = useT();
  const { lang } = useLanguage();
  const frame = useRef<HTMLIFrameElement>(null);
  const [state, setState] = useState<State>({ kind: "loading" });
  const [height, setHeight] = useState(320);
  const [attempt, setAttempt] = useState(0);
  const [src, setSrc] = useState<string | null>(null);
  const url = `/api/plugins/${encodeURIComponent(plugin.id)}/ui`;

  // Ask for the page first, so that a refusal (not installed, unsafe module)
  // is shown in words instead of as a JSON text inside the frame.
  useEffect(() => {
    const controller = new AbortController();
    fetch(url, { credentials: "same-origin", signal: controller.signal }).then(
      async (res) => {
        if (res.ok) {
          setSrc(url);
          return;
        }
        let message = t("nodes.plugin.unavailable");
        try {
          const body = (await res.json()) as { error?: string };
          if (body.error) message = body.error;
        } catch {
          // keep the generic text
        }
        setState({ kind: "error", message });
      },
      (err: unknown) => {
        if (!controller.signal.aborted) setState({ kind: "error", message: err instanceof Error ? err.message : String(err) });
      },
    );
    return () => controller.abort();
    // t changes with the language and must not reload the page.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [url, attempt]);

  const post = useCallback((message: Record<string, unknown>) => {
    // An opaque origin can only be addressed with "*"; the window is the one
    // this component created, so nothing else receives it.
    frame.current?.contentWindow?.postMessage({ regulus: PROTOCOL, ...message }, "*");
  }, []);

  useEffect(() => {
    if (src === null) return;
    const timer = window.setTimeout(() => {
      setState((s) => (s.kind === "loading" ? { kind: "error", message: t("nodes.plugin.timeout") } : s));
    }, READY_TIMEOUT_MS);
    const onMessage = (e: MessageEvent) => {
      if (!frame.current || e.source !== frame.current.contentWindow) return;
      const m = parseFrameMessage(e.data);
      if (!m) return;
      if (m.type === "ready") {
        window.clearTimeout(timer);
        setState({ kind: "ready" });
        post({ type: "init", locale: lang, theme: readTheme(), context: { node } });
      } else if (m.type === "resize") {
        setHeight(clampHeight(m.height));
      } else {
        callPluginOp(plugin.id, m.op, m.params).then(
          (data) => post({ type: "response", id: m.id, ok: true, data }),
          (err: unknown) => {
            const code = err instanceof ApiError && err.code ? err.code : err instanceof ApiError ? `http_${err.status}` : "network";
            post({ type: "response", id: m.id, ok: false, error: { code, message: err instanceof Error ? err.message : String(err) } });
          },
        );
      }
    };
    window.addEventListener("message", onMessage);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener("message", onMessage);
    };
    // The page is told the language and the node when it says "ready".
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [src, plugin.id, node, post]);

  // A change of theme is sent to a page that is already running.
  useEffect(() => {
    const observer = new MutationObserver(() => post({ type: "theme", theme: readTheme() }));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    return () => observer.disconnect();
  }, [post]);

  const title = lang === "it" ? plugin.node_tab?.title.it : plugin.node_tab?.title.en;
  return (
    <div className="node-body single plugin-pane">
      {state.kind === "loading" && <p>{t("nodes.plugin.loading")}</p>}
      {state.kind === "error" && (
        <div className="placeholder-pane" role="alert">
          <h3>{t("nodes.plugin.failed", { name: title ?? plugin.id })}</h3>
          <p>{state.message}</p>
          <button
            type="button"
            onClick={() => {
              setSrc(null);
              setState({ kind: "loading" });
              setAttempt((n) => n + 1);
            }}
          >
            {t("nodes.plugin.retry")}
          </button>
        </div>
      )}
      {src !== null && state.kind !== "error" && (
        <iframe
          key={`${src}:${attempt}`}
          ref={frame}
          className="plugin-frame"
          title={title ?? plugin.id}
          src={src}
          sandbox="allow-scripts"
          referrerPolicy="no-referrer"
          style={{ height, visibility: state.kind === "ready" ? "visible" : "hidden" }}
        />
      )}
    </div>
  );
}
