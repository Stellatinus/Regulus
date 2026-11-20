import { act, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PluginsStatus, User } from "./api";
import { FakeSocket } from "./fake-socket";
import { LanguageProvider } from "./i18n";
import { LiveProvider } from "./LiveProvider";
import { PluginsPage } from "./PluginsPage";
import { SessionContext } from "./session";
import { VIEWER_PERMISSIONS } from "./test-users";

const viewer: User = { username: "eve", role: "viewer", permissions: VIEWER_PERMISSIONS, created_at: "", disabled: false, two_factor: false };

function mockPlugins(data: PluginsStatus, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve({ ok: status < 400, status, json: () => Promise.resolve(data) })),
  );
}

function renderPage() {
  return render(
    <LanguageProvider>
      <SessionContext.Provider value={{ user: viewer, signOut: () => Promise.resolve(), expired: vi.fn() }}>
        <PluginsPage />
      </SessionContext.Provider>
    </LanguageProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe("PluginsPage", () => {
  it("shows the hub's plugins and those of the servers, with their state", async () => {
    mockPlugins({
      hub: {
        enabled: true,
        plugins: [
          { id: "example-hello", version: "0.1.0", state: "running", restarts: 0 },
          { id: "crasher", version: "1.0.0", state: "failed", restarts: 2, last_error: "exit status 1" },
        ],
      },
      agents: [{ id: "a1", name: "web1", plugins: [{ id: "speedtest", version: "2.0.0", state: "restarting", restarts: 1 }] }],
    });
    renderPage();
    const hub = await screen.findByRole("region", { name: "Hub" });
    expect(within(hub).getByText("example-hello")).toBeInTheDocument();
    expect(within(hub).getByText("running")).toBeInTheDocument();
    expect(within(hub).getByText("failed")).toBeInTheDocument();
    expect(within(hub).getByText("exit status 1")).toBeInTheDocument();
    expect(within(hub).getByText(/not started again until Regulus restarts/)).toBeInTheDocument();
    const servers = screen.getByRole("region", { name: "Servers" });
    expect(within(servers).getByRole("heading", { name: "web1" })).toBeInTheDocument();
    expect(within(servers).getByText("restarting")).toBeInTheDocument();
  });

  it("says how to turn plugins on when the hub runs none", async () => {
    mockPlugins({ hub: { enabled: false, plugins: [] }, agents: [] });
    renderPage();
    expect(await screen.findByText(/set plugins_dir in its configuration/)).toBeInTheDocument();
    expect(screen.getByText("No connected server reports plugins.")).toBeInTheDocument();
  });

  it("shows an unknown state as it is, in Italian too", async () => {
    window.localStorage.setItem("regulus.language", "it");
    mockPlugins({ hub: { enabled: true, plugins: [{ id: "p", version: "1", state: "sleeping", restarts: 0 }] }, agents: [] });
    renderPage();
    expect(await screen.findByText("sleeping")).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Stato" })).toBeInTheDocument();
  });

  it("lists a directory that is not a usable plugin, with its error", async () => {
    mockPlugins({
      hub: {
        enabled: true,
        plugins: [{ id: "broken", version: "", state: "invalid", restarts: 0, last_error: "plugin.yaml: id is required" }],
      },
      agents: [],
    });
    renderPage();
    const hub = await screen.findByRole("region", { name: "Hub" });
    expect(within(hub).getByText("broken")).toBeInTheDocument();
    expect(within(hub).getByText("invalid")).toBeInTheDocument();
    expect(within(hub).getByText("plugin.yaml: id is required")).toBeInTheDocument();
    expect(within(hub).getByText(/not a usable plugin/)).toBeInTheDocument();
  });

  it("reads the plugins again when the stream says they changed, and does not poll", async () => {
    let current: PluginsStatus = {
      hub: { enabled: true, plugins: [{ id: "p", version: "1", state: "starting", restarts: 0 }] },
      agents: [],
    };
    const fetchMock = vi.fn((path: string) => {
      const reply = (data: unknown) => Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(data) });
      if (path === "/api/agents") return reply([]);
      if (path === "/api/plugins") return reply(current);
      return Promise.resolve({ ok: false, status: 404, json: () => Promise.resolve({}) });
    });
    vi.stubGlobal("fetch", fetchMock);
    const pluginCalls = () => fetchMock.mock.calls.filter(([path]) => path === "/api/plugins").length;
    render(
      <LanguageProvider>
        <SessionContext.Provider value={{ user: viewer, signOut: () => Promise.resolve(), expired: vi.fn() }}>
          <LiveProvider>
            <PluginsPage />
          </LiveProvider>
        </SessionContext.Provider>
      </LanguageProvider>,
    );
    expect(await screen.findByText("starting")).toBeInTheDocument();
    await waitFor(() => expect(FakeSocket.last).toBeDefined());
    act(() => FakeSocket.last.open());
    act(() => FakeSocket.last.send({ type: "snapshot", stale_after_seconds: 30, agents: [] }));
    await waitFor(() => expect(pluginCalls()).toBeGreaterThanOrEqual(2));

    // Nothing changes without an event: no request after the snapshot's.
    const settled = pluginCalls();
    await new Promise((r) => setTimeout(r, 400));
    expect(pluginCalls()).toBe(settled);

    current = {
      hub: { enabled: true, plugins: [{ id: "p", version: "1", state: "failed", restarts: 5, last_error: "exit status 7" }] },
      agents: [],
    };
    act(() => FakeSocket.last.send({ type: "plugins.changed", source: "hub" }));
    expect(await screen.findByText("exit status 7")).toBeInTheDocument();
    expect(screen.getByText("failed")).toBeInTheDocument();
  });
});
