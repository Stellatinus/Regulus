import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, User } from "./api";
import { LiveProvider } from "./LiveProvider";
import { Shell } from "./Shell";
import { ServersPage } from "./ServersPage";
import { SessionContext } from "./session";
import { ADMIN_PERMISSIONS } from "./test-users";
import { naturalType } from "./nodeTypes";

const now = new Date().toISOString();
const admin: User = { username: "ana", role: "admin", permissions: ADMIN_PERMISSIONS, created_at: now, disabled: false, two_factor: false };

const base: Agent = {
  id: "web1",
  name: "Web",
  version: "0.1.0",
  status: "connected",
  hostname: "web1.lan",
  os: "linux",
  os_version: "Ubuntu 24.04",
  arch: "amd64",
  connected_at: now,
  last_heartbeat: now,
  metrics_at: now,
  uptime_seconds: 3700,
};
const hub: Agent = { ...base, id: "local", name: "Hub", role: "hub", hostname: "hub.lan" };
const db: Agent = { ...base, id: "db1", name: "Database", hostname: "db1.lan", status: "offline", last_heartbeat: null, uptime_seconds: null };
const behind: Agent = { ...base, id: "old", name: "Behind", hostname: "old.lan", compatibility: "behind" };

type Handler = (body?: unknown) => { status?: number; data?: unknown };

function mockHub(agents: Agent[], extra: Record<string, Handler> = {}) {
  const calls: { method: string; path: string; body?: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(init.body as string) : undefined;
      calls.push({ method, path, body });
      const handlers: Record<string, Handler> = {
        "GET /api/agents?all=true": () => ({ data: agents }),
        "GET /api/agents/listener": () => ({ data: { enabled: true, editable: true } }),
        "GET /api/layout": () => ({ data: { layout: null, updated_at: null } }),
        ...Object.fromEntries(agents.map((a) => [`GET /api/agents/${a.id}`, () => ({ data: a })])),
        ...extra,
      };
      const { status = 200, data } = handlers[`${method} ${path}`]?.(body) ?? { status: 404, data: { error: "not found" } };
      return Promise.resolve({ ok: status < 400, status, json: () => Promise.resolve(data) });
    }),
  );
  return calls;
}

function renderPage(page: React.ReactNode, user: User = admin) {
  return render(
    <SessionContext.Provider value={{ user, signOut: () => Promise.resolve(), expired: vi.fn() }}>
      <LiveProvider>{page}</LiveProvider>
    </SessionContext.Provider>,
  );
}

beforeEach(() => {
  window.localStorage.clear();
  window.location.hash = "#/servers";
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.location.hash = "";
});

