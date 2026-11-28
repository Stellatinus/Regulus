import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, PLUGINS_CHANGED_EVENT, setReauthHandler, type StoreCatalog, type User } from "./api";
import { LanguageProvider } from "./i18n";
import { SessionContext } from "./session";
import { ADMIN_PERMISSIONS, VIEWER_PERMISSIONS } from "./test-users";
import { hasUpdate, offered, StorePage, STORE_INSTALL_PERMISSION, storeErrorText } from "./StorePage";

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
    // The All / Installed / Updates selector is gone (#534): one switch instead.
    for (const name of ["All", "Installed", "Updates"]) expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    await u.click(screen.getByRole("switch", { name: /Downloaded/ }));
    expect(screen.queryByRole("heading", { name: "Disks" })).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Hello world" })).toBeInTheDocument();
    await u.click(screen.getByRole("switch", { name: /Downloaded/ }));
    expect(screen.getByRole("heading", { name: "Disks" })).toBeInTheDocument();
    await u.selectOptions(screen.getByRole("combobox"), "feature");
    expect(screen.queryByRole("heading", { name: "Hello world" })).not.toBeInTheDocument();
    await u.selectOptions(screen.getByRole("combobox"), "other");
    expect(screen.getByText("No plugin matches the search and filters.")).toBeInTheDocument();
  });

  it("has a Downloaded switch: off is the whole catalogue, on only what is installed, and it is remembered (#534)", async () => {
    const u = userEvent.setup();
    mock({ "GET /api/store": { body: catalog } });
    const first = renderPage();
    const sw = await screen.findByRole("switch", { name: /Downloaded/ });
    expect(sw).not.toBeChecked();
    expect(screen.getByRole("heading", { name: "Disks" })).toBeInTheDocument();
    await u.click(sw);
    expect(screen.queryByRole("heading", { name: "Disks" })).not.toBeInTheDocument();
    expect(window.localStorage.getItem("regulus.store.downloaded")).toBe("on");
    first.unmount();
    // A new visit starts as it was left.
    mock({ "GET /api/store": { body: catalog } });
    renderPage();
    expect(await screen.findByRole("switch", { name: /Downloaded/ })).toBeChecked();
    expect(screen.queryByRole("heading", { name: "Disks" })).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Hello world" })).toBeInTheDocument();
  });

  it("keeps updates visible without the Updates view: a count, updatable plugins first, and Update on the card (#534)", async () => {
    mock({ "GET /api/store": { body: { ...catalog, plugins: [catalog.plugins[0], catalog.plugins[1]] } } });
    renderPage();
    await screen.findByRole("heading", { name: "Disks" });
    expect(screen.getByText("1 to update")).toBeInTheDocument();
    const titles = screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(titles.indexOf("Hello world")).toBeLessThan(titles.indexOf("Disks"));
    expect(screen.getByRole("button", { name: "Update: Hello world" })).toBeInTheDocument();
  });

  it("says nothing is downloaded when the switch is on and no plugin is installed (#534)", async () => {
    const u = userEvent.setup();
    mock({ "GET /api/store": { body: { ...catalog, plugins: [catalog.plugins[0]] } } });
    renderPage();
    await u.click(await screen.findByRole("switch", { name: /Downloaded/ }));
    expect(screen.getByText("Nothing downloaded yet.")).toBeInTheDocument();
  });

  it("has [Install] [i] on a card, Update or Remove for an installed plugin, and no Details button (#532)", async () => {
    mock({ "GET /api/store": { body: catalog } });
    renderPage();
    await screen.findByRole("heading", { name: "Disks" });
    expect(screen.queryByRole("button", { name: /^Details/ })).not.toBeInTheDocument();
    const disks = screen.getByRole("heading", { name: "Disks" }).closest("li") as HTMLElement;
    const buttons = within(disks).getAllByRole("button");
    expect(buttons.map((b) => b.textContent)).toEqual(["Install", "i"]);
    const info = within(disks).getByRole("button", { name: "Information about Disks" });
    expect(info).toHaveAttribute("title", "Information about Disks");
    // An installed plugin with a newer version shows Update where Install was.
    const hello = screen.getByRole("heading", { name: "Hello world" }).closest("li") as HTMLElement;
    expect(within(hello).getByRole("button", { name: "Update: Hello world" })).toBeInTheDocument();
    expect(within(hello).queryByRole("button", { name: /^Install/ })).not.toBeInTheDocument();
  });

  it("installs from the card after showing the permissions and needs it would have shown", async () => {
    const u = userEvent.setup();
    const fn = mock({
      "GET /api/store": { body: catalog },
      "POST /api/store/plugins/disks/install": { body: { plugin: "disks", action: "install", version: "0.1.0", source: "regulus-plugins" } },
    });
    renderPage();
    await u.click(await screen.findByRole("button", { name: "Install: Disks" }));
    const confirm = await screen.findByRole("alertdialog", { hidden: true });
    expect(confirm).toHaveTextContent("Install Disks 0.1.0?");
    expect(within(confirm).getByText("disks.read")).toBeInTheDocument();
    expect(within(confirm).getByText("inventory.read@1 (optional)")).toBeInTheDocument();
    expect(fn.mock.calls.some(([url]) => url === "/api/store/plugins/disks/install")).toBe(false);
    await u.click(within(confirm).getByRole("button", { name: "Install Disks", hidden: true }));
    expect(await screen.findByText("Disks 0.1.0 was installed.", undefined, { timeout: 2000 })).toBeInTheDocument();
    expect(fn.mock.calls.find(([url]) => url === "/api/store/plugins/disks/install")?.[1]).toMatchObject({ method: "POST", body: JSON.stringify({ version: "0.1.0", source: "regulus-plugins" }) });
  });

  it("installs from the card at once when the version asks for nothing", async () => {
    const u = userEvent.setup();
    const plain: StoreCatalog = {
      ...catalog,
      plugins: [{ id: "plain", name: "Plain", source: "regulus-plugins", installed: null, latest: "1.0.0", versions: [{ version: "1.0.0", channel: "stable", compatible: true, yanked: false }] }],
    };
    const fn = mock({
      "GET /api/store": { body: plain },
      "POST /api/store/plugins/plain/install": { body: { plugin: "plain", action: "install", version: "1.0.0" } },
    });
    renderPage();
    await u.click(await screen.findByRole("button", { name: "Install: Plain" }));
    expect(await screen.findByText("Plain 1.0.0 was installed.", undefined, { timeout: 2000 })).toBeInTheDocument();
    expect(screen.queryByRole("alertdialog", { hidden: true })).not.toBeInTheDocument();
    expect(fn.mock.calls.some(([url]) => url === "/api/store/plugins/plain/install")).toBe(true);
  });

  it("asks before removing from the card, and shows Remove when there is no update", async () => {
    const u = userEvent.setup();
    const current: StoreCatalog = {
      ...catalog,
      plugins: [{ ...catalog.plugins[1], installed: { version: "0.2.0" } }],
    };
    const fn = mock({
      "GET /api/store": { body: current },
      "DELETE /api/store/plugins/hello-world": { body: { plugin: "hello-world", action: "remove", from: "0.2.0" } },
    });
    renderPage();
    await u.click(await screen.findByRole("button", { name: "Remove: Hello world" }));
    const confirm = await screen.findByRole("alertdialog", { hidden: true });
    expect(confirm).toHaveTextContent("Remove Hello world?");
    expect(fn.mock.calls.some(([, init]) => init?.method === "DELETE")).toBe(false);
    await u.click(within(confirm).getByRole("button", { name: "Remove Hello world", hidden: true }));
    expect(await screen.findByText("Hello world was removed.")).toBeInTheDocument();
  });

  it("shows permissions before installing, and greys out a version that cannot run", async () => {
    const u = userEvent.setup();
    mock({ "GET /api/store": { body: catalog } });
    renderPage();
    await u.click(await screen.findByRole("button", { name: "Information about Disks" }));
    const dialog = await screen.findByRole("dialog", { hidden: true });
    expect(within(dialog).getByText("disks.read")).toBeInTheDocument();
    expect(within(dialog).getByText("inventory.read@1 (optional)")).toBeInTheDocument();
    const bad = within(dialog).getByText("0.2.0").closest("li");
    expect(bad).toHaveAttribute("aria-disabled", "true");
    expect(bad).toHaveTextContent("Cannot run here: needs Regulus >=0.5.0");
  });

  const detailOf = async (u: ReturnType<typeof userEvent.setup>, name: string) => {
    await u.click(await screen.findByRole("button", { name: `Information about ${name}` }));
    return screen.findByRole("dialog", { hidden: true });
  };

  it("installs, reads the catalog and the plugin tabs again, and says so", async () => {
    const u = userEvent.setup();
    const changed = vi.fn();
    window.addEventListener(PLUGINS_CHANGED_EVENT, changed);
    const fn = mock({
      "GET /api/store": { body: catalog },
      "POST /api/store/plugins/disks/install": { body: { plugin: "disks", action: "install", version: "0.1.0", source: "regulus-plugins" } },
    });
    renderPage();
    await detailOf(u, "Disks");
    await u.click(await screen.findByRole("button", { name: "Install 0.1.0", hidden: true }));
    expect(await screen.findByText("Disks 0.1.0 was installed.", undefined, { timeout: 2000 })).toBeInTheDocument();
    const call = fn.mock.calls.find(([url]) => url === "/api/store/plugins/disks/install");
    expect(call?.[1]).toMatchObject({ method: "POST", body: JSON.stringify({ version: "0.1.0", source: "regulus-plugins" }) });
    expect(changed).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(fn.mock.calls.filter(([url, init]) => url === "/api/store" && (init?.method ?? "GET") === "GET")).toHaveLength(2));
    window.removeEventListener(PLUGINS_CHANGED_EVENT, changed);
  });

  it("updates an installed plugin to the offered version", async () => {
    const u = userEvent.setup();
    const fn = mock({
      "GET /api/store": { body: catalog },
      "POST /api/store/plugins/hello-world/update": { body: { plugin: "hello-world", action: "update", version: "0.2.0", from: "0.1.0" } },
    });
    renderPage();
    await detailOf(u, "Hello world");
    expect(screen.queryByRole("button", { name: /^Install \d/, hidden: true })).not.toBeInTheDocument();
    await u.click(await screen.findByRole("button", { name: "Update to 0.2.0", hidden: true }));
    expect(await screen.findByText("Hello world was updated to 0.2.0.")).toBeInTheDocument();
    expect(fn.mock.calls.find(([url]) => url === "/api/store/plugins/hello-world/update")?.[1]).toMatchObject({ method: "POST", body: JSON.stringify({ version: "0.2.0" }) });
  });

  it("asks before removing, names the plugin, and removes", async () => {
    const u = userEvent.setup();
    const changed = vi.fn();
    window.addEventListener(PLUGINS_CHANGED_EVENT, changed);
    const fn = mock({
      "GET /api/store": { body: catalog },
      "DELETE /api/store/plugins/hello-world": { body: { plugin: "hello-world", action: "remove", from: "0.1.0" } },
    });
    renderPage();
    await detailOf(u, "Hello world");
    await u.click(screen.getByRole("button", { name: "Remove", hidden: true }));
    const confirm = await screen.findByRole("alertdialog", { hidden: true });
    expect(confirm).toHaveTextContent("Remove Hello world?");
    expect(confirm).toHaveTextContent("The tabs and screens it adds to the dashboard disappear.");
    expect(fn.mock.calls.some(([, init]) => init?.method === "DELETE")).toBe(false);
    await u.click(within(confirm).getByRole("button", { name: "Cancel", hidden: true }));
    expect(screen.queryByRole("alertdialog", { hidden: true })).not.toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Remove", hidden: true }));
    await u.click(await screen.findByRole("button", { name: "Remove Hello world", hidden: true }));
    expect(await screen.findByText("Hello world was removed.")).toBeInTheDocument();
    expect(changed).toHaveBeenCalledTimes(1);
    window.removeEventListener(PLUGINS_CHANGED_EVENT, changed);
  });

  it("keeps the busy state, with every action off, until the hub answers", async () => {
    const u = userEvent.setup();
    let finish: (v: unknown) => void = () => undefined;
    const fn = vi.fn((url: string, init?: RequestInit) => {
      if (url === "/api/store/plugins/disks/install")
        return new Promise((resolve) => {
          finish = () => resolve({ ok: true, status: 200, headers: new Headers(), json: () => Promise.resolve({ plugin: "disks", action: "install", version: "0.1.0" }) });
        });
      return Promise.resolve({ ok: true, status: 200, headers: new Headers(), json: () => Promise.resolve(catalog), method: init?.method });
    });
    vi.stubGlobal("fetch", fn);
    renderPage();
    await detailOf(u, "Disks");
    await u.click(await screen.findByRole("button", { name: "Install 0.1.0", hidden: true }));
    const busy = await screen.findByRole("status", { hidden: true });
    expect(busy).toHaveTextContent("Installing Disks… The hub downloads and checks the package");
    expect(busy).toHaveTextContent("up to two minutes");
    expect(screen.getByRole("button", { name: "Installing…", hidden: true })).toBeDisabled();
    // Closing the dialog does not forget it.
    await u.click(screen.getByRole("button", { name: "Close", hidden: true }));
    expect(await screen.findByRole("status")).toHaveTextContent("Installing Disks…");
    finish(undefined);
    expect(await screen.findByText("Disks 0.1.0 was installed.")).toBeInTheDocument();
  });

  it("turns the store's refusals into texts of the page, never a success", async () => {
    const u = userEvent.setup();
    mock({
      "GET /api/store": { body: catalog },
      "POST /api/store/plugins/disks/install": { status: 409, body: { error: "english", code: "busy" } },
    });
    renderPage();
    await detailOf(u, "Disks");
    await u.click(await screen.findByRole("button", { name: "Install 0.1.0", hidden: true }));
    expect(await screen.findByRole("alert", { hidden: true })).toHaveTextContent(
      "Not installed: Another plugin change is already in progress. Try again when it ends.",
    );
    expect(screen.queryByText(/was installed/)).not.toBeInTheDocument();
  });

  it("shows the helper's own reason, in Italian too", async () => {
    window.localStorage.setItem("regulus.language", "it");
    const u = userEvent.setup();
    mock({
      "GET /api/store": { body: catalog },
      "POST /api/store/plugins/disks/install": { status: 422, body: { error: "english", code: "helper_failed", args: { helper_code: "checksum" } } },
    });
    renderPage();
    await u.click(await screen.findByRole("button", { name: "Informazioni su Disks" }));
    await u.click(await screen.findByRole("button", { name: "Installa 0.1.0", hidden: true }));
    expect(await screen.findByRole("alert", { hidden: true })).toHaveTextContent(
      "Non installato: Il pacchetto non corrisponde al checksum dell'indice firmato.",
    );
  });

  it("does not let a role without the permission install", async () => {
    const u = userEvent.setup();
    mock({ "GET /api/store": { body: catalog } });
    renderPage(user(VIEWER_PERMISSIONS));
    expect(await screen.findByText(/needs the plugins\.manage permission/)).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Information about Disks" }));
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

describe("progress of an install (#530)", () => {
  const running = (over: object) => ({
    body: { progress: { plugin: "disks", action: "install", version: "0.1.0", state: "running", step: "downloading", percent: 40, started_at: "2026-11-28T10:00:00Z", at: "2026-11-28T10:00:01Z", ...over } },
  });

  it("shows a bar with the percentage while downloading, and the step by name after", { timeout: 15000 }, async () => {
    const u = userEvent.setup();
    let progress: { body: unknown } = { body: { progress: null } };
    let finish: (v: unknown) => void = () => undefined;
    const fn = vi.fn((url: string) => {
      if (url === "/api/store/plugins/disks/install")
        return new Promise((resolve) => {
          finish = () => resolve({ ok: true, status: 200, headers: new Headers(), json: () => Promise.resolve({ plugin: "disks", action: "install", version: "0.1.0" }) });
        });
      const body = url === "/api/store/progress" ? progress.body : catalog;
      return Promise.resolve({ ok: true, status: 200, headers: new Headers(), json: () => Promise.resolve(body) });
    });
    vi.stubGlobal("fetch", fn);
    renderPage();
    await u.click(await screen.findByRole("button", { name: "Install: Disks" }));
    progress = running({});
    await u.click(await screen.findByRole("button", { name: "Install Disks", hidden: true }));
    const bar = await screen.findByRole("progressbar", { name: "Disks" });
    await waitFor(() => expect(bar).toHaveAttribute("aria-valuenow", "40"));
    expect(bar).toHaveAttribute("aria-valuetext", "Downloading… 40%");
    expect(screen.getByText("Downloading… 40%")).toBeInTheDocument();
    // The next steps have no percentage: an indeterminate bar and the step's name.
    progress = running({ step: "verifying", percent: -1 });
    await waitFor(() => expect(screen.getByRole("progressbar", { name: "Disks" })).not.toHaveAttribute("aria-valuenow"), { timeout: 3000 });
    expect(screen.getByRole("progressbar", { name: "Disks" })).toHaveAttribute("aria-valuetext", "Verifying the signature and the checksum");
    progress = running({ step: "installing", percent: -1 });
    expect(await screen.findByText("Installing", { selector: "p[aria-live]" }, { timeout: 3000 })).toBeInTheDocument();
    // The hub says it is done (it keeps the record for a minute), then answers the request.
    progress = running({ state: "done", step: undefined, percent: -1 });
    finish(undefined);
    expect(await screen.findByText("Disks 0.1.0 was installed.")).toBeInTheDocument();
    await new Promise((r) => setTimeout(r, 1200)); // a poll after the end must not bring the bar back
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("shows an operation that is already running when the page opens, and turns every action off", async () => {
    mock({ "GET /api/store": { body: catalog }, "GET /api/store/progress": running({ plugin: "hello-world", action: "update", step: "installing", percent: -1 }) });
    renderPage();
    expect(await screen.findByRole("progressbar", { name: "Hello world" })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Updating Hello world…");
    expect(screen.getByRole("button", { name: "Install: Disks" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Update: Hello world" })).toBeDisabled();
  });

  it("ends the bar in an error with the reason when another administrator's operation failed, in Italian too", async () => {
    window.localStorage.setItem("regulus.language", "it");
    mock({
      "GET /api/store": { body: catalog },
      "GET /api/store/progress": running({ state: "failed", step: undefined, percent: -1, code: "helper_failed", helper_code: "checksum" }),
    });
    renderPage();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Non riuscito: Il pacchetto non corrisponde al checksum dell'indice firmato.");
    expect(within(alert).getByRole("progressbar")).toHaveAttribute("aria-valuetext", expect.stringContaining("Non riuscito"));
    // The buttons are on again.
    expect(screen.getByRole("button", { name: "Installa: Disks" })).toBeEnabled();
  });

  it("says a failure of its own with the reason, and turns the buttons on again", async () => {
    const u = userEvent.setup();
    mock({
      "GET /api/store": { body: catalog },
      "POST /api/store/plugins/disks/install": { status: 504, body: { error: "english", code: "helper_timeout" } },
    });
    renderPage();
    await u.click(await screen.findByRole("button", { name: "Install: Disks" }));
    await u.click(await screen.findByRole("button", { name: "Install Disks", hidden: true }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Not installed: The root helper did not answer within two minutes.");
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install: Disks" })).toBeEnabled();
  });
});

describe("step-up", () => {
  it("asks for the password again when the hub wants a recent proof, then repeats the action", async () => {
    const u = userEvent.setup();
    const reauth = vi.fn(() => Promise.resolve(true));
    const remove = setReauthHandler(reauth);
    let calls = 0;
    const fn = vi.fn((_url: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      if (method === "DELETE") {
        calls++;
        const ok = calls > 1;
        return Promise.resolve({
          ok,
          status: ok ? 200 : 403,
          headers: new Headers(),
          json: () => Promise.resolve(ok ? { plugin: "hello-world", action: "remove" } : { error: "needs password", code: "reauth_required" }),
        });
      }
      return Promise.resolve({ ok: true, status: 200, headers: new Headers(), json: () => Promise.resolve(catalog) });
    });
    vi.stubGlobal("fetch", fn);
    renderPage();
    await u.click(await screen.findByRole("button", { name: "Information about Hello world" }));
    await u.click(screen.getByRole("button", { name: "Remove", hidden: true }));
    await u.click(await screen.findByRole("button", { name: "Remove Hello world", hidden: true }));
    expect(await screen.findByText("Hello world was removed.")).toBeInTheDocument();
    expect(reauth).toHaveBeenCalledTimes(1);
    expect(calls).toBe(2);
    remove();
  });

  it("shows the hub's refusal when the password is not given", async () => {
    const u = userEvent.setup();
    const remove = setReauthHandler(() => Promise.resolve(false));
    vi.stubGlobal(
      "fetch",
      vi.fn((_url: string, init?: RequestInit) =>
        Promise.resolve({
          ok: (init?.method ?? "GET") === "GET",
          status: (init?.method ?? "GET") === "GET" ? 200 : 403,
          headers: new Headers(),
          json: () => Promise.resolve((init?.method ?? "GET") === "GET" ? catalog : { error: "needs password", code: "reauth_required" }),
        }),
      ),
    );
    renderPage();
    await u.click(await screen.findByRole("button", { name: "Information about Hello world" }));
    await u.click(screen.getByRole("button", { name: "Remove", hidden: true }));
    await u.click(await screen.findByRole("button", { name: "Remove Hello world", hidden: true }));
    expect(await screen.findByRole("alert", { hidden: true })).toHaveTextContent(/Not removed:/);
    remove();
  });
});

describe("storeErrorText", () => {
  const t = (key: string, params?: Record<string, string | number>) => `${key}${params ? JSON.stringify(params) : ""}`;
  it("maps every code and helper code to a text of the catalog", () => {
    const codes = ["plugin_not_found", "not_installed", "source_unknown", "already_installed", "up_to_date", "source_ambiguous", "no_source", "plugins_off", "busy", "no_compatible_version", "version_not_offered", "source_unavailable", "download_failed", "helper_timeout"];
    for (const c of codes) expect(storeErrorText(t as never, new ApiError(409, "x", c))).toBe(`store.error.${c}`);
    const helper = ["not_verified", "checksum", "incompatible", "yanked", "unsafe_package", "bad_manifest", "already_installed", "not_installed", "source_mismatch", "hand_installed", "plugins_dir", "internal"];
    for (const h of helper) expect(storeErrorText(t as never, new ApiError(422, "x", "helper_failed", undefined, undefined, { helper_code: h }))).toBe(`store.error.helper.${h}`);
    expect(storeErrorText(t as never, new ApiError(422, "x", "helper_failed", undefined, undefined, { helper_code: "new" }))).toContain("store.error.helper_failed");
    expect(storeErrorText(t as never, new ApiError(500, "the hub's text", "other"))).toBe("the hub's text");
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
