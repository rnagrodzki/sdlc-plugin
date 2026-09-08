'use strict';

/**
 * Tests for the RC CHANGELOG behavior added to release-on-main.cjs.
 * Uses Node's built-in test runner (node --test) — no npm install required,
 * consistent with the script's own "Node built-ins only" design.
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execSync } = require('node:child_process');

const { prependChangelog, checkTagState } = require('../release-on-main.cjs');

function mkTmpDir(prefix) {
  return fs.mkdtempSync(path.join(os.tmpdir(), prefix));
}

function initGitRepo(dir) {
  execSync('git init -q', { cwd: dir });
  execSync('git config user.email "test@example.com"', { cwd: dir });
  execSync('git config user.name "Test"', { cwd: dir });
  fs.writeFileSync(path.join(dir, 'file.txt'), 'x', 'utf8');
  execSync('git add file.txt', { cwd: dir });
  execSync('git commit -q -m "initial"', { cwd: dir });
}

describe('RC CHANGELOG behavior', () => {
  test('prepends RC entry when config.changelog is true', () => {
    const dir = mkTmpDir('release-on-main-changelog-');
    fs.writeFileSync(path.join(dir, 'CHANGELOG.md'), '# Changelog\n\n', 'utf8');

    prependChangelog(dir, 'CHANGELOG.md', '0.0.1-rc1', 'Fixed a bug.');

    const content = fs.readFileSync(path.join(dir, 'CHANGELOG.md'), 'utf8');
    assert.match(content, /## \[0\.0\.1-rc1\] - \d{4}-\d{2}-\d{2}/);
    assert.match(content, /Fixed a bug\./);
  });

  test('second RC prepends above first', () => {
    const dir = mkTmpDir('release-on-main-changelog-');
    prependChangelog(dir, 'CHANGELOG.md', '0.0.1-rc1', 'First RC notes.');
    prependChangelog(dir, 'CHANGELOG.md', '0.0.1-rc2', 'Second RC notes.');

    const content = fs.readFileSync(path.join(dir, 'CHANGELOG.md'), 'utf8');
    const idxRc1 = content.indexOf('## [0.0.1-rc1]');
    const idxRc2 = content.indexOf('## [0.0.1-rc2]');
    assert.ok(idxRc1 > -1 && idxRc2 > -1, 'both RC entries present');
    assert.ok(idxRc2 < idxRc1, 'rc2 heading appears above rc1 heading');
  });

  test('creates CHANGELOG if missing', () => {
    const dir = mkTmpDir('release-on-main-changelog-');
    const clPath = prependChangelog(dir, 'CHANGELOG.md', '0.0.1-rc1', 'Notes.');

    assert.ok(fs.existsSync(clPath));
    const content = fs.readFileSync(clPath, 'utf8');
    assert.match(content, /^# Changelog/);
    assert.match(content, /## \[0\.0\.1-rc1\]/);
  });

  test('idempotency guard: a reachable RC tag short-circuits before a duplicate CHANGELOG write', () => {
    // The RC flow's idempotency comes from checkTagState() in main() —
    // when the RC tag already exists and is reachable from HEAD, main()
    // exits before prependChangelog runs again. This verifies that guard
    // reports 'reachable' for a re-run at the same commit.
    const dir = mkTmpDir('release-on-main-tagstate-');
    initGitRepo(dir);
    execSync('git tag -a v0.0.1-rc1 -m "rc1"', { cwd: dir });

    assert.equal(checkTagState('v0.0.1-rc1', dir), 'reachable');
  });

  test('checkTagState reports missing for a tag that does not exist', () => {
    const dir = mkTmpDir('release-on-main-tagstate-');
    initGitRepo(dir);

    assert.equal(checkTagState('v9.9.9-rc1', dir), 'missing');
  });
});
