import type { Agent, MetricsPoint } from "./api";
import { toAgent, toAgents, toMetricsPoint } from "./shapes";

// How many reports are kept per agent; the hub keeps the same.
export const MAX_POINTS = 3600;

// How many reports the dashboard asks the hub for when it loads the history of
// an agent. The charts show the last 10 minutes, and 600 reports cover them at
// the shortest metrics interval the hub accepts (1 second).
export const HISTORY_POINTS = 600;

export type Connection = "connecting" | "live" | "offline";

export type LiveAction =
  | { type: "agents"; agents: Agent[]; staleAfterSeconds?: number }
  | { type: "connected"; agent: Agent }
  | { type: "disconnected"; id: string }
  | { type: "heartbeat"; id: string; at: string }
  | { type: "metrics"; id: string; point: MetricsPoint }
  | { type: "history"; id: string; points: MetricsPoint[] }
  | { type: "plugins" };

// A window of a series: the reports and when each was taken (epoch ms).
export interface SeriesWindow {
  points: MetricsPoint[];
  times: number[];
}

// PointRing holds the recent reports of one agent, ordered by time, in a ring
// buffer: adding a report newer than the last one costs the same however long
// the series is, and the oldest beyond the capacity are overwritten. It is
// mutable; version changes whenever its content does.
export class PointRing {
  private items: MetricsPoint[] = [];
  private stamps: number[] = [];
  // Index of the oldest report once the ring is full.
  private head = 0;
  private cache: { version: number; windowMs: number; value: SeriesWindow } | null = null;
  version = 0;

  constructor(readonly capacity = MAX_POINTS) {}

  get size(): number {
    return this.items.length;
  }

  private index(i: number): number {
    return (this.head + i) % this.items.length;
  }

  timeAt(i: number): number {
    return this.stamps[this.index(i)];
  }

  pointAt(i: number): MetricsPoint {
    return this.items[this.index(i)];
  }

  get last(): MetricsPoint | undefined {
    return this.size === 0 ? undefined : this.pointAt(this.size - 1);
  }

  get lastTime(): number {
    return this.size === 0 ? -Infinity : this.timeAt(this.size - 1);
  }

  // indexOf returns the first index whose time is at least t (size if none).
  private indexOf(t: number): number {
    let lo = 0;
    let hi = this.size;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (this.timeAt(mid) < t) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }

  private append(point: MetricsPoint, t: number) {
    if (this.items.length < this.capacity) {
      this.items.push(point);
      this.stamps.push(t);
    } else {
      this.items[this.head] = point;
      this.stamps[this.head] = t;
      this.head = (this.head + 1) % this.capacity;
    }
  }

  // add puts reports into the series, dropping repeats (a report can arrive
  // both from the history and from the stream) and reports without a valid
  // time. It tells whether anything changed. Reports newer than the last take
  // the fast path; an older one that is missing rebuilds the ring.
  add(incoming: MetricsPoint[]): boolean {
    let changed = false;
    const late: { p: MetricsPoint; t: number }[] = [];
    for (const p of incoming) {
      const t = Date.parse(p.at);
      if (Number.isNaN(t)) continue;
      if (t > this.lastTime) {
        this.append(p, t);
        changed = true;
      } else {
        const i = this.indexOf(t);
        if (i >= this.size || this.timeAt(i) !== t) late.push({ p, t });
      }
    }
    if (late.length > 0) {
      const all: { p: MetricsPoint; t: number }[] = [];
      for (let i = 0; i < this.size; i++) all.push({ p: this.pointAt(i), t: this.timeAt(i) });
      const seen = new Set(all.map((e) => e.t));
      for (const e of late) {
        if (!seen.has(e.t)) {
          seen.add(e.t);
          all.push(e);
        }
      }
      all.sort((a, b) => a.t - b.t);
      const kept = all.slice(Math.max(0, all.length - this.capacity));
      this.items = kept.map((e) => e.p);
      this.stamps = kept.map((e) => e.t);
      this.head = 0;
      changed = true;
    }
    if (changed) this.version++;
    return changed;
  }

  // window returns the reports of the last windowMs milliseconds up to the
  // newest one. Its cost depends on the window, not on the whole series, and
  // the result is kept until the series changes, so callers can memoise on it.
  window(windowMs: number): SeriesWindow {
    const c = this.cache;
    if (c && c.version === this.version && c.windowMs === windowMs) return c.value;
    const points: MetricsPoint[] = [];
    const times: number[] = [];
    for (let i = this.indexOf(this.lastTime - windowMs); i < this.size; i++) {
      points.push(this.pointAt(i));
      times.push(this.timeAt(i));
    }
    const value = { points, times };
    this.cache = { version: this.version, windowMs, value };
    return value;
  }

