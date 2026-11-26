import { useEffect, useRef, useState } from "react";
import { useAgentsPolling, useSharedAgents, type AgentsValue } from "./agents";
import { NodesView } from "./NodesView";
import { ServerPage } from "./ServerPage";
import { useRoute } from "./route";
import {
  createEnrollment,
  enableRemoteAgents,
  fetchAgentListener,
  fetchEnrollmentTokens,
  revokeEnrollmentToken,
  type AgentListener,
  type Enrollment,
  type PendingEnrollment,
} from "./api";
import { useT } from "./i18n";
import { useRoster } from "./LiveProvider";
import { useCan } from "./session";
import { ConfirmButton, CopyIconButton } from "./ui";
import { errorMessage, useApiList, type ListState } from "./useApiList";

// ServersPage is the Nodes page: the servers as cards, a list or the map of the
// mesh, and for those who may manage servers a way to add new ones. A server
// opens in a window over the page (the address #/servers/<id>). While remote
// agents are off it says so first, and lets those who may manage servers turn
// them on, instead of offering a token that cannot work.
export function ServersPage() {
  const shared = useSharedAgents();
  return shared ? <Servers agents={shared} /> : <StandaloneServers />;
}

// StandaloneServers reads the list itself, for a page shown outside the shell.
function StandaloneServers() {
  return <Servers agents={useAgentsPolling()} />;
}

function Servers({ agents }: { agents: AgentsValue }) {
  const t = useT();
  const canManage = useCan("servers.manage");
  const route = useRoute();
  const { reload } = agents;
  // undefined while loading; null when the hub does not say (an older hub).
  const [listener, setListener] = useState<AgentListener | null | undefined>(undefined);
  useEffect(() => {
    let live = true;
    fetchAgentListener().then(
      (l) => live && setListener(l),
      () => live && setListener(null),
    );
    return () => {
      live = false;
    };
  }, []);
  const off = listener != null && !listener.enabled;
  // The list is read again when a server connects or leaves, so a server that
  // just enrolled shows up without waiting for the next refresh (#426).
  const connected = useRoster()
    .map((a) => a.id)
    .join(",");
  const lastConnected = useRef(connected);
  useEffect(() => {
    if (lastConnected.current === connected) return;
    lastConnected.current = connected;
    reload();
  }, [connected, reload]);
  // "Add a server" is needed rarely: it is closed until the + button of the
  // control bar opens it (#428).
  const [adding, setAdding] = useState(false);
  const canAdd = canManage && listener !== undefined && !off;
  const state = agents.state;
  return (
    <section aria-labelledby="agents-heading">
      <h2 id="agents-heading" className="visually-hidden">
        {t("nodes.title")}
      </h2>
      {state.kind === "loading" && <p>{t("servers.loading")}</p>}
      {state.kind === "error" && <p role="alert">{t("servers.list.loadError", { message: state.message })}</p>}
      {state.kind === "ready" && (
        <NodesView
          agents={state.agents}
          actions={canAdd ? <AddButton open={adding} onToggle={() => setAdding((v) => !v)} /> : undefined}
          belowControls={canAdd && adding ? <AddServer onAdded={reload} onClose={() => setAdding(false)} /> : undefined}
        />
      )}
      {off && <RemoteAgentsOff listener={listener} canManage={canManage} onEnabled={setListener} />}
      {route === "/server" && <ServerPage />}
    </section>
  );
}

// AddButton is the + in the control bar. It has an accessible name and a
// tooltip, and says whether the panel it controls is open.
function AddButton({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  const t = useT();
  return (
    <button
      type="button"
      className="icon-btn"
      aria-label={t("servers.add.title")}
      title={t("servers.add.title")}
      aria-expanded={open}
      aria-controls="add-server-panel"
      onClick={onToggle}
    >
      <span aria-hidden="true">{open ? "−" : "+"}</span>
    </button>
  );
}

const DEFAULT_AGENT_PORT = 8443;

function RemoteAgentsOff({
  listener,
  canManage,
  onEnabled,
}: {
  listener: AgentListener;
  canManage: boolean;
  onEnabled: (l: AgentListener) => void;
}) {
  const t = useT();
  const [port, setPort] = useState(String(DEFAULT_AGENT_PORT));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const n = Number(port);
  const valid = Number.isInteger(n) && n >= 1024 && n <= 65535;

  const turnOn = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      onEnabled(await enableRemoteAgents(n));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="panel" aria-labelledby="remote-agents-heading">
      <h3 id="remote-agents-heading">{t("servers.remoteOff.title")}</h3>
      <p>{t("servers.remoteOff.intro")}</p>
      {canManage && listener.editable ? (
        <form onSubmit={(e) => void turnOn(e)}>
          <label>
            {t("servers.remoteOff.portLabel")}
            <input value={port} onChange={(e) => setPort(e.target.value.trim())} inputMode="numeric" autoComplete="off" />
          </label>
          {!valid && <p className="muted">{t("servers.remoteOff.portHint")}</p>}
          {error && <p role="alert">{error}</p>}
          <button type="submit" disabled={busy || !valid}>
            {t("servers.remoteOff.turnOn")}
          </button>
          <p className="muted">{t("servers.remoteOff.turnOnNote")}</p>
        </form>
      ) : (
        <p className="muted">{t("servers.remoteOff.askAdmin")}</p>
      )}
    </section>
  );
}

