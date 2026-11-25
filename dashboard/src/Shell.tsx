import { useEffect, useState, type ReactNode } from "react";
import { AccountPage } from "./AccountPage";
import { Dashboard } from "./Dashboard";
import { ServerPage } from "./ServerPage";
import { ServersPage } from "./ServersPage";
import { useRoute, type Route } from "./route";
import { can } from "./api";
import { LanguageSelect, ThemeSwitch } from "./Appearance";
import { CertificateBanner } from "./CertificateBanner";
import { useConnection } from "./LiveProvider";
import { useT, type MessageKey } from "./i18n";
import type { Session } from "./session";
import { UpdatesPage } from "./UpdatesPage";
import { UsersPage } from "./UsersPage";
import { PluginsPage } from "./PluginsPage";

// Icons are drawn inline (no icon library): 24-unit strokes in currentColor.
const icon = (d: ReactNode) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    {d}
  </svg>
);

const icons = {
  dashboard: icon(
    <>
      <rect x="3" y="3" width="7" height="9" rx="1.5" />
      <rect x="14" y="3" width="7" height="5" rx="1.5" />
      <rect x="14" y="12" width="7" height="9" rx="1.5" />
      <rect x="3" y="16" width="7" height="5" rx="1.5" />
    </>,
  ),
  servers: icon(
    <>
      <rect x="3" y="4" width="18" height="7" rx="1.5" />
      <rect x="3" y="13" width="18" height="7" rx="1.5" />
      <path d="M7 7.5h.01M7 16.5h.01M11 7.5h6M11 16.5h6" />
    </>,
  ),
  users: icon(
    <>
      <circle cx="9" cy="8" r="3.5" />
      <path d="M2.5 20c.8-3.6 3.4-5.5 6.5-5.5s5.7 1.9 6.5 5.5" />
      <path d="M16 4.5a3.5 3.5 0 0 1 0 7M18 14.8c1.8.8 3 2.5 3.5 5.2" />
    </>,
  ),
  updates: icon(
    <>
      <path d="M20 12a8 8 0 1 1-2.4-5.7" />
      <path d="M20 4v5h-5" />
      <path d="M12 8v5l3 2" />
    </>,
  ),
  plugins: icon(
    <>
      <path d="M9 3v4M15 3v4" />
      <path d="M6 7h12v4a6 6 0 0 1-12 0z" />
      <path d="M12 17v4" />
    </>,
  ),
  menu: icon(<path d="M4 7h16M4 12h16M4 17h16" />),
  chevron: icon(<path d="M15 6l-6 6 6 6" />),
};

// A page with permissions is shown to anyone who holds one of them.
const nav: { route: Route; label: MessageKey; icon: ReactNode; section: "overview" | "admin"; permissions?: string[] }[] = [
  { route: "/", label: "nav.dashboard", icon: icons.dashboard, section: "overview" },
  { route: "/servers", label: "nav.servers", icon: icons.servers, section: "overview" },
  { route: "/plugins", label: "nav.plugins", icon: icons.plugins, section: "overview", permissions: ["servers.read"] },
  { route: "/users", label: "nav.users", icon: icons.users, section: "admin", permissions: ["users.manage", "roles.manage", "audit.read"] },
  { route: "/updates", label: "nav.updates", icon: icons.updates, section: "admin", permissions: ["core.update"] },
];

const titles: Record<Route, MessageKey> = {
  "/": "nav.dashboard",
  "/servers": "nav.servers",
  "/server": "nav.servers",
  "/plugins": "nav.plugins",
  "/users": "nav.users",
  "/updates": "nav.updates",
  "/account": "nav.account",
  "/reset": "nav.account",
};

function Page({ route }: { route: Route }) {
  switch (route) {
    case "/servers":
      return <ServersPage />;
    case "/server":
      return <ServerPage />;
    case "/plugins":
      return <PluginsPage />;
    case "/users":
      return <UsersPage />;
    case "/updates":
      return <UpdatesPage />;
    case "/account":
      return <AccountPage />;
    default:
      return <Dashboard />;
  }
}

const COLLAPSE_KEY = "regulus.sidebar";

function savedCollapsed(): boolean {
  try {
    return window.localStorage.getItem(COLLAPSE_KEY) === "collapsed";
  } catch {
    return false;
  }
}

// How long the live stream may stay down before the indicator turns to the
// error colour (#435). A hub restart takes a few seconds and the dashboard
// reconnects by itself, so a shorter outage is only a warning.
export const LONG_OUTAGE_MS = 60_000;

