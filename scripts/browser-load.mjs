// Measures the dashboard in a real browser while a hub holds many agents (#370).
//
// 1. Build the dashboard into the binary (make build) and start the hub with
//    simulated agents that stay connected:
//      REGULUS_E2E_BIN=bin/regulus REGULUS_LOAD=1 REGULUS_LOAD_AGENTS=50 \
//      REGULUS_LOAD_DASHBOARDS=0 REGULUS_LOAD_INTERVAL=10s REGULUS_LOAD_SECONDS=1 \
//      REGULUS_LOAD_HOLD=5m go test -run TestHubUnderRealLoad -v -count=1 ./internal/e2e/
//    It prints "HOLD ...: dashboard http://127.0.0.1:PORT, user admin, ...".
// 2. node scripts/browser-load.mjs http://127.0.0.1:PORT [seconds]
//
// It signs in as admin, opens the Nodes page, and for the given time (60 s)
// records, from Chromium itself: the time until every server is on the page,
// the JavaScript heap (after a forced garbage collection) and the DOM size at
// the start and at the end, the time the page spent running scripts, laying
// out and painting per second while live events arrive, and the longest
// tasks. It also saves a heap snapshot and prints its size. This is not the
// React Profiler (that needs a profiling build of React and the DevTools):
// it says how much the tab costs, not which component does.
//
// Needs the `playwright` package and a Chromium (the cloud session has both).
import { createRequire } from "node:module";

// require, not import: it finds a globally installed playwright through NODE_PATH.
const { chromium } = createRequire(import.meta.url)("playwright");

const base = process.argv[2];
const seconds = Number(process.argv[3] ?? 60);
if (!Number.isFinite(seconds) || seconds <= 0) {
  console.error("seconds must be a positive number");
  process.exit(2);
}
const password = process.env.REGULUS_LOAD_PASSWORD ?? "e2e-test-password";
if (!base) {
  console.error("usage: node scripts/browser-load.mjs http://127.0.0.1:PORT [seconds]");
  process.exit(2);
}

const browser = await chromium.launch({ args: ["--enable-precise-memory-info", "--js-flags=--expose-gc"] });
const page = await browser.newPage();
const cdp = await page.context().newCDPSession(page);
await cdp.send("Performance.enable");

// Long tasks (over 50 ms), collected inside the page.
await page.addInitScript(() => {
  window.__long = [];
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) window.__long.push(e.duration);
  }).observe({ entryTypes: ["longtask"] });
});

const metrics = async () => {
  const { metrics } = await cdp.send("Performance.getMetrics");
  return Object.fromEntries(metrics.map((m) => [m.name, m.value]));
};
const gc = async () => {
  await cdp.send("HeapProfiler.collectGarbage");
  await cdp.send("HeapProfiler.collectGarbage");
};

const t0 = Date.now();
await page.goto(base);
await page.getByLabel("Username").fill("admin");
await page.getByLabel("Password").fill(password);
await page.getByRole("button", { name: "Sign in" }).click();
await page.getByRole("link", { name: "Nodes" }).first().click();
// Every connected server is a card that links to its window (#/servers/<id>): wait for the list to stop growing.
let rows = 0;
for (let i = 0; i < 120; i++) {
  const now = await page.locator('main a[href^="#/servers/"]').count();
  if (now > 0 && now === rows) break;
  rows = now;
  await page.waitForTimeout(500);
}
const ready = Date.now() - t0;
if (rows === 0) {
  console.error("the Nodes page showed no entries: the page did not load or nobody is connected, so there is nothing to measure");
  process.exit(1);
}

await gc();
const start = await metrics();
const dom0 = start.Nodes;
console.log(`signed in and the Nodes page showed ${rows} entries ${(ready / 1000).toFixed(1)} s after the first request`);
console.log(`start: heap ${(start.JSHeapUsedSize / 2 ** 20).toFixed(1)} MiB, ${start.Nodes} DOM nodes, ${start.JSEventListeners} event listeners`);

await page.waitForTimeout(seconds * 1000);

const mid = await metrics();
await gc();
const end = await metrics();
const long = await page.evaluate(() => window.__long);
const per = (k) => (((mid[k] - start[k]) / seconds) * 1000).toFixed(1);
console.log(`over ${seconds} s with live events: scripts ${per("ScriptDuration")} ms/s, layout ${per("LayoutDuration")} ms/s, style ${per("RecalcStyleDuration")} ms/s, all tasks ${per("TaskDuration")} ms/s (of 1000 ms/s)`);
console.log(`end: heap ${(end.JSHeapUsedSize / 2 ** 20).toFixed(1)} MiB after a forced GC (${((end.JSHeapUsedSize - start.JSHeapUsedSize) / 2 ** 20).toFixed(1)} MiB more than at the start), ${end.Nodes} DOM nodes (${end.Nodes - dom0} more), ${end.JSEventListeners} event listeners`);
console.log(`long tasks (over 50 ms): ${long.length}${long.length ? `, longest ${Math.max(...long).toFixed(0)} ms` : ""}`);

let size = 0;
cdp.on("HeapProfiler.addHeapSnapshotChunk", (e) => (size += e.chunk.length));
await cdp.send("HeapProfiler.takeHeapSnapshot", { reportProgress: false });
console.log(`heap snapshot: ${(size / 2 ** 20).toFixed(1)} MiB`);
await browser.close();
