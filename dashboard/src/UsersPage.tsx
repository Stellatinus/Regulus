import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
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
import { AUDIT_GROUPS, auditGroup, type AuditGroup } from "./auditGroups";
import { RolesPanel } from "./RolesPanel";
import { useT } from "./i18n";
import { useSession } from "./session";
import { ConfirmButton, CopyButton, Modal, RowActions, useModal } from "./ui";
import type { MessageKey } from "./i18n";
import { errorMessage, useApiList } from "./useApiList";

// UsersPage is where accounts, roles and the audit log are managed; each part
// shows to those who hold its permission (users.manage, roles.manage,
// audit.read). The Admin section shows one part per tab ("accounts", "roles",
// "activity"); without a part the page holds all of them.
export type UsersPart = "all" | "accounts" | "roles" | "activity";

export function UsersPage({ part = "all" }: { part?: UsersPart }) {
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
  const accounts = part === "all" || part === "accounts";
  const roles = part === "all" || part === "roles";
  const activity = part === "all" || part === "activity";
  // The page is named after its tab; "Users" is the heading of the table inside it (#514).
  const heading = part === "roles" ? t("users.tab.roles") : part === "activity" ? t("users.tab.activity") : t("users.tab.accounts");
  return (
    <section aria-labelledby="users-heading">
      <h2 id="users-heading">{heading}</h2>
      {((accounts && manageUsers) || (roles && manageRoles)) && (
        <Accounts self={user.username} manageUsers={manageUsers && accounts} manageRoles={manageRoles && roles} />
      )}
      {accounts && manageUsers && <Security />}
      {activity && readAudit && <Audit />}
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

// PlusButton is the "+" at the right of a heading row: it opens the popup that
// adds a user or a role (#514). It has an accessible name and a tooltip, and
// says it opens a dialog.
export function PlusButton({ label, modal }: { label: string; modal: ReturnType<typeof useModal> }) {
  return (
    <button ref={modal.trigger} type="button" className="icon-btn plus-btn" aria-label={label} title={label} aria-haspopup="dialog" onClick={modal.show}>
      <span aria-hidden="true">+</span>
    </button>
  );
}

function UserTable({ self, roles }: { self: string; roles: RoleInfo[] }) {
  const t = useT();
  const load = useCallback((signal: AbortSignal) => fetchUsers(signal), []);
  const [users, reload] = useApiList<User>(load);
  const [error, setError] = useState("");
  const [reset, setReset] = useState<PasswordReset | null>(null);
  const add = useModal();

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
      <div className="panel-head">
        <h3>{t("users.title")}</h3>
        <PlusButton label={t("users.add.title")} modal={add} />
      </div>
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
      {add.open && (
        <Modal labelId="new-user-title" onClose={add.hide}>
          <NewUserForm
            roles={roles}
            onCreated={() => {
              add.hide();
              reload();
            }}
            onCancel={add.hide}
          />
        </Modal>
      )}
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

function NewUserForm({ roles, onCreated, onCancel }: { roles: RoleInfo[]; onCreated: () => void; onCancel: () => void }) {
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
      onCreated();
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  return (
    <form onSubmit={(e) => void submit(e)}>
      <h3 id="new-user-title">{t("users.add.title")}</h3>
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
      <div className="dialog-actions">
        <button type="button" onClick={onCancel}>
          {t("ui.cancel")}
        </button>
        <button type="submit" disabled={busy}>
          {t("users.add.submit")}
        </button>
      </div>
    </form>
  );
}

const GROUP_LABEL: Record<AuditGroup, MessageKey> = {
  security: "users.auditGroup.security",
  nodes: "users.auditGroup.nodes",
  accounts: "users.auditGroup.accounts",
  plugins: "users.auditGroup.plugins",
  other: "users.auditGroup.other",
};

// Audit is Recent activity: a toolbar (search, refresh, filter), the chips of
// the event groups and the entries. The hub's API cannot filter yet, so the
// search and the filters work on the entries loaded (the last 100); #514 says
// the server side is a separate issue.
function Audit() {
  const t = useT();
  const load = useCallback((signal: AbortSignal) => fetchAudit(100, signal), []);
  const [entries, reload] = useApiList(load);
  const [search, setSearch] = useState("");
  const [account, setAccount] = useState("");
  const [group, setGroup] = useState<AuditGroup | "all">("all");
  const [filterOpen, setFilterOpen] = useState(false);
  const filterWrapper = useRef<HTMLDivElement>(null);
  const filterButton = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!filterOpen) return;
    const onDown = (e: MouseEvent) => {
      if (filterWrapper.current && !filterWrapper.current.contains(e.target as Node)) setFilterOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setFilterOpen(false);
        filterButton.current?.focus();
      }
    };
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [filterOpen]);

  const items = entries.kind === "ready" ? entries.items : [];
  // The accounts and the groups come from the entries themselves, not from a list.
  const accounts = useMemo(() => [...new Set(items.map((a) => a.actor).filter(Boolean))].sort((x, y) => x.localeCompare(y)), [items]);
  const groups = useMemo(() => AUDIT_GROUPS.filter((g) => items.some((a) => auditGroup(a.event) === g)), [items]);
  const active = (account ? 1 : 0) + (group !== "all" ? 1 : 0);

  const needle = search.trim().toLowerCase();
  const shown = items.filter(
    (a) =>
      (account === "" || a.actor === account) &&
      (group === "all" || auditGroup(a.event) === group) &&
      (!needle ||
        [a.actor, a.event, eventText(t, a.event), a.target, a.remote, a.detail ? auditEntryDetail(t, a) : ""].some((v) => (v ?? "").toLowerCase().includes(needle))),
  );
  const clear = () => {
    setAccount("");
    setGroup("all");
  };

  return (
    <section aria-labelledby="audit-heading" className="panel">
      <h3 id="audit-heading">{t("users.audit.title")}</h3>
      <div className="control-bar audit-bar">
        <div className="search-box">
          <span className="search-icon" aria-hidden="true">
            <svg width="16" height="16" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" focusable="false">
              <circle cx="9" cy="9" r="5.5" />
              <path d="M13.5 13.5L17 17" />
            </svg>
          </span>
          <input
            className="search-input"
            type="search"
            role="searchbox"
            aria-label={t("users.audit.search")}
            placeholder={t("users.audit.search")}
            autoComplete="off"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <div className="control-actions">
          <button type="button" className="icon-btn square-btn" aria-label={t("users.audit.refresh")} title={t("users.audit.refresh")} onClick={reload}>
            <svg width="16" height="16" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
              <path d="M16.5 8A6.5 6.5 0 0 0 5 5.6L3.5 7.2M3.5 3.5v3.7h3.7" />
              <path d="M3.5 12A6.5 6.5 0 0 0 15 14.4l1.5-1.6M16.5 16.5v-3.7h-3.7" />
            </svg>
          </button>
          <div className="filter-wrapper" ref={filterWrapper}>
            <button
              ref={filterButton}
              type="button"
              className={`icon-btn${filterOpen || active > 0 ? " active" : ""}`}
              aria-haspopup="true"
              aria-expanded={filterOpen}
              aria-controls={filterOpen ? "audit-filter" : undefined}
              title={t("users.audit.filterTitle")}
              onClick={() => setFilterOpen((v) => !v)}
            >
              <svg width="16" height="16" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" aria-hidden="true" focusable="false">
                <path d="M3.5 5.5h13M6 10h8M8.5 14.5h3" />
              </svg>
              <span className="filter-btn-label">{t("users.audit.filterButton")}</span>
              {active > 0 && (
                <span className="filter-badge" aria-label={t("users.audit.filtersActive", { count: active })}>
                  {active}
                </span>
              )}
            </button>
            {filterOpen && (
              <div id="audit-filter" className="floating-menu filter-popover open" role="group" aria-label={t("users.audit.filterTitle")}>
                <label className="popover-field">
                  <span className="popover-title">{t("users.audit.account")}</span>
                  <select value={account} onChange={(e) => setAccount(e.target.value)}>
                    <option value="">{t("users.audit.allAccounts")}</option>
                    {accounts.map((a) => (
                      <option key={a} value={a}>
                        {a}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="popover-field">
                  <span className="popover-title">{t("users.audit.group")}</span>
                  <select value={group} onChange={(e) => setGroup(e.target.value as AuditGroup | "all")}>
                    <option value="all">{t("users.audit.groupAll")}</option>
                    {groups.map((g) => (
                      <option key={g} value={g}>
                        {t(GROUP_LABEL[g])}
                      </option>
                    ))}
                  </select>
                </label>
                {active > 0 && (
                  <button type="button" className="popover-reset-btn" onClick={clear}>
                    {t("users.audit.clearFilters")}
                  </button>
                )}
              </div>
            )}
          </div>
        </div>
      </div>
      {groups.length > 0 && (
        <div className="store-cat-pills audit-chips" role="group" aria-label={t("users.audit.group")}>
          <button type="button" className="filter-chip" aria-pressed={group === "all"} onClick={() => setGroup("all")}>
            {t("users.audit.groupAll")}
          </button>
          {groups.map((g) => (
            <button key={g} type="button" className="filter-chip" aria-pressed={group === g} onClick={() => setGroup(g)}>
              {t(GROUP_LABEL[g])}
            </button>
          ))}
        </div>
      )}
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

