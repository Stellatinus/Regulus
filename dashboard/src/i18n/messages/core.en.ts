// Header, navigation and the database banner.
export const core = {
  "app.brand": "Regulus",
  "app.language": "Language",
  "app.languageBrowser": "Browser language",
  "app.theme": "Theme",
  "theme.system": "System",
  "theme.light": "Light",
  "theme.dark": "Dark",

  "nav.main": "Main",
  "nav.dashboard": "Dashboard",
  "nav.servers": "Servers",
  "nav.users": "Users",
  "nav.updates": "Updates",
  "nav.signOut": "Sign out",
  "nav.account": "Account",
  "nav.accountOf": "Account: {name} ({role})",
  "nav.sidebar": "Sidebar",
  "nav.menu": "Menu",
  "nav.collapse": "Collapse the sidebar",
  "nav.expand": "Expand the sidebar",
  "nav.sectionOverview": "Overview",
  "nav.sectionAdmin": "Administration",

  "ui.cancel": "Cancel",
  "ui.actions": "Actions",
  "ui.actionsFor": "Actions for {name}",
  "ui.copied": "Copied",
  "ui.copyManual": "This browser cannot copy here (the hub is not on HTTPS): select the text and press Ctrl+C.",

  "time.day": "d",
  "time.hour": "h",
  "time.minute": "m",
  "time.second": "s",
  "time.ago": "{duration} ago",
  "time.never": "never",

  "database.down":
    "The database is not answering. Signing in and most pages will fail until it is back. The hub keeps running and reconnects by itself.",
  "database.recovered": "The database is answering again.",
} as const;
