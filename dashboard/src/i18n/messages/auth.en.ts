// Texts of first-run setup, sign-in and the password reset page (AuthGate, ResetPage).
// Keys start with "auth.". Add the Italian text in auth.it.ts.
export const auth = {
  "auth.loading": "Loading…",
  "auth.unreachable": "Could not reach the hub: {message}",
  "auth.tagline": "CodexCircle Server Manager",
  "auth.passwordsMismatch": "The passwords do not match.",

  "auth.enroll.title": "Set up two-factor authentication",
  "auth.enroll.intro": "This hub requires two-factor authentication. You can use Regulus once it is on.",
  "auth.signOut": "Sign out",

  "auth.login.title": "Sign in",
  "auth.login.username": "Username",
  "auth.login.password": "Password",
  "auth.login.submit": "Sign in",
  "auth.login.forgot": "Forgot your password? Ask an administrator for a reset link.",

  "auth.code.title": "Two-factor authentication",
  "auth.code.label": "Authentication code",
  "auth.code.hint": "Enter the 6-digit code from your authenticator app, or one of your recovery codes.",
  "auth.code.verify": "Verify",
  "auth.code.back": "Back",

  "auth.error.credentials": "Wrong username or password.",
  "auth.error.code": "The code is not valid. Check it and try again.",
  "auth.error.expired": "This sign-in expired. Go back and sign in again.",
  "auth.error.tooMany": "Too many failed attempts. Try again in {minutes} minutes.",
  "auth.error.tooManyOne": "Too many failed attempts. Try again in a minute.",
  "auth.error.tooManyLater": "Too many failed attempts. Try again later.",
  "auth.error.busy": "The hub is busy checking passwords. Trying again in {seconds} seconds…",
  "auth.error.busyGaveUp": "The hub is still busy checking passwords. Try again in a moment.",
  "auth.error.setupToken": "The setup token is not valid. Copy it again from the hub log.",
  "auth.error.setupDone": "The admin account was already created. Reload the page to sign in.",

  "auth.recovery.one": "You signed in with a recovery code; {count} is left. If this was not you, change your password. Make new codes on the Account page.",
  "auth.recovery.many": "You signed in with a recovery code; {count} are left. If this was not you, change your password. Make new codes on the Account page.",
  "auth.recovery.dismiss": "Dismiss",

  "auth.setup.title": "Create the admin account",
  "auth.setup.intro": "This hub has no accounts yet. Copy the one-time setup token from the hub log (the line that says \"no accounts yet\").",
  "auth.setup.token": "Setup token",
  "auth.setup.username": "Username",
  "auth.setup.password": "Password (at least 10 characters)",
  "auth.setup.repeat": "Repeat the password",
  "auth.setup.submit": "Create the admin",

  "auth.reset.title": "Choose a new password",
  "auth.reset.intro": "The link works once and expires an hour after the administrator made it.",
  "auth.reset.password": "New password (at least 10 characters)",
  "auth.reset.repeat": "Repeat the password",
  "auth.reset.submit": "Change password",
  "auth.reset.doneTitle": "Password changed",
  "auth.reset.done": "Your password was changed and all your sessions were signed out.",
  "auth.reset.signIn": "Sign in",
} as const;
