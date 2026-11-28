// The macro groups of the audit log: Recent activity is filtered by them
// (#514). A group is not a list of its own: it is read from the name of an
// event, so an event a newer hub adds falls into the group its name says, or
// into "other". A test checks that every event the dashboard has a text for is
// in a group, so a new event is not forgotten here.
export type AuditGroup = "security" | "nodes" | "accounts" | "plugins" | "other";

// The groups in the order they are offered.
export const AUDIT_GROUPS: readonly AuditGroup[] = ["security", "nodes", "accounts", "plugins", "other"];

const RULES: readonly [AuditGroup, RegExp][] = [
  // Sign-in and access: sessions, passwords, two factors, API tokens, setup.
  ["security", /^(login|password_|reauthenticated$|recovery_|session_|setup$|reset_|two_factor_|api_token_)/],
  // Accounts and roles.
  ["accounts", /^(user_|role_)/],
  // Servers and their updates, and the core's updates.
  ["nodes", /^(agent_|agents_|enroll_|update_)/],
  // Plugins (the store) and, later, automations.
  ["plugins", /^(plugin_|automation_)/],
];

// auditGroup is the group of an event.
export function auditGroup(event: string): AuditGroup {
  for (const [group, re] of RULES) if (re.test(event)) return group;
  return "other";
}
