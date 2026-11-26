import { useSyncExternalStore } from "react";

export type Route = "/" | "/servers" | "/server" | "/plugins" | "/automations" | "/users" | "/updates" | "/account" | "/settings" | "/reset";

const known: readonly Route[] = ["/", "/servers", "/plugins", "/automations", "/users", "/updates", "/account", "/settings"];

const RESET_PREFIX = "/reset/";
const SERVER_PREFIX = "/servers/";

// Pages with tabs keep the tab in the address: "#/settings/updates",
// "#/users/roles". The page is the first part; tabFromHash is the rest.
const TABBED = ["/settings", "/users", "/plugins"];

function baseOf(path: string): string {
  const base = TABBED.find((b) => path.startsWith(`${b}/`));
  return base ?? path;
}

// tabFromHash returns the tab of a page with tabs ("#/settings/updates" is
// "updates"), or "" for the page's first tab.
export function tabFromHash(hash: string): string {
  const path = hash.replace(/^#/, "");
  const base = TABBED.find((b) => path.startsWith(`${b}/`));
  return base ? decodeURIComponent(path.slice(base.length + 1).split("/")[0]) : "";
}

// routeFromHash maps location.hash ("#/servers") to a page. Hash routes keep
// the hub simple: it serves one index.html and needs no fallback rules.
export function routeFromHash(hash: string): Route {
  const path = baseOf(hash.replace(/^#/, "") || "/");
  if (path.startsWith(RESET_PREFIX) && path.length > RESET_PREFIX.length) return "/reset";
  if (path.startsWith(SERVER_PREFIX) && path.length > SERVER_PREFIX.length) return "/server";
  return known.find((r) => r === path) ?? "/";
}

// resetTokenFromHash returns the token of a password reset link
// ("#/reset/<token>"), or "" for any other address. The token is in the
// fragment, so it never reaches the hub's logs or other sites.
export function resetTokenFromHash(hash: string): string {
  const path = hash.replace(/^#/, "");
  return path.startsWith(RESET_PREFIX) ? decodeURIComponent(path.slice(RESET_PREFIX.length)) : "";
}

// serverIdFromHash returns the agent id of a server page ("#/servers/<id>"),
// or "" for any other address.
export function serverIdFromHash(hash: string): string {
  const path = hash.replace(/^#/, "");
  return path.startsWith(SERVER_PREFIX) ? decodeURIComponent(path.slice(SERVER_PREFIX.length)) : "";
}

// serverHref is the address of the page of one server.
export const serverHref = (id: string) => `#${SERVER_PREFIX}${encodeURIComponent(id)}`;

function subscribe(callback: () => void) {
  window.addEventListener("hashchange", callback);
  return () => window.removeEventListener("hashchange", callback);
}

export function useRoute(): Route {
  return routeFromHash(useSyncExternalStore(subscribe, () => window.location.hash));
}

// useTab is the tab of the current page ("" for the first one).
export function useTab(): string {
  return tabFromHash(useSyncExternalStore(subscribe, () => window.location.hash));
}
