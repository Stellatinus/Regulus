import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { StoreCatalog, User } from "./api";
import { LanguageProvider } from "./i18n";
import { SessionContext } from "./session";
import { ADMIN_PERMISSIONS, VIEWER_PERMISSIONS } from "./test-users";
import { hasUpdate, offered, StorePage, STORE_INSTALL_PERMISSION } from "./StorePage";

const user = (permissions: string[]): User => ({ username: "eve", role: "admin", permissions, created_at: "", disabled: false, two_factor: false });
const manager = user([...ADMIN_PERMISSIONS, STORE_INSTALL_PERMISSION]);

const catalog: StoreCatalog = {
  sources: [{ name: "regulus-plugins", trust: "official", state: "ok", issued_at: "2026-11-26T09:00:00Z", expires: "2026-12-17T09:00:00Z" }],
  plugins: [
    {
      id: "disks",
      name: "Disks",
      description: "Physical disks of each server.",
      source: "regulus-plugins",
      kind: "feature",
      audience: "admin",
      installed: null,
      latest: "0.2.0",
      versions: [
        { version: "0.2.0", channel: "beta", compatible: false, incompatible_reason: "needs Regulus >=0.5.0", yanked: false },
        { version: "0.1.0", channel: "stable", compatible: true, yanked: false, permissions: [{ id: "disks.read", description: "See the disks." }], provides: [], needs: [{ capability: "inventory.read@1", optional: true }] },
      ],
    },
    {
      id: "hello-world",
      name: "Hello world",
      source: "regulus-plugins",
      kind: "integration",
      installed: { version: "0.1.0" },
      latest: "0.2.0",
      versions: [
        { version: "0.2.0", channel: "stable", compatible: true, yanked: false },
        { version: "0.1.0", channel: "stable", compatible: true, yanked: false },
      ],
    },
  ],
};

function mock(routes: Record<string, { status?: number; body: unknown }>) {
  const fn = vi.fn((url: string, init?: RequestInit) => {
    const r = routes[`${init?.method ?? "GET"} ${url}`];
    const status = r?.status ?? 200;
    return Promise.resolve({ ok: status < 400, status, headers: new Headers(), json: () => Promise.resolve(r?.body) });
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

function renderPage(u: User = manager) {
  return render(
    <LanguageProvider>
      <SessionContext.Provider value={{ user: u, signOut: () => Promise.resolve(), expired: vi.fn() }}>
        <StorePage />
      </SessionContext.Provider>
    </LanguageProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe("StorePage", () => {
  it("lists what the hub returns, and nothing else", async () => {
    mock({ "GET /api/store": { body: catalog } });
    renderPage();
    expect(await screen.findByRole("heading", { name: "Disks" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Hello world" })).toBeInTheDocument();
    expect(screen.getByText("Not installed")).toBeInTheDocument();
    expect(screen.getByText(/Installed 0\.1\.0/)).toBeInTheDocument();
    expect(screen.getByText(/Update to 0\.2\.0/)).toBeInTheDocument();
    // The newest version of Disks cannot run here, so the offered one is 0.1.0.
    expect(screen.getByText(/Version 0\.1\.0 · from regulus-plugins/)).toBeInTheDocument();
  });

  it("searches and filters", async () => {
    const u = userEvent.setup();
    mock({ "GET /api/store": { body: catalog } });
    renderPage();
    await screen.findByRole("heading", { name: "Disks" });
    await u.type(screen.getByRole("searchbox", { name: "Search plugins" }), "hello");
    expect(screen.queryByRole("heading", { name: "Disks" })).not.toBeInTheDocument();
    await u.clear(screen.getByRole("searchbox"));
    await u.click(screen.getByRole("button", { name: "Updates" }));
    expect(screen.queryByRole("heading", { name: "Disks" })).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Hello world" })).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "All" }));
    await u.selectOptions(screen.getByRole("combobox"), "feature");
    expect(screen.queryByRole("heading", { name: "Hello world" })).not.toBeInTheDocument();
    await u.selectOptions(screen.getByRole("combobox"), "other");
    expect(screen.getByText("No plugin matches the search and filters.")).toBeInTheDocument();
  });

  it("shows permissions before installing, and greys out a version that cannot run", async () => {
    const u = userEvent.setup();
    mock({ "GET /api/store": { body: catalog } });
    renderPage();
    await u.click(await screen.findByRole("button", { name: "Details: Disks" }));
    const dialog = await screen.findByRole("dialog", { hidden: true });
    expect(within(dialog).getByText("disks.read")).toBeInTheDocument();
    expect(within(dialog).getByText("inventory.read@1 (optional)")).toBeInTheDocument();
    const bad = within(dialog).getByText("0.2.0").closest("li");
    expect(bad).toHaveAttribute("aria-disabled", "true");
    expect(bad).toHaveTextContent("Cannot run here: needs Regulus >=0.5.0");
  });

  it("shows the hub's refusal to install, never a success", async () => {
    const u = userEvent.setup();
    const fn = mock({
      "GET /api/store": { body: catalog },
      "POST /api/store/plugins/disks/install": { status: 501, body: { error: "installation needs the root helper, not built yet" } },
    });
    renderPage();
    await u.click(await screen.findByRole("button", { name: "Details: Disks" }));
    await u.click(await screen.findByRole("button", { name: "Install 0.1.0", hidden: true }));
    expect(await screen.findByRole("alert", { hidden: true })).toHaveTextContent("Not installed: installation needs the root helper, not built yet");
    expect(fn).toHaveBeenCalledWith("/api/store/plugins/disks/install", expect.objectContaining({ method: "POST" }));
  });

  it("does not let a role without the permission install", async () => {
    const u = userEvent.setup();
    mock({ "GET /api/store": { body: catalog } });
    renderPage(user(VIEWER_PERMISSIONS));
    expect(await screen.findByText(/needs the plugins\.manage permission/)).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Details: Disks" }));
    expect(await screen.findByRole("button", { name: "Install 0.1.0", hidden: true })).toBeDisabled();
  });

  it("says plainly when an index is expired or not usable", async () => {
    mock({
      "GET /api/store": {
        body: {
          sources: [
            { name: "official", trust: "official", state: "expired", issued_at: "2026-01-01T00:00:00Z" },
            { name: "mine", trust: "custom", state: "error", error: "signature does not match" },
          ],
          plugins: [],
        },
      },
    });
    renderPage();
    expect(await screen.findByText("Index expired")).toBeInTheDocument();
    expect(screen.getByText(/signature does not match/)).toBeInTheDocument();
    expect(screen.getByText("No plugin is offered")).toBeInTheDocument();
  });

  it("has an empty state with no source, and shows a read failure", async () => {
    mock({ "GET /api/store": { body: { sources: [], plugins: [] } } });
    const first = renderPage();
    expect(await screen.findByText("No source is configured")).toBeInTheDocument();
    first.unmount();
    mock({ "GET /api/store": { status: 500, body: { error: "boom" } } });
    renderPage();
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("The store could not be read: boom"));
  });
});

describe("offered", () => {
  it("skips yanked and incompatible versions", () => {
    const p = { ...catalog.plugins[0]!, latest: "0.2.0" };
    expect(offered(p)?.version).toBe("0.1.0");
    expect(hasUpdate(p)).toBe(false);
    expect(offered({ ...p, versions: [{ ...p.versions[1]!, yanked: true }] })).toBeUndefined();
  });
});
