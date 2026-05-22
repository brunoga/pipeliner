/**
 * Tests for the condition editor logic added to visual-editor.js:
 *   - expression parsing / serialisation (exprToFlatModel, flatModelToExpr, clauseToStr)
 *   - field-narrowing logic (condNarrowedFields — AND vs OR behaviour, semantic groups)
 *   - condition config round-trips (condRulesFromConfig, buildCondConfig)
 *   - live field computation (computeInputFields, collectOutputFields)
 */

import { describe, it, expect, beforeAll, beforeEach } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src   = readFileSync(join(__dir, '..', 'visual-editor.js'), 'utf8');

// Functions under test
let exprToFlatModel, flatModelToExpr, clauseToStr, topLevelSplit, parseClause,
    parseExprLiteral, isFullyWrapped, opIsNoValue,
    condNarrowedFields, condRejectedFields, condRejectPromotedFields,
    condRulesFromConfig, buildCondConfig,
    computeInputFields, collectOutputFields, findNodeGraph, fieldWarnings,
    ve;

beforeAll(() => {
  const mod = new Function('exports', 'document', 'fetch', src + `
    exports.exprToFlatModel     = exprToFlatModel;
    exports.flatModelToExpr     = flatModelToExpr;
    exports.clauseToStr         = clauseToStr;
    exports.topLevelSplit       = topLevelSplit;
    exports.parseClause         = parseClause;
    exports.parseExprLiteral    = parseExprLiteral;
    exports.isFullyWrapped      = isFullyWrapped;
    exports.opIsNoValue         = opIsNoValue;
    exports.condNarrowedFields  = condNarrowedFields;
    exports.condRejectedFields        = condRejectedFields;
    exports.condRejectPromotedFields  = condRejectPromotedFields;
    exports.fieldWarnings       = fieldWarnings;
    exports.condRulesFromConfig = condRulesFromConfig;
    exports.buildCondConfig     = buildCondConfig;
    exports.computeInputFields  = computeInputFields;
    exports.collectOutputFields = collectOutputFields;
    exports.findNodeGraph       = findNodeGraph;
    exports.ve                  = ve;
  `);
  const exports  = {};
  const noopDoc  = new Proxy({}, { get: () => () => null });
  mod(exports, noopDoc, () => Promise.resolve());
  ({ exprToFlatModel, flatModelToExpr, clauseToStr, topLevelSplit, parseClause,
     parseExprLiteral, isFullyWrapped, opIsNoValue,
     condNarrowedFields, condRejectedFields, condRejectPromotedFields,
     condRulesFromConfig, buildCondConfig,
     computeInputFields, collectOutputFields, findNodeGraph, fieldWarnings, ve } = exports);
});

// ── helpers ───────────────────────────────────────────────────────────────────

const RSS_PLUGIN = {
  name: 'rss', role: 'source',
  produces:    ['source', 'title', 'rss_feed'],
  may_produce: ['description', 'published_date', 'rss_guid', 'rss_category',
                'torrent_seeds', 'torrent_leechers', 'torrent_info_hash'],
};
const METAINFO_TVDB_PLUGIN = {
  name: 'metainfo_tvdb', role: 'processor',
  produces:    ['enriched'],
  may_produce: ['video_year', 'video_language', 'video_genres', 'series_season', 'series_episode'],
};
const CONDITION_PLUGIN = {
  name: 'condition', role: 'processor',
  produces: [], may_produce: [],
};
const PRINT_PLUGIN = {
  name: 'print', role: 'sink',
  produces: [], may_produce: [],
};

function setupGraph(nodes) {
  ve.graphs      = [{ name: 'test', schedule: '', nodes }];
  ve.activeGraph = 0;
  ve.plugins     = [RSS_PLUGIN, METAINFO_TVDB_PLUGIN, CONDITION_PLUGIN, PRINT_PLUGIN];
}

// ── opIsNoValue ───────────────────────────────────────────────────────────────

describe('opIsNoValue', () => {
  it('recognises no-value operators', () => {
    expect(opIsNoValue('!= ""')).toBe(true);
    expect(opIsNoValue('== ""')).toBe(true);
    expect(opIsNoValue('> 0')).toBe(true);
    expect(opIsNoValue('== 0')).toBe(true);
    expect(opIsNoValue('== true')).toBe(true);
    expect(opIsNoValue('== false')).toBe(true);
  });

  it('rejects operators that need a value', () => {
    expect(opIsNoValue('==')).toBe(false);
    expect(opIsNoValue('>=')).toBe(false);
    expect(opIsNoValue('contains')).toBe(false);
  });
});

// ── parseExprLiteral ──────────────────────────────────────────────────────────

describe('parseExprLiteral', () => {
  it('parses double-quoted string', () => {
    expect(parseExprLiteral('"hello"')).toBe('hello');
  });
  it('parses single-quoted string', () => {
    expect(parseExprLiteral("'hello'")).toBe('hello');
  });
  it('parses integer', () => {
    expect(parseExprLiteral('42')).toBe(42);
  });
  it('parses float', () => {
    expect(parseExprLiteral('7.5')).toBe(7.5);
  });
  it('parses true / false', () => {
    expect(parseExprLiteral('true')).toBe(true);
    expect(parseExprLiteral('false')).toBe(false);
  });
  it('parses empty string', () => {
    expect(parseExprLiteral('""')).toBe('');
  });
  it('returns null for bare identifiers', () => {
    expect(parseExprLiteral('foo')).toBeNull();
  });
});

