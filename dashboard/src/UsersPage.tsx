import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  createReset,
  createUser,
  deleteUser,
  fetchAudit,
  fetchRoles,
  fetchSecurity,
  fetchUsers,
  resetUserTwoFactor,
  saveSecurity,
  updateUser,
  can,
  type PasswordReset,
  type Role,
  type RoleInfo,
  type RoleList,
  type SecuritySettings,
  type TwoFactorPolicy,
  type User,
} from "./api";
import { formatAgo } from "./format";
import { auditEntryDetail, eventText } from "./hubTexts";
import { RolesPanel } from "./RolesPanel";
import { useT } from "./i18n";
import { useSession } from "./session";
import { ConfirmButton, CopyButton, RowActions } from "./ui";
import { errorMessage, useApiList } from "./useApiList";

// UsersPage is where accounts, roles and the audit log are managed; each part
// shows to those who hold its permission (users.manage, roles.manage,
// audit.read).
export function UsersPage() {
  const t = useT();
  const session = useSession();
  const user = session?.user;
  const manageUsers = can(user, "users.manage");
  const manageRoles = can(user, "roles.manage");
  const readAudit = can(user, "audit.read");
  if (!user || !(manageUsers || manageRoles || readAudit)) {
    return (
      <section aria-labelledby="users-heading">
        <h2 id="users-heading">{t("users.title")}</h2>
        <p role="alert">{t("users.notAllowed")}</p>
      </section>
    );
  }
  return (
    <section aria-labelledby="users-heading">
      <h2 id="users-heading">{t("users.title")}</h2>
      {(manageUsers || manageRoles) && <Accounts self={user.username} manageUsers={manageUsers} manageRoles={manageRoles} />}
      {manageUsers && <Security />}
      {readAudit && <Audit />}
    </section>
  );
}

// Accounts loads the roles once for the account table, the new account form
// and the roles panel.
function Accounts({ self, manageUsers, manageRoles }: { self: string; manageUsers: boolean; manageRoles: boolean }) {
  const t = useT();
  // The list state holds one item: the roles with the permission catalogue.
  const load = useCallback(async (signal: AbortSignal) => [await fetchRoles(signal)], []);
  const [loaded, reload] = useApiList<RoleList>(load);
  const list = loaded.kind === "ready" ? loaded.items[0] : undefined;
  return (
    <>
      {loaded.kind === "error" && <p role="alert">{t("users.rolesLoadFailed", { message: loaded.message })}</p>}
      {manageUsers && <UserTable self={self} roles={list?.roles ?? []} />}
      {manageRoles && list && <RolesPanel roles={list.roles} permissions={list.permissions} onChanged={reload} />}
    </>
  );
}

// RoleOptions lists the roles of an account's select, keeping a role that has
// since been removed from the list so it still shows.
function RoleOptions({ roles, current }: { roles: RoleInfo[]; current?: Role }) {
  const names = roles.map((r) => r.name);
  if (current && !names.includes(current)) names.push(current);
  return (
    <>
      {names.map((n) => (
        <option key={n} value={n}>
          {n}
        </option>
      ))}
    </>
  );
}