// LiveState shows whether the live updates (/api/stream) are flowing:
// colour and a word, announced politely when it changes. Connected is green
// ("Online"); connecting or reconnecting is the warning colour; the error
// colour and its own text come only when the stream stayed down for
// LONG_OUTAGE_MS.
export function LiveState() {
  const t = useT();
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
  const look = live
    ? { sev: "var(--ok)", text: t("live.connected") }
    : longOutage
        ? { sev: "var(--crit)", text: t("live.failed") }
      : connection === "connecting"
          ? { sev: "var(--warn)", text: t("live.connecting") }
          : { sev: "var(--warn)", text: t("live.lost") };
  return (
    <span className="live-state" role="status" style={{ "--sev": look.sev } as React.CSSProperties} title={t("live.title")}>
      <i aria-hidden="true" />
      {look.text}
    </span>
  );
}

// Shell is the frame of the signed-in dashboard: the sidebar with the
// navigation and the account, the top bar with the page title and the
// appearance switches, and the current page. Below 52rem the sidebar becomes a
// drawer opened by the menu button.
export function Shell({ user, signOut }: Session) {
  const route = useRoute();
  const t = useT();
  const [collapsed, setCollapsed] = useState(savedCollapsed);
  // The drawer is open on the page where it was opened: following a link
  // closes it.
  const [openOn, setOpenOn] = useState<Route | null>(null);
  const open = openOn === route;
  const setOpen = (v: boolean) => setOpenOn(v ? route : null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpenOn(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  const toggleCollapsed = () => {
    const next = !collapsed;
    setCollapsed(next);
    try {
      window.localStorage.setItem(COLLAPSE_KEY, next ? "collapsed" : "open");
    } catch {
      // not saved
    }
  };

  const visible = nav.filter((n) => !n.permissions || n.permissions.some((p) => can(user, p)));
  const current = (r: Route) => route === r || (r === "/servers" && route === "/server");
  const sections: { id: "overview" | "admin"; label: MessageKey }[] = [
    { id: "overview", label: "nav.sectionOverview" },
    { id: "admin", label: "nav.sectionAdmin" },
  ];

  const classes = ["shell", collapsed && "collapsed", open && "open"].filter(Boolean).join(" ");
  return (
    <div className={classes}>
      {open && <div className="scrim" onClick={() => setOpen(false)} aria-hidden="true" />}
      <aside className="side" id="sidebar" aria-label={t("nav.sidebar")}>
        <a className="brand" href="#/" aria-label={t("app.brand")}>
          <i aria-hidden="true" />
          <span className="txt">{t("app.brand")}</span>
        </a>
        <nav aria-label={t("nav.main")}>
          {sections.map((s) => {
            const items = visible.filter((n) => n.section === s.id);
            if (items.length === 0) return null;
            return (
              <div key={s.id}>
                <div className="navsec" aria-hidden="true">
                  {t(s.label)}
                </div>
                {items.map((n) => (
                  <a key={n.route} className="nv" href={`#${n.route}`} aria-current={current(n.route) ? "page" : undefined} title={collapsed ? t(n.label) : undefined}>
                    {n.icon}
                    <span className="txt">{t(n.label)}</span>
                  </a>
                ))}
              </div>
            );
          })}
        </nav>
        <div className="sp" />
        <a className="me" href="#/account" aria-current={route === "/account" ? "page" : undefined} aria-label={t("nav.accountOf", { name: user.username, role: user.role })}>
          <span className="av" aria-hidden="true">
            {user.username.slice(0, 2)}
          </span>
          <div>
            <b>{user.username}</b>
            <small>
              {user.role} · {t("nav.account")}
            </small>
          </div>
          <span aria-hidden="true">→</span>
        </a>
        <div className="side-actions">
          <button type="button" className="signout" onClick={() => void signOut()}>
            {t("nav.signOut")}
          </button>
          <button type="button" className="collapse" onClick={toggleCollapsed} aria-pressed={collapsed} aria-label={collapsed ? t("nav.expand") : t("nav.collapse")} title={collapsed ? t("nav.expand") : t("nav.collapse")}>
            {icons.chevron}
          </button>
        </div>
      </aside>
      <div className="page">
        <header className="topbar">
          <button type="button" className="menu-btn" aria-controls="sidebar" aria-expanded={open} aria-label={t("nav.menu")} onClick={() => setOpen(!open)}>
            {icons.menu}
          </button>
          <div className="crumb">
            <span aria-hidden="true">{t("app.brand")} /</span>
            <strong>{t(titles[route] ?? "nav.dashboard")}</strong>
          </div>
          <LiveState />
          <ThemeSwitch />
          <LanguageSelect />
        </header>
        <main>
          <CertificateBanner />
          <Page route={route} />
        </main>
      </div>
    </div>
  );
}
