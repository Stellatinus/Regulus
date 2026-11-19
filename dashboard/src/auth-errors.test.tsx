import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, parseRetryAfter } from "./api";
import { AuthGate, authErrorText, BUSY_RETRIES } from "./AuthGate";
import { translate, type MessageKey } from "./i18n";

const json = (body: unknown, status = 200, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });

// hub answers /api/auth/me with me and every other request with the next
// answer of the list (the last one again when the list runs out).
function hub(me: unknown, ...answers: (() => Response)[]) {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string) => {
      if (path === "/api/auth/me") return Promise.resolve(json(me));
      calls.push(path);
      const next = answers.length > 1 ? answers.shift()! : answers[0];
      return Promise.resolve(next());
    }),
  );
  return calls;
}

const user = { username: "admin", role: "admin", created_at: "2026-01-01T00:00:00Z", disabled: false, two_factor: false };

async function signIn() {
  fireEvent.change(await screen.findByLabelText(/^(Username|Nome utente)$/), { target: { value: "admin" } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: "secret-password" } });
  fireEvent.click(screen.getByRole("button", { name: /^(Sign in|Accedi)$/ }));
}

const t = (key: MessageKey, params?: Record<string, string | number>) => translate("en", key, params);

describe("authErrorText", () => {
  it("maps each status of the auth forms to a translated text", () => {
    expect(authErrorText(t, "login", new ApiError(401, "invalid username or password"))).toBe("Wrong username or password.");
    expect(authErrorText(t, "code", new ApiError(401, "x", "invalid_code"))).toBe("The code is not valid. Check it and try again.");
    expect(authErrorText(t, "code", new ApiError(401, "x", "login_expired"))).toBe("This sign-in expired. Go back and sign in again.");
    expect(authErrorText(t, "setup", new ApiError(403, "invalid setup token"))).toMatch(/setup token is not valid/);
    expect(authErrorText(t, "setup", new ApiError(409, "setup was already completed"))).toMatch(/already created/);
    expect(authErrorText(t, "login", new ApiError(429, "x", undefined, 900))).toBe("Too many failed attempts. Try again in 15 minutes.");
    expect(authErrorText(t, "code", new ApiError(429, "x", undefined, 30))).toBe("Too many failed attempts. Try again in a minute.");
    expect(authErrorText(t, "login", new ApiError(429, "x"))).toBe("Too many failed attempts. Try again later.");
    expect(authErrorText(t, "setup", new ApiError(503, "busy", undefined, 5))).toMatch(/still busy/);
  });

  it("shows a generic request error in the form's language", () => {
    const err = new ApiError(0, "Nessuna risposta…", "network_error", undefined, {
      key: "request.network",
      params: { method: "POST", path: "/api/auth/login" },
    });
    expect(authErrorText(t, "login", err)).toMatch(/^No answer from the hub to POST \/api\/auth\/login\./);
  });

  it("keeps the hub's text for answers without a key of their own", () => {
    expect(authErrorText(t, "setup", new ApiError(400, "password must be at least 10 characters"))).toBe(
      "password must be at least 10 characters",
    );
  });
});

describe("parseRetryAfter", () => {
  it("reads seconds and HTTP dates", () => {
    expect(parseRetryAfter("900")).toBe(900);
    expect(parseRetryAfter(" 5 ")).toBe(5);
    const now = Date.parse("2026-11-19T10:00:00Z");
    expect(parseRetryAfter("Thu, 19 Nov 2026 10:02:00 GMT", now)).toBe(120);
    expect(parseRetryAfter("Thu, 19 Nov 2026 09:00:00 GMT", now)).toBe(0);
    expect(parseRetryAfter(null)).toBeUndefined();
    expect(parseRetryAfter("soon")).toBeUndefined();
  });
});

