import { describe, expect, it } from "vitest";
import type { Agent, MetricsPoint } from "./api";
import { agentKey, isStale, LiveStore, MAX_POINTS, parseStreamMessage, pickAgent, PLUGINS, PointRing, ROSTER, STATUS, WATCH } from "./live";

function agent(id: string, over: Partial<Agent> = {}): Agent {
  return {
    id,
    version: "1",
    status: "connected",
    connected_at: "2026-01-01T00:00:00Z",
    last_heartbeat: null,
    metrics_at: null,
    uptime_seconds: null,
    ...over,
  };
}

function point(at: string, over: Partial<MetricsPoint> = {}): MetricsPoint {
  return {
    at,
    cpu_percent: 1,
    load_1: 0,
    load_5: 0,
    load_15: 0,
    memory_total_bytes: 100,
    memory_used_bytes: 10,
    memory_available_bytes: 90,
    swap_total_bytes: 0,
    swap_used_bytes: 0,
    uptime_seconds: 5,
    disks: [],
    networks: [],
    ...over,
  };
}

const at = (second: number) => new Date(Date.UTC(2026, 0, 1) + second * 1000).toISOString();

function withAgents(...ids: string[]): LiveStore {
  const store = new LiveStore();
  store.dispatch({ type: "agents", agents: ids.map((id) => agent(id)), staleAfterSeconds: 30 });
  return store;
}

const series = (store: LiveStore, id: string) => store.entries.get(id)?.points.toArray().map((p) => p.at);

describe("LiveStore", () => {
  it("replaces the agents on a snapshot, sorted, and keeps the history of those still there", () => {
    const s = withAgents("b", "a");
    expect(s.roster.map((a) => a.id)).toEqual(["a", "b"]);
    expect(s.ready).toBe(true);
    expect(s.staleAfterMs).toBe(30000);

    s.dispatch({ type: "history", id: "a", points: [point(at(1))] });
    s.dispatch({ type: "history", id: "b", points: [point(at(1))] });
    s.dispatch({ type: "agents", agents: [agent("b")] });
    expect([...s.entries.keys()]).toEqual(["b"]);
    expect(series(s, "b")).toEqual([at(1)]);
    expect(s.staleAfterMs).toBe(30000); // not sent this time: unchanged
  });

  it("adds, and removes with its metrics, an agent", () => {
    const s = withAgents("a");
    s.dispatch({ type: "connected", agent: agent("0") });
    expect(s.roster.map((a) => a.id)).toEqual(["0", "a"]);
    s.dispatch({ type: "metrics", id: "a", point: point(at(1)) });
    s.dispatch({ type: "disconnected", id: "a" });
    expect(s.roster.map((a) => a.id)).toEqual(["0"]);
    expect(s.entries.has("a")).toBe(false);
  });

  it("a heartbeat revives a stale agent", () => {
    const s = new LiveStore();
    s.dispatch({ type: "agents", agents: [agent("a", { status: "stale" })] });
    s.dispatch({ type: "heartbeat", id: "a", at: at(9) });
    expect(s.entries.get("a")?.agent).toMatchObject({ status: "connected", last_heartbeat: at(9) });
  });

  it("appends metrics and updates the agent, ignoring unknown agents", () => {
    const s = withAgents("a");
    s.dispatch({ type: "metrics", id: "a", point: point(at(1), { uptime_seconds: 77 }) });
    expect(s.entries.get("a")?.agent).toMatchObject({ metrics_at: at(1), uptime_seconds: 77 });
    expect(series(s, "a")).toHaveLength(1);
    s.dispatch({ type: "metrics", id: "ghost", point: point(at(2)) });
    s.dispatch({ type: "history", id: "ghost", points: [point(at(2))] });
    expect(s.entries.has("ghost")).toBe(false);
  });

  it("wakes only the subscribers of the agent that changed", () => {
    const s = withAgents("a", "b");
    const calls: string[] = [];
    for (const key of [agentKey("a"), agentKey("b"), ROSTER, STATUS]) s.subscribe(key, () => calls.push(key));
    const before = s.entries.get("b");
    s.dispatch({ type: "metrics", id: "a", point: point(at(1)) });
    s.dispatch({ type: "heartbeat", id: "a", at: at(2) });
    expect(calls).toEqual([agentKey("a"), agentKey("a")]);
    expect(s.entries.get("b")).toBe(before);
    // A repeated report from the history changes nothing and wakes nobody.
    s.dispatch({ type: "history", id: "a", points: [point(at(1))] });
    expect(calls).toHaveLength(2);
  });

  it("tells when an agent starts being shown", () => {
    const s = new LiveStore();
    let woken = 0;
    s.subscribe(WATCH, () => woken++);
    const off1 = s.watch("a");
    const off2 = s.watch("a");
    expect(s.watched()).toEqual(["a"]);
    expect(woken).toBe(1);
    off1();
    off1(); // twice is harmless
    expect(s.watched()).toEqual(["a"]);
    off2();
    expect(s.watched()).toEqual([]);
  });
});

