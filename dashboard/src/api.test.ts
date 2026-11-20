import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, apiErrorText, fetchMe, fetchMetrics, INVALID_RESPONSE, NETWORK_ERROR } from "./api";
import { translate, type MessageKey } from "./i18n";

afterEach(() => {
  vi.unstubAllGlobals();
});

const answer = (body: string, status: number, type?: string) =>
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve(new Response(body, { status, headers: type ? { "Content-Type": type } : {} }))),
  );

describe("request", () => {
  it("turns a 200 HTML page (a proxy or captive portal) into an ApiError", async () => {
    answer("<!doctype html><title>Sign in to the Wi-Fi</title>", 200, "text/html; charset=utf-8");
    const err: unknown = await fetchMe().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 200, code: INVALID_RESPONSE });
    expect((err as Error).message).toMatch(/not JSON \(text\/html/);
  });

  it("turns a 2xx answer that does not parse into an ApiError, not a SyntaxError", async () => {
    answer("<html>", 200);
    await expect(fetchMe()).rejects.toMatchObject({ name: "ApiError", code: INVALID_RESPONSE });
    answer("{broken", 200, "application/json");
    await expect(fetchMe()).rejects.toMatchObject({ name: "ApiError", code: INVALID_RESPONSE });
  });

  it("still reads JSON and the hub's errors", async () => {
    answer(JSON.stringify({ setup_required: false, user: null }), 200, "application/json");
    await expect(fetchMe()).resolves.toEqual({ setup_required: false, user: null });
    answer(JSON.stringify({ error: "nope" }), 500, "application/json");
    await expect(fetchMe()).rejects.toMatchObject({ status: 500, message: "nope" });
  });
});

describe("generic request errors", () => {
  afterEach(() => {
    window.localStorage.removeItem("regulus.language");
  });

  const en = (key: MessageKey, params?: Record<string, string | number>) => translate("en", key, params);

  it("are catalog texts in the page's language, and can be translated again", async () => {
    window.localStorage.setItem("regulus.language", "it");
    answer("<html>", 200, "text/html");
    const notJSON: unknown = await fetchMe().catch((e: unknown) => e);
    expect(notJSON).toMatchObject({ code: INVALID_RESPONSE, messageKey: "request.notJson" });
    expect((notJSON as Error).message).toBe(
      "La risposta a GET /api/auth/me non è JSON (text/html); potrebbe esserci di mezzo un proxy o un captive portal.",
    );
    expect(apiErrorText(en, notJSON)).toBe(
      "The answer to GET /api/auth/me is not JSON (text/html); a proxy or captive portal may be in the way.",
    );

    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve({ ok: true, status: 200, headers: new Headers(), json: () => Promise.reject(new SyntaxError("x")) })),
    );
    await expect(fetchMe()).rejects.toMatchObject({
      message: "La risposta a GET /api/auth/me non è JSON (non ha un tipo di contenuto); potrebbe esserci di mezzo un proxy o un captive portal.",
    });

    answer("Bad Gateway", 502, "text/plain");
    const failed: unknown = await fetchMe().catch((e: unknown) => e);
    expect(failed).toMatchObject({ status: 502, message: "L'hub ha risposto 502 a GET /api/auth/me, senza una spiegazione." });
    expect(apiErrorText(en, failed)).toBe("The hub answered 502 to GET /api/auth/me, without an explanation.");
  });

  it("turn a failed fetch into an ApiError with status 0", async () => {
    window.localStorage.setItem("regulus.language", "it");
    vi.stubGlobal("fetch", vi.fn(() => Promise.reject(new TypeError("Failed to fetch"))));
    const err: unknown = await fetchMe().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 0, code: NETWORK_ERROR, messageKey: "request.network" });
    expect((err as Error).message).toBe(
      "Nessuna risposta dall'hub a GET /api/auth/me. Controlla la connessione di rete e che l'hub sia in funzione.",
    );
    expect(apiErrorText(en, err)).toMatch(/^No answer from the hub to GET \/api\/auth\/me\./);
  });

  it("let an abort through as it is", async () => {
    const controller = new AbortController();
    controller.abort();
    vi.stubGlobal("fetch", vi.fn(() => Promise.reject(new DOMException("aborted", "AbortError"))));
    await expect(fetchMe(controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  });

  it("keep the hub's own explanation", async () => {
    answer(JSON.stringify({ error: "nope" }), 500, "application/json");
    const err: unknown = await fetchMe().catch((e: unknown) => e);
    expect(err).toMatchObject({ message: "nope", messageKey: undefined });
    expect(apiErrorText(en, err)).toBe("nope");
  });
});

describe("fetchMetrics", () => {
  it("asks for the newest points and drops reports it cannot read", async () => {
    const good = {
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
      disks: null,
    };
    answer(JSON.stringify({ agent: "a", points: [good, { at: "2026-01-01T00:00:01Z" }, null] }), 200, "application/json");
    const points = await fetchMetrics("a", "2025-12-31T00:00:00Z", undefined, 600);
    expect(points).toEqual([{ ...good, disks: [], networks: [] }]);
    expect(vi.mocked(fetch).mock.calls[0][0]).toBe("/api/agents/a/metrics?since=2025-12-31T00%3A00%3A00Z&limit=600");
  });
});
