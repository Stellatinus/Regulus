import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, fetchMe, fetchMetrics, INVALID_RESPONSE } from "./api";

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