describe("PointRing", () => {
  it("orders by time, drops repeats and invalid times, and caps the length", () => {
    const ring = new PointRing();
    const a = point("2026-01-01T00:00:01Z");
    const b = point("2026-01-01T00:00:02Z");
    const c = point("2026-01-01T00:00:03.5Z");
    ring.add([a, c]);
    expect(ring.add([b, a])).toBe(true);
    expect(ring.toArray().map((p) => p.at)).toEqual([a.at, b.at, c.at]);
    expect(ring.add([])).toBe(false);
    expect(ring.add([a, point("not a time")])).toBe(false);
    // 2026-01-01T00:00:03Z sorts after 00:00:03.5Z as text but not as a time
    const late = new PointRing();
    late.add([c, point("2026-01-01T00:00:03Z")]);
    expect(late.toArray().map((p) => p.at)).toEqual(["2026-01-01T00:00:03Z", c.at]);

    const many = Array.from({ length: MAX_POINTS + 5 }, (_, i) => point(at(i)));
    const capped = new PointRing();
    capped.add(many);
    expect(capped.size).toBe(MAX_POINTS);
    expect(capped.pointAt(0)).toBe(many[5]);
    capped.add([point(at(MAX_POINTS + 100))]);
    expect(capped.size).toBe(MAX_POINTS);
    expect(capped.pointAt(0)).toBe(many[6]);
    expect(capped.last?.at).toBe(at(MAX_POINTS + 100));
    // An older report that is missing still lands in order after wrapping.
    capped.add([point(at(MAX_POINTS + 50))]);
    expect(capped.size).toBe(MAX_POINTS);
    expect(capped.toArray().at(-2)?.at).toBe(at(MAX_POINTS + 50));
  });

  it("gives the window up to the newest report, cached until the series changes", () => {
    const ring = new PointRing(100);
    ring.add(Array.from({ length: 150 }, (_, i) => point(at(i))));
    const w = ring.window(10_000);
    expect(w.points.map((p) => p.at)).toEqual(Array.from({ length: 11 }, (_, i) => at(139 + i)));
    expect(w.times).toEqual(w.points.map((p) => Date.parse(p.at)));
    expect(ring.window(10_000)).toBe(w);
    ring.add([point(at(150))]);
    expect(ring.window(10_000)).not.toBe(w);
    expect(new PointRing().window(1000)).toEqual({ points: [], times: [] });
  });
});

// Per-message cost: a report appended to a full series must not cost more
// than one appended to a short one. Timing with a wide margin, so it only
// fails when the cost grows with the series (the old code copied 3600 points
// per report, about 100 times the work of a 36-point series).
describe("per-message cost", () => {
  const AGENTS = 50;

  function loaded(perAgent: number): LiveStore {
    const s = new LiveStore();
    s.dispatch({ type: "agents", agents: Array.from({ length: AGENTS }, (_, i) => agent(`a${i}`)) });
    for (let i = 0; i < AGENTS; i++) {
      s.dispatch({ type: "history", id: `a${i}`, points: Array.from({ length: perAgent }, (_, n) => point(at(n))) });
    }
    return s;
  }

  function cost(s: LiveStore, from: number): number {
    const messages = 20000;
    const start = performance.now();
    for (let n = 0; n < messages; n++) {
      s.dispatch({ type: "metrics", id: `a${n % AGENTS}`, point: point(at(from + n)) });
    }
    return performance.now() - start;
  }

  it("does not grow with the length of the series (50 agents x 3600 points)", () => {
    const short = loaded(36);
    const long = loaded(MAX_POINTS);
    cost(short, 100); // warm up
    cost(long, MAX_POINTS);
    const best = (s: LiveStore, from: number) => Math.min(...[0, 1, 2].map((r) => cost(s, from + 100000 * (r + 1))));
    const tShort = best(short, 1000);
    const tLong = best(long, MAX_POINTS + 1000);
    expect(long.entries.get("a0")?.points.size).toBe(MAX_POINTS);
    expect(tLong).toBeLessThan(tShort * 5 + 20);
  });
});