describe("sign-in errors", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
    window.localStorage.removeItem("regulus.language");
  });

  describe("in Italian", () => {
    beforeEach(() => {
      window.localStorage.setItem("regulus.language", "it");
    });

    it("says the credentials are wrong on a 401", async () => {
      hub({ setup_required: false, user: null }, () => json({ error: "invalid username or password" }, 401));
      render(<AuthGate>{() => <p>inside</p>}</AuthGate>);
      await signIn();
      expect(await screen.findByRole("alert")).toHaveTextContent("Nome utente o password errati.");
    });

    it("states the wait from Retry-After on a 429", async () => {
      hub({ setup_required: false, user: null }, () =>
        json({ error: "too many failed attempts, try again later" }, 429, { "Retry-After": "900" }),
      );
      render(<AuthGate>{() => <p>inside</p>}</AuthGate>);
      await signIn();
      expect(await screen.findByRole("alert")).toHaveTextContent("Troppi tentativi non riusciti. Riprova tra 15 minuti.");
    });

    it("tells a wrong code from an expired sign-in at the code step", async () => {
      hub(
        { setup_required: false, user: null },
        () => json({ two_factor_required: true, pending_token: "p", expires_at: "2026-11-19T10:05:00Z" }),
        () => json({ error: "the code is not valid", code: "invalid_code" }, 401),
        () => json({ error: "this sign-in expired: sign in again", code: "login_expired" }, 401),
      );
      render(<AuthGate>{() => <p>inside</p>}</AuthGate>);
      await signIn();
      fireEvent.change(await screen.findByLabelText("Codice di autenticazione"), { target: { value: "000000" } });
      fireEvent.click(screen.getByRole("button", { name: "Verifica" }));
      expect(await screen.findByText("Il codice non è valido. Controllalo e riprova.")).toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "Verifica" }));
      expect(await screen.findByText("Questo accesso è scaduto. Torna indietro e accedi di nuovo.")).toBeInTheDocument();
    });

    it("states the wait from Retry-After on a 429 at the code step", async () => {
      hub(
        { setup_required: false, user: null },
        () => json({ two_factor_required: true, pending_token: "p", expires_at: "2026-11-19T10:05:00Z" }),
        () => json({ error: "too many failed attempts, try again later" }, 429, { "Retry-After": "900" }),
      );
      render(<AuthGate>{() => <p>inside</p>}</AuthGate>);
      await signIn();
      fireEvent.change(await screen.findByLabelText("Codice di autenticazione"), { target: { value: "000000" } });
      fireEvent.click(screen.getByRole("button", { name: "Verifica" }));
      expect(await screen.findByText("Troppi tentativi non riusciti. Riprova tra 15 minuti.")).toBeInTheDocument();
    });
  });

  describe("in English", () => {
    beforeEach(() => {
      window.localStorage.setItem("regulus.language", "en");
    });

    it("waits Retry-After on a 503 and tries again by itself", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const calls = hub(
        { setup_required: false, user: null },
        () => json({ error: "the server is busy checking passwords, try again in a moment" }, 503, { "Retry-After": "5" }),
        () => json(user),
      );
      render(<AuthGate>{() => <p>inside</p>}</AuthGate>);
      await signIn();
      expect(await screen.findByRole("alert")).toHaveTextContent("The hub is busy checking passwords. Trying again in 5 seconds…");
      expect(screen.getByRole("button", { name: "Sign in" })).toBeDisabled();
      expect(calls).toHaveLength(1);
      await act(() => vi.advanceTimersByTimeAsync(5000));
      expect(calls).toEqual(["/api/auth/login", "/api/auth/login"]);
    });

    it("gives up after a few busy answers", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const calls = hub({ setup_required: false, user: null }, () =>
        json({ error: "busy" }, 503, { "Retry-After": "1" }),
      );
      render(<AuthGate>{() => <p>inside</p>}</AuthGate>);
      await signIn();
      await screen.findByRole("alert");
      for (let i = 0; i < BUSY_RETRIES; i++) await act(() => vi.advanceTimersByTimeAsync(1000));
      expect(await screen.findByRole("alert")).toHaveTextContent("The hub is still busy checking passwords. Try again in a moment.");
      expect(calls).toHaveLength(BUSY_RETRIES + 1);
      expect(screen.getByRole("button", { name: "Sign in" })).toBeEnabled();
    });

    it("explains a wrong setup token", async () => {
      hub(
        { setup_required: true, user: null },
        () => json({ error: "invalid setup token" }, 403),
      );
      render(<AuthGate>{() => <p>inside</p>}</AuthGate>);
      fireEvent.change(await screen.findByLabelText("Setup token"), { target: { value: "wrong" } });
      fireEvent.change(screen.getByLabelText("Username"), { target: { value: "admin" } });
      fireEvent.change(screen.getByLabelText("Password (at least 10 characters)"), { target: { value: "secret-password" } });
      fireEvent.change(screen.getByLabelText("Repeat the password"), { target: { value: "secret-password" } });
      fireEvent.click(screen.getByRole("button", { name: "Create the admin" }));
      expect(await screen.findByRole("alert")).toHaveTextContent("The setup token is not valid. Copy it again from the hub log.");
    });
  });
});
