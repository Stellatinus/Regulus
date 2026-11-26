// The messages between the dashboard and the page of a plugin, protocol 1 (#21,
// docs/plugins/runtime.md). The page runs in a frame with an opaque origin: it
// has no session and cannot reach the hub, so this is its only door.
//
//   dashboard -> page  {regulus:1, type:"init", locale, theme:{"--var":"value"}, context:{node}}
//                      {regulus:1, type:"response", id, ok:true, data}
//                      {regulus:1, type:"response", id, ok:false, error:{code, message}}
//                      {regulus:1, type:"theme", theme}
//   page -> dashboard  {regulus:1, type:"ready"}
//                      {regulus:1, type:"request", id, op, params}
//                      {regulus:1, type:"resize", height}

export const PROTOCOL = 1;

// The CSS variables of the dashboard's theme that a page may use.
export const THEME_VARS = [
  "--bg", "--surface", "--surface-2", "--surface-deep", "--ink", "--muted", "--faint", "--line", "--line-2",
  "--ok", "--warn", "--crit", "--info", "--off", "--series-1", "--series-2", "--f-body", "--f-mono",
] as const;

// readTheme gives the current value of each variable, for the page to apply.
export function readTheme(root: HTMLElement = document.documentElement): Record<string, string> {
  const style = getComputedStyle(root);
  const out: Record<string, string> = {};
  for (const name of THEME_VARS) {
    const v = style.getPropertyValue(name).trim();
    if (v) out[name] = v;
  }
  return out;
}

export type FrameMessage =
  | { type: "ready" }
  | { type: "request"; id: string | number; op: string; params?: unknown }
  | { type: "resize"; height: number };

// parseFrameMessage accepts only what the protocol defines; anything else is
// null and is ignored.
export function parseFrameMessage(data: unknown): FrameMessage | null {
  if (typeof data !== "object" || data === null) return null;
  const m = data as Record<string, unknown>;
  if (m.regulus !== PROTOCOL) return null;
  switch (m.type) {
    case "ready":
      return { type: "ready" };
    case "request":
      if ((typeof m.id === "string" || typeof m.id === "number") && typeof m.op === "string" && m.op.length <= 80) {
        return { type: "request", id: m.id, op: m.op, params: m.params };
      }
      return null;
    case "resize":
      return typeof m.height === "number" && Number.isFinite(m.height) ? { type: "resize", height: m.height } : null;
    default:
      return null;
  }
}

// clampHeight keeps a page's frame between a useful minimum and a maximum.
export const clampHeight = (h: number) => Math.min(4000, Math.max(120, Math.ceil(h)));
