import { useState, type FormEvent } from "react";
import { createRole, deleteRole, updateRole, type PermissionInfo, type RoleInfo } from "./api";
import { permissionText, roleText } from "./hubTexts";
import { useT } from "./i18n";
import { ConfirmButton, Modal, RowActions, useModal } from "./ui";
import { PlusButton } from "./UsersPage";
import { errorMessage } from "./useApiList";

interface Props {
  roles: RoleInfo[];
  permissions: PermissionInfo[];
  onChanged: () => void;
}

// RolesPanel lists the roles and lets people with roles.manage make, change
// and delete their own. The built-in ones are fixed; the hub refuses anything
// that would give someone more than the person asking holds.
export function RolesPanel({ roles, permissions, onChanged }: Props) {
  const t = useT();
  // Editing a role stays on the page; adding one is a popup opened by the +.
  const [editing, setEditing] = useState<RoleInfo | null>(null);
  const add = useModal();
  const [error, setError] = useState("");

  const remove = async (role: RoleInfo) => {
    setError("");
    try {
      await deleteRole(role.name);
    } catch (err) {
      setError(errorMessage(err));
    }
    onChanged();
  };

  return (
    <section aria-labelledby="roles-heading" className="panel">
      <div className="panel-head">
        <h3 id="roles-heading">{t("users.roles.title")}</h3>
        <PlusButton label={t("users.roles.add")} modal={add} />
      </div>
      {error && <p role="alert">{error}</p>}
      <table className="cards">
        <thead>
          <tr>
            <th>{t("users.roles.colRole")}</th>
            <th>{t("users.roles.colCan")}</th>
            <th>{t("users.roles.colUsers")}</th>
            <th>
              <span className="visually-hidden">{t("users.roles.colActions")}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {roles.map((r) => (
            <tr key={r.name}>
              <td data-label={t("users.roles.colRole")} className="wrap">
                <div>
                  <strong>{r.name}</strong>
                  {r.builtin && t("users.roles.builtIn")}
                  {r.description && <div className="hint">{roleText(t, r.name, r.builtin, r.description)}</div>}
                </div>
              </td>
              <td data-label={t("users.roles.colCan")} className="chips-cell">
                {r.permissions.length === 0 ? (
                  t("users.roles.nothing")
                ) : (
                  <ul className="chips" aria-label={t("users.roles.permissionsOf", { name: r.name })}>
                    {r.permissions.map((p) => (
                      <li key={p}>{p}</li>
                    ))}
                  </ul>
                )}
              </td>
              <td data-label={t("users.roles.colUsers")}>{r.users}</td>
              <td>
                {!r.builtin && (
                  <RowActions name={r.name}>
                    <button type="button" onClick={() => setEditing(r)}>
                      {t("users.roles.edit", { name: r.name })}
                    </button>
                    {r.users > 0 ? (
                      <span className="hint">{t("users.roles.inUse", { count: r.users })}</span>
                    ) : (
                      <ConfirmButton
                        label={t("users.roles.delete", { name: r.name })}
                        message={t("users.roles.confirmDelete", { name: r.name })}
                        confirmLabel={t("users.roles.confirm.delete")}
                        onConfirm={() => void remove(r)}
                      />
                    )}
                  </RowActions>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {editing && (
        <RoleForm
          key={editing.name}
          role={editing}
          permissions={permissions}
          onDone={() => {
            setEditing(null);
            onChanged();
          }}
          onCancel={() => setEditing(null)}
        />
      )}
      {add.open && (
        <Modal labelId="new-role-title" onClose={add.hide}>
          <RoleForm
            role={null}
            inDialog
            permissions={permissions}
            onDone={() => {
              add.hide();
              onChanged();
            }}
            onCancel={add.hide}
          />
        </Modal>
      )}
    </section>
  );
}

function RoleForm({
  role,
  permissions,
  inDialog = false,
  onDone,
  onCancel,
}: {
  role: RoleInfo | null;
  // In the add-a-role popup: the form has no panel of its own and its title
  // is what labels the dialog.
  inDialog?: boolean;
  permissions: PermissionInfo[];
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const [name, setName] = useState(role?.name ?? "");
  const [description, setDescription] = useState(role?.description ?? "");
  const [granted, setGranted] = useState<Set<string>>(new Set(role?.permissions));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const toggle = (id: string) =>
    setGranted((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    // Keep the order of the catalogue so the hub and the page agree.
    const chosen = permissions.map((p) => p.id).filter((id) => granted.has(id));
    try {
      if (role) {
        await updateRole(role.name, { name: name.trim(), description, permissions: chosen });
      } else {
        await createRole(name.trim(), description, chosen);
      }
      onDone();
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  return (
    <form
      className={inDialog ? undefined : "panel"}
      onSubmit={(e) => void submit(e)}
      aria-label={inDialog ? undefined : role ? t("users.roles.formLabel", { name: role.name }) : t("users.roles.add")}
    >
      {inDialog ? (
        <h3 id="new-role-title">{t("users.roles.add")}</h3>
      ) : (
        <h4>{role ? t("users.roles.formTitle", { name: role.name }) : t("users.roles.add")}</h4>
      )}
      <label>
        {t("users.roles.name")}
        <input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" required />
      </label>
      <label>
        {t("users.roles.description")}
        <input value={description} onChange={(e) => setDescription(e.target.value)} autoComplete="off" />
      </label>
      <fieldset>
        <legend>{t("users.roles.permissions")}</legend>
        {permissions.map((p) => (
          <label key={p.id} className="check">
            <input type="checkbox" checked={granted.has(p.id)} onChange={() => toggle(p.id)} />
            <span>
              {p.id} <span className="hint">{permissionText(t, p.id, p.description)}</span>
            </span>
          </label>
        ))}
      </fieldset>
      {error && <p role="alert">{error}</p>}
      <p className="row-actions">
        <button type="submit" disabled={busy}>
          {t("users.roles.save")}
        </button>
        <button type="button" onClick={onCancel}>
          {t("users.roles.cancel")}
        </button>
      </p>
    </form>
  );
}
