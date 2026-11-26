import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PluginUI } from "./api";
import { PluginFrame } from "./PluginFrame";
import { clampHeight, parseFrameMessage } from "./pluginFrame";

const plugin: PluginUI = {
  id: "disks",
  version: "0.1.0",
  module: "ui/disks.js",
  node_tab: { id: "disks", title: { en: "Disks", it: "Dischi" } },
  permissions: ["disks.read"],
};

type Calls = { path: string; init?: RequestInit }[];

function mockHub(calls: Calls, over: Record<string, () => Promise<unknown>> = {}) {
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, init?: RequestInit) => {
      calls.push({ path, init });
      const reply = (status: number, data?: unknown) => Promise.resolve({ ok: status < 400, status, json: () => Promise.resolve(data) });
      if (over[path]) return over[path]();
      if (path === "/api/plugins/disks/ui") return reply(200);
      if (path === "/api/plugins/disks/call") return reply(200, { agent: "web1", disks: [] });
      return reply(404, { error: "not found" });
    }),
  );
}

// The frame is a real <iframe> in jsdom; what the page inside would send is
// delivered as a message event whose source is the frame's window.
function fromFrame(frame: HTMLIFrameElement, data: unknown) {
  act(() => {
    window.dispatchEvent(new MessageEvent("message", { data, source: frame.contentWindow }));
  });
}

async function frameOf(): Promise<{ frame: HTMLIFrameElement; posted: unknown[] }> {
  const frame = (await screen.findByTitle("Disks")) as HTMLIFrameElement;
  const posted: unknown[] = [];
  vi.spyOn(frame.contentWindow!, "postMessage").mockImplementation((m: unknown) => {
    posted.push(m);
  });
  return { frame, posted };
}

