import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { ApiError, fetchAgents, type Agent } from "./api";
import { useSession } from "./session";

// The list of every enrolled server, online or not, read from the hub every
// few seconds. The shell reads it once (the "6/8 online" of the notch) and
// the Nodes page shares that reading; a page rendered alone reads it itself.

const REFRESH_MS = 5000;

export type AgentsState =
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "ready"; agents: Agent[] };

export interface AgentsValue {
  state: AgentsState;
  // Reads the list again now (after a server was added, renamed or revoked).
  reload: () => void;
}

const AgentsContext = createContext<AgentsValue | null>(null);

export function useAgentsPolling(refreshMs = REFRESH_MS): AgentsValue {
  const session = useSession();
  const onUnauthorized = session?.expired;
  const [state, setState] = useState<AgentsState>({ kind: "loading" });
  const [round, setRound] = useState(0);
  const reload = useCallback(() => setRound((n) => n + 1), []);

  useEffect(() => {
    const controller = new AbortController();
    const load = () => {
      fetchAgents(controller.signal, true).then(
        (agents) => setState({ kind: "ready", agents }),
        (err: unknown) => {
          if (controller.signal.aborted) return;
          if (err instanceof ApiError && err.status === 401) {
            onUnauthorized?.();
            return;
          }
          setState({ kind: "error", message: err instanceof Error ? err.message : String(err) });
        },
      );
    };
    load();
    const timer = setInterval(load, refreshMs);
    return () => {
      controller.abort();
      clearInterval(timer);
    };
  }, [refreshMs, onUnauthorized, round]);

  return useMemo(() => ({ state, reload }), [state, reload]);
}

export function AgentsProvider({ children }: { children: ReactNode }) {
  const value = useAgentsPolling();
  return <AgentsContext.Provider value={value}>{children}</AgentsContext.Provider>;
}

// useSharedAgents is the shell's reading, or null outside the shell.
export const useSharedAgents = (): AgentsValue | null => useContext(AgentsContext);

// isOnline: the server is connected (a stale one has missed a heartbeat but is
// still connected, so it counts as online here, as in the list).
export const isOnline = (a: Agent) => a.status === "connected" || a.status === "stale";
