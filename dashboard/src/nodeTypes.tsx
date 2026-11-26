import { useSyncExternalStore, type ReactNode } from "react";
import type { Agent } from "./api";
import type { MessageKey } from "./i18n";

// The logo library of the node types (design system v3, docs/design-system.md).
// Seven symbols, drawn for Regulus: each one is an SVG in currentColor, so a
// colour class (role-* in shell.css) tints icon, pill and box together.
// Only the hub and the agent exist on the hub today (docs/node-types.md); the
// other types are a choice of look that this browser remembers, until the hub
// knows node types (#164).

export type NodeTypeId = "hub" | "hub-backup" | "compute" | "generic" | "proxy" | "storage" | "dev-ai";

interface NodeTypeDef {
  id: NodeTypeId;
  label: MessageKey;
  // The logo, drawn on a 28-unit square.
  draw: ReactNode;
}

const hub = (
  <>
    <circle cx="14" cy="14" r="9.2" stroke="currentColor" strokeWidth="1.3" strokeOpacity="0.3" strokeDasharray="2.5 2.5" />
    <path d="M14 5.2L16.2 11.4L22.5 14L16.2 16.6L14 22.8L11.8 16.6L5.5 14L11.8 11.4L14 5.2Z" fill="currentColor" fillOpacity="0.16" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" />
    <circle cx="14" cy="14" r="2" fill="currentColor" />
    <circle cx="14" cy="4.2" r="1.4" fill="currentColor" />
    <circle cx="22.8" cy="18.8" r="1.4" fill="currentColor" fillOpacity="0.75" />
    <circle cx="5.2" cy="18.8" r="1.4" fill="currentColor" fillOpacity="0.75" />
  </>
);

const hubBackup = (
  <>
    <path d="M14 3.4L22.4 6.9V13.7C22.4 19 18.8 22.8 14 24.6C9.2 22.8 5.6 19 5.6 13.7V6.9L14 3.4Z" fill="currentColor" fillOpacity="0.08" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" />
    <path d="M10.1 13.4A3.9 3.9 0 0 1 17.4 11.5" stroke="currentColor" strokeWidth="1.55" strokeLinecap="round" />
    <path d="M17.9 14.6A3.9 3.9 0 0 1 10.6 16.5" stroke="currentColor" strokeWidth="1.55" strokeLinecap="round" strokeOpacity="0.5" />
    <path d="M16.6 9.6L17.7 11.8L15.4 12" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    <circle cx="14" cy="14" r="1.5" fill="currentColor" />
  </>
);

const compute = (
  <>
    <rect x="7.5" y="7.5" width="13" height="13" rx="2.4" fill="currentColor" fillOpacity="0.1" stroke="currentColor" strokeWidth="1.55" />
    <rect x="11" y="11" width="6" height="6" rx="1" fill="currentColor" fillOpacity="0.28" stroke="currentColor" strokeWidth="1.4" />
    <path d="M11 4V7.5M14 4V7.5M17 4V7.5M11 20.5V24M14 20.5V24M17 20.5V24M4 11H7.5M4 14H7.5M4 17H7.5M20.5 11H24M20.5 14H24M20.5 17H24" stroke="currentColor" strokeWidth="1.45" strokeLinecap="round" strokeOpacity="0.7" />
  </>
);

const generic = (
  <>
    <path d="M14 4.2L22.5 9V19L14 23.8L5.5 19V9L14 4.2Z" fill="currentColor" fillOpacity="0.08" stroke="currentColor" strokeWidth="1.55" strokeLinejoin="round" />
    <path d="M5.5 9L14 13.8L22.5 9" stroke="currentColor" strokeWidth="1.45" strokeLinejoin="round" strokeOpacity="0.65" />
    <path d="M14 13.8V23.8" stroke="currentColor" strokeWidth="1.45" strokeOpacity="0.65" />
    <circle cx="14" cy="9" r="1.2" fill="currentColor" fillOpacity="0.85" />
  </>
);

const proxy = (
  <>
    <rect x="11" y="4.5" width="6" height="19" rx="3" fill="currentColor" fillOpacity="0.1" stroke="currentColor" strokeWidth="1.5" />
    <path d="M4 11H14.5M14.5 11L12 8.5M14.5 11L12 13.5" stroke="currentColor" strokeWidth="1.55" strokeLinecap="round" strokeLinejoin="round" />
    <path d="M24 17H13.5M13.5 17L16 14.5M13.5 17L16 19.5" stroke="currentColor" strokeWidth="1.55" strokeLinecap="round" strokeLinejoin="round" strokeOpacity="0.72" />
  </>
);

