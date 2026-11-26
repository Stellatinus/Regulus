import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, User } from "./api";
import { AutomationsPage, loadAutomations } from "./AutomationsPage";
import { LiveProvider } from "./LiveProvider";
import { ServicesPage } from "./ServicesPage";
import { SettingsPage } from "./SettingsPage";
import { SessionContext } from "./session";
import { ADMIN_PERMISSIONS } from "./test-users";

const now = new Date().toISOString();
const admin: User = { username: "ana", role: "admin", permissions: ADMIN_PERMISSIONS, created_at: now, disabled: false, two_factor: false };

type Handler = () => { status?: number; data?: unknown };

function mockHub(extra: Record<string, Handler> = {}) {
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, init?: RequestInit) => {
      const handlers: Record<string, Handler> = {
        "GET /api/agents?all=true": () => ({ data: [] as Agent[] }),
        "GET /api/account/two-factor": () => ({ data: { enabled: false, mandatory: false, policy: "off", recovery_codes_left: 0 } }),
        "GET /api/account/sessions": () => ({ data: [] }),
        "GET /api/plugins": () => ({ data: { hub: { enabled: false, plugins: [] }, agents: [] } }),
        ...extra,
      };
      const { status = 200, data } = handlers[`${init?.method ?? "GET"} ${path}`]?.() ?? { status: 404, data: { error: "not found" } };
      return Promise.resolve({ ok: status < 400, status, json: () => Promise.resolve(data) });
    }),
  );
}

function renderAs(page: React.ReactNode, user: User = admin) {
  return render(
    <SessionContext.Provider value={{ user, signOut: () => Promise.resolve(), expired: vi.fn() }}>
      <LiveProvider>{page}</LiveProvider>
    </SessionContext.Provider>,
  );
}

beforeEach(() => {
  window.localStorage.clear();
  window.location.hash = "";
  mockHub();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.location.hash = "";
});

describe("Settings", () => {
  it("lists the categories in two groups and opens the one in the address", async () => {
    window.location.hash = "#/settings/appearance";
    renderAs(<SettingsPage />);
    const nav = screen.getByRole("navigation", { name: "Categories of settings" });
    expect(within(nav).getAllByRole("link").map((l) => l.textContent)).toEqual([
      "Account and security",
      "Appearance and language",
      "Plugins, apps, automations",
      "Network and core",
      "Database",
      "Metrics and telemetry",
      "Mesh updates",
    ]);
    expect(within(nav).getByRole("link", { name: "Appearance and language" })).toHaveAttribute("aria-current", "page");
    expect(screen.getByLabelText("Language")).toBeInTheDocument();
  });

  it("opens Account and security first, and hides Mesh updates from those who may not update", () => {
    renderAs(<SettingsPage />, { ...admin, role: "viewer", permissions: ["servers.read"] });
    const nav = screen.getByRole("navigation", { name: "Categories of settings" });
    expect(within(nav).getByRole("link", { name: "Account and security" })).toHaveAttribute("aria-current", "page");
    expect(within(nav).queryByRole("link", { name: "Mesh updates" })).not.toBeInTheDocument();
  });

  it("says what the configuration file holds for the categories the dashboard cannot edit yet", () => {
    window.location.hash = "#/settings/metrics";
    renderAs(<SettingsPage />);
    expect(screen.getByText("metrics_interval")).toBeInTheDocument();
    expect(screen.getByText(/Editing it from the dashboard is not available yet/)).toBeInTheDocument();
  });

  it("switches the three features, which start with Plugins on", async () => {
    window.location.hash = "#/settings/features";
    const user = userEvent.setup();
    renderAs(<SettingsPage />);
    expect(screen.getByRole("switch", { name: "Plugins" })).toBeChecked();
    expect(screen.getByRole("switch", { name: "Apps" })).not.toBeChecked();
    expect(screen.getByRole("switch", { name: "Automations" })).not.toBeChecked();
    await user.click(screen.getByRole("switch", { name: "Apps" }));
    await user.click(screen.getByRole("switch", { name: "Plugins" }));
    expect(window.localStorage.getItem("regulus.feature.apps")).toBe("on");
    expect(window.localStorage.getItem("regulus.feature.plugins")).toBe("off");
  });
});