// ── isFullyWrapped ────────────────────────────────────────────────────────────

describe('isFullyWrapped', () => {
  it('detects outer parens', () => {
    expect(isFullyWrapped('(a == 1)')).toBe(true);
  });
  it('returns false when outer paren closes early', () => {
    expect(isFullyWrapped('(a == 1) or (b == 2)')).toBe(false);
  });
  it('returns false for unwrapped expression', () => {
    expect(isFullyWrapped('a == 1')).toBe(false);
  });
});

// ── topLevelSplit ─────────────────────────────────────────────────────────────

describe('topLevelSplit', () => {
  it('splits on top-level " and "', () => {
    const parts = topLevelSplit('a and b and c', ' and ');
    expect(parts).toEqual(['a', 'b', 'c']);
  });
  it('does not split inside parens', () => {
    const parts = topLevelSplit('(a and b) and c', ' and ');
    expect(parts).toHaveLength(2);
    expect(parts[0]).toBe('(a and b)');
    expect(parts[1]).toBe('c');
  });
  it('does not split inside quoted strings', () => {
    const parts = topLevelSplit('title == "foo and bar" and source != ""', ' and ');
    expect(parts).toHaveLength(2);
  });
  it('returns single element when no separator found', () => {
    expect(topLevelSplit('a == 1', ' or ')).toEqual(['a == 1']);
  });
});

// ── parseClause ───────────────────────────────────────────────────────────────

describe('parseClause', () => {
  it('parses numeric comparison', () => {
    const c = parseClause('torrent_seeds >= 10');
    expect(c).toEqual({field: 'torrent_seeds', op: '>=', value: 10});
  });
  it('parses string equality', () => {
    const c = parseClause('title == "Breaking Bad"');
    expect(c).toEqual({field: 'title', op: '==', value: 'Breaking Bad'});
  });
  it('parses boolean comparison', () => {
    const c = parseClause('enriched == true');
    expect(c).toEqual({field: 'enriched', op: '==', value: true});
  });
  it('parses no-value "is set" operator', () => {
    const c = parseClause('description != ""');
    expect(c).toEqual({field: 'description', op: '!= ""', value: ''});
  });
  it('parses "is not set" operator', () => {
    const c = parseClause('description == ""');
    expect(c).toEqual({field: 'description', op: '== ""', value: ''});
  });
  it('parses contains operator', () => {
    const c = parseClause('rss_category contains "Anime"');
    expect(c).toEqual({field: 'rss_category', op: 'contains', value: 'Anime'});
  });
  it('strips outer parens', () => {
    const c = parseClause('(torrent_seeds > 0)');
    expect(c).toEqual({field: 'torrent_seeds', op: '>', value: 0});
  });
  it('returns null for negation (not supported in builder)', () => {
    expect(parseClause('not enriched')).toBeNull();
  });
  it('returns null for bare identifier', () => {
    expect(parseClause('true')).toBeNull();
  });
});

// ── exprToFlatModel ───────────────────────────────────────────────────────────

describe('exprToFlatModel', () => {
  it('parses single clause', () => {
    const m = exprToFlatModel('torrent_seeds >= 10');
    expect(m).not.toBeNull();
    expect(m.combinator).toBe('and');
    expect(m.clauses).toHaveLength(1);
    expect(m.clauses[0]).toEqual({field: 'torrent_seeds', op: '>=', value: 10});
  });

  it('parses AND expression', () => {
    const m = exprToFlatModel('enriched == true and torrent_seeds >= 10');
    expect(m).not.toBeNull();
    expect(m.combinator).toBe('and');
    expect(m.clauses).toHaveLength(2);
    expect(m.clauses[0].field).toBe('enriched');
    expect(m.clauses[1].field).toBe('torrent_seeds');
  });

  it('parses OR expression', () => {
    const m = exprToFlatModel('description != "" or rss_category != ""');
    expect(m).not.toBeNull();
    expect(m.combinator).toBe('or');
    expect(m.clauses).toHaveLength(2);
  });

  it('parses no-value "is set" pattern', () => {
    const m = exprToFlatModel('source != ""');
    expect(m).not.toBeNull();
    expect(m.clauses[0]).toEqual({field: 'source', op: '!= ""', value: ''});
  });

  it('returns null for mixed AND/OR (raw mode fallback)', () => {
    expect(exprToFlatModel('a == 1 and b == 2 or c == 3')).toBeNull();
  });

  it('returns null for NOT expression', () => {
    expect(exprToFlatModel('not enriched')).toBeNull();
  });

  it('returns an empty model for empty string (not null)', () => {
    const m = exprToFlatModel('');
    expect(m).not.toBeNull();
    expect(m.clauses).toHaveLength(0);
  });

  it('returns null for bare "true" literal', () => {
    // "true" is a valid expression but not a comparison clause
    expect(exprToFlatModel('true')).toBeNull();
  });

  it('handles three AND clauses', () => {
    const m = exprToFlatModel('a != "" and b != "" and c != ""');
    expect(m.combinator).toBe('and');
    expect(m.clauses).toHaveLength(3);
  });
});

