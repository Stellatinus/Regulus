import { describe, expect, it } from "vitest";
import { eventText, permissionText, roleText } from "./hubTexts";
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
