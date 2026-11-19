import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Certificates, User } from "./api";
import { CertificateBanner, daysLeft } from "./CertificateBanner";
import { SessionContext } from "./session";
import { ADMIN_PERMISSIONS, VIEWER_PERMISSIONS } from "./test-users";

const now = "2026-01-01T00:00:00Z";
const admin: User = { username: "admin", role: "admin", permissions: ADMIN_PERMISSIONS, created_at: now, disabled: false, two_factor: false };
const viewer: User = { username: "eve", role: "viewer", permissions: VIEWER_PERMISSIONS, created_at: now, disabled: false, two_factor: false };

const end = (days: number) => new Date(Date.now() + days * 86_400_000 + 3_600_000).toISOString();

function mockCerts(data: Certificates) {
  const fetch = vi.fn(() => Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(data) }));
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

function renderAs(user: User) {
  return render(
    <SessionContext.Provider value={{ user, signOut: () => Promise.resolve(), expired: vi.fn() }}>
      <CertificateBanner />
    </SessionContext.Provider>,
  );
}

const ca = { kind: "ca" as const, not_after: end(7000), days_left: 7000 };

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("CertificateBanner", () => {
  it("warns about an agent certificate that ends soon, with the way to renew it", async () => {
    const web = { kind: "agent" as const, agent_id: "a1", name: "web1", not_after: end(30), days_left: 30 };
    mockCerts({ ca, listener: null, soonest: web, agents_unknown: 0, warn_days: 90, warnings: [web] });
    renderAs(admin);
    expect(await screen.findByText("Certificates end soon")).toBeInTheDocument();
    expect(screen.getByText(/The certificate of web1 ends on .*, in 30 days\./)).toBeInTheDocument();
    expect(screen.getByText(/revoke the server and enroll it again/)).toBeInTheDocument();
    expect(screen.queryByText(/a new CA means/)).not.toBeInTheDocument();
  });

  it("shows nothing when nothing ends soon", async () => {
    const fetch = mockCerts({ ca, listener: null, soonest: ca, agents_unknown: 0, warn_days: 90, warnings: [] });
    const { container } = renderAs(admin);
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("does not ask the hub for users who cannot manage servers", () => {
    const fetch = mockCerts({ ca, listener: null, soonest: ca, agents_unknown: 0, warn_days: 90, warnings: [ca] });
    const { container } = renderAs(viewer);
    expect(fetch).not.toHaveBeenCalled();
    expect(container).toBeEmptyDOMElement();
  });
});

describe("daysLeft", () => {
  it("counts whole days and goes negative after the end", () => {
    const t0 = Date.parse("2026-01-01T00:00:00Z");
    expect(daysLeft("2026-01-31T12:00:00Z", t0)).toBe(30);
    expect(daysLeft("2025-12-31T00:00:00Z", t0)).toBe(-1);
  });
});