// ── clauseToStr ───────────────────────────────────────────────────────────────

describe('clauseToStr', () => {
  it('serialises numeric comparison', () => {
    expect(clauseToStr('torrent_seeds', '>=', 10)).toBe('torrent_seeds >= 10');
  });
  it('serialises string comparison with quotes', () => {
    expect(clauseToStr('title', '==', 'foo')).toBe('title == "foo"');
  });
  it('serialises boolean', () => {
    expect(clauseToStr('enriched', '==', true)).toBe('enriched == true');
  });
  it('serialises no-value op without a value token', () => {
    expect(clauseToStr('source', '!= ""', '')).toBe('source != ""');
    expect(clauseToStr('description', '== ""', '')).toBe('description == ""');
  });
  it('escapes double quotes in string values', () => {
    expect(clauseToStr('title', '==', 'say "hi"')).toBe('title == "say \\"hi\\""');
  });
  it('uses single-quote for empty string value to avoid collision with == "" / != "" ops', () => {
    // Without this, clauseToStr('f','==','') → 'f == ""' which parseClause
    // re-parses as op='== ""' (is not set), resetting the dropdown.
    expect(clauseToStr('torrent_link_type', '==', '')).toBe("torrent_link_type == ''");
    expect(clauseToStr('title', '!=', '')).toBe("title != ''");
  });
  it('empty-string round-trip: clauseToStr → exprToFlatModel preserves op', () => {
    const expr  = clauseToStr('torrent_link_type', '==', '');
    const model = exprToFlatModel(expr);
    expect(model).not.toBeNull();
    expect(model.clauses[0].op).toBe('==');
    expect(model.clauses[0].value).toBe('');
  });
});

// ── flatModelToExpr ───────────────────────────────────────────────────────────

describe('flatModelToExpr', () => {
  it('serialises single clause', () => {
    expect(flatModelToExpr([{field:'source', op:'!= ""', value:''}], 'and'))
      .toBe('source != ""');
  });
  it('serialises AND of two clauses', () => {
    const clauses = [
      {field:'enriched',    op:'== true', value:''},
      {field:'torrent_seeds', op:'>=',   value:10},
    ];
    expect(flatModelToExpr(clauses, 'and'))
      .toBe('enriched == true and torrent_seeds >= 10');
  });
  it('serialises OR of two clauses', () => {
    const clauses = [
      {field:'description', op:'!= ""', value:''},
      {field:'rss_category', op:'!= ""', value:''},
    ];
    expect(flatModelToExpr(clauses, 'or'))
      .toBe('description != "" or rss_category != ""');
  });
  it('returns empty string for empty clause list', () => {
    expect(flatModelToExpr([], 'and')).toBe('');
  });
});

// ── exprToFlatModel ↔ flatModelToExpr round-trip ──────────────────────────────

describe('expression round-trip', () => {
  const cases = [
    'torrent_seeds >= 10',
    'enriched == true and torrent_seeds > 0',
    'description != "" or rss_category != ""',
    'source != ""',
    'title == "foo"',
  ];
  for (const expr of cases) {
    it(`round-trips: ${expr}`, () => {
      const m = exprToFlatModel(expr);
      expect(m).not.toBeNull();
      expect(flatModelToExpr(m.clauses, m.combinator)).toBe(expr);
    });
  }
});

// ── condRejectedFields ────────────────────────────────────────────────────────

describe('condRejectedFields — single clause', () => {
  const nf = {reachable: ['source', 'title', 'description', 'rss_category', 'torrent_seeds']};

  it('removes field when reject uses is-set op (!= "")', () => {
    const removed = condRejectedFields('description != ""', nf);
    expect(removed).toContain('description');
  });

  it('removes field when reject uses numeric is-set op (> 0)', () => {
    const removed = condRejectedFields('torrent_seeds > 0', nf);
    expect(removed).toContain('torrent_seeds');
  });

  it('does NOT remove field for non-presence ops (specific value reject)', () => {
    // reject: torrent_seeds > 5 — passing entries can still have seeds (0..5)
    const removed = condRejectedFields('torrent_seeds > 5', nf);
    expect(removed).not.toContain('torrent_seeds');
  });

  it('does NOT remove field for equality reject', () => {
    // reject: description == "foo" — field might still be present with other value
    const removed = condRejectedFields('description == "foo"', nf);
    expect(removed).not.toContain('description');
  });

  it('does NOT remove field not in reachable', () => {
    const removed = condRejectedFields('movie_tagline != ""', nf);
    expect(removed).not.toContain('movie_tagline');
  });

  it('returns empty for empty expression', () => {
    expect(condRejectedFields('', nf)).toHaveLength(0);
  });
});

