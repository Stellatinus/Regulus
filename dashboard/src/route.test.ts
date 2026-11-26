import { describe, expect, it } from "vitest";
import { resetTokenFromHash, routeFromHash, serverHref, serverIdFromHash, tabFromHash } from "./route";

describe("routeFromHash", () => {
  it.each([
    ["", "/"],
    ["#", "/"],
    ["#/", "/"],
    ["#/servers", "/servers"],
    ["#/users", "/users"],
    ["#/updates", "/updates"],
    ["#/account", "/account"],
    ["#/servers/agent-1", "/server"],
    ["#/servers/", "/"],
    ["#/reset/abc_DEF-1", "/reset"],
    ["#/reset/", "/"],
    ["#/reset", "/"],
    ["#/settings", "/settings"],
    ["#/settings/updates", "/settings"],
    ["#/users/roles", "/users"],
    ["#/plugins/store", "/plugins"],
    ["#/nowhere", "/"],
  ])("%j is %s", (hash, want) => {
    expect(routeFromHash(hash)).toBe(want);
  });
});

describe("resetTokenFromHash", () => {
  it.each([
    ["#/reset/abc_DEF-1", "abc_DEF-1"],
    ["#/reset/a%20b", "a b"],
    ["#/reset/", ""],
    ["#/users", ""],
    ["", ""],
  ])("%j is %j", (hash, want) => {
    expect(resetTokenFromHash(hash)).toBe(want);
  });
});

describe("server pages", () => {
  it.each([
    ["#/servers/agent-1", "agent-1"],
    ["#/servers/a%2Fb", "a/b"],
    ["#/servers", ""],
    ["#/users", ""],
  ])("%j is %j", (hash, want) => {
    expect(serverIdFromHash(hash)).toBe(want);
  });

  it("builds an address that reads back", () => {
    expect(serverIdFromHash(serverHref("we/ird id"))).toBe("we/ird id");
  });
});

describe("tabFromHash", () => {
  it.each([
    ["#/settings/updates", "updates"],
    ["#/users/roles", "roles"],
    ["#/users/a%20b", "a b"],
    ["#/settings", ""],
    ["#/servers/x", ""],
    ["", ""],
  ])("%j is %j", (hash, want) => {
    expect(tabFromHash(hash)).toBe(want);
  });
});
