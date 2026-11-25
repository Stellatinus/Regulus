import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { ApiError, fetchAgents, fetchMetrics, type Agent, type MetricsPoint } from "./api";
import { DEFAULT_WINDOW_MS } from "./Chart";
import {
  HISTORY_POINTS,
  LiveStore,
  PLUGINS,
  ROSTER,
  STATUS,
  WATCH,
  agentKey,
  isStale,
  parseStreamMessage,
  pickAgent,
  type AgentEntry,
  type Connection,
} from "./live";
import { useSession } from "./session";
import { toAgents } from "./shapes";

export type { Connection } from "./live";

// The part of WebSocket that the provider uses, so tests can supply a fake.
export interface SocketLike {
  onopen: (() => void) | null;
  onmessage: ((event: { data: unknown }) => void) | null;
  // The browser passes a CloseEvent; code and reason tell why the hub closed.
  onclose: ((event?: { code?: number; reason?: string }) => void) | null;
  onerror: (() => void) | null;
  close(): void;
}

export type SocketFactory = (url: string) => SocketLike;

const browserSocket: SocketFactory = (url) => new WebSocket(url) as unknown as SocketLike;

const MAX_RETRY_MS = 15000;
const CLOCK_MS = 5000;
// A connection counts as working, and the wait before reconnecting goes back
// to the first one, only after it stayed open this long: a hub that accepts
// and closes at once must not be hammered.
export const STABLE_MS = 5000;
// The hub closes the stream with this code and reason when the browser reads
// too slowly (internal/hub/stream.go). Reconnecting at once would only repeat
// it, so the first wait is at least this long.
const POLICY_VIOLATION = 1008;
const TOO_SLOW_RETRY_MS = 5000;

// retryDelay is the wait before the next attempt after failures failed ones:
// it doubles up to a ceiling, and a random half of it is left out so that
// many dashboards cut off together do not come back together.
export function retryDelay(firstMs: number, failures: number, random = Math.random): number {
  const ceiling = Math.min(firstMs * 2 ** failures, MAX_RETRY_MS);
  return Math.round(ceiling / 2 + (random() * ceiling) / 2);
}

const LiveContext = createContext<LiveStore | null>(null);

interface Props {
  children: ReactNode;
  createSocket?: SocketFactory;
  // First wait before reconnecting; it doubles after each failure, and a
  // random part of up to half of it is left out.
  retryMs?: number;
  // The store to fill; tests pass their own to look inside.
  store?: LiveStore;
}

// LiveProvider keeps the agents and their recent metrics up to date for the
// widgets: it loads them over HTTP, then follows /api/stream. If the stream
// drops it reconnects with a growing delay and fills the gap from the history.
// The state lives in a LiveStore, not in React state, so that a report of one
// agent only draws again the widgets that show that agent; the history is
// loaded only for the agents some widget shows.
export function LiveProvider({ children, createSocket = browserSocket, retryMs = 1000, store: given }: Props) {
  const session = useSession();
  const [own] = useState(() => new LiveStore());
  const store = given ?? own;
  const [generation, setGeneration] = useState(0);

  const expired = useRef(session?.expired);
  useEffect(() => {
    expired.current = session?.expired;
  });

  useEffect(() => {
    const timer = setInterval(() => store.tick(Date.now()), CLOCK_MS);
    return () => clearInterval(timer);
  }, [store]);

  useEffect(() => {
    let stopped = false;
    let socket: SocketLike | null = null;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let stable: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    const controller = new AbortController();

    const retry = (atLeastMs = 0) => {
      if (stopped) return;
      store.setConnection("offline");
      const delay = Math.max(retryDelay(retryMs, failures), atLeastMs);
      failures++;
      timer = setTimeout(() => {
        store.setConnection("connecting");
        connect();
      }, delay);
    };

    const openSocket = () => {
      const scheme = window.location.protocol === "https:" ? "wss" : "ws";
      const ws = createSocket(`${scheme}://${window.location.host}/api/stream`);
      socket = ws;
      ws.onopen = () => {
        stable = setTimeout(() => {
          failures = 0;
        }, STABLE_MS);
        store.setConnection("live");
        setGeneration((g) => g + 1);
      };
      ws.onmessage = (event) => {
        const action = parseStreamMessage(event.data);
        if (action) store.dispatch(action);
      };
      ws.onerror = () => undefined; // a close always follows
      ws.onclose = (event) => {
        socket = null;
        clearTimeout(stable);
        const tooSlow = event?.code === POLICY_VIOLATION && event.reason === "too slow";
        retry(tooSlow ? TOO_SLOW_RETRY_MS : 0);
      };
    };

    // Loading the agents over HTTP first shows data quickly and tells an
    // ended session (401) from a hub that is just unreachable.
    const connect = () => {
      fetchAgents(controller.signal).then(
        (agents: Agent[]) => {
          if (stopped) return;
          store.dispatch({ type: "agents", agents: toAgents(agents) });
          openSocket();
        },
        (err: unknown) => {
          if (stopped) return;
          if (err instanceof ApiError && err.status === 401) {
            expired.current?.();
            return;
          }
          retry();
        },
      );
    };

    connect();
    return () => {
      stopped = true;
      clearTimeout(timer);
      clearTimeout(stable);
      controller.abort();
      if (socket) {
        socket.onclose = null;
        socket.close();
      }
    };
  }, [createSocket, retryMs, store]);

  // Fetch the history of each agent a widget shows: the last minutes the
  // first time, afterwards only what was missed while the stream was down.
  // loaded holds the agents whose series has no gap up to the stream.
  const requested = useRef(new Map<string, number>());
  const loaded = useRef(new Set<string>());
  useEffect(() => {
    const controller = new AbortController();
    const asked = requested.current;
    const watched = () => new Set(store.watched().filter((id) => store.entries.has(id)));
    // An agent nobody shows misses nothing it needs while the stream is
    // up, but a reconnection leaves a gap: load it in full when shown again.
    const shown = watched();
    for (const id of [...loaded.current]) {
      if (!shown.has(id)) loaded.current.delete(id);
    }
    const sync = () => {
      const ids = watched();
      for (const id of [...requested.current.keys()]) {
        if (!ids.has(id)) requested.current.delete(id);
      }
      for (const id of [...loaded.current]) {
        if (!store.entries.has(id)) loaded.current.delete(id);
      }
      for (const id of ids) {
        if (requested.current.get(id) === generation) continue;
        requested.current.set(id, generation);
        const since = loaded.current.has(id) ? store.lastAt(id) : undefined;
        fetchMetrics(id, since, controller.signal, HISTORY_POINTS).then(
          (points: MetricsPoint[]) => {
            store.dispatch({ type: "history", id, points });
            if (store.entries.has(id)) loaded.current.add(id);
          },
          (err: unknown) => {
            if (controller.signal.aborted) return;
            if (err instanceof ApiError && err.status === 401) expired.current?.();
            requested.current.delete(id); // try again at the next change
          },
        );
      }
    };
    sync();
    const offWatch = store.subscribe(WATCH, sync);
    const offRoster = store.subscribe(ROSTER, sync);
    return () => {
      offWatch();
      offRoster();
      controller.abort();
      // Requests of this generation that did not finish must be made again
      // (StrictMode runs effects twice with the same generation).
      for (const [id, gen] of [...asked]) {
        if (gen === generation) asked.delete(id);
      }
    };
  }, [generation, store]);

  return <LiveContext.Provider value={store}>{children}</LiveContext.Provider>;
}

