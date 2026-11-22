import { catalogs, type MessageKey } from "./i18n";

type Translate = (key: MessageKey, params?: Record<string, string | number>) => string;

// Some texts come from the hub in English and name a thing by an id: the
// permissions, the built-in roles and the events of the audit log. The
// dashboard shows them from its catalogs ("users.perm.<id>",
// "users.roleDesc.<name>", "users.event.<event>") so that they follow the
// language of the page (#125). An id without a text (a newer hub's) shows the
// hub's own text, as before.
function known(t: Translate, key: string, fallback: string): string {
  return key in catalogs.en ? t(key as MessageKey) : fallback;
}

// permissionText is what a permission allows, in the language of t.
export const permissionText = (t: Translate, id: string, hubText: string) => known(t, `users.perm.${id}`, hubText);

// roleText is the description of a role: a built-in one comes from the
// catalog, one made by an administrator is what they wrote.
export const roleText = (t: Translate, name: string, builtin: boolean, hubText: string) =>
  builtin ? known(t, `users.roleDesc.${name}`, hubText) : hubText;

// eventText is the name of an audit event, such as "role created".
export const eventText = (t: Translate, event: string) => known(t, `users.event.${event}`, event.replace(/_/g, " "));

// The `detail` of an audit entry is free text the hub writes in English from a
// few fixed forms (internal/auth, internal/hub, internal/update). The hub
// stores the English text, so the dashboard recognises the forms and shows
// them from its catalogs ("users.detail.<name>") (#354). A detail it does not
// recognise (the permission list of a role, the explanation of an update that
// failed, a form added by a newer hub) is shown as the hub wrote it. This is a
// stopgap: the proper fix is to store a code and arguments with the entry; see
// docs/decisions.md ("GUI translations").
const DETAIL_FORMS: { event?: string; re: RegExp; key: string; params?: (m: RegExpMatchArray) => Record<string, string | number> }[] = [
  { event: "user_created", re: /^role (.+)$/, key: "users.detail.role", params: (m) => ({ role: m[1] }) },
  { event: "role_changed", re: /^(.+) to (.+)$/, key: "users.detail.fromTo", params: (m) => ({ from: m[1], to: m[2] }) },
  { event: "update_agent_updated", re: /^(.+) to (.+)$/, key: "users.detail.fromTo", params: (m) => ({ from: m[1], to: m[2] }) },
  { re: /^other sessions ended$/, key: "users.detail.otherSessionsEnded" },
  { re: /^reset link used, sessions ended$/, key: "users.detail.resetLinkUsed" },
  { re: /^sessions ended$/, key: "users.detail.sessionsEnded" },
  { re: /^first admin$/, key: "users.detail.firstAdmin" },
  { re: /^reset-admin command$/, key: "users.detail.resetAdminCommand" },
  { re: /^wrong second factor$/, key: "users.detail.wrongSecondFactor" },
  { re: /^account disabled$/, key: "users.detail.accountDisabled" },
  { re: /^recovery code$/, key: "users.detail.recoveryCode" },
  { event: "agents_listener_on", re: /^port (\d+)$/, key: "users.detail.port", params: (m) => ({ port: m[1] }) },
  { event: "update_rollout_started", re: /^(\d+) servers?, (\d+) at a time$/, key: "users.detail.rolloutStarted", params: (m) => ({ servers: m[1], wave: m[2] }) },
  { event: "update_rollout_finished", re: /^(stopped|done|failed)$/, key: "users.detail.rolloutState" },
  { event: "update_apply_requested", re: /^from (.+)$/, key: "users.detail.fromVersion", params: (m) => ({ version: m[1] }) },
  { event: "role_updated", re: /^renamed from ([^;]+); (.*)$/, key: "users.detail.roleRenamed", params: (m) => ({ name: m[1], permissions: m[2] }) },
];

// auditDetailText is the detail of an audit entry in the language of t, or the
// hub's own text when it is not one of the known forms.
export function auditDetailText(t: Translate, event: string, detail: string): string {
  for (const f of DETAIL_FORMS) {
    if (f.event !== undefined && f.event !== event) continue;
    const m = detail.match(f.re);
    if (!m) continue;
    if (f.key === "users.detail.rolloutState") return known(t, `users.detail.rollout.${m[1]}`, detail);
    return f.key in catalogs.en ? t(f.key as MessageKey, f.params?.(m)) : detail;
  }
  return detail;
}

// auditEntryDetail is the detail of an audit entry in the language of t. An
// entry the hub wrote with a code (#368) is shown from the catalog
// ("users.detail.code.<code>", with its arguments); the permission ids in its
// arguments are shown by their texts. An entry with no code, or a code this
// dashboard does not know (a newer hub's), falls back to the English-form
// recognition above, and then to the hub's own text.
export function auditEntryDetail(t: Translate, e: { event: string; detail: string; code?: string; args?: Record<string, string> }): string {
  if (e.code && `users.detail.code.${e.code}` in catalogs.en) {
    const args = { ...(e.args ?? {}) };
    if (args.permissions !== undefined) {
      args.permissions = args.permissions
        .split(",")
        .filter((id) => id !== "")
        .map((id) => permissionText(t, id, id))
        .join(", ");
    }
    if (e.code === "rollout_state") return known(t, `users.detail.rollout.${args.state}`, e.detail);
    return t(`users.detail.code.${e.code}` as MessageKey, args);
  }
  return auditDetailText(t, e.event, e.detail);
}

// The message the installer on a server wrote (result.json), or a rollout
// step's message, in the language of t: from the catalog when it has a code,
// else as the hub or the helper wrote it (an older helper's, or a failure that
// has no code).
export function applyMessageText(t: Translate, m: { message?: string; code?: string; args?: Record<string, string> }): string {
  const key = `updates.applyMsg.${m.code ?? ""}`;
  return m.code && key in catalogs.en ? t(key as MessageKey, m.args ?? {}) : (m.message ?? "");
}

export function rolloutMessageText(t: Translate, m: { message?: string; code?: string; args?: Record<string, string> }): string {
  const key = `updates.rolloutMsg.${m.code ?? ""}`;
  return m.code && key in catalogs.en ? t(key as MessageKey, m.args ?? {}) : (m.message ?? "");
}

// auditDetailKeys lists the catalog keys the forms above use, for the test that
// checks both languages have each.
export const auditDetailKeys = [
  ...DETAIL_FORMS.map((f) => f.key).filter((k) => k !== "users.detail.rolloutState"),
  "users.detail.rollout.stopped",
  "users.detail.rollout.done",
  "users.detail.rollout.failed",
];