describe('condRejectedFields — AND behaviour', () => {
  const nf = {reachable: ['description', 'rss_category']};

  it('does NOT remove fields for multi-clause AND (NOT(A∧B) = ¬A∨¬B)', () => {
    // Passing entries satisfy ¬description∨¬rss_category — can't guarantee either absent
    const removed = condRejectedFields('description != "" and rss_category != ""', nf);
    expect(removed).toHaveLength(0);
  });

  it('removes field for single-clause AND (degenerate case)', () => {
    const removed = condRejectedFields('description != ""', nf);
    expect(removed).toContain('description');
  });
});

describe('condRejectedFields — OR behaviour', () => {
  const nf = {reachable: ['description', 'rss_category', 'torrent_seeds']};

  it('removes BOTH fields when all OR clauses use presence ops (NOT(A∨B) = ¬A∧¬B)', () => {
    const removed = condRejectedFields('description != "" or rss_category != ""', nf);
    expect(removed).toContain('description');
    expect(removed).toContain('rss_category');
  });

  it('removes NOTHING when not all OR clauses use presence ops', () => {
    // description != "" is presence, but torrent_seeds > 5 is not
    const removed = condRejectedFields('description != "" or torrent_seeds > 5', nf);
    expect(removed).toHaveLength(0);
  });
});

// ── condRejectPromotedFields ──────────────────────────────────────────────────

describe('condRejectPromotedFields — absence-check rejection promotes field', () => {
  const nf = {
    certain:  ['source'],
    reachable:['source', 'description', 'torrent_files', 'rss_category'],
  };

  it('promotes field when reject uses absence op (== "")', () => {
    const promoted = condRejectPromotedFields('torrent_files == ""', nf);
    expect(promoted).toContain('torrent_files');
  });

  it('promotes field when reject uses numeric absence op (== 0)', () => {
    const promoted = condRejectPromotedFields('torrent_files == 0', nf);
    expect(promoted).toContain('torrent_files');
  });

  it('does NOT promote field for presence ops (those belong to condRejectedFields)', () => {
    // reject: field != "" removes the field; condRejectPromotedFields should return []
    expect(condRejectPromotedFields('torrent_files != ""', nf)).toHaveLength(0);
  });

  it('does NOT promote field not in reachable', () => {
    expect(condRejectPromotedFields('movie_tagline == ""', nf)).not.toContain('movie_tagline');
  });

  it('does NOT promote already-certain field', () => {
    expect(condRejectPromotedFields('source == ""', nf)).not.toContain('source');
  });

  it('does NOT promote for multi-clause AND (NOT(A∧B) = ¬A∨¬B — ambiguous)', () => {
    const p = condRejectPromotedFields('description == "" and rss_category == ""', nf);
    expect(p).toHaveLength(0);
  });

  it('promotes BOTH fields for OR of absence ops (NOT(A∨B) = ¬A∧¬B)', () => {
    const p = condRejectPromotedFields('description == "" or rss_category == ""', nf);
    expect(p).toContain('description');
    expect(p).toContain('rss_category');
  });

  it('does NOT promote for mixed OR (presence + absence ops)', () => {
    const p = condRejectPromotedFields('description == "" or torrent_files != ""', nf);
    expect(p).toHaveLength(0);
  });
});

// ── computeInputFields — reject rule removes fields downstream ────────────────

describe('computeInputFields — reject rule narrows downstream', () => {
  beforeEach(() => {
    ve.plugins = [RSS_PLUGIN, CONDITION_PLUGIN, PRINT_PLUGIN];
  });

  it('removes field from downstream reachable when reject rule uses is-set', () => {
    const rssNode  = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const condNode = {
      id:'cond_1', plugin:'condition', upstreams:['rss_0'],
      config:{reject:'description != ""'},
    };
    const printNode = {id:'print_2', plugin:'print', upstreams:['cond_1'], config:{}};
    setupGraph([rssNode, condNode, printNode]);

    const nf = computeInputFields(printNode);
    // description was in rss MayProduce, but is removed by the reject rule
    expect(nf.reachable).not.toContain('description');
    expect(nf.certain).not.toContain('description');
    // Other rss fields should still be present
    expect(nf.certain).toContain('source');
    expect(nf.reachable).toContain('torrent_seeds');
  });

  it('removes fields for OR reject rule (both absent in passing entries)', () => {
    const rssNode  = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const condNode = {
      id:'cond_1', plugin:'condition', upstreams:['rss_0'],
      config:{reject:'description != "" or rss_category != ""'},
    };
    const printNode = {id:'print_2', plugin:'print', upstreams:['cond_1'], config:{}};
    setupGraph([rssNode, condNode, printNode]);

    const nf = computeInputFields(printNode);
    expect(nf.reachable).not.toContain('description');
    expect(nf.reachable).not.toContain('rss_category');
  });

  it('does NOT remove fields for AND reject rule (ambiguous which is absent)', () => {
    const rssNode  = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const condNode = {
      id:'cond_1', plugin:'condition', upstreams:['rss_0'],
      config:{reject:'description != "" and rss_category != ""'},
    };
    const printNode = {id:'print_2', plugin:'print', upstreams:['cond_1'], config:{}};
    setupGraph([rssNode, condNode, printNode]);

    const nf = computeInputFields(printNode);
    // Can't guarantee which field is absent — both may still appear
    expect(nf.reachable).toContain('description');
    expect(nf.reachable).toContain('rss_category');
  });

  it('promotes field when reject rule uses absence op (== "") — the torrent_files case', () => {
    // This is the exact scenario the user reported:
    //   metainfo_torrent MayProduces torrent_files
    //   condition(reject: torrent_files == "") gates the content node
    //   → entries that pass have torrent_files SET → should be certain downstream
    const rssNode  = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const metaNode = {id:'meta_1', plugin:'metainfo_torrent', upstreams:['rss_0'], config:{}};
    const condNode = {
      id:'cond_2', plugin:'condition', upstreams:['meta_1'],
      config:{reject:'torrent_files == ""'},
    };
    const printNode = {id:'print_3', plugin:'print', upstreams:['cond_2'], config:{}};
    setupWarningGraph([rssNode, metaNode, condNode, printNode]);

    const nf = computeInputFields(printNode);
    // torrent_files was in MayProduce; the reject rule guarantees it is set
    expect(nf.certain).toContain('torrent_files');
  });
});

