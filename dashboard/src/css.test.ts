import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// An unclosed block in styles.css swallows every rule after it (#425: the
// step-up dialog lost its overlay and ended up at the bottom of the page).
describe("styles.css", () => {
  it("has balanced braces", () => {
    const css = readFileSync("src/styles.css", "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
    let depth = 0;
    for (const ch of css) {
      if (ch === "{") depth++;
      if (ch === "}") depth--;
      expect(depth).toBeGreaterThanOrEqual(0);
    }
    expect(depth).toBe(0);
  });
});