function UserTable({ self, roles }: { self: string; roles: RoleInfo[] }) {
  const t = useT();
  const load = useCallback((signal: AbortSignal) => fetchUsers(signal), []);
  const [users, reload] = useApiList<User>(load);
  const [error, setError] = useState("");
  const [reset, setReset] = useState<PasswordReset | null>(null);

  // run performs a change, then shows the new list or the hub's refusal.
  const run = async (action: () => Promise<unknown>) => {
    setError("");
    try {
      await action();
    } catch (err) {
      setError(errorMessage(err));
    }
    reload();
  };

  return (
    <>
      {users.kind === "loading" && <p>{t("users.loading")}</p>}
      {users.kind === "error" && <p role="alert">{t("users.loadFailed", { message: users.message })}</p>}
      {error && <p role="alert">{error}</p>}
      {users.kind === "ready" && (
        <table className="cards">
          <thead>
            <tr>
              <th>{t("users.col.user")}</th>
              <th>{t("users.col.role")}</th>
              <th>{t("users.col.status")}</th>
              <th>{t("users.col.twoFactor")}</th>
              <th>{t("users.col.created")}</th>
              <th>
                <span className="visually-hidden">{t("users.col.actions")}</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {users.items.map((u) => (
              <tr key={u.username}>
                <td data-label={t("users.col.user")}>
                  <span>
                    {u.username}
                    {u.username === self && t("users.you")}
                  </span>
                </td>
                <td data-label={t("users.col.role")}>
                  <select
                    aria-label={t("users.roleOf", { name: u.username })}
                    value={u.role}
                    disabled={u.username === self}
                    onChange={(e) => void run(() => updateUser(u.username, { role: e.target.value as Role }))}
                  >
                    <RoleOptions roles={roles} current={u.role} />
                  </select>
                </td>
                <td data-label={t("users.col.status")}>
                  <span className={`status ${u.disabled ? "disabled" : "active"}`}>
                    {u.disabled ? t("users.status.disabled") : t("users.status.active")}
                  </span>
                </td>
                <td data-label={t("users.col.twoFactor")}>
                  <span className={`status ${u.two_factor ? "on" : "off"}`}>
                    {u.two_factor ? t("users.twoFactor.on") : t("users.twoFactor.off")}
                  </span>
                </td>
                <td data-label={t("users.col.created")}>{formatAgo(u.created_at, undefined, t)}</td>
                <td>
                  <RowActions name={u.username}>
                    <button
                      type="button"
                      disabled={u.username === self}
                      onClick={() => void run(() => updateUser(u.username, { disabled: !u.disabled }))}
                    >
                      {u.disabled ? t("users.enable", { name: u.username }) : t("users.disable", { name: u.username })}
                    </button>
                    <ConfirmButton
                      danger={false}
                      disabled={u.username === self || u.disabled}
                      label={t("users.resetPassword", { name: u.username })}
                      message={t("users.confirmResetPassword", { name: u.username })}
                      confirmLabel={t("users.confirm.resetPassword")}
                      onConfirm={() => void run(async () => setReset(await createReset(u.username)))}
                    />
                    {u.two_factor && u.username !== self && (
                      <ConfirmButton
                        label={t("users.resetTwoFactor", { name: u.username })}
                        message={t("users.confirmResetTwoFactor", { name: u.username })}
                        confirmLabel={t("users.confirm.resetTwoFactor")}
                        onConfirm={() => void run(() => resetUserTwoFactor(u.username))}
                      />
                    )}
                    <ConfirmButton
                      disabled={u.username === self}
                      label={t("users.delete", { name: u.username })}
                      message={t("users.confirmDelete", { name: u.username })}
                      confirmLabel={t("users.confirm.delete")}
                      onConfirm={() => void run(() => deleteUser(u.username))}
                    />
                  </RowActions>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {reset && <ResetLink reset={reset} onClose={() => setReset(null)} />}
      <NewUserForm roles={roles} onCreated={reload} />
    </>
  );
}

// ResetLink shows the link of a password reset once, for the admin to hand to
// the user.
function ResetLink({ reset, onClose }: { reset: PasswordReset; onClose: () => void }) {
  const t = useT();
  const link = `${window.location.origin}${window.location.pathname}#/reset/${reset.token}`;

  return (
    <div className="panel" role="status" aria-label={t("users.resetLink.label", { name: reset.username })}>
      <h3>{t("users.resetLink.label", { name: reset.username })}</h3>
      <p>{t("users.resetLink.text", { name: reset.username, expires: new Date(reset.expires_at).toLocaleString() })}</p>
      <input readOnly value={link} aria-label={t("users.resetLink.input")} onFocus={(e) => e.target.select()} />
      <p className="row-actions">
        <CopyButton text={link} label={t("users.resetLink.copy")} copiedLabel={t("users.resetLink.copied")} />
        <button type="button" onClick={onClose}>
          {t("users.resetLink.done")}
        </button>
      </p>
    </div>
  );
}

// Security is where admins choose who must use two-factor authentication.
function Security() {
  const t = useT();
  const [settings, setSettings] = useState<SecuritySettings | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    fetchSecurity(controller.signal).then(setSettings, (err: unknown) => {
      if (!controller.signal.aborted) setError(errorMessage(err));
    });
    return () => controller.abort();
  }, []);

  const change = async (policy: TwoFactorPolicy) => {
    setError("");
    try {
      setSettings(await saveSecurity(policy));
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  return (
    <section aria-labelledby="security-heading" className="panel">
      <h3 id="security-heading">{t("users.security.title")}</h3>
      {error && <p role="alert">{error}</p>}
      {settings && (
        <label>
          {t("users.security.who")}
          <select
            value={settings.two_factor_policy}
            disabled={!settings.two_factor_available}
            onChange={(e) => void change(e.target.value as TwoFactorPolicy)}
          >
            <option value="off">{t("users.security.nobody")}</option>
            <option value="admins">{t("users.security.admins")}</option>
            <option value="all">{t("users.security.all")}</option>
          </select>
        </label>
      )}
      <p className="hint">{t("users.security.hint")}</p>
    </section>
  );
}

function NewUserForm({ roles, onCreated }: { roles: RoleInfo[]; onCreated: () => void }) {
  const t = useT();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      await createUser(username.trim(), password, role);
      setUsername("");
      setPassword("");
      setRole("viewer");
      onCreated();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="panel" onSubmit={(e) => void submit(e)}>
      <h3>{t("users.add.title")}</h3>
      <label>
        {t("users.add.username")}
        <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" required />
      </label>
      <label>
        {t("users.add.password")}
        <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" minLength={10} required />
      </label>
      <label>
        {t("users.add.role")}
        <select value={role} onChange={(e) => setRole(e.target.value as Role)}>
          <RoleOptions roles={roles} current="viewer" />
        </select>
      </label>
      {error && <p role="alert">{error}</p>}
      <button type="submit" disabled={busy}>
        {t("users.add.submit")}
      </button>
    </form>
  );
}

function Audit() {
  const t = useT();
  const load = useCallback((signal: AbortSignal) => fetchAudit(100, signal), []);
  const [entries, reload] = useApiList(load);
  const [filter, setFilter] = useState("");
  const needle = filter.trim().toLowerCase();
  const shown =
    entries.kind === "ready"
      ? entries.items.filter((a) => !needle || [a.actor, a.event, eventText(t, a.event), a.target].some((v) => (v ?? "").toLowerCase().includes(needle)))
      : [];
  return (
    <section aria-labelledby="audit-heading" className="panel">
      <h3 id="audit-heading">{t("users.audit.title")}</h3>
      <div className="toolbar">
        <button type="button" onClick={reload}>
          {t("users.audit.refresh")}
        </button>
        <input
          type="search"
          style={{ maxWidth: "18rem" }}
          aria-label={t("users.audit.filter")}
          placeholder={t("users.audit.filter")}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
      </div>
      {entries.kind === "loading" && <p>{t("users.loading")}</p>}
      {entries.kind === "error" && <p role="alert">{t("users.audit.loadFailed", { message: entries.message })}</p>}
      {entries.kind === "ready" && entries.items.length === 0 && <p>{t("users.audit.empty")}</p>}
      {entries.kind === "ready" && entries.items.length > 0 && shown.length === 0 && <p>{t("users.audit.noMatch")}</p>}
      {entries.kind === "ready" && shown.length > 0 && (
        <table className="cards">
          <thead>
            <tr>
              <th>{t("users.audit.when")}</th>
              <th>{t("users.audit.who")}</th>
              <th>{t("users.audit.event")}</th>
              <th>{t("users.audit.target")}</th>
              <th>{t("users.audit.from")}</th>
              <th>{t("users.audit.detail")}</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((a) => (
              <tr key={a.id}>
                <td data-label={t("users.audit.when")}>{formatAgo(a.at, undefined, t)}</td>
                <td data-label={t("users.audit.who")}>{a.actor || "–"}</td>
                <td data-label={t("users.audit.event")}>{eventText(t, a.event)}</td>
                <td data-label={t("users.audit.target")}>{a.target || "–"}</td>
                <td data-label={t("users.audit.from")} className="mono">{a.remote || "–"}</td>
                <td data-label={t("users.audit.detail")} className="wrap">{a.detail ? auditEntryDetail(t, a) : "–"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

