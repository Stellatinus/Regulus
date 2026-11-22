import { useCallback } from "react";
import {
  fetchAgentDisks,
  fetchDiskMoves,
  type Disk,
  type DiskMove,
} from "./api";
import { formatBytes } from "./format";
import { useT, type MessageKey } from "./i18n";
import { useApiList } from "./useApiList";

const KINDS = new Set(["wwn", "eui64", "nguid", "model-serial", "none"]);
const REASONS = new Set([
  "no-identifier",
  "virtual",
  "usb-bridge",
  "placeholder-serial",
  "duplicate",
]);

// DisksPanel shows the physical disks of a server with the identity the hub
// gave each one, the disks that moved to or from it, and a disk that another
// connected server reports at the same time (a conflict, #382). It is only
// rendered for a role with inventory.read: serial numbers identify hardware,
// and the hub refuses the requests of anyone else.
export function DisksPanel({ serverId }: { serverId: string }) {
  const t = useT();
  const loadDisks = useCallback(
    (signal: AbortSignal) => fetchAgentDisks(serverId, signal),
    [serverId],
  );
  const loadMoves = useCallback(
    (signal: AbortSignal) => fetchDiskMoves(200, signal),
    [],
  );
  const [disks] = useApiList(loadDisks);
  const [moves] = useApiList(loadMoves);

  return (
    <section className="panel" aria-labelledby="disks-heading">
      <h3 id="disks-heading">{t("servers.disks.title")}</h3>
      <p className="muted">{t("servers.disks.intro")}</p>
      {disks.kind === "loading" && <p>{t("servers.disks.loading")}</p>}
      {disks.kind === "error" && (
        <p role="alert">
          {t("servers.disks.loadError", { message: disks.message })}
        </p>
      )}
      {disks.kind === "ready" &&
        (disks.items.length === 0 ? (
          <p>{t("servers.disks.none")}</p>
        ) : (
          <table className="cards">
            <thead>
              <tr>
                <th>{t("servers.disks.column.device")}</th>
                <th>{t("servers.disks.column.disk")}</th>
                <th>{t("servers.disks.column.serial")}</th>
                <th>{t("servers.disks.column.size")}</th>
                <th>{t("servers.disks.column.identity")}</th>
                <th>{t("servers.disks.column.state")}</th>
              </tr>
            </thead>
            <tbody>
              {disks.items.map((d) => (
                <DiskRow key={d.key + d.agent} disk={d} serverId={serverId} />
              ))}
            </tbody>
          </table>
        ))}
      <h4>{t("servers.disks.moves.title")}</h4>
      {moves.kind === "ready" && (
        <Moves moves={moves.items} serverId={serverId} />
      )}
    </section>
  );
}

function kindLabel(kind: string, t: ReturnType<typeof useT>): string {
  return KINDS.has(kind)
    ? t(`servers.disks.kind.${kind}` as MessageKey)
    : kind;
}

function reasonLabel(reason: string, t: ReturnType<typeof useT>): string {
  return t(
    (REASONS.has(reason)
      ? `servers.disks.reason.${reason}`
      : "servers.disks.reason.other") as MessageKey,
  );
}

function DiskRow({ disk, serverId }: { disk: Disk; serverId: string }) {
  const t = useT();
  // On the page of the server that contests a disk, the other one is the
  // server that holds it.
  const other = disk.agent === serverId ? disk.contested_by : disk.agent;
  const state = disk.contested
    ? { cls: "warn", text: t("servers.disks.state.contested", { other: other ?? "?" }) }
    : !disk.identifiable
      ? {
          cls: "warn",
          text: t("servers.disks.state.notIdentifiable", {
            reason: reasonLabel(disk.reason ?? "", t),
          }),
        }
      : !disk.present
        ? { cls: "off", text: t("servers.disks.state.absent") }
        : { cls: "ok", text: t("servers.disks.state.ok") };
  return (
    <tr>
      <td className="mono">{disk.device}</td>
      <td>{[disk.vendor, disk.model].filter(Boolean).join(" ") || "-"}</td>
      <td className="mono">{disk.serial || "-"}</td>
      <td>{disk.size_bytes ? formatBytes(disk.size_bytes) : "-"}</td>
      <td>
        {disk.identifiable ? (
          <>
            <span className="mono">{disk.key}</span>{" "}
            <span className="muted">({kindLabel(disk.kind, t)})</span>
          </>
        ) : (
          "-"
        )}
      </td>
      <td>
        <span className={`status ${state.cls}`}>{state.text}</span>
      </td>
    </tr>
  );
}

function Moves({ moves, serverId }: { moves: DiskMove[]; serverId: string }) {
  const t = useT();
  const mine = moves.filter((m) => m.from === serverId || m.to === serverId);
  if (mine.length === 0) return <p>{t("servers.disks.moves.none")}</p>;
  return (
    <table className="cards">
      <thead>
        <tr>
          <th>{t("servers.disks.moves.column.when")}</th>
          <th>{t("servers.disks.moves.column.disk")}</th>
          <th>{t("servers.disks.moves.column.from")}</th>
          <th>{t("servers.disks.moves.column.to")}</th>
        </tr>
      </thead>
      <tbody>
        {mine.map((m) => (
          <tr key={m.id}>
            <td>{new Date(m.at).toLocaleString()}</td>
            <td>
              {m.model || m.serial
                ? [m.model, m.serial].filter(Boolean).join(" ")
                : t("servers.disks.moves.unknownDisk")}
            </td>
            <td className="mono">{m.from}</td>
            <td className="mono">{m.to}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
