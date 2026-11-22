// Helpers of the browser tests (#390): start the real hub binary and real
// agents, compute a two-factor code, keep what a failed scenario leaves behind.
import { spawn } from "node:child_process";
import { createHmac } from "node:crypto";
import { createRequire } from "node:module";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

// require, not import: it finds a globally installed playwright through NODE_PATH.
export const { chromium } = createRequire(import.meta.url)("playwright");

export const PASSWORD = "browser-test-password";
export const BIN = resolve(process.env.REGULUS_BIN ?? "bin/regulus");
export const ARTIFACTS = resolve(process.env.REGULUS_BROWSER_ARTIFACTS ?? "browser-test-artifacts");

// A process of the real binary, its JSON log kept in memory.
export class Proc {
  constructor(args, env = {}) {
    this.lines = [];
    this.exited = false;
    this.child = spawn(BIN, [...args, "--log-format", "json"], {
      // Never reach out for releases, and keep REGULUS_* of the caller out.
      env: { PATH: process.env.PATH, HOME: process.env.HOME, REGULUS_UPDATES_CHECK: "false", ...env },
      stdio: ["ignore", "pipe", "pipe"],
    });
    let pending = "";
    const take = (chunk) => {
      pending += chunk.toString();
      const parts = pending.split("\n");
      pending = parts.pop();
      for (const l of parts) this.lines.push(l);
    };
    this.child.stdout.on("data", take);
    this.child.stderr.on("data", take);
    this.child.on("exit", () => (this.exited = true));
  }
  // field returns key of the newest log line with this message.
  field(msg, key) {
    for (let i = this.lines.length - 1; i >= 0; i--) {
      try {
        const o = JSON.parse(this.lines[i]);
        if (o.msg === msg || o.msg?.startsWith(msg)) return o[key];
      } catch {
        // not a JSON line
      }
    }
    return undefined;
  }
  log() {
    return this.lines.join("\n");
  }
  async waitFor(what, fn, seconds = 60) {
    const end = Date.now() + seconds * 1000;
    for (;;) {
      const v = fn();
      if (v) return v;
      if (this.exited) throw new Error(`the process exited while waiting for ${what}:\n${this.log()}`);
      if (Date.now() > end) throw new Error(`${what} did not happen within ${seconds}s:\n${this.log()}`);
      await new Promise((r) => setTimeout(r, 100));
    }
  }
  async stop() {
    if (this.exited) return;
    this.child.kill("SIGTERM");
    await new Promise((r) => {
      const t = setTimeout(() => (this.child.kill("SIGKILL"), r()), 10000);
      this.child.on("exit", () => (clearTimeout(t), r()));
    });
  }
}

export function freePort() {
  return 20000 + Math.floor(Math.random() * 20000);
}

// startHub starts the hub on loopback with a temporary data directory and no
// built-in agent, with the agent listener on, and waits until it listens.
export async function startHub() {
  const dir = mkdtempSync(join(tmpdir(), "regulus-browser-"));
  const hub = new Proc([
    "hub", "--listen", "127.0.0.1:0", "--agent-listen", `127.0.0.1:${freePort()}`,
    "--data-dir", join(dir, "hub"), "--builtin-agent=false", "--metrics-interval", "1s",
  ]);
  hub.dir = dir;
  const http = await hub.waitFor("the hub address", () => hub.field("hub listening", "addr"));
  hub.base = `http://${http}`;
  hub.agentAddr = await hub.waitFor("the agent listener", () => hub.field("agent listener ready", "addr"));
  hub.ca = hub.field("agent listener ready", "ca");
  hub.setupToken = await hub.waitFor("the setup token", () => hub.field("no accounts yet", "setup_token"));
  hub.agents = [];
  hub.cleanup = async () => {
    for (const a of hub.agents) await a.stop();
    await hub.stop();
    rmSync(dir, { recursive: true, force: true });
  };
  return hub;
}

// startAgent enrolls a real agent with a token (made through the API of a
// signed-in browser context) and waits until the hub log says it connected.
export async function startAgent(hub, name, token) {
  const agent = new Proc([
    "agent", "--hub", hub.agentAddr, "--token", token, "--ca-fingerprint", hub.ca,
    "--data-dir", join(hub.dir, `agent-${name}`),
  ]);
  hub.agents.push(agent);
  await agent.waitFor("the agent to connect", () => agent.field("connected to the hub", "agent"));
  return agent;
}

// totp is the code an authenticator app shows: RFC 6238, SHA-1, 30 s, 6 digits.
export function totp(secret, atMs = Date.now()) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const c of secret.replace(/[\s=]/g, "").toUpperCase()) bits += alphabet.indexOf(c).toString(2).padStart(5, "0");
  const key = Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(atMs / 30000)));
  const h = createHmac("sha1", key).update(counter).digest();
  const o = h[h.length - 1] & 15;
  const n = ((h[o] & 0x7f) << 24) | (h[o + 1] << 16) | (h[o + 2] << 8) | h[o + 3];
  return String(n % 1000000).padStart(6, "0");
}

// keepFailure saves what a failed scenario leaves for the CI artifact: a
// screenshot, the Playwright trace and the console of the page.
export async function keepFailure(name, context, page, consoleLines, error) {
  mkdirSync(ARTIFACTS, { recursive: true });
  const slug = name.replace(/[^a-z0-9]+/gi, "-").toLowerCase();
  try {
    await page.screenshot({ path: join(ARTIFACTS, `${slug}.png`), fullPage: true });
  } catch {
    // the page may be gone
  }
  try {
    await context.tracing.stop({ path: join(ARTIFACTS, `${slug}.trace.zip`) });
  } catch {
    // tracing may not have started
  }
  writeFileSync(join(ARTIFACTS, `${slug}.txt`), `${error?.stack ?? error}\n\n--- console ---\n${consoleLines.join("\n")}\n`);
}
