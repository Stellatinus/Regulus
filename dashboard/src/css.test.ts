// @ts-expect-error the dashboard has no Node type definitions; vitest runs in Node
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// The hub of the test is the working directory: vitest runs from dashboard/.
const stylesheet = (): string => readFileSync("src/styles.css", "utf8") as string;

// An unclosed block in styles.css swallows every rule after it (#425: the
// step-up dialog lost its overlay and ended up at the bottom of the page).
// rule returns the declarations of the first rule with exactly this selector.
function rule(css: string, selector: string): string {
  const start = css.indexOf(`${selector} {`);
  expect(start, `rule ${selector}`).toBeGreaterThanOrEqual(0);
  return css.slice(start, css.indexOf("}", start));
}

describe("styles.css", () => {
  it("makes the widget title a drag handle that cannot be selected, in edit mode only (#430)", () => {
    const css = stylesheet();
    const handle = rule(css, ".grid.editing .widget-title");
    expect(handle).toContain("user-select: none");
    expect(handle).toContain("-webkit-user-select: none");
    expect(handle).toContain("cursor: grab");
    expect(rule(css, ".widget-title")).not.toContain("user-select");
    expect(rule(css, ".widget-body")).not.toContain("user-select");
  });

  it("has balanced braces", () => {
    const css = stylesheet().replace(/\/\*[\s\S]*?\*\//g, "");
    let depth = 0;
    for (const ch of css) {
      if (ch === "{") depth++;
      if (ch === "}") depth--;
      expect(depth).toBeGreaterThanOrEqual(0);
    }
    expect(depth).toBe(0);
  });
});
