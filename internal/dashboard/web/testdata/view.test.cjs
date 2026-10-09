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

  test('returns an empty string with no detail or an unknown kind', () => {
    assert.equal(view.sectionMeta({}), '');
    assert.equal(view.sectionMeta({ detail: { kind: 'other' } }), '');
  });
});

describe('isWideSection', () => {
  test('waves with 2 or more waves, explorers, and rounds are wide', () => {
    assert.equal(view.isWideSection({ kind: 'waves', waves: [{}, {}] }), true);
    assert.equal(view.isWideSection({ kind: 'explorers' }), true);
    assert.equal(view.isWideSection({ kind: 'rounds' }), true);
  });

  test('one wave, dimensions, and findings are not wide', () => {
    assert.equal(view.isWideSection({ kind: 'waves', waves: [{}] }), false);
    assert.equal(view.isWideSection({ kind: 'dimensions' }), false);
    assert.equal(view.isWideSection({ kind: 'findings' }), false);
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

// The fake document: the DOM calls render.js may use, and no more. A new DOM
// call in render.js needs a matching method here.
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

  test('markup in a branch name stays text: <b>x</b>', () => {
    const head = render.blockHead(fakeDoc(), view, REPO, pipeline({ branch: '<b>x</b>' }), false, 0);
    const branch = oneByClass(head, 'pipe-branch');
    assert.equal(branch.textContent, '<b>x</b>');
    assert.equal(branch.children.length, 0);
    assert.equal(branch.attrs.title, '<b>x</b>');
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
});

describe('render pipelineBlock', () => {
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
});

describe('render headerTotals', () => {
  test('running, stalled and failed over every repo', () => {
    const repos = [
      { root: '/a', pipelines: [{ status: 'running' }, { status: 'failed' }] },
      { root: '/b', pipelines: [{ status: 'running' }, { status: 'completed' }] },
    ];
    const out = render.headerTotals(fakeDoc(), view, repos);
    assert.deepEqual(out.map((n) => n.className), ['c-run', 'c-stall', 'c-fail']);
    assert.deepEqual(out.map(textOf), ['running · 2', 'stalled · 0', 'failed · 1']);
    assert.equal(out[0].children[0].tagName, 'strong');
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
      learnings: [{ heading: 'plan: keep maps small', branch: 'main' }],
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
    assert.deepEqual(tilesOf(block).map((t) => t.attrs['data-section']), ['plan', 'execute', 'review', 'issues', 'session']);
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
    const rows = byClass(t, 'dim-row');
    assert.equal(rows.length, 5);
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

  test('a plan rounds tile: summary line, none and – when a round found 0, one chip for each lens', () => {
    const t = tileByName(fixtureBlock('sdlc-plugin', PLAN), 'review');
    assert.ok(classesOf(t).includes('wide'));
    assert.equal(textOf(oneByClass(t, 'round-sum')), '5 of 5 rounds · 19 issues found · 19 fixed');
    const rows = byClass(t, 'round-row').filter((r) => !classesOf(r).includes('head'));
    assert.equal(rows.length, 5);
    assert.deepEqual(rows[0].children.slice(0, 3).map((c) => c.textContent), ['round 1', '8', '8']);
    assert.deepEqual(rows[4].children.slice(0, 3).map((c) => c.textContent), ['round 5', 'none', '–']);
    const chips = byClass(rows[1], 'lens-chip');
    assert.deepEqual(chips.map((c) => c.className), ['lens-chip issues', 'lens-chip ok', 'lens-chip issues']);
    assert.equal(chips[1].textContent, 'requirements · approved');
    assert.equal(chips[0].textContent, 'architecture · issues');
  });

  test('a standalone execute: commits off on every wave, queued shows its tasks', () => {
    const block = fixtureBlock('payments-service', EXECUTE);
    const badges = byClass(oneByClass(block, 'step-detail'), 'commit-badge');
    assert.equal(badges.length, 3);
    for (const b of badges) assert.equal(b.textContent, 'commits off');
    const queued = tileByName(block, 'queued');
    assert.equal(textOf(oneByClass(queued, 'wave-head')), 'queued2');
    assert.deepEqual(byClass(queued, 'task-id').map((n) => n.textContent), ['T7', 'T8']);
    assert.ok(!classesOf(queued).includes('wide'));
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

  test('a wide section gets the wide class; one wave stays a column; the selected step is marked', () => {
    const one = { name: 'execute', status: 'completed', detail: { kind: 'waves', waves: [{ number: 1, tasks: [] }] } };
    const two = { name: 'execute', status: 'completed', detail: { kind: 'waves', waves: [{ number: 1, tasks: [] }, { number: 2, tasks: [] }] } };
    assert.equal(render.stepTile(fakeDoc(), view, pipeline(), one, 0, true, false).className, 'step-sec');
    assert.equal(render.stepTile(fakeDoc(), view, pipeline(), two, 0, true, true).className, 'step-sec wide selected');
    const rounds = { name: 'review', status: 'completed', detail: { kind: 'rounds', rounds: [], maxRounds: 5 } };
    assert.equal(render.stepTile(fakeDoc(), view, pipeline(), rounds, 0, true, false).className, 'step-sec wide');
  });

  test('every detail kind has a body builder', () => {
    assert.deepEqual(Object.keys(render.TILE_BODIES).sort(), ['dimensions', 'explorers', 'findings', 'rounds', 'waves']);
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

  test('rounds: a summary with no rounds', () => {
    const body = render.roundsBody(fakeDoc(), view, { kind: 'rounds', maxRounds: 3 });
    assert.equal(textOf(oneByClass(body, 'round-sum')), '0 of 3 rounds · 0 issues found · 0 fixed');
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
      'blockHead',
      'dimensionsBody',
      'el',
      'emptyState',
      'explorersBody',
      'filterChips',
      'findingsBody',
      'headerTotals',
      'historyTable',
      'issuesTile',
      'pipelineBlock',
      'roundsBody',
      'sessionTile',
      'stationTrack',
      'stepTile',
      'stepTiles',
      'wavesBody',
    ]);
  });
});
