import { useEffect, useMemo, useRef, useState } from "react";
import type { Agent } from "./api";
import { displayName, needsUpdate } from "./AgentList";
import { useT } from "./i18n";
import { NodeIcon, nodeTypeDef, type NodeTypeId } from "./nodeTypes";
import { serverHref } from "./route";
import { computeTopologyLayout, TOPO } from "./topology";

const MIN_SCALE = 0.32;
const MAX_SCALE = 1.65;

interface View {
  scale: number;
  panX: number;
  panY: number;
}

// Topology is the map of the mesh: the primary hub in the middle and every
// node linked to it, drawn on a canvas that pans with a drag and zooms with the
// wheel. A node that does not match the search or the filters fades.
export function Topology({
  agents,
  matches,
  typeOf,
}: {
  agents: Agent[];
  // Null when no search or filter is active.
  matches: ((a: Agent) => boolean) | null;
  typeOf: (a: Agent) => NodeTypeId;
}) {
  const t = useT();
  const wrapper = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<View | null>(null);
  const [hover, setHover] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const dragged = useRef(false);

  const layout = useMemo(() => computeTopologyLayout(agents.map((a) => ({ id: a.id, type: typeOf(a) }))), [agents, typeOf]);
  const byId = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);
  const live = (a: Agent | undefined) => a?.status === "connected" || a?.status === "stale";

  // Fit the map in the frame the first time, and on request.
  const fit = () => {
    const el = wrapper.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const b = layout.bounds;
    const availW = Math.max(200, rect.width - 144);
    const availH = Math.max(200, rect.height - 128);
    const scale = Number(Math.min(1, Math.max(0.35, Math.min(availW / b.width, availH / b.height))).toFixed(2));
    setView({
      scale,
      panX: Math.round(rect.width / 2 - ((b.minX + b.maxX) / 2) * scale),
      panY: Math.round(rect.height / 2 - ((b.minY + b.maxY) / 2) * scale),
    });
  };
  useEffect(() => {
    if (!view) fit();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, layout]);

  const zoomAt = (cx: number, cy: number, delta: number) =>
    setView((v) => {
      if (!v) return v;
      const next = Math.min(MAX_SCALE, Math.max(MIN_SCALE, Number((v.scale + delta).toFixed(2))));
      if (next === v.scale) return v;
      const ratio = next / v.scale;
      return { scale: next, panX: cx - (cx - v.panX) * ratio, panY: cy - (cy - v.panY) * ratio };
    });

  // The wheel must not scroll the page: React's onWheel is passive, so the
  // listener is added by hand.
  useEffect(() => {
    const el = wrapper.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      zoomAt(e.clientX - rect.left, e.clientY - rect.top, e.deltaY < 0 ? 0.1 : -0.1);
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  const pointer = useRef<{ x: number; y: number; panX: number; panY: number } | null>(null);
  const onPointerDown = (e: React.PointerEvent) => {
    if ((e.target as HTMLElement).closest(".topo-controls")) return;
    dragged.current = false;
    if (view) pointer.current = { x: e.clientX, y: e.clientY, panX: view.panX, panY: view.panY };
  };
  const onPointerMove = (e: React.PointerEvent) => {
    const p = pointer.current;
    if (!p) return;
    const dx = e.clientX - p.x;
    const dy = e.clientY - p.y;
    if (!dragged.current && Math.hypot(dx, dy) > 5) {
      dragged.current = true;
      setDragging(true);
    }
    if (dragged.current) setView((v) => (v ? { ...v, panX: p.panX + dx, panY: p.panY + dy } : v));
  };
  const endPointer = () => {
    pointer.current = null;
    setDragging(false);
  };

  const connected = useMemo(() => {
    if (!hover) return null;
    const ids = new Set([hover]);
    for (const e of layout.edges) {
      if (e.from === hover) ids.add(e.to);
      if (e.to === hover) ids.add(e.from);
    }
    return ids;
  }, [hover, layout]);

  if (agents.length === 0) return null;
  const v = view ?? { scale: 1, panX: 0, panY: 0 };
  const stepZoom = (delta: number) => {
    const rect = wrapper.current?.getBoundingClientRect();
    if (rect) zoomAt(rect.width / 2, rect.height / 2, delta);
  };

  return (
    <div
      ref={wrapper}
      className={`topology-wrapper${dragging ? " dragging" : ""}`}
      style={{ backgroundPosition: `${v.panX}px ${v.panY}px, center center` }}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endPointer}
      onPointerCancel={endPointer}
      onPointerLeave={endPointer}
    >
      <div className="topo-badge-title">
        <span className="dot-indicator" style={{ background: "var(--accent-mesh)" }} />
        <span>{t("nodes.topology.hint")}</span>
      </div>
      <div className="topo-controls">
        <button type="button" className="topo-ctrl-btn" aria-label={t("nodes.topology.zoomOut")} title={t("nodes.topology.zoomOut")} onClick={() => stepZoom(-0.14)}>
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true"><path strokeLinecap="round" d="M3.5 8h9" /></svg>
        </button>
        <span className="topo-ctrl-btn mono" aria-live="off">{Math.round(v.scale * 100)}%</span>
        <button type="button" className="topo-ctrl-btn" aria-label={t("nodes.topology.zoomIn")} title={t("nodes.topology.zoomIn")} onClick={() => stepZoom(0.14)}>
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true"><path strokeLinecap="round" d="M8 3.5v9M3.5 8h9" /></svg>
        </button>
        <button type="button" className="topo-ctrl-btn" aria-label={t("nodes.topology.fit")} title={t("nodes.topology.fit")} onClick={fit}>
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true"><circle cx="8" cy="8" r="3" /><path strokeLinecap="round" d="M8 2v2M8 12v2M2 8h2M12 8h2" /></svg>
        </button>
      </div>

      <div className="topo-world" style={{ transform: `translate(${v.panX}px, ${v.panY}px) scale(${v.scale})` }}>
        <svg className="topo-svg-layer" fill="none" aria-hidden="true">
          <defs>
            <filter id="pulseGlow" x="-120%" y="-120%" width="340%" height="340%">
              <feGaussianBlur stdDeviation="2.2" result="blur" />
              <feMerge>
                <feMergeNode in="blur" />
                <feMergeNode in="SourceGraphic" />
              </feMerge>
            </filter>
          </defs>
          {layout.edges.map((edge, idx) => {
            const bothLive = live(byId.get(edge.from)) && live(byId.get(edge.to));
            const failover = edge.kind === "failover";
            let stroke = "rgba(255,255,255,0.18)";
            let dash: string | undefined;
            let cls = "topo-edge";
            if (!bothLive) {
              stroke = "rgba(239,68,68,0.34)";
              dash = "5 6";
            } else if (failover) {
              stroke = "rgba(16,185,129,0.58)";
              dash = "6 6";
              cls += " failover-dash";
            } else if (edge.kind === "gateway") stroke = "rgba(45,212,191,0.46)";
            const direct = hover !== null && (edge.from === hover || edge.to === hover);
            const pulse = edge.kind === "gateway" ? "#2dd4bf" : "#a78bfa";
            return (
              <g key={edge.id} style={{ opacity: hover === null || direct ? 1 : 0.15 }}>
                <path id={edge.id} d={edge.d} stroke={stroke} strokeWidth={direct ? 2.5 : failover ? 1.9 : 1.55} strokeDasharray={dash} strokeLinecap="round" strokeLinejoin="round" className={cls} />
                {bothLive && !failover && (
                  <circle r="2.8" fill={pulse} opacity="0.95" filter="url(#pulseGlow)">
                    <animateMotion dur={`${(2.6 + (idx % 4) * 0.55).toFixed(2)}s`} repeatCount="indefinite" keyPoints="0;1" keyTimes="0;1" calcMode="linear">
                      <mpath href={`#${edge.id}`} />
                    </animateMotion>
                  </circle>
                )}
              </g>
            );
          })}
        </svg>
        {layout.failoverLabels.map((l, i) => (
          <div key={i} className="failover-label" style={{ left: l.x, top: l.y }}>
            {t("nodes.topology.failover")}
          </div>
        ))}
        {agents.map((a) => {
          const pos = layout.positions[a.id];
          if (!pos) return null;
          const type = typeOf(a);
          const dimmedByFilter = matches !== null && !matches(a);
          const dimmed = connected ? !connected.has(a.id) : dimmedByFilter;
          return (
            <a
              key={a.id}
              className={`topo-node${dimmed ? " dimmed" : ""}`}
              href={serverHref(a.id)}
              aria-label={t("nodes.open", { name: displayName(a) })}
              style={{ left: pos.x, top: pos.y, width: TOPO.NODE_W, height: TOPO.NODE_H }}
              draggable={false}
              onMouseEnter={() => setHover(a.id)}
              onMouseLeave={() => setHover(null)}
              onFocus={() => setHover(a.id)}
              onBlur={() => setHover(null)}
              onClick={(e) => {
                if (dragged.current) e.preventDefault();
              }}
            >
              <div className="topo-node-head">
                <div className={`topo-node-icon role-icon-box role-${type}`}>
                  <NodeIcon type={type} size={19} />
                </div>
                <div className="topo-node-info">
                  <div className="topo-node-title-row">
                    <span className="topo-node-name">{displayName(a)}</span>
                    <span className={`status-dot ${a.status}`} style={{ color: live(a) ? "var(--ok)" : "var(--crit)" }} title={a.status} />
                  </div>
                  <div className="topo-node-role">{t(nodeTypeDef(type).label)}</div>
                </div>
              </div>
              <div className="topo-node-foot">
                <span className="topo-node-sub">{a.hostname && a.hostname !== displayName(a) ? a.hostname : a.id}</span>
                {needsUpdate(a) ? <span className="topo-dot" title={t("nodes.badge.update")} /> : live(a) ? <span className="topo-ok">OK</span> : null}
              </div>
            </a>
          );
        })}
      </div>
    </div>
  );
}
