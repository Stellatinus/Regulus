import { useState } from "react";
import { useSharedAgents } from "./agents";
import { displayName } from "./AgentList";
import { useT, type MessageKey } from "./i18n";
import { ConfirmButton } from "./ui";

// The automation designer (design system v3): a list of automations and an
// editor with a trigger, the nodes it watches, a condition and a list of
// actions. Automations are a plugin that does not exist yet (#476), so what
// is saved here is a draft kept in this browser: it does not run, and the page
// says so.

const TRIGGERS: { id: string; label: MessageKey }[] = [
  { id: "node_offline", label: "automations.trigger.nodeOffline" },
  { id: "updates_available", label: "automations.trigger.updates" },
  { id: "cpu_high", label: "automations.trigger.cpuHigh" },
  { id: "schedule_nightly", label: "automations.trigger.nightly" },
  { id: "webhook", label: "automations.trigger.webhook" },
];

const CONDITIONS: { id: string; label: MessageKey }[] = [
  { id: "always", label: "automations.condition.always" },
  { id: "hubs_only", label: "automations.condition.hubs" },
  { id: "only_online", label: "automations.condition.online" },
  { id: "work_hours", label: "automations.condition.hours" },
];

const ACTIONS: { id: string; label: MessageKey }[] = [
  { id: "notify_dashboard", label: "automations.action.dashboard" },
  { id: "notify_email", label: "automations.action.email" },
  { id: "notify_chat", label: "automations.action.chat" },
  { id: "node_updates", label: "automations.action.updates" },
  { id: "node_restart", label: "automations.action.restart" },
];

interface Action {
  target: string;
  detail: string;
}

export interface Automation {
  id: string;
  name: string;
  enabled: boolean;
  trigger: string;
  node: string;
  condition: string;
  actions: Action[];
}

const KEY = "regulus.automations";

export function loadAutomations(): Automation[] {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(KEY) ?? "[]");
    return Array.isArray(parsed) ? (parsed as Automation[]).filter((a) => a && typeof a.id === "string") : [];
  } catch {
    return [];
  }
}

function saveAutomations(list: Automation[]) {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(list));
  } catch {
    // not saved
  }
}

const blank = (): Automation => ({
  id: `auto-${Date.now().toString(36)}`,
  name: "",
  enabled: false,
  trigger: "node_offline",
  node: "all",
  condition: "always",
  actions: [{ target: "notify_dashboard", detail: "" }],
});

export function AutomationsPage() {
  const t = useT();
  const [list, setList] = useState<Automation[]>(loadAutomations);
  // The draft being edited, and whether it is a new one.
  const [editing, setEditing] = useState<{ draft: Automation; isNew: boolean } | null>(null);

  const commit = (next: Automation[]) => {
    setList(next);
    saveAutomations(next);
  };
  const label = (items: { id: string; label: MessageKey }[], id: string) => {
    const found = items.find((i) => i.id === id);
    return found ? t(found.label) : id;
  };

  if (editing) {
    return (
      <Editor
        draft={editing.draft}
        isNew={editing.isNew}
        onCancel={() => setEditing(null)}
        onSave={(a) => {
          commit(editing.isNew ? [...list, a] : list.map((x) => (x.id === a.id ? a : x)));
          setEditing(null);
        }}
      />
    );
  }

  return (
    <section aria-labelledby="automations-heading">
      <div className="section-header">
        <div>
          <h2 id="automations-heading">{t("automations.title")}</h2>
          <p>{t("automations.subtitle")}</p>
        </div>
        <button type="button" className="primary" onClick={() => setEditing({ draft: blank(), isNew: true })}>
          + {t("automations.new")}
        </button>
      </div>
      <p className="notice warn" role="note">
        {t("automations.draftNotice")}
      </p>
      {list.length === 0 ? (
        <div className="placeholder-pane">
          <h3>{t("automations.empty.title")}</h3>
          <p>{t("automations.empty.body")}</p>
        </div>
      ) : (
        <div className="auto-list">
          {list.map((a) => (
            <article key={a.id} className="auto-card" aria-label={a.name}>
              <div>
                <h3 className="auto-name">{a.name || t("automations.untitled")}</h3>
                <div className="auto-flow">
                  <span className="flow-pill trigger">⚡ {label(TRIGGERS, a.trigger)}</span>
                  {a.actions.map((x, i) => (
                    <span key={i} className="flow-pill-wrap">
                      <span aria-hidden="true">→</span>
                      <span className="flow-pill action">{label(ACTIONS, x.target)}</span>
                    </span>
                  ))}
                </div>
              </div>
              <div className="auto-actions">
                <button type="button" onClick={() => setEditing({ draft: structuredClone(a), isNew: false })}>
                  {t("automations.edit")}
                </button>
                <ConfirmButton
                  label={t("automations.delete", { name: a.name })}
                  message={t("automations.deleteConfirm", { name: a.name })}
                  confirmLabel={t("automations.deleteYes")}
                  onConfirm={() => commit(list.filter((x) => x.id !== a.id))}
                />
                <input
                  type="checkbox"
                  role="switch"
                  className="switch"
                  aria-label={t("automations.enable", { name: a.name })}
                  checked={a.enabled}
                  onChange={(e) => commit(list.map((x) => (x.id === a.id ? { ...x, enabled: e.target.checked } : x)))}
                />
              </div>
            </article>
          ))}
        </div>
      )}
    </section>
  );
}

