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
    assert.deepEqual(view.headerCounts(repos), { running: 2, stalled: 1, failed: 1 });
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
  test('an article with data-key, the head, the track, and an empty step-detail', () => {
    const p = pipeline();
    const block = render.pipelineBlock(fakeDoc(), view, REPO, p, { collapsed: false, selected: 0 });
    assert.equal(block.tagName, 'article');
    assert.equal(block.className, 'pipe-block');
    assert.equal(block.attrs['data-key'], view.pipelineKey(REPO, p));
    assert.equal(block.attrs['data-id'], 'ship-1');
    oneByClass(block, 'pipe-head');
    oneByClass(block, 'track');
    assert.equal(oneByClass(block, 'step-detail').children.length, 0);
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

describe('render.js browser global fallback', () => {
  test('render.js assigns root.sdlcRender when module.exports is unavailable', () => {
    const source = fs.readFileSync(path.join(__dirname, '../static/render.js'), 'utf8');
    const fakeRoot = {};
    // eslint-disable-next-line no-new-func
    const run = new Function('module', source);
    run.call(fakeRoot);
    assert.deepEqual(Object.keys(fakeRoot.sdlcRender).sort(), [
      'activityPanel',
      'blockHead',
      'el',
      'emptyState',
      'filterChips',
      'headerTotals',
      'pipelineBlock',
      'stationTrack',
    ]);
  });
});
