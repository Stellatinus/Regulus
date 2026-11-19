import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Agent, MetricsPoint } from "../api";
import { FakeSocket } from "../fake-socket";
import { LiveProvider, retryDelay, STABLE_MS } from "../LiveProvider";
import { CpuWidget, DiskWidget, MemoryWidget, NetworkWidget, SystemWidget, UptimeWidget } from "./BaseWidgets";
import { registry } from "./index";
import type { WidgetProps } from "./registry";
import { ADMIN_PERMISSIONS } from "../test-users";

const GIB = 1024 ** 3;
const T0 = Date.parse("2026-01-01T12:00:00Z");

function agent(id: string, over: Partial<Agent> = {}): Agent {
  return {
    id,
    version: "0.1.0",
    status: "connected",
    hostname: `host-${id}`,
    os: "linux",
    os_version: "Ubuntu 24.04",
    kernel: "6.8.0",
    arch: "amd64",
    cpu_count: 4,
    memory_total_bytes: 16 * GIB,
    connected_at: new Date(T0).toISOString(),
    last_heartbeat: new Date(T0).toISOString(),
    metrics_at: new Date(T0).toISOString(),
    uptime_seconds: 90061,
    ...over,
  };
}

function point(offsetSeconds: number, over: Partial<MetricsPoint> = {}): MetricsPoint {
  return {
    at: new Date(T0 + offsetSeconds * 1000).toISOString(),
    cpu_percent: 42,
    load_1: 0.5,
    load_5: 0.4,
    load_15: 0.3,
    memory_total_bytes: 16 * GIB,
    memory_used_bytes: 4 * GIB,
    memory_available_bytes: 12 * GIB,
    swap_total_bytes: 2 * GIB,
    swap_used_bytes: GIB / 2,
    uptime_seconds: 90061,
    disks: [
      { mount_point: "/", filesystem: "ext4", total_bytes: 100 * GIB, used_bytes: 95 * GIB },
      { mount_point: "/data", filesystem: "xfs", total_bytes: 200 * GIB, used_bytes: 50 * GIB },
    ],
    networks: [{ interface: "eth0", rx_bytes: 1, tx_bytes: 1, rx_bytes_per_sec: 2048, tx_bytes_per_sec: 512 }],
    ...over,
  };
}

interface Hub {
  agents: Agent[];
  history: Record<string, MetricsPoint[]>;
  agentsStatus: number;
}

function mockHub(init: Partial<Hub> = {}): Hub {
  const hub: Hub = { agents: [agent("local")], history: { local: [point(-10), point(0)] }, agentsStatus: 200, ...init };
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) => {
      const reply = (status: number, data?: unknown) =>
        Promise.resolve({ ok: status < 400, status, json: () => Promise.resolve(data) });
      if (path === "/api/agents") return hub.agentsStatus >= 400 ? reply(hub.agentsStatus, { error: "no" }) : reply(200, hub.agents);
      const m = /^\/api\/agents\/([^/]+)\/metrics/.exec(path);
      if (m) return reply(200, { agent: m[1], points: hub.history[m[1]] ?? [] });
      return reply(404, { error: "not found" });
    }),
  );
  return hub;
}

const noop = () => undefined;
const props = (over: Partial<WidgetProps> = {}): WidgetProps => ({ instanceId: "w", config: {}, setConfig: noop, ...over });