function Editor({ draft: initial, isNew, onSave, onCancel }: { draft: Automation; isNew: boolean; onSave: (a: Automation) => void; onCancel: () => void }) {
  const t = useT();
  const shared = useSharedAgents();
  const agents = shared?.state.kind === "ready" ? shared.state.agents : [];
  const [d, setD] = useState<Automation>(initial);
  const setAction = (i: number, patch: Partial<Action>) => setD({ ...d, actions: d.actions.map((x, j) => (j === i ? { ...x, ...patch } : x)) });
  const valid = d.name.trim() !== "" && d.actions.length > 0;

  return (
    <section aria-labelledby="automation-editor-heading">
      <div className="section-header">
        <div>
          <h2 id="automation-editor-heading">{isNew ? t("automations.editor.newTitle") : t("automations.editor.editTitle")}</h2>
          <p>{t("automations.editor.subtitle")}</p>
        </div>
        <button type="button" onClick={onCancel}>
          ← {t("automations.editor.back")}
        </button>
      </div>
      <form
        className="auto-editor"
        onSubmit={(e) => {
          e.preventDefault();
          if (valid) onSave({ ...d, name: d.name.trim() });
        }}
      >
        <div className="settings-card">
          <label>
            {t("automations.editor.name")}
            <input value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} maxLength={80} autoComplete="off" />
          </label>
        </div>
        <div className="settings-card">
          <div className="settings-card-title">1 · {t("automations.editor.when")}</div>
          <label>
            {t("automations.editor.trigger")}
            <select value={d.trigger} onChange={(e) => setD({ ...d, trigger: e.target.value })}>
              {TRIGGERS.map((x) => (
                <option key={x.id} value={x.id}>
                  {t(x.label)}
                </option>
              ))}
            </select>
          </label>
          <label>
            {t("automations.editor.node")}
            <select value={d.node} onChange={(e) => setD({ ...d, node: e.target.value })}>
              <option value="all">{t("automations.editor.allNodes")}</option>
              {agents.map((a) => (
                <option key={a.id} value={a.id}>
                  {displayName(a)}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div className="settings-card">
          <div className="settings-card-title">2 · {t("automations.editor.if")}</div>
          <label>
            {t("automations.editor.condition")}
            <select value={d.condition} onChange={(e) => setD({ ...d, condition: e.target.value })}>
              {CONDITIONS.map((x) => (
                <option key={x.id} value={x.id}>
                  {t(x.label)}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div className="settings-card">
          <div className="settings-card-title">3 · {t("automations.editor.then")}</div>
          {d.actions.map((x, i) => (
            <div key={i} className="auto-action-row">
              <label>
                {t("automations.editor.action", { n: i + 1 })}
                <select value={x.target} onChange={(e) => setAction(i, { target: e.target.value })}>
                  {ACTIONS.map((a) => (
                    <option key={a.id} value={a.id}>
                      {t(a.label)}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                {t("automations.editor.detail")}
                <input value={x.detail} onChange={(e) => setAction(i, { detail: e.target.value })} maxLength={200} autoComplete="off" />
              </label>
              <button type="button" className="danger" disabled={d.actions.length === 1} aria-label={t("automations.editor.removeAction", { n: i + 1 })} onClick={() => setD({ ...d, actions: d.actions.filter((_, j) => j !== i) })}>
                ×
              </button>
            </div>
          ))}
          <button type="button" onClick={() => setD({ ...d, actions: [...d.actions, { target: "notify_dashboard", detail: "" }] })}>
            + {t("automations.editor.addAction")}
          </button>
        </div>
        <p className="notice warn" role="note">
          {t("automations.draftNotice")}
        </p>
        <div className="dialog-actions">
          <button type="button" onClick={onCancel}>
            {t("ui.cancel")}
          </button>
          <button type="submit" disabled={!valid}>
            {t("automations.editor.save")}
          </button>
        </div>
      </form>
    </section>
  );
}
