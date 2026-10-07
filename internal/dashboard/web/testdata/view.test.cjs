'use strict';

/**
 * Tests for the dashboard page logic module (internal/dashboard/web/static/view.js).
 * Uses Node's built-in test runner (node --test) — no npm install required,
 * consistent with the project's other .cjs test files. view.js has no DOM
 * access, so it is required directly here exactly as app.js uses it in the
 * browser.
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