// LiveAgent is what a widget shows: one agent and its recent reports.
export interface LiveAgent {
  agents: Agent[];
  agent: Agent;
  stale: boolean;
  // The reports of the last 10 minutes (what the charts show), oldest first,
  // and when each was taken in epoch milliseconds. Both arrays stay the same
  // until a report of this agent arrives, so they can key memoised values.
  points: MetricsPoint[];
  times: number[];
  // The newest report, if any yet.
  latest: MetricsPoint | undefined;
}

export type LiveView =
  | { status: "loading" }
  | { status: "empty" }
  | ({ status: "ready"; connection: Connection } & LiveAgent);

// Without a provider the widgets show "loading" for ever.
const noStore = new LiveStore();
const noop = () => undefined;

// useLiveAgent returns the agent a widget should show: the one named in its
// settings if connected, otherwise the hub's own or the first. The widget is
// drawn again only when that agent, the list of agents or the connection
// changes, or when the agent turns stale.
export function useLiveAgent(preferred: unknown): LiveView {
  const store = useContext(LiveContext) ?? noStore;
  const onStatus = useCallback((cb: () => void) => store.subscribe(STATUS, cb), [store]);
  const onRoster = useCallback((cb: () => void) => store.subscribe(ROSTER, cb), [store]);
  const ready = useSyncExternalStore(onStatus, () => store.ready);
  const connection = useSyncExternalStore(onStatus, () => store.connection);
  const roster = useSyncExternalStore(onRoster, () => store.roster);
  const id = pickAgent(roster, preferred)?.id;
  const onAgent = useCallback((cb: () => void) => (id === undefined ? noop : store.subscribe(agentKey(id), cb)), [store, id]);
  const entry: AgentEntry | undefined = useSyncExternalStore(onAgent, () => (id === undefined ? undefined : store.entries.get(id)));
  const stale = useSyncExternalStore(onStatus, () => entry !== undefined && isStale(entry.agent, store.staleAfterMs, store.now));
  useEffect(() => (id === undefined ? undefined : store.watch(id)), [store, id]);
  const series = useMemo(() => entry?.points.window(DEFAULT_WINDOW_MS), [entry]);

  if (!ready) return { status: "loading" };
  if (!entry || !series) return { status: "empty" };
  return {
    status: "ready",
    connection,
    agents: roster,
    agent: entry.agent,
    stale,
    points: series.points,
    times: series.times,
    latest: entry.points.last,
  };
}

// usePluginsVersion is a number that grows whenever the plugins may have
// changed (LiveStore.pluginsVersion): a view of the plugins loads them again
// when it changes, instead of polling. Without a provider it stays 0.
export function usePluginsVersion(): number {
  const store = useContext(LiveContext) ?? noStore;
  return useSyncExternalStore(
    (onChange) => store.subscribe(PLUGINS, onChange),
    () => store.pluginsVersion,
    () => store.pluginsVersion,
  );
}

// useRoster is the list of connected agents the live stream last told about
// (a snapshot, then agent.connected and agent.disconnected). A page that must
// react when a server enrolls and connects, such as Add a server, follows it
// instead of polling. Without a provider it stays empty.
export function useRoster(): Agent[] {
  const store = useContext(LiveContext) ?? noStore;
  return useSyncExternalStore(
    (onChange) => store.subscribe(ROSTER, onChange),
    () => store.roster,
    () => store.roster,
  );
}

// useConnection is the state of the live stream (/api/stream): connecting,
// live or offline (retrying). Without a provider it reports "connecting".
export function useConnection(): Connection {
  const store = useContext(LiveContext) ?? noStore;
  return useSyncExternalStore(
    (onChange) => store.subscribe(STATUS, onChange),
    () => store.connection,
    () => store.connection,
  );
}
