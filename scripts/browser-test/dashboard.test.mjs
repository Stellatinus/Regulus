// The dashboard in a real browser, against the real hub binary and real agents
// (#390). Run it with `make browser-test`; see docs/development.md for what it
// covers and what stays manual.
//
// The scenarios share one hub and run in order: the first creates the admin
// through the page, the others sign in as that admin. Each scenario gets a
// browser context of its own, with a Playwright trace; a failed one leaves a
// screenshot, the trace and the console in browser-test-artifacts/ (a CI
// artifact), a passing one leaves nothing.
import assert from "node:assert/strict";
import { after, before, describe, it } from "node:test";
import { BIN, PASSWORD, chromium, keepFailure, startAgent, startHub, totp } from "./harness.mjs";

let hub;
let browser;
let secret; // the two-factor secret, once the scenario that makes it has run
const cspViolations = [];

before(async () => {
  hub = await startHub();
  browser = await chromium.launch({ executablePath: process.env.REGULUS_BROWSER || undefined });
});

after(async () => {
  await browser?.close();
  await hub?.cleanup();
});

// scenario runs fn(page, context) in a fresh context. options are the context
// options (viewport, locale…).
async function scenario(name, fn, options = {}) {
  const context = await browser.newContext({ baseURL: hub.base, ...options });
  await context.tracing.start({ screenshots: true, snapshots: true });
  const page = await context.newPage();
  const consoleLines = [];
  page.on("console", (m) => {
    consoleLines.push(`${m.type()}: ${m.text()}`);
    if (/content security policy/i.test(m.text())) cspViolations.push(`${name}: ${m.text()}`);
  });
  page.on("pageerror", (e) => consoleLines.push(`pageerror: ${e.message}`));
  try {
    await fn(page, context);
    await context.tracing.stop();
  } catch (error) {
    await keepFailure(name, context, page, consoleLines, error);
    throw error;
  } finally {
    await context.close();
  }
}

async function signIn(page, code) {
  await page.goto("/");
  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  if (code) {
    await page.getByLabel("Authentication code").fill(code);
    await page.getByRole("button", { name: "Verify" }).click();
  }
  await page.getByRole("link", { name: "Nodes" }).waitFor();
}

const luminance = (rgb) => {
  const [r, g, b] = rgb.match(/\d+/g).map(Number);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};

