import type { NodeTypeId } from "./nodeTypes";

// The layout of the mesh map (design system v3). It is computed, not drawn by
// hand, so any number of nodes fits without overlaps: the primary hub at the
// centre, secondary hubs below it, proxies and gateways to the left, every
// other node to the right in columns of MAX_PER_COL. A column after the first
// is reached by a line that runs through a free corridor above the nodes.

export const TOPO = {
  NODE_W: 232,
  NODE_H: 84,
  ROW_STEP: 122,
  COL_STEP: 368,
  MAX_PER_COL: 6,
  LEFT_START_X: -(232 + 170),
  RIGHT_START_X: 232 + 190,
  SECONDARY_START_Y: 210,
} as const;

export interface TopoInput {
  id: string;
  type: NodeTypeId;
}

export interface TopoPosition {
  x: number;
  y: number;
  col: number;
}

export interface TopoEdge {
  id: string;
  from: string;
  to: string;
  kind: "failover" | "gateway" | "mesh";
  d: string;
}

export interface TopoLayout {
  positions: Record<string, TopoPosition>;
  edges: TopoEdge[];
  // Where the failover line gets its label.
  failoverLabels: { x: number; y: number }[];
  bounds: { minX: number; maxX: number; minY: number; maxY: number; width: number; height: number };
}

