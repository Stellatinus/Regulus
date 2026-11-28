import { describe, expect, it } from "vitest";
import { auditGroup } from "./auditGroups";
import { catalogs } from "./i18n";

describe("auditGroup", () => {
  it("puts an event in the group its name says", () => {
    expect(auditGroup("login")).toBe("security");
    expect(auditGroup("two_factor_enabled")).toBe("security");
    expect(auditGroup("api_token_created")).toBe("security");
    expect(auditGroup("user_created")).toBe("accounts");
    expect(auditGroup("role_deleted")).toBe("accounts");
    expect(auditGroup("agent_enrolled")).toBe("nodes");
    expect(auditGroup("update_applied")).toBe("nodes");
    expect(auditGroup("plugin_installed")).toBe("plugins");
    expect(auditGroup("something_new")).toBe("other");
  });

  it("knows a group for every event the dashboard has a text for, so a new event is not forgotten", () => {
    const events = Object.keys(catalogs.en)
      .filter((k) => k.startsWith("users.event."))
      .map((k) => k.slice("users.event.".length));
    expect(events.length).toBeGreaterThan(30);
    // backup_restored is the one event that belongs to no group yet.
    const ungrouped = events.filter((e) => auditGroup(e) === "other");
    expect(ungrouped).toEqual(["backup_restored"]);
  });
});
