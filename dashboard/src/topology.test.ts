import { describe, expect, it } from "vitest";
import type { NodeTypeId } from "./nodeTypes";
import { computeTopologyLayout, TOPO } from "./topology";

const node = (id: string, type: NodeTypeId) => ({ id, type });

describe("computeTopologyLayout", () => {
  it("puts the primary hub in the middle, the secondary hub below, proxies left and the rest right", () => {
    const l = computeTopologyLayout([node("h", "hub"), node("b", "hub-backup"), node("p", "proxy"), node("w", "generic"), node("s", "storage")]);
    expect(l.positions.h).toMatchObject({ x: 0, y: 0 });
    expect(l.positions.b.x).toBe(0);
    expect(l.positions.b.y).toBeGreaterThan(0);
    expect(l.positions.p.x).toBeLessThan(0);
    expect(l.positions.w.x).toBeGreaterThan(0);
    expect(l.positions.s.x).toBeGreaterThan(0);
    // One line per node but the hub: a failover line, a gateway line, two mesh lines.
    expect(l.edges.map((e) => e.kind).sort()).toEqual(["failover", "gateway", "mesh", "mesh"]);
    expect(l.failoverLabels).toHaveLength(1);
  });

  it("never overlaps two nodes, however many there are", () => {
    const many = [node("h", "hub"), ...Array.from({ length: 30 }, (_, i) => node(`w${i}`, "generic")), ...Array.from({ length: 9 }, (_, i) => node(`p${i}`, "proxy"))];
    const l = computeTopologyLayout(many);
    const boxes = Object.values(l.positions);
    expect(boxes).toHaveLength(many.length);
    for (let i = 0; i < boxes.length; i++) {
      for (let j = i + 1; j < boxes.length; j++) {
        const apart = Math.abs(boxes[i].x - boxes[j].x) >= TOPO.NODE_W || Math.abs(boxes[i].y - boxes[j].y) >= TOPO.NODE_H;
        expect(apart, `${i} and ${j}`).toBe(true);
      }
    }
    // The bounds hold every node and the corridor of the lines above them.
    for (const p of boxes) {
      expect(p.x - TOPO.NODE_W / 2).toBeGreaterThanOrEqual(l.bounds.minX);
      expect(p.x + TOPO.NODE_W / 2).toBeLessThanOrEqual(l.bounds.maxX);
    }
  });

  it("makes the first node the centre when no node is a hub, and copes with no node at all", () => {
    const l = computeTopologyLayout([node("a", "generic"), node("b", "generic")]);
    expect(l.positions.a).toMatchObject({ x: 0, y: 0 });
    expect(l.edges).toHaveLength(1);
    expect(computeTopologyLayout([]).edges).toEqual([]);
  });
});
