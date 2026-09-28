'use strict';

/**
 * Tests for release-cleanup.cjs's selectReleasesToDelete() retention logic
 * and its require.main import guard.
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const SCRIPT_PATH = path.join(__dirname, '..', 'release-cleanup.cjs');
const { selectReleasesToDelete, DEFAULT_KEEP_COUNT } = require(SCRIPT_PATH);

function release(tagName, createdAt, isDraft = false) {
  return { tagName, createdAt, isDraft };
}

describe('selectReleasesToDelete', () => {
  test('keeps the N most recent, returns the rest for deletion', () => {
    const releases = [
      release('v1.0.0', '2026-01-01T00:00:00Z'),
      release('v1.1.0', '2026-02-01T00:00:00Z'),
      release('v1.2.0', '2026-03-01T00:00:00Z'),
    ];
    const toDelete = selectReleasesToDelete(releases, 2);
    assert.deepEqual(toDelete.map(r => r.tagName), ['v1.0.0']);
  });

  test('keepCount >= release count deletes nothing', () => {
    const releases = [release('v1.0.0', '2026-01-01T00:00:00Z')];
    assert.deepEqual(selectReleasesToDelete(releases, DEFAULT_KEEP_COUNT), []);
  });

  test('keepCount 0 deletes every published release', () => {
    const releases = [
      release('v1.0.0', '2026-01-01T00:00:00Z'),
      release('v1.1.0', '2026-02-01T00:00:00Z'),
    ];
    const toDelete = selectReleasesToDelete(releases, 0);
    assert.equal(toDelete.length, 2);
  });

  test('drafts are excluded from both kept and deleted sets', () => {
    const releases = [
      release('v1.0.0', '2026-01-01T00:00:00Z'),
      release('v1.1.0-draft', '2026-03-01T00:00:00Z', true),
    ];
    const toDelete = selectReleasesToDelete(releases, 0);
    assert.deepEqual(toDelete.map(r => r.tagName), ['v1.0.0']);
  });

  test('input order does not matter — sorts by createdAt before slicing', () => {
    const releases = [
      release('v1.2.0', '2026-03-01T00:00:00Z'),
      release('v1.0.0', '2026-01-01T00:00:00Z'),
      release('v1.1.0', '2026-02-01T00:00:00Z'),
    ];
    const toDelete = selectReleasesToDelete(releases, 1);
    assert.deepEqual(toDelete.map(r => r.tagName), ['v1.1.0', 'v1.0.0']);
  });
});

describe('release-cleanup.cjs require.main import guard', () => {
  test('requiring the module does not call gh or exit the process', () => {
    const result = spawnSync(
      'node',
      ['-e', `require(${JSON.stringify(SCRIPT_PATH)}); console.log('required-ok');`],
      { encoding: 'utf8' }
    );
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /required-ok/);
  });
});