// ── condRulesFromConfig / buildCondConfig ─────────────────────────────────────

describe('condRulesFromConfig', () => {
  it('reads single accept key', () => {
    const rules = condRulesFromConfig({accept: 'source != ""'});
    expect(rules).toEqual([{type:'accept', expr:'source != ""'}]);
  });
  it('reads single reject key', () => {
    const rules = condRulesFromConfig({reject: 'torrent_seeds == 0'});
    expect(rules).toEqual([{type:'reject', expr:'torrent_seeds == 0'}]);
  });
  it('reads rules array', () => {
    const rules = condRulesFromConfig({
      rules: [{reject:'torrent_seeds == 0'}, {accept:'enriched == true'}],
    });
    expect(rules).toHaveLength(2);
    expect(rules[0]).toEqual({type:'reject', expr:'torrent_seeds == 0'});
    expect(rules[1]).toEqual({type:'accept', expr:'enriched == true'});
  });
  it('returns empty array for null config', () => {
    expect(condRulesFromConfig(null)).toEqual([]);
    expect(condRulesFromConfig({})).toEqual([]);
  });
});

describe('buildCondConfig', () => {
  it('single rule → top-level key', () => {
    expect(buildCondConfig([{type:'accept', expr:'source != ""'}]))
      .toEqual({accept:'source != ""'});
  });
  it('multiple rules → rules array', () => {
    const cfg = buildCondConfig([
      {type:'reject', expr:'torrent_seeds == 0'},
      {type:'accept', expr:'true'},
    ]);
    expect(cfg).toEqual({rules:[
      {reject:'torrent_seeds == 0'},
      {accept:'true'},
    ]});
  });
  it('filters out empty expressions', () => {
    expect(buildCondConfig([{type:'accept', expr:''}])).toEqual({});
  });
  it('trims whitespace from expressions', () => {
    expect(buildCondConfig([{type:'accept', expr:'  source != ""  '}]))
      .toEqual({accept:'source != ""'});
  });
});

describe('condRulesFromConfig / buildCondConfig round-trip', () => {
  it('round-trips single rule', () => {
    const original = {accept: 'description != ""'};
    const rules    = condRulesFromConfig(original);
    expect(buildCondConfig(rules)).toEqual(original);
  });
  it('round-trips multiple rules', () => {
    const original = {rules:[{reject:'a == ""'},{accept:'b != ""'}]};
    const rules    = condRulesFromConfig(original);
    expect(buildCondConfig(rules)).toEqual(original);
  });
});

// ── condNarrowedFields ────────────────────────────────────────────────────────

describe('condNarrowedFields — AND behaviour', () => {
  const nf = {
    certain:  ['source', 'title'],
    reachable:['source', 'title', 'description', 'torrent_seeds', 'enriched',
               'video_year', 'video_language', 'video_genres', 'video_rating',
               'video_popularity', 'video_imdb_id',
               'series_season', 'series_episode', 'series_episode_id',
               'torrent_leechers', 'torrent_file_size', 'torrent_info_hash'],
  };

  it('promotes field referenced in a simple comparison', () => {
    const promoted = condNarrowedFields('torrent_seeds >= 10', nf);
    expect(promoted).toContain('torrent_seeds');
  });

  it('promotes field from "is set" no-value operator', () => {
    const promoted = condNarrowedFields('description != ""', nf);
    expect(promoted).toContain('description');
  });

  it('promotes fields from both sides of AND', () => {
    const promoted = condNarrowedFields('description != "" and torrent_seeds > 0', nf);
    expect(promoted).toContain('description');
    expect(promoted).toContain('torrent_seeds');
  });

  it('does not promote fields already certain', () => {
    const promoted = condNarrowedFields('source != ""', nf);
    expect(promoted).not.toContain('source'); // already certain
  });

  it('does not promote fields not in reachable', () => {
    const promoted = condNarrowedFields('movie_tagline != ""', nf);
    expect(promoted).not.toContain('movie_tagline');
  });

  it('fires enriched semantic group for AND', () => {
    const promoted = condNarrowedFields('enriched == true', nf);
    expect(promoted).toContain('video_year');
    expect(promoted).toContain('video_language');
  });

  it('fires series_episode_id semantic group', () => {
    const promoted = condNarrowedFields('series_episode_id != ""', nf);
    expect(promoted).toContain('series_season');
    expect(promoted).toContain('series_episode');
  });
});

