import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, Disk, DiskMove, User } from "./api";
import { LiveProvider } from "./LiveProvider";
import { ServerPage } from "./ServerPage";
import { SessionContext } from "./session";
import { ADMIN_PERMISSIONS, VIEWER_PERMISSIONS } from "./test-users";

const now = new Date().toISOString();
const user = (role: string, permissions: string[]): User => ({ username: role, role, permissions, created_at: now, disabled: false, two_factor: false });
const admin = user("admin", ADMIN_PERMISSIONS);
const viewer = user("viewer", VIEWER_PERMISSIONS);

const server: Agent = {
  id: "web1",
  name: "Web",
  version: "0.1.0",
  status: "offline",
  hostname: "web1.lan",
  connected_at: null,
  last_heartbeat: null,
  metrics_at: null,
  uptime_seconds: null,
};

const disk = (over: Partial<Disk>): Disk => ({
  key: "model-serial:st4000|za1",
  kind: "model-serial",
  identifiable: true,
  conflict: false,
  contested: false,
  agent: "web1",
  device: "sda",
  vendor: "",
  model: "ST4000VN008",
  serial: "ZA1",
  wwn: "",
  transport: "sata",
  size_bytes: 4 * 1024 ** 4,
  virtual: false,
  present: true,
  first_seen: now,
  last_seen: now,
  ...over,
});

const disks: Disk[] = [
  disk({}),
  disk({ key: "slot:web1:vda", kind: "none", identifiable: false, reason: "virtual", device: "vda", model: "QEMU HARDDISK", serial: "", size_bytes: 0 }),
  disk({ key: "wwn:5000c500a1b2c3d4", kind: "wwn", device: "sdb", serial: "ZA2", present: false }),
  disk({ key: "model-serial:st4000|clone", device: "sdc", serial: "CLONE", contested: true, contested_by: "db1" }),
];
const moves: DiskMove[] = [
  { id: 2, at: now, key: "k", model: "ST4000VN008", serial: "ZA9", from: "db1", to: "web1" },
  { id: 1, at: now, key: "k2", model: "ST4000VN008", serial: "ZA8", from: "other1", to: "other2" },
];

function mockHub(seen: string[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) => {
      seen.push(path);
      const reply = (status: number, data?: unknown) => Promise.resolve({ ok: status < 400, status, json: () => Promise.resolve(data) });
      if (path === "/api/agents/web1") return reply(200, server);
      if (path === "/api/agents/web1/disks") return reply(200, { agent: "web1", disks });
      if (path.startsWith("/api/disks/moves")) return reply(200, { moves });
      return reply(404, { error: "not found" });
    }),
  );
}

function renderAs(u: User) {
  return render(
    <SessionContext.Provider value={{ user: u, signOut: () => Promise.resolve(), expired: vi.fn() }}>
      <LiveProvider>
        <ServerPage id="web1" />
      </LiveProvider>
    </SessionContext.Provider>,
  );
}

beforeEach(() => window.localStorage.removeItem("regulus.language"));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("DisksPanel on the page of a server", () => {
  it("shows identity, kind, state, the reason a disk is not identifiable and its moves", async () => {
    mockHub([]);
    renderAs(admin);
    expect(await screen.findByRole("heading", { name: "Physical disks" })).toBeInTheDocument();
    expect(await screen.findByText("model-serial:st4000|za1")).toBeInTheDocument();
    expect(screen.getAllByText("(model and serial)").length).toBeGreaterThan(0);
    expect(screen.getByText("wwn:5000c500a1b2c3d4")).toBeInTheDocument();
    expect(screen.getByText("(WWN)")).toBeInTheDocument();
    expect(screen.getByText("present")).toBeInTheDocument();
    expect(screen.getByText("not reported now")).toBeInTheDocument();
    expect(screen.getByText("not identifiable: a virtual disk (its serial is not the drive's)")).toBeInTheDocument();
    // A disk two connected servers report is flagged as a conflict.
    expect(screen.getByText("Conflict: server db1 reports this disk too")).toBeInTheDocument();
    // Only the moves that involve this server.
    expect(await screen.findByText("ZA9", { exact: false })).toBeInTheDocument();
    expect(screen.queryByText("ZA8", { exact: false })).not.toBeInTheDocument();
  });

  it("is in Italian", async () => {
    window.localStorage.setItem("regulus.language", "it");
    mockHub([]);
    renderAs(admin);
    expect(await screen.findByRole("heading", { name: "Dischi fisici" })).toBeInTheDocument();
    expect(await screen.findByText("Conflitto: anche il server db1 segnala questo disco")).toBeInTheDocument();
    expect(screen.getByText("non identificabile: un disco virtuale (il seriale non è quello del disco)")).toBeInTheDocument();
    expect(screen.getByText("non segnalato ora")).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "Spostamenti di dischi" })).toBeInTheDocument();
  });

  it("is not shown, and not even asked for, without inventory.read", async () => {
    const seen: string[] = [];
    mockHub(seen);
    renderAs(viewer);
    expect(await screen.findByText("Web")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Physical disks" })).not.toBeInTheDocument();
    expect(seen.some((p) => p.includes("disks"))).toBe(false);
  });

  it("keeps the server page when the moves have an unexpected shape (#424)", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    vi.stubGlobal(
      "fetch",
      vi.fn((path: string) => {
        const reply = (status: number, data?: unknown) => Promise.resolve({ ok: status < 400, status, json: () => Promise.resolve(data) });
        if (path === "/api/agents/web1") return reply(200, server);
        if (path === "/api/agents/web1/disks") return reply(200, { agent: "web1", disks });
        // A bare array is not what the hub sends; the panel must not take the page down.
        if (path.startsWith("/api/disks/moves")) return reply(200, moves);
        return reply(404, { error: "not found" });
      }),
    );
    renderAs(admin);
    expect(await screen.findByText(/This part of the page failed|Could not load the disk moves/)).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Web" })).toBeInTheDocument();
  });

  it("says so when the disks cannot be loaded", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((path: string) => {
        const reply = (status: number, data?: unknown) => Promise.resolve({ ok: status < 400, status, json: () => Promise.resolve(data) });
        return path === "/api/agents/web1" ? reply(200, server) : reply(500, { error: "boom" });
      }),
    );
    renderAs(admin);
    expect(await screen.findByText(/Could not load the disks/)).toBeInTheDocument();
  });
});