describe("parseStreamMessage", () => {
  const json = (v: unknown) => JSON.stringify(v);

  it("understands every event of the stream", () => {
    expect(parseStreamMessage(json({ type: "snapshot", stale_after_seconds: 30, agents: [] }))).toEqual({
      type: "agents",
      agents: [],
      staleAfterSeconds: 30,
    });
    expect(parseStreamMessage(json({ type: "agent.connected", agent: { id: "a" } }))).toMatchObject({ type: "connected" });
    // A snapshot keeps the agents it can read.
    expect(parseStreamMessage(json({ type: "snapshot", agents: [{ id: "a" }, { id: 7 }, null] }))).toMatchObject({
      agents: [{ id: "a" }],
    });
    expect(parseStreamMessage(json({ type: "agent.disconnected", id: "a" }))).toEqual({ type: "disconnected", id: "a" });
    expect(parseStreamMessage(json({ type: "agent.heartbeat", id: "a", at: "t" }))).toEqual({ type: "heartbeat", id: "a", at: "t" });
    expect(parseStreamMessage(json({ type: "agent.metrics", id: "a", point: point(at(1)) }))).toMatchObject({ type: "metrics", id: "a" });
  });

  it("ignores what it does not understand", () => {
    for (const bad of [
      "not json",
      "null",
      "42",
      json({ type: "future.event" }),
      json({ type: "snapshot" }),
      json({ type: "agent.heartbeat", id: "a" }),
      json({ type: "agent.metrics", id: "a" }),
      json({ type: "agent.metrics", id: "a", point: {} }),
      json({ type: "agent.metrics", id: "a", point: { ...point(at(1)), cpu_percent: "high" } }),
      json({ type: "agent.connected", agent: { name: "no id" } }),
      json({ type: "agent.connected" }),
      new ArrayBuffer(1),
    ]) {
      expect(parseStreamMessage(bad)).toBeNull();
    }
  });
});

describe("isStale", () => {
  const now = Date.parse("2026-01-01T00:01:00Z");

  it("compares the last news with the hub's limit as time passes", () => {
    const a = agent("a", { last_heartbeat: "2026-01-01T00:00:50Z" });
    expect(isStale(a, 30000, now)).toBe(false);
    expect(isStale(a, 5000, now)).toBe(true);
    // metrics count as news too
    expect(isStale(agent("a", { last_heartbeat: "2026-01-01T00:00:00Z", metrics_at: "2026-01-01T00:00:55Z" }), 30000, now)).toBe(false);
  });

  it("trusts the hub's flag until the limit is known", () => {
    expect(isStale(agent("a", { status: "stale" }), null, now)).toBe(true);
    expect(isStale(agent("a"), null, now)).toBe(false);
  });
});

describe("pickAgent", () => {
  const agents = [agent("a"), agent("local"), agent("z")];

  it("prefers the saved one, then the built-in agent, then the first", () => {
    expect(pickAgent(agents, "z")?.id).toBe("z");
    expect(pickAgent(agents, "gone")?.id).toBe("local");
    expect(pickAgent(agents, 42)?.id).toBe("local");
    expect(pickAgent([agent("a"), agent("z")], undefined)?.id).toBe("a");
    expect(pickAgent([], "a")).toBeUndefined();
  });
});

describe("plugin changes on the stream (#319)", () => {
  it("parses plugins.changed", () => {
    expect(parseStreamMessage(JSON.stringify({ type: "plugins.changed", source: "agent", id: "a1" }))).toEqual({ type: "plugins" });
  });

  it("grows the plugins version on a change, a snapshot and an agent that comes or goes", () => {
    const store = new LiveStore();
    let woken = 0;
    store.subscribe(PLUGINS, () => woken++);
    const a1 = { id: "a1", version: "1", status: "connected", connected_at: "2026-01-01T00:00:00Z", last_heartbeat: null, metrics_at: null, uptime_seconds: null } as Agent;
    store.dispatch({ type: "agents", agents: [] });
    store.dispatch({ type: "plugins" });
    store.dispatch({ type: "connected", agent: a1 });
    store.dispatch({ type: "disconnected", id: "a1" });
    store.dispatch({ type: "disconnected", id: "a1" }); // already gone: nothing
    store.dispatch({ type: "heartbeat", id: "a1", at: "2026-01-01T00:00:01Z" });
    expect(store.pluginsVersion).toBe(4);
    expect(woken).toBe(4);
  });
});