// show renders a widget inside a provider with a fake stream, and lets the
// first HTTP answers arrive.
async function show(ui: React.ReactElement, retryMs = 1000) {
  render(<LiveProvider retryMs={retryMs}>{ui}</LiveProvider>);
  await act(async () => undefined);
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("built-in widgets", () => {
  it("are registered, and the default dashboard has the useful ones", () => {
    const ids = registry.list().map((w) => w.id);
    expect(ids).toEqual(expect.arrayContaining(["system", "cpu", "memory", "network", "disk", "uptime", "agents"]));
    const inDefault = registry.list().filter((w) => w.inDefaultLayout).map((w) => w.id);
    expect(inDefault).toEqual(["system", "cpu", "memory", "network", "disk", "agents"]);
  });

  it("system shows the host and its load", async () => {
    mockHub();
    await show(<SystemWidget {...props()} />);
    expect(screen.getByText("host-local (local)")).toBeInTheDocument();
    expect(screen.getByText("Ubuntu 24.04 amd64")).toBeInTheDocument();
    expect(screen.getByText("16.0 GiB")).toBeInTheDocument();
    expect(screen.getByText("0.50 · 0.40 · 0.30")).toBeInTheDocument();
    expect(screen.getByText("1d 1h")).toBeInTheDocument();
  });

  it("cpu shows usage, load and a chart", async () => {
    mockHub();
    await show(<CpuWidget {...props()} />);
    expect(screen.getByText("42%")).toBeInTheDocument();
    expect(screen.getByText("load 0.50 · 0.40 · 0.30 on 4 cores")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /CPU usage/ })).toBeInTheDocument();
  });

  it("memory shows use and swap", async () => {
    mockHub();
    await show(<MemoryWidget {...props()} />);
    expect(screen.getByText("25%")).toBeInTheDocument();
    expect(screen.getByText("4.0 GiB of 16.0 GiB used · swap 512 MiB of 2.0 GiB")).toBeInTheDocument();
  });

  it("disk shows each mount and warns when nearly full", async () => {
    mockHub();
    await show(<DiskWidget {...props()} />);
    const root = screen.getByRole("meter", { name: "/ used" });
    expect(root).toHaveAttribute("aria-valuenow", "95");
    expect(root).toHaveClass("full");
    expect(screen.getByRole("meter", { name: "/data used" })).toHaveAttribute("aria-valuenow", "25");
    expect(screen.getByText("95.0 GiB of 100 GiB (95%)")).toBeInTheDocument();
  });

  it("network shows the totals and a row per interface", async () => {
    mockHub();
    await show(<NetworkWidget {...props()} />);
    expect(screen.getByText("↓ 2.0 KiB/s")).toBeInTheDocument();
    expect(screen.getByText("↑ 512 B/s")).toBeInTheDocument();
    const row = screen.getByRole("row", { name: /eth0/ });
    expect(within(row).getByText("2.0 KiB/s")).toBeInTheDocument();
  });

  it("network total does not count bridges and container ports twice", async () => {
    const nic = (name: string, rate: number) => ({ interface: name, rx_bytes: 1, tx_bytes: 1, rx_bytes_per_sec: rate, tx_bytes_per_sec: rate });
    // 1 KiB/s really crosses eth0; the same bytes also show on docker0 and the veth port.
    mockHub({ history: { local: [point(0, { networks: [nic("eth0", 1024), nic("docker0", 1024), nic("veth1a2b", 1024)] })] } });
    await show(<NetworkWidget {...props()} />);
    expect(screen.getByText("↓ 1.0 KiB/s")).toBeInTheDocument();
    // The table still lists every interface.
    expect(screen.getByRole("row", { name: /docker0/ })).toBeInTheDocument();
    expect(screen.getByRole("row", { name: /veth1a2b/ })).toBeInTheDocument();
  });

  it("network shows dashes while rates are not known yet", async () => {
    mockHub({
      history: { local: [point(0, { networks: [{ interface: "eth0", rx_bytes: 1, tx_bytes: 1, rx_bytes_per_sec: null, tx_bytes_per_sec: null }] })] },
    });
    await show(<NetworkWidget {...props()} />);
    expect(screen.getAllByText("-")).toHaveLength(2);
    expect(screen.getByText("↓ 0 B/s")).toBeInTheDocument();
  });

  it("uptime shows how long the server has been up", async () => {
    mockHub();
    await show(<UptimeWidget {...props()} />);
    expect(screen.getByText("1d 1h")).toBeInTheDocument();
    expect(screen.getByText(/^since /)).toBeInTheDocument();
  });

  it("waits for the first report when there is none", async () => {
    mockHub({ history: {} });
    await show(<CpuWidget {...props()} />);
    expect(screen.getByText("Waiting for the first report…")).toBeInTheDocument();
  });

  it("says so when no agent is connected", async () => {
    mockHub({ agents: [] });
    await show(<CpuWidget {...props()} />);
    expect(screen.getByText("No agents connected.")).toBeInTheDocument();
  });

  it("shows loading before the hub has answered", () => {
    mockHub();
    render(
      <LiveProvider>
        <CpuWidget {...props()} />
      </LiveProvider>,
    );
    expect(screen.getByText("Loading…")).toBeInTheDocument();
  });
});