describe("the dashboard in a real browser", { concurrency: false }, () => {
  it("creates the first admin from the setup token", () =>
    scenario("first admin", async (page) => {
      await page.goto("/");
      await page.getByRole("heading", { name: "Create the admin account" }).waitFor();
      await page.getByLabel("Username").fill("admin");
      await page.getByLabel("Password (at least 10 characters)").fill(PASSWORD);
      await page.getByLabel("Repeat the password").fill(PASSWORD);
      // A wrong token is refused with a sentence, and nothing is created.
      await page.getByLabel("Setup token").fill("not-the-token");
      await page.getByRole("button", { name: "Create the admin" }).click();
      await page.getByText("The setup token is not valid").waitFor();
      await page.getByLabel("Setup token").fill(hub.setupToken);
      await page.getByRole("button", { name: "Create the admin" }).click();
      await page.getByRole("link", { name: "Nodes" }).waitFor();
    }));

  it("shows a connected agent live, without a reload", () =>
    scenario("live stream", async (page, context) => {
      await signIn(page);
      await page.goto("/#/servers");
      await page.getByRole("heading", { name: "Nodes" }).first().waitFor();
      assert.equal(await page.getByRole("link", { name: "Open the node web1" }).count(), 0, "no server yet");
      // The page is open on a live stream (a WebSocket) before the agent exists.
      await page.getByText("Online", { exact: true }).waitFor();

      await context.request.post("/api/account/reauth", { data: { password: PASSWORD } });
      const res = await context.request.post("/api/enrollment-tokens", { data: { name: "web1" } });
      assert.equal(res.status(), 201, await res.text());
      const { token } = await res.json();
      await startAgent(hub, "web1", token);

      // The card appears and turns online by itself: the page was not reloaded.
      await page.getByRole("link", { name: "Open the node web1" }).waitFor({ timeout: 30000 });
      await page.getByText("online", { exact: true }).waitFor({ timeout: 30000 });
    }));

  it("switches between English and Italian", () =>
    scenario("language", async (page) => {
      await signIn(page);
      // The language is chosen in Settings, behind the cog of the notch.
      await page.getByRole("link", { name: "Settings" }).click();
      await page.getByLabel("Language").selectOption("it");
      await page.getByRole("link", { name: "Nodi" }).waitFor();
      assert.equal(await page.getByRole("link", { name: "Nodes" }).count(), 0);
      assert.equal(await page.locator("html").getAttribute("lang"), "it");
      await page.getByLabel("Lingua").selectOption("en");
      await page.getByRole("link", { name: "Nodes" }).waitFor();
      assert.equal(await page.locator("html").getAttribute("lang"), "en");
    }));

  it("has a light and a dark theme", () =>
    scenario("themes", async (page) => {
      await signIn(page);
      await page.getByRole("link", { name: "Settings" }).click();
      const background = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
      await page.getByRole("button", { name: "Light" }).click();
      assert.equal(await page.locator("html").getAttribute("data-theme"), "light");
      const light = luminance(await background());
      await page.getByRole("button", { name: "Dark" }).click();
      assert.equal(await page.locator("html").getAttribute("data-theme"), "dark");
      const dark = luminance(await background());
      assert.ok(light > 200 && dark < 80 && light > dark, `light ${light}, dark ${dark}`);
    }));

  it("fits a phone screen without a horizontal scrollbar", () =>
    scenario(
      "phone layout",
      async (page) => {
        await signIn(page);
        const routes = ["/", "/servers", "/plugins", "/users", "/updates", "/account", "/settings"];
        for (const r of routes) {
          await page.goto(`/#${r}`);
          await page.waitForLoadState("networkidle");
          const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
          assert.ok(over <= 0, `${r}: the page is ${over}px wider than the screen`);
        }
        // The page of a server too, with its charts.
        await page.goto("/#/servers");
        await page.getByRole("link", { name: "Open the node web1" }).click();
        await page.getByRole("heading", { name: "CPU" }).waitFor({ timeout: 30000 });
        // Measure the settled page, as for the other routes: the disks panel
        // (and its badges) arrives from its own request.
        await page.waitForLoadState("networkidle");
        const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
        assert.ok(over <= 0, `the server page is ${over}px wider than the screen`);
      },
      { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true },
    ));

  it("shows a refused sign-in in Italian, then lets the next try through", () =>
    scenario(
      "sign-in errors",
      async (page) => {
        await page.addInitScript(() => localStorage.setItem("regulus.language", "it"));
        // The hub says "too many attempts, wait two minutes" (429).
        await page.route("**/api/auth/login", (route) =>
          route.fulfill({ status: 429, headers: { "Retry-After": "120" }, contentType: "application/json", body: JSON.stringify({ error: "too many failed attempts", code: "too_many_attempts" }) }),
        );
        await page.goto("/");
        await page.getByLabel("Nome utente").fill("admin");
        await page.getByLabel("Password").fill(PASSWORD);
        await page.getByRole("button", { name: "Accedi" }).click();
        await page.getByText("Troppi tentativi non riusciti. Riprova tra 2 minuti.").waitFor();
        await page.unroute("**/api/auth/login");

        // A wrong password through the real hub: a sentence in Italian.
        await page.getByLabel("Password").fill("not the password");
        await page.getByRole("button", { name: "Accedi" }).click();
        await page.getByText("Nome utente o password errati.").waitFor();

        // The hub is busy (503 with Retry-After): the page says so, waits, and
        // asks again by itself; the second answer is the real one.
        let calls = 0;
        await page.route("**/api/auth/login", (route) => {
          calls++;
          if (calls === 1) {
            return route.fulfill({ status: 503, headers: { "Retry-After": "1" }, contentType: "application/json", body: JSON.stringify({ error: "busy", code: "login_busy" }) });
          }
          return route.continue();
        });
        await page.getByLabel("Password").fill(PASSWORD);
        await page.getByRole("button", { name: "Accedi" }).click();
        await page.getByText("L'hub è occupato a verificare le password.").waitFor();
        await page.getByRole("link", { name: "Nodi" }).waitFor({ timeout: 30000 });
        assert.equal(calls, 2, "the page asked again once");
      },
      { locale: "en-US" },
    ));

  it("asks for the two-factor code at sign-in", () =>
    scenario("two-factor", async (page) => {
      await signIn(page);
      await page.goto("/#/account");
      await page.getByLabel("Your password", { exact: true }).fill(PASSWORD);
      await page.getByRole("button", { name: "Set up two-factor authentication" }).click();
      secret = (await page.getByLabel("Secret key").textContent()).trim();
      await page.getByLabel("2. Enter the 6-digit code the app shows").fill(totp(secret));
      await page.getByRole("button", { name: "Turn on two-factor authentication" }).click();
      await page.getByText("Recovery codes", { exact: false }).first().waitFor();

      await page.getByRole("button", { name: /^Account: admin/ }).click();
      await page.getByRole("menuitem", { name: "Sign out" }).click();
      // Password alone is not enough; the next step of the clock gives a code
      // that was not used to turn it on.
      await page.getByLabel("Username").fill("admin");
      await page.getByLabel("Password").fill(PASSWORD);
      await page.getByRole("button", { name: "Sign in" }).click();
      await page.getByRole("heading", { name: "Two-factor authentication" }).waitFor();
      await page.getByLabel("Authentication code").fill("000000");
      await page.getByRole("button", { name: "Verify" }).click();
      await page.getByText("The code is not valid").waitFor();
      await page.getByLabel("Authentication code").fill(totp(secret, Date.now() + 30000));
      await page.getByRole("button", { name: "Verify" }).click();
      await page.getByRole("link", { name: "Nodes" }).waitFor();
    }));

  it("raised no Content-Security-Policy violation in any page it visited", () => {
    assert.deepEqual(cspViolations, []);
    assert.ok(BIN);
  });
});
