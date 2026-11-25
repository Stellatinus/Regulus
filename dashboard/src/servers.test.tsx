import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, User } from "./api";
import { FakeSocket } from "./fake-socket";
import { LiveProvider } from "./LiveProvider";
import { ServerPage } from "./ServerPage";
import { agentCommand, installCommand, ServersPage } from "./ServersPage";
import { SessionContext } from "./session";
import { ADMIN_PERMISSIONS, VIEWER_PERMISSIONS } from "./test-users";

const now = new Date().toISOString();
const admin: User = { username: "admin", role: "admin", permissions: ADMIN_PERMISSIONS, created_at: now, disabled: false, two_factor: false };
const viewer: User = { username: "eve", role: "viewer", permissions: VIEWER_PERMISSIONS, created_at: now, disabled: false, two_factor: false };

const online: Agent = {
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

const offline: Agent = {
  id: "db1",
  version: "0.1.0",
  status: "offline",
  hostname: "db1.lan",
  connected_at: null,
  last_heartbeat: null,
  metrics_at: null,
  uptime_seconds: null,
  last_seen_at: new Date(Date.now() - 2 * 3600 * 1000).toISOString(),
};

const revoked: Agent = { ...offline, id: "old", hostname: "old.lan", status: "revoked" };

interface Call {
  method: string;
  path: string;
  body: unknown;
}

// mockHub answers from handlers keyed by "METHOD path".
function mockHub(handlers: Record<string, () => { status?: number; data?: unknown }>): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      calls.push({ method, path, body: init?.body ? (JSON.parse(init.body as string) as unknown) : undefined });
      const h = handlers[`${method} ${path}`];
      const { status = 200, data } = h ? h() : { status: 404, data: { error: "not found" } };
      return Promise.resolve({ ok: status < 400, status, json: () => Promise.resolve(data) });
    }),
  );
  return calls;
}

function renderAs(user: User, page: React.ReactNode) {
  return render(
    <SessionContext.Provider value={{ user, signOut: () => Promise.resolve(), expired: vi.fn() }}>
      <LiveProvider>{page}</LiveProvider>
    </SessionContext.Provider>,
  );
}

