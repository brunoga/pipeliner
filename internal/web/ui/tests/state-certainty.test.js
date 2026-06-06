/**
 * Tests for the per-state field certainty model in visual-editor.js.
 * Mirrors internal/dag/swap_state_test.go so the live preview the visual
 * editor shows stays in lockstep with what the server's Validate /
 * ComputeNodeFields will report at the next /api/parse round-trip.
 *
 * Scenarios covered:
 *   - require → swap_state → consumer: revived rejected entries lack the
 *     required field, so a downstream Requires check must warn.
 *   - condition(reject-absence) → swap_state → consumer: same defeat, via
 *     the absence-promotion code path.
 *   - benign swap_state right before a sink with no Requires: no warning.
 *   - require alone narrows downstream certainty.
 *   - condition accept rule populates Accepted bucket (the
 *     promoteAccepted path) so a downstream sink reading Accepted-only
 *     sees the promoted field.
 *   - merge with one branch missing a field intersects per state
 *     (downstream sees nothing certain).
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src   = readFileSync(join(__dir, '..', 'visual-editor.js'), 'utf8');

let computeInputFields, fieldWarnings, ve;

beforeAll(() => {
  const mod = new Function('exports', 'document', 'fetch', src + `
    exports.computeInputFields  = computeInputFields;
    exports.fieldWarnings       = fieldWarnings;
    exports.ve                  = ve;
  `);
  const exports = {};
  const noopDoc = new Proxy({}, { get: () => () => null });
  mod(exports, noopDoc, () => Promise.resolve());
  ({ computeInputFields, fieldWarnings, ve } = exports);
});

// Plugins used across the test scenarios. `input_states` matches what the
// server's /api/plugins response carries — when omitted, the JS falls back
// to a role-appropriate default (sink → accepted; processor → acc∪und).
const SRC = {
  name: 'src', role: 'source',
  produces: ['title'], may_produce: ['video_year'], requires: [],
};
const REQUIRE = {
  name: 'require', role: 'processor',
  produces: [], may_produce: [], requires: [],
};
const SWAP_STATE = {
  name: 'swap_state', role: 'processor',
  produces: [], may_produce: [], requires: [],
  input_states: ['accepted', 'undecided', 'rejected', 'failed'],
};
const CONSUMER = {
  name: 'consumer', role: 'processor',
  produces: [], may_produce: [], requires: ['video_year'],
};
const SINK = {
  name: 'sink', role: 'sink',
  produces: [], may_produce: [], requires: [],
};
const CONDITION = {
  name: 'condition', role: 'processor',
  produces: [], may_produce: [], requires: [],
};
const PRINT = {
  name: 'print', role: 'sink',
  produces: [], may_produce: [], requires: [],
};
const CLASSIFIER = {
  name: 'classifier', role: 'processor',
  produces: ['media_type'], may_produce: [], requires: ['title'],
};

function setupGraph(nodes, plugins) {
  ve.graphs      = [{ name: 'test', schedule: '', nodes }];
  ve.activeGraph = 0;
  ve.plugins     = plugins;
}

describe('swap_state defeats require narrowing', () => {
  it('downstream Requires=video_year warns after revival', () => {
    const src      = { id: 'n_src',  plugin: 'src',        upstreams: [],            config: {} };
    const req      = { id: 'n_req',  plugin: 'require',    upstreams: ['n_src'],     config: { fields: ['video_year'] } };
    const swap     = { id: 'n_swap', plugin: 'swap_state', upstreams: ['n_req'],     config: { swap: ['rejected', 'accepted'] } };
    const consumer = { id: 'n_con',  plugin: 'consumer',   upstreams: ['n_swap'],    config: {} };
    setupGraph([src, req, swap, consumer], [SRC, REQUIRE, SWAP_STATE, CONSUMER]);

    const warns = fieldWarnings(consumer);
    expect(warns.some(w => w.level === 'warn' && w.msg.includes('video_year'))).toBe(true);
  });

  it('without swap_state, require alone makes the field certain', () => {
    const src      = { id: 'n_src', plugin: 'src',      upstreams: [],         config: {} };
    const req      = { id: 'n_req', plugin: 'require',  upstreams: ['n_src'],  config: { fields: ['video_year'] } };
    const consumer = { id: 'n_con', plugin: 'consumer', upstreams: ['n_req'],  config: {} };
    setupGraph([src, req, consumer], [SRC, REQUIRE, CONSUMER]);

    expect(fieldWarnings(consumer).filter(w => w.msg.includes('video_year'))).toHaveLength(0);
  });
});

describe('swap_state defeats condition reject-absence narrowing', () => {
  it('warns at consumer after reject-absence + swap', () => {
    const src      = { id: 'n_src',  plugin: 'src',        upstreams: [],         config: {} };
    const cond     = { id: 'n_cond', plugin: 'condition',  upstreams: ['n_src'],  config: { reject: 'video_year == 0' } };
    const swap     = { id: 'n_swap', plugin: 'swap_state', upstreams: ['n_cond'], config: { swap: ['rejected', 'accepted'] } };
    const consumer = { id: 'n_con',  plugin: 'consumer',   upstreams: ['n_swap'], config: {} };
    setupGraph([src, cond, swap, consumer], [SRC, CONDITION, SWAP_STATE, CONSUMER]);

    const warns = fieldWarnings(consumer);
    expect(warns.some(w => w.level === 'warn' && w.msg.includes('video_year'))).toBe(true);
  });
});

describe('swap_state in benign position', () => {
  it('classifier → swap_state → sink emits no Requires warnings', () => {
    const src        = { id: 'n_src',  plugin: 'src',        upstreams: [],          config: {} };
    const classifier = { id: 'n_cls',  plugin: 'classifier', upstreams: ['n_src'],   config: {} };
    const swap       = { id: 'n_swap', plugin: 'swap_state', upstreams: ['n_cls'],   config: { swap: ['accepted', 'rejected'] } };
    const sink       = { id: 'n_sink', plugin: 'sink',       upstreams: ['n_swap'],  config: {} };
    setupGraph([src, classifier, swap, sink], [SRC, CLASSIFIER, SWAP_STATE, SINK]);

    expect(fieldWarnings(sink)).toHaveLength(0);
  });
});

describe('condition accept rule populates Accepted bucket', () => {
  it('downstream sink (Accepted-only) sees the promoted field as certain', () => {
    // src → condition(accept: video_year != '') → print
    // print's input_states defaults to ['accepted']. The accept rule moves
    // matching entries from Undecided to Accepted with video_year set, so
    // the sink reads the freshly-populated Accepted bucket.
    const src   = { id: 'n_src',  plugin: 'src',       upstreams: [],         config: {} };
    const cond  = { id: 'n_cond', plugin: 'condition', upstreams: ['n_src'],  config: { accept: 'video_year != ""' } };
    const print = { id: 'n_pr',   plugin: 'print',     upstreams: ['n_cond'], config: {} };
    setupGraph([src, cond, print], [SRC, CONDITION, PRINT]);

    const nf = computeInputFields(print);
    expect(nf.certain).toContain('video_year');
    // Source's Produces survive too — Acc inherited from Und before
    // promotion.
    expect(nf.certain).toContain('title');
  });
});

describe('merges intersect per state', () => {
  it('field certain on only one branch is not certain after merge', () => {
    // Two sources producing different fields → merge → consumer Requires X.
    const SRC_X = {
      name: 'src_x', role: 'source',
      produces: ['x', 'common'], may_produce: [], requires: [],
    };
    const SRC_Y = {
      name: 'src_y', role: 'source',
      produces: ['common'], may_produce: [], requires: [],
    };
    const MERGE = {
      name: 'merge', role: 'processor',
      produces: [], may_produce: [], requires: [],
    };
    const CONS = {
      name: 'cons', role: 'processor',
      produces: [], may_produce: [], requires: ['x'],
    };
    const a    = { id: 'a',     plugin: 'src_x', upstreams: [],          config: {} };
    const b    = { id: 'b',     plugin: 'src_y', upstreams: [],          config: {} };
    const mrg  = { id: 'mrg',   plugin: 'merge', upstreams: ['a', 'b'],  config: {} };
    const cons = { id: 'cons',  plugin: 'cons',  upstreams: ['mrg'],     config: {} };
    setupGraph([a, b, mrg, cons], [SRC_X, SRC_Y, MERGE, CONS]);

    // 'common' is on both branches → certain at consumer.
    const nf = computeInputFields(cons);
    expect(nf.certain).toContain('common');
    // 'x' is only on branch a → not certain after merge; fieldWarnings
    // should report a may-not-be-present warning, not a hard error.
    expect(nf.certain).not.toContain('x');
    const warns = fieldWarnings(cons);
    expect(warns.some(w => w.level === 'warn' && w.msg.includes('"x"'))).toBe(true);
  });
});
