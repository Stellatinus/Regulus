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
