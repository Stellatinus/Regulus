import type { Agent, DiskUsage, MetricsPoint, NetworkUsage } from "./api";

// Guards for the JSON the hub sends to the live widgets (GET /api/agents, the
// metrics history and /api/stream). The hub is trusted, but a proxy, an older
// or newer hub or a bug can send another shape, and a widget that reads a
// missing field would fail. These turn a value into the expected shape or
// return null, so the caller drops what it cannot use instead of crashing.
// They check what the widgets read, not every field of the API.

type Obj = Record<string, unknown>;

const isObj = (v: unknown): v is Obj => typeof v === "object" && v !== null && !Array.isArray(v);
const isNum = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);
const numOrNull = (v: unknown): number | null => (isNum(v) ? v : null);
const strOrNull = (v: unknown): string | null => (typeof v === "string" ? v : null);

// list maps an array with a guard and drops what fails; a missing list (Go
// encodes an empty slice as null) is an empty one.
function list<T>(v: unknown, guard: (item: unknown) => T | null): T[] {
  if (!Array.isArray(v)) return [];
  const out: T[] = [];
  for (const item of v) {
    const ok = guard(item);
    if (ok !== null) out.push(ok);
  }
  return out;
}

function toDisk(v: unknown): DiskUsage | null {
  if (!isObj(v) || typeof v.mount_point !== "string" || !isNum(v.total_bytes) || !isNum(v.used_bytes)) return null;
  return { ...(v as unknown as DiskUsage), filesystem: typeof v.filesystem === "string" ? v.filesystem : "" };
}

function toNetwork(v: unknown): NetworkUsage | null {
  if (!isObj(v) || typeof v.interface !== "string") return null;
  return {
    ...(v as unknown as NetworkUsage),
    rx_bytes: isNum(v.rx_bytes) ? v.rx_bytes : 0,
    tx_bytes: isNum(v.tx_bytes) ? v.tx_bytes : 0,
    rx_bytes_per_sec: numOrNull(v.rx_bytes_per_sec),
    tx_bytes_per_sec: numOrNull(v.tx_bytes_per_sec),
  };
}

const POINT_NUMBERS = [
  "load_1",
  "load_5",
  "load_15",
  "memory_total_bytes",
  "memory_used_bytes",
  "memory_available_bytes",
  "swap_total_bytes",
  "swap_used_bytes",
  "uptime_seconds",
] as const;

// toMetricsPoint returns a report with every field the widgets read, or null
// when it has no valid time or a number is missing. Missing disk and network
// lists become empty, and bad entries in them are dropped.
export function toMetricsPoint(v: unknown): MetricsPoint | null {
  if (!isObj(v) || typeof v.at !== "string" || Number.isNaN(Date.parse(v.at))) return null;
  if (!POINT_NUMBERS.every((k) => isNum(v[k]))) return null;
  // The CPU share is a number or an explicit null (the agent could not read it).
  if (v.cpu_percent !== null && !isNum(v.cpu_percent)) return null;
  return {
    ...(v as unknown as MetricsPoint),
    disks: list(v.disks, toDisk),
    networks: list(v.networks, toNetwork),
    unavailable: strings(v.unavailable),
  };
}

// strings keeps the strings of a list, or returns undefined when there are none.
function strings(v: unknown): string[] | undefined {
  const out = list(v, strOrNull);
  return out.length > 0 ? out : undefined;
}

// toAgent returns an agent with an id and the fields the widgets read set to
// a value of the right type, or null without an id.
export function toAgent(v: unknown): Agent | null {
  if (!isObj(v) || typeof v.id !== "string" || v.id === "") return null;
  return {
    ...(v as unknown as Agent),
    version: typeof v.version === "string" ? v.version : "",
    status: typeof v.status === "string" ? (v.status as Agent["status"]) : "connected",
    connected_at: strOrNull(v.connected_at),
    last_heartbeat: strOrNull(v.last_heartbeat),
    metrics_at: strOrNull(v.metrics_at),
    uptime_seconds: numOrNull(v.uptime_seconds),
    collector: typeof v.collector === "string" ? v.collector : undefined,
    container: typeof v.container === "string" ? v.container : undefined,
    unavailable: strings(v.unavailable),
  };
}

export const toAgents = (v: unknown): Agent[] => list(v, toAgent);
export const toMetricsPoints = (v: unknown): MetricsPoint[] => list(v, toMetricsPoint);
