'use strict';

/**
 * Tests for the pull_request CHANGELOG.md guard added to check-changelog.cjs.
 * check-changelog.cjs calls main() unconditionally at require time (no
 * require.main guard, unlike its sibling scripts), so it cannot be required
 * in-process for testing — it is invoked as a real `node` subprocess instead,
 * matching how it actually runs in CI.
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execSync, spawnSync } = require('node:child_process');

const SCRIPT_PATH = path.join(__dirname, '..', 'check-changelog.cjs');

function mkTmpDir(prefix) {
  return fs.mkdtempSync(path.join(os.tmpdir(), prefix));
}

function writeConfig(dir, versionConfig) {
  fs.mkdirSync(path.join(dir, '.sdlc-v2'), { recursive: true });
  fs.writeFileSync(
    path.join(dir, '.sdlc-v2', 'config.json'),
    JSON.stringify({ version: versionConfig }, null, 2),
    'utf8'
  );
}

/**
 * Builds a repo with a "main" branch (mirrored to a fake `origin/main`
 * remote-tracking ref via update-ref — no real remote needed) and a feature
 * branch on top with one extra commit, then runs check-changelog.cjs as a
 * pull_request event against it.
 */
function runPRGuard(dir, extraFileToModify, headRef = 'feature') {
  execSync('git init -q -b main', { cwd: dir });
  execSync('git config user.email "test@example.com"', { cwd: dir });
  execSync('git config user.name "Test"', { cwd: dir });

  fs.writeFileSync(path.join(dir, 'CHANGELOG.md'), '# Changelog\n', 'utf8');
  execSync('git add -A', { cwd: dir });
  execSync('git commit -q -m "initial"', { cwd: dir });
  // Fake a remote-tracking ref without needing an actual remote.
  execSync('git update-ref refs/remotes/origin/main HEAD', { cwd: dir });

  execSync(`git checkout -q -b "${headRef}"`, { cwd: dir });
  const target = path.join(dir, extraFileToModify);
  fs.writeFileSync(target, (fs.existsSync(target) ? fs.readFileSync(target, 'utf8') : '') + '\nedited\n', 'utf8');
  execSync(`git add "${extraFileToModify}"`, { cwd: dir });
  execSync('git commit -q -m "edit"', { cwd: dir });

  return spawnSync('node', [SCRIPT_PATH], {
    cwd: dir,
    encoding: 'utf8',
    env: { ...process.env, GITHUB_EVENT_NAME: 'pull_request', GITHUB_REF_NAME: headRef, GITHUB_HEAD_REF: headRef },
  });
}

describe('check-changelog.cjs pull_request CHANGELOG.md guard', () => {
  test('warns (exit 0) when CHANGELOG.md is modified and changelog automation is enabled', () => {
    const dir = mkTmpDir('check-changelog-pr-');
    writeConfig(dir, { changelog: true, changelogFile: 'CHANGELOG.md' });

    const result = runPRGuard(dir, 'CHANGELOG.md');

    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /WARNING: CHANGELOG\.md modified in this pull request/);
    assert.match(result.stdout, /::warning file=CHANGELOG\.md::/);
  });

  test('does not warn (exit 0) when CHANGELOG.md is untouched', () => {
    const dir = mkTmpDir('check-changelog-pr-');
    writeConfig(dir, { changelog: true, changelogFile: 'CHANGELOG.md' });
    fs.writeFileSync(path.join(dir, 'README.md'), '# Readme\n', 'utf8');

    const result = runPRGuard(dir, 'README.md');

    assert.equal(result.status, 0, result.stderr);
    assert.doesNotMatch(result.stdout, /WARNING:/);
    assert.match(result.stdout, /changelog-entry check skipped/);
  });

  test('does not warn when changelog automation is not enabled, even if CHANGELOG.md changed', () => {
    const dir = mkTmpDir('check-changelog-pr-');
    writeConfig(dir, { changelog: false });

    const result = runPRGuard(dir, 'CHANGELOG.md');

    assert.equal(result.status, 0, result.stderr);
    assert.doesNotMatch(result.stdout, /WARNING:/);
  });

  test('does not warn on the automated changelog/<tag> branch itself', () => {
    const dir = mkTmpDir('check-changelog-pr-');
    writeConfig(dir, { changelog: true, changelogFile: 'CHANGELOG.md' });

    const result = runPRGuard(dir, 'CHANGELOG.md', 'changelog/v1.2.3');

    assert.equal(result.status, 0, result.stderr);
    assert.doesNotMatch(result.stdout, /WARNING:/);
    assert.match(result.stdout, /changelog-entry check skipped/);
  });
});
