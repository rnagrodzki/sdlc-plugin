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
    assert.equal(view.pipelineTone('stalled'), 'signal-amber');
    assert.equal(view.pipelineTone('running'), 'signal-green');
    assert.equal(view.pipelineTone('completed'), 'rail');
  });
});

describe('sortPipelines', () => {
  test('knows exactly the 4 pipeline status values', () => {
    const one = { status: 'completed' };
    const two = { status: 'failed' };
    const three = { status: 'stalled' };
    const four = { status: 'running' };
    const sorted = view.sortPipelines([one, two, three, four]);
    assert.deepEqual(sorted, [two, three, four, one]);
  });

  test('orders failed, then stalled, then running, then completed', () => {
    const completed = { id: 'a', status: 'completed' };
    const running = { id: 'b', status: 'running' };
    const stalled = { id: 'c', status: 'stalled' };
    const failed = { id: 'd', status: 'failed' };
    const sorted = view.sortPipelines([completed, running, stalled, failed]);
    assert.deepEqual(sorted.map((p) => p.id), ['d', 'c', 'b', 'a']);
  });

  test('returns a new array and does not mutate the input', () => {
    const input = [{ status: 'completed' }, { status: 'failed' }];
    const sorted = view.sortPipelines(input);
    assert.notEqual(sorted, input);
    assert.equal(input[0].status, 'completed');
  });

  test('is stable for pipelines sharing a status', () => {
    const a = { id: 'a', status: 'running' };
    const b = { id: 'b', status: 'running' };
    const sorted = view.sortPipelines([a, b]);
    assert.deepEqual(sorted.map((p) => p.id), ['a', 'b']);
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

describe('loadOpenGroups / saveOpenGroups', () => {
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

  test('loadOpenGroups returns an empty set when storage holds no value', () => {
    const storage = fakeStorage();
    assert.deepEqual(view.loadOpenGroups(storage), new Set());
  });

  test('loadOpenGroups returns an empty set for bad JSON', () => {
    const storage = fakeStorage({ 'sdlc-dashboard-open-groups': 'not json' });
    assert.deepEqual(view.loadOpenGroups(storage), new Set());
  });

  test('loadOpenGroups returns an empty set when storage throws', () => {
    const storage = {
      getItem() {
        throw new Error('blocked');
      },
    };
    assert.deepEqual(view.loadOpenGroups(storage), new Set());
  });

  test('saveOpenGroups then loadOpenGroups round-trips the set', () => {
    const storage = fakeStorage();
    view.saveOpenGroups(storage, new Set(['/repo-a', '/repo-b']));
    assert.deepEqual(view.loadOpenGroups(storage), new Set(['/repo-a', '/repo-b']));
  });

  test('saveOpenGroups does not throw when storage throws', () => {
    const storage = {
      setItem() {
        throw new Error('quota exceeded');
      },
    };
    assert.doesNotThrow(() => view.saveOpenGroups(storage, new Set(['/repo-a'])));
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
