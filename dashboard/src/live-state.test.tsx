import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FakeSocket } from "./fake-socket";
import { LiveProvider } from "./LiveProvider";
import { LONG_OUTAGE_MS, MeshPill } from "./Shell";

const answer = (data: unknown) =>
  Promise.resolve({ ok: true, status: 200, headers: new Headers({ "Content-Type": "application/json" }), json: () => Promise.resolve(data) });

function show() {
  return render(
    <LiveProvider createSocket={(url) => new FakeSocket(url)}>
      <MeshPill />
    </LiveProvider>,
  );
}

const flush = () => act(async () => void (await vi.advanceTimersByTimeAsync(0)));

beforeEach(() => {
  vi.useFakeTimers();
  FakeSocket.reset();
  vi.stubGlobal("fetch", vi.fn(() => answer([])));
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

// The indicator after a hub restart (#435), now the pill of the notch: green
// ("6/8 Online", or "Online" before the servers are read) while the stream flows, the warning colour while it reconnects, the error colour only
// after a long outage.
describe("the notch pill as the connection indicator", () => {
  it.each([
    ["en", "Online", "Reconnecting…", "Hub unreachable, retrying…"],
    ["it", "Online", "Riconnessione…", "Hub non raggiungibile, nuovo tentativo…"],
  ])("in %s says online, then warns, then fails only after a long outage", async (lang, online, reconnecting, failed) => {
    window.localStorage.setItem("regulus.language", lang);
    show();
    await flush();
    act(() => FakeSocket.last.open());
    const state = screen.getByRole("status");
    expect(state).toHaveTextContent(online);
    expect(state.style.getPropertyValue("--sev")).toBe("var(--ok)");

    // The hub restarts: the stream drops.
    act(() => FakeSocket.last.drop());
    expect(screen.getByRole("status")).toHaveTextContent(reconnecting);
    expect(screen.getByRole("status").style.getPropertyValue("--sev")).toBe("var(--warn)");

    await act(async () => void (await vi.advanceTimersByTimeAsync(LONG_OUTAGE_MS - 1000)));
    expect(screen.getByRole("status").style.getPropertyValue("--sev")).not.toBe("var(--crit)");

    await act(async () => void (await vi.advanceTimersByTimeAsync(2000)));
    expect(screen.getByRole("status")).toHaveTextContent(failed);
    expect(screen.getByRole("status").style.getPropertyValue("--sev")).toBe("var(--crit)");
  });

  it("goes back to online, and to the warning colour again, when the hub is back", async () => {
    window.localStorage.setItem("regulus.language", "en");
    show();
    await flush();
    act(() => FakeSocket.last.open());
    act(() => FakeSocket.last.drop());
    await act(async () => void (await vi.advanceTimersByTimeAsync(LONG_OUTAGE_MS + 1000)));
    expect(screen.getByRole("status").style.getPropertyValue("--sev")).toBe("var(--crit)");

    await act(async () => void (await vi.advanceTimersByTimeAsync(20_000)));
    act(() => FakeSocket.last.open());
    expect(screen.getByRole("status")).toHaveTextContent("Online");
    expect(screen.getByRole("status").style.getPropertyValue("--sev")).toBe("var(--ok)");

    act(() => FakeSocket.last.drop());
    expect(screen.getByRole("status").style.getPropertyValue("--sev")).toBe("var(--warn)");
  });
});