const RELEASES_URL = "https://github.com/Stellatinus/Regulus/releases";

const enrollOptions = (grant: Enrollment, hubHost: string) =>
  [`--hub ${hubHost}:${grant.agent_port}`, `--token ${grant.token}`, `--ca-fingerprint ${grant.ca_fingerprint}`].join(" ");

// installCommand installs Regulus on a clean Ubuntu or Debian server as an
// agent and enrolls it. It downloads the script first and then runs it (piping
// it into sh breaks as soon as "-s" is lost), and installs the version the hub
// runs, from that release, so the agent matches the hub even before a stable
// release exists. A development hub has no release: it falls back to the
// latest stable one.
export function installCommand(grant: Enrollment, hubHost: string): string {
  const v = grant.hub_version;
  const script = v ? `${RELEASES_URL}/download/v${v}/install.sh` : `${RELEASES_URL}/latest/download/install.sh`;
  const version = v ? `--version ${v} ` : "";
  return `curl -fsSLO ${script} && sh install.sh ${version}--role agent ${enrollOptions(grant, hubHost)}`;
}

// agentCommand enrolls and runs the agent on a server that already has the
// regulus binary.
export function agentCommand(grant: Enrollment, hubHost: string): string {
  return `regulus agent ${enrollOptions(grant, hubHost)} --data-dir /var/lib/regulus-agent`;
}

// The lifetimes the Add a server form offers (#280); the hub accepts 1 minute
// to 7 days.
export const TOKEN_LIFETIMES = [
  { ttl: "15m", label: "servers.add.ttl15m" },
  { ttl: "1h", label: "servers.add.ttl1h" },
  { ttl: "24h", label: "servers.add.ttl24h" },
  { ttl: "168h", label: "servers.add.ttl7d" },
] as const;