describe('condNarrowedFields — OR behaviour', () => {
  const nf = {
    certain:  ['source'],
    reachable:['source', 'description', 'rss_category', 'torrent_seeds', 'enriched',
               'video_year', 'video_language'],
  };

  it('promotes NOTHING when different fields in each OR branch', () => {
    const promoted = condNarrowedFields('description != "" or rss_category != ""', nf);
    expect(promoted).not.toContain('description');
    expect(promoted).not.toContain('rss_category');
  });

  it('promotes field present in BOTH OR branches (intersection)', () => {
    const promoted = condNarrowedFields('description != "" or description contains "x"', nf);
    expect(promoted).toContain('description');
  });

  it('does NOT fire semantic groups for OR conditions', () => {
    // enriched may be true on one branch and something else on the other
    const promoted = condNarrowedFields('enriched == true or description != ""', nf);
    expect(promoted).not.toContain('video_year');
    expect(promoted).not.toContain('video_language');
  });

  it('behaves like AND for single-clause expression', () => {
    const promoted = condNarrowedFields('torrent_seeds >= 10', nf);
    expect(promoted).toContain('torrent_seeds');
  });
});

// ── computeInputFields / collectOutputFields ───────────────────────────────────

describe('computeInputFields', () => {
  beforeEach(() => {
    ve.graphs      = [];
    ve.activeGraph = 0;
    ve.plugins     = [RSS_PLUGIN, METAINFO_TVDB_PLUGIN, CONDITION_PLUGIN, PRINT_PLUGIN];
  });

  it('returns empty for source node (no upstreams)', () => {
    const rssNode = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    setupGraph([rssNode]);
    const nf = computeInputFields(rssNode);
    expect(nf.certain).toHaveLength(0);
    expect(nf.reachable).toHaveLength(0);
  });

  it('returns source produces as certain at its direct downstream', () => {
    const rssNode  = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const condNode = {id:'cond_1', plugin:'condition', upstreams:['rss_0'], config:{}};
    setupGraph([rssNode, condNode]);

    const nf = computeInputFields(condNode);
    expect(nf.certain).toContain('source');
    expect(nf.certain).toContain('title');
    expect(nf.certain).toContain('rss_feed');
  });

  it('returns source may_produce as reachable (not certain)', () => {
    const rssNode  = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const condNode = {id:'cond_1', plugin:'condition', upstreams:['rss_0'], config:{}};
    setupGraph([rssNode, condNode]);

    const nf = computeInputFields(condNode);
    expect(nf.certain).not.toContain('description');
    expect(nf.reachable).toContain('description');
  });

  it('accumulates fields through a multi-hop chain', () => {
    const rssNode  = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const tvdbNode = {id:'tvdb_1', plugin:'metainfo_tvdb', upstreams:['rss_0'], config:{}};
    const printNode= {id:'print_2', plugin:'print', upstreams:['tvdb_1'], config:{}};
    setupGraph([rssNode, tvdbNode, printNode]);

    const nf = computeInputFields(printNode);
    // From rss (via metainfo_tvdb passthrough):
    expect(nf.certain).toContain('source');
    // From metainfo_tvdb itself:
    expect(nf.certain).toContain('enriched');
    expect(nf.reachable).toContain('video_year');
  });

  it('promotes field to certain when condition accept rule guarantees it', () => {
    const rssNode  = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const condNode = {
      id:'cond_1', plugin:'condition', upstreams:['rss_0'],
      config:{accept:'description != ""'},
    };
    const printNode= {id:'print_2', plugin:'print', upstreams:['cond_1'], config:{}};
    setupGraph([rssNode, condNode, printNode]);

    const nf = computeInputFields(printNode);
    expect(nf.certain).toContain('description');
  });

  it('does not promote OR-condition fields that differ per branch', () => {
    const rssNode  = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const condNode = {
      id:'cond_1', plugin:'condition', upstreams:['rss_0'],
      config:{accept:'description != "" or rss_category != ""'},
    };
    const printNode= {id:'print_2', plugin:'print', upstreams:['cond_1'], config:{}};
    setupGraph([rssNode, condNode, printNode]);

    const nf = computeInputFields(printNode);
    // Neither is guaranteed — only one of the two will be present
    expect(nf.certain).not.toContain('description');
    expect(nf.certain).not.toContain('rss_category');
  });

  it('reflects changes when upstream is removed', () => {
    const rssNode  = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const printNode= {id:'print_1', plugin:'print', upstreams:['rss_0'], config:{}};
    setupGraph([rssNode, printNode]);

    // With rss connected: fields should be present
    let nf = computeInputFields(printNode);
    expect(nf.certain).toContain('source');

    // Remove the connection (update upstreams to empty)
    printNode.upstreams = [];
    nf = computeInputFields(printNode);
    expect(nf.certain).toHaveLength(0);
    expect(nf.reachable).toHaveLength(0);
  });

  it('does not leak fields from a different pipeline (same-graph restriction)', () => {
    // Second pipeline with trakt_list producing trakt_id
    const traktNode = {id:'trakt_0', plugin:'trakt_list', upstreams:[], config:{}};
    ve.plugins.push({
      name:'trakt_list', role:'source',
      produces:[], may_produce:['trakt_id','trakt_imdb_id'],
    });

    const rssNode   = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const printNode = {id:'print_1', plugin:'print', upstreams:['rss_0'], config:{}};

    // Two graphs: test (rss→print) and other (trakt)
    ve.graphs = [
      {name:'test',  nodes:[rssNode, printNode]},
      {name:'other', nodes:[traktNode]},
    ];
    ve.activeGraph = 0;

    const nf = computeInputFields(printNode);
    // Fields from other pipeline must not appear
    expect(nf.reachable).not.toContain('trakt_id');
    expect(nf.reachable).not.toContain('trakt_imdb_id');
    // Fields from same pipeline still present
    expect(nf.certain).toContain('source');
  });
});