beforeEach(() => window.localStorage.removeItem("regulus.language"));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("PluginFrame", () => {
  it("runs the page in a sandbox with an opaque origin", async () => {
    mockHub([]);
    render(<PluginFrame plugin={plugin} node="web1" />);
    const { frame } = await frameOf();
    expect(frame.getAttribute("sandbox")).toBe("allow-scripts");
    expect(frame.getAttribute("sandbox")).not.toContain("allow-same-origin");
    expect(frame.getAttribute("src")).toBe("/api/plugins/disks/ui");
  });

  it("tells the page the node, language and theme when it is ready", async () => {
    mockHub([]);
    document.documentElement.style.setProperty("--ink", "#123456");
    render(<PluginFrame plugin={plugin} node="web1" />);
    const { frame, posted } = await frameOf();
    fromFrame(frame, { regulus: 1, type: "ready" });
    const init = posted[0] as { regulus: number; type: string; locale: string; theme: Record<string, string>; context: { node: string } };
    expect(init).toMatchObject({ regulus: 1, type: "init", locale: "en", context: { node: "web1" } });
    expect(init.theme["--ink"]).toBe("#123456");
    document.documentElement.style.removeProperty("--ink");
  });

  it("forwards a request to the bridge and answers with the data", async () => {
    const calls: Calls = [];
    mockHub(calls);
    render(<PluginFrame plugin={plugin} node="web1" />);
    const { frame, posted } = await frameOf();
    fromFrame(frame, { regulus: 1, type: "ready" });
    fromFrame(frame, { regulus: 1, type: "request", id: 7, op: "inventory.disks", params: { node: "web1" } });
    await waitFor(() => expect(posted.length).toBe(2));
    expect(posted[1]).toEqual({ regulus: 1, type: "response", id: 7, ok: true, data: { agent: "web1", disks: [] } });
    const call = calls.find((c) => c.path === "/api/plugins/disks/call");
    expect(call?.init?.method).toBe("POST");
    expect(JSON.parse(String(call?.init?.body))).toEqual({ op: "inventory.disks", params: { node: "web1" } });
  });

  it("answers an error with its code, and the page can show it", async () => {
    mockHub([], {
      "/api/plugins/disks/call": () => Promise.resolve({ ok: false, status: 403, json: () => Promise.resolve({ error: "no", code: "plugin_forbidden" }) }),
    });
    render(<PluginFrame plugin={plugin} node="web1" />);
    const { frame, posted } = await frameOf();
    fromFrame(frame, { regulus: 1, type: "ready" });
    fromFrame(frame, { regulus: 1, type: "request", id: "a", op: "inventory.disks", params: { node: "web1" } });
    await waitFor(() => expect(posted.length).toBe(2));
    expect(posted[1]).toMatchObject({ type: "response", id: "a", ok: false, error: { code: "plugin_forbidden" } });
  });

  it("ignores messages that do not come from its frame, or that are not the protocol", async () => {
    const calls: Calls = [];
    mockHub(calls);
    render(<PluginFrame plugin={plugin} node="web1" />);
    const { frame, posted } = await frameOf();
    // Another window (here the dashboard itself) is not the page.
    act(() => {
      window.dispatchEvent(new MessageEvent("message", { data: { regulus: 1, type: "request", id: 1, op: "inventory.disks" }, source: window }));
      window.dispatchEvent(new MessageEvent("message", { data: { regulus: 1, type: "ready" }, source: null }));
    });
    // Wrong version, unknown type, bad shapes.
    for (const data of [{ regulus: 2, type: "ready" }, { regulus: 1, type: "delete" }, { regulus: 1, type: "request", id: {}, op: "x" }, "ready", null]) {
      fromFrame(frame, data);
    }
    expect(posted).toEqual([]);
    expect(calls.filter((c) => c.path.endsWith("/call"))).toEqual([]);
  });

  it("sizes the frame to the page, within limits", async () => {
    mockHub([]);
    render(<PluginFrame plugin={plugin} node="web1" />);
    const { frame } = await frameOf();
    fromFrame(frame, { regulus: 1, type: "ready" });
    fromFrame(frame, { regulus: 1, type: "resize", height: 555.2 });
    expect(frame.style.height).toBe("556px");
    fromFrame(frame, { regulus: 1, type: "resize", height: 10_000_000 });
    expect(frame.style.height).toBe("4000px");
  });

  it("says why the page could not load, and tries again", async () => {
    let ok = false;
    const calls: Calls = [];
    mockHub(calls, {
      "/api/plugins/disks/ui": () =>
        Promise.resolve(ok ? { ok: true, status: 200 } : { ok: false, status: 422, json: () => Promise.resolve({ error: "the ui module cannot be shown" }) }),
    });
    render(<PluginFrame plugin={plugin} node="web1" />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Disks could not be shown");
    expect(screen.getByRole("alert")).toHaveTextContent("the ui module cannot be shown");
    expect(screen.queryByTitle("Disks")).not.toBeInTheDocument();
    ok = true;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByTitle("Disks")).toBeInTheDocument();
  });

  it("is in Italian", async () => {
    window.localStorage.setItem("regulus.language", "it");
    mockHub([], { "/api/plugins/disks/ui": () => Promise.resolve({ ok: false, status: 404, json: () => Promise.resolve({}) }) });
    render(<PluginFrame plugin={plugin} node="web1" />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Dischi non può essere mostrato");
    expect(screen.getByRole("button", { name: "Riprova" })).toBeInTheDocument();
  });

  it("fails when the page never says it is ready", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      mockHub([]);
      render(<PluginFrame plugin={plugin} node="web1" />);
      await frameOf();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10_500);
      });
      expect(await screen.findByRole("alert")).toHaveTextContent("did not start in time");
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("the protocol", () => {
  it("accepts only the defined messages", () => {
    expect(parseFrameMessage({ regulus: 1, type: "ready" })).toEqual({ type: "ready" });
    expect(parseFrameMessage({ regulus: 1, type: "resize", height: 200 })).toEqual({ type: "resize", height: 200 });
    expect(parseFrameMessage({ regulus: 1, type: "resize", height: NaN })).toBeNull();
    expect(parseFrameMessage({ regulus: 1, type: "request", id: 1, op: "inventory.disks", params: { node: "a" } })).toEqual({
      type: "request",
      id: 1,
      op: "inventory.disks",
      params: { node: "a" },
    });
    expect(parseFrameMessage({ regulus: 1, type: "request", id: 1, op: "x".repeat(81) })).toBeNull();
    expect(clampHeight(1)).toBe(120);
  });
});
