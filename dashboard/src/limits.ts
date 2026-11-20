import type { Agent, MetricsPoint } from "./api";
import type { MessageKey } from "./i18n";
import type { Translator } from "./widgets/registry";

// What an agent could not read on its server (#230): the parts of a metrics
// report it names in "unavailable", and the "basic" collector of a system
// without /proc. The widgets use this to say "not available" instead of
// showing zeros; see docs/remote-agents.md, "Supported platforms".

// The parts an agent can name. A newer agent may send another one, which is
// shown as it is.
export type Part = "cpu" | "load" | "memory" | "disks" | "network" | "uptime";

const PART_NAME: Record<Part, MessageKey> = {
  cpu: "servers.part.cpu",
  load: "servers.part.load",
  memory: "servers.part.memory",
  disks: "servers.part.disks",
  network: "servers.part.network",
  uptime: "servers.part.uptime",
};

// isLimited tells whether the agent could not read everything in its last
// report.
export const isLimited = (a: Agent): boolean => a.collector === "basic" || (a.unavailable?.length ?? 0) > 0;

// missing tells whether a part is not available: from the newest report when
// there is one, else from what the agent list says about the last report.
export function missing(part: Part, agent: Agent, latest: MetricsPoint | undefined): boolean {
  const list = latest ? latest.unavailable : agent.unavailable;
  return list?.includes(part) ?? false;
}

// partName is the translated name of a part, with its article ("the memory").
export function partName(part: string, t: Translator): string {
  return part in PART_NAME ? t(PART_NAME[part as Part]) : part;
}

// metricsSummary says in a few words what a server's agent reports, from its
// collector and the parts it could not read; "" when the agent says nothing
// about it (an older agent, or no report yet).
export function metricsSummary(collector: string | undefined, unavailable: string[] | undefined, t: Translator): string {
  if (collector === "basic") return t("servers.metrics.basic");
  if (unavailable && unavailable.length > 0) {
    return t("servers.metrics.missing", { parts: unavailable.map((p) => partName(p, t)).join(", ") });
  }
  return collector ? t("servers.metrics.complete") : "";
}
