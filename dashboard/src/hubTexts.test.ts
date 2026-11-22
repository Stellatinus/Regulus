import { describe, expect, it } from "vitest";
import { auditDetailKeys, auditDetailText, eventText, permissionText, roleText } from "./hubTexts";
import { catalogs } from "./i18n";
import { translate, type Lang, type MessageKey } from "./i18n";

const tIn = (lang: Lang) => (key: MessageKey, params?: Record<string, string | number>) => translate(lang, key, params);

// #125: what the hub names by an id is shown in the language of the page.
describe("texts the hub sends in English", () => {
  it("permissions and built-in roles follow the language", () => {
    expect(permissionText(tIn("en"), "audit.read", "hub text")).toBe("Read the audit log");
    expect(permissionText(tIn("it"), "audit.read", "hub text")).toBe("Leggere il registro delle attività");
    expect(roleText(tIn("it"), "viewer", true, "Read only")).toBe("Sola lettura");
    expect(roleText(tIn("en"), "viewer", true, "hub text")).toBe("Read only");
  });

  it("a role made by an administrator keeps what they wrote, even with a built-in name's text", () => {
    expect(roleText(tIn("it"), "viewer", false, "My own words")).toBe("My own words");
  });

  it("audit events are named, and an unknown one stays readable", () => {
    expect(eventText(tIn("en"), "role_created")).toBe("role created");
    expect(eventText(tIn("it"), "role_created")).toBe("ruolo creato");
    expect(eventText(tIn("it"), "something_from_a_newer_hub")).toBe("something from a newer hub");
    expect(permissionText(tIn("it"), "plugins.run", "From a newer hub")).toBe("From a newer hub");
  });
});

// #354: the fixed forms of an audit entry's detail follow the language.
describe("the detail of an audit entry", () => {
  it("is translated for the forms the hub writes, and left alone otherwise", () => {
    expect(auditDetailText(tIn("it"), "role_changed", "viewer to admin")).toBe("viewer → admin");
    expect(auditDetailText(tIn("it"), "user_created", "role operator")).toBe("ruolo operator");
    expect(auditDetailText(tIn("it"), "password_changed", "other sessions ended")).toBe("le altre sessioni sono state chiuse");
    expect(auditDetailText(tIn("en"), "update_rollout_started", "3 servers, 1 at a time")).toBe("3 servers, 1 at a time");
    expect(auditDetailText(tIn("it"), "update_rollout_started", "3 servers, 2 at a time")).toBe("3 server, 2 alla volta");
    expect(auditDetailText(tIn("it"), "update_rollout_finished", "failed")).toBe("fallito");
    expect(auditDetailText(tIn("it"), "agents_listener_on", "port 8443")).toBe("porta 8443");
    expect(auditDetailText(tIn("it"), "update_apply_requested", "from 0.2.0")).toBe("dalla 0.2.0");
    // A form of another event, an unknown form and a free text stay as written.
    expect(auditDetailText(tIn("it"), "login", "role x")).toBe("role x");
    expect(auditDetailText(tIn("it"), "update_download_failed", "the release server said 502")).toBe("the release server said 502");
  });

  it("has a text in both languages for every form", () => {
    for (const key of auditDetailKeys) {
      expect(key in catalogs.en, `English text for ${key}`).toBe(true);
      expect(key in catalogs.it, `Italian text for ${key}`).toBe(true);
    }
  });
});