describe("the Nodes page", () => {
  it("searches by name, host and type, and says how many nodes are shown", async () => {
    mockHub([hub, base, db]);
    const user = userEvent.setup();
    renderPage(<ServersPage />);

    await screen.findByRole("link", { name: "Open the node Web" });
    await user.type(screen.getByRole("searchbox"), "db1");
    expect(screen.getByRole("status", { name: "1 of 3 nodes shown" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Open the node Web" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open the node Database" })).toBeInTheDocument();

    await user.clear(screen.getByRole("searchbox"));
    await user.type(screen.getByRole("searchbox"), "primary hub");
    expect(screen.getByRole("link", { name: "Open the node Hub" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Open the node Web" })).not.toBeInTheDocument();

    await user.clear(screen.getByRole("searchbox"));
    await user.type(screen.getByRole("searchbox"), "nothing like this");
    expect(await screen.findByText("No node found")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Reset the filters" }));
    expect(screen.getAllByRole("link", { name: /Open the node/ })).toHaveLength(3);
  });

  it("filters by state, by update and by type from the filter popover", async () => {
    mockHub([hub, base, db, behind]);
    const user = userEvent.setup();
    renderPage(<ServersPage />);
    await screen.findByRole("link", { name: "Open the node Web" });

    // The popover is closed, so its chips are not on the page yet.
    expect(screen.queryByRole("button", { name: "Update available" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Filter/ }));
    await user.click(screen.getByRole("button", { name: "offline" }));
    expect(screen.getAllByRole("link", { name: /Open the node/ })).toHaveLength(1);
    expect(screen.getByRole("link", { name: "Open the node Database" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Update available" }));
    expect(screen.getByRole("link", { name: "Open the node Behind" })).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: /Open the node/ })).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "All" }));
    await user.click(screen.getByRole("button", { name: /Primary hub/ }));
    expect(screen.getAllByRole("link", { name: /Open the node/ })).toHaveLength(1);
    expect(screen.getByRole("link", { name: "Open the node Hub" })).toBeInTheDocument();
    // Two filters on at once: the button counts them.
    await user.click(screen.getByRole("button", { name: "offline" }));
    expect(await screen.findByText("No node found")).toBeInTheDocument();
  });

  it("switches between the grid, the list and the map, and remembers the choice", async () => {
    mockHub([hub, base, db]);
    const user = userEvent.setup();
    const first = renderPage(<ServersPage />);
    await screen.findByRole("link", { name: "Open the node Web" });
    expect(document.querySelector(".servers-grid")).not.toBeNull();

    await user.click(screen.getByRole("button", { name: "Compact view (list)" }));
    expect(document.querySelector(".servers-list")).not.toBeNull();
    expect(document.querySelector(".servers-grid")).toBeNull();

    await user.click(screen.getByRole("button", { name: "Topology view (map of the mesh)" }));
    expect(document.querySelector(".topology-wrapper")).not.toBeNull();
    // Every node is on the map and opens the same window.
    expect(screen.getAllByRole("link", { name: /Open the node/ })).toHaveLength(3);
    first.unmount();

    renderPage(<ServersPage />);
    await screen.findByRole("link", { name: "Open the node Web" });
    expect(document.querySelector(".topology-wrapper")).not.toBeNull();
    expect(screen.getByRole("button", { name: "Topology view (map of the mesh)" })).toHaveAttribute("aria-pressed", "true");
  });

  it("opens a node in a window over the page, with the three parts of the node", async () => {
    mockHub([hub, base]);
    window.location.hash = "#/servers/web1";
    const user = userEvent.setup();
    renderPage(<ServersPage />);

    const dialog = await screen.findByRole("dialog", { name: /Web/ });
    // The list stays behind the window.
    expect(screen.getByRole("link", { name: "Open the node Hub" })).toBeInTheDocument();
    const parts = within(dialog).getByRole("radiogroup", { name: "What to show of the node" });
    expect(within(parts).getByRole("radio", { name: "General" })).toBeChecked();

    await user.click(within(parts).getByRole("radio", { name: "SSH terminal" }));
    expect(within(parts).getByRole("radio", { name: "SSH terminal" })).toBeChecked();
    expect(within(dialog).getByText(/will open here/)).toBeInTheDocument();
    expect(within(dialog).queryByRole("region", { name: "CPU" })).not.toBeInTheDocument();

    await user.click(within(parts).getByRole("radio", { name: "File editor" }));
    expect(within(dialog).getByText(/file manager and editor/)).toBeInTheDocument();

    await user.click(within(parts).getByRole("radio", { name: "General" }));
    expect(within(dialog).getByRole("region", { name: "CPU" })).toBeInTheDocument();
  });

  it("closes the node window with the button, with Escape and with a click outside", async () => {
    mockHub([hub, base]);
    const user = userEvent.setup();
    for (const how of ["button", "escape", "outside"]) {
      window.location.hash = "#/servers/web1";
      const view = renderPage(<ServersPage />);
      await screen.findByRole("dialog", { name: /Web/ });
      if (how === "button") await user.click(screen.getByRole("button", { name: "Close the node window" }));
      else if (how === "escape") await user.keyboard("{Escape}");
      else await user.click(document.querySelector(".node-backdrop") as HTMLElement);
      await waitFor(() => expect(window.location.hash).toBe("#/servers"));
      view.unmount();
    }
  });

  it("edits the name on the hub and the type in this browser", async () => {
    const calls = mockHub([hub, base], { "PATCH /api/agents/web1": () => ({ status: 204 }) });
    window.location.hash = "#/servers/web1";
    const user = userEvent.setup();
    renderPage(<ServersPage />);

    await screen.findByRole("dialog", { name: /Web/ });
    await user.click(screen.getByRole("button", { name: "Edit the node" }));
    const edit = screen.getByRole("dialog", { name: "Edit Web" });
    const name = within(edit).getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "Frontend");
    await user.click(within(edit).getByRole("button", { name: /Storage/ }));
    await user.click(within(edit).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ name: "Frontend" }));
    expect(JSON.parse(window.localStorage.getItem("regulus.nodeTypes") ?? "{}")).toEqual({ web1: "storage" });
    // The window shows the new type, and the list behind it too.
    expect(await within(screen.getByRole("dialog", { name: /Web/ })).findByText("Storage / NAS")).toBeInTheDocument();
  });

  it("does not let the type of the hub change, nor the name of the built-in server", async () => {
    mockHub([hub, base]);
    window.location.hash = "#/servers/local";
    const user = userEvent.setup();
    renderPage(<ServersPage />);
    await screen.findByRole("dialog", { name: /Hub/ });
    await user.click(screen.getByRole("button", { name: "Edit the node" }));
    const edit = screen.getByRole("dialog", { name: "Edit Hub" });
    expect(within(edit).getByLabelText("Name")).toBeDisabled();
    expect(within(edit).getByText("The hub keeps its own type.")).toBeInTheDocument();
    expect(within(edit).queryByRole("button", { name: /Storage/ })).not.toBeInTheDocument();
  });
});

