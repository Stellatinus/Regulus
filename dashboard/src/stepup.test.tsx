import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ApiError,
  CODE_REQUIRED,
  createEnrollment,
  createUser,
  REAUTH_REQUIRED,
  revokeAgent,
  updateUser,
} from "./api";
import { StepUp } from "./StepUp";

interface Call {
  method: string;
  path: string;
  body: unknown;
}

// mockHub refuses the sensitive requests with reauth_required until
// POST /api/account/reauth succeeds with the right password (and code).
function mockHub(opts: { code?: string } = {}) {
  const calls: Call[] = [];
  let proved = false;
  const answer = (status: number, data: unknown) =>
    Promise.resolve({
      ok: status < 400,
      status,
      headers: new Headers({ "Content-Type": "application/json" }),
      json: () => Promise.resolve(data),
    });
  vi.stubGlobal(
    "fetch",
    vi.fn((path: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      const body = init?.body
        ? (JSON.parse(init.body as string) as Record<string, string>)
        : undefined;
      calls.push({ method, path, body });
      if (path === "/api/account/reauth") {
        if (body?.password !== "right password")
          return answer(403, { error: "the current password is incorrect" });
        if (opts.code && body?.code !== opts.code)
          return answer(403, { error: "invalid code" });
        proved = true;
        return answer(200, { reauth_until: new Date().toISOString() });
      }
      if (!proved)
        return answer(403, {
          error: "confirm your password to do this",
          code: REAUTH_REQUIRED,
        });
      if (method === "DELETE")
        return Promise.resolve({
          ok: true,
          status: 204,
          headers: new Headers(),
          json: () => Promise.resolve(undefined),
        });
      return answer(201, {
        username: "newbie",
        role: "viewer",
        created_at: "",
        disabled: false,
        two_factor: false,
      });
    }),
  );
  return calls;
}

