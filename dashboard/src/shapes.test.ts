import { describe, expect, it } from "vitest";
import { toAgent, toAgents, toMetricsPoint } from "./shapes";

const point = {
  at: "2026-01-01T00:00:00Z",
  cpu_percent: 1,
  load_1: 0,
  load_5: 0,
  load_15: 0,
  memory_total_bytes: 1,
  memory_used_bytes: 1,
  memory_available_bytes: 0,
  swap_total_bytes: 0,
  swap_used_bytes: 0,
  uptime_seconds: 1,
  disks: [{ mount_point: "/", filesystem: "ext4", total_bytes: 2, used_bytes: 1 }],
  networks: [{ interface: "eth0", rx_bytes: 1, tx_bytes: 1, rx_bytes_per_sec: 3, tx_bytes_per_sec: null }],
};

describe("toMetricsPoint", () => {
  it("keeps a good report as it is", () => {
    expect(toMetricsPoint(point)).toEqual(point);
  });

  it("refuses a report without a valid time or with a missing number", () => {
    for (const bad of [null, 1, "x", [], { ...point, at: "soon" }, { ...point, at: undefined }, { ...point, load_5: "1" }, { ...point, uptime_seconds: NaN }]) {
      expect(toMetricsPoint(bad)).toBeNull();
    }
  });

  it("fills in missing lists and drops bad entries in them", () => {
    expect(toMetricsPoint({ ...point, disks: undefined, networks: null })).toMatchObject({ disks: [], networks: [] });
    const p = toMetricsPoint({ ...point, disks: [point.disks[0], { mount_point: 3 }], networks: [{ interface: "lo" }, 7] });
    expect(p?.disks).toHaveLength(1);
    expect(p?.networks).toEqual([{ interface: "lo", rx_bytes: 0, tx_bytes: 0, rx_bytes_per_sec: null, tx_bytes_per_sec: null }]);
  });

  it("keeps the names of the parts the agent could not read", () => {
    expect(toMetricsPoint({ ...point, unavailable: ["memory", 3, "disks"] })?.unavailable).toEqual(["memory", "disks"]);
    expect(toMetricsPoint({ ...point, unavailable: "memory" })?.unavailable).toBeUndefined();
  });
});

describe("toAgent", () => {
  it("needs an id and gives the widget fields a value of the right type", () => {
    expect(toAgent({ status: "connected" })).toBeNull();
    expect(toAgent({ id: "" })).toBeNull();
    expect(toAgent({ id: "a", hostname: "h", uptime_seconds: "long" })).toEqual({
      id: "a",
      hostname: "h",
      version: "",
      status: "connected",
      connected_at: null,
      last_heartbeat: null,
      metrics_at: null,
      uptime_seconds: null,
    });
    expect(toAgents([{ id: "a" }, null, { id: 2 }]).map((a) => a.id)).toEqual(["a"]);
    expect(toAgents("nope")).toEqual([]);
  });
});
