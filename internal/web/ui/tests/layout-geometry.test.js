/**
 * Geometry of the auto-layout (layoutGraph in visual-editor.js), which is what
 * the "Tidy layout" button runs.
 *
 * Two properties are asserted here, both reported from the live editor:
 *
 *  1. Nothing overlaps. Sub-nodes (a `list` title source, a `search` backend,
 *     a route port) hang off their parent in the parent's own column, and the
 *     slot they want is very often already taken by a main node of another
 *     track — a `list` card landed on top of the node one row above it.
 *
 *  2. Edges run straight where they can. An edge between two nodes one column
 *     apart should be horizontal unless something is genuinely competing for
 *     the row: a merge (a node cannot sit on two upstreams' rows at once) or
 *     the second and later legs of a fan-out.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'visual-editor.js'), 'utf8');

let layoutGraph, NODE_W, NODE_H;

const escHelper = `function esc(s) { return String(s ?? ''); }\n`;

beforeAll(() => {
  const mod = new Function('exports', 'document', 'fetch', escHelper + src + `
exports.layoutGraph = layoutGraph;
exports.NODE_W      = NODE_W;
exports.NODE_H      = NODE_H;
`);
  const exports = {};
  const noopDoc = new Proxy({}, {
    get: (_, prop) => {
      if (prop === 'querySelectorAll') return () => [];
      return () => null;
    },
  });
  mod(exports, noopDoc, () => Promise.resolve());
  ({ layoutGraph, NODE_W, NODE_H } = exports);
});

// ── helpers ───────────────────────────────────────────────────────────────────

const node = (id, upstreams = [], extra = {}) => ({
  id, plugin: id.replace(/_[a-z0-9]+$/, ''), config: {}, upstreams,
  searchNodeIds: [], listNodeIds: [], portNodeIds: [], ...extra,
});

const graph = nodes => ({ name: 'p', schedule: '', nodes });

const byId = (g, id) => g.nodes.find(n => n.id === id);

// Two cards overlap when their rectangles intersect. NODE_W/NODE_H are the
// dimensions the layout itself reasons in.
const overlaps = (a, b) =>
  Math.abs(a.x - b.x) < NODE_W && Math.abs(a.y - b.y) < NODE_H;

// Every unordered pair of nodes that overlap, as readable id pairs.
function overlappingPairs(g) {
  const out = [];
  for (let i = 0; i < g.nodes.length; i++) {
    for (let j = i + 1; j < g.nodes.length; j++) {
      const a = g.nodes[i], b = g.nodes[j];
      if (a.x == null || b.x == null) continue;
      if (overlaps(a, b)) out.push(`${a.id} ∩ ${b.id}`);
    }
  }
  return out;
}

// A chain of ids, each the sole upstream of the next: ['a','b','c'] builds
// a → b → c.
const chain = (ids, rootUpstreams = []) =>
  ids.map((id, i) => node(id, i === 0 ? rootUpstreams : [ids[i - 1]]));

// The tvshows shape that produced the report: one source fanning out to two
// long parallel tracks, the lower one carrying a `list` sub-node partway
// along, plus a third short disjoint track.
function tvshowsLikeGraph({ thirdTrack = false } = {}) {
  const nodes = [
    node('jackett'),
    ...chain(['ta_a', 'mf_a', 'req_a', 'q_a', 'premiere'], ['jackett']),
    ...chain(['ta_b', 'mf_b', 'req_b', 'q_b', 'series'],   ['jackett']),
    node('tvdb_favorites', [], { isListNode: true, listParentId: 'series' }),
  ];
  byId({ nodes }, 'series').listNodeIds = ['tvdb_favorites'];
  if (thirdTrack) {
    nodes.push(
      node('webhook'),
      ...chain(['req_c', 'cond_c', 'favadd_c'], ['webhook']),
    );
  }
  return graph(nodes);
}

// ── 1. nothing overlaps ───────────────────────────────────────────────────────

describe('layoutGraph: nothing overlaps', () => {
  it('keeps a list sub-node clear of the track above its parent', () => {
    const g = tvshowsLikeGraph();
    layoutGraph(g, 40);

    const list = byId(g, 'tvdb_favorites');
    const hits = g.nodes
      .filter(n => n.id !== 'tvdb_favorites' && !n.isListNode)
      .filter(n => overlaps(n, list))
      .map(n => n.id);

    expect(hits).toEqual([]);
  });

  it('leaves no overlapping pair anywhere in a two-track pipeline', () => {
    const g = tvshowsLikeGraph({ thirdTrack: true });
    layoutGraph(g, 40);
    expect(overlappingPairs(g)).toEqual([]);
  });

  it('keeps a search sub-row clear of the track below its parent', () => {
    const nodes = [
      node('webhook'),
      ...chain(['disc', 'ta_a', 'out_a'], ['webhook']),
      ...chain(['ta_b', 'mf_b', 'out_b'], ['webhook']),
      node('jk', [], { isSearchNode: true, searchParentId: 'disc' }),
      node('rb', [], { isSearchNode: true, searchParentId: 'disc' }),
    ];
    const g = graph(nodes);
    byId(g, 'disc').searchNodeIds = ['jk', 'rb'];
    layoutGraph(g, 40);
    expect(overlappingPairs(g)).toEqual([]);
  });

  it('keeps a two-wide sub-row clear of the NEIGHBOURING column too', () => {
    // A row of two sub-nodes is 418px wide against a 260px column, so it
    // reaches into the columns either side of its parent — the case a
    // column-local lane cannot express.
    const nodes = [
      node('src'),
      ...chain(['a1', 'a2', 'a3', 'a4'], ['src']),
      ...chain(['b1', 'b2', 'b3', 'b4'], ['src']),
      node('l1', [], { isListNode: true, listParentId: 'b3' }),
      node('l2', [], { isListNode: true, listParentId: 'b3' }),
    ];
    const g = graph(nodes);
    byId(g, 'b3').listNodeIds = ['l1', 'l2'];
    layoutGraph(g, 40);
    expect(overlappingPairs(g)).toEqual([]);
  });

  it('keeps route ports clear of a parallel track', () => {
    const nodes = [
      node('src'),
      ...chain(['r', 'tail_a'], ['src']),
      ...chain(['b1', 'b2'], ['src']),
      node('p1', [], { isRoutePort: true, routeParentId: 'r' }),
      node('p2', [], { isRoutePort: true, routeParentId: 'r' }),
    ];
    const g = graph(nodes);
    byId(g, 'r').portNodeIds = ['p1', 'p2'];
    layoutGraph(g, 40);
    expect(overlappingPairs(g)).toEqual([]);
  });
});

// ── 2. edges run straight where they can ──────────────────────────────────────

describe('layoutGraph: straight edges', () => {
  // Every edge whose target has exactly one upstream and whose source has
  // exactly one downstream — the edges that have no reason to bend.
  function bentSoleEdges(g) {
    const mains = g.nodes.filter(n => !n.isListNode && !n.isSearchNode && !n.isRoutePort);
    const outDeg = new Map(mains.map(n => [n.id, 0]));
    for (const n of mains) {
      for (const u of n.upstreams) outDeg.set(u, (outDeg.get(u) ?? 0) + 1);
    }
    const bent = [];
    for (const n of mains) {
      if (n.upstreams.length !== 1) continue;      // a merge may bend
      const u = byId(g, n.upstreams[0]);
      if (!u || outDeg.get(u.id) !== 1) continue;  // a fan-out may bend
      if (u.y !== n.y) bent.push(`${u.id}→${n.id} (${u.y} vs ${n.y})`);
    }
    return bent;
  }

  it('runs a straight chain on one row', () => {
    const g = graph(chain(['a', 'b', 'c', 'd', 'e']));
    layoutGraph(g, 40);
    expect(bentSoleEdges(g)).toEqual([]);
  });

  it('keeps both tracks of a fan-out horizontal after the split', () => {
    const g = tvshowsLikeGraph();
    layoutGraph(g, 40);
    expect(bentSoleEdges(g)).toEqual([]);
  });

  it('does not bend a third track just because another track fans out', () => {
    // The reported shape: `webhook`'s chain sits in the same columns as two
    // longer tracks. The single upstream-only barycentre pass pushed it down
    // one row per column, so every edge in it ran diagonally.
    const g = tvshowsLikeGraph({ thirdTrack: true });
    layoutGraph(g, 40);
    expect(bentSoleEdges(g)).toEqual([]);
  });

  it('still straightens the legs of a fan-out that have room', () => {
    // dedup fans out to two chains; each chain is straight from its own head.
    const nodes = [
      node('src'),
      node('dedup', ['src']),
      ...chain(['p1', 'p2', 'p3'], ['dedup']),
      ...chain(['s1', 's2', 's3'], ['dedup']),
    ];
    const g = graph(nodes);
    layoutGraph(g, 40);
    expect(bentSoleEdges(g)).toEqual([]);
    // One leg of the fan-out keeps the parent's row; the other steps off it.
    const ys = ['p1', 's1'].map(id => byId(g, id).y);
    expect(ys).toContain(byId(g, 'dedup').y);
  });

  it('leaves a merge free to bend', () => {
    // Nothing here should throw or loop; a merge target simply cannot be on
    // both upstreams' rows.
    const nodes = [
      node('a'), node('b'),
      node('m', ['a', 'b']),
      node('out', ['m']),
    ];
    const g = graph(nodes);
    layoutGraph(g, 40);
    expect(overlappingPairs(g)).toEqual([]);
    expect(byId(g, 'out').y).toBe(byId(g, 'm').y);
  });
});

// ── 3. the real pipeline, and a size sanity check ─────────────────────────────

describe('layoutGraph: the live tvshows shape', () => {
  // The merged tvshows pipeline as deployed: one feed fanning out to a
  // premiere track and a favourites track, the premiere track itself fanning
  // out to a short side branch, the favourites track carrying a `list` card,
  // and a disjoint webhook track beside them.
  function tvshowsLive() {
    const nodes = [
      node('jackett'),
      ...chain(['ta_a', 'mf_a', 'req_a', 'q_a', 'premiere', 'tvdb_a', 'req_enr', 'cond_a', 'dedup_a'], ['jackett']),
      ...chain(['pathfmt_a', 'seen_a', 'deluge_a', 'notify_a'], ['dedup_a']),
      ...chain(['cond_scifi', 'req_tvdbid', 'favadd_a'], ['dedup_a']),
      ...chain(['ta_b', 'mf_b', 'req_b', 'q_b', 'series', 'tvdb_b', 'req_enr_b', 'dedup_b',
                'pathfmt_b', 'seen_b', 'deluge_b', 'notify_b'], ['jackett']),
      node('tvdb_favorites', [], { isListNode: true, listParentId: 'series' }),
      node('webhook'),
      ...chain(['req_c', 'cond_c', 'favadd_c'], ['webhook']),
    ];
    const g = graph(nodes);
    byId(g, 'series').listNodeIds = ['tvdb_favorites'];
    return g;
  }

  it('overlaps nothing', () => {
    const g = tvshowsLive();
    layoutGraph(g, 40);
    expect(overlappingPairs(g)).toEqual([]);
  });

  it('leaves every sole edge horizontal', () => {
    const g = tvshowsLive();
    layoutGraph(g, 40);
    const mains = g.nodes.filter(n => !n.isListNode);
    const outDeg = new Map(mains.map(n => [n.id, 0]));
    for (const n of mains) for (const u of n.upstreams) outDeg.set(u, (outDeg.get(u) ?? 0) + 1);
    const bent = mains
      .filter(n => n.upstreams.length === 1 && outDeg.get(n.upstreams[0]) === 1)
      .filter(n => byId(g, n.upstreams[0]).y !== n.y)
      .map(n => `${n.upstreams[0]}→${n.id}`);
    expect(bent).toEqual([]);
  });

  it('puts each track on one row', () => {
    const g = tvshowsLive();
    layoutGraph(g, 40);
    const rowOf = ids => new Set(ids.map(id => byId(g, id).y));
    expect(rowOf(['ta_a', 'mf_a', 'req_a', 'q_a', 'premiere', 'dedup_a', 'notify_a']).size).toBe(1);
    expect(rowOf(['ta_b', 'mf_b', 'req_b', 'q_b', 'series', 'dedup_b', 'notify_b']).size).toBe(1);
    expect(rowOf(['webhook', 'req_c', 'cond_c', 'favadd_c']).size).toBe(1);
  });

  it('finishes quickly on a pipeline far larger than any real one', () => {
    // The ordering phase is iterative hill-climbing; this pins that it stays
    // a button-click, not a hang, well past the ~33 nodes of the real config.
    const nodes = [node('root')];
    for (let t = 0; t < 12; t++) {
      const ids = Array.from({ length: 12 }, (_, i) => `t${t}_n${i}`);
      nodes.push(...chain(ids, ['root']));
    }
    const g = graph(nodes);
    const t0 = Date.now();
    layoutGraph(g, 40);
    const ms = Date.now() - t0;
    expect(overlappingPairs(g)).toEqual([]);
    expect(ms).toBeLessThan(4000);
  });
});