describe("choosing the server", () => {
  it("shows a selector with several agents and saves the choice", async () => {
    mockHub({ agents: [agent("local"), agent("web")], history: { local: [point(0)], web: [point(0, { cpu_percent: 77 })] } });
    const setConfig = vi.fn();
    await show(<CpuWidget {...props({ setConfig })} />);
    expect(screen.getByText("42%")).toBeInTheDocument(); // the built-in agent by default

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Server" }), "web");
    expect(setConfig).toHaveBeenCalledWith({ agentId: "web" });
  });

  it("shows the agent saved in the settings", async () => {
    mockHub({ agents: [agent("local"), agent("web")], history: { local: [point(0)], web: [point(0, { cpu_percent: 77 })] } });
    await show(<CpuWidget {...props({ config: { agentId: "web" } })} />);
    expect(screen.getByText("77%")).toBeInTheDocument();
  });

  it("has no selector with a single agent", async () => {
    mockHub();
    await show(<CpuWidget {...props()} />);
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });
});

describe("live updates", () => {
  it("opens the stream and follows its events", async () => {
    mockHub();
    await show(<CpuWidget {...props()} />);
    expect(FakeSocket.last.url).toMatch(/^wss?:\/\/.*\/api\/stream$/);

    act(() => FakeSocket.last.open());
    act(() => FakeSocket.last.send({ type: "agent.metrics", id: "local", point: point(5, { cpu_percent: 88 }) }));
    expect(screen.getByText("88%")).toBeInTheDocument();

    // an event for a report that was already in the history changes nothing
    act(() => FakeSocket.last.send({ type: "agent.metrics", id: "local", point: point(0, { cpu_percent: 1 }) }));
    expect(screen.getByText("88%")).toBeInTheDocument();
  });

  it("shows when the connection to the hub is lost, then reconnects and fills the gap", async () => {
    vi.useFakeTimers();
    mockHub();
    await show(<CpuWidget {...props()} />, 1000);
    act(() => FakeSocket.last.open());
    expect(screen.queryByText(/reconnecting/)).not.toBeInTheDocument();

    act(() => FakeSocket.last.drop());
    expect(screen.getByText(/reconnecting/)).toBeInTheDocument();
    expect(FakeSocket.instances).toHaveLength(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(FakeSocket.instances).toHaveLength(2);
    act(() => FakeSocket.last.open());
    expect(screen.queryByText(/reconnecting/)).not.toBeInTheDocument();
    // the history was asked again, only for what is newer than what we have
    const urls = vi.mocked(fetch).mock.calls.map((c) => String(c[0]));
    expect(urls.some((u) => u.includes("?since="))).toBe(true);
  });

  it("calls an agent stale when it falls silent", async () => {
    vi.useFakeTimers({ now: T0 + 5000 });
    mockHub({ agents: [agent("local")] });
    await show(<CpuWidget {...props()} />);
    act(() => FakeSocket.last.open());
    act(() => FakeSocket.last.send({ type: "snapshot", stale_after_seconds: 10, agents: [agent("local")] }));
    expect(screen.queryByText(/No news from this server/)).not.toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30000);
    });
    expect(screen.getByText(/No news from this server/)).toBeInTheDocument();
  });

  it("goes back to the login when the session ended", async () => {
    mockHub({ agentsStatus: 401 });
    const expired = vi.fn();
    const { SessionContext } = await import("../session");
    render(
      <SessionContext.Provider value={{ user: { username: "a", role: "admin", permissions: ADMIN_PERMISSIONS, created_at: "", disabled: false, two_factor: false }, signOut: () => Promise.resolve(), expired }}>
        <LiveProvider>
          <CpuWidget {...props()} />
        </LiveProvider>
      </SessionContext.Provider>,
    );
    await act(async () => undefined);
    expect(expired).toHaveBeenCalled();
    expect(FakeSocket.instances).toHaveLength(0);
  });

  it("drops a malformed report and keeps drawing; fills in missing lists", async () => {
    mockHub();
    await show(
      <>
        <CpuWidget {...props()} />
        <NetworkWidget {...props()} />
      </>,
    );
    act(() => FakeSocket.last.open());
    // A number is missing, then a string where a number goes, then no time.
    const bad = { ...point(5), cpu_percent: undefined };
    act(() => FakeSocket.last.send({ type: "agent.metrics", id: "local", point: bad }));
    act(() => FakeSocket.last.send({ type: "agent.metrics", id: "local", point: { ...point(6), cpu_percent: "99" } }));
    act(() => FakeSocket.last.send({ type: "agent.metrics", id: "local", point: { ...point(7), at: "yesterday" } }));
    expect(screen.getByText("42%")).toBeInTheDocument();

    // No network list at all (or null, as Go writes an empty one): no crash.
    const noNetworks: Partial<MetricsPoint> = point(8, { cpu_percent: 64 });
    delete noNetworks.networks;
    act(() => FakeSocket.last.send({ type: "agent.metrics", id: "local", point: noNetworks }));
    expect(screen.getByText("64%")).toBeInTheDocument();
    expect(screen.getByText("↓ 0 B/s")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("backs off longer each time the hub accepts and closes at once", async () => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(1); // no jitter, to read the delays
    mockHub();
    await show(<CpuWidget {...props()} />, 100);

    // nextDelay drops the open socket and measures the wait for the next one.
    const nextDelay = async (code?: number, reason?: string) => {
      const before = FakeSocket.instances.length;
      act(() => FakeSocket.last.drop(code, reason));
      let waited = 0;
      while (FakeSocket.instances.length === before && waited < 20000) {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(10);
        });
        waited += 10;
      }
      act(() => FakeSocket.last.open());
      return waited;
    };

    act(() => FakeSocket.last.open());
    const delays = [await nextDelay(), await nextDelay(), await nextDelay(), await nextDelay()];
    expect(delays).toEqual([100, 200, 400, 800]);

    // Once a connection stays up for a while, the next wait is short again.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(STABLE_MS + 10);
    });
    expect(await nextDelay()).toBe(100);

    // "too slow" from the hub: wait at least a few seconds.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(STABLE_MS + 10);
    });
    expect(await nextDelay(1008, "too slow")).toBeGreaterThanOrEqual(5000);
    vi.mocked(Math.random).mockRestore();
  });

  it("retries while the hub cannot be reached", async () => {
    vi.useFakeTimers();
    const hub = mockHub({ agentsStatus: 503 });
    await show(<CpuWidget {...props()} />, 1000);
    expect(screen.getByText("Loading…")).toBeInTheDocument();

    hub.agentsStatus = 200;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(screen.getByText("42%")).toBeInTheDocument();
  });
});

describe("retryDelay", () => {
  it("doubles up to a ceiling and leaves out a random part of up to half", () => {
    expect([0, 1, 2, 3].map((n) => retryDelay(1000, n, () => 1))).toEqual([1000, 2000, 4000, 8000]);
    expect(retryDelay(1000, 10, () => 1)).toBe(15000);
    expect(retryDelay(1000, 2, () => 0)).toBe(2000);
    for (let i = 0; i < 50; i++) {
      const d = retryDelay(1000, 3);
      expect(d).toBeGreaterThanOrEqual(4000);
      expect(d).toBeLessThanOrEqual(8000);
    }
  });
});
