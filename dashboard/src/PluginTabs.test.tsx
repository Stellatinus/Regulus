import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, PluginUI, User } from "./api";
import { LiveProvider } from "./LiveProvider";
import { ServerPage } from "./ServerPage";
import { SessionContext } from "./session";
import { ADMIN_PERMISSIONS } from "./test-users";

const now = new Date().toISOString();
const admin: User = { username: "admin", role: "admin", permissions: ADMIN_PERMISSIONS, created_at: now, disabled: false, two_factor: false };
const server: Agent = { id: "web1", name: "Web", version: "0.1.0", status: "offline", hostname: "web1.lan", connected_at: null, last_heartbeat: null, metrics_at: null, uptime_seconds: null };

const disksPlugin: PluginUI = {
  id: "disks",
  version: "0.1.0",
  module: "ui/disks.js",
  node_tab: { id: "disks", title: { en: "Disks", it: "Dischi" } },
  permissions: ["disks.read"],
};
const otherPlugin: PluginUI = { id: "backup", version: "1.0.0", module: "ui/b.js", node_tab: { id: "backup", title: { en: "Backups", it: "Backup" } }, permissions: [] };
const noTab: PluginUI = { id: "widgets", version: "1.0.0", module: "ui/w.js", permissions: [] };

function mockHub(plugins: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) => {
      const reply = (status: number, data?: unknown) => Promise.resolve({ ok: status < 400, status, json: () => Promise.resolve(data) });
      if (path === "/api/agents/web1") return reply(200, server);
      if (path === "/api/agents/web1/disks") return reply(200, { agent: "web1", disks: [] });
      if (path.startsWith("/api/disks/moves")) return reply(200, { moves: [] });
      if (path === "/api/plugins/ui") return plugins === "fail" ? reply(500, { error: "boom" }) : reply(200, plugins);
      if (path.startsWith("/api/plugins/")) return reply(200);
      return reply(404, { error: "not found" });
    }),
  );
}

function renderPage() {
  return render(
    <SessionContext.Provider value={{ user: admin, signOut: () => Promise.resolve(), expired: vi.fn() }}>
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

describe("the tabs of plugins in the node window", () => {
  it("puts a plugin's tab after General and before the SSH terminal", async () => {
    mockHub({ plugins: [disksPlugin, otherPlugin, noTab] });
    renderPage();
    const switcher = await screen.findByRole("radiogroup", { name: "What to show of the node" });
    await within(switcher).findByRole("radio", { name: "Disks" });
    const names = within(switcher).getAllByRole("radio").map((r) => r.textContent);
    expect(names).toEqual(["General", "Disks", "Backups", "SSH terminal", "File editor"]);
  });

  it("shows the tab in Italian", async () => {
    window.localStorage.setItem("regulus.language", "it");
    mockHub({ plugins: [disksPlugin] });
    renderPage();
    const switcher = await screen.findByRole("radiogroup");
    expect(await within(switcher).findByRole("radio", { name: "Dischi" })).toBeInTheDocument();
  });

  it("opens the plugin's page in a sandboxed frame for this node", async () => {
    mockHub({ plugins: [disksPlugin] });
    renderPage();
    await userEvent.click(await screen.findByRole("radio", { name: "Disks" }));
    const frame = (await screen.findByTitle("Disks")) as HTMLIFrameElement;
    expect(frame.getAttribute("sandbox")).toBe("allow-scripts");
    expect(frame.getAttribute("src")).toBe("/api/plugins/disks/ui");
  });

  it("hides the core's Physical disks panel only while the Disks plugin is installed", async () => {
    mockHub({ plugins: [disksPlugin] });
    const first = renderPage();
    await within(await screen.findByRole("radiogroup")).findByRole("radio", { name: "Disks" });
    expect(screen.queryByRole("heading", { name: "Physical disks" })).not.toBeInTheDocument();
    first.unmount();

    // Another plugin with a tab does not hide it, and neither does no plugin.
    mockHub({ plugins: [otherPlugin] });
    const second = renderPage();
    expect(await screen.findByRole("heading", { name: "Physical disks" })).toBeInTheDocument();
    second.unmount();

    mockHub({ plugins: [] });
    renderPage();
    expect(await screen.findByRole("heading", { name: "Physical disks" })).toBeInTheDocument();
  });

  it("keeps the window as it was when the list of plugins cannot be read", async () => {
    for (const answer of ["fail", {}, { plugins: null }]) {
      mockHub(answer);
      const view = renderPage();
      expect(await screen.findByRole("heading", { name: "Physical disks" })).toBeInTheDocument();
      const names = within(screen.getByRole("radiogroup")).getAllByRole("radio").map((r) => r.textContent);
      expect(names).toEqual(["General", "SSH terminal", "File editor"]);
      view.unmount();
    }
  });
});
