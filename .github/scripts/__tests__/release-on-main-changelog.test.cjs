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

const { prependChangelog, checkTagState, pushChangelogViaPR, resolveChangelogMethod } = require('../release-on-main.cjs');

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

/**
 * Installs a fake `gh` CLI on PATH for the duration of `fn`. The fake logs
 * every invocation's argv (space-joined) as one line to `logPath` and exits
 * 0, unless `failOn` matches the first two args ("pr merge"), in which case
 * it exits 1 — used to exercise pushChangelogViaPR's non-fatal fallback
 * when auto-merge isn't available. Restores PATH afterward.
 */
function withFakeGh(logPath, failOn, fn) {
  const binDir = mkTmpDir('fake-gh-bin-');
  const ghPath = path.join(binDir, 'gh');
  const failCheck = failOn
    ? `if [ "$1 $2" = "${failOn}" ]; then exit 1; fi\n`
    : '';
  const script = `#!/bin/sh
echo "$@" >> "${logPath}"
${failCheck}exit 0
`;
  fs.writeFileSync(ghPath, script, 'utf8');
  fs.chmodSync(ghPath, 0o755);

  const originalPath = process.env.PATH;
  process.env.PATH = `${binDir}${path.delimiter}${originalPath}`;
  try {
    return fn();
  } finally {
    process.env.PATH = originalPath;
  }
}

describe('resolveChangelogMethod', () => {
  test('explicit "pr" wins', () => {
    assert.strictEqual(resolveChangelogMethod({ changelogMethod: 'pr' }), 'pr');
  });

  test('explicit "push" wins', () => {
    assert.strictEqual(resolveChangelogMethod({ changelogMethod: 'push' }), 'push');
  });

  test('explicit "skip" wins', () => {
    assert.strictEqual(resolveChangelogMethod({ changelogMethod: 'skip' }), 'skip');
  });

  test('legacy true maps to push', () => {
    assert.strictEqual(resolveChangelogMethod({ changelog: true }), 'push');
  });

  test('legacy false maps to skip', () => {
    assert.strictEqual(resolveChangelogMethod({ changelog: false }), 'skip');
  });

  test('neither key defaults to skip', () => {
    assert.strictEqual(resolveChangelogMethod({}), 'skip');
  });

  test('explicit wins over legacy', () => {
    assert.strictEqual(resolveChangelogMethod({ changelogMethod: 'pr', changelog: false }), 'pr');
  });
});

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

describe('pushChangelogViaPR (PR-based changelog delivery)', () => {
  test('pushes the changelog branch and opens a PR with the no-release label', () => {
    const dir = mkTmpDir('release-on-main-pr-');
    initGitRepo(dir);
    execSync('git remote add origin .', { cwd: dir }); // local-path "remote" is enough for a branch push

    const logPath = path.join(mkTmpDir('release-on-main-pr-log-'), 'gh.log');
    withFakeGh(logPath, null, () => {
      pushChangelogViaPR(dir, 'main', 'v1.2.3');
    });

    // The changelog branch was actually created and pushed to the "remote"
    // (here, the same repo — origin points at `.`).
    const branches = execSync('git branch --list "changelog/v1.2.3"', { cwd: dir, encoding: 'utf8' });
    assert.match(branches, /changelog\/v1\.2\.3/);

    const log = fs.readFileSync(logPath, 'utf8');
    assert.match(log, /pr create/);
    assert.match(log, /--base main/);
    assert.match(log, /--head changelog\/v1\.2\.3/);
    assert.match(log, /--title chore\(release\): changelog for v1\.2\.3/);
    assert.match(log, /--label no-release/);
    assert.match(log, /pr merge changelog\/v1\.2\.3/);
    assert.match(log, /--auto/);
  });

  test('does not throw when gh pr merge (auto-merge) is unavailable', () => {
    const dir = mkTmpDir('release-on-main-pr-');
    initGitRepo(dir);
    execSync('git remote add origin .', { cwd: dir });

    const logPath = path.join(mkTmpDir('release-on-main-pr-log-'), 'gh.log');
    assert.doesNotThrow(() => {
      withFakeGh(logPath, 'pr merge', () => {
        pushChangelogViaPR(dir, 'main', 'v9.9.9');
      });
    });

    const log = fs.readFileSync(logPath, 'utf8');
    assert.match(log, /pr create/, 'PR creation must still happen even though merge will fail');
    assert.match(log, /pr merge/, 'merge is attempted even though it fails');
  });
});
