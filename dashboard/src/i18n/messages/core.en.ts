// Header, navigation, the database banner and the last-resort error screen.
export const core = {
  "app.brand": "Regulus",
  "app.language": "Language",
  "app.crashed.title": "Something went wrong",
  "app.crashed.body": "The dashboard hit an error it could not handle. Reloading the page usually fixes it; your servers are not affected.",
  "app.crashed.reload": "Reload the page",
  "app.crashed.details": "Technical details",

  "nav.main": "Main",
  "nav.dashboard": "Dashboard",
  "nav.servers": "Servers",
  "nav.users": "Users",
  "nav.updates": "Updates",
  "nav.signOut": "Sign out",

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