  toArray(): MetricsPoint[] {
    return Array.from({ length: this.size }, (_, i) => this.pointAt(i));
  }
}

// AgentEntry is what the store knows of one connected agent. A new entry
// object replaces the old one at every change, so a component can tell that
// the agent changed by comparing references; the ring itself is shared.
export interface AgentEntry {
  readonly agent: Agent;
  readonly points: PointRing;
}

// Keys a component can subscribe to. Each agent has its own key, so a report
// of one agent wakes only the components that show it.
export const ROSTER = "roster";
export const STATUS = "status";
export const WATCH = "watch";
// PLUGINS wakes the views of the plugins (the Plugins page) when they may
// have changed: see LiveStore.pluginsVersion.
export const PLUGINS = "plugins";
export const agentKey = (id: string) => `agent:${id}`;

const byId = (a: Agent, b: Agent) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);

// sameRoster tells whether two lists name the same agents the same way, so the
// selectors that list them need not be drawn again.
const sameRoster = (a: Agent[], b: Agent[]) =>
  a.length === b.length && a.every((x, i) => x.id === b[i].id && x.name === b[i].name && x.hostname === b[i].hostname);

// LiveStore holds the live state of the agents for the widgets, outside
// React: components read it with useSyncExternalStore and subscribe only to
// the keys they need (the list of agents, one agent, the connection status),
// so a report of one agent does not draw the widgets of the others again.
export class LiveStore {
  // Connected agents, ordered by id. It changes only when agents come, go or
  // are renamed, not at every report.
  roster: Agent[] = [];
  entries = new Map<string, AgentEntry>();
  // Silence after which the hub calls an agent stale; null until known.
  staleAfterMs: number | null = null;
  // Whether the list of agents was received at least once.
  ready = false;
  connection: Connection = "connecting";
  // The current time, refreshed every few seconds, for staleness checks.
  now = Date.now();
  // pluginsVersion grows whenever the state of the plugins may have changed:
  // a "plugins.changed" event, a new list of agents (a snapshot after a
  // reconnection, which may have missed events) and an agent that connects
  // or disconnects (its plugins come and go with it). The stream carries no
  // plugin state; a view reads GET /api/plugins again when it grows.
  pluginsVersion = 0;

  private listeners = new Map<string, Set<() => void>>();
  private watchers = new Map<string, number>();

  subscribe(key: string, listener: () => void): () => void {
    let set = this.listeners.get(key);
    if (!set) {
      set = new Set();
      this.listeners.set(key, set);
    }
    const own = set;
    own.add(listener);
    return () => {
      own.delete(listener);
      if (own.size === 0 && this.listeners.get(key) === own) this.listeners.delete(key);
    };
  }

  private emit(key: string) {
    const set = this.listeners.get(key);
    if (set) for (const listener of [...set]) listener();
  }

  // watch marks an agent as shown by a component, so that its history is
  // loaded; the returned function undoes it.
  watch(id: string): () => void {
    const n = (this.watchers.get(id) ?? 0) + 1;
    this.watchers.set(id, n);
    if (n === 1) this.emit(WATCH);
    let done = false;
    return () => {
      if (done) return;
      done = true;
      const left = (this.watchers.get(id) ?? 1) - 1;
      if (left <= 0) this.watchers.delete(id);
      else this.watchers.set(id, left);
    };
  }

  watched(): string[] {
    return [...this.watchers.keys()];
  }

  // lastAt is the time of the newest report of an agent, as the hub wrote it.
  lastAt(id: string): string | undefined {
    return this.entries.get(id)?.points.last?.at;
  }

  setConnection(connection: Connection) {
    if (this.connection === connection) return;
    this.connection = connection;
    this.emit(STATUS);
  }

  tick(now: number) {
    this.now = now;
    this.emit(STATUS);
  }

  private pluginsChanged() {
    this.pluginsVersion++;
    this.emit(PLUGINS);
  }

  private setRoster(roster: Agent[]) {
    if (sameRoster(this.roster, roster)) return;
    this.roster = roster;
    this.emit(ROSTER);
  }

  private setReady(staleAfterMs: number | null) {
    if (this.ready && this.staleAfterMs === staleAfterMs) return;
    this.ready = true;
    this.staleAfterMs = staleAfterMs;
    this.emit(STATUS);
  }

  private put(agent: Agent) {
    const old = this.entries.get(agent.id);
    this.entries.set(agent.id, { agent, points: old?.points ?? new PointRing() });
    this.emit(agentKey(agent.id));
  }