describe("naturalType", () => {
  it("gives the hub its look, and every other server a generic one", () => {
    expect(naturalType({ id: "local" })).toBe("hub");
    expect(naturalType({ id: "x", role: "hub" })).toBe("hub");
    expect(naturalType({ id: "x", role: "hub-redundant" })).toBe("hub-backup");
    expect(naturalType({ id: "x", role: "agent" })).toBe("generic");
    expect(naturalType({ id: "x", role: "something new" })).toBe("generic");
  });
});

describe("the shell", () => {
  function renderShell(user: User = admin) {
    return render(
      <SessionContext.Provider value={{ user, signOut: () => Promise.resolve(), expired: vi.fn() }}>
        <LiveProvider>
          <Shell user={user} signOut={() => Promise.resolve()} expired={vi.fn()} />
        </LiveProvider>
      </SessionContext.Provider>,
    );
  }

  it("has the notch on top with the count of online servers and the settings cog, and the dock below", async () => {
    mockHub([hub, base, db]);
    renderShell();
    expect(await screen.findByText("2/3 Online")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Settings" })).toHaveAttribute("href", "#/settings");
    const dock = screen.getByRole("navigation", { name: "Main" });
    expect(within(dock).getAllByRole("link").map((l) => l.textContent)).toEqual(["Dashboard", "Nodes", "Services", "Admin"]);
    expect(within(dock).getByRole("link", { name: "Nodes" })).toHaveAttribute("aria-current", "page");
  });

  it("shows only the dock sections the user may use", async () => {
    mockHub([hub]);
    renderShell({ ...admin, role: "viewer", permissions: ["servers.read"] });
    const dock = await screen.findByRole("navigation", { name: "Main" });
    expect(within(dock).queryByRole("link", { name: "Admin" })).not.toBeInTheDocument();
    mockHub([hub]);
    renderShell({ ...admin, role: "none", permissions: [] });
    expect(screen.getAllByRole("navigation", { name: "Main" }).at(-1)).not.toHaveTextContent("Services");
  });

  it("shows Automations in the dock only after the setting is switched on", async () => {
    mockHub([hub]);
    const user = userEvent.setup();
    window.location.hash = "#/settings";
    renderShell();
    const dock = await screen.findByRole("navigation", { name: "Main" });
    expect(within(dock).queryByRole("link", { name: "Automations" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("switch", { name: "Automations" }));
    expect(within(dock).getByRole("link", { name: "Automations" })).toHaveAttribute("href", "#/automations");
    expect(window.localStorage.getItem("regulus.feature.automations")).toBe("on");

    await user.click(within(dock).getByRole("link", { name: "Automations" }));
    expect(await screen.findByRole("heading", { name: "Automations" })).toBeInTheDocument();

    window.location.hash = "#/settings";
    await user.click(await screen.findByRole("switch", { name: "Automations" }));
    expect(within(dock).queryByRole("link", { name: "Automations" })).not.toBeInTheDocument();
  });

  it("offers the pages of Admin as tabs", async () => {
    mockHub([hub], { "GET /api/users": () => ({ data: [] }), "GET /api/roles": () => ({ data: { roles: [], permissions: [] } }) });
    window.location.hash = "#/updates";
    renderShell();
    const tabs = await screen.findByRole("navigation", { name: "Admin", hidden: false });
    expect(within(tabs).getByRole("link", { name: "Updates" })).toHaveAttribute("aria-current", "page");
    expect(within(tabs).getByRole("link", { name: "Users" })).toHaveAttribute("href", "#/users");
    // The Admin item of the dock stays lit on both pages.
    expect(within(screen.getByRole("navigation", { name: "Main" })).getByRole("link", { name: "Admin" })).toHaveAttribute("aria-current", "page");
  });

  it("opens the account menu with the account page and sign out", async () => {
    mockHub([hub]);
    const user = userEvent.setup();
    renderShell();
    await user.click(await screen.findByRole("button", { name: /Account: ana/ }));
    expect(screen.getByRole("menuitem", { name: "Account" })).toHaveAttribute("href", "#/account");
    expect(screen.getByRole("menuitem", { name: "Sign out" })).toBeInTheDocument();
  });
});