const storage = (
  <>
    <rect x="4.5" y="5" width="19" height="5.2" rx="1.6" fill="currentColor" fillOpacity="0.12" stroke="currentColor" strokeWidth="1.5" />
    <rect x="4.5" y="11.4" width="19" height="5.2" rx="1.6" fill="currentColor" fillOpacity="0.08" stroke="currentColor" strokeWidth="1.5" />
    <rect x="4.5" y="17.8" width="19" height="5.2" rx="1.6" fill="currentColor" fillOpacity="0.05" stroke="currentColor" strokeWidth="1.5" />
    <circle cx="8" cy="7.6" r="1" fill="currentColor" />
    <circle cx="8" cy="14" r="1" fill="currentColor" />
    <circle cx="8" cy="20.4" r="1" fill="currentColor" />
    <path d="M16.5 7.6H20M16.5 14H20M16.5 20.4H20" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeOpacity="0.55" />
  </>
);

const devAi = (
  <>
    <rect x="4" y="6" width="20" height="16" rx="3" fill="currentColor" fillOpacity="0.09" stroke="currentColor" strokeWidth="1.5" />
    <path d="M8 11.5L11 14L8 16.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    <path d="M12.2 16.8H14.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeOpacity="0.6" />
    <path d="M18.5 9.5L19.4 12.1L22 13L19.4 13.9L18.5 16.5L17.6 13.9L15 13L17.6 12.1L18.5 9.5Z" fill="currentColor" stroke="currentColor" strokeWidth="0.6" strokeLinejoin="round" />
  </>
);

export const NODE_TYPES: readonly NodeTypeDef[] = [
  { id: "hub", label: "nodes.type.hub", draw: hub },
  { id: "hub-backup", label: "nodes.type.hubBackup", draw: hubBackup },
  { id: "compute", label: "nodes.type.compute", draw: compute },
  { id: "generic", label: "nodes.type.generic", draw: generic },
  { id: "proxy", label: "nodes.type.proxy", draw: proxy },
  { id: "storage", label: "nodes.type.storage", draw: storage },
  { id: "dev-ai", label: "nodes.type.devAi", draw: devAi },
];

export const nodeTypeDef = (id: NodeTypeId): NodeTypeDef => NODE_TYPES.find((n) => n.id === id) ?? NODE_TYPES[3];

// NodeIcon draws the logo of a node type at a size in pixels.
export function NodeIcon({ type, size = 24 }: { type: NodeTypeId; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 28 28" fill="none" aria-hidden="true" focusable="false">
      {nodeTypeDef(type).draw}
    </svg>
  );
}

// ---- the type a node shows ----

const STORAGE_KEY = "regulus.nodeTypes";
const CHANGED = "regulus:node-types";

const NO_TYPES: Record<string, NodeTypeId> = {};

const isTypeId = (v: unknown): v is NodeTypeId => NODE_TYPES.some((n) => n.id === v);

function readOverrides(): Record<string, NodeTypeId> {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "{}");
    if (!parsed || typeof parsed !== "object") return {};
    return Object.fromEntries(Object.entries(parsed).filter((e): e is [string, NodeTypeId] => isTypeId(e[1])));
  } catch {
    return {};
  }
}

// A snapshot must keep its identity until the data changes, or
// useSyncExternalStore redraws forever.
let cachedRaw: string | null | undefined;
let cachedValue: Record<string, NodeTypeId> = NO_TYPES;

function snapshot(): Record<string, NodeTypeId> {
  let raw: string | null = null;
  try {
    raw = window.localStorage.getItem(STORAGE_KEY);
  } catch {
    // no storage
  }
  if (raw !== cachedRaw) {
    cachedRaw = raw;
    cachedValue = readOverrides();
  }
  return cachedValue;
}

function subscribe(callback: () => void) {
  window.addEventListener(CHANGED, callback);
  window.addEventListener("storage", callback);
  return () => {
    window.removeEventListener(CHANGED, callback);
    window.removeEventListener("storage", callback);
  };
}

// setNodeType remembers, in this browser, which logo a node shows; "auto"
// forgets the choice.
export function setNodeType(agentId: string, type: NodeTypeId | "auto") {
  const next = { ...readOverrides() };
  if (type === "auto") delete next[agentId];
  else next[agentId] = type;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
  } catch {
    // not saved
  }
  window.dispatchEvent(new Event(CHANGED));
}

// naturalType is the type the hub's facts give: the hub is the hub, a
// redundant hub is the secondary hub, every other server is a generic node.
export function naturalType(agent: Pick<Agent, "id" | "role">): NodeTypeId {
  const role = agent.role ?? (agent.id === "local" ? "hub" : "agent");
  if (role === "hub") return "hub";
  if (role === "hub-redundant" || role === "redundant-hub") return "hub-backup";
  return "generic";
}

export function useNodeTypes(): (agent: Pick<Agent, "id" | "role">) => NodeTypeId {
  const overrides = useSyncExternalStore(subscribe, snapshot, () => NO_TYPES);
  return (agent) => {
    const natural = naturalType(agent);
    // The hub keeps its look: it is a fact, not a choice.
    return natural === "hub" || natural === "hub-backup" ? natural : (overrides[agent.id] ?? natural);
  };
}

export const savedNodeType = (agentId: string): NodeTypeId | undefined => snapshot()[agentId];