beforeEach(() => {
  window.localStorage.setItem("regulus.language", "en");
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe("step-up re-authentication", () => {
  it("asks for the password and repeats the refused request", async () => {
    const calls = mockHub();
    render(<StepUp twoFactor={false} />);
    const result = createUser("newbie", "a long password", "viewer");

    const dialog = await screen.findByRole("dialog", {
      name: "Confirm it is you",
    });
    expect(dialog).toBeInTheDocument();
    expect(screen.queryByLabelText("Authentication code")).toBeNull();

    await userEvent.type(screen.getByLabelText("Password"), "wrong password");
    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "the current password is incorrect",
    );

    await userEvent.clear(screen.getByLabelText("Password"));
    await userEvent.type(screen.getByLabelText("Password"), "right password");
    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));

    await expect(result).resolves.toMatchObject({ username: "newbie" });
    expect(screen.queryByRole("dialog")).toBeNull();
    const users = calls.filter((c) => c.path === "/api/users");
    expect(users).toHaveLength(2);
    expect(users[1].body).toEqual(users[0].body);
    expect(
      calls.filter((c) => c.path === "/api/account/reauth").at(-1)?.body,
    ).toEqual({ password: "right password" });
  });

  it("sends the code when two-factor is on", async () => {
    const calls = mockHub({ code: "123456" });
    render(<StepUp twoFactor={true} />);
    const result = revokeAgent("agent-1");

    await screen.findByRole("dialog");
    await userEvent.type(screen.getByLabelText("Password"), "right password");
    await userEvent.type(
      screen.getByLabelText("Authentication code"),
      " 123456 ",
    );
    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));

    await expect(result).resolves.toBeUndefined();
    expect(calls.find((c) => c.path === "/api/account/reauth")?.body).toEqual({
      password: "right password",
      code: "123456",
    });
  });

  it("fails the action with the hub's message when cancelled", async () => {
    const calls = mockHub();
    render(<StepUp twoFactor={false} />);
    const result = createUser("newbie", "a long password", "viewer").then(
      () => null,
      (err: unknown) => err,
    );

    await screen.findByRole("dialog");
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    const err = await result;
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 403, code: REAUTH_REQUIRED });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(calls.filter((c) => c.path === "/api/users")).toHaveLength(1);
  });

  it("shares one dialog between requests refused together", async () => {
    const calls = mockHub();
    render(<StepUp twoFactor={false} />);
    const first = createUser("one", "a long password", "viewer");
    const second = createUser("two", "a long password", "viewer");

    await waitFor(() => expect(screen.getAllByRole("dialog")).toHaveLength(1));
    await userEvent.type(screen.getByLabelText("Password"), "right password");
    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));

    await expect(Promise.all([first, second])).resolves.toHaveLength(2);
    expect(calls.filter((c) => c.path === "/api/account/reauth")).toHaveLength(
      1,
    );
  });

  it("is in Italian", async () => {
    window.localStorage.setItem("regulus.language", "it");
    mockHub({ code: "123456" });
    render(<StepUp twoFactor={true} />);
    void createUser("newbie", "a long password", "viewer").catch(() => {});

    expect(
      await screen.findByRole("dialog", { name: "Conferma la tua identità" }),
    ).toBeInTheDocument();
    expect(
      screen.getByLabelText("Codice di autenticazione"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Conferma" }),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Annulla" }));
  });

  it("asks for the code when the hub says two-factor is on", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((path: string, init?: RequestInit) => {
        const body = init?.body
          ? (JSON.parse(init.body as string) as Record<string, string>)
          : {};
        const json = (status: number, data: unknown) =>
          Promise.resolve({
            ok: status < 400,
            status,
            headers: new Headers({ "Content-Type": "application/json" }),
            json: () => Promise.resolve(data),
          });
        if (path === "/api/account/reauth") {
          return body.code
            ? json(200, { reauth_until: "" })
            : json(403, { error: "enter the code", code: CODE_REQUIRED });
        }
        return json(403, { error: "confirm", code: REAUTH_REQUIRED });
      }),
    );
    render(<StepUp twoFactor={false} />);
    void createUser("newbie", "a long password", "viewer").catch(() => {});
    await screen.findByRole("dialog");
    await userEvent.type(screen.getByLabelText("Password"), "right password");
    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));
    expect(
      await screen.findByLabelText("Authentication code"),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
  });

  it("repeats an enrollment token request after the password", async () => {
    const calls = mockHub();
    render(<StepUp twoFactor={false} />);
    const result = createEnrollment("second server", "1h");
    await screen.findByRole("dialog");
    await userEvent.type(screen.getByLabelText("Password"), "right password");
    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await result;
    const tokens = calls.filter((c) => c.path === "/api/enrollment-tokens");
    expect(tokens).toHaveLength(2);
    expect(tokens[1].body).toEqual({ name: "second server", ttl: "1h" });
  });

  it("repeats a role change after the password", async () => {
    const calls = mockHub();
    render(<StepUp twoFactor={false} />);
    const result = updateUser("newbie", { role: "operator" });
    await screen.findByRole("dialog");
    await userEvent.type(screen.getByLabelText("Password"), "right password");
    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await result;
    const patches = calls.filter((c) => c.method === "PATCH");
    expect(patches).toHaveLength(2);
    expect(patches[1].body).toEqual({ role: "operator" });
  });

  it("moves the focus into the dialog, keeps Tab inside, closes on Escape and gives the focus back", async () => {
    mockHub();
    render(
      <>
        <button>opener</button>
        <StepUp twoFactor={false} />
      </>,
    );
    const opener = screen.getByRole("button", { name: "opener" });
    opener.focus();
    const result = createUser("newbie", "a long password", "viewer").then(
      () => null,
      (err: unknown) => err,
    );
    await screen.findByRole("dialog");
    const password = screen.getByLabelText("Password");
    expect(password).toHaveAttribute("type", "password");
    expect(password).toHaveAttribute("autocomplete", "current-password");
    expect(password).toHaveFocus();

    await userEvent.tab({ shift: true });
    expect(screen.getByRole("button", { name: "Cancel" })).toHaveFocus();
    await userEvent.tab();
    expect(password).toHaveFocus();

    await userEvent.keyboard("{Escape}");
    expect(await result).toBeInstanceOf(ApiError);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(opener).toHaveFocus();
  });

  it("without the dialog, the refusal reaches the caller", async () => {
    mockHub();
    await expect(
      createUser("newbie", "a long password", "viewer"),
    ).rejects.toMatchObject({ code: REAUTH_REQUIRED });
  });
});

describe("step-up above another modal dialog", () => {
  it("opens as a modal dialog of its own, so it is in the top layer above the store's window", async () => {
    mockHub();
    const showModal = vi.fn(function (this: HTMLDialogElement) {
      this.setAttribute("open", "");
    });
    const original = HTMLDialogElement.prototype.showModal;
    HTMLDialogElement.prototype.showModal = showModal;
    try {
      render(<StepUp twoFactor={false} />);
      const pending = createUser("newbie", "a long password", "viewer").then(
        () => null,
        (err: unknown) => err,
      );
      const dialog = await screen.findByRole("dialog", {
        name: "Confirm it is you",
      });
      expect(dialog.tagName).toBe("DIALOG");
      expect(showModal).toHaveBeenCalledTimes(1);
      await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
      expect(await pending).toBeInstanceOf(Error);
    } finally {
      HTMLDialogElement.prototype.showModal = original;
    }
  });
});