// ── fieldWarnings — condition narrowing clears warnings ───────────────────────

const METAINFO_TORRENT_PLUGIN = {
  name: 'metainfo_torrent', role: 'processor',
  produces: [], may_produce: ['torrent_files', 'torrent_info_hash', 'torrent_file_size'],
};
const CONTENT_PLUGIN = {
  name: 'content', role: 'processor',
  produces: [], may_produce: [],
  requires: ['torrent_files'],
};

// Helper that sets both graphs and the full plugin list for fieldWarnings tests.
function setupWarningGraph(nodes) {
  ve.graphs      = [{ name: 'test', schedule: '', nodes }];
  ve.activeGraph = 0;
  ve.plugins     = [RSS_PLUGIN, METAINFO_TORRENT_PLUGIN, CONDITION_PLUGIN, CONTENT_PLUGIN, PRINT_PLUGIN];
}

describe('fieldWarnings', () => {

  it('emits soft warning when required field is only in may_produce upstream', () => {
    const rssNode    = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const metaNode   = {id:'meta_1', plugin:'metainfo_torrent', upstreams:['rss_0'], config:{}};
    const contentNode= {id:'content_2', plugin:'content', upstreams:['meta_1'], config:{}};
    setupWarningGraph([rssNode, metaNode, contentNode]);

    const warns = fieldWarnings(contentNode);
    const warnLevels = warns.map(w => w.level);
    expect(warnLevels).toContain('warn');
    expect(warns.some(w => w.msg.includes('torrent_files'))).toBe(true);
  });

  it('clears soft warning when condition accept rule guarantees the required field', () => {
    // rss → metainfo_torrent → condition(accept: torrent_files != "") → content
    // The condition guarantees torrent_files is present, so content should see it as certain.
    const rssNode    = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const metaNode   = {id:'meta_1', plugin:'metainfo_torrent', upstreams:['rss_0'], config:{}};
    const condNode   = {
      id:'cond_2', plugin:'condition', upstreams:['meta_1'],
      config:{accept:'torrent_files != ""'},
    };
    const contentNode= {id:'content_3', plugin:'content', upstreams:['cond_2'], config:{}};
    setupWarningGraph([rssNode, metaNode, condNode, contentNode]);

    const warns = fieldWarnings(contentNode);
    // The condition accept rule guarantees torrent_files → no warning.
    expect(warns.filter(w => w.msg.includes('torrent_files'))).toHaveLength(0);
  });

  it('emits error when required field has no upstream producer at all', () => {
    // rss → content (torrent_files not produced by anything upstream)
    const rssNode    = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const contentNode= {id:'content_1', plugin:'content', upstreams:['rss_0'], config:{}};
    setupWarningGraph([rssNode, contentNode]);

    const warns = fieldWarnings(contentNode);
    expect(warns.some(w => w.level === 'error' && w.msg.includes('torrent_files'))).toBe(true);
  });

  it('emits soft warning for OR condition (field only guaranteed on one branch)', () => {
    // OR condition: NOT both guaranteed, so torrent_files stays conditional.
    const rssNode    = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    const metaNode   = {id:'meta_1', plugin:'metainfo_torrent', upstreams:['rss_0'], config:{}};
    const condNode   = {
      id:'cond_2', plugin:'condition', upstreams:['meta_1'],
      config:{accept:'torrent_files != "" or torrent_info_hash != ""'},
    };
    const contentNode= {id:'content_3', plugin:'content', upstreams:['cond_2'], config:{}};
    setupWarningGraph([rssNode, metaNode, condNode, contentNode]);

    const warns = fieldWarnings(contentNode);
    // OR means only one branch matched — torrent_files not certain → still warns.
    expect(warns.some(w => w.level === 'warn' && w.msg.includes('torrent_files'))).toBe(true);
  });

  it('emits no warnings when node has no requires', () => {
    const rssNode = {id:'rss_0', plugin:'rss', upstreams:[], config:{}};
    setupWarningGraph([rssNode]);
    expect(fieldWarnings(rssNode)).toHaveLength(0);
  });
});

