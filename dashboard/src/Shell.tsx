import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { AccountPage } from "./AccountPage";
import { AutomationsPage } from "./AutomationsPage";
import { Dashboard } from "./Dashboard";
import { ServersPage } from "./ServersPage";
import { SettingsPage } from "./SettingsPage";
import { useRoute, type Route } from "./route";
import { can } from "./api";
import { AgentsProvider, useSharedAgents } from "./agents";
import { CertificateBanner } from "./CertificateBanner";
import { useFeature } from "./features";
import { useConnection } from "./LiveProvider";
import { useT, type MessageKey } from "./i18n";
import type { Session } from "./session";
import { UpdatesPage } from "./UpdatesPage";
import { UsersPage } from "./UsersPage";
import { PluginsPage } from "./PluginsPage";
import { icons } from "./icons";

function Page({ route }: { route: Route }) {
  switch (route) {
    case "/servers":
    case "/server":
      return <ServersPage />;
    case "/plugins":
      return <PluginsPage />;
    case "/automations":
      return <AutomationsPage />;
    case "/users":
      return <UsersPage />;
    case "/updates":
      return <UpdatesPage />;
    case "/account":
      return <AccountPage />;
    case "/settings":
      return <SettingsPage />;
    default:
      return <Dashboard />;
  }
}

// How long the live stream may stay down before the indicator turns to the
// error colour (#435). A hub restart takes a few seconds and the dashboard
// reconnects by itself, so a shorter outage is only a warning.
export const LONG_OUTAGE_MS = 60_000;

// The raccord of the notch: a concave curve that joins the island to the top
// edge of the page.
function Fillet({ side }: { side: "left" | "right" }) {
  return (
    <svg className={`notch-fillet ${side}`} viewBox="0 0 28 28" fill="none" aria-hidden="true" focusable="false">
      {side === "left" ? (
        <>
          <path d="M28 0H0C15.464 0 28 12.536 28 28V0Z" fill="currentColor" />
          <path d="M0 0.5C15.188 0.5 27.5 12.812 27.5 28" stroke="var(--line)" strokeWidth="1" />
        </>
      ) : (
        <>
          <path d="M0 0H28C12.536 0 0 12.536 0 28V0Z" fill="currentColor" />
          <path d="M28 0.5C12.812 0.5 0.5 12.812 0.5 28" stroke="var(--line)" strokeWidth="1" />
        </>
      )}
    </svg>
  );
}

// The pill of the notch is the connection indicator too: "6/8 Online" while the
// live stream to the hub flows (so the hub is online and the servers are online
// in part), and the warning colour with "Reconnecting…" while it does not. The
// error colour and its own text come only when the stream stayed down for
// LONG_OUTAGE_MS.
export function MeshPill() {
  const t = useT();
  const shared = useSharedAgents();
  const connection = useConnection();
  const [longOutage, setLongOutage] = useState(false);
  const live = connection === "live";
  useEffect(() => {
    if (live) return;
    const timer = setTimeout(() => setLongOutage(true), LONG_OUTAGE_MS);
    return () => {
      clearTimeout(timer);
      setLongOutage(false);
    };
  }, [live]);
  const agents = shared?.state.kind === "ready" ? shared.state.agents : null;
  const online = agents ? agents.filter((a) => a.status === "connected").length : 0;
  let sev = "var(--ok)";
  let text = agents ? t("shell.online", { online, total: agents.length }) : t("live.connected");
  let title = t("shell.meshTitle");
  if (!live) {
    sev = longOutage ? "var(--crit)" : "var(--warn)";
    text = longOutage ? t("live.failed") : connection === "connecting" ? t("live.connecting") : t("live.lost");
    title = t("live.title");
  }
  return (
    <a className="mesh-pill" href="#/servers" title={title} role="status" style={{ "--sev": sev } as CSSProperties}>
      <span className="pulse-dot" aria-hidden="true" />
      <span>{text}</span>
    </a>
  );
}

function AccountMenu({ user, signOut }: Pick<Session, "user" | "signOut">) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const wrapper = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (wrapper.current && !wrapper.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const initials = user.username.slice(0, 2);
  return (
    <div className="account-wrapper" ref={wrapper}>
      <button type="button" className="account-btn" aria-haspopup="true" aria-expanded={open} aria-label={t("nav.accountOf", { name: user.username, role: user.role })} onClick={() => setOpen((v) => !v)}>
        <span className="account-avatar" aria-hidden="true">
          {initials}
        </span>
        <span className="account-meta">
          <span className="account-name">{user.username}</span>
          <span className="account-role">{user.role}</span>
        </span>
        <span className="account-chevron">{icons.chevron}</span>
      </button>
      <div className={`floating-menu account-dropdown${open ? " open" : ""}`} role="menu" aria-hidden={!open}>
        <div className="account-card">
          <div className="account-card-top">
            <span className="account-avatar" aria-hidden="true">
              {initials}
            </span>
            <div className="account-card-title">{user.username}</div>
          </div>
          <div className="account-card-badge-row">
            <span>{t("shell.accessLevel")}</span>
            <span className="badge-role">{user.role}</span>
          </div>
        </div>
        <a className="menu-item" role="menuitem" href="#/account" tabIndex={open ? 0 : -1} onClick={() => setOpen(false)}>
          {icons.user}
          <span>{t("nav.account")}</span>
        </a>
        <a className="menu-item" role="menuitem" href="#/settings" tabIndex={open ? 0 : -1} onClick={() => setOpen(false)}>
          {icons.settings}
          <span>{t("nav.settings")}</span>
        </a>
        <div className="menu-divider" />
        <button type="button" className="menu-item danger" role="menuitem" tabIndex={open ? 0 : -1} onClick={() => void signOut()}>
          {icons.logout}
          <span>{t("nav.signOut")}</span>
        </button>
      </div>
    </div>
  );
}