beforeEach(() => {
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("ServersPage", () => {
  it("lists every server with its state and links to its page", async () => {
    const calls = mockHub({ "GET /api/agents?all=true": () => ({ data: [online, offline, revoked] }) });
    renderAs(viewer, <ServersPage />);

    const web = await screen.findByRole("link", { name: "Open the node Web" });
    expect(web).toHaveAttribute("href", "#/servers/web1");
    const db = screen.getByRole("link", { name: "Open the node db1.lan" });
    expect(db).toHaveAttribute("href", "#/servers/db1");
    expect(within(web).getByText("online")).toBeInTheDocument();
    expect(within(db).getByText("offline")).toBeInTheDocument();
    expect(within(screen.getByRole("link", { name: "Open the node old.lan" })).getByText("revoked")).toBeInTheDocument();
    expect(screen.getAllByText(/^2h .*ago$/)).toHaveLength(2); // offline and revoked
    // The control bar counts the nodes shown out of all.
    expect(screen.getByRole("status", { name: "3 of 3 nodes shown" })).toBeInTheDocument();
    expect(calls.some((c) => c.path === "/api/agents?all=true")).toBe(true);
  });

  it("shows the type of every server: the hub's own server is the primary hub (#429)", async () => {
    const hub: Agent = { ...online, id: "local", name: "Hub", role: "hub" };
    const remote: Agent = { ...online, role: "agent" };
    // A hub older than 0.3.0-alpha.2 sends no role: it is derived from the id.
    const legacyRemote: Agent = { ...online, id: "w2", name: "Web2" };
    mockHub({ "GET /api/agents?all=true": () => ({ data: [hub, remote, legacyRemote, { ...online, id: "x", name: "Fancy", role: "redundant-hub" }] }) });
    renderAs(viewer, <ServersPage />);

    await screen.findByRole("link", { name: "Open the node Hub" });
    const cardOf = (name: string) => screen.getByRole("link", { name: `Open the node ${name}` });
    expect(within(cardOf("Hub")).getByText("Primary hub")).toBeInTheDocument();
    expect(within(cardOf("Web")).getByText("Generic node")).toBeInTheDocument();
    expect(within(cardOf("Web2")).getByText("Generic node")).toBeInTheDocument();
    expect(within(cardOf("Fancy")).getByText("Secondary hub")).toBeInTheDocument();
  });

  it("tells which servers run another release than the hub", async () => {
    const behind: Agent = { ...online, id: "b", name: "Behind", compatibility: "behind", version: "0.2.1" };
    const ahead: Agent = { ...online, id: "a", name: "Ahead", compatibility: "ahead", version: "0.4.0" };
    const refused: Agent = { ...offline, id: "r", hostname: "r.lan", status: "incompatible", incompatible_reason: "agent version 0.1.0 is too far behind", version: "0.1.0" };
    mockHub({ "GET /api/agents?all=true": () => ({ data: [online, behind, ahead, refused] }) });
    renderAs(viewer, <ServersPage />);

    // A pill says what to do in a word; its tooltip has the whole sentence.
    expect(await screen.findByTitle("behind the hub")).toHaveTextContent("Update");
    expect(screen.getByTitle("ahead of the hub: update the hub")).toHaveTextContent("Ahead of the hub");
    expect(screen.getByTitle("too old for this hub: update it")).toHaveTextContent("Incompatible");
    expect(within(screen.getByRole("link", { name: "Open the node r.lan" })).getByText("incompatible")).toBeInTheDocument();
  });

  it("says when a server is connected only to be updated", async () => {
    const old: Agent = { ...online, id: "o", name: "Old", compatibility: "too_old", update_only: true, version: "0.1.0" };
    mockHub({ "GET /api/agents?all=true": () => ({ data: [old] }) });
    renderAs(viewer, <ServersPage />);
    expect(await screen.findByTitle("too old for this hub: connected only to be updated (Updates page)")).toHaveTextContent("Update");
  });

  it("marks a server whose agent cannot read everything", async () => {
    const limited: Agent = { ...online, collector: "basic", unavailable: ["cpu", "memory"] };
    mockHub({ "GET /api/agents?all=true": () => ({ data: [limited] }) });
    renderAs(viewer, <ServersPage />);
    expect(await screen.findByText("limited metrics")).toBeInTheDocument();
  });

  it("says when there is no server", async () => {
    mockHub({ "GET /api/agents?all=true": () => ({ data: [] }) });
    renderAs(viewer, <ServersPage />);
    expect(await screen.findByText("No servers yet.")).toBeInTheDocument();
  });

  it("offers adding a server to administrators only", async () => {
    mockHub({ "GET /api/agents?all=true": () => ({ data: [] }) });
    const first = renderAs(viewer, <ServersPage />);
    await screen.findByText("No servers yet.");
    expect(screen.queryByRole("heading", { name: "Add a server" })).not.toBeInTheDocument();
    first.unmount();

    renderAs(admin, <ServersPage />);
    // The form is closed until the + button of the title row opens it (#428).
    const add = await screen.findByRole("button", { name: "Add a server" });
    expect(screen.queryByRole("heading", { name: "Add a server" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Name (optional)")).not.toBeInTheDocument();
    expect(add).toHaveAttribute("aria-expanded", "false");
    await userEvent.setup().click(add);
    expect(await screen.findByRole("heading", { name: "Add a server" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add a server" })).toHaveAttribute("aria-expanded", "true");
  });

  it("puts the add button in the control bar, and closes Add a server with Close or Escape (#428)", async () => {
    mockHub({ "GET /api/agents?all=true": () => ({ data: [online, offline] }) });
    const user = userEvent.setup();
    renderAs(admin, <ServersPage />);

    const row = await screen.findByRole("region", { name: "Search and filters of the nodes" });
    expect(within(row).getByRole("button", { name: "Add a server" })).toBeInTheDocument();

    await user.click(within(row).getByRole("button", { name: "Add a server" }));
    expect(await screen.findByRole("heading", { name: "Add a server" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("heading", { name: "Add a server" })).not.toBeInTheDocument();

    await user.click(within(row).getByRole("button", { name: "Add a server" }));
    await user.type(await screen.findByLabelText("Name (optional)"), "{Escape}");
    expect(screen.queryByRole("heading", { name: "Add a server" })).not.toBeInTheDocument();
  });

  it("shows the install command once the token is created", async () => {
    const grant = {
      token: "tok123",
      name: "Web",
      expires_at: now,
      ca_fingerprint: "sha256:abcd",
      agent_port: 7443,
      hub_version: "0.1.0-alpha.1",
    };
    const calls = mockHub({
      "GET /api/agents?all=true": () => ({ data: [] }),
      "POST /api/enrollment-tokens": () => ({ data: grant }),
    });
    const user = userEvent.setup();
    renderAs(admin, <ServersPage />);

    await user.click(await screen.findByRole("button", { name: "Add a server" }));
    await user.type(await screen.findByLabelText("Name (optional)"), " Web ");
    await user.click(screen.getByRole("button", { name: "Create enrollment token" }));

    const host = window.location.hostname;
    const options = `--hub ${host}:7443 --token tok123 --ca-fingerprint sha256:abcd`;
    const install = (await screen.findByLabelText("Install command")) as HTMLInputElement;
    expect(install.value).toBe(
      `curl -fsSLO https://github.com/Stellatinus/Regulus/releases/download/v0.1.0-alpha.1/install.sh && sh install.sh --version 0.1.0-alpha.1 --role agent ${options}`,
    );
    expect((screen.getByLabelText("Agent command") as HTMLInputElement).value).toBe(
      `regulus agent ${options} --data-dir /var/lib/regulus-agent`,
    );
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name: "Web", ttl: "1h" });
    // The list is fetched again so the new server shows up.
    await waitFor(() => expect(calls.filter((c) => c.path === "/api/agents?all=true").length).toBeGreaterThan(1));
  });

  it("lists the pending enrollment tokens and cancels one after confirmation", async () => {
    let tokens = [
      { id: "aaaa1111bbbb2222", name: "Web", created_by: "admin", created_at: now, expires_at: now },
      { id: "cccc3333dddd4444", name: "", created_by: "ana", created_at: now, expires_at: now },
    ];
    const calls = mockHub({
      "GET /api/agents?all=true": () => ({ data: [] }),
      "GET /api/enrollment-tokens": () => ({ data: tokens }),
      "DELETE /api/enrollment-tokens/aaaa1111bbbb2222": () => {
        tokens = tokens.slice(1);
        return { status: 204 };
      },
    });
    const user = userEvent.setup();
    renderAs(admin, <ServersPage />);

    await user.click(await screen.findByRole("button", { name: "Add a server" }));
    expect(await screen.findByRole("heading", { name: "Pending enrollment tokens" })).toBeInTheDocument();
    expect(screen.getByText("(no name)")).toBeInTheDocument();
    expect(screen.getByText(/made by ana/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Cancel token Web" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);

    await user.click(screen.getByRole("button", { name: "Cancel token Web" }));
    await user.click(screen.getByRole("button", { name: "Cancel the token" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Cancel token Web" })).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Cancel token (no name)" })).toBeInTheDocument();
  });

  describe("the command of a token that is no longer valid (#426)", () => {
    const grant = { token: "tok123", name: "Web", expires_at: new Date(Date.now() + 3600_000).toISOString(), ca_fingerprint: "sha256:abcd", agent_port: 7443 };
    const pendingToken = { id: "aaaa1111bbbb2222", name: "Web", created_by: "admin", created_at: now, expires_at: grant.expires_at };

    it("clears the command when the token shown is cancelled", async () => {
      let tokens: (typeof pendingToken)[] = [];
      mockHub({
        "GET /api/agents?all=true": () => ({ data: [] }),
        "GET /api/enrollment-tokens": () => ({ data: tokens }),
        "POST /api/enrollment-tokens": () => {
          tokens = [pendingToken];
          return { data: grant };
        },
        "DELETE /api/enrollment-tokens/aaaa1111bbbb2222": () => {
          tokens = [];
          return { status: 204 };
        },
      });
      const user = userEvent.setup();
      renderAs(admin, <ServersPage />);
      await user.click(await screen.findByRole("button", { name: "Add a server" }));
      await user.type(await screen.findByLabelText("Name (optional)"), "Web");
      await user.click(screen.getByRole("button", { name: "Create enrollment token" }));
      expect(await screen.findByLabelText("Install command")).toBeInTheDocument();

      await user.click(await screen.findByRole("button", { name: "Cancel token Web" }));
      await user.click(screen.getByRole("button", { name: "Cancel the token" }));

      await waitFor(() => expect(screen.queryByLabelText("Install command")).not.toBeInTheDocument());
      expect(screen.queryByLabelText("Agent command")).not.toBeInTheDocument();
      expect(screen.queryByText(/This token is shown only now/)).not.toBeInTheDocument();
    });

    it("clears the command and the pending entry, without a reload, when a server enrolls with the token", async () => {
      let tokens: (typeof pendingToken)[] = [];
      mockHub({
        "GET /api/agents": () => ({ data: [] }),
        "GET /api/agents?all=true": () => ({ data: [] }),
        "GET /api/enrollment-tokens": () => ({ data: tokens }),
        "POST /api/enrollment-tokens": () => {
          tokens = [pendingToken];
          return { data: grant };
        },
      });
      const user = userEvent.setup();
      renderAs(admin, <ServersPage />);
      await waitFor(() => expect(FakeSocket.last).toBeDefined());
      act(() => FakeSocket.last.open());
      act(() => FakeSocket.last.send({ type: "snapshot", stale_after_seconds: 30, agents: [] }));

      await user.click(await screen.findByRole("button", { name: "Add a server" }));
      await user.type(await screen.findByLabelText("Name (optional)"), "Web");
      await user.click(screen.getByRole("button", { name: "Create enrollment token" }));
      expect(await screen.findByLabelText("Install command")).toBeInTheDocument();
      expect(await screen.findByRole("button", { name: "Cancel token Web" })).toBeInTheDocument();

      // The server runs the command and enrolls: the hub no longer lists the
      // token and sends agent.connected on the stream.
      tokens = [];
      act(() => FakeSocket.last.send({ type: "agent.connected", agent: { ...online, id: "agent-1", name: "Web" } }));

      await waitFor(() => expect(screen.queryByLabelText("Install command")).not.toBeInTheDocument());
      expect(screen.queryByRole("button", { name: "Cancel token Web" })).not.toBeInTheDocument();
      expect(screen.getByText(/no longer pending/)).toBeInTheDocument();
    });

    it("keeps the command while the list has not shown the new token yet", async () => {
      // The list read right after the token was made may not have it; that is
      // not the token being gone.
      mockHub({
        "GET /api/agents?all=true": () => ({ data: [] }),
        "GET /api/enrollment-tokens": () => ({ data: [] }),
        "POST /api/enrollment-tokens": () => ({ data: grant }),
      });
      const user = userEvent.setup();
      renderAs(admin, <ServersPage />);
      await user.click(await screen.findByRole("button", { name: "Add a server" }));
      await user.click(await screen.findByRole("button", { name: "Create enrollment token" }));
      expect(await screen.findByLabelText("Install command")).toBeInTheDocument();
      await new Promise((r) => setTimeout(r, 50));
      expect(screen.getByLabelText("Install command")).toBeInTheDocument();
    });
  });

  describe("copying the commands (#427)", () => {
    const grant = { token: "tok123", name: "Web", expires_at: new Date(Date.now() + 3600_000).toISOString(), ca_fingerprint: "sha256:abcd", agent_port: 7443 };

    async function openWithCommand() {
      mockHub({
        "GET /api/agents?all=true": () => ({ data: [] }),
        "GET /api/enrollment-tokens": () => ({ data: [{ id: "aaaa1111bbbb2222", name: "Web", created_by: "admin", created_at: now, expires_at: grant.expires_at }] }),
        "POST /api/enrollment-tokens": () => ({ data: grant }),
      });
      const user = userEvent.setup();
      renderAs(admin, <ServersPage />);
      await user.click(await screen.findByRole("button", { name: "Add a server" }));
      await user.type(await screen.findByLabelText("Name (optional)"), "Web");
      await user.click(screen.getByRole("button", { name: "Create enrollment token" }));
      await screen.findByLabelText("Install command");
      return user;
    }

    it("has icon buttons with a name and a tooltip, not the long text", async () => {
      await openWithCommand();
      for (const name of ["Copy install command", "Copy agent command"]) {
        const button = screen.getByRole("button", { name });
        expect(button).toHaveAttribute("title", name);
        expect(button.textContent).toBe("");
        expect(button.querySelector("svg")).not.toBeNull();
      }
    });

    it("says on a line under the command, in the warning style, that the browser cannot copy", async () => {
      // Plain HTTP: no clipboard API.
      vi.stubGlobal("isSecureContext", false);
      const user = await openWithCommand();
      await user.click(screen.getByRole("button", { name: "Copy install command" }));

      const warning = await screen.findByRole("alert");
      expect(warning).toHaveTextContent("This browser cannot copy here");
      expect(warning).toHaveClass("copy-warning");
      // It is not beside the button: it follows the row that holds the command and the button.
      const box = screen.getByLabelText("Install command").closest(".command-box") as HTMLElement;
      expect(box.contains(warning)).toBe(true);
      expect(warning.previousElementSibling).toHaveClass("command");
      expect(warning.parentElement).toBe(box);
      // The other command shows no message.
      expect(screen.getAllByRole("alert")).toHaveLength(1);
    });

    it("says Copied, and no warning, where copying works", async () => {
      const writeText = vi.fn(() => Promise.resolve());
      vi.stubGlobal("isSecureContext", true);
      const user = await openWithCommand();
      // user-event installs its own clipboard when it is set up: replace it.
      Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
      await user.click(screen.getByRole("button", { name: "Copy agent command" }));

      await waitFor(() => expect(writeText).toHaveBeenCalledWith(expect.stringContaining("regulus agent")));
      expect(await screen.findByText("Copied")).toBeInTheDocument();
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    });
  });

  it("shows nothing about tokens when none is pending", async () => {
    mockHub({ "GET /api/agents?all=true": () => ({ data: [] }), "GET /api/enrollment-tokens": () => ({ data: [] }) });
    renderAs(admin, <ServersPage />);
    await userEvent.setup().click(await screen.findByRole("button", { name: "Add a server" }));
    await screen.findByRole("heading", { name: "Add a server" });
    expect(screen.queryByRole("heading", { name: "Pending enrollment tokens" })).not.toBeInTheDocument();
  });

  it("says when remote agents are off and lets an administrator turn them on", async () => {
    const off = { enabled: false, ca_fingerprint: "sha256:f", editable: true };
    const on = { enabled: true, port: 8443, source: "dashboard", ca_fingerprint: "sha256:f", editable: false };
    const calls = mockHub({
      "GET /api/agents?all=true": () => ({ data: [] }),
      "GET /api/agents/listener": () => ({ data: off }),
      "PUT /api/agents/listener": () => ({ data: on }),
    });
    const user = userEvent.setup();
    renderAs(admin, <ServersPage />);

    expect(await screen.findByRole("heading", { name: "Remote agents are off" })).toBeInTheDocument();
    // No token while it could not work.
    expect(screen.queryByRole("button", { name: "Add a server" })).not.toBeInTheDocument();

    const port = screen.getByLabelText("Port for remote agents");
    await user.clear(port);
    await user.type(port, "80");
    expect(screen.getByRole("button", { name: "Turn on remote agents" })).toBeDisabled();
    await user.clear(port);
    await user.type(port, "8443");
    await user.click(screen.getByRole("button", { name: "Turn on remote agents" }));

    expect(await screen.findByRole("button", { name: "Add a server" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Remote agents are off" })).not.toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ port: 8443 });
  });

  it("tells a viewer that remote agents are off, without the form", async () => {
    mockHub({
      "GET /api/agents?all=true": () => ({ data: [] }),
      "GET /api/agents/listener": () => ({ data: { enabled: false, ca_fingerprint: "sha256:f", editable: true } }),
    });
    renderAs(viewer, <ServersPage />);
    expect(await screen.findByRole("heading", { name: "Remote agents are off" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Turn on remote agents" })).not.toBeInTheDocument();
  });

  it("shows why remote agents could not be turned on", async () => {
    mockHub({
      "GET /api/agents?all=true": () => ({ data: [] }),
      "GET /api/agents/listener": () => ({ data: { enabled: false, ca_fingerprint: "sha256:f", editable: true } }),
      "PUT /api/agents/listener": () => ({ status: 409, data: { error: "the port cannot be opened" } }),
    });
    const user = userEvent.setup();
    renderAs(admin, <ServersPage />);
    await user.click(await screen.findByRole("button", { name: "Turn on remote agents" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("the port cannot be opened");
  });

  it("shows why a token could not be created", async () => {
    mockHub({
      "GET /api/agents?all=true": () => ({ data: [] }),
      "POST /api/enrollment-tokens": () => ({ status: 409, data: { error: "agent listener is off" } }),
    });
    const user = userEvent.setup();
    renderAs(admin, <ServersPage />);
    await user.click(await screen.findByRole("button", { name: "Add a server" }));
    await user.click(await screen.findByRole("button", { name: "Create enrollment token" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("agent listener is off");
    expect(screen.queryByLabelText("Install command")).not.toBeInTheDocument();
  });

  it("builds the commands from the grant", () => {
    const grant = { token: "t", name: "", expires_at: now, ca_fingerprint: "sha256:f", agent_port: 1 };
    // A development hub has no release to point to: the latest stable one.
    expect(installCommand(grant, "hub.lan")).toBe(
      "curl -fsSLO https://github.com/Stellatinus/Regulus/releases/latest/download/install.sh && sh install.sh --role agent --hub hub.lan:1 --token t --ca-fingerprint sha256:f",
    );
    // A release hub pins its own version, from its own release.
    expect(installCommand({ ...grant, hub_version: "0.2.0-beta.3" }, "hub.lan")).toBe(
      "curl -fsSLO https://github.com/Stellatinus/Regulus/releases/download/v0.2.0-beta.3/install.sh && sh install.sh --version 0.2.0-beta.3 --role agent --hub hub.lan:1 --token t --ca-fingerprint sha256:f",
    );
    expect(agentCommand(grant, "hub.lan")).toBe(
      "regulus agent --hub hub.lan:1 --token t --ca-fingerprint sha256:f --data-dir /var/lib/regulus-agent",
    );
  });
});

describe("ServerPage", () => {
  it("shows the facts and the widgets of an online server", async () => {
    mockHub({ "GET /api/agents/web1": () => ({ data: online }) });
    renderAs(viewer, <ServerPage id="web1" />);

    expect(await screen.findByRole("dialog", { name: /Web/ })).toBeInTheDocument();
    expect(screen.getAllByText("web1.lan").length).toBeGreaterThan(0); // header and fact
    expect(screen.getByText("Ubuntu 24.04 amd64")).toBeInTheDocument();
    for (const title of ["System", "CPU", "Memory", "Network", "Disk usage"]) {
      expect(screen.getByRole("region", { name: title })).toBeInTheDocument();
    }
    // A viewer cannot manage the server.
    expect(screen.queryByRole("button", { name: "Rename" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Revoke this server" })).not.toBeInTheDocument();
  });

  it("says what the agent of a server cannot read, and its container", async () => {
    const limited: Agent = { ...online, collector: "linux", unavailable: ["memory", "disks"], container: "docker" };
    mockHub({ "GET /api/agents/web1": () => ({ data: limited }) });
    renderAs(viewer, <ServerPage id="web1" />);
    expect(await screen.findByText("limited: the agent cannot read the memory, the disks")).toBeInTheDocument();
    expect(screen.getByText("docker")).toBeInTheDocument();
  });

  it("explains an offline server instead of showing widgets", async () => {
    mockHub({ "GET /api/agents/db1": () => ({ data: offline }) });
    renderAs(viewer, <ServerPage id="db1" />);

    expect(await screen.findByRole("status")).toHaveTextContent("offline");
    expect(screen.queryByRole("region", { name: "CPU" })).not.toBeInTheDocument();
    expect(screen.getAllByText(/^2h .*ago$/).length).toBeGreaterThan(0);
  });

  it("says at the top that an offline server is offline, since when, and that the data is the last known (#436)", async () => {
    mockHub({ "GET /api/agents/db1": () => ({ data: offline }) });
    renderAs(viewer, <ServerPage id="db1" />);

    const notice = await screen.findByRole("status");
    expect(notice).toHaveTextContent("offline: last contact");
    expect(notice).toHaveTextContent("2h");
    expect(notice).toHaveTextContent("last known");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    // The notice comes before the facts.
    expect(notice.compareDocumentPosition(screen.getByText("Host")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("says that a stale server has not reported, and keeps the page", async () => {
    const stale: Agent = { ...online, status: "stale", last_heartbeat: new Date(Date.now() - 90_000).toISOString() };
    mockHub({ "GET /api/agents/web1": () => ({ data: stale }) });
    renderAs(viewer, <ServerPage id="web1" />);

    const notice = await screen.findByText(/has not received a report/);
    expect(notice).toBeInTheDocument();
    expect(screen.getAllByText(/^stale$/).length).toBeGreaterThan(0);
    expect(screen.getByRole("dialog", { name: /Web/ })).toBeInTheDocument();
  });

  it("says the time and the state of a revoked server, and that it was never connected when so (#436)", async () => {
    mockHub({ "GET /api/agents/old": () => ({ data: revoked }) });
    renderAs(admin, <ServerPage id="old" />);
    const notice = await screen.findByRole("status");
    expect(notice).toHaveTextContent("revoked: last contact");

    mockHub({ "GET /api/agents/new": () => ({ data: { ...offline, id: "new", last_seen_at: undefined } }) });
    renderAs(admin, <ServerPage id="new" />);
    await waitFor(() => expect(screen.getAllByRole("status").some((n) => /never connected/.test(n.textContent ?? ""))).toBe(true));
  });

  it("says a revoked server cannot come back", async () => {
    mockHub({ "GET /api/agents/old": () => ({ data: revoked }) });
    renderAs(admin, <ServerPage id="old" />);

    expect(await screen.findByRole("status")).toHaveTextContent("revoked");
    expect(screen.queryByRole("button", { name: "Revoke this server" })).not.toBeInTheDocument();
  });

  it("reports an unknown server", async () => {
    mockHub({});
    renderAs(viewer, <ServerPage id="nope" />);
    expect(await screen.findByRole("heading", { name: "Server not found" })).toBeInTheDocument();
  });

  it("lets an administrator rename a server", async () => {
    const calls = mockHub({
      "GET /api/agents/web1": () => ({ data: online }),
      "PATCH /api/agents/web1": () => ({ status: 204 }),
    });
    const user = userEvent.setup();
    renderAs(admin, <ServerPage id="web1" />);

    const input = await screen.findByLabelText("Name");
    await user.clear(input);
    await user.type(input, "Frontend");
    await user.click(screen.getByRole("button", { name: "Rename" }));

    await waitFor(() => expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ name: "Frontend" }));
    // The page is fetched again after the change.
    await waitFor(() => expect(calls.filter((c) => c.path === "/api/agents/web1" && c.method === "GET").length).toBeGreaterThan(1));
  });

  it("shows when the server's certificate ends, or that it is not known yet", async () => {
    const end = new Date(Date.now() + 40 * 86_400_000 + 3_600_000).toISOString();
    mockHub({ "GET /api/agents/web1": () => ({ data: { ...online, cert_not_after: end } }) });
    const first = renderAs(admin, <ServerPage id="web1" />);
    expect(await screen.findByText(/\(in 40 days\)/)).toBeInTheDocument();
    first.unmount();
    mockHub({ "GET /api/agents/web1": () => ({ data: online }) });
    renderAs(admin, <ServerPage id="web1" />);
    expect(await screen.findByText(/not known yet/)).toBeInTheDocument();
  });

  it("revokes a server after confirmation only", async () => {
    const calls = mockHub({
      "GET /api/agents/web1": () => ({ data: online }),
      "DELETE /api/agents/web1": () => ({ status: 204 }),
    });
    const user = userEvent.setup();
    renderAs(admin, <ServerPage id="web1" />);

    await user.click(await screen.findByRole("button", { name: "Revoke this server" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(screen.getByRole("button", { name: "Revoke the server" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });

  it("shows the role on the page of a server (#429)", async () => {
    mockHub({
      "GET /api/agents/local": () => ({ data: { ...online, id: "local", name: "Hub", role: "hub" } }),
      "GET /api/agents/web1": () => ({ data: { ...online, role: "agent" } }),
    });
    const first = renderAs(viewer, <ServerPage id="local" />);
    expect(await screen.findByText("Role")).toBeInTheDocument();
    expect(screen.getByText("Primary hub")).toBeInTheDocument(); // the type, in the header
    expect(screen.getByText("hub")).toBeInTheDocument(); // the role, in the facts
    first.unmount();
    renderAs(viewer, <ServerPage id="web1" />);
    await screen.findByText("Role");
    expect(screen.getByText("agent")).toBeInTheDocument();
    expect(screen.getByText("Generic node")).toBeInTheDocument();
    expect(screen.queryByText("hub")).not.toBeInTheDocument();
  });

  it("does not offer to manage the built-in agent", async () => {
    mockHub({ "GET /api/agents/local": () => ({ data: { ...online, id: "local", name: undefined } }) });
    renderAs(admin, <ServerPage id="local" />);
    await screen.findByRole("dialog", { name: /web1\.lan/ });
    expect(screen.queryByRole("button", { name: "Rename" })).not.toBeInTheDocument();
  });
});
