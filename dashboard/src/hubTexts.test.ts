import { describe, expect, it } from "vitest";
import { applyMessageText, auditDetailKeys, auditDetailText, auditEntryDetail, eventText, rolloutMessageText, permissionText, roleText } from "./hubTexts";
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

// #368: an entry written with a code is translated from the code, not from the
// English words; the fallbacks keep old rows and newer hubs readable.
describe("the detail of an audit entry with a code", () => {
  it("is translated from the code and its arguments", () => {
    const e = { event: "role_changed", detail: "viewer to admin", code: "from_to", args: { from: "viewer", to: "admin" } };
    // The English words are deliberately different: the code decides.
    expect(auditEntryDetail(tIn("it"), { ...e, detail: "not the usual words" })).toBe("viewer → admin");
    expect(auditEntryDetail(tIn("it"), { event: "update_rollout_finished", detail: "x", code: "rollout_state", args: { state: "failed" } })).toBe("fallito");
    expect(auditEntryDetail(tIn("it"), { event: "update_rollout_started", detail: "x", code: "rollout_started", args: { servers: "3", wave: "2" } })).toBe("3 server, 2 alla volta");
  });

  it("shows the permissions of a role by their texts", () => {
    const e = { event: "role_created", detail: "audit.read, servers.read", code: "permissions", args: { permissions: "audit.read,servers.read" } };
    expect(auditEntryDetail(tIn("en"), e)).toBe("Read the audit log, See the servers, their state and their metrics");
    expect(auditEntryDetail(tIn("it"), e)).toContain("Leggere il registro delle attività");
  });

  // #372
  it("covers the update settings, a failed download and a failed password check", () => {
    expect(auditEntryDetail(tIn("it"), { event: "update_settings_changed", detail: "x", code: "update_settings_window", args: { mode: "automatic", channel: "stable", from: "02:00", to: "04:00" } })).toBe("modalità automatic, canale stable, finestra 02:00-04:00");
    expect(auditEntryDetail(tIn("it"), { event: "password_change_failed", detail: "re-authentication", code: "reauthentication" })).toBe("nuova autenticazione");
    expect(auditEntryDetail(tIn("en"), { event: "update_download_failed", detail: "boom", code: "update_download_failed", args: { error: "boom" } })).toBe("boom");
  });

  it("shows the helper's message of a failed update from the updates catalog", () => {
    const e = { event: "update_apply_failed", detail: "from 1.0.0: English", code: "from_version_message", args: { version: "1.0.0", message: "English", apply_code: "swap_failed", a_error: "disk full" } };
    expect(auditEntryDetail(tIn("it"), e)).toBe("dalla 1.0.0: Non è stato possibile mettere a posto il nuovo programma: disk full");
    // an old helper's result has no apply_code: the English message stays
    expect(auditEntryDetail(tIn("it"), { ...e, args: { version: "1.0.0", message: "English" } })).toBe("dalla 1.0.0: English");
  });

  it("falls back to the forms, then to the hub's text, for an entry without a known code", () => {
    expect(auditEntryDetail(tIn("it"), { event: "role_changed", detail: "viewer to admin" })).toBe("viewer → admin");
    expect(auditEntryDetail(tIn("it"), { event: "x", detail: "free text", code: "from_a_newer_hub" })).toBe("free text");
  });
});

describe("the messages of the installer and of a rollout step", () => {
  it("follow the language by code and keep the text without one", () => {
    expect(applyMessageText(tIn("it"), { message: "x", code: "not_newer", args: { version: "1.0.0", installed: "1.1.0" } })).toContain("1.0.0");
    expect(applyMessageText(tIn("it"), { message: "x", code: "not_newer", args: { version: "1.0.0", installed: "1.1.0" } })).toContain("non è più recente");
    expect(applyMessageText(tIn("it"), { message: "an older helper's text" })).toBe("an older helper's text");
    expect(applyMessageText(tIn("it"), { message: "kept", code: "from_a_newer_helper" })).toBe("kept");
    expect(rolloutMessageText(tIn("it"), { message: "x", code: "not_connected" })).toBe("Il server non è collegato.");
    expect(rolloutMessageText(tIn("en"), { message: "the agent's own error" })).toBe("the agent's own error");
  });
});