// Notch is the top island: the brand, the connection and online-count pill, and
// the account menu (account page, Settings, sign out).
function Notch({ user, signOut }: Pick<Session, "user" | "signOut">) {
  const t = useT();
  return (
    <header className="top-header">
      <div className="top-frame-line" />
      <div className="notch-wrapper">
        <div className="notch-shell">
          <Fillet side="left" />
          <div className="organic-notch">
            <a className="brand-group" href="#/" aria-label={t("app.brand")}>
              <span className="brand-mark" aria-hidden="true">
                <svg width="18" height="18" viewBox="0 0 28 28" fill="none">
                  <polygon points="14,2.5 24,8.25 24,19.75 14,25.5 4,19.75 4,8.25" stroke="currentColor" strokeWidth="1.5" strokeOpacity="0.35" />
                  <path d="M14 6.2L16.3 11.7L21.8 14L16.3 16.3L14 21.8L11.7 16.3L6.2 14L11.7 11.7L14 6.2Z" fill="currentColor" fillOpacity="0.16" stroke="currentColor" strokeWidth="1.55" strokeLinejoin="round" />
                  <circle cx="14" cy="14" r="1.8" fill="currentColor" />
                </svg>
              </span>
              <span className="brand-name">{t("app.brand").toUpperCase()}</span>
            </a>
            <MeshPill />
            <AccountMenu user={user} signOut={signOut} />
          </div>
          <Fillet side="right" />
        </div>
      </div>
    </header>
  );
}

interface DockItem {
  id: string;
  href: string;
  label: MessageKey;
  icon: ReactNode;
  routes: Route[];
}

// Dock is the floating bar at the bottom with the sections. A section that
// needs a permission shows only to those who hold one of them; Automations
// show only while the setting is on.
function Dock({ user, route }: { user: Session["user"]; route: Route }) {
  const t = useT();
  const [automations] = useFeature("automations");
  const canUsers = ["users.manage", "roles.manage", "audit.read"].some((p) => can(user, p));
  const canUpdates = can(user, "core.update");
  const adminHref = canUsers ? "#/users" : canUpdates ? "#/updates" : null;

  const items: DockItem[] = [
    { id: "dashboard", href: "#/", label: "nav.dashboard", icon: icons.dashboard, routes: ["/"] },
    { id: "nodes", href: "#/servers", label: "nav.nodes", icon: icons.nodes, routes: ["/servers", "/server"] },
  ];
  if (can(user, "servers.read")) items.push({ id: "services", href: "#/plugins", label: "nav.services", icon: icons.services, routes: ["/plugins"] });
  if (automations) items.push({ id: "automations", href: "#/automations", label: "nav.automations", icon: icons.automations, routes: ["/automations"] });

  return (
    <nav className="bottom-dock-nav" aria-label={t("nav.main")}>
      <div className="floating-dock">
        {items.map((item) => (
          <a key={item.id} className="dock-item" href={item.href} aria-current={item.routes.includes(route) ? "page" : undefined} title={t(item.label)}>
            {item.icon}
            <span className="dock-label">{t(item.label)}</span>
          </a>
        ))}
        {adminHref && (
          <>
            <span className="dock-divider" aria-hidden="true" />
            <a className="dock-item" href={adminHref} aria-current={route === "/users" || route === "/updates" ? "page" : undefined} title={t("nav.admin")}>
              {icons.admin}
              <span className="dock-label">{t("nav.admin")}</span>
            </a>
          </>
        )}
      </div>
    </nav>
  );
}

// AdminTabs switches between the pages of the Admin section. It shows above
// them, only with more than one page to choose from.
function AdminTabs({ user, route }: { user: Session["user"]; route: Route }) {
  const t = useT();
  const tabs: { route: Route; href: string; label: MessageKey; allowed: boolean }[] = [
    { route: "/users", href: "#/users", label: "nav.users", allowed: ["users.manage", "roles.manage", "audit.read"].some((p) => can(user, p)) },
    { route: "/updates", href: "#/updates", label: "nav.updates", allowed: can(user, "core.update") },
  ];
  const shown = tabs.filter((x) => x.allowed);
  if (shown.length < 2) return null;
  return (
    <nav className="admin-tabs" aria-label={t("nav.admin")}>
      {shown.map((x) => (
        <a key={x.route} href={x.href} aria-current={route === x.route ? "page" : undefined}>
          {t(x.label)}
        </a>
      ))}
    </nav>
  );
}

// Shell is the frame of the signed-in dashboard: the notch on top (brand,
// online count, live state, settings, account), the current page, and the dock
// at the bottom with the sections.
export function Shell({ user, signOut }: Session) {
  const route = useRoute();
  const admin = route === "/users" || route === "/updates";
  return (
    <AgentsProvider>
      <Notch user={user} signOut={signOut} />
      <main className="main-container">
        <CertificateBanner />
        {admin && <AdminTabs user={user} route={route} />}
        <Page route={route} />
      </main>
      <Dock user={user} route={route} />
    </AgentsProvider>
  );
}