function AddServer({ onAdded, onClose }: { onAdded: () => void; onClose: () => void }) {
  const t = useT();
  const [name, setName] = useState("");
  const [ttl, setTtl] = useState<string>("1h");
  const [grant, setGrant] = useState<Enrollment | null>(null);
  // The shown command stops being valid when its token is no longer pending:
  // it was cancelled, used or has expired. "listed" is set once the pending
  // list has shown it, so the list read before the token existed does not
  // count as the token having gone.
  const [listed, setListed] = useState(false);
  const [gone, setGone] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [pending, reloadPending] = useApiList<PendingEnrollment>(fetchEnrollmentTokens);

  // A server that connects may be the one that used the token: read the
  // pending list again (the hub already sends agent.connected on the stream).
  const connected = useRoster()
    .map((a) => a.id)
    .join(",");
  const lastConnected = useRef(connected);
  useEffect(() => {
    if (lastConnected.current === connected) return;
    lastConnected.current = connected;
    reloadPending();
  }, [connected, reloadPending]);

  // State that follows from the list is adjusted while rendering, not in an
  // effect (React redraws at once, without a frame with the stale command).
  if (grant && pending.kind === "ready") {
    const present = pending.items.some((tok) => sameToken(tok, grant));
    if (present && !listed) setListed(true);
    if (!present && listed) {
      setGrant(null);
      setListed(false);
      setGone(true);
    }
  }

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setGone(false);
    setBusy(true);
    try {
      setGrant(await createEnrollment(name.trim(), ttl));
      setListed(false);
      setName("");
      reloadPending();
      onAdded();
    } catch (err) {
      setGrant(null);
      setListed(false);
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const host = window.location.hostname;
  return (
    <section
      id="add-server-panel"
      className="panel"
      aria-labelledby="add-server-heading"
      onKeyDown={(e) => {
        if (e.key === "Escape") onClose();
      }}
    >
      <div className="page-title">
        <h3 id="add-server-heading" style={{ margin: 0 }}>{t("servers.add.title")}</h3>
        <span className="title-actions">
          <button type="button" onClick={onClose}>
            {t("servers.add.close")}
          </button>
        </span>
      </div>
      <form onSubmit={(e) => void create(e)}>
        <label>
          {t("servers.add.nameLabel")}
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={64} autoComplete="off" />
        </label>
        <label>
          {t("servers.add.ttlLabel")}
          <select value={ttl} onChange={(e) => setTtl(e.target.value)}>
            {TOKEN_LIFETIMES.map((l) => (
              <option key={l.ttl} value={l.ttl}>
                {t(l.label)}
              </option>
            ))}
          </select>
        </label>
        {error && <p role="alert">{error}</p>}
        <button type="submit" disabled={busy}>
          {t("servers.add.createToken")}
        </button>
      </form>
      {gone && (
        <p role="status" className="muted">
          {t("servers.add.tokenGone")}
        </p>
      )}
      {grant && (
        <div role="status" aria-label={t("servers.add.tokenLabel")} className="notice">
          <p>
            <strong>{t("servers.add.shownOnce")}</strong>
          </p>
          <p>
            {grant.hub_version
              ? t("servers.add.instructionsVersion", { version: grant.hub_version, expires: new Date(grant.expires_at).toLocaleString() })
              : t("servers.add.instructions", { expires: new Date(grant.expires_at).toLocaleString() })}
          </p>
          <CommandBox label={t("servers.add.installCommand")} copyLabel={t("servers.add.copyInstall")} command={installCommand(grant, host)} />
          <p className="muted">
            {t("servers.add.replaceBefore")} <code>{host}</code> {t("servers.add.replaceAfter")}
          </p>
          <p>{t("servers.add.alreadyInstalled")}</p>
          <CommandBox label={t("servers.add.agentCommand")} copyLabel={t("servers.add.copyAgent")} command={agentCommand(grant, host)} />
        </div>
      )}
      <PendingTokens
        state={pending}
        onChanged={reloadPending}
        onCancelled={(tok) => {
          // Cancelling the token whose command is on the page removes the
          // command at once: it is no longer valid.
          if (grant && sameToken(tok, grant)) {
            setGrant(null);
            setListed(false);
          }
        }}
      />
    </section>
  );
}

// sameToken tells whether a pending token is the one just created: the grant
// has no id (the hub shows the secret once and keeps only its hash), so the
// name and the end time identify it.
function sameToken(pending: PendingEnrollment, grant: Enrollment): boolean {
  return pending.name === grant.name && Date.parse(pending.expires_at) === Date.parse(grant.expires_at);
}

// PendingTokens lists the enrollment tokens nobody has used yet, so one that
// was pasted where it should not have been can be cancelled. A list that
// cannot be loaded (an older hub has none) is left out; it is not an error of
// the page.
function PendingTokens({
  state,
  onChanged,
  onCancelled,
}: {
  state: ListState<PendingEnrollment>;
  onChanged: () => void;
  onCancelled: (token: PendingEnrollment) => void;
}) {
  const t = useT();
  const [error, setError] = useState("");
  if (state.kind === "error") return <p className="muted">{t("servers.pending.loadError")}</p>;
  if (state.kind !== "ready" || state.items.length === 0) return null;

  const cancel = async (token: PendingEnrollment) => {
    setError("");
    try {
      await revokeEnrollmentToken(token.id);
      onCancelled(token);
    } catch (err) {
      setError(errorMessage(err));
    }
    onChanged();
  };

  return (
    <div aria-labelledby="pending-tokens-heading">
      <h4 id="pending-tokens-heading">{t("servers.pending.title")}</h4>
      <p className="muted">{t("servers.pending.intro")}</p>
      {error && <p role="alert">{error}</p>}
      <ul>
        {state.items.map((token) => {
          const name = token.name || t("servers.pending.noName");
          return (
            <li key={token.id}>
              <strong>{name}</strong>{" "}
              <span className="muted">
                {t("servers.pending.detail", { user: token.created_by, expires: new Date(token.expires_at).toLocaleString() })}
              </span>{" "}
              <ConfirmButton
                label={t("servers.pending.cancel", { name })}
                message={t("servers.pending.confirm", { name })}
                confirmLabel={t("servers.pending.confirmCancel")}
                onConfirm={() => void cancel(token)}
              />
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// CommandBox shows a command in a box that keeps the whole row but for a
// square icon button. Where the browser cannot copy (the clipboard API needs
// HTTPS or localhost) the way to do it by hand is a line of its own under the
// command, in the warning colour (#427).
function CommandBox({ label, copyLabel, command }: { label: string; copyLabel: string; command: string }) {
  const t = useT();
  const [manual, setManual] = useState(false);
  return (
    <div className="command-box">
      <p className="command">
        <input readOnly value={command} aria-label={label} onFocus={(e) => e.target.select()} className="mono" />
        <CopyIconButton text={command} label={copyLabel} copiedLabel={t("servers.add.copied")} onResult={(ok) => setManual(!ok)} />
      </p>
      {manual && (
        <p role="alert" className="copy-warning">
          {t("ui.copyManual")}
        </p>
      )}
    </div>
  );
}
