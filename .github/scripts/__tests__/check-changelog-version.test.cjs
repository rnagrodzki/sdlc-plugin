'use strict';

/**
 * Tests for resolveVersionFromTags in check-changelog.cjs — the "current
 * version from git tags" resolution used when versionFile is not enabled.
 * Prior to this change, the function matched the first tag (by
 * `--sort=-v:refname` order) that merely started with a semver-looking
 * prefix, so an RC tag such as `v1.0.1-rc1` sorting ahead of the final
 * `v1.0.0` tag was picked as "the current version" — wrong, because the RC
 * has not shipped yet. It now requires a strict `X.Y.Z` match (no
 * pre-release suffix) and accepts an optional tag prefix, mirroring
 * highestSemverTag in release-on-main.cjs.
 *
 * check-changelog.cjs now guards its main() call with `require.main ===
 * module`, so it can be required in-process here instead of spawned as a
 * subprocess (unlike check-changelog-pr-guard.test.cjs, written before that
 * guard existed).
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execSync } = require('node:child_process');

const { resolveVersionFromTags } = require('../check-changelog.cjs');

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

describe('resolveVersionFromTags', () => {
  test('skips a higher-sorting RC tag and returns the final tag', () => {
    const dir = mkTmpDir('check-changelog-version-');
    initGitRepo(dir);
    execSync('git tag v1.0.0', { cwd: dir });
    execSync('git tag v1.0.1-rc1', { cwd: dir });

    const result = resolveVersionFromTags(dir, '');
    assert.equal(result, '1.0.0');
  });

  test('returns the final tag once it exists, even with an RC tag present', () => {
    const dir = mkTmpDir('check-changelog-version-');
    initGitRepo(dir);
    execSync('git tag v1.0.1-rc1', { cwd: dir });
    execSync('git tag v1.0.1', { cwd: dir });

    const result = resolveVersionFromTags(dir, '');
    assert.equal(result, '1.0.1');
  });

  test('respects a configured tag prefix', () => {
    const dir = mkTmpDir('check-changelog-version-');
    initGitRepo(dir);
    execSync('git tag app-v2.0.0', { cwd: dir });
    execSync('git tag v9.0.0', { cwd: dir });

    const result = resolveVersionFromTags(dir, 'app-v');
    assert.equal(result, '2.0.0');
  });

  test('returns null when no tag matches', () => {
    const dir = mkTmpDir('check-changelog-version-');
    initGitRepo(dir);
    execSync('git tag v1.0.0-rc1', { cwd: dir });

    const result = resolveVersionFromTags(dir, '');
    assert.equal(result, null);
  });

  test('returns null when there are no tags at all', () => {
    const dir = mkTmpDir('check-changelog-version-');
    initGitRepo(dir);

    const result = resolveVersionFromTags(dir, '');
    assert.equal(result, null);
  });
});
