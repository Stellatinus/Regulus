import { act, render, screen } from "@testing-library/react";
import { Profiler } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Agent, MetricsPoint } from "./api";
import { FakeSocket } from "./fake-socket";
import { HISTORY_POINTS, MAX_POINTS } from "./live";
import { LiveProvider } from "./LiveProvider";
import { CpuWidget } from "./widgets/BaseWidgets";

// 50 agents with a full hour of reports each: a report of one agent must draw
// again only the widget that shows it.
const AGENTS = 50;
const T0 = Date.parse("2026-01-01T12:00:00Z");

const agent = (id: string): Agent => ({
  id,
  version: "1",
  status: "connected",
  cpu_count: 2,
  connected_at: new Date(T0).toISOString(),
  last_heartbeat: null,
  metrics_at: null,
  uptime_seconds: null,
});

const point = (second: number, cpu = 10): MetricsPoint => ({
  at: new Date(T0 + second * 1000).toISOString(),
  cpu_percent: cpu,
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
});

const ids = Array.from({ length: AGENTS }, (_, i) => `a${String(i).padStart(2, "0")}`);
const hour = Array.from({ length: MAX_POINTS }, (_, i) => point(i - MAX_POINTS));

function mockHub() {
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) => {
      const reply = (data: unknown) => Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(data) });
      if (path === "/api/agents") return reply(ids.map(agent));
      const m = /^\/api\/agents\/([^/?]+)\/metrics(?:\?(.*))?$/.exec(path);
      if (m) {
        const limit = Number(new URLSearchParams(m[2] ?? "").get("limit") ?? MAX_POINTS);
        return reply({ agent: m[1], points: hour.slice(-limit) });
      }
      return Promise.resolve({ ok: false, status: 404, json: () => Promise.resolve({}) });
    }),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("live rendering with many agents", () => {
  it("a report of agent A does not draw the widgets of agent B again", async () => {
    mockHub();
    const renders = new Map<string, number>();
    const count = (id: string) => renders.set(id, (renders.get(id) ?? 0) + 1);
    render(
      <LiveProvider>
        {ids.map((id) => (
          <Profiler key={id} id={id} onRender={() => count(id)}>
            <CpuWidget instanceId={id} config={{ agentId: id, fixed: true }} setConfig={() => undefined} />
          </Profiler>
        ))}
      </LiveProvider>,
    );
    await act(async () => undefined);
    act(() => FakeSocket.last.open());
    await act(async () => undefined);
    expect(screen.getAllByRole("img", { name: /CPU usage/ })).toHaveLength(AGENTS);

    // The history is asked only for the last minutes.
    const urls = vi.mocked(fetch).mock.calls.map((c) => String(c[0])).filter((u) => u.includes("/metrics"));
    expect(urls.length).toBeGreaterThanOrEqual(AGENTS);
    expect(urls.every((u) => u.includes(`limit=${HISTORY_POINTS}`))).toBe(true);

    const before = new Map(renders);
    act(() => FakeSocket.last.send({ type: "agent.metrics", id: "a07", point: point(1, 99) }));
    expect(screen.getByText("99%")).toBeInTheDocument();
    const changed = ids.filter((id) => renders.get(id) !== before.get(id));
    expect(changed).toEqual(["a07"]);

    // A heartbeat of another agent touches only that one, too.
    const before2 = new Map(renders);
    act(() => FakeSocket.last.send({ type: "agent.heartbeat", id: "a33", at: new Date(T0 + 2000).toISOString() }));
    expect(ids.filter((id) => renders.get(id) !== before2.get(id))).toEqual(["a33"]);
  });

  it("loads the history only of the agents a widget shows", async () => {
    mockHub();
    render(
      <LiveProvider>
        <CpuWidget instanceId="w" config={{ agentId: "a05", fixed: true }} setConfig={() => undefined} />
      </LiveProvider>,
    );
    await act(async () => undefined);
    const urls = vi.mocked(fetch).mock.calls.map((c) => String(c[0])).filter((u) => u.includes("/metrics"));
    expect(urls.length).toBeGreaterThan(0);
    expect(urls.every((u) => u.startsWith("/api/agents/a05/metrics"))).toBe(true);
  });
});