export function computeTopologyLayout(nodes: TopoInput[]): TopoLayout {
  const M = TOPO;
  const halfW = M.NODE_W / 2;
  const halfH = M.NODE_H / 2;
  const positions: Record<string, TopoPosition> = {};
  const edges: TopoEdge[] = [];
  const failoverLabels: { x: number; y: number }[] = [];

  if (nodes.length === 0) {
    return { positions, edges, failoverLabels, bounds: { minX: -halfW, maxX: halfW, minY: -halfH, maxY: halfH, width: M.NODE_W, height: M.NODE_H } };
  }

  // Without a primary hub the first secondary hub, or else the first node,
  // takes its place in the middle: every line starts from it.
  const mainHub = nodes.find((n) => n.type === "hub") ?? nodes.find((n) => n.type === "hub-backup") ?? nodes[0];
  const rest = nodes.filter((n) => n.id !== mainHub.id);
  const primaryHubs = nodes.filter((n) => n.type === "hub");
  const secondaryHubs = rest.filter((n) => n.type === "hub-backup");
  const proxyNodes = rest.filter((n) => n.type === "proxy");
  const workerNodes = rest.filter((n) => n.type !== "hub" && n.type !== "hub-backup" && n.type !== "proxy");

  // Further primary hubs (not expected) stack above the first.
  primaryHubs.forEach((node, idx) => {
    positions[node.id] = { x: 0, y: 0 - idx * M.ROW_STEP, col: 0 };
  });
  positions[mainHub.id] = { x: 0, y: 0, col: 0 };

  const hubPos = positions[mainHub.id];
  const hubLeftX = hubPos.x - halfW;
  const hubRightX = hubPos.x + halfW;
  const hubCenterY = hubPos.y;
  const hubBottomY = hubPos.y + halfH;

  const secondary = secondaryHubs;
  secondary.forEach((node, idx) => {
    const y = M.SECONDARY_START_Y + idx * M.ROW_STEP;
    positions[node.id] = { x: 0, y, col: 0 };
    if (idx === 0) {
      const targetTopY = y - halfH;
      edges.push({ id: `edge-${edges.length}`, from: mainHub.id, to: node.id, kind: "failover", d: `M ${hubPos.x} ${hubBottomY} L ${hubPos.x} ${targetTopY}` });
      failoverLabels.push({ x: hubPos.x, y: (hubBottomY + targetTopY) / 2 });
    } else {
      const prev = secondary[idx - 1];
      const prevBottomY = positions[prev.id].y + halfH;
      edges.push({ id: `edge-${edges.length}`, from: prev.id, to: node.id, kind: "failover", d: `M 0 ${prevBottomY} L 0 ${y - halfH}` });
    }
  });

  const proxyCount = proxyNodes.length;
  proxyNodes.forEach((node, i) => {
    const col = Math.floor(i / M.MAX_PER_COL);
    const rowInCol = i % M.MAX_PER_COL;
    const itemsInCol = Math.min(M.MAX_PER_COL, proxyCount - col * M.MAX_PER_COL);
    positions[node.id] = { x: M.LEFT_START_X - col * M.COL_STEP, y: (rowInCol - (itemsInCol - 1) / 2) * M.ROW_STEP, col };
  });

  const workerCount = workerNodes.length;
  let minRightTopY = Infinity;
  let maxRightCol = 0;
  workerNodes.forEach((node, i) => {
    const col = Math.floor(i / M.MAX_PER_COL);
    const rowInCol = i % M.MAX_PER_COL;
    const itemsInCol = Math.min(M.MAX_PER_COL, workerCount - col * M.MAX_PER_COL);
    const y = (rowInCol - (itemsInCol - 1) / 2) * M.ROW_STEP;
    positions[node.id] = { x: M.RIGHT_START_X + col * M.COL_STEP, y, col };
    if (y - halfH < minRightTopY) minRightTopY = y - halfH;
    if (col > maxRightCol) maxRightCol = col;
  });
  if (!isFinite(minRightTopY)) minRightTopY = -halfH;

  let minLeftTopY = Infinity;
  proxyNodes.forEach((node) => {
    const p = positions[node.id];
    if (p.y - halfH < minLeftTopY) minLeftTopY = p.y - halfH;
  });
  if (!isFinite(minLeftTopY)) minLeftTopY = -halfH;

  const r = 16;
  proxyNodes.forEach((node) => {
    const p = positions[node.id];
    const pRightX = p.x + halfW;
    let d: string;
    if (p.col === 0) {
      const dx = hubLeftX - pRightX;
      d = `M ${pRightX} ${p.y} C ${pRightX + dx * 0.48} ${p.y}, ${hubLeftX - dx * 0.48} ${hubCenterY}, ${hubLeftX} ${hubCenterY}`;
    } else {
      const corridorY = minLeftTopY - 58 - (p.col - 1) * 18;
      const channelX = pRightX + 58;
      const entryX = hubLeftX - 44 - (p.col - 1) * 12;
      d =
        `M ${pRightX} ${p.y} L ${channelX - r} ${p.y} Q ${channelX} ${p.y}, ${channelX} ${p.y - r}` +
        ` L ${channelX} ${corridorY + r} Q ${channelX} ${corridorY}, ${channelX + r} ${corridorY}` +
        ` L ${entryX - r} ${corridorY} Q ${entryX} ${corridorY}, ${entryX} ${corridorY + r}` +
        ` C ${entryX} ${hubCenterY - 24}, ${hubLeftX - 24} ${hubCenterY}, ${hubLeftX} ${hubCenterY}`;
    }
    edges.push({ id: `edge-${edges.length}`, from: node.id, to: mainHub.id, kind: "gateway", d });
  });

  workerNodes.forEach((node) => {
    const p = positions[node.id];
    const nodeLeftX = p.x - halfW;
    let d: string;
    if (p.col === 0) {
      const dx = nodeLeftX - hubRightX;
      d = `M ${hubRightX} ${hubCenterY} C ${hubRightX + dx * 0.48} ${hubCenterY}, ${nodeLeftX - dx * 0.48} ${p.y}, ${nodeLeftX} ${p.y}`;
    } else {
      const corridorY = minRightTopY - 58 - (p.col - 1) * 18;
      const channelX = nodeLeftX - 58;
      const exitX = hubRightX + 44 + (p.col - 1) * 14;
      const dirY = p.y >= corridorY ? 1 : -1;
      d =
        `M ${hubRightX} ${hubCenterY} C ${hubRightX + 26} ${hubCenterY}, ${exitX} ${hubCenterY - 26}, ${exitX} ${corridorY + r}` +
        ` Q ${exitX} ${corridorY}, ${exitX + r} ${corridorY} L ${channelX - r} ${corridorY}` +
        ` Q ${channelX} ${corridorY}, ${channelX} ${corridorY + r} L ${channelX} ${p.y - dirY * r}` +
        ` Q ${channelX} ${p.y}, ${channelX + r} ${p.y} L ${nodeLeftX} ${p.y}`;
    }
    edges.push({ id: `edge-${edges.length}`, from: mainHub.id, to: node.id, kind: "mesh", d });
  });

  let minX = Infinity;
  let maxX = -Infinity;
  let minY = Infinity;
  let maxY = -Infinity;
  Object.values(positions).forEach((p) => {
    minX = Math.min(minX, p.x - halfW);
    maxX = Math.max(maxX, p.x + halfW);
    minY = Math.min(minY, p.y - halfH);
    maxY = Math.max(maxY, p.y + halfH);
  });
  if (maxRightCol >= 1) minY = Math.min(minY, minRightTopY - 58 - (maxRightCol - 1) * 18 - 14);

  return { positions, edges, failoverLabels, bounds: { minX, maxX, minY, maxY, width: maxX - minX, height: maxY - minY } };
}