  private update(id: string, change: (e: AgentEntry) => AgentEntry | null) {
    const entry = this.entries.get(id);
    if (!entry) return;
    const next = change(entry);
    if (!next) return;
    this.entries.set(id, next);
    this.emit(agentKey(id));
  }

  dispatch(action: LiveAction) {
    switch (action.type) {
      case "agents": {
        const agents = [...action.agents].sort(byId);
        const ids = new Set(agents.map((a) => a.id));
        for (const id of [...this.entries.keys()]) {
          if (!ids.has(id)) {
            this.entries.delete(id);
            this.emit(agentKey(id));
          }
        }
        for (const agent of agents) this.put(agent);
        this.setRoster(agents);
        this.setReady(action.staleAfterSeconds === undefined ? this.staleAfterMs : action.staleAfterSeconds * 1000);
        this.pluginsChanged();
        return;
      }
      case "connected":
        this.put(action.agent);
        this.setRoster([...this.roster.filter((a) => a.id !== action.agent.id), action.agent].sort(byId));
        this.setReady(this.staleAfterMs);
        this.pluginsChanged();
        return;
      case "disconnected":
        if (!this.entries.delete(action.id)) return;
        this.emit(agentKey(action.id));
        this.setRoster(this.roster.filter((a) => a.id !== action.id));
        this.pluginsChanged();
        return;
      case "plugins":
        this.pluginsChanged();
        return;
      case "heartbeat":
        this.update(action.id, (e) => ({ ...e, agent: { ...e.agent, status: "connected", last_heartbeat: action.at } }));
        return;
      case "metrics": {
        const { point } = action;
        this.update(action.id, (e) => {
          e.points.add([point]);
          return {
            points: e.points,
            agent: { ...e.agent, status: "connected", metrics_at: point.at, uptime_seconds: point.uptime_seconds },
          };
        });
        return;
      }
      case "history":
        this.update(action.id, (e) => (e.points.add(action.points) ? { ...e } : null));
        return;
    }
  }
}

// parseStreamMessage turns a message of GET /api/stream into an action, or
// null for anything it does not understand (so new event types are ignored).
export function parseStreamMessage(data: unknown): LiveAction | null {
  if (typeof data !== "string") return null;
  let msg: Record<string, unknown>;
  try {
    const parsed: unknown = JSON.parse(data);
    if (typeof parsed !== "object" || parsed === null) return null;
    msg = parsed as Record<string, unknown>;
  } catch {
    return null;
  }
  const str = (v: unknown): v is string => typeof v === "string" && v !== "";
  // The payloads are checked: an agent without an id or a report with a
  // missing number is dropped here rather than breaking a widget later.
  switch (msg.type) {
    case "snapshot":
      return Array.isArray(msg.agents)
        ? {
            type: "agents",
            agents: toAgents(msg.agents),
            staleAfterSeconds: typeof msg.stale_after_seconds === "number" ? msg.stale_after_seconds : undefined,
          }
        : null;
    case "agent.connected": {
      const agent = toAgent(msg.agent);
      return agent ? { type: "connected", agent } : null;
    }
    case "agent.disconnected":
      return str(msg.id) ? { type: "disconnected", id: msg.id } : null;
    case "agent.heartbeat":
      return str(msg.id) && str(msg.at) ? { type: "heartbeat", id: msg.id, at: msg.at } : null;
    case "agent.metrics": {
      const point = toMetricsPoint(msg.point);
      return str(msg.id) && point ? { type: "metrics", id: msg.id, point } : null;
    }
    case "plugins.changed":
      return { type: "plugins" };
    default:
      return null;
  }
}

// lastSeen is when the hub last heard from the agent, in epoch milliseconds.
export function lastSeen(agent: Agent): number {
  return Math.max(...[agent.last_heartbeat, agent.metrics_at, agent.connected_at].map((t) => (t ? Date.parse(t) : 0)));
}

// isStale tells whether the agent has been silent for longer than the hub's
// staleness limit. The hub's own flag only changes when a message arrives, so
// the dashboard also checks as time passes.
export function isStale(agent: Agent, staleAfterMs: number | null, now: number): boolean {
  if (staleAfterMs !== null && now - lastSeen(agent) > staleAfterMs) return true;
  return agent.status === "stale" && staleAfterMs === null;
}

// pickAgent chooses the agent a widget shows: the one saved in its settings if
// it is connected, else the hub's own built-in agent, else the first one.
export function pickAgent(agents: Agent[], preferred: unknown): Agent | undefined {
  return (
    agents.find((a) => a.id === preferred) ?? agents.find((a) => a.id === "local") ?? agents[0]
  );
}