// ── collectOutputFields — route port narrowing ────────────────────────────────
// Tests the isRoutePort branch added to collectOutputFields: a route port node
// applies both condNarrowedFields (presence → promote) and
// condAcceptAbsenceRemovedFields (absence → remove) to downstream field sets.

const ROUTE_PLUGIN = {
  name: 'route', role: 'processor',
  produces:    ['_route_port'],
  may_produce: [],
};
const JACKETT_PLUGIN = {
  name: 'jackett', role: 'source',
  produces:    ['source', 'title'],
  may_produce: ['torrent_url', 'magnet_url', 'series_episode_id', 'description'],
};

function setupRouteGraph(nodes) {
  ve.graphs      = [{ name: 'test', schedule: '', nodes }];
  ve.activeGraph = 0;
  ve.plugins     = [RSS_PLUGIN, ROUTE_PLUGIN, JACKETT_PLUGIN, CONDITION_PLUGIN, PRINT_PLUGIN];
}

describe('collectOutputFields — route port nodes', () => {
  it('presence-check accept promotes field to certain for downstream node', () => {
    // jackett → route → port(accept: "series_episode_id != ''") → print
    const src      = {id:'src_0',  plugin:'jackett', upstreams:[], config:{}};
    const routeN   = {id:'route_1', plugin:'route',   upstreams:['src_0'], config:{rules:[]}};
    const portNode = {
      id:'port_2', plugin:'route_selector', upstreams:['route_1'],
      config:{}, isRoutePort:true, routeParentId:'route_1', routePortName:'tv',
      portAcceptExpr: "series_episode_id != ''",
    };
    const downstream = {id:'print_3', plugin:'print', upstreams:['port_2'], config:{}};
    setupRouteGraph([src, routeN, portNode, downstream]);

    const fields = computeInputFields(downstream);
    // series_episode_id was reachable (may_produce of jackett); the port promotes it to certain
    expect(fields.certain).toContain('series_episode_id');
  });

  it('absence-check accept removes field from reachable for downstream node', () => {
    // jackett → route → port(accept: "magnet_url == ''") → print
    const src      = {id:'src_0',  plugin:'jackett', upstreams:[], config:{}};
    const routeN   = {id:'route_1', plugin:'route',   upstreams:['src_0'], config:{rules:[]}};
    const portNode = {
      id:'port_2', plugin:'route_selector', upstreams:['route_1'],
      config:{}, isRoutePort:true, routeParentId:'route_1', routePortName:'no-magnet',
      portAcceptExpr: "magnet_url == ''",
    };
    const downstream = {id:'print_3', plugin:'print', upstreams:['port_2'], config:{}};
    setupRouteGraph([src, routeN, portNode, downstream]);

    const fields = computeInputFields(downstream);
    // magnet_url was reachable from jackett; absence-check removes it entirely
    const all = [...fields.certain, ...fields.reachable];
    expect(all).not.toContain('magnet_url');
  });

  it('AND expression: promotes presence-op fields AND removes absence-op fields', () => {
    // port: "torrent_url != '' and magnet_url == ''"
    const src      = {id:'src_0',  plugin:'jackett', upstreams:[], config:{}};
    const routeN   = {id:'route_1', plugin:'route',   upstreams:['src_0'], config:{rules:[]}};
    const portNode = {
      id:'port_2', plugin:'route_selector', upstreams:['route_1'],
      config:{}, isRoutePort:true, routeParentId:'route_1', routePortName:'torrent',
      portAcceptExpr: "torrent_url != '' and magnet_url == ''",
    };
    const downstream = {id:'print_3', plugin:'print', upstreams:['port_2'], config:{}};
    setupRouteGraph([src, routeN, portNode, downstream]);

    const fields = computeInputFields(downstream);
    expect(fields.certain).toContain('torrent_url');
    const all = [...fields.certain, ...fields.reachable];
    expect(all).not.toContain('magnet_url');
  });

  it('port with no accept expression passes fields through unchanged', () => {
    const src      = {id:'src_0',  plugin:'jackett', upstreams:[], config:{}};
    const routeN   = {id:'route_1', plugin:'route',   upstreams:['src_0'], config:{rules:[]}};
    const portNode = {
      id:'port_2', plugin:'route_selector', upstreams:['route_1'],
      config:{}, isRoutePort:true, routeParentId:'route_1', routePortName:'all',
      portAcceptExpr: '',
    };
    const downstream = {id:'print_3', plugin:'print', upstreams:['port_2'], config:{}};
    setupRouteGraph([src, routeN, portNode, downstream]);

    const fields = computeInputFields(downstream);
    // Upstream fields still flow through; nothing promoted or removed
    expect([...fields.certain, ...fields.reachable]).toContain('torrent_url');
    expect([...fields.certain, ...fields.reachable]).toContain('magnet_url');
    expect(fields.certain).not.toContain('torrent_url'); // still reachable-only
  });
});