describe("Services", () => {
  it("shows the installed plugins and a store tab that says it is not built", async () => {
    window.location.hash = "#/plugins";
    const user = userEvent.setup();
    renderAs(<ServicesPage />);
    expect(await screen.findByRole("heading", { name: "Plugins" })).toBeInTheDocument();
    window.location.hash = "#/plugins/store";
    await user.click(screen.getByRole("link", { name: "Regulus Store" }));
    expect(await screen.findByText("The store is not available yet")).toBeInTheDocument();
  });

  it("says when plugins are switched off", () => {
    window.localStorage.setItem("regulus.feature.plugins", "off");
    renderAs(<ServicesPage />);
    expect(screen.getByText("Plugins are switched off in Settings.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open the settings" })).toHaveAttribute("href", "#/settings/features");
  });
});

describe("the automation designer", () => {
  it("says that automations are drafts, and starts empty", () => {
    renderAs(<AutomationsPage />);
    expect(screen.getByRole("heading", { name: "Automations" })).toBeInTheDocument();
    expect(screen.getByText(/they are kept in this browser and do not run/i)).toBeInTheDocument();
    expect(screen.getByText("No automation yet")).toBeInTheDocument();
  });

  it("creates, switches on, edits and deletes an automation", async () => {
    const user = userEvent.setup();
    renderAs(<AutomationsPage />);

    await user.click(screen.getByRole("button", { name: /New automation/ }));
    const save = screen.getByRole("button", { name: "Save the draft" });
    expect(save).toBeDisabled(); // no name yet
    await user.type(screen.getByLabelText("Name"), "Offline alert");
    await user.selectOptions(screen.getByLabelText("Trigger"), "A node goes offline");
    await user.selectOptions(screen.getByLabelText("Action 1"), "Send an e-mail");
    await user.click(screen.getByRole("button", { name: /Add an action/ }));
    await user.selectOptions(screen.getByLabelText("Action 2"), "Restart the node");
    await user.click(save);

    const card = screen.getByRole("article", { name: "Offline alert" });
    expect(within(card).getByText(/A node goes offline/)).toBeInTheDocument();
    expect(within(card).getByText("Send an e-mail")).toBeInTheDocument();
    expect(within(card).getByText("Restart the node")).toBeInTheDocument();
    const stored = loadAutomations();
    expect(stored).toHaveLength(1);
    expect(stored[0]).toMatchObject({ name: "Offline alert", enabled: false, trigger: "node_offline" });
    expect(stored[0].actions.map((a) => a.target)).toEqual(["notify_email", "node_restart"]);

    await user.click(within(card).getByRole("switch", { name: "Enable Offline alert" }));
    expect(loadAutomations()[0].enabled).toBe(true);

    await user.click(within(card).getByRole("button", { name: "Edit" }));
    const name = screen.getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "Alert");
    await user.click(screen.getByRole("button", { name: "Save the draft" }));
    expect(screen.getByRole("article", { name: "Alert" })).toBeInTheDocument();
    expect(loadAutomations()).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "Delete Alert" }));
    await user.click(screen.getByRole("button", { name: "Delete the automation" }));
    expect(screen.getByText("No automation yet")).toBeInTheDocument();
    expect(loadAutomations()).toEqual([]);
  });

  it("keeps the last action: it cannot be removed", async () => {
    const user = userEvent.setup();
    renderAs(<AutomationsPage />);
    await user.click(screen.getByRole("button", { name: /New automation/ }));
    expect(screen.getByRole("button", { name: "Remove action 1" })).toBeDisabled();
  });
});
