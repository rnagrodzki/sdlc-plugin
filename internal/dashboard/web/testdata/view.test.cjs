'use strict';

/**
 * Tests for the dashboard page logic module (internal/dashboard/web/static/view.js).
 * Uses Node's built-in test runner (node --test) — no npm install required,
 * consistent with the project's other .cjs test files. view.js has no DOM
 * access, so it is required directly here exactly as app.js uses it in the
 * browser. The DOM builders of render.js run here on a hand-written fake
 * document (fakeDoc below).
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const view = require('../static/view.js');

const SHIP_STATE_SCHEMA_PATH = path.join(
  __dirname,
  '../../../../plugins/sdlc/schemas/ship-state.schema.json'
);

function readStepStatusEnum() {
  const schema = JSON.parse(fs.readFileSync(SHIP_STATE_SCHEMA_PATH, 'utf8'));
  return schema.properties.steps.items.properties.status.enum;
}

function readStepNameEnum() {
  const schema = JSON.parse(fs.readFileSync(SHIP_STATE_SCHEMA_PATH, 'utf8'));
  return schema.properties.steps.items.properties.name.enum;
}

describe('stepGlyph', () => {
  test('keys equal the steps[].status enum of ship-state.schema.json', () => {
    const glyphStatuses = ['completed', 'skipped', 'in_progress', 'failed', 'pending'];
    const schemaStatuses = readStepStatusEnum();
    assert.deepEqual(glyphStatuses.slice().sort(), schemaStatuses.slice().sort());
  });

  test('maps each step status to one glyph and one color token', () => {
    assert.deepEqual(view.stepGlyph('completed'), { glyph: '●', token: 'signal-green' });
    assert.deepEqual(view.stepGlyph('skipped'), { glyph: '○', token: 'rail' });
    assert.deepEqual(view.stepGlyph('in_progress'), { glyph: '◉', token: 'signal-amber' });
    assert.deepEqual(view.stepGlyph('failed'), { glyph: '✕', token: 'signal-red' });
    assert.deepEqual(view.stepGlyph('pending'), { glyph: '○', token: 'rail' });
  });
});

describe('pipelineTone', () => {
  test('knows exactly the 4 pipeline status values', () => {
    const known = ['failed', 'stalled', 'running', 'completed'];
    for (const status of known) {
      assert.equal(typeof view.pipelineTone(status), 'string');
    }
    for (const status of ['pending', 'in_progress', 'skipped', 'unknown', '']) {
      assert.equal(view.pipelineTone(status), undefined);
    }
  });

  test('maps each pipeline status to its color token', () => {
    assert.equal(view.pipelineTone('failed'), 'signal-red');
    assert.equal(view.pipelineTone('stalled'), 'rail');
    assert.equal(view.pipelineTone('running'), 'signal-amber');
    assert.equal(view.pipelineTone('completed'), 'signal-green');
  });
});

describe('feedOrder', () => {
  test('puts unfinished pipelines first, then completed, input order kept', () => {
    const c1 = { id: 'c1', status: 'completed' };
    const r1 = { id: 'r1', status: 'running' };
    const c2 = { id: 'c2', status: 'completed' };
    const f1 = { id: 'f1', status: 'failed' };
    const s1 = { id: 's1', status: 'stalled' };
    const ordered = view.feedOrder([c1, r1, c2, f1, s1]);
    assert.deepEqual(ordered.map((p) => p.id), ['r1', 'f1', 's1', 'c1', 'c2']);
  });

  test('returns a new array and does not mutate the input', () => {
    const input = [{ status: 'completed' }, { status: 'failed' }];
    const ordered = view.feedOrder(input);
    assert.notEqual(ordered, input);
    assert.equal(input[0].status, 'completed');
  });

  test('returns an empty array for a missing list', () => {
    assert.deepEqual(view.feedOrder(undefined), []);
  });

  test('returns 5 groups in order: attention, running, failed, stalled, completed', () => {
    const attention = { kind: 'question', askedAt: '2026-10-08T10:00:00Z', header: 'Q', text: 't' };
    const c1 = { id: 'c1', status: 'completed' };
    const s1 = { id: 's1', status: 'stalled' };
    const f1 = { id: 'f1', status: 'failed' };
    const r1 = { id: 'r1', status: 'running' };
    const a1 = { id: 'a1', status: 'running', attention };
    const ordered = view.feedOrder([c1, s1, f1, r1, a1]);
    assert.deepEqual(ordered.map((p) => p.id), ['a1', 'r1', 'f1', 's1', 'c1']);
  });

  test('an attention pipeline goes first whatever its status', () => {
    const attention = { kind: 'permission', askedAt: '2026-10-08T10:00:00Z', header: 'Permission', text: 't' };
    const r1 = { id: 'r1', status: 'running' };
    const c1 = { id: 'c1', status: 'completed', attention };
    assert.deepEqual(view.feedOrder([r1, c1]).map((p) => p.id), ['c1', 'r1']);
  });

  test('inside a group the newer startedAt comes first', () => {
    const old = { id: 'old', status: 'running', startedAt: '2026-10-08T08:00:00Z' };
    const mid = { id: 'mid', status: 'running', startedAt: '2026-10-08T09:00:00Z' };
    const fresh = { id: 'fresh', status: 'running', startedAt: '2026-10-08T10:00:00Z' };
    assert.deepEqual(view.feedOrder([old, fresh, mid]).map((p) => p.id), ['fresh', 'mid', 'old']);
  });

  test('startedAt orders by time, not by text, across UTC offsets', () => {
    // 10:00+02:00 is 08:00Z, which is older than 09:00Z although its text sorts later.
    const offset = { id: 'offset', status: 'failed', startedAt: '2026-10-08T10:00:00+02:00' };
    const utc = { id: 'utc', status: 'failed', startedAt: '2026-10-08T09:00:00Z' };
    assert.deepEqual(view.feedOrder([offset, utc]).map((p) => p.id), ['utc', 'offset']);
  });

  test('an empty or unreadable startedAt comes last inside its group', () => {
    const none = { id: 'none', status: 'stalled', startedAt: '' };
    const bad = { id: 'bad', status: 'stalled', startedAt: 'not a time' };
    const absent = { id: 'absent', status: 'stalled' };
    const dated = { id: 'dated', status: 'stalled', startedAt: '2026-10-08T09:00:00Z' };
    assert.deepEqual(view.feedOrder([none, bad, absent, dated]).map((p) => p.id), ['dated', 'none', 'bad', 'absent']);
  });

  test('the sort is stable: equal pipelines keep their input order', () => {
    const t = '2026-10-08T09:00:00Z';
    const list = ['a', 'b', 'c', 'd', 'e', 'f'].map((id) => ({ id, status: 'completed', startedAt: t }));
    assert.deepEqual(view.feedOrder(list).map((p) => p.id), ['a', 'b', 'c', 'd', 'e', 'f']);
  });

  test('returns the same objects, not copies', () => {
    const r1 = { id: 'r1', status: 'running' };
    const c1 = { id: 'c1', status: 'completed' };
    const ordered = view.feedOrder([c1, r1]);
    assert.equal(ordered[0], r1);
    assert.equal(ordered[1], c1);
  });

  test('a missing entry or an unknown status ranks with the running group', () => {
    const c1 = { id: 'c1', status: 'completed' };
    const f1 = { id: 'f1', status: 'failed' };
    const odd = { id: 'odd', status: 'weird' };
    const ordered = view.feedOrder([c1, f1, null, odd]);
    assert.deepEqual(ordered.map((p) => (p ? p.id : null)), [null, 'odd', 'f1', 'c1']);
  });
});

describe('compareStartedDesc', () => {
  const older = { startedAt: '2026-10-08T08:00:00Z' };
  const newer = { startedAt: '2026-10-08T09:00:00Z' };

  test('a newer pipeline comes first', () => {
    assert.ok(view.compareStartedDesc(newer, older) < 0);
    assert.ok(view.compareStartedDesc(older, newer) > 0);
  });

  test('the same start time gives 0', () => {
    assert.equal(view.compareStartedDesc(newer, { startedAt: '2026-10-08T09:00:00Z' }), 0);
  });

  test('an empty startedAt comes after one that has a time', () => {
    assert.equal(view.compareStartedDesc({ startedAt: '' }, newer), 1);
    assert.equal(view.compareStartedDesc(newer, {}), -1);
  });

  test('2 empty or unreadable values give 0', () => {
    assert.equal(view.compareStartedDesc({ startedAt: '' }, { startedAt: 'bad' }), 0);
    assert.equal(view.compareStartedDesc(null, undefined), 0);
  });
});

describe('page logic exports', () => {
  test('view.js exports the feed, count, title, and elapsed helpers', () => {
    ['feedOrder', 'headerCounts', 'pageTitle', 'formatElapsed', 'compareStartedDesc'].forEach((name) => {
      assert.equal(typeof view[name], 'function', name);
    });
  });

  test('view.js exports the request builders and the detail lookup', () => {
    ['archiveRequest', 'clearRequest', 'learningUrl', 'detailKey', 'detailItem'].forEach((name) => {
      assert.equal(typeof view[name], 'function', name);
    });
  });

  test('view.js exports the card layout, severity order, and fix count helpers', () => {
    ['severityOrder', 'fixCounts', 'columnCount', 'balanceColumns', 'isWideSection', 'sectionMeta'].forEach((name) => {
      assert.equal(typeof view[name], 'function', name);
    });
  });
});

describe('severityOrder', () => {
  test('puts the highest severity first, and an unknown severity after info', () => {
    assert.deepEqual(
      view.severityOrder([{ severity: 'low' }, { severity: 'critical' }, { severity: 'x' }, { severity: 'low' }]),
      [1, 0, 3, 2]
    );
    assert.deepEqual(
      view.severityOrder([{ severity: 'x' }, { severity: 'info' }, { severity: 'medium' }, { severity: 'high' }]),
      [3, 2, 1, 0]
    );
  });

  test('items with the same severity keep their list order', () => {
    assert.deepEqual(view.severityOrder([{ severity: 'high' }, { severity: 'high' }, { severity: 'high' }]), [0, 1, 2]);
  });

  test('an empty or missing list gives an empty order', () => {
    assert.deepEqual(view.severityOrder([]), []);
    assert.deepEqual(view.severityOrder(undefined), []);
    assert.deepEqual(view.severityOrder(null), []);
  });

  test('a null item or a missing severity ranks as unknown', () => {
    assert.deepEqual(view.severityOrder([null, {}, { severity: 'info' }]), [2, 0, 1]);
  });
});

describe('fixCounts', () => {
  test('counts each status and the total', () => {
    assert.deepEqual(view.fixCounts([{ status: 'fixed' }, { status: 'fixing' }, { status: 'queued' }]), {
      total: 3, fixed: 1, fixing: 1, queued: 1, failed: 0, deferred: 0,
    });
    assert.deepEqual(view.fixCounts([{ status: 'failed' }, { status: 'deferred' }, { status: 'fixed' }, { status: 'fixed' }]), {
      total: 4, fixed: 2, fixing: 0, queued: 0, failed: 1, deferred: 1,
    });
  });

  test('an unknown status counts in the total only', () => {
    assert.deepEqual(view.fixCounts([{ status: 'weird' }, { status: 'fixed' }]), {
      total: 2, fixed: 1, fixing: 0, queued: 0, failed: 0, deferred: 0,
    });
  });

  test('a null item is skipped and does not count', () => {
    assert.deepEqual(view.fixCounts([null, { status: 'fixed' }, undefined]), {
      total: 1, fixed: 1, fixing: 0, queued: 0, failed: 0, deferred: 0,
    });
  });

  test('an empty or missing list gives all zeros', () => {
    const zeros = { total: 0, fixed: 0, fixing: 0, queued: 0, failed: 0, deferred: 0 };
    assert.deepEqual(view.fixCounts([]), zeros);
    assert.deepEqual(view.fixCounts(undefined), zeros);
  });
});

describe('columnCount', () => {
  test('a zero, negative, or missing width gives 1 column', () => {
    assert.equal(view.columnCount(0, 360, 12), 1);
    assert.equal(view.columnCount(-5, 360, 12), 1);
    assert.equal(view.columnCount(undefined, 360, 12), 1);
    assert.equal(view.columnCount(NaN, 360, 12), 1);
  });

  test('gives the number of columns of at least the minimum width that fit', () => {
    assert.equal(view.columnCount(1104, 360, 12), 3);
    assert.equal(view.columnCount(732, 360, 12), 2);
    assert.equal(view.columnCount(731, 360, 12), 1);
    assert.equal(view.columnCount(200, 360, 12), 1);
  });
});

describe('balanceColumns', () => {
  test('puts the tallest card first and each card in the shortest column', () => {
    assert.deepEqual(view.balanceColumns([100, 300, 200], 2), [[1], [0, 2]]);
  });

  test('cards keep their source order inside a column', () => {
    assert.deepEqual(view.balanceColumns([50, 50, 50, 50], 2), [[0, 2], [1, 3]]);
    assert.deepEqual(view.balanceColumns([10, 400, 10, 10, 10], 2), [[1], [0, 2, 3, 4]]);
  });

  test('one column holds every card in source order', () => {
    assert.deepEqual(view.balanceColumns([30, 10, 20], 1), [[0, 1, 2]]);
  });

  test('more columns than cards leaves the extra columns empty', () => {
    assert.deepEqual(view.balanceColumns([10, 20], 3), [[1], [0], []]);
  });

  test('no cards gives empty columns', () => {
    assert.deepEqual(view.balanceColumns([], 2), [[], []]);
  });
});

describe('fixes tile', () => {
  test('isWideSection is true for a fixes detail', () => {
    assert.equal(view.isWideSection({ kind: 'fixes' }), true);
  });

  test('sectionMeta counts the fixed fixes, and names the fixing ones', () => {
    const detail = { kind: 'fixes', fixes: [{ status: 'fixed' }, { status: 'fixing' }] };
    assert.equal(view.sectionMeta({ name: 'received-review', detail }), '1/2 fixed · 1 fixing');
  });

  test('sectionMeta omits the fixing part when no fix is running', () => {
    const detail = { kind: 'fixes', fixes: [{ status: 'fixed' }, { status: 'queued' }, { status: 'deferred' }] };
    assert.equal(view.sectionMeta({ name: 'received-review', detail }), '1/3 fixed');
  });

  test('sectionMeta of a fixes detail without fixes is 0/0 fixed', () => {
    assert.equal(view.sectionMeta({ name: 'received-review', detail: { kind: 'fixes' } }), '0/0 fixed');
  });
});

describe('defaultCollapsed', () => {
  test('COLLAPSE_FINISHED is true', () => {
    assert.equal(view.COLLAPSE_FINISHED, true);
  });

  test('a completed pipeline starts collapsed', () => {
    assert.equal(view.defaultCollapsed({ status: 'completed' }), true);
  });

  test('any other pipeline starts open', () => {
    for (const status of ['running', 'stalled', 'failed']) {
      assert.equal(view.defaultCollapsed({ status }), false);
    }
  });
});

describe('hasSection / sectionKind', () => {
  test('hasSection is true only for a step with a detail', () => {
    assert.equal(view.hasSection({ detail: { kind: 'waves' } }), true);
    assert.equal(view.hasSection({}), false);
    assert.equal(view.hasSection(null), false);
  });

  test('sectionKind returns the detail kind, or empty with no detail', () => {
    assert.equal(view.sectionKind({ detail: { kind: 'rounds' } }), 'rounds');
    assert.equal(view.sectionKind({}), '');
  });
});

describe('sectionMeta', () => {
  const task = (id, status) => ({ id, name: '', status });

  test('2 or more waves: wave count and task count', () => {
    const waves = [1, 2, 3, 4].map((n) => ({
      number: n,
      tasks: [task(n + 'a', 'completed'), task(n + 'b', 'completed'), task(n + 'c', 'completed')],
    }));
    assert.equal(
      view.sectionMeta({ detail: { kind: 'waves', waves } }),
      '4 waves · 12/12 tasks done'
    );
  });

  test('one wave: task count only', () => {
    const waves = [{ number: 1, tasks: [task('1', 'completed'), task('2', 'completed')] }];
    assert.equal(view.sectionMeta({ detail: { kind: 'waves', waves } }), '2/2 tasks done');
  });

  test('queued only: queued count', () => {
    const queued = [task('1', 'pending'), task('2', 'pending'), task('3', 'pending')];
    assert.equal(view.sectionMeta({ detail: { kind: 'waves', queued } }), '3 queued');
  });

  test('waves plus queued: queued tasks count in the total', () => {
    const waves = [{ number: 1, tasks: [task('1', 'completed')] }];
    const queued = [task('2', 'pending'), task('3', 'pending')];
    assert.equal(view.sectionMeta({ detail: { kind: 'waves', waves, queued } }), '1/3 tasks done');
  });

  test('a planned wave that has not started reads 0 of its tasks, with no queued part', () => {
    const wave1 = [{ number: 1, status: 'pending', tasks: [task('1', 'pending'), task('2', 'pending')] }];
    const wave2 = [{ number: 2, status: 'pending', tasks: [task('3', 'pending')] }];
    assert.equal(view.sectionMeta({ detail: { kind: 'waves', waves: wave1 } }), '0/2 tasks done');
    assert.equal(view.sectionMeta({ detail: { kind: 'waves', waves: wave2, queued: [] } }), '0/1 tasks done');
  });

  test('dimensions: done count, plus findings when reviewTotals exists', () => {
    const dimensions = [
      { name: 'a', status: 'completed' },
      { name: 'b', status: 'completed' },
      { name: 'c', status: 'completed' },
      { name: 'd', status: 'in_progress' },
      { name: 'e', status: 'pending' },
    ];
    assert.equal(
      view.sectionMeta({ detail: { kind: 'dimensions', dimensions } }),
      '3/5 dimensions done'
    );
    const reviewTotals = { found: 9, fixed: 6, deferred: 3, unaccounted: 0 };
    assert.equal(
      view.sectionMeta({ detail: { kind: 'dimensions', dimensions, reviewTotals } }),
      '3/5 dimensions done · 9 findings'
    );
  });

  test('dimensions: the planned count of the review plan is the total, not the listed rows', () => {
    const dimensions = [
      { name: 'a', status: 'completed' },
      { name: 'b', status: 'skipped', reason: 'stalled' },
    ];
    const reviewPlan = { wavesPlanned: 3, wavesRun: 1, dimensionsPlanned: 23, dimensionsRun: 2, neverStarted: 21 };
    assert.equal(
      view.sectionMeta({ detail: { kind: 'dimensions', dimensions, reviewPlan } }),
      '1/23 dimensions done'
    );
    const reviewTotals = { found: 1, fixed: 0, deferred: 0, unaccounted: 1 };
    assert.equal(
      view.sectionMeta({ detail: { kind: 'dimensions', dimensions, reviewPlan, reviewTotals } }),
      '1/23 dimensions done · 1 finding'
    );
  });

  test('dimensions: a review plan with 0 planned dimensions falls back to the listed rows', () => {
    const dimensions = [{ name: 'a', status: 'completed' }, { name: 'b', status: 'pending' }];
    const reviewPlan = { wavesPlanned: 0, wavesRun: 0, dimensionsPlanned: 0, dimensionsRun: 0, neverStarted: 0 };
    assert.equal(
      view.sectionMeta({ detail: { kind: 'dimensions', dimensions, reviewPlan } }),
      '1/2 dimensions done'
    );
  });

  test('explorers: area count and the sum of totals', () => {
    const explorers = [20, 15, 14, 12, 10].map((total, i) => ({ name: 'e' + i, total }));
    assert.equal(
      view.sectionMeta({ detail: { kind: 'explorers', explorers } }),
      '5 areas · 71 findings'
    );
  });

  test('rounds: row count of maxRounds', () => {
    const rounds = [1, 2, 3, 4, 5].map((n) => ({ n }));
    assert.equal(
      view.sectionMeta({ detail: { kind: 'rounds', rounds, maxRounds: 5 } }),
      '5 of 5 rounds'
    );
  });

  test('findings: 1 finding, N findings, or no findings', () => {
    const f = { text: 'x', severity: 'low' };
    assert.equal(view.sectionMeta({ detail: { kind: 'findings', findings: [f] } }), '1 finding');
    assert.equal(
      view.sectionMeta({ detail: { kind: 'findings', findings: [f, f, f] } }),
      '3 findings'
    );
    assert.equal(view.sectionMeta({ detail: { kind: 'findings' } }), 'no findings');
  });

  test('guardrails: the count, singular for 1, 0 with no counts', () => {
    assert.equal(view.sectionMeta({ detail: { kind: 'guardrails', guardrails: { total: 3, error: 2, warning: 1 } } }), '3 guardrails');
    assert.equal(view.sectionMeta({ detail: { kind: 'guardrails', guardrails: { total: 1, error: 1, warning: 0 } } }), '1 guardrail');
    assert.equal(view.sectionMeta({ detail: { kind: 'guardrails' } }), '0 guardrails');
  });

  test('result: the text before the first colon, or the whole text, or empty', () => {
    assert.equal(
      view.sectionMeta({ detail: { kind: 'result', result: 'nothing to commit: execute committed 2 wave commit(s)' } }),
      'nothing to commit'
    );
    assert.equal(view.sectionMeta({ detail: { kind: 'result', result: 'nothing to commit' } }), 'nothing to commit');
    assert.equal(view.sectionMeta({ detail: { kind: 'result' } }), '');
  });

  test('returns an empty string with no detail or an unknown kind', () => {
    assert.equal(view.sectionMeta({}), '');
    assert.equal(view.sectionMeta({ detail: { kind: 'other' } }), '');
  });
});

describe('isWideSection', () => {
  test('every waves tile is wide, with 0, 1, or N waves', () => {
    assert.equal(view.isWideSection({ kind: 'waves' }), true);
    assert.equal(view.isWideSection({ kind: 'waves', waves: [] }), true);
    assert.equal(view.isWideSection({ kind: 'waves', waves: [{}] }), true);
    assert.equal(view.isWideSection({ kind: 'waves', waves: [{}, {}] }), true);
    assert.equal(view.isWideSection({ kind: 'waves', waves: [{}, {}, {}] }), true);
  });

  test('explorers, rounds, and dimensions are wide', () => {
    assert.equal(view.isWideSection({ kind: 'explorers' }), true);
    assert.equal(view.isWideSection({ kind: 'rounds' }), true);
    assert.equal(view.isWideSection({ kind: 'dimensions' }), true);
  });

  test('a missing detail and findings are not wide', () => {
    assert.equal(view.isWideSection(null), false);
    assert.equal(view.isWideSection(undefined), false);
    assert.equal(view.isWideSection({ kind: 'findings' }), false);
    assert.equal(view.isWideSection({ kind: 'guardrails' }), false);
    assert.equal(view.isWideSection({ kind: 'result' }), false);
  });
});

describe('defaultStationIndex', () => {
  test('picks the first in_progress step', () => {
    const steps = [{ status: 'failed' }, { status: 'in_progress' }, { status: 'in_progress' }];
    assert.equal(view.defaultStationIndex(steps), 1);
  });

  test('else the first failed step', () => {
    const steps = [{ status: 'completed', detail: { kind: 'waves' } }, { status: 'failed' }];
    assert.equal(view.defaultStationIndex(steps), 1);
  });

  test('else the first step with a section', () => {
    const steps = [{ status: 'completed' }, { status: 'completed', detail: { kind: 'waves' } }];
    assert.equal(view.defaultStationIndex(steps), 1);
  });

  test('else 0', () => {
    assert.equal(view.defaultStationIndex([{ status: 'pending' }, { status: 'pending' }]), 0);
    assert.equal(view.defaultStationIndex([]), 0);
  });
});

describe('tileCount', () => {
  const steps = [{ detail: { kind: 'waves' } }, {}, { detail: { kind: 'dimensions' } }];

  test('counts steps with a section', () => {
    assert.equal(view.tileCount({ steps, issues: [] }, null), 2);
  });

  test('adds 1 with issues', () => {
    assert.equal(view.tileCount({ steps, issues: [{ text: 'x' }] }, null), 3);
  });

  test('adds 1 with a session', () => {
    assert.equal(view.tileCount({ steps, issues: [{ text: 'x' }] }, { id: 's' }), 4);
  });
});

describe('waveCommitState', () => {
  const done = { status: 'completed' };
  const open = { status: 'in_progress' };

  test('commitWaves false gives off', () => {
    assert.equal(view.waveCommitState({ committedSha: 'abc', tasks: [done] }, false), 'off');
  });

  test('a sha gives committed', () => {
    assert.equal(view.waveCommitState({ committedSha: 'abc', tasks: [done] }, true), 'committed');
  });

  test('no sha with all tasks done gives due', () => {
    assert.equal(view.waveCommitState({ committedSha: '', tasks: [done, done] }, true), 'due');
  });

  test('no sha with open tasks gives not-committed', () => {
    assert.equal(view.waveCommitState({ committedSha: '', tasks: [done, open] }, true), 'not-committed');
  });

  test('a wave with no tasks gives not-committed', () => {
    assert.equal(view.waveCommitState({ committedSha: '', tasks: [] }, true), 'not-committed');
  });

  test('an absent commitWaves behaves as on', () => {
    assert.equal(view.waveCommitState({ committedSha: '', tasks: [done] }, undefined), 'due');
  });

  test('a pending wave has no commit state, whatever commitWaves says', () => {
    const wave = { status: 'pending', committedSha: '', tasks: [open] };
    assert.equal(view.waveCommitState(wave, true), '');
    assert.equal(view.waveCommitState(wave, false), '');
    assert.equal(view.waveCommitState(wave, undefined), '');
  });

  test('a wave that is not pending keeps its commit state', () => {
    assert.equal(view.waveCommitState({ status: 'in_progress', committedSha: '', tasks: [open] }, true), 'not-committed');
    assert.equal(view.waveCommitState({ status: 'completed', committedSha: '', tasks: [done] }, false), 'off');
  });
});

describe('taskCounts', () => {
  test('counts completed tasks of the total', () => {
    const tasks = [{ status: 'completed' }, { status: 'completed' }, { status: 'pending' }];
    assert.deepEqual(view.taskCounts(tasks), { done: 2, total: 3 });
  });

  test('returns zeros for a missing list', () => {
    assert.deepEqual(view.taskCounts(undefined), { done: 0, total: 0 });
  });
});

describe('reviewTotalsText', () => {
  test('lists found, fixed, deferred, and unaccounted', () => {
    assert.equal(
      view.reviewTotalsText({ found: 9, fixed: 6, deferred: 3, unaccounted: 0 }),
      '9 findings · 6 fixed · 3 deferred · 0 unaccounted'
    );
  });
});

describe('reviewPlanText', () => {
  test('lists waves run, dimensions run, and never started', () => {
    assert.equal(
      view.reviewPlanText({ wavesPlanned: 3, wavesRun: 1, dimensionsPlanned: 23, dimensionsRun: 8, neverStarted: 15 }),
      'waves 1/3 run · dimensions 8/23 run · 15 never started'
    );
  });

  test('is empty without a plan', () => {
    assert.equal(view.reviewPlanText(undefined), '');
  });
});

describe('explorerMore', () => {
  test('returns the findings not shown', () => {
    assert.equal(view.explorerMore({ total: 12 }, 2), 10);
  });

  test('never goes below 0', () => {
    assert.equal(view.explorerMore({ total: 1 }, 2), 0);
  });
});

describe('kindLabel', () => {
  test('shortens execute to exec', () => {
    assert.equal(view.kindLabel('execute'), 'exec');
  });

  test('keeps ship, plan, and review unchanged', () => {
    for (const kind of ['ship', 'plan', 'review']) {
      assert.equal(view.kindLabel(kind), kind);
    }
  });
});

describe('stationLabel', () => {
  const LABELS = {
    execute: 'exec',
    commit: 'commit',
    review: 'review',
    harden: 'harden',
    'received-review': 'rcv-rev',
    'commit-fixes': 'fixes',
    'verify-openspec': 'spec-chk',
    'archive-openspec': 'archive',
    pr: 'pr',
    'verify-pipeline': 'ci',
    'await-remote-review': 'remote',
    'learnings-commit': 'learn',
  };

  test('the label table covers exactly the steps[].name enum of ship-state.schema.json', () => {
    assert.deepEqual(Object.keys(LABELS).sort(), readStepNameEnum().slice().sort());
  });

  test('maps each ship step name to its station label', () => {
    for (const name of readStepNameEnum()) {
      assert.equal(view.stationLabel(name), LABELS[name], name);
    }
  });

  test('returns an unknown name unchanged', () => {
    assert.equal(view.stationLabel('queued'), 'queued');
    assert.equal(view.stationLabel('wave 2'), 'wave 2');
  });
});

describe('repoChips', () => {
  test('returns one chip for each repo, in snapshot order', () => {
    const repos = [
      { root: '/b', name: 'b', pipelines: [{ status: 'running' }, { status: 'failed' }] },
      { root: '/a', name: 'a', pipelines: [{ status: 'completed' }] },
      { root: '/c', name: 'c' },
    ];
    assert.deepEqual(view.repoChips(repos), [
      { root: '/b', name: 'b', count: 2, hasFail: true },
      { root: '/a', name: 'a', count: 1, hasFail: false },
      { root: '/c', name: 'c', count: 0, hasFail: false },
    ]);
  });
});

describe('inScope', () => {
  test('an empty or missing scope holds every root', () => {
    assert.equal(view.inScope(new Set(), '/a'), true);
    assert.equal(view.inScope(undefined, '/a'), true);
  });

  test('a scope holds only its roots', () => {
    const scope = new Set(['/a']);
    assert.equal(view.inScope(scope, '/a'), true);
    assert.equal(view.inScope(scope, '/b'), false);
  });
});

describe('scopedCounts / headerCounts', () => {
  const repos = [
    {
      root: '/a',
      pipelines: [{ status: 'running' }, { status: 'failed' }],
      learnings: [{}, {}],
      deferred: [{}],
      history: [{}],
    },
    {
      root: '/b',
      pipelines: [{ status: 'stalled' }, { status: 'running' }, { status: 'completed' }],
      learnings: [],
      deferred: [{}, {}],
      history: [{}, {}, {}],
    },
  ];

  test('scopedCounts counts every repo with an empty scope', () => {
    assert.deepEqual(view.scopedCounts(repos, new Set()), { pipelines: 5, activity: 5, history: 4 });
  });

  test('scopedCounts counts only the repos in scope; activity is learnings plus deferred', () => {
    assert.deepEqual(view.scopedCounts(repos, new Set(['/a'])), {
      pipelines: 2,
      activity: 3,
      history: 1,
    });
  });

  test('headerCounts counts running, stalled, and failed over all repos', () => {
    assert.deepEqual(view.headerCounts(repos), { running: 2, stalled: 1, failed: 1, waiting: 0 });
  });

  test('headerCounts waiting counts pipelines with attention over all repos', () => {
    const attention = { kind: 'question', askedAt: '2026-10-08T10:00:00Z', header: 'Q', text: 't' };
    const waitingRepos = [
      { root: '/a', pipelines: [{ status: 'running', attention }, { status: 'running' }] },
      { root: '/b', pipelines: [{ status: 'running', attention }, { status: 'failed' }, null] },
    ];
    assert.deepEqual(view.headerCounts(waitingRepos), { running: 3, stalled: 0, failed: 1, waiting: 2 });
  });

  test('headerCounts ignores a status that is not running, stalled, or failed', () => {
    const odd = [{ root: '/a', pipelines: [{ status: 'waiting' }, { status: 'completed' }, { status: 'constructor' }] }];
    assert.deepEqual(view.headerCounts(odd), { running: 0, stalled: 0, failed: 0, waiting: 0 });
  });

  test('headerCounts returns zeros for a missing list', () => {
    assert.deepEqual(view.headerCounts(undefined), { running: 0, stalled: 0, failed: 0, waiting: 0 });
  });
});

describe('pageTitle', () => {
  const attention = { kind: 'permission', askedAt: '2026-10-08T10:00:00Z', header: 'Permission', text: 't' };
  const repoA = { root: '/a', pipelines: [{ status: 'running', attention }, { status: 'running' }] };
  const repoB = { root: '/b', pipelines: [{ status: 'running', attention }, null] };

  test('puts the count of waiting runs before the base title', () => {
    assert.equal(view.pageTitle('SDLC dashboard', [repoA, repoB]), '(2) SDLC dashboard');
  });

  test('counts only the repos it is given', () => {
    assert.equal(view.pageTitle('SDLC dashboard', [repoA]), '(1) SDLC dashboard');
  });

  test('returns the base title when no run waits', () => {
    assert.equal(view.pageTitle('SDLC dashboard', [{ root: '/c', pipelines: [{ status: 'running' }] }]), 'SDLC dashboard');
  });

  test('returns the base title for a missing repo list or a repo without pipelines', () => {
    assert.equal(view.pageTitle('SDLC dashboard', undefined), 'SDLC dashboard');
    assert.equal(view.pageTitle('SDLC dashboard', [{ root: '/d' }]), 'SDLC dashboard');
  });
});

describe('toggleAllLabel', () => {
  test('returns the collapse text when any detail is open', () => {
    assert.equal(view.toggleAllLabel(true), 'Collapse all details');
  });

  test('returns the expand text when no detail is open', () => {
    assert.equal(view.toggleAllLabel(false), 'Expand all details');
  });
});

describe('severityTone', () => {
  test('critical and high are red', () => {
    assert.equal(view.severityTone('critical'), 'signal-red');
    assert.equal(view.severityTone('high'), 'signal-red');
  });

  test('medium is amber', () => {
    assert.equal(view.severityTone('medium'), 'signal-amber');
  });

  test('low and info are rail', () => {
    assert.equal(view.severityTone('low'), 'rail');
    assert.equal(view.severityTone('info'), 'rail');
  });
});

describe('issueLocation', () => {
  test('file and line', () => {
    assert.equal(view.issueLocation({ file: 'a.go', line: '12-14' }), 'a.go:12-14');
  });

  test('file only', () => {
    assert.equal(view.issueLocation({ file: 'a.go', line: '' }), 'a.go');
  });

  test('ref when there is no file', () => {
    assert.equal(view.issueLocation({ file: '', ref: 'security' }), 'security');
  });

  test('empty when there is no file and no ref', () => {
    assert.equal(view.issueLocation({}), '');
  });
});

describe('outcomeGlyph', () => {
  test('maps each history outcome to a glyph', () => {
    assert.equal(view.outcomeGlyph('success'), '✓');
    assert.equal(view.outcomeGlyph('failure'), '✕');
    assert.equal(view.outcomeGlyph('partial'), '◐');
    assert.equal(view.outcomeGlyph('other'), '·');
  });
});

describe('formatDuration', () => {
  test('seconds under 1 minute', () => {
    assert.equal(view.formatDuration(26000), '26s');
  });

  test('minutes under 1 hour', () => {
    assert.equal(view.formatDuration(720000), '12m');
  });

  test('hours and zero-padded minutes', () => {
    assert.equal(view.formatDuration(3720000), '1h 02m');
  });

  test('empty for a bad value', () => {
    assert.equal(view.formatDuration(-1), '');
    assert.equal(view.formatDuration(undefined), '');
  });
});

describe('formatElapsed', () => {
  test('seconds under 1 minute', () => {
    assert.equal(view.formatElapsed(0), '0s');
    assert.equal(view.formatElapsed(26000), '26s');
  });

  test('minutes and seconds under 1 hour', () => {
    assert.equal(view.formatElapsed(60000), '1m 0s');
    assert.equal(view.formatElapsed(252000), '4m 12s');
    assert.equal(view.formatElapsed(3599999), '59m 59s');
  });

  test('1 hour or more uses the formatDuration text', () => {
    assert.equal(view.formatElapsed(3600000), '1h 00m');
    assert.equal(view.formatElapsed(3780000), '1h 03m');
    assert.equal(view.formatElapsed(3780000), view.formatDuration(3780000));
  });

  test('empty for a bad value', () => {
    assert.equal(view.formatElapsed(-1), '');
    assert.equal(view.formatElapsed(NaN), '');
    assert.equal(view.formatElapsed(Infinity), '');
    assert.equal(view.formatElapsed(undefined), '');
    assert.equal(view.formatElapsed('252000'), '');
  });
});

describe('relativeWhen / clockLabel', () => {
  const now = Date.parse('2026-10-07T14:02:09Z');

  test('under 60 s is just now', () => {
    assert.equal(view.relativeWhen('2026-10-07T14:01:30Z', now, 'UTC'), 'just now');
  });

  test('a time in the future is just now', () => {
    assert.equal(view.relativeWhen('2026-10-07T15:00:00Z', now, 'UTC'), 'just now');
  });

  test('under 60 min is minutes ago', () => {
    assert.equal(view.relativeWhen('2026-10-07T13:50:00Z', now, 'UTC'), '12m ago');
  });

  test('under 24 h is hours ago', () => {
    assert.equal(view.relativeWhen('2026-10-07T11:00:00Z', new Date(now), 'UTC'), '3h ago');
  });

  test('24 h or more is the date and clock in the time zone', () => {
    assert.equal(view.relativeWhen('2026-10-06T14:02:00Z', now, 'UTC'), 'Oct 6 14:02');
  });

  test('empty for a bad timestamp', () => {
    assert.equal(view.relativeWhen('not a date', now, 'UTC'), '');
    assert.equal(view.clockLabel('', 'UTC'), '');
  });

  test('clockLabel returns HH:MM in the time zone', () => {
    assert.equal(view.clockLabel('2026-10-07T14:02:09Z', 'UTC'), '14:02');
    assert.equal(view.clockLabel('2026-10-07T00:05:00Z', 'UTC'), '00:05');
  });
});

describe('shortId', () => {
  test('cuts an id longer than 8 characters', () => {
    assert.equal(view.shortId('abcdef1234'), 'abcdef12…');
    assert.equal(view.shortId('abcdef123'), 'abcdef12…');
  });

  test('keeps an id of 8 characters or fewer', () => {
    assert.equal(view.shortId('abcdef12'), 'abcdef12');
    assert.equal(view.shortId('abc'), 'abc');
  });
});

describe('pickSession', () => {
  const sessions = [
    { id: 's1', branch: 'feat', lastSeen: '2026-10-07T10:00:00Z' },
    { id: 's2', branch: 'feat', lastSeen: '2026-10-07T12:00:00Z' },
    { id: 's3', branch: 'main', lastSeen: '2026-10-07T13:00:00Z' },
  ];

  test('returns the session whose id matches', () => {
    assert.equal(view.pickSession({ sessionId: 's1', branch: 'feat' }, sessions), sessions[0]);
  });

  test('else the same-branch session with the newest lastSeen', () => {
    assert.equal(view.pickSession({ sessionId: '', branch: 'feat' }, sessions), sessions[1]);
    assert.equal(view.pickSession({ sessionId: 'gone', branch: 'feat' }, sessions), sessions[1]);
  });

  test('else null', () => {
    assert.equal(view.pickSession({ sessionId: '', branch: 'other' }, sessions), null);
    assert.equal(view.pickSession({ sessionId: '', branch: 'feat' }, []), null);
  });
});

describe('nextTabIndex', () => {
  test('ArrowLeft moves back and wraps', () => {
    assert.equal(view.nextTabIndex(0, 'ArrowLeft', 3), 2);
    assert.equal(view.nextTabIndex(2, 'ArrowLeft', 3), 1);
  });

  test('ArrowRight moves on and wraps', () => {
    assert.equal(view.nextTabIndex(2, 'ArrowRight', 3), 0);
    assert.equal(view.nextTabIndex(0, 'ArrowRight', 3), 1);
  });

  test('Home and End go to the ends', () => {
    assert.equal(view.nextTabIndex(1, 'Home', 3), 0);
    assert.equal(view.nextTabIndex(1, 'End', 3), 2);
  });

  test('any other key keeps the index', () => {
    assert.equal(view.nextTabIndex(1, 'Enter', 3), 1);
  });
});

describe('pipelineKey / sectionKey', () => {
  test('pipelineKey joins the repo root and the pipeline id with a newline', () => {
    assert.equal(view.pipelineKey({ root: '/r' }, { id: 'ship-x' }), '/r\nship-x');
  });

  test('sectionKey appends the section name', () => {
    assert.equal(view.sectionKey('/r\nship-x', 'review'), '/r\nship-x/review');
    assert.equal(view.sectionKey('/r\nship-x', 'issues'), '/r\nship-x/issues');
    assert.equal(view.sectionKey('/r\nship-x', 'session'), '/r\nship-x/session');
  });
});

describe('parseHash', () => {
  const tabs = view.TAB_NAMES;

  test('TAB_NAMES lists the 3 tabs', () => {
    assert.deepEqual(view.TAB_NAMES, ['pipelines', 'activity', 'history']);
  });

  test('a tab name selects the tab', () => {
    assert.deepEqual(view.parseHash('#history', tabs), { tab: 'history' });
    assert.deepEqual(view.parseHash('#activity', tabs), { tab: 'activity' });
  });

  test('a pipeline id and a station number select both', () => {
    assert.deepEqual(view.parseHash('#ship-x/2', tabs), { pipelineId: 'ship-x', station: 2 });
  });

  test('a pipeline id alone selects no station', () => {
    assert.deepEqual(view.parseHash('#ship-x', tabs), { pipelineId: 'ship-x', station: null });
  });

  test('a part after the last slash that is not a number stays in the id', () => {
    assert.deepEqual(view.parseHash('#ship-x/abc', tabs), { pipelineId: 'ship-x/abc', station: null });
  });

  test('an empty hash gives null', () => {
    assert.equal(view.parseHash('', tabs), null);
    assert.equal(view.parseHash('#', tabs), null);
  });
});

function fakeStorage(initial) {
  const map = new Map(initial ? Object.entries(initial) : []);
  return {
    getItem(key) {
      return map.has(key) ? map.get(key) : null;
    },
    setItem(key, value) {
      map.set(key, value);
    },
    _map: map,
  };
}

const throwingStorage = {
  getItem() {
    throw new Error('blocked');
  },
  setItem() {
    throw new Error('quota exceeded');
  },
};

describe('loadTab / saveTab', () => {
  test('TAB_KEY is sdlc-dashboard-tab', () => {
    assert.equal(view.TAB_KEY, 'sdlc-dashboard-tab');
  });

  test('loadTab returns pipelines when storage holds no value', () => {
    assert.equal(view.loadTab(fakeStorage()), 'pipelines');
  });

  test('loadTab returns pipelines for an unknown value', () => {
    assert.equal(view.loadTab(fakeStorage({ 'sdlc-dashboard-tab': 'bogus' })), 'pipelines');
  });

  test('loadTab returns pipelines when storage throws', () => {
    assert.equal(view.loadTab(throwingStorage), 'pipelines');
  });

  test('saveTab then loadTab round-trips the tab', () => {
    const storage = fakeStorage();
    view.saveTab(storage, 'history');
    assert.equal(storage._map.get('sdlc-dashboard-tab'), 'history');
    assert.equal(view.loadTab(storage), 'history');
  });

  test('saveTab does not throw when storage throws', () => {
    assert.doesNotThrow(() => view.saveTab(throwingStorage, 'history'));
  });
});

describe('loadRepoFilter / saveRepoFilter', () => {
  test('REPO_FILTER_KEY is sdlc-dashboard-repo-filter', () => {
    assert.equal(view.REPO_FILTER_KEY, 'sdlc-dashboard-repo-filter');
  });

  test('loadRepoFilter returns an empty set when storage holds no value', () => {
    assert.deepEqual(view.loadRepoFilter(fakeStorage()), new Set());
  });

  test('loadRepoFilter returns an empty set for bad JSON', () => {
    const storage = fakeStorage({ 'sdlc-dashboard-repo-filter': 'not json' });
    assert.deepEqual(view.loadRepoFilter(storage), new Set());
  });

  test('loadRepoFilter returns an empty set for JSON that is not an array', () => {
    const storage = fakeStorage({ 'sdlc-dashboard-repo-filter': '{"a":1}' });
    assert.deepEqual(view.loadRepoFilter(storage), new Set());
  });

  test('loadRepoFilter returns an empty set when storage throws', () => {
    assert.deepEqual(view.loadRepoFilter(throwingStorage), new Set());
  });

  test('saveRepoFilter then loadRepoFilter round-trips the set', () => {
    const storage = fakeStorage();
    view.saveRepoFilter(storage, new Set(['/repo-a', '/repo-b']));
    assert.equal(storage._map.get('sdlc-dashboard-repo-filter'), '["/repo-a","/repo-b"]');
    assert.deepEqual(view.loadRepoFilter(storage), new Set(['/repo-a', '/repo-b']));
  });

  test('saveRepoFilter does not throw when storage throws', () => {
    assert.doesNotThrow(() => view.saveRepoFilter(throwingStorage, new Set(['/repo-a'])));
  });
});

describe('cutText', () => {
  test('returns text unchanged when at or under the limit', () => {
    assert.equal(view.cutText('short text'), 'short text');
    const exactly120 = 'x'.repeat(120);
    assert.equal(view.cutText(exactly120), exactly120);
  });

  test('cuts to 120 runes plus an ellipsis for longer text', () => {
    const longText = 'x'.repeat(130);
    const result = view.cutText(longText);
    assert.equal(Array.from(result).length, 121);
    assert.ok(result.endsWith('…'));
    assert.equal(result.slice(0, 120), 'x'.repeat(120));
  });

  test('counts Unicode code points, not UTF-16 code units', () => {
    // U+1F600 is a surrogate pair in UTF-16 but a single code point.
    const longText = '\u{1F600}'.repeat(125);
    const result = view.cutText(longText);
    assert.equal(Array.from(result).length, 121);
    assert.ok(result.endsWith('…'));
  });

  test('accepts a custom max', () => {
    assert.equal(view.cutText('abcdef', 3), 'abc…');
    assert.equal(view.cutText('abc', 3), 'abc');
  });

  test('returns an empty string for empty or missing text', () => {
    assert.equal(view.cutText(''), '');
    assert.equal(view.cutText(null), '');
    assert.equal(view.cutText(undefined), '');
  });
});

describe('emptyText', () => {
  const EXPECTED = 'Nothing is running. Start /sdlc:ship in any repo and it shows here.';

  test('returns the empty-page text when no repo has a pipeline', () => {
    assert.equal(view.emptyText({ repos: [] }), EXPECTED);
    assert.equal(view.emptyText({ repos: [{ pipelines: [] }, { pipelines: [] }] }), EXPECTED);
  });

  test('returns the empty-page text for a missing or malformed snapshot', () => {
    assert.equal(view.emptyText(null), EXPECTED);
    assert.equal(view.emptyText(undefined), EXPECTED);
    assert.equal(view.emptyText({}), EXPECTED);
  });

  test('returns an empty string when any repo has a pipeline', () => {
    const snapshot = { repos: [{ pipelines: [] }, { pipelines: [{ status: 'running' }] }] };
    assert.equal(view.emptyText(snapshot), '');
  });
});

describe('connectionText', () => {
  test('returns Reconnecting for error', () => {
    assert.equal(view.connectionText('error'), 'Reconnecting…');
  });

  test('returns an empty string for open and snapshot', () => {
    assert.equal(view.connectionText('open'), '');
    assert.equal(view.connectionText('snapshot'), '');
  });

  test('returns the stop outcome text for stopped and stop-failed', () => {
    assert.equal(
      view.connectionText('stopped'),
      'Server stopped. Run /sdlc:dashboard to start it again.'
    );
    assert.equal(
      view.connectionText('stop-failed'),
      'The server did not stop. Run /sdlc:dashboard --stop.'
    );
  });

  test('returns an empty string for an unknown event', () => {
    assert.equal(view.connectionText('bogus'), '');
  });
});

describe('stopRequest', () => {
  test('returns null for an empty token', () => {
    assert.equal(view.stopRequest(''), null);
  });

  test('returns the fetch url and init for a non-empty token', () => {
    assert.deepEqual(view.stopRequest('abc123'), {
      url: '/api/stop',
      init: {
        method: 'POST',
        headers: { 'X-Sdlc-Token': 'abc123' },
      },
    });
  });
});

describe('stopResult', () => {
  test('returns stopped for 202', () => {
    assert.equal(view.stopResult(202), 'stopped');
  });

  test('returns stop-failed for any other status, including a network error (0)', () => {
    assert.equal(view.stopResult(403), 'stop-failed');
    assert.equal(view.stopResult(500), 'stop-failed');
    assert.equal(view.stopResult(0), 'stop-failed');
  });
});

describe('archiveRequest / clearRequest', () => {
  test('archiveRequest returns null when the token, repo, or run id is empty', () => {
    assert.equal(view.archiveRequest('', '/abs/repo', 'ship-x-1', false), null);
    assert.equal(view.archiveRequest('abc123', '', 'ship-x-1', false), null);
    assert.equal(view.archiveRequest('abc123', '/abs/repo', '', false), null);
  });

  test('archiveRequest returns a JSON POST with the token header', () => {
    const req = view.archiveRequest('abc123', '/abs/repo', 'ship-x-1', false);
    assert.equal(req.method, 'POST');
    assert.equal(req.url, '/api/run-archive');
    assert.deepEqual(req.headers, { 'X-Sdlc-Token': 'abc123', 'Content-Type': 'application/json' });
    assert.equal(typeof req.body, 'string');
    assert.deepEqual(JSON.parse(req.body), { repo: '/abs/repo', runId: 'ship-x-1', confirmStalled: false });
  });

  test('archiveRequest always sends confirmStalled as a boolean', () => {
    assert.equal(JSON.parse(view.archiveRequest('t', '/r', 'id', true).body).confirmStalled, true);
    assert.equal(JSON.parse(view.archiveRequest('t', '/r', 'id', false).body).confirmStalled, false);
    assert.equal(JSON.parse(view.archiveRequest('t', '/r', 'id').body).confirmStalled, false);
    assert.equal(JSON.parse(view.archiveRequest('t', '/r', 'id', 'yes').body).confirmStalled, false);
  });

  test('clearRequest returns null when the token or repo is empty', () => {
    assert.equal(view.clearRequest('', '/abs/repo'), null);
    assert.equal(view.clearRequest('abc123', ''), null);
  });

  test('clearRequest returns a JSON POST with the token header', () => {
    const req = view.clearRequest('abc123', '/abs/repo');
    assert.equal(req.method, 'POST');
    assert.equal(req.url, '/api/cache-clear');
    assert.deepEqual(req.headers, { 'X-Sdlc-Token': 'abc123', 'Content-Type': 'application/json' });
    assert.deepEqual(JSON.parse(req.body), { repo: '/abs/repo' });
  });
});

describe('learningUrl', () => {
  test('returns null when the repo, date, or heading is empty', () => {
    assert.equal(view.learningUrl('', '2026-10-08', 'plan: x'), null);
    assert.equal(view.learningUrl('/abs/repo', '', 'plan: x'), null);
    assert.equal(view.learningUrl('/abs/repo', '2026-10-08', ''), null);
  });

  test('encodes every value', () => {
    const url = view.learningUrl('/abs/repo', '2026-10-08', 'plan: x');
    assert.equal(url, '/api/learning?repo=%2Fabs%2Frepo&date=2026-10-08&heading=plan%3A%20x');
    const query = new URL(url, 'http://localhost').searchParams;
    assert.equal(query.get('repo'), '/abs/repo');
    assert.equal(query.get('date'), '2026-10-08');
    assert.equal(query.get('heading'), 'plan: x');
  });

  test('keeps characters that would split the query', () => {
    const url = view.learningUrl('/a b/repo', '2026-10-08', 'a&b=c#d');
    const query = new URL(url, 'http://localhost').searchParams;
    assert.equal(query.get('repo'), '/a b/repo');
    assert.equal(query.get('heading'), 'a&b=c#d');
  });
});

describe('page wiring helpers', () => {
  test('view.js exports the four wiring helpers and errorText', () => {
    ['closeFocusSelector', 'confirmSteps', 'archiveResultText', 'clearResultText', 'errorText'].forEach((name) => {
      assert.equal(typeof view[name], 'function', name);
    });
  });
});

describe('errorText', () => {
  test('returns the message only, never the suggestion', () => {
    const body = { error: { code: 'X', message: 'It failed.', suggestion: 'Try again.' } };
    assert.equal(view.errorText(body, 'fallback'), 'It failed.');
  });

  test('returns the fallback when there is no message, even with a suggestion', () => {
    assert.equal(view.errorText({ error: { suggestion: 'Try again.' } }, 'fallback'), 'fallback');
    assert.equal(view.errorText({ error: { message: '' } }, 'fallback'), 'fallback');
    assert.equal(view.errorText(null, 'fallback'), 'fallback');
  });
});

describe('closeFocusSelector', () => {
  test('returns the Activity tab button when the key is empty', () => {
    assert.equal(view.closeFocusSelector(''), '#tab-activity');
    assert.equal(view.closeFocusSelector(), '#tab-activity');
    assert.equal(view.closeFocusSelector(null), '#tab-activity');
  });

  test('returns the attribute selector of the key', () => {
    assert.equal(view.closeFocusSelector('deferred:d-1'), '[data-detail="deferred:d-1"]');
    assert.equal(
      view.closeFocusSelector('learning:2026-10-08:plan: split the lanes'),
      '[data-detail="learning:2026-10-08:plan: split the lanes"]'
    );
  });

  test('escapes the double quote, the backslash, and line breaks of a key', () => {
    assert.equal(view.closeFocusSelector('learning:2026-10-08:say "hi"'), '[data-detail="learning:2026-10-08:say \\"hi\\""]');
    assert.equal(view.closeFocusSelector('deferred:a\\b'), '[data-detail="deferred:a\\\\b"]');
    assert.equal(view.closeFocusSelector('deferred:a\nb'), '[data-detail="deferred:a\\a b"]');
  });

  test('builds the selector of the key that detailKey builds for each kind', () => {
    const keys = [
      view.detailKey('deferred', { id: 'd-1' }),
      view.detailKey('issue', null, { pipeline: 'ship-1', index: 0 }),
      view.detailKey('learning', { date: '2026-10-08', heading: 'plan: x' }),
      view.detailKey('finding', null, { pipeline: 'ship-1', dimension: 'security', index: 2 }),
    ];
    keys.forEach((key) => {
      assert.equal(view.closeFocusSelector(key), '[data-detail="' + key + '"]');
    });
  });
});

describe('confirmSteps', () => {
  test('archive of a run that is not stalled asks once', () => {
    assert.deepEqual(view.confirmSteps('archive', 'completed'), ['archive']);
    assert.deepEqual(view.confirmSteps('archive', 'failed'), ['archive']);
    assert.deepEqual(view.confirmSteps('archive'), ['archive']);
  });

  test('archive of a stalled run asks a second time', () => {
    assert.deepEqual(view.confirmSteps('archive', 'stalled'), ['archive', 'stalled']);
  });

  test('clear asks once, whatever the status', () => {
    assert.deepEqual(view.confirmSteps('clear'), ['clear']);
    assert.deepEqual(view.confirmSteps('clear', 'stalled'), ['clear']);
  });

  test('an unknown action asks nothing', () => {
    assert.deepEqual(view.confirmSteps('stop'), []);
    assert.deepEqual(view.confirmSteps(''), []);
  });
});

describe('archiveResultText', () => {
  test('200 is ok with no text', () => {
    assert.deepEqual(view.archiveResultText(200, { runId: 'ship-1', dir: '/d', moved: [], deleted: [] }), {
      ok: true,
      text: '',
    });
  });

  test('409 shows the error message', () => {
    const body = { error: { code: 'RUN_ACTIVE', message: 'The run is still running.' } };
    assert.deepEqual(view.archiveResultText(409, body), { ok: false, text: 'The run is still running.' });
  });

  test('an error with a suggestion shows the message only', () => {
    const body = { error: { code: 'RUN_STALLED', message: 'The run stalled.', suggestion: 'Confirm to archive it.' } };
    assert.deepEqual(view.archiveResultText(409, body), {
      ok: false,
      text: 'The run stalled.',
    });
  });

  test('a response with no JSON body names the status', () => {
    assert.deepEqual(view.archiveResultText(500, null), { ok: false, text: 'Archive failed (HTTP 500).' });
  });

  test('a body with no message names the status', () => {
    assert.deepEqual(view.archiveResultText(500, {}), { ok: false, text: 'Archive failed (HTTP 500).' });
    assert.deepEqual(view.archiveResultText(500, { error: {} }), { ok: false, text: 'Archive failed (HTTP 500).' });
    assert.deepEqual(view.archiveResultText(500, { error: { message: '' } }), {
      ok: false,
      text: 'Archive failed (HTTP 500).',
    });
    assert.deepEqual(view.archiveResultText(500, 'boom'), { ok: false, text: 'Archive failed (HTTP 500).' });
  });

  test('a network error (status 0) is not ok', () => {
    assert.deepEqual(view.archiveResultText(0, null), { ok: false, text: 'Archive failed (HTTP 0).' });
  });
});

describe('clearResultText', () => {
  const MB = 1024 * 1024;

  test('two 200 results of 1 MB each give the sum', () => {
    const text = view.clearResultText([
      { repo: '/abs/repo-a', status: 200, body: { freedBytes: MB } },
      { repo: '/abs/repo-b', status: 200, body: { freedBytes: MB } },
    ]);
    assert.equal(text, 'Freed 2.0 MB.');
  });

  test('a failed request adds one line and does not change the sum', () => {
    const text = view.clearResultText([
      { repo: '/abs/repo-a', status: 200, body: { freedBytes: MB } },
      { repo: '/abs/repo', status: 404, body: { error: { code: 'REPO_NOT_FOUND', message: 'No such repo.' } } },
    ]);
    assert.equal(text, 'Freed 1.0 MB.\n/abs/repo: No such repo.');
  });

  test('one line for each failed request, in order, with the message only', () => {
    const text = view.clearResultText([
      { repo: '/a', status: 403, body: { error: { message: 'Forbidden.', suggestion: 'Reload the page.' } } },
      { repo: '/b', status: 500, body: null },
      { repo: '/c', status: 0, body: null },
    ]);
    assert.equal(
      text,
      [
        'Freed 0.0 KB.',
        '/a: Forbidden.',
        '/b: Clear failed (HTTP 500).',
        '/c: Clear failed (HTTP 0).',
      ].join('\n')
    );
  });

  test('a size under 1 MiB shows KB', () => {
    assert.equal(view.clearResultText([{ repo: '/a', status: 200, body: { freedBytes: 1536 } }]), 'Freed 1.5 KB.');
    assert.equal(view.clearResultText([{ repo: '/a', status: 200, body: { freedBytes: 0 } }]), 'Freed 0.0 KB.');
  });

  test('a size of 1 MiB or more shows MB', () => {
    assert.equal(view.clearResultText([{ repo: '/a', status: 200, body: { freedBytes: MB } }]), 'Freed 1.0 MB.');
    assert.equal(view.clearResultText([{ repo: '/a', status: 200, body: { freedBytes: 5.5 * MB } }]), 'Freed 5.5 MB.');
  });

  test('a 200 result with no freedBytes counts as 0', () => {
    assert.equal(view.clearResultText([{ repo: '/a', status: 200, body: null }]), 'Freed 0.0 KB.');
    assert.equal(view.clearResultText([{ repo: '/a', status: 200, body: {} }]), 'Freed 0.0 KB.');
  });

  test('no results give a size of 0', () => {
    assert.equal(view.clearResultText([]), 'Freed 0.0 KB.');
    assert.equal(view.clearResultText(), 'Freed 0.0 KB.');
  });
});

// A snapshot with one item of each detail kind. The ship pipeline has two
// review dimensions that each hold a finding at index 0. The review pipeline
// is a standalone review: one step for each dimension, kind findings.
function detailSnapshot() {
  return {
    repos: [
      {
        root: '/abs/repo-a',
        name: 'repo-a',
        learnings: [
          { date: '2026-10-08', heading: 'plan: split the lanes', runId: 'r1', branch: 'main' },
          { date: '2026-10-09', heading: 'harden', runId: 'r2', branch: 'main' },
        ],
        deferred: [
          {
            id: 'D-1', priority: 'high', description: 'Fix the race', created: '2026-10-01',
            source: 'review', severity: 'high', file: 'a.go', line: 42, reason: 'out of scope',
          },
          {
            id: 'D-2', priority: 'low', description: 'Rename it', created: '',
            source: '', severity: '', file: '', line: 0, reason: '',
          },
        ],
        pipelines: [
          {
            id: 'ship-x-1',
            kind: 'ship',
            issues: [
              { source: 'step', severity: 'high', text: 'commit failed', file: 'b.go', line: '12-14', ref: 'commit' },
              { source: 'wave', severity: 'low', text: 'slow wave', file: '', line: '', ref: 'wave 2' },
            ],
            steps: [
              { name: 'execute', detail: { kind: 'waves', waves: [] } },
              {
                name: 'review',
                detail: {
                  kind: 'dimensions',
                  dimensions: [
                    {
                      name: 'security',
                      findingItems: [{ text: 'SQL built from input', severity: 'high', file: 's.go', line: '7' }],
                    },
                    {
                      name: 'performance',
                      findingItems: [{ text: 'N+1 query', severity: 'medium', file: 'p.go', line: '' }],
                    },
                  ],
                },
              },
            ],
          },
          {
            id: 'review-20261008T120000',
            kind: 'review',
            issues: [],
            steps: [
              {
                name: 'docs',
                detail: { kind: 'findings', findings: [{ text: 'Stale doc', severity: 'low', file: 'd.md', line: '3' }] },
              },
            ],
          },
        ],
      },
      {
        root: '/abs/repo-b',
        name: 'repo-b',
        learnings: [],
        deferred: [],
        pipelines: [],
      },
    ],
  };
}

describe('detailKey', () => {
  test('builds the key of each kind from the fields the key uses', () => {
    assert.equal(view.detailKey('deferred', { id: 'D-1' }), 'deferred:D-1');
    assert.equal(view.detailKey('issue', null, { pipeline: 'ship-x-1', index: 3 }), 'issue:ship-x-1:3');
    assert.equal(
      view.detailKey('learning', { date: '2026-10-08', heading: 'plan: x' }),
      'learning:2026-10-08:plan: x'
    );
    assert.equal(
      view.detailKey('finding', null, { pipeline: 'ship-x-1', dimension: 'security', index: 0 }),
      'finding:ship-x-1:security:0'
    );
  });

  test('ignores fields that are not in the key', () => {
    assert.equal(
      view.detailKey('deferred', { id: 'D-1', priority: 'low' }, { pipeline: 'p', index: 9 }),
      'deferred:D-1'
    );
    assert.equal(
      view.detailKey('issue', { id: 'D-1', date: 'x' }, { pipeline: 'p', index: 1, dimension: 'd' }),
      'issue:p:1'
    );
  });

  test('accepts index 0', () => {
    assert.equal(view.detailKey('issue', null, { pipeline: 'p', index: 0 }), 'issue:p:0');
  });

  test('returns an empty string for an unknown kind or a missing key field', () => {
    assert.equal(view.detailKey('other', { id: 'D-1' }, { pipeline: 'p', index: 0 }), '');
    assert.equal(view.detailKey('deferred', {}), '');
    assert.equal(view.detailKey('deferred'), '');
    assert.equal(view.detailKey('issue', null, { pipeline: 'p' }), '');
    assert.equal(view.detailKey('issue', null, { index: 0 }), '');
    assert.equal(view.detailKey('learning', { date: '2026-10-08' }), '');
    assert.equal(view.detailKey('learning', { heading: 'h' }), '');
    assert.equal(view.detailKey('finding', null, { pipeline: 'p', index: 0 }), '');
    assert.equal(view.detailKey('finding', null, { pipeline: 'p', dimension: 'd' }), '');
  });

  test('gives the same key for the same item in two snapshots', () => {
    const first = detailSnapshot();
    const second = detailSnapshot();
    second.repos[0].deferred[0].description = 'changed text';
    const keyIn = (snap) => ({
      deferred: view.detailKey('deferred', snap.repos[0].deferred[0]),
      learning: view.detailKey('learning', snap.repos[0].learnings[0]),
      issue: view.detailKey('issue', snap.repos[0].pipelines[0].issues[0], { pipeline: snap.repos[0].pipelines[0].id, index: 0 }),
    });
    assert.deepEqual(keyIn(first), keyIn(second));
  });

  test('gives two ship dimensions with a finding at index 0 two keys', () => {
    const a = view.detailKey('finding', null, { pipeline: 'ship-x-1', dimension: 'security', index: 0 });
    const b = view.detailKey('finding', null, { pipeline: 'ship-x-1', dimension: 'performance', index: 0 });
    assert.notEqual(a, b);
  });
});

describe('detailItem', () => {
  test('finds a deferred item with its metadata', () => {
    const item = view.detailItem(detailSnapshot(), 'deferred:D-1');
    assert.deepEqual(item, {
      kind: 'deferred',
      repo: '/abs/repo-a',
      title: 'D-1 (high)',
      text: 'Fix the race',
      meta: [
        ['Created', '2026-10-01'],
        ['Source', 'review'],
        ['Severity', 'high'],
        ['File', 'a.go'],
        ['Line', '42'],
        ['Reason', 'out of scope'],
      ],
    });
  });

  test('leaves out the empty metadata of a deferred item', () => {
    const item = view.detailItem(detailSnapshot(), 'deferred:D-2');
    assert.equal(item.title, 'D-2 (low)');
    assert.deepEqual(item.meta, []);
  });

  test('finds an issue by pipeline id and row index', () => {
    const snap = detailSnapshot();
    assert.deepEqual(view.detailItem(snap, 'issue:ship-x-1:0'), {
      kind: 'issue',
      repo: '/abs/repo-a',
      title: 'high: step',
      text: 'commit failed',
      meta: [
        ['Source', 'step'],
        ['Severity', 'high'],
        ['File', 'b.go'],
        ['Line', '12-14'],
        ['Ref', 'commit'],
      ],
    });
    assert.equal(view.detailItem(snap, 'issue:ship-x-1:1').text, 'slow wave');
    assert.equal(view.detailItem(snap, 'issue:ship-x-1:2'), null);
  });

  test('finds a learning whose heading holds a colon, with an empty text', () => {
    const item = view.detailItem(detailSnapshot(), 'learning:2026-10-08:plan: split the lanes');
    assert.deepEqual(item, {
      kind: 'learning',
      repo: '/abs/repo-a',
      title: 'plan: split the lanes',
      text: '',
      meta: [['Date', '2026-10-08']],
    });
  });

  test('finds a finding of a ship dimension', () => {
    const item = view.detailItem(detailSnapshot(), 'finding:ship-x-1:security:0');
    assert.deepEqual(item, {
      kind: 'finding',
      repo: '/abs/repo-a',
      title: 'high: s.go:7',
      text: 'SQL built from input',
      meta: [
        ['Pipeline', 'ship-x-1'],
        ['Dimension', 'security'],
        ['Severity', 'high'],
      ],
    });
  });

  test('finds a finding of a standalone review step by the step name', () => {
    const item = view.detailItem(detailSnapshot(), 'finding:review-20261008T120000:docs:0');
    assert.equal(item.kind, 'finding');
    assert.equal(item.title, 'low: d.md:3');
    assert.equal(item.text, 'Stale doc');
    assert.deepEqual(item.meta[1], ['Dimension', 'docs']);
  });

  test('two ship dimensions with a finding at index 0 give two keys and two items', () => {
    const snap = detailSnapshot();
    const keyA = view.detailKey('finding', null, { pipeline: 'ship-x-1', dimension: 'security', index: 0 });
    const keyB = view.detailKey('finding', null, { pipeline: 'ship-x-1', dimension: 'performance', index: 0 });
    assert.notEqual(keyA, keyB);
    const itemA = view.detailItem(snap, keyA);
    const itemB = view.detailItem(snap, keyB);
    assert.equal(itemA.text, 'SQL built from input');
    assert.equal(itemB.text, 'N+1 query');
    assert.deepEqual(itemA.meta[1], ['Dimension', 'security']);
    assert.deepEqual(itemB.meta[1], ['Dimension', 'performance']);
  });

  test('a finding without a line shows the file only', () => {
    assert.equal(view.detailItem(detailSnapshot(), 'finding:ship-x-1:performance:0').title, 'medium: p.go');
  });

  test('returns the item of every key a builder makes', () => {
    const snap = detailSnapshot();
    const keys = [
      view.detailKey('deferred', snap.repos[0].deferred[1]),
      view.detailKey('learning', snap.repos[0].learnings[1]),
      view.detailKey('issue', null, { pipeline: 'ship-x-1', index: 1 }),
      view.detailKey('finding', null, { pipeline: 'review-20261008T120000', dimension: 'docs', index: 0 }),
    ];
    keys.forEach((key) => {
      assert.notEqual(view.detailItem(snap, key), null, key);
    });
  });

  test('returns the item again after a snapshot rebuild', () => {
    const key = view.detailKey('deferred', { id: 'D-1' });
    const rebuilt = detailSnapshot();
    rebuilt.repos[0].deferred[0].description = 'Fix the race, now with a test';
    assert.equal(view.detailItem(rebuilt, key).text, 'Fix the race, now with a test');
  });

  test('returns the root of the repo that holds the item', () => {
    const snap = detailSnapshot();
    snap.repos[1].deferred = [{ id: 'D-9', priority: 'low', description: 'in b' }];
    assert.equal(view.detailItem(snap, 'deferred:D-9').repo, '/abs/repo-b');
  });

  test('returns null for a gone item, an empty key, or an empty snapshot', () => {
    const snap = detailSnapshot();
    assert.equal(view.detailItem(snap, 'deferred:D-404'), null);
    assert.equal(view.detailItem(snap, 'finding:ship-x-1:security:1'), null);
    assert.equal(view.detailItem(snap, 'finding:ship-x-1:other:0'), null);
    assert.equal(view.detailItem(snap, 'learning:2026-10-08:gone'), null);
    assert.equal(view.detailItem(snap, 'bogus'), null);
    assert.equal(view.detailItem(snap, ''), null);
    assert.equal(view.detailItem(null, 'deferred:D-1'), null);
    assert.equal(view.detailItem({}, 'deferred:D-1'), null);
    assert.equal(view.detailItem({ repos: [{ root: '/r' }] }, 'deferred:D-1'), null);
  });
});

describe('worktreeLabel', () => {
  test('returns an empty string when worktree is empty', () => {
    assert.equal(view.worktreeLabel('/repo', ''), '');
  });

  test('returns an empty string when worktree equals repoRoot', () => {
    assert.equal(view.worktreeLabel('/repo', '/repo'), '');
  });

  test('returns the last path part of a linked worktree', () => {
    assert.equal(view.worktreeLabel('/repo', '/repo-feat-x'), 'repo-feat-x');
    assert.equal(view.worktreeLabel('/Users/me/repo', '/Users/me/repo-feat-x'), 'repo-feat-x');
  });

  test('ignores a trailing slash', () => {
    assert.equal(view.worktreeLabel('/repo', '/repo-feat-x/'), 'repo-feat-x');
  });
});

describe('browser global fallback', () => {
  test('view.js assigns root.sdlcView when module.exports is unavailable', () => {
    const source = fs.readFileSync(path.join(__dirname, '../static/view.js'), 'utf8');
    const fakeRoot = {};
    // The file's own IIFE calls itself with `this` as `root`, so bind `this`
    // via .call() instead of passing a `root` argument.
    // eslint-disable-next-line no-new-func
    const run = new Function('module', source);
    run.call(fakeRoot);
    assert.equal(typeof fakeRoot.sdlcView, 'object');
    assert.equal(typeof fakeRoot.sdlcView.stepGlyph, 'function');
  });
});

// --- render.js: DOM builders on a fake document ---------------------------------

const render = require('../static/render.js');

// The fake document: the DOM calls the render.js builders may use, and no more.
// A new DOM call in a builder needs a matching method here. tickElapsed reads
// the page with querySelectorAll and getAttribute, so its tests use
// fakeElapsedDoc and fakeElapsedNode below.
function fakeDoc() {
  function node(tag) { return { tagName: tag, className: '', attrs: {}, children: [], textContent: '', open: false, hidden: false,
    setAttribute(k, v) { this.attrs[k] = String(v); }, appendChild(c) { this.children.push(c); return c; } }; }
  return { createElement: node };
}

// Visible text of a node: its own textContent, then the text of its children in order.
function textOf(node) {
  return node.textContent + node.children.map(textOf).join('');
}

function findAll(node, match) {
  const out = match(node) ? [node] : [];
  for (const child of node.children) out.push(...findAll(child, match));
  return out;
}

function classesOf(node) {
  return node.className.split(' ').filter(Boolean);
}

function byClass(node, cls) {
  return findAll(node, (n) => classesOf(n).includes(cls));
}

function oneByClass(node, cls) {
  const found = byClass(node, cls);
  assert.equal(found.length, 1, `want one .${cls}, got ${found.length}`);
  return found[0];
}

const REPO = { root: '/src/app', name: 'app', sessions: [] };

function pipeline(extra) {
  return Object.assign(
    {
      id: 'ship-1',
      kind: 'ship',
      branch: 'feat/x',
      worktree: '/src/app',
      status: 'running',
      steps: [
        { name: 'execute', status: 'completed', detail: { kind: 'waves', waves: [] } },
        { name: 'commit', status: 'completed' },
        { name: 'review', status: 'in_progress', detail: { kind: 'dimensions', dimensions: [] } },
        { name: 'pr', status: 'pending' },
      ],
      issues: [],
      sessionId: '',
    },
    extra
  );
}

// An open wait: asked 4m 12s before NOW_WAIT.
const ATTENTION = {
  kind: 'question',
  askedAt: '2026-10-08T14:00:00Z',
  header: 'Guardrail',
  text: 'Approve the splice_test.go change?',
};

// 4m 12s after ATTENTION.askedAt, in ms.
const NOW_WAIT = Date.parse('2026-10-08T14:04:12Z');

describe('render el', () => {
  test('sets the class and puts text through textContent', () => {
    const node = render.el(fakeDoc(), 'span', 'pipe-kind', 'ship');
    assert.equal(node.tagName, 'span');
    assert.equal(node.className, 'pipe-kind');
    assert.equal(node.textContent, 'ship');
  });

  test('turns a number into text and leaves null text empty', () => {
    assert.equal(render.el(fakeDoc(), 'span', '', 3).textContent, '3');
    assert.equal(render.el(fakeDoc(), 'span', '', null).textContent, '');
  });
});

describe('render filterChips', () => {
  const chips = [
    { root: '/a', name: 'a', count: 2, hasFail: false },
    { root: '/b', name: 'b', count: 1, hasFail: true },
  ];

  test('All first, then one button for each repo, with data-root and type button', () => {
    const out = render.filterChips(fakeDoc(), view, chips, new Set());
    assert.deepEqual(out.map((b) => b.attrs['data-root']), ['', '/a', '/b']);
    for (const b of out) {
      assert.equal(b.tagName, 'button');
      assert.equal(b.attrs.type, 'button');
      assert.equal(b.className, 'chip');
    }
    assert.equal(textOf(out[0]), 'All3');
    assert.equal(textOf(out[1]), 'a2');
  });

  test('an empty scope presses only All', () => {
    const out = render.filterChips(fakeDoc(), view, chips, new Set());
    assert.deepEqual(out.map((b) => b.attrs['aria-pressed']), ['true', 'false', 'false']);
  });

  test('a scope presses its repos and releases All', () => {
    const out = render.filterChips(fakeDoc(), view, chips, new Set(['/b']));
    assert.deepEqual(out.map((b) => b.attrs['aria-pressed']), ['false', 'false', 'true']);
  });

  test('a repo with a failed run has a red count', () => {
    const out = render.filterChips(fakeDoc(), view, chips, new Set());
    assert.equal(oneByClass(out[1], 'chip-n').className, 'chip-n');
    assert.equal(oneByClass(out[2], 'chip-n').className, 'chip-n has-fail');
    assert.equal(oneByClass(out[0], 'chip-n').className, 'chip-n');
    assert.match(out[2].attrs['aria-label'], /has a failed run/);
  });
});

describe('render blockHead', () => {
  test('lamp, kind label, branch, repo, status word and the details toggle', () => {
    const head = render.blockHead(fakeDoc(), view, REPO, pipeline({ kind: 'execute' }), false, 3);
    assert.equal(head.tagName, 'header');
    assert.equal(oneByClass(head, 'lamp').className, 'lamp running');
    assert.equal(oneByClass(head, 'lamp').attrs['aria-hidden'], 'true');
    assert.equal(oneByClass(head, 'pipe-kind').textContent, 'exec');
    assert.equal(oneByClass(head, 'pipe-branch').textContent, 'feat/x');
    assert.equal(oneByClass(head, 'pipe-repo').textContent, 'app');
    const status = oneByClass(head, 'pipe-status');
    assert.equal(status.className, 'pipe-status running');
    assert.equal(status.textContent, 'running');
    assert.match(textOf(head), /running/);
    const fold = oneByClass(head, 'fold-btn');
    assert.equal(fold.tagName, 'button');
    assert.equal(fold.attrs['aria-expanded'], 'true');
    assert.equal(textOf(fold), '▸details3');
  });

  test('a collapsed block has aria-expanded false', () => {
    const head = render.blockHead(fakeDoc(), view, REPO, pipeline(), true, 0);
    assert.equal(oneByClass(head, 'fold-btn').attrs['aria-expanded'], 'false');
  });

  test('no issue chip without issues', () => {
    const head = render.blockHead(fakeDoc(), view, REPO, pipeline(), false, 0);
    assert.equal(byClass(head, 'issue-chip').length, 0);
  });

  test('an issue chip with issues', () => {
    const one = render.blockHead(fakeDoc(), view, REPO, pipeline({ issues: [{ text: 'a' }] }), false, 1);
    assert.equal(oneByClass(one, 'issue-chip').textContent, '1 issue');
    const two = render.blockHead(fakeDoc(), view, REPO, pipeline({ issues: [{ text: 'a' }, { text: 'b' }] }), false, 1);
    assert.equal(oneByClass(two, 'issue-chip').textContent, '2 issues');
  });

  test('the branch title names a linked worktree', () => {
    const main = render.blockHead(fakeDoc(), view, REPO, pipeline(), false, 0);
    assert.equal(oneByClass(main, 'pipe-branch').attrs.title, 'feat/x');
    const linked = render.blockHead(fakeDoc(), view, REPO, pipeline({ worktree: '/wt/app-feat-x' }), false, 0);
    assert.equal(oneByClass(linked, 'pipe-branch').attrs.title, 'feat/x · worktree app-feat-x');
  });

  test('a pipeline with an attention has the class waiting on its lamp', () => {
    const head = render.blockHead(fakeDoc(), view, REPO, pipeline({ attention: ATTENTION }), false, 0);
    assert.equal(oneByClass(head, 'lamp').className, 'lamp running waiting');
    assert.equal(oneByClass(head, 'lamp').attrs['aria-hidden'], 'true');
    assert.equal(oneByClass(head, 'pipe-status').className, 'pipe-status running');
  });

  test('a pipeline without an attention has no waiting class on its lamp', () => {
    const head = render.blockHead(fakeDoc(), view, REPO, pipeline(), false, 0);
    assert.equal(oneByClass(head, 'lamp').className, 'lamp running');
    assert.equal(byClass(head, 'waiting').length, 0);
  });

  test('markup in a branch name stays text: <b>x</b>', () => {
    const head = render.blockHead(fakeDoc(), view, REPO, pipeline({ branch: '<b>x</b>' }), false, 0);
    const branch = oneByClass(head, 'pipe-branch');
    assert.equal(branch.textContent, '<b>x</b>');
    assert.equal(branch.children.length, 0);
    assert.equal(branch.attrs.title, '<b>x</b>');
  });
});

describe('render blockHead total time', () => {
  const STARTED = '2026-10-08T14:00:00Z'; // NOW_WAIT is 4m 12s after this
  const head = (extra, now = NOW_WAIT) =>
    render.blockHead(fakeDoc(), view, REPO, pipeline(Object.assign({ startedAt: STARTED }, extra)), false, 0, now);

  test('a running pipeline gets a live chip with a lamp, as the next sibling of the branch', () => {
    const h = head({ status: 'running' });
    const chip = oneByClass(h, 'pipe-dur');
    assert.equal(chip.className, 'pipe-dur live');
    assert.equal(chip.attrs.title, 'Time since the pipeline started');
    assert.equal(h.children[h.children.indexOf(oneByClass(h, 'pipe-branch')) + 1], chip);
    assert.equal(h.children.indexOf(chip) + 1, h.children.indexOf(oneByClass(h, 'pipe-side')));
    assert.deepEqual(chip.children.map((c) => c.className), ['lamp running', 'dur-label', 'dur-live']);
    assert.equal(chip.children[0].attrs['aria-hidden'], 'true');
    assert.equal(textOf(chip), 'total4m 12s');
  });

  test('the value span of a running chip carries data-since for the timer', () => {
    const value = oneByClass(head({ status: 'running' }), 'dur-live');
    assert.equal(value.attrs['data-since'], STARTED);
    assert.equal(value.textContent, '4m 12s');
  });

  test('now can be a Date', () => {
    assert.equal(oneByClass(head({ status: 'running' }, new Date(NOW_WAIT)), 'dur-live').textContent, '4m 12s');
  });

  test('a completed pipeline stops at completedAt: plain chip, no lamp, no timer', () => {
    const h = head({ status: 'completed', completedAt: '2026-10-08T14:12:30Z' }, NOW_WAIT + 3600000);
    const chip = oneByClass(h, 'pipe-dur');
    assert.equal(chip.className, 'pipe-dur');
    assert.equal(chip.attrs.title, 'Total time of the pipeline');
    assert.deepEqual(chip.children.map((c) => c.className), ['dur-label', '']);
    assert.equal(textOf(chip), 'total12m 30s');
    assert.equal(byClass(h, 'dur-live').length, 0);
    assert.equal(byClass(h, 'lamp').length, 1);
    assert.equal(chip.children[1].attrs['data-since'], undefined);
  });

  test('a stalled or failed pipeline stops at updatedAt: plain chip', () => {
    for (const status of ['stalled', 'failed']) {
      const h = head({ status, updatedAt: '2026-10-08T14:02:05Z' }, NOW_WAIT + 3600000);
      const chip = oneByClass(h, 'pipe-dur');
      assert.equal(chip.className, 'pipe-dur', status);
      assert.equal(textOf(chip), 'total2m 5s', status);
      assert.equal(byClass(h, 'dur-live').length, 0, status);
      assert.equal(byClass(h, 'lamp').length, 1, status);
    }
  });

  test('only a running pipeline gets the live class', () => {
    for (const status of ['completed', 'failed', 'stalled']) {
      const h = head({ status, completedAt: '2026-10-08T14:12:30Z', updatedAt: '2026-10-08T14:12:30Z' });
      assert.ok(!classesOf(oneByClass(h, 'pipe-dur')).includes('live'), status);
    }
    assert.ok(classesOf(oneByClass(head({ status: 'running' }), 'pipe-dur')).includes('live'));
  });

  test('a running pipeline that has completedAt stops there: plain chip', () => {
    const h = head({ status: 'running', completedAt: '2026-10-08T14:01:00Z' });
    assert.equal(oneByClass(h, 'pipe-dur').className, 'pipe-dur');
    assert.equal(textOf(oneByClass(h, 'pipe-dur')), 'total1m 0s');
    assert.equal(byClass(h, 'dur-live').length, 0);
  });

  test('a bad or absent startedAt gives no chip', () => {
    for (const startedAt of [undefined, null, '', 'not a time']) {
      for (const status of ['running', 'completed', 'failed']) {
        const h = head({ status, startedAt, completedAt: '2026-10-08T14:12:30Z', updatedAt: '2026-10-08T14:12:30Z' });
        assert.equal(byClass(h, 'pipe-dur').length, 0, `${status} startedAt ${JSON.stringify(startedAt)}`);
      }
    }
  });

  test('a running pipeline with no now gives no chip, and a stalled one with no updatedAt gives none', () => {
    const noNow = render.blockHead(fakeDoc(), view, REPO, pipeline({ status: 'running', startedAt: STARTED }), false, 0);
    assert.equal(byClass(noNow, 'pipe-dur').length, 0);
    assert.equal(byClass(head({ status: 'stalled', updatedAt: undefined }), 'pipe-dur').length, 0);
  });

  test('a clock that runs behind the start gives 0s, not a negative time', () => {
    assert.equal(oneByClass(head({ status: 'running' }, Date.parse(STARTED) - 60000), 'dur-live').textContent, '0s');
  });

  test('a pipeline with no startedAt keeps the old head: lamp, kind, branch, side', () => {
    const h = render.blockHead(fakeDoc(), view, REPO, pipeline(), false, 0, NOW_WAIT);
    assert.deepEqual(h.children.map((c) => c.className), ['lamp running', 'pipe-kind', 'pipe-branch', 'pipe-side']);
  });
});

describe('render stationTrack', () => {
  test('a station with a section is a button with aria-label "name, status"', () => {
    const track = render.stationTrack(fakeDoc(), view, pipeline(), 2);
    const stations = byClass(track, 'station');
    assert.equal(stations.length, 4);
    const exec = stations[0];
    assert.equal(exec.tagName, 'button');
    assert.equal(exec.attrs.type, 'button');
    assert.equal(exec.attrs['aria-label'], 'execute, completed');
    assert.equal(exec.attrs['data-station'], '0');
    assert.equal(exec.attrs['data-section'], 'execute');
    assert.equal(stations[2].attrs['aria-label'], 'review, in progress');
  });

  test('a station with no section is not a button and has no click target', () => {
    const track = render.stationTrack(fakeDoc(), view, pipeline(), 0);
    const commit = byClass(track, 'station')[1];
    assert.equal(commit.tagName, 'div');
    assert.equal(commit.attrs['data-station'], undefined);
    assert.equal(commit.attrs.type, undefined);
    assert.ok(!classesOf(commit).includes('has-section'));
    assert.equal(oneByClass(commit, 'sr-only').textContent, ', completed');
  });

  test('marks the selected station, lit wires, glyphs, and the current label', () => {
    const track = render.stationTrack(fakeDoc(), view, pipeline(), 2);
    const stations = byClass(track, 'station');
    assert.deepEqual(stations.map((s) => classesOf(s).includes('selected')), [false, false, true, false]);
    assert.deepEqual(stations.map((s) => oneByClass(s, 'wire').className), ['wire', 'wire lit', 'wire lit', 'wire']);
    assert.equal(oneByClass(stations[2], 'glyph').className, 'glyph in_progress');
    assert.equal(oneByClass(stations[2], 'glyph').textContent, view.stepGlyph('in_progress').glyph);
    assert.equal(oneByClass(stations[2], 'label').className, 'label current');
    assert.equal(oneByClass(stations[0], 'label').textContent, view.stationLabel('execute'));
  });

  test('with an attention the current station shows the waiting glyph and the rest keep their glyphs', () => {
    const track = render.stationTrack(fakeDoc(), view, pipeline({ attention: ATTENTION }), 2);
    const glyphs = byClass(track, 'glyph');
    assert.deepEqual(glyphs.map((g) => g.className), ['glyph completed', 'glyph completed', 'glyph in_progress', 'glyph pending']);
    assert.deepEqual(glyphs.map((g) => g.textContent), [
      view.stepGlyph('completed').glyph,
      view.stepGlyph('completed').glyph,
      '◈',
      view.stepGlyph('pending').glyph,
    ]);
  });

  test('without an attention the current station keeps the in_progress glyph', () => {
    const track = render.stationTrack(fakeDoc(), view, pipeline(), 2);
    const current = byClass(track, 'glyph').filter((g) => classesOf(g).includes('in_progress'));
    assert.deepEqual(current.map((g) => g.textContent), [view.stepGlyph('in_progress').glyph]);
    assert.ok(!textOf(track).includes('◈'));
  });
});

describe('render stationTrack step time', () => {
  const T = (s) => `2026-10-08T14:${s}Z`; // T('00:00') is 4m 12s before NOW_WAIT
  const stepsOf = (steps) => render.stationTrack(fakeDoc(), view, pipeline({ steps }), 0, NOW_WAIT);
  const durs = (track) => byClass(track, 'station').map((s) => byClass(s, 'dur'));

  test('the current step counts up to now: span.dur.current.dur-live with data-since, right after the label', () => {
    const track = stepsOf([{ name: 'review', status: 'in_progress', startedAt: T('02:00') }]);
    const station = byClass(track, 'station')[0];
    const dur = oneByClass(station, 'dur');
    assert.equal(dur.className, 'dur current dur-live');
    assert.equal(dur.textContent, '2m 12s');
    assert.equal(dur.attrs['data-since'], T('02:00'));
    assert.equal(dur.attrs.title, 'Time since the step started');
    assert.equal(station.children[station.children.indexOf(oneByClass(station, 'label')) + 1], dur);
  });

  test('a completed step with both times shows the duration: plain span.dur, no timer', () => {
    const track = stepsOf([{ name: 'execute', status: 'completed', startedAt: T('00:00'), completedAt: T('01:30') }]);
    const dur = oneByClass(track, 'dur');
    assert.equal(dur.className, 'dur');
    assert.equal(dur.textContent, '1m 30s');
    assert.equal(dur.attrs['data-since'], undefined);
    assert.equal(dur.attrs.title, 'Time the step took');
    assert.equal(byClass(track, 'dur-live').length, 0);
  });

  test('a failed step with both times shows the duration', () => {
    const track = stepsOf([{ name: 'review', status: 'failed', startedAt: T('00:00'), completedAt: T('00:45') }]);
    const dur = oneByClass(track, 'dur');
    assert.equal(dur.className, 'dur');
    assert.equal(dur.textContent, '45s');
  });

  test('a failed or completed step with no completedAt has no time', () => {
    for (const status of ['failed', 'completed']) {
      const track = stepsOf([{ name: 'review', status, startedAt: T('00:00') }]);
      assert.equal(byClass(track, 'dur').length, 0, status);
    }
  });

  test('a pending or skipped step has no time, even with times set', () => {
    for (const status of ['pending', 'skipped']) {
      const track = stepsOf([{ name: 'pr', status, startedAt: T('00:00'), completedAt: T('01:00') }]);
      assert.equal(byClass(track, 'dur').length, 0, status);
    }
  });

  test('a bad or absent startedAt gives no time for a current, completed, or failed step', () => {
    for (const startedAt of [undefined, null, '', 'not a time']) {
      for (const status of ['in_progress', 'completed', 'failed']) {
        const track = stepsOf([{ name: 'review', status, startedAt, completedAt: T('01:00') }]);
        assert.equal(byClass(track, 'dur').length, 0, `${status} startedAt ${JSON.stringify(startedAt)}`);
      }
    }
  });

  test('a step with no snapshot times draws no span.dur', () => {
    assert.deepEqual(durs(render.stationTrack(fakeDoc(), view, pipeline(), 2, NOW_WAIT)), [[], [], [], []]);
  });

  test('with no now, the current step has no time', () => {
    const track = render.stationTrack(fakeDoc(), view, pipeline({ steps: [{ name: 'review', status: 'in_progress', startedAt: T('02:00') }] }), 0);
    assert.equal(byClass(track, 'dur').length, 0);
  });

  test('each station holds its own time', () => {
    const track = stepsOf([
      { name: 'execute', status: 'completed', startedAt: T('00:00'), completedAt: T('01:30') },
      { name: 'commit', status: 'completed' },
      { name: 'review', status: 'in_progress', startedAt: T('02:00') },
      { name: 'pr', status: 'pending' },
    ]);
    assert.deepEqual(durs(track).map((d) => d.map((n) => n.textContent)), [['1m 30s'], [], ['2m 12s'], []]);
  });

  test('a clock that runs behind the start gives 0s, not a negative time', () => {
    const track = render.stationTrack(fakeDoc(), view, pipeline({ steps: [{ name: 'review', status: 'in_progress', startedAt: T('10:00') }] }), 0, NOW_WAIT);
    assert.equal(oneByClass(track, 'dur').textContent, '0s');
  });
});

describe('render pipelineBlock', () => {
  test('passes now to the head chip and to the current step, so both count up to the same moment', () => {
    const p = pipeline({
      startedAt: '2026-10-08T14:00:00Z',
      steps: [
        { name: 'execute', status: 'completed', startedAt: '2026-10-08T14:00:00Z', completedAt: '2026-10-08T14:01:30Z' },
        { name: 'review', status: 'in_progress', startedAt: '2026-10-08T14:02:00Z' },
      ],
    });
    const block = render.pipelineBlock(fakeDoc(), view, REPO, p, { collapsed: false, selected: 0, now: NOW_WAIT });
    assert.deepEqual(byClass(block, 'dur-live').map((n) => n.textContent), ['4m 12s', '2m 12s']);
    assert.equal(textOf(oneByClass(block, 'pipe-dur')), 'total4m 12s');
    assert.deepEqual(byClass(block, 'dur').map((n) => n.textContent), ['1m 30s', '2m 12s']);
  });

  test('an article with data-key, the head, the track, and one tile for each step with a section', () => {
    const p = pipeline();
    const block = render.pipelineBlock(fakeDoc(), view, REPO, p, { collapsed: false, selected: 0 });
    assert.equal(block.tagName, 'article');
    assert.equal(block.className, 'pipe-block');
    assert.equal(block.attrs['data-key'], view.pipelineKey(REPO, p));
    assert.equal(block.attrs['data-id'], 'ship-1');
    oneByClass(block, 'pipe-head');
    oneByClass(block, 'track');
    const tiles = oneByClass(block, 'step-detail').children;
    assert.deepEqual(tiles.map((t) => t.attrs['data-section']), ['execute', 'review']);
    assert.ok(classesOf(byClass(block, 'station')[0]).includes('selected'));
  });

  test('a collapsed block has the collapsed class', () => {
    const block = render.pipelineBlock(fakeDoc(), view, REPO, pipeline(), { collapsed: true, selected: 0 });
    assert.equal(block.className, 'pipe-block collapsed');
    assert.equal(oneByClass(block, 'fold-btn').attrs['aria-expanded'], 'false');
  });

  test('the details count is the tile count, session included', () => {
    const repo = Object.assign({}, REPO, { sessions: [{ id: 's1', branch: 'feat/x', lastSeen: '2026-01-01T00:00:00Z' }] });
    const p = pipeline({ issues: [{ text: 'a' }] });
    const block = render.pipelineBlock(fakeDoc(), view, repo, p, { collapsed: false, selected: 0 });
    assert.equal(oneByClass(block, 'fold-count').textContent, String(view.tileCount(p, repo.sessions[0])));
    assert.equal(oneByClass(block, 'fold-count').textContent, '4');
  });

  test('with no selected index the default station is marked', () => {
    const p = pipeline();
    const block = render.pipelineBlock(fakeDoc(), view, REPO, p, { collapsed: false });
    const marked = byClass(block, 'station').map((s) => classesOf(s).includes('selected'));
    assert.equal(marked.indexOf(true), view.defaultStationIndex(p.steps));
  });

  test('without an attention the block has no attn class and no banner', () => {
    const block = render.pipelineBlock(fakeDoc(), view, REPO, pipeline(), { collapsed: false, selected: 0, now: NOW_WAIT });
    assert.equal(block.className, 'pipe-block');
    assert.equal(byClass(block, 'attn-bar').length, 0);
    assert.equal(byClass(block, 'attn-text').length, 0);
    assert.equal(byClass(block, 'waiting').length, 0);
  });

  test('with an attention the block has the attn class, the banner first, the waiting lamp, and the waiting glyph', () => {
    const block = render.pipelineBlock(fakeDoc(), view, REPO, pipeline({ attention: ATTENTION }), {
      collapsed: false,
      selected: 0,
      now: NOW_WAIT,
    });
    assert.equal(block.className, 'pipe-block attn');
    assert.deepEqual(block.children.map((c) => c.className), ['attn-bar', 'attn-text', 'pipe-head', 'track-panel']);
    assert.equal(textOf(block.children[0]), '◈ WAITING ON YOU · 4m 12s');
    assert.equal(textOf(block.children[1]), 'Guardrail: "Approve the splice_test.go change?"');
    assert.equal(oneByClass(block, 'waiting').className, 'lamp running waiting');
    assert.equal(byClass(block, 'glyph').filter((g) => g.textContent === '◈').length, 1);
  });

  test('a collapsed block with an attention keeps the banner and has both classes', () => {
    const block = render.pipelineBlock(fakeDoc(), view, REPO, pipeline({ attention: ATTENTION }), {
      collapsed: true,
      selected: 0,
      now: NOW_WAIT,
    });
    assert.equal(block.className, 'pipe-block collapsed attn');
    assert.equal(block.children[0].className, 'attn-bar');
  });

  test('now can be a Date', () => {
    const block = render.pipelineBlock(fakeDoc(), view, REPO, pipeline({ attention: ATTENTION }), {
      collapsed: false,
      now: new Date(NOW_WAIT),
    });
    assert.equal(oneByClass(block, 'attn-elapsed').textContent, '4m 12s');
  });
});

describe('render elapsedText', () => {
  const ASKED = '2026-10-08T14:00:00Z';
  const ASKED_MS = Date.parse(ASKED);

  test('seconds, then minutes with seconds, then hours with minutes', () => {
    assert.equal(render.elapsedText(view, ASKED, ASKED_MS + 26000), '26s');
    assert.equal(render.elapsedText(view, ASKED, ASKED_MS + 252000), '4m 12s');
    assert.equal(render.elapsedText(view, ASKED, ASKED_MS + 3780000), '1h 03m');
  });

  test('now can be a Date', () => {
    assert.equal(render.elapsedText(view, ASKED, new Date(ASKED_MS + 252000)), '4m 12s');
  });

  test('a time in the future gives 0s, not a negative time', () => {
    assert.equal(render.elapsedText(view, ASKED, ASKED_MS - 5000), '0s');
  });

  test('a bad or missing timestamp gives an empty string', () => {
    assert.equal(render.elapsedText(view, 'bad', ASKED_MS), '');
    assert.equal(render.elapsedText(view, '', ASKED_MS), '');
    assert.equal(render.elapsedText(view, undefined, ASKED_MS), '');
  });
});

describe('render attentionRows', () => {
  test('the bar holds the glyph, the label, and the elapsed span with data-asked', () => {
    const [bar] = render.attentionRows(fakeDoc(), view, ATTENTION, NOW_WAIT);
    assert.equal(bar.tagName, 'div');
    assert.equal(bar.className, 'attn-bar');
    assert.equal(textOf(bar), '◈ WAITING ON YOU · 4m 12s');
    const glyph = oneByClass(bar, 'attn-glyph');
    assert.equal(glyph.textContent, '◈');
    assert.equal(glyph.attrs['aria-hidden'], 'true');
    const elapsed = oneByClass(bar, 'attn-elapsed');
    assert.equal(elapsed.attrs['data-asked'], '2026-10-08T14:00:00Z');
    assert.equal(elapsed.attrs['aria-live'], 'off');
    assert.equal(elapsed.textContent, '4m 12s');
  });

  test('the text line is header, colon, and the quoted text', () => {
    const rows = render.attentionRows(fakeDoc(), view, ATTENTION, NOW_WAIT);
    assert.equal(rows.length, 2);
    assert.equal(rows[1].className, 'attn-text');
    assert.equal(rows[1].textContent, 'Guardrail: "Approve the splice_test.go change?"');
  });

  test('a header with no text gives the header alone', () => {
    const rows = render.attentionRows(fakeDoc(), view, { askedAt: ATTENTION.askedAt, header: 'Permission' }, NOW_WAIT);
    assert.equal(rows[1].textContent, 'Permission');
  });

  test('a text with no header gives the quoted text alone', () => {
    const rows = render.attentionRows(fakeDoc(), view, { askedAt: ATTENTION.askedAt, text: 'Run it?' }, NOW_WAIT);
    assert.equal(rows[1].textContent, '"Run it?"');
  });

  test('no header and no text gives no text line', () => {
    const rows = render.attentionRows(fakeDoc(), view, { askedAt: ATTENTION.askedAt }, NOW_WAIT);
    assert.equal(rows[0].className, 'attn-bar');
    assert.equal(rows[1], null);
  });

  test('no askedAt gives an empty data-asked and an empty elapsed text', () => {
    const [bar] = render.attentionRows(fakeDoc(), view, { header: 'Permission' }, NOW_WAIT);
    const elapsed = oneByClass(bar, 'attn-elapsed');
    assert.equal(elapsed.attrs['data-asked'], '');
    assert.equal(elapsed.textContent, '');
  });

  test('markup in the header and the text stays text: <b>x</b>', () => {
    const rows = render.attentionRows(fakeDoc(), view, { askedAt: ATTENTION.askedAt, header: '<i>h</i>', text: '<b>x</b>' }, NOW_WAIT);
    assert.equal(rows[1].textContent, '<i>h</i>: "<b>x</b>"');
    assert.equal(rows[1].children.length, 0);
  });
});

describe('render scopedTitle', () => {
  const BASE = 'SDLC dashboard';
  const waiting = { status: 'running', attention: ATTENTION };
  const repos = [
    { root: '/a', pipelines: [waiting, { status: 'running' }] },
    { root: '/b', pipelines: [{ status: 'running' }] },
    { root: '/c', pipelines: [waiting] },
  ];

  test('an empty scope counts the waiting runs of every repo', () => {
    assert.equal(render.scopedTitle(view, BASE, repos, new Set()), '(2) SDLC dashboard');
  });

  test('a missing scope counts every repo', () => {
    assert.equal(render.scopedTitle(view, BASE, repos, undefined), '(2) SDLC dashboard');
  });

  test('a scope that leaves out every waiting repo gives the base title', () => {
    assert.equal(render.scopedTitle(view, BASE, repos, new Set(['/b'])), 'SDLC dashboard');
  });

  test('a scope that holds one waiting repo counts only that repo', () => {
    assert.equal(render.scopedTitle(view, BASE, repos, new Set(['/a', '/b'])), '(1) SDLC dashboard');
    assert.equal(render.scopedTitle(view, BASE, repos, new Set(['/c'])), '(1) SDLC dashboard');
  });

  test('a scope that holds both waiting repos counts both', () => {
    assert.equal(render.scopedTitle(view, BASE, repos, new Set(['/a', '/c'])), '(2) SDLC dashboard');
  });

  test('no repo gives the base title', () => {
    assert.equal(render.scopedTitle(view, BASE, [], new Set(['/a'])), 'SDLC dashboard');
  });
});

// A fake wait-banner node: only getAttribute and textContent, the two members tickElapsed uses.
function fakeElapsedNode(askedAt, text) {
  return {
    textContent: text,
    getAttribute(name) {
      return name === 'data-asked' ? askedAt : null;
    },
  };
}

// A fake duration node: only getAttribute and textContent, the two members tickElapsed uses.
function fakeDurNode(since, text) {
  return {
    textContent: text,
    getAttribute(name) {
      return name === 'data-since' ? since : null;
    },
  };
}

// A fake document for tickElapsed: querySelectorAll gives nodes for '.attn-elapsed', durNodes for
// '.dur-live', and nothing for any other selector; queries records every selector asked.
function fakeElapsedDoc(nodes, durNodes = []) {
  const queries = [];
  return {
    queries,
    querySelectorAll(selector) {
      queries.push(selector);
      if (selector === '.attn-elapsed') return nodes;
      return selector === '.dur-live' ? durNodes : [];
    },
  };
}

describe('render tickElapsed', () => {
  const ASKED = '2026-10-08T14:00:00Z';
  const ASKED_MS = Date.parse(ASKED);

  test('a readable data-asked rewrites the text from now', () => {
    const node = fakeElapsedNode(ASKED, '0s');
    const doc = fakeElapsedDoc([node]);
    render.tickElapsed(doc, view, ASKED_MS + 252000);
    assert.equal(node.textContent, '4m 12s');
    assert.deepEqual(doc.queries, ['.attn-elapsed', '.dur-live']);
  });

  test('now can be a Date', () => {
    const node = fakeElapsedNode(ASKED, '0s');
    render.tickElapsed(fakeElapsedDoc([node]), view, new Date(ASKED_MS + 26000));
    assert.equal(node.textContent, '26s');
  });

  test('an unreadable data-asked keeps the old text', () => {
    for (const asked of ['not a time', '', null]) {
      const node = fakeElapsedNode(asked, '4m 12s');
      render.tickElapsed(fakeElapsedDoc([node]), view, ASKED_MS + 300000);
      assert.equal(node.textContent, '4m 12s', `data-asked ${JSON.stringify(asked)}`);
    }
  });

  test('every node is handled, and a bad node does not stop the next one', () => {
    const first = fakeElapsedNode(ASKED, '0s');
    const bad = fakeElapsedNode('not a time', 'kept');
    const second = fakeElapsedNode('2026-10-08T13:59:00Z', '0s');
    render.tickElapsed(fakeElapsedDoc([first, bad, second]), view, ASKED_MS + 26000);
    assert.equal(first.textContent, '26s');
    assert.equal(bad.textContent, 'kept');
    assert.equal(second.textContent, '1m 26s');
  });

  test('a page with no wait banner changes nothing', () => {
    const doc = fakeElapsedDoc([]);
    render.tickElapsed(doc, view, ASKED_MS);
    assert.deepEqual(doc.queries, ['.attn-elapsed', '.dur-live']);
  });

  test('a readable data-since rewrites the text of a .dur-live node from now', () => {
    const chip = fakeDurNode(ASKED, '0s');
    const step = fakeDurNode('2026-10-08T14:03:00Z', '0s');
    render.tickElapsed(fakeElapsedDoc([], [chip, step]), view, ASKED_MS + 252000);
    assert.equal(chip.textContent, '4m 12s');
    assert.equal(step.textContent, '1m 12s');
  });

  test('one pass rewrites the wait banner and the duration nodes together', () => {
    const banner = fakeElapsedNode(ASKED, '0s');
    const dur = fakeDurNode(ASKED, '0s');
    render.tickElapsed(fakeElapsedDoc([banner], [dur]), view, new Date(ASKED_MS + 26000));
    assert.equal(banner.textContent, '26s');
    assert.equal(dur.textContent, '26s');
  });

  test('an unreadable data-since keeps the old text, and the next node is still handled', () => {
    for (const since of ['not a time', '', null]) {
      const bad = fakeDurNode(since, '4m 12s');
      const good = fakeDurNode(ASKED, '0s');
      render.tickElapsed(fakeElapsedDoc([], [bad, good]), view, ASKED_MS + 26000);
      assert.equal(bad.textContent, '4m 12s', `data-since ${JSON.stringify(since)}`);
      assert.equal(good.textContent, '26s');
    }
  });

  test('a start time after now gives 0s, not a negative time', () => {
    const node = fakeDurNode(ASKED, '5m 0s');
    render.tickElapsed(fakeElapsedDoc([], [node]), view, ASKED_MS - 60000);
    assert.equal(node.textContent, '0s');
  });
});

describe('render headerTotals', () => {
  test('running, stalled, failed and waiting over every repo', () => {
    const repos = [
      { root: '/a', pipelines: [{ status: 'running' }, { status: 'failed' }] },
      { root: '/b', pipelines: [{ status: 'running' }, { status: 'completed' }] },
    ];
    const out = render.headerTotals(fakeDoc(), view, repos);
    assert.deepEqual(out.map((n) => n.className), ['c-run', 'c-stall', 'c-fail', 'c-wait']);
    assert.deepEqual(out.map(textOf), ['running · 2', 'stalled · 0', 'failed · 1', 'waiting · 0']);
    assert.equal(out[0].children[0].tagName, 'strong');
  });

  test('the waiting span counts the pipelines with an attention, in every repo', () => {
    const repos = [
      { root: '/a', pipelines: [{ status: 'running', attention: ATTENTION }, { status: 'running' }] },
      { root: '/b', pipelines: [{ status: 'running', attention: ATTENTION }] },
    ];
    const out = render.headerTotals(fakeDoc(), view, repos);
    assert.deepEqual(out.map(textOf), ['running · 3', 'stalled · 0', 'failed · 0', 'waiting · 2']);
    assert.equal(out[3].className, 'c-wait');
    assert.equal(out[3].children[0].tagName, 'strong');
    assert.equal(out[3].children[0].textContent, '2');
  });
});

describe('render activityPanel', () => {
  const repos = [
    {
      root: '/a',
      name: 'a',
      deferred: [
        { id: 'd-1', priority: 'high', description: 'fix the cursor' },
        { id: 'd-2', priority: 'low', description: 'rename a helper' },
      ],
      learnings: [{ date: '2026-10-08', heading: 'plan: keep maps small', branch: 'main' }],
    },
    { root: '/b', name: 'b', deferred: [{ id: 'd-9', priority: 'medium', description: 'other repo' }], learnings: [] },
  ];

  test('deferred panel first, then learnings, with counts', () => {
    const grid = render.activityPanel(fakeDoc(), view, repos, new Set());
    assert.equal(grid.className, 'act-grid');
    const titles = byClass(grid, 'list-title').map(textOf);
    assert.deepEqual(titles, ['Open deferred (3)', 'Learnings today (1)']);
  });

  test('a deferred row: severity chip, text, then repo · id, and the hint', () => {
    const grid = render.activityPanel(fakeDoc(), view, repos, new Set());
    const deferredPanel = grid.children[0];
    const row = byClass(deferredPanel, 'act-row')[0];
    assert.equal(row.children[0].className, 'sev sev-high');
    assert.equal(row.children[0].textContent, 'high');
    assert.equal(oneByClass(row, 'act-text').textContent, 'fix the cursor');
    assert.equal(oneByClass(row, 'act-meta').textContent, 'a · d-1');
    assert.equal(textOf(oneByClass(deferredPanel, 'hint')), 'Triage these with /sdlc:deferred');
  });

  test('a learning row: chip learning, heading, then repo · branch', () => {
    const grid = render.activityPanel(fakeDoc(), view, repos, new Set());
    const row = oneByClass(grid.children[1], 'act-row');
    assert.equal(row.children[0].className, 'sev');
    assert.equal(row.children[0].textContent, 'learning');
    assert.equal(oneByClass(row, 'act-text').textContent, 'plan: keep maps small');
    assert.equal(oneByClass(row, 'act-meta').textContent, 'a · main');
  });

  test('every row is a button with a data-detail key: deferred by id, learning by date and heading', () => {
    const grid = render.activityPanel(fakeDoc(), view, repos, new Set());
    const rows = byClass(grid, 'act-row');
    assert.deepEqual(rows.map((r) => r.tagName), ['button', 'button', 'button', 'button']);
    assert.deepEqual(rows.map((r) => r.attrs.type), ['button', 'button', 'button', 'button']);
    assert.deepEqual(rows.map((r) => r.attrs['data-detail']), [
      'deferred:d-1', 'deferred:d-2', 'deferred:d-9', 'learning:2026-10-08:plan: keep maps small',
    ]);
    for (const row of rows) assert.ok(view.detailItem({ repos }, row.attrs['data-detail']), row.attrs['data-detail']);
  });

  test('a row with no id or date has no data-detail, so a click opens nothing', () => {
    const grid = render.activityPanel(fakeDoc(), view, [
      { root: '/c', name: 'c', deferred: [{ priority: 'low', description: 'x' }], learnings: [{ heading: 'h', branch: 'main' }] },
    ], new Set());
    const rows = byClass(grid, 'act-row');
    assert.equal(rows.length, 2);
    for (const row of rows) assert.equal(row.attrs['data-detail'], undefined);
  });

  test('the list keeps the first 200 characters and has no tooltip; the viewer shows the full text', () => {
    const long = 'x'.repeat(250);
    const grid = render.activityPanel(fakeDoc(), view, [
      { root: '/c', name: 'c', deferred: [{ id: 'd-7', priority: 'low', description: long }], learnings: [] },
    ], new Set());
    const row = oneByClass(grid, 'act-row');
    const text = oneByClass(row, 'act-text');
    assert.equal(text.textContent, 'x'.repeat(200) + '…');
    assert.equal(text.attrs.title, undefined);
    assert.equal(view.detailItem({ repos: [{ root: '/c', deferred: [{ id: 'd-7', priority: 'low', description: long }] }] }, 'deferred:d-7').text, long);
  });

  test('the scope hides rows of other repos', () => {
    const grid = render.activityPanel(fakeDoc(), view, repos, new Set(['/b']));
    const titles = byClass(grid, 'list-title').map(textOf);
    assert.deepEqual(titles, ['Open deferred (1)', 'Learnings today (0)']);
    assert.equal(oneByClass(grid.children[0], 'act-meta').textContent, 'b · d-9');
    assert.equal(textOf(oneByClass(grid.children[1], 'generic-line')), 'No learnings today.');
  });

  test('empty lists show their empty states and no hint', () => {
    const grid = render.activityPanel(fakeDoc(), view, [{ root: '/c', name: 'c' }], new Set());
    assert.equal(oneByClass(grid.children[0], 'generic-line').textContent, 'No open deferred items.');
    assert.equal(byClass(grid.children[0], 'hint').length, 0);
    assert.equal(oneByClass(grid.children[1], 'generic-line').textContent, 'No learnings today.');
  });
});

describe('render emptyState', () => {
  const cases = [
    ['none-in-scope', undefined, 'No pipelines for the selected repos.'],
    ['no-deferred', undefined, 'No open deferred items.'],
    ['no-learnings', undefined, 'No learnings today.'],
    ['no-history', undefined, 'No finished runs for the selected repos.'],
    ['no-pipelines', view.emptyText({ repos: [] }), view.emptyText({ repos: [] })],
    ['repo-error', { name: 'app', error: 'open state: permission denied' }, 'Cannot read app: open state: permission denied'],
  ];
  for (const [kind, detail, want] of cases) {
    test(`${kind} has its own text`, () => {
      const line = render.emptyState(fakeDoc(), kind, detail);
      assert.equal(line.tagName, 'p');
      assert.equal(line.className, 'generic-line');
      assert.equal(line.attrs['data-empty'], kind);
      assert.equal(line.textContent, want);
      assert.equal(line.children.length, 0);
    });
  }

  test('no-pipelines takes its text from view.emptyText', () => {
    assert.match(render.emptyState(fakeDoc(), 'no-pipelines', view.emptyText({ repos: [] })).textContent, /Nothing is running/);
  });
});

// --- render.js: tiles and the History table --------------------------------------

const FIXTURE = JSON.parse(fs.readFileSync(path.join(__dirname, 'snapshot.fixture.json'), 'utf8'));
const FIXTURE_NOW = Date.parse(FIXTURE.generatedAt);

function fixtureRepo(name) {
  const repo = FIXTURE.repos.find((r) => r.name === name);
  assert.ok(repo, `fixture has no repo ${name}`);
  return repo;
}

function fixturePipeline(repoName, match) {
  const p = fixtureRepo(repoName).pipelines.find(match);
  assert.ok(p, `fixture repo ${repoName} has no matching pipeline`);
  return p;
}

function fixtureBlock(repoName, match, state) {
  const repo = fixtureRepo(repoName);
  const p = fixturePipeline(repoName, match);
  const s = Object.assign({ collapsed: false, closed: {}, now: FIXTURE_NOW, tz: 'UTC' }, state);
  return render.pipelineBlock(fakeDoc(), view, repo, p, s);
}

function tilesOf(block) {
  return oneByClass(block, 'step-detail').children;
}

function tileByName(block, name) {
  return tilesOf(block).find((t) => t.attrs['data-section'] === name);
}

function stepOf(p, name) {
  return p.steps.find((s) => s.name === name);
}

const SHIP = (p) => p.kind === 'ship';
const PLAN = (p) => p.kind === 'plan';
const EXECUTE = (p) => p.kind === 'execute';
const REVIEW = (p) => p.kind === 'review';

describe('render tiles from the shared fixture', () => {
  // One check for each item of the fixture content table: each gives at least one node.
  const items = [
    ['repos: a failed run in one repo', () =>
      FIXTURE.repos.map((r) => r.pipelines.filter((p) => p.status === 'failed')).flat().map((p) =>
        oneByClass(render.blockHead(fakeDoc(), view, fixtureRepo('identity-service'), p, false, 0), 'pipe-status'))
        .filter((n) => n.textContent === 'failed')],
    ['ship: nested execute waves', () => byClass(tileByName(fixtureBlock('sdlc-plugin', SHIP), 'execute'), 'wave-block')],
    ['ship: review totals with unaccounted', () =>
      byClass(tileByName(fixtureBlock('sdlc-plugin', SHIP), 'review'), 'round-sum').filter((n) => /1 unaccounted/.test(n.textContent))],
    ['ship: plan station', () => byClass(tileByName(fixtureBlock('sdlc-plugin', SHIP), 'plan'), 'wave-block')],
    ['plan: explorers', () => byClass(tileByName(fixtureBlock('sdlc-plugin', PLAN), 'explore'), 'wave-block')],
    ['plan: rounds', () => byClass(tileByName(fixtureBlock('sdlc-plugin', PLAN), 'review'), 'round-n')],
    ['plan: setup guardrails line', () => byClass(tileByName(fixtureBlock('sdlc-plugin', PLAN), 'setup'), 'generic-line')],
    ['plan: review totals, repair limit flag, and answered findings', () => {
      const t = tileByName(fixtureBlock('payments-service', PLAN), 'review');
      return [...byClass(t, 'round-sum'), ...byClass(t, 'lens-chips'), ...byClass(t, 'find-row')];
    }],
    ['ship: commit result line', () => byClass(tileByName(fixtureBlock('identity-service', SHIP), 'commit'), 'generic-line')],
    ['standalone execute: waves', () =>
      tilesOf(fixtureBlock('payments-service', EXECUTE)).filter((t) => byClass(t, 'wave-block').length > 0)],
    ['standalone execute: queued tasks', () => byClass(tileByName(fixtureBlock('payments-service', EXECUTE), 'queued'), 'task-row')],
    ['standalone review: findings tile with a finding', () =>
      byClass(tileByName(fixtureBlock('payments-service', REVIEW), 'correctness'), 'dim-row')],
    ['standalone review: findings tile with none', () =>
      byClass(tileByName(fixtureBlock('payments-service', REVIEW), 'security'), 'generic-line')],
    ['stalled run: stalled issue', () =>
      byClass(tileByName(fixtureBlock('payments-service', EXECUTE), 'issues'), 'issue-path').filter((n) => /^last update /.test(n.textContent))],
    ['issues: one chip for each severity', () =>
      byClass(tileByName(fixtureBlock('sdlc-plugin', SHIP), 'issues'), 'sev')],
    ['harden step: track station only', () =>
      byClass(fixtureBlock('sdlc-plugin', SHIP), 'station').filter((n) => n.attrs.title === 'harden, in progress')],
    ['activity: session by sessionId', () => [tileByName(fixtureBlock('sdlc-plugin', SHIP), 'session')].filter(Boolean)],
    ['activity: session by branch fallback', () => [tileByName(fixtureBlock('payments-service', EXECUTE), 'session')].filter(Boolean)],
    ['history: rows in two repos', () =>
      findAll(render.historyTable(fakeDoc(), view, FIXTURE.repos, new Set(), FIXTURE_NOW, 'UTC'), (n) => n.tagName === 'tr')],
  ];
  for (const [name, nodes] of items) {
    test(`fixture item gives a node: ${name}`, () => {
      assert.ok(nodes().length >= 1);
    });
  }

  test('every pipeline: the details count is the number of tiles, each a details element with data-section', () => {
    for (const repo of FIXTURE.repos) {
      for (const p of repo.pipelines) {
        const block = render.pipelineBlock(fakeDoc(), view, repo, p, { collapsed: false, closed: {}, now: FIXTURE_NOW, tz: 'UTC' });
        const tiles = tilesOf(block);
        assert.equal(oneByClass(block, 'fold-count').textContent, String(tiles.length), p.id);
        for (const t of tiles) {
          assert.equal(t.tagName, 'details');
          assert.ok(t.attrs['data-section']);
          // data-section sits on the tile only, never on a child.
          assert.equal(findAll(t, (n) => n !== t && 'data-section' in n.attrs).length, 0);
        }
      }
    }
  });

  test('every node: no link, no href, no style, no inline handler', () => {
    const all = [];
    for (const repo of FIXTURE.repos) {
      for (const p of repo.pipelines) {
        all.push(...findAll(render.pipelineBlock(fakeDoc(), view, repo, p, { now: FIXTURE_NOW, tz: 'UTC' }), () => true));
      }
    }
    all.push(...findAll(render.historyTable(fakeDoc(), view, FIXTURE.repos, new Set(), FIXTURE_NOW, 'UTC'), () => true));
    assert.equal(all.filter((n) => n.tagName === 'a').length, 0);
    for (const n of all) {
      for (const k of Object.keys(n.attrs)) {
        assert.ok(k !== 'href' && k !== 'style' && !/^on/.test(k), `attribute ${k} on ${n.tagName}`);
      }
    }
  });

  test('tiles follow track order, then issues, then session; skipped and harden steps give no tile', () => {
    const block = fixtureBlock('sdlc-plugin', SHIP);
    assert.deepEqual(tilesOf(block).map((t) => t.attrs['data-section']), ['plan', 'execute', 'review', 'received-review', 'issues', 'session']);
  });

  test('a ship execute tile: three waves, committed with the short sha, a task with no name shows its id only', () => {
    const p = fixturePipeline('sdlc-plugin', SHIP);
    const t = tileByName(fixtureBlock('sdlc-plugin', SHIP), 'execute');
    const waves = byClass(t, 'wave-block');
    assert.equal(waves.length, 3);
    assert.ok(classesOf(t).includes('wide'));
    const first = stepOf(p, 'execute').detail.waves[0];
    const badge = oneByClass(waves[0], 'commit-badge');
    assert.equal(badge.className, 'commit-badge committed');
    assert.equal(badge.textContent, 'committed ' + first.committedSha.slice(0, 7));
    assert.equal(badge.attrs.title, 'Wave commit ' + first.committedSha);
    assert.equal(waves[0].children[0].textContent, 'wave 1');
    assert.equal(oneByClass(waves[0], 'wave-count').textContent, '3/3');
    const noName = byClass(waves[2], 'task-row').find((r) => oneByClass(r, 'task-id').textContent === 'T8');
    assert.equal(byClass(noName, 'task-name').length, 0);
    assert.equal(textOf(noName), 'T8');
    assert.equal(oneByClass(noName, 'lamp').className, 'lamp completed');
  });

  test('a review dimensions tile: totals line above the rows, an amber count above 0', () => {
    const p = fixturePipeline('sdlc-plugin', SHIP);
    const t = tileByName(fixtureBlock('sdlc-plugin', SHIP), 'review');
    const body = t.children[1];
    assert.equal(body.children[0].className, 'round-sum');
    assert.equal(body.children[0].textContent, view.reviewTotalsText(stepOf(p, 'review').detail.reviewTotals));
    const rows = byClass(t, 'dim-cols');
    assert.equal(rows.length, 5);
    assert.equal(byClass(t, 'dim-find').length, 5);
    assert.equal(oneByClass(rows[0], 'dim-name').textContent, 'security');
    assert.equal(oneByClass(rows[0], 'dim-meta').className, 'dim-meta has-findings');
    assert.equal(oneByClass(rows[0], 'dim-meta').textContent, '2 findings');
    assert.equal(oneByClass(rows[2], 'dim-meta').className, 'dim-meta');
    assert.equal(oneByClass(rows[2], 'lamp').className, 'lamp completed');
  });

  test('a running dimension reads running, a pending one queued', () => {
    const body = render.dimensionsBody(fakeDoc(), view, { kind: 'dimensions', dimensions: [
      { name: 'a', status: 'in_progress', findings: 0 },
      { name: 'b', status: 'pending', findings: 0 },
      { name: 'c', status: 'failed', findings: 0 },
    ] });
    assert.deepEqual(byClass(body, 'dim-meta').map((n) => n.textContent), ['running', 'queued', '0 findings']);
    assert.deepEqual(byClass(body, 'lamp').map((n) => n.className), ['lamp running', 'lamp stalled', 'lamp failed']);
    assert.equal(byClass(body, 'round-sum').length, 0);
  });

  test('a skipped dimension reads skipped with its reason, or skipped alone', () => {
    const body = render.dimensionsBody(fakeDoc(), view, { kind: 'dimensions', dimensions: [
      { name: 'a', status: 'skipped', reason: 'stalled', findings: 0 },
      { name: 'b', status: 'skipped', reason: 'unstopped', findings: 0 },
      { name: 'c', status: 'skipped', findings: 0 },
    ] });
    assert.deepEqual(byClass(body, 'dim-meta').map((n) => n.textContent), ['skipped · stalled', 'skipped · unstopped', 'skipped']);
    assert.deepEqual(byClass(body, 'dim-meta').map((n) => n.className), ['dim-meta', 'dim-meta', 'dim-meta']);
  });

  test('dimensions with a wave sit under one Wave heading for each wave, lowest wave first', () => {
    const body = render.dimensionsBody(fakeDoc(), view, { kind: 'dimensions', dimensions: [
      { name: 'docs-review', status: 'pending', findings: 0, wave: 2 },
      { name: 'security-review', status: 'completed', findings: 2, wave: 1 },
      { name: 'perf-review', status: 'skipped', reason: 'stalled', findings: 0, wave: 1 },
      { name: 'style-review', status: 'pending', findings: 0, wave: 3 },
    ] });
    const heads = byClass(body, 'wave-head');
    assert.deepEqual(heads.map((n) => n.textContent), ['Wave 1', 'Wave 2', 'Wave 3']);
    // Document order: each heading is followed by the rows of its wave.
    const sequence = body.children.map((n) => (classesOf(n).includes('wave-head') ? n.textContent : oneByClass(n, 'dim-name').textContent));
    assert.deepEqual(sequence, [
      'Wave 1', 'security-review', 'perf-review',
      'Wave 2', 'docs-review',
      'Wave 3', 'style-review',
    ]);
    const metas = byClass(body, 'dim-meta').map((n) => n.textContent);
    assert.deepEqual(metas, ['2 findings', 'skipped · stalled', 'queued', 'queued']);
  });

  test('dimensions without a wave render as a flat list with no heading', () => {
    const body = render.dimensionsBody(fakeDoc(), view, { kind: 'dimensions', dimensions: [
      { name: 'a', status: 'completed', findings: 1 },
      { name: 'b', status: 'pending', findings: 0, wave: 0 },
    ] });
    assert.equal(byClass(body, 'wave-head').length, 0);
    assert.deepEqual(body.children.map((n) => oneByClass(n, 'dim-name').textContent), ['a', 'b']);
  });

  test('rows without a wave come first, before the first Wave heading', () => {
    const body = render.dimensionsBody(fakeDoc(), view, { kind: 'dimensions', dimensions: [
      { name: 'planned', status: 'completed', findings: 0, wave: 1 },
      { name: 'loose', status: 'completed', findings: 0 },
    ] });
    const sequence = body.children.map((n) => (classesOf(n).includes('wave-head') ? n.textContent : oneByClass(n, 'dim-name').textContent));
    assert.deepEqual(sequence, ['loose', 'Wave 1', 'planned']);
  });

  test('a review plan adds the totals line under the review totals line', () => {
    const reviewTotals = { found: 2, fixed: 1, deferred: 0, unaccounted: 1 };
    const reviewPlan = { wavesPlanned: 3, wavesRun: 1, dimensionsPlanned: 23, dimensionsRun: 8, neverStarted: 15 };
    const body = render.dimensionsBody(fakeDoc(), view, { kind: 'dimensions', reviewTotals, reviewPlan, dimensions: [
      { name: 'a', status: 'completed', findings: 2, wave: 1 },
    ] });
    const sums = byClass(body, 'round-sum');
    assert.deepEqual(sums.map((n) => n.textContent), [
      '2 findings · 1 fixed · 0 deferred · 1 unaccounted',
      'waves 1/3 run · dimensions 8/23 run · 15 never started',
    ]);
    assert.deepEqual(body.children.slice(0, 2), sums);
  });

  test('a review plan without review totals still shows the totals line first', () => {
    const reviewPlan = { wavesPlanned: 1, wavesRun: 1, dimensionsPlanned: 1, dimensionsRun: 1, neverStarted: 0 };
    const body = render.dimensionsBody(fakeDoc(), view, { kind: 'dimensions', reviewPlan, dimensions: [] });
    assert.equal(body.children.length, 1);
    assert.equal(body.children[0].textContent, 'waves 1/1 run · dimensions 1/1 run · 0 never started');
  });

  test('a plan explorers tile: name, N findings, 2 findings and N more; an unreadable explorer has no rows', () => {
    const t = tileByName(fixtureBlock('sdlc-plugin', PLAN), 'explore');
    assert.ok(classesOf(t).includes('wide'));
    const blocks = byClass(t, 'wave-block');
    const big = blocks.find((b) => textOf(b.children[0]).startsWith('pipeline-state-progress-model'));
    assert.equal(oneByClass(big, 'count-badge').textContent, '23 findings');
    assert.equal(byClass(big, 'find-row').length, 2);
    assert.equal(oneByClass(big, 'find-more').textContent, '21 more');
    const unreadable = blocks.find((b) => textOf(b.children[0]).startsWith('telemetry'));
    assert.equal(oneByClass(unreadable, 'lamp').className, 'lamp failed');
    assert.equal(byClass(unreadable, 'find-row').length, 0);
    assert.equal(byClass(unreadable, 'find-more').length, 0);
  });

  test('a plan rounds tile with no roundTotals: no totals line, none and – when a round found 0, one chip for each lens', () => {
    const t = tileByName(fixtureBlock('sdlc-plugin', PLAN), 'review');
    assert.ok(classesOf(t).includes('wide'));
    assert.equal(byClass(t, 'round-sum').length, 0);
    assert.equal(byClass(t, 'find-row').length, 0);
    assert.equal(byClass(t, 'lens-chips').filter((n) => textOf(n) === 'REPAIR LIMIT REACHED').length, 0);
    const rows = byClass(t, 'round-row').filter((r) => !classesOf(r).includes('head'));
    assert.equal(rows.length, 5);
    assert.deepEqual(rows[0].children.slice(0, 3).map((c) => c.textContent), ['round 1', '8', '8']);
    assert.deepEqual(rows[4].children.slice(0, 3).map((c) => c.textContent), ['round 5', 'none', '–']);
    const chips = byClass(rows[1], 'lens-chip');
    assert.deepEqual(chips.map((c) => c.className), ['lens-chip issues', 'lens-chip ok', 'lens-chip issues']);
    assert.equal(chips[1].textContent, 'requirements · approved');
    assert.equal(chips[0].textContent, 'architecture · issues');
  });

  test('a plan review tile with roundTotals: totals line, repair limit flag, and the answered finding', () => {
    const p = fixturePipeline('payments-service', PLAN);
    const detail = stepOf(p, 'review').detail;
    const t = tileByName(fixtureBlock('payments-service', PLAN), 'review');
    assert.equal(textOf(oneByClass(t, 'round-sum')), '5 iterations · 7 violations · 6 fixes');
    assert.deepEqual(byClass(t, 'lens-chip').filter((c) => c.className === 'lens-chip issues').map((c) => c.textContent).filter((x) => x === 'REPAIR LIMIT REACHED'), ['REPAIR LIMIT REACHED']);
    const outcome = detail.outcomes[0];
    const rows = byClass(t, 'find-row');
    assert.equal(rows.length, 1);
    assert.equal(textOf(rows[0]), `${outcome.choice} · ${outcome.id} · ${outcome.text} — ${outcome.reason}`);
  });

  test('a plan setup tile: one guardrails line; a ship commit tile: one result line', () => {
    const setup = tileByName(fixtureBlock('sdlc-plugin', PLAN), 'setup');
    assert.equal(oneByClass(setup, 'sec-meta').textContent, '3 guardrails');
    assert.equal(textOf(oneByClass(setup, 'generic-line')), '3 guardrails loaded (2 error, 1 warning)');
    assert.ok(!classesOf(setup).includes('wide'));
    const commit = tileByName(fixtureBlock('identity-service', SHIP), 'commit');
    const result = stepOf(fixturePipeline('identity-service', SHIP), 'commit').detail.result;
    assert.equal(oneByClass(commit, 'sec-meta').textContent, 'nothing to commit');
    assert.equal(textOf(oneByClass(commit, 'generic-line')), result);
    assert.ok(!classesOf(commit).includes('wide'));
  });

  test('a standalone execute: commits off on every wave, queued shows its tasks', () => {
    const block = fixtureBlock('payments-service', EXECUTE);
    const badges = byClass(oneByClass(block, 'step-detail'), 'commit-badge');
    assert.equal(badges.length, 3);
    for (const b of badges) assert.equal(b.textContent, 'commits off');
    const queued = tileByName(block, 'queued');
    assert.equal(textOf(oneByClass(queued, 'wave-head')), 'queued2');
    assert.deepEqual(byClass(queued, 'task-id').map((n) => n.textContent), ['T7', 'T8']);
    assert.ok(classesOf(queued).includes('wide'));
  });

  test('the stalled issue shows the age of the pipeline updatedAt through relativeWhen', () => {
    const p = fixturePipeline('payments-service', EXECUTE);
    const issues = tileByName(fixtureBlock('payments-service', EXECUTE), 'issues');
    const row = byClass(issues, 'issue-row').find((r) => oneByClass(r, 'issue-text').textContent === 'no update for 30+ min');
    const want = 'last update ' + view.relativeWhen(p.updatedAt, FIXTURE_NOW, 'UTC');
    assert.equal(oneByClass(row, 'issue-path').textContent, want);
    assert.equal(want, 'last update 1h ago');
  });

  test('a standalone review: No findings. on an empty tile, a row for a finding, no tile for a running step', () => {
    const block = fixtureBlock('payments-service', REVIEW);
    assert.deepEqual(tilesOf(block).map((t) => t.attrs['data-section']), ['security', 'correctness', 'issues']);
    assert.equal(oneByClass(tileByName(block, 'security'), 'generic-line').textContent, 'No findings.');
    const row = oneByClass(tileByName(block, 'correctness'), 'dim-row');
    assert.equal(oneByClass(row, 'lamp').className, 'lamp running');
    assert.equal(oneByClass(row, 'dim-meta').textContent, 'medium');
    assert.equal(oneByClass(row, 'dim-name').attrs.title, 'internal/payout/batch.go:88');
  });

  test('the session tile: closed, counts in the summary, short id and timeline rows', () => {
    const repo = fixtureRepo('sdlc-plugin');
    const session = repo.sessions[0];
    const t = tileByName(fixtureBlock('sdlc-plugin', SHIP), 'session');
    assert.equal(t.open, false);
    assert.ok(classesOf(t).includes('span2'));
    assert.equal(oneByClass(t, 'sec-meta').textContent, '3 prompts · 3 commands · 2 mcp calls');
    assert.equal(oneByClass(t, 'sec-id').textContent, 'id ' + view.shortId(session.id));
    const rows = byClass(t, 'timeline-row');
    assert.equal(rows.length, session.timeline.length);
    assert.deepEqual(rows[0].children.map((c) => c.textContent), [
      view.clockLabel(session.timeline[0].at, 'UTC'),
      session.timeline[0].kind,
      session.timeline[0].text,
    ]);
  });

  test('no issues tile and no session tile when there is nothing to show', () => {
    const plan = fixtureBlock('sdlc-plugin', PLAN);
    assert.equal(tileByName(plan, 'issues'), undefined);
    const review = fixtureBlock('payments-service', REVIEW);
    assert.equal(tileByName(review, 'session'), undefined);
    assert.equal(render.issuesTile(fakeDoc(), view, pipeline({ issues: [] }), true, FIXTURE_NOW, 'UTC'), null);
    assert.equal(render.issuesTile(fakeDoc(), view, pipeline({ issues: undefined }), true, FIXTURE_NOW, 'UTC'), null);
    assert.equal(render.sessionTile(fakeDoc(), view, null, false, 'UTC'), null);
  });
});

describe('render stepTile and stepTiles', () => {
  const step = { name: 'review', status: 'in_progress', detail: { kind: 'dimensions', dimensions: [{ name: 'a', status: 'completed', findings: 0 }] } };

  test('summary holds chevron, lamp, step name, and sectionMeta in that order', () => {
    const t = render.stepTile(fakeDoc(), view, pipeline(), step, 2, true, false);
    assert.equal(t.tagName, 'details');
    assert.equal(t.attrs['data-section'], 'review');
    assert.equal(t.open, true);
    const summary = t.children[0];
    assert.equal(summary.tagName, 'summary');
    assert.deepEqual(summary.children.map((c) => c.className), ['chev', 'lamp running', 'sec-name', 'sec-meta']);
    assert.equal(summary.children[0].attrs['aria-hidden'], 'true');
    assert.equal(summary.children[1].attrs['aria-hidden'], 'true');
    assert.equal(summary.children[2].textContent, 'review');
    assert.equal(summary.children[3].textContent, view.sectionMeta(step));
  });

  test('a wide section gets the wide class; an execute tile is wide with 0, 1, or 2 waves; the selected step is marked', () => {
    const none = { name: 'execute', status: 'completed', detail: { kind: 'waves', waves: [] } };
    const one = { name: 'execute', status: 'completed', detail: { kind: 'waves', waves: [{ number: 1, tasks: [] }] } };
    const two = { name: 'execute', status: 'completed', detail: { kind: 'waves', waves: [{ number: 1, tasks: [] }, { number: 2, tasks: [] }] } };
    assert.equal(render.stepTile(fakeDoc(), view, pipeline(), none, 0, true, false).className, 'step-sec wide');
    assert.equal(render.stepTile(fakeDoc(), view, pipeline(), one, 0, true, false).className, 'step-sec wide');
    assert.equal(render.stepTile(fakeDoc(), view, pipeline(), two, 0, true, true).className, 'step-sec wide selected');
    const dims = { name: 'review', status: 'completed', detail: { kind: 'dimensions', dimensions: [] } };
    assert.equal(render.stepTile(fakeDoc(), view, pipeline(), dims, 0, true, false).className, 'step-sec wide');
    const rounds = { name: 'review', status: 'completed', detail: { kind: 'rounds', rounds: [], maxRounds: 5 } };
    assert.equal(render.stepTile(fakeDoc(), view, pipeline(), rounds, 0, true, false).className, 'step-sec wide');
  });

  test('every detail kind has a body builder', () => {
    assert.deepEqual(Object.keys(render.TILE_BODIES).sort(), [
      'dimensions', 'explorers', 'findings', 'guardrails', 'result', 'rounds', 'waves',
    ]);
  });

  test('a tile the user closed stays closed; the others stay open', () => {
    const p = pipeline();
    const key = view.pipelineKey(REPO, p);
    const closed = {};
    closed[view.sectionKey(key, 'review')] = true;
    closed[view.sectionKey(key, 'execute')] = false;
    const tiles = render.stepTiles(fakeDoc(), view, p, { key: key, closed: closed, selected: 2 });
    assert.deepEqual(tiles.map((t) => [t.attrs['data-section'], t.open]), [['execute', true], ['review', false]]);
    assert.ok(classesOf(tiles[1]).includes('selected'));
    assert.ok(!classesOf(tiles[0]).includes('selected'));
  });

  test('pipelineBlock: issues open and session closed by default; the user choice wins over both', () => {
    const repo = Object.assign({}, REPO, { sessions: [{ id: 's1', branch: 'feat/x', lastSeen: '2026-01-01T00:00:00Z', counts: {}, timeline: [] }] });
    const p = pipeline({ issues: [{ source: 'step', severity: 'high', text: 'x', file: '', line: '', ref: 'pr' }] });
    const key = view.pipelineKey(repo, p);
    const plain = render.pipelineBlock(fakeDoc(), view, repo, p, { closed: {} });
    assert.equal(tileByName(plain, 'issues').open, true);
    assert.equal(tileByName(plain, 'session').open, false);
    const closed = {};
    closed[view.sectionKey(key, 'issues')] = true;
    closed[view.sectionKey(key, 'session')] = false;
    const chosen = render.pipelineBlock(fakeDoc(), view, repo, p, { closed: closed });
    assert.equal(tileByName(chosen, 'issues').open, false);
    assert.equal(tileByName(chosen, 'session').open, true);
  });

  test('markup in snapshot text stays text in a tile', () => {
    const t = render.stepTile(fakeDoc(), view, pipeline(), {
      name: '<i>s</i>', status: 'completed', detail: { kind: 'findings', findings: [{ text: '<img src=x>', severity: 'low', file: '', line: '' }] },
    }, 0, true, false);
    assert.equal(oneByClass(t, 'sec-name').textContent, '<i>s</i>');
    const text = oneByClass(t, 'dim-name');
    assert.equal(text.textContent, '<img src=x>');
    assert.equal(text.children.length, 0);
  });
});

describe('render tile bodies', () => {
  test('waves: commits off, committed, not committed, and a due wave in amber', () => {
    const detail = { kind: 'waves', waves: [
      { number: 1, committedSha: 'abcdef0123456789abcdef0123456789abcdef01', tasks: [{ id: 'T1', name: 'a', status: 'completed' }] },
      { number: 2, committedSha: '', tasks: [{ id: 'T2', name: 'b', status: 'completed' }] },
      { number: 3, committedSha: '', tasks: [{ id: 'T3', name: 'c', status: 'in_progress' }] },
    ] };
    const on = byClass(render.wavesBody(fakeDoc(), view, detail, { commitWaves: true }), 'commit-badge');
    assert.deepEqual(on.map((b) => [b.className, b.textContent]), [
      ['commit-badge committed', 'committed abcdef0'],
      ['commit-badge pending-commit', 'not committed'],
      ['commit-badge', 'not committed'],
    ]);
    const off = byClass(render.wavesBody(fakeDoc(), view, detail, { commitWaves: false }), 'commit-badge');
    assert.deepEqual(off.map((b) => b.textContent), ['commits off', 'commits off', 'commits off']);
  });

  test('waves: a pending wave is a block with its tasks and no commit badge', () => {
    const detail = { kind: 'waves', waves: [
      { number: 1, status: 'pending', committedSha: '', tasks: [{ id: 'T1', name: 'a', status: 'pending' }, { id: 'T2', name: 'b', status: 'pending' }] },
      { number: 2, status: 'pending', committedSha: '', tasks: [{ id: 'T3', name: 'c', status: 'pending' }] },
    ] };
    for (const commitWaves of [true, false]) {
      const body = render.wavesBody(fakeDoc(), view, detail, { commitWaves });
      const blocks = byClass(body, 'wave-block');
      assert.deepEqual(blocks.map((b) => textOf(oneByClass(b, 'wave-head'))), ['wave 10/2', 'wave 20/1']);
      assert.deepEqual(blocks.map((b) => byClass(b, 'task-id').map((n) => n.textContent)), [['T1', 'T2'], ['T3']]);
      assert.equal(byClass(body, 'commit-badge').length, 0);
    }
  });

  test('waves: a started wave keeps its badge beside a pending wave', () => {
    const detail = { kind: 'waves', waves: [
      { number: 1, status: 'completed', committedSha: '', tasks: [{ id: 'T1', name: 'a', status: 'completed' }] },
      { number: 2, status: 'pending', committedSha: '', tasks: [{ id: 'T2', name: 'b', status: 'pending' }] },
    ] };
    const blocks = byClass(render.wavesBody(fakeDoc(), view, detail, { commitWaves: true }), 'wave-block');
    assert.deepEqual(byClass(blocks[0], 'commit-badge').map((b) => b.textContent), ['not committed']);
    assert.equal(byClass(blocks[1], 'commit-badge').length, 0);
  });

  test('waves: the queued block shows only when queued is not empty', () => {
    const waves = [{ number: 1, status: 'pending', tasks: [{ id: 'T1', name: 'a', status: 'pending' }] }];
    const heads = (queued) => byClass(render.wavesBody(fakeDoc(), view, { kind: 'waves', waves, queued }, {}), 'wave-head').map((h) => textOf(h));
    assert.deepEqual(heads(undefined), ['wave 10/1']);
    assert.deepEqual(heads([]), ['wave 10/1']);
    assert.deepEqual(heads([{ id: 'T9', name: 'z', status: 'pending' }]), ['wave 10/1', 'queued1']);
  });

  test('waves: a task row has a lamp, the id, and the name', () => {
    const body = render.wavesBody(fakeDoc(), view, { kind: 'waves', waves: [{ number: 1, tasks: [{ id: 'T1', name: 'Add x', status: 'failed' }] }] }, {});
    const row = oneByClass(body, 'task-row');
    assert.deepEqual(row.children.map((c) => c.className), ['lamp failed', 'task-id', 'task-name']);
    assert.equal(textOf(row), 'T1Add x');
    assert.equal(oneByClass(row, 'task-name').attrs.title, 'Add x');
  });

  test('explorers: 2 of N findings, N more, and a running explorer lamp', () => {
    const explorer = { name: 'area', status: 'running', total: 7, findings: [
      { summary: 'one', ref: 'a.go:1' }, { summary: 'two', ref: 'b.go:2' }, { summary: 'three', ref: 'c.go:3' },
    ] };
    const body = render.explorersBody(fakeDoc(), view, { kind: 'explorers', explorers: [explorer] });
    assert.deepEqual(byClass(body, 'find-text').map((n) => n.textContent), ['one', 'two']);
    assert.equal(oneByClass(body, 'find-more').textContent, view.explorerMore(explorer, 2) + ' more');
    assert.equal(oneByClass(body, 'find-more').textContent, '5 more');
    assert.equal(oneByClass(body, 'count-badge').textContent, '7 findings');
    assert.equal(oneByClass(body, 'lamp').className, 'lamp running');
  });

  test('explorers: no N more line when every finding shows', () => {
    const body = render.explorersBody(fakeDoc(), view, { kind: 'explorers', explorers: [
      { name: 'area', status: 'done', total: 1, findings: [{ summary: 'one', ref: '' }] },
    ] });
    assert.equal(byClass(body, 'find-more').length, 0);
    assert.equal(byClass(body, 'find-ref').length, 0);
  });

  test('explorers: a URL ref is text, with no a element and no href', () => {
    const url = 'https://pkg.go.dev/net/http#ResponseController';
    const body = render.explorersBody(fakeDoc(), view, { kind: 'explorers', explorers: [
      { name: 'web', status: 'done', total: 1, findings: [{ summary: 'flush', ref: url }] },
    ] });
    const ref = oneByClass(body, 'find-ref');
    assert.equal(ref.tagName, 'span');
    assert.equal(ref.textContent, url);
    assert.equal(findAll(body, (n) => n.tagName === 'a').length, 0);
    assert.equal(findAll(body, (n) => 'href' in n.attrs).length, 0);
  });

  test('findings: No findings. on an empty tile', () => {
    const body = render.findingsBody(fakeDoc(), view, { kind: 'findings' });
    assert.equal(body.tagName, 'p');
    assert.equal(body.className, 'generic-line');
    assert.equal(body.textContent, 'No findings.');
  });

  test('findings: one row for each finding, lamp by severity', () => {
    const body = render.findingsBody(fakeDoc(), view, { kind: 'findings', findings: [
      { text: 'a', severity: 'critical', file: 'x.go', line: '3' },
      { text: 'b', severity: 'medium', file: '', line: '' },
      { text: 'c', severity: 'info', file: '', line: '' },
    ] });
    const rows = byClass(body, 'dim-row');
    assert.equal(rows.length, 3);
    assert.deepEqual(rows.map((r) => oneByClass(r, 'lamp').className), ['lamp failed', 'lamp running', 'lamp stalled']);
    assert.deepEqual(rows.map((r) => textOf(r)), ['acritical', 'bmedium', 'cinfo']);
  });

  test('rounds: no roundTotals gives no totals line, no flag, no outcome rows, and the head row stays', () => {
    const body = render.roundsBody(fakeDoc(), view, { kind: 'rounds', maxRounds: 3 });
    assert.equal(byClass(body, 'round-sum').length, 0);
    assert.equal(byClass(body, 'lens-chips').length, 0);
    assert.equal(byClass(body, 'find-row').length, 0);
    assert.deepEqual(byClass(body, 'round-row').map((r) => r.className), ['round-row head']);
  });

  test('rounds: the totals line shows roundTotals, and the page does not add up found and fixed', () => {
    const detail = {
      kind: 'rounds',
      maxRounds: 5,
      rounds: [{ n: 1, found: 4, fixed: 4, lenses: [] }, { n: 2, found: 3, fixed: 3, lenses: [] }],
      roundTotals: { iterations: 2, violations: 5, fixes: 5, distinct: true },
    };
    const sum = oneByClass(render.roundsBody(fakeDoc(), view, detail), 'round-sum');
    assert.equal(textOf(sum), '2 iterations · 5 violations · 5 fixes');
    assert.deepEqual(sum.children.map((c) => c.tagName), ['strong', 'span', 'strong', 'span', 'strong', 'span']);
  });

  test('rounds: (sum) ends the totals line when distinct is false, and only then', () => {
    const totals = { iterations: 3, violations: 7, fixes: 6, distinct: false };
    const sum = render.roundsBody(fakeDoc(), view, { kind: 'rounds', roundTotals: totals });
    assert.equal(textOf(oneByClass(sum, 'round-sum')), '3 iterations · 7 violations · 6 fixes (sum)');
    const distinct = render.roundsBody(fakeDoc(), view, { kind: 'rounds', roundTotals: Object.assign({}, totals, { distinct: true }) });
    assert.equal(textOf(oneByClass(distinct, 'round-sum')), '3 iterations · 7 violations · 6 fixes');
  });

  test('rounds: a count of 1 reads in the singular', () => {
    const body = render.roundsBody(fakeDoc(), view, { kind: 'rounds', roundTotals: { iterations: 1, violations: 1, fixes: 1, distinct: true } });
    assert.equal(textOf(oneByClass(body, 'round-sum')), '1 iteration · 1 violation · 1 fix');
    const zero = render.roundsBody(fakeDoc(), view, { kind: 'rounds', roundTotals: { iterations: 0, violations: 0, fixes: 0, distinct: true } });
    assert.equal(textOf(oneByClass(zero, 'round-sum')), '0 iterations · 0 violations · 0 fixes');
  });

  test('rounds: REPAIR LIMIT REACHED shows when repairLimit is true, never when false or absent', () => {
    const flags = (detail) => byClass(render.roundsBody(fakeDoc(), view, detail), 'lens-chip').map((c) => [c.className, c.textContent]);
    assert.deepEqual(flags({ kind: 'rounds', repairLimit: true }), [['lens-chip issues', 'REPAIR LIMIT REACHED']]);
    assert.deepEqual(flags({ kind: 'rounds', repairLimit: false }), []);
    assert.deepEqual(flags({ kind: 'rounds' }), []);
  });

  test('rounds: each outcome is <choice> · <id> · <text> — <reason>; an empty reason drops the dash', () => {
    const body = render.roundsBody(fakeDoc(), view, { kind: 'rounds', outcomes: [
      { id: 'f-9d01aa42', text: 'Missing test for the stop route', choice: 'accepted', reason: 'Covered by Task 19 flow walk' },
      { id: 'f-0b77d2c4', text: 'Wrong path in Task 4', choice: 'rejected', reason: 'the path exists' },
      { id: 'f-11111111', text: 'Open question', choice: 'stop', reason: '' },
    ] });
    const rows = byClass(body, 'find-row');
    assert.deepEqual(rows.map((r) => textOf(r)), [
      'accepted · f-9d01aa42 · Missing test for the stop route — Covered by Task 19 flow walk',
      'rejected · f-0b77d2c4 · Wrong path in Task 4 — the path exists',
      'stop · f-11111111 · Open question',
    ]);
    assert.equal(oneByClass(rows[2], 'find-text').attrs.title, 'stop · f-11111111 · Open question');
  });

  test('rounds: order is totals line, flag, outcome rows, then the round table', () => {
    const body = render.roundsBody(fakeDoc(), view, {
      kind: 'rounds',
      rounds: [{ n: 1, found: 1, fixed: 0, lenses: [] }],
      roundTotals: { iterations: 1, violations: 1, fixes: 0, distinct: true },
      repairLimit: true,
      outcomes: [{ id: 'f-1', text: 't', choice: 'accepted', reason: 'r' }],
    });
    assert.deepEqual(body.children.map((c) => c.className), ['round-sum', 'lens-chips', 'find-row', 'round-row head', 'round-row']);
  });

  test('guardrails: the loaded line, singular for 1, and the empty line for 0 or no counts', () => {
    const line = (detail) => {
      const p = render.guardrailsBody(fakeDoc(), view, detail);
      assert.equal(p.tagName, 'p');
      assert.equal(p.className, 'generic-line');
      return p.textContent;
    };
    assert.equal(line({ kind: 'guardrails', guardrails: { total: 3, error: 2, warning: 1 } }), '3 guardrails loaded (2 error, 1 warning)');
    assert.equal(line({ kind: 'guardrails', guardrails: { total: 1, error: 1, warning: 0 } }), '1 guardrail loaded (1 error, 0 warning)');
    assert.equal(line({ kind: 'guardrails', guardrails: { total: 0, error: 0, warning: 0 } }), 'No plan guardrails configured');
    assert.equal(line({ kind: 'guardrails' }), 'No plan guardrails configured');
  });

  test('result: one text line, markup stays text, empty when the result is absent', () => {
    const p = render.resultBody(fakeDoc(), view, { kind: 'result', result: 'nothing to commit: <b>x</b>' });
    assert.equal(p.tagName, 'p');
    assert.equal(p.className, 'generic-line');
    assert.equal(p.textContent, 'nothing to commit: <b>x</b>');
    assert.equal(p.children.length, 0);
    assert.equal(render.resultBody(fakeDoc(), view, { kind: 'result' }).textContent, '');
  });
});

describe('render issuesTile', () => {
  const issues = [
    { source: 'review', severity: 'critical', text: 'a', file: 'x.go', line: '1', ref: 'security' },
    { source: 'review', severity: 'high', text: 'b', file: 'x.go', line: '', ref: 'security' },
    { source: 'review', severity: 'medium', text: 'c', file: '', line: '', ref: 'docs' },
    { source: 'review', severity: 'low', text: 'd', file: '', line: '', ref: '' },
    { source: 'state', severity: 'info', text: 'e', file: '', line: '', ref: '' },
  ];

  test('a span2 tile, open, with N open and a red lamp when a critical or high issue exists', () => {
    const t = render.issuesTile(fakeDoc(), view, pipeline({ issues: issues }), true, FIXTURE_NOW, 'UTC');
    assert.equal(t.className, 'step-sec span2');
    assert.equal(t.attrs['data-section'], 'issues');
    assert.equal(t.open, true);
    assert.equal(oneByClass(t, 'sec-meta').textContent, '5 open');
    assert.equal(oneByClass(t, 'lamp').className, 'lamp failed');
  });

  test('an amber lamp when no issue is critical or high', () => {
    const t = render.issuesTile(fakeDoc(), view, pipeline({ issues: issues.slice(2) }), true, FIXTURE_NOW, 'UTC');
    assert.equal(oneByClass(t, 'lamp').className, 'lamp running');
  });

  test('severity chips first, rationale, then the location under it', () => {
    const t = render.issuesTile(fakeDoc(), view, pipeline({ issues: issues }), true, FIXTURE_NOW, 'UTC');
    const rows = byClass(t, 'issue-row');
    assert.deepEqual(rows.map((r) => r.children[0].className), [
      'sev sev-critical', 'sev sev-high', 'sev sev-medium', 'sev sev-low', 'sev sev-info',
    ]);
    assert.deepEqual(rows.map((r) => r.children[0].textContent), ['critical', 'high', 'medium', 'low', 'info']);
    assert.deepEqual(rows.map((r) => byClass(r, 'issue-path').map((n) => n.textContent)), [
      ['x.go:1'], ['x.go'], ['docs'], [], [],
    ]);
    assert.equal(oneByClass(rows[0], 'issue-text').textContent, 'a');
  });
});

// --- render.js: rows that open the detail viewer, command groups, Archive, viewer body -----

// The structure of a fake node without its methods, so two nodes can be compared.
function shape(node) {
  return { tag: node.tagName, cls: node.className, attrs: node.attrs, text: node.textContent, children: node.children.map(shape) };
}

function detailButtons(node) {
  return findAll(node, (n) => 'data-detail' in n.attrs);
}

describe('render findingRow', () => {
  const finding = { text: 'Missing check', severity: 'high', file: 'a.go', line: '7' };

  test('a button of type button: lamp by severity, text with the location as title, severity', () => {
    const row = render.findingRow(fakeDoc(), view, finding, 'finding:p1:security:0');
    assert.equal(row.tagName, 'button');
    assert.equal(row.attrs.type, 'button');
    assert.equal(row.attrs['data-detail'], 'finding:p1:security:0');
    assert.deepEqual(classesOf(row), ['dim-row', 'dim-find']);
    assert.deepEqual(row.children.map((c) => c.className), ['lamp failed', 'dim-name', 'dim-meta']);
    assert.equal(oneByClass(row, 'dim-name').attrs.title, 'a.go:7');
    assert.equal(textOf(row), 'Missing checkhigh');
  });

  test('an empty key leaves out data-detail', () => {
    assert.equal(render.findingRow(fakeDoc(), view, finding, '').attrs['data-detail'], undefined);
  });

  test('markup in a finding stays text', () => {
    const row = render.findingRow(fakeDoc(), view, { text: '<img src=x>', severity: 'low', file: '', line: '' }, 'k');
    const text = oneByClass(row, 'dim-name');
    assert.equal(text.textContent, '<img src=x>');
    assert.equal(text.children.length, 0);
  });
});

describe('render dimensionsBody finding rows', () => {
  const sec = [
    { text: 'path join', severity: 'critical', file: 'server.go', line: '142' },
    { text: 'no origin check', severity: 'high', file: 'server.go', line: '201-208' },
  ];
  const detail = {
    kind: 'dimensions',
    dimensions: [
      { name: 'security', status: 'completed', findings: 2, wave: 1, findingItems: sec },
      { name: 'docs', status: 'completed', findings: 0, wave: 1, findingItems: [] },
      { name: 'tests', status: 'completed', findings: 1, findingItems: [{ text: 'gap', severity: 'low', file: '', line: '' }] },
    ],
  };
  const P = { id: 'ship-9' };

  test('each dimension row is followed by its finding rows in one div.dim-findings; none for a dimension with no finding', () => {
    const body = render.dimensionsBody(fakeDoc(), view, detail, P);
    // Flat rows first (tests), then Wave 1 (security, docs).
    assert.deepEqual(body.children.map((n) => n.className), [
      'dim-row dim-cols', 'dim-findings',
      'wave-head', 'dim-row dim-cols', 'dim-findings', 'dim-row dim-cols',
    ]);
    assert.equal(byClass(body, 'dim-cols').length, 3);
    assert.equal(byClass(body, 'dim-find').length, 3);
    assert.equal(byClass(body.children[4], 'dim-find').length, 2);
  });

  test('a finding row key is the pipeline id, the dimension name, and the index in findingItems', () => {
    const body = render.dimensionsBody(fakeDoc(), view, detail, P);
    assert.deepEqual(byClass(body, 'dim-find').map((r) => r.attrs['data-detail']), [
      'finding:ship-9:tests:0', 'finding:ship-9:security:0', 'finding:ship-9:security:1',
    ]);
  });

  test('a finding row is the same row findingsBody builds for the same finding', () => {
    const fromDims = byClass(render.dimensionsBody(fakeDoc(), view, detail, P), 'dim-find').slice(1);
    const fromFindings = byClass(render.findingsBody(fakeDoc(), view, { kind: 'findings', findings: sec }, P, { name: 'security' }), 'dim-find');
    assert.equal(fromFindings.length, 2);
    assert.deepEqual(fromDims.map(shape), fromFindings.map(shape));
  });

  test('without a pipeline the rows still render and carry no data-detail', () => {
    const body = render.dimensionsBody(fakeDoc(), view, detail);
    const rows = byClass(body, 'dim-find');
    assert.equal(rows.length, 3);
    for (const row of rows) assert.equal(row.attrs['data-detail'], undefined);
  });

  test('findingsBody keys use the step name as the dimension', () => {
    const body = render.findingsBody(fakeDoc(), view, { kind: 'findings', findings: sec }, P, { name: 'correctness' });
    assert.deepEqual(byClass(body, 'dim-find').map((r) => r.attrs['data-detail']), [
      'finding:ship-9:correctness:0', 'finding:ship-9:correctness:1',
    ]);
  });

  test('stepTile gives the pipeline and the step to the body builder', () => {
    const p = pipeline({ id: 'ship-3' });
    const review = { name: 'review', status: 'completed', detail: { kind: 'dimensions', dimensions: detail.dimensions } };
    const dims = render.stepTile(fakeDoc(), view, p, review, 2, true, false);
    assert.equal(byClass(dims, 'dim-find')[0].attrs['data-detail'], 'finding:ship-3:tests:0');
    const standalone = { name: 'security', status: 'completed', detail: { kind: 'findings', findings: sec } };
    const tile = render.stepTile(fakeDoc(), view, p, standalone, 0, true, false);
    assert.equal(byClass(tile, 'dim-find')[1].attrs['data-detail'], 'finding:ship-3:security:1');
  });
});

describe('render issue rows', () => {
  const issues = [
    { source: 'review', severity: 'critical', text: 'a', file: 'x.go', line: '1', ref: 'security' },
    { source: 'state', severity: 'info', text: 'b', file: '', line: '', ref: '' },
  ];

  test('each issue row is a button with the key issue:<pipeline id>:<index>', () => {
    const t = render.issuesTile(fakeDoc(), view, pipeline({ id: 'ship-5', issues }), true, FIXTURE_NOW, 'UTC');
    const rows = byClass(t, 'issue-row');
    assert.deepEqual(rows.map((r) => r.tagName), ['button', 'button']);
    assert.deepEqual(rows.map((r) => r.attrs.type), ['button', 'button']);
    assert.deepEqual(rows.map((r) => r.attrs['data-detail']), ['issue:ship-5:0', 'issue:ship-5:1']);
    assert.equal(rows[0].children[0].className, 'sev sev-critical');
  });
});

describe('render rows and the detail lookup, over the fixture', () => {
  const keys = [];
  const rows = [];
  for (const repo of FIXTURE.repos) {
    for (const p of repo.pipelines) {
      rows.push(...detailButtons(render.pipelineBlock(fakeDoc(), view, repo, p, { now: FIXTURE_NOW, tz: 'UTC' })));
    }
  }
  rows.push(...detailButtons(render.activityPanel(fakeDoc(), view, FIXTURE.repos, new Set())));
  for (const row of rows) keys.push(row.attrs['data-detail']);

  test('activity, issue, and finding rows are buttons of type button', () => {
    assert.ok(rows.length > 0);
    for (const row of rows) {
      assert.equal(row.tagName, 'button');
      assert.equal(row.attrs.type, 'button');
    }
  });

  test('the rows cover the four item kinds, and every key is different', () => {
    assert.deepEqual([...new Set(keys.map((k) => k.split(':')[0]))].sort(), ['deferred', 'finding', 'issue', 'learning']);
    assert.equal(new Set(keys).size, keys.length);
  });

  test('every key finds its item again in the snapshot', () => {
    for (const key of keys) assert.ok(view.detailItem(FIXTURE, key), key);
  });
});

describe('render Archive button', () => {
  const head = (extra) => render.blockHead(fakeDoc(), view, REPO, pipeline(extra), false, 0);

  test('a completed, failed, or stalled row gets one button with the three data attributes', () => {
    for (const status of ['completed', 'failed', 'stalled']) {
      const button = oneByClass(head({ status }), 'archive-btn');
      assert.equal(button.tagName, 'button');
      assert.equal(button.textContent, '');
      assert.deepEqual(button.attrs, {
        type: 'button',
        'data-archive': 'ship-1',
        'data-repo': '/src/app',
        'data-status': status,
        'aria-label': 'Archive',
        title: 'Archive this run',
      });
    }
  });

  test('a running row gets none, and neither does a row with an unknown status or no id', () => {
    assert.equal(byClass(head({ status: 'running' }), 'archive-btn').length, 0);
    assert.equal(byClass(head({ status: 'constructor' }), 'archive-btn').length, 0);
    assert.equal(byClass(head({ status: 'completed', id: '' }), 'archive-btn').length, 0);
  });

  test('the icon is the first item of the side group, before the repo and far from the details toggle', () => {
    const side = oneByClass(head({ status: 'failed', issues: [{ text: 'a' }] }), 'pipe-side');
    assert.deepEqual(side.children.map((c) => c.className), ['archive-btn', 'pipe-repo', 'issue-chip', 'pipe-status failed', 'fold-btn']);
  });

  test('the button is not a step tile: every child of .step-detail stays a details element', () => {
    const block = render.pipelineBlock(fakeDoc(), view, REPO, pipeline({ status: 'completed' }), { collapsed: true, selected: 0 });
    oneByClass(block, 'archive-btn');
    for (const child of oneByClass(block, 'step-detail').children) assert.equal(child.tagName, 'details');
  });

  test('over the fixture: one button for each completed, failed, or stalled pipeline, none for a running one', () => {
    for (const repo of FIXTURE.repos) {
      for (const p of repo.pipelines) {
        const buttons = byClass(render.pipelineBlock(fakeDoc(), view, repo, p, { now: FIXTURE_NOW, tz: 'UTC' }), 'archive-btn');
        const want = ['completed', 'failed', 'stalled'].includes(p.status) ? 1 : 0;
        assert.equal(buttons.length, want, p.id);
        if (want) {
          assert.equal(buttons[0].attrs['data-archive'], p.id);
          assert.equal(buttons[0].attrs['data-repo'], repo.root);
          assert.equal(buttons[0].attrs['data-status'], p.status);
        }
      }
    }
  });
});

describe('render commandGroupTable', () => {
  const groups = [
    { label: 'go', programs: ['go'], count: 2, share: 0.67, majority: true, lastAt: '2026-10-08T14:21:02Z' },
    { label: 'git + go + task', programs: ['git', 'go', 'task'], count: 1, share: 0.33, majority: false, lastAt: '2026-10-08T11:12:08Z' },
  ];

  test('no table for no groups', () => {
    assert.equal(render.commandGroupTable(fakeDoc(), []), null);
    assert.equal(render.commandGroupTable(fakeDoc(), undefined), null);
  });

  test('a head row, then one row for each group: label, count, share as a percent, and the mark', () => {
    const table = render.commandGroupTable(fakeDoc(), groups);
    assert.equal(table.tagName, 'table');
    assert.equal(table.className, 'cmd-groups');
    assert.deepEqual(findAll(table, (n) => n.tagName === 'th').map((n) => n.textContent), ['command', 'count', 'share', 'majority']);
    const body = findAll(table, (n) => n.tagName === 'tbody')[0];
    assert.deepEqual(body.children.map((r) => r.children.slice(0, 3).map((c) => c.textContent)), [
      ['go', '2', '67%'],
      ['git + go + task', '1', '33%'],
    ]);
  });

  test('only the majority group has the mark: a hidden glyph and the word yes for a screen reader', () => {
    const body = findAll(render.commandGroupTable(fakeDoc(), groups), (n) => n.tagName === 'tbody')[0];
    assert.deepEqual(body.children.map((r) => r.className), ['cg-row majority', 'cg-row']);
    const mark = oneByClass(body.children[0], 'cg-mark');
    assert.equal(oneByClass(mark, 'cg-glyph').textContent, '◆');
    assert.equal(oneByClass(mark, 'cg-glyph').attrs['aria-hidden'], 'true');
    assert.equal(oneByClass(mark, 'sr-only').textContent, 'yes');
    assert.equal(oneByClass(body.children[1], 'cg-mark').children.length, 0);
  });

  test('a share of 1 reads 100%, and markup in a label stays text', () => {
    const table = render.commandGroupTable(fakeDoc(), [{ label: '<b>x</b>', programs: [], count: 3, share: 1, majority: true, lastAt: '' }]);
    assert.equal(oneByClass(table, 'cg-share').textContent, '100%');
    const label = oneByClass(table, 'cg-label');
    assert.equal(label.textContent, '<b>x</b>');
    assert.equal(label.children.length, 0);
  });

  test('the session tile puts the table after the short id and before the timeline rows', () => {
    const session = FIXTURE.repos[0].sessions[0];
    const t = render.sessionTile(fakeDoc(), view, session, true, 'UTC');
    assert.deepEqual(t.children.slice(1, 3).map((c) => c.className), ['sec-id', 'cmd-groups']);
    assert.equal(t.children[3].className, 'timeline-row');
    const labels = byClass(t, 'cg-label').map((n) => n.textContent);
    assert.deepEqual(labels, session.commandGroups.map((g) => g.label));
    assert.equal(byClass(t, 'majority').length, 1);
  });

  test('a session with no command groups has no table', () => {
    const t = render.sessionTile(fakeDoc(), view, { id: 's1', counts: {}, commandGroups: [], timeline: [] }, true, 'UTC');
    assert.equal(byClass(t, 'cmd-groups').length, 0);
    const bare = render.sessionTile(fakeDoc(), view, { id: 's1', counts: {}, timeline: [] }, true, 'UTC');
    assert.equal(byClass(bare, 'cmd-groups').length, 0);
  });
});

describe('render detailBody', () => {
  test('title with tabindex -1, a metadata list of dt and dd, and the text, from a deferred item', () => {
    const item = view.detailItem(FIXTURE, 'deferred:d-12');
    const body = render.detailBody(fakeDoc(), item);
    assert.equal(body.className, 'detail-body');
    assert.deepEqual(body.children.map((c) => c.className), ['detail-heading', 'detail-meta', 'detail-text']);
    const heading = body.children[0];
    assert.equal(heading.tagName, 'h2');
    assert.equal(heading.attrs.tabindex, '-1');
    assert.equal(heading.textContent, item.title);
    const list = body.children[1];
    assert.equal(list.tagName, 'dl');
    assert.deepEqual(list.children.map((c) => c.tagName), item.meta.flatMap(() => ['dt', 'dd']));
    assert.deepEqual(list.children.map((c) => c.textContent), item.meta.flat());
    assert.equal(body.children[2].textContent, item.text);
    assert.equal(body.children[2].textContent, 'shipRunInFlight has no failed terminal case');
  });

  test('a learning item has an empty text element that the page fills later', () => {
    const item = view.detailItem(FIXTURE, 'learning:2026-10-08:ship: harden reads the reasoning of the deferring agent');
    const body = render.detailBody(fakeDoc(), item);
    const text = oneByClass(body, 'detail-text');
    assert.equal(text.textContent, '');
    text.textContent = 'body from the server';
    assert.equal(textOf(oneByClass(body, 'detail-text')), 'body from the server');
  });

  test('an item with no metadata has no dl; a missing item gives an empty title and text', () => {
    const none = render.detailBody(fakeDoc(), { title: 't', text: 'x', meta: [] });
    assert.deepEqual(none.children.map((c) => c.className), ['detail-heading', 'detail-text']);
    const gone = render.detailBody(fakeDoc(), null);
    assert.deepEqual(gone.children.map((c) => [c.className, c.textContent]), [['detail-heading', ''], ['detail-text', '']]);
    assert.equal(gone.children[0].attrs.tabindex, '-1');
  });

  test('markup in the title, the metadata, and the text stays text; no style, link, or handler attribute', () => {
    const body = render.detailBody(fakeDoc(), {
      title: '<b>t</b>',
      text: '<img src=x onerror=alert(1)>\nline two',
      meta: [['<i>k</i>', '<u>v</u>']],
    });
    assert.equal(oneByClass(body, 'detail-heading').textContent, '<b>t</b>');
    assert.equal(oneByClass(body, 'detail-text').textContent, '<img src=x onerror=alert(1)>\nline two');
    assert.deepEqual(byClass(body, 'detail-meta')[0].children.map((c) => c.textContent), ['<i>k</i>', '<u>v</u>']);
    for (const n of findAll(body, (x) => ['h2', 'dt', 'dd'].includes(x.tagName) || classesOf(x).includes('detail-text'))) {
      assert.equal(n.children.length, 0, n.tagName);
    }
    for (const n of findAll(body, () => true)) {
      for (const k of Object.keys(n.attrs)) assert.equal(k, 'tabindex', `attribute ${k} on ${n.tagName}`);
    }
  });
});

describe('render explorersBody with review rounds', () => {
  const explorers = [{ name: 'area', status: 'done', total: 1, findings: [{ summary: 'one', ref: '' }] }];
  const rounds = [
    { n: 1, status: 'Issues Found', found: 3, fixed: 3, lenses: [{ name: 'risk', verdict: 'Issues Found' }] },
    { n: 2, status: 'Approved', found: 0, fixed: 0, lenses: [{ name: 'risk', verdict: 'Approved' }] },
  ];

  test('no rounds, or an empty list, gives the explorer grid alone', () => {
    for (const detail of [{ kind: 'explorers', explorers }, { kind: 'explorers', explorers, rounds: [], maxRounds: 5 }]) {
      const body = render.explorersBody(fakeDoc(), view, detail);
      assert.equal(body.className, 'waves');
      assert.equal(byClass(body, 'round-row').length, 0);
    }
  });

  test('rounds append the rounds table under the grid, with a review rounds heading', () => {
    const body = render.explorersBody(fakeDoc(), view, { kind: 'explorers', explorers, rounds, maxRounds: 5 });
    assert.deepEqual(body.children.map((c) => c.className), ['waves', 'wave-head', '']);
    assert.equal(body.children[1].textContent, 'review rounds');
    const table = body.children[2];
    assert.equal(byClass(body.children[0], 'round-row').length, 0);
    const rows = byClass(table, 'round-row');
    assert.deepEqual(rows.map((r) => r.className), ['round-row head', 'round-row', 'round-row']);
    assert.deepEqual(rows[1].children.slice(0, 3).map((c) => c.textContent), ['round 1', '3', '3']);
    assert.deepEqual(rows[2].children.slice(0, 3).map((c) => c.textContent), ['round 2', 'none', '–']);
  });

  test('the fixture: the ship plan station lists its rounds, the plan explore station does not', () => {
    const ship = tileByName(fixtureBlock('sdlc-plugin', SHIP), 'plan');
    const detail = stepOf(fixturePipeline('sdlc-plugin', SHIP), 'plan').detail;
    assert.equal(detail.maxRounds, 5);
    assert.equal(byClass(ship, 'round-row').filter((r) => !classesOf(r).includes('head')).length, detail.rounds.length);
    assert.ok(classesOf(ship).includes('wide'));
    assert.equal(byClass(tileByName(fixtureBlock('sdlc-plugin', PLAN), 'explore'), 'round-row').length, 0);
  });
});

describe('render execute stations', () => {
  const step = {
    name: 'execute',
    status: 'completed',
    detail: { kind: 'waves', waves: [{ number: 1, status: 'completed', committedSha: '', tasks: [{ id: 'T1', name: 'a', status: 'completed' }] }] },
  };

  test('a ship execute station and a standalone wave station use the same wavesBody', () => {
    assert.equal(render.TILE_BODIES.waves, render.wavesBody);
    const ship = render.stepTile(fakeDoc(), view, pipeline({ kind: 'ship', commitWaves: true }), step, 0, true, false);
    const solo = render.stepTile(fakeDoc(), view, pipeline({ kind: 'execute', commitWaves: true }), step, 0, true, false);
    assert.deepEqual(shape(ship), shape(solo));
    assert.equal(ship.className, 'step-sec wide');
  });

  test('over the fixture: both stations are wide and each wave is a wave-block with a wave head first', () => {
    const tiles = [
      tileByName(fixtureBlock('sdlc-plugin', SHIP), 'execute'),
      tilesOf(fixtureBlock('payments-service', EXECUTE)).find((t) => byClass(t, 'wave-block').length > 0),
    ];
    for (const t of tiles) {
      assert.ok(classesOf(t).includes('wide'));
      for (const block of byClass(t, 'wave-block')) assert.equal(block.children[0].className, 'wave-head');
    }
  });
});

describe('render historyTable', () => {
  const repos = [
    { root: '/a', name: 'a', history: [
      { kind: 'ship', branch: 'feat/a', outcome: 'success', startedAt: '', endedAt: '2026-10-08T14:00:00Z', durationMs: 2460000 },
      { kind: 'plan', branch: 'main', outcome: 'failure', startedAt: '', endedAt: '2026-10-08T10:00:00Z', durationMs: 26000 },
    ] },
    { root: '/b', name: 'b', history: [
      { kind: 'execute', branch: 'fix/b', outcome: 'partial', startedAt: '', endedAt: '2026-10-08T12:00:00Z', durationMs: 3720000 },
    ] },
  ];
  const now = Date.parse('2026-10-08T14:30:00Z');

  test('Finished runs (n), the column heads, and one row of each outcome, newest first', () => {
    const panel = render.historyTable(fakeDoc(), view, repos, new Set(), now, 'UTC');
    assert.equal(panel.className, 'list-panel hist-panel');
    assert.equal(textOf(oneByClass(panel, 'list-title')), 'Finished runs (3)');
    const table = oneByClass(panel, 'hist');
    assert.equal(table.tagName, 'table');
    assert.deepEqual(findAll(table, (n) => n.tagName === 'th').map((n) => n.textContent),
      ['outcome', 'kind', 'branch', 'repo', 'finished', 'duration']);
    const body = findAll(table, (n) => n.tagName === 'tbody')[0];
    const rows = body.children;
    assert.deepEqual(rows.map((r) => oneByClass(r, 'out').className), ['out success', 'out partial', 'out failure']);
    assert.deepEqual(rows.map((r) => textOf(oneByClass(r, 'out'))), ['✓success', '◐partial', '✕failure']);
    assert.equal(oneByClass(rows[0], 'out').children[0].attrs['aria-hidden'], 'true');
    assert.deepEqual(rows[1].children.slice(1).map((c) => c.textContent), [
      'execute', 'fix/b', 'b', view.relativeWhen('2026-10-08T12:00:00Z', now, 'UTC'), view.formatDuration(3720000),
    ]);
    assert.deepEqual(rows[1].children.slice(4).map((c) => c.textContent), ['2h ago', '1h 02m']);
    assert.deepEqual(rows[0].children.map((c) => c.className), ['', 'h-kind', 'h-branch', 'h-repo', 'h-when', 'h-dur']);
  });

  test('the scope keeps rows of the repos in scope only', () => {
    const panel = render.historyTable(fakeDoc(), view, repos, new Set(['/b']), now, 'UTC');
    assert.equal(textOf(oneByClass(panel, 'list-title')), 'Finished runs (1)');
    assert.deepEqual(byClass(panel, 'h-repo').map((n) => n.textContent), ['b']);
  });

  test('no rows shows the no-history empty state and no table', () => {
    const panel = render.historyTable(fakeDoc(), view, repos, new Set(['/c']), now, 'UTC');
    assert.equal(textOf(oneByClass(panel, 'list-title')), 'Finished runs (0)');
    assert.equal(byClass(panel, 'hist').length, 0);
    const line = oneByClass(panel, 'generic-line');
    assert.equal(line.attrs['data-empty'], 'no-history');
    assert.equal(line.textContent, 'No finished runs for the selected repos.');
  });

  test('the fixture history: one row of each outcome over two repos', () => {
    const panel = render.historyTable(fakeDoc(), view, FIXTURE.repos, new Set(), FIXTURE_NOW, 'UTC');
    const outcomes = new Set(byClass(panel, 'out').map((n) => n.className.split(' ')[1]));
    assert.deepEqual([...outcomes].sort(), ['failure', 'partial', 'success']);
    assert.ok(new Set(byClass(panel, 'h-repo').map((n) => n.textContent)).size >= 2);
  });
});

describe('page scripts ship no preview code', () => {
  for (const name of ['render.js', 'app.js']) {
    test(`${name} has no fixture data and no note box`, () => {
      const source = fs.readFileSync(path.join(__dirname, '../static', name), 'utf8');
      for (const word of ['identity-service', 'payments-service', 'scenarios', 'snapshot.fixture', 'note']) {
        assert.ok(!source.includes(word), `${name} contains ${word}`);
      }
    });
  }
});

describe('render.js browser global fallback', () => {
  test('render.js assigns root.sdlcRender when module.exports is unavailable', () => {
    const source = fs.readFileSync(path.join(__dirname, '../static/render.js'), 'utf8');
    const fakeRoot = {};
    // eslint-disable-next-line no-new-func
    const run = new Function('module', source);
    run.call(fakeRoot);
    assert.deepEqual(Object.keys(fakeRoot.sdlcRender).sort(), [
      'TILE_BODIES',
      'activityPanel',
      'attentionRows',
      'blockHead',
      'commandGroupTable',
      'detailBody',
      'dimensionsBody',
      'el',
      'elapsedText',
      'emptyState',
      'explorersBody',
      'filterChips',
      'findingRow',
      'findingsBody',
      'guardrailsBody',
      'headerTotals',
      'historyTable',
      'issuesTile',
      'pipelineBlock',
      'resultBody',
      'roundsBody',
      'scopedTitle',
      'sessionTile',
      'stationTrack',
      'stepTile',
      'stepTiles',
      'tickElapsed',
      'wavesBody',
    ]);
  });
});
