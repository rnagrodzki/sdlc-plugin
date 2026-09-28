'use strict';

/**
 * Tests for retag-release.cjs's require.main import guard and its
 * isReleaseBumpCommit() release-bump-skip check (added so retag-release
 * doesn't fight release-on-main.cjs/promote-release.cjs over their own
 * "chore(release): ..." commits).
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execSync, spawnSync } = require('node:child_process');

const SCRIPT_PATH = path.join(__dirname, '..', 'retag-release.cjs');

function mkTmpDir(prefix) {
  return fs.mkdtempSync(path.join(os.tmpdir(), prefix));
}

function writeConfig(dir) {
  fs.mkdirSync(path.join(dir, '.sdlc-v2'), { recursive: true });
  const toml = [
    '[version]',
    '',
    '[version.tag]',
    'prefix = "v"',
    '',
    '[version.versionFile]',
    'enabled = false',
    '',
  ].join('\n');
  fs.writeFileSync(path.join(dir, '.sdlc-v2', 'config.toml'), toml, 'utf8');
}

function initRepo(dir) {
  execSync('git init -q -b main', { cwd: dir });
  execSync('git config user.email "test@example.com"', { cwd: dir });
  execSync('git config user.name "Test"', { cwd: dir });
}

function commit(dir, message, file = 'file.txt') {
  fs.writeFileSync(path.join(dir, file), `${message}\n`, 'utf8');
  execSync(`git add "${file}"`, { cwd: dir });
  execSync(`git commit -q -m "${message}"`, { cwd: dir });
}

describe('retag-release.cjs require.main import guard', () => {
  test('requiring the module does not execute main() or touch git/tags', () => {
    const dir = mkTmpDir('retag-require-guard-');
    initRepo(dir);
    commit(dir, 'initial');
    // Deliberately no .sdlc-v2/config.toml: if require() ran main(), the
    // "No .sdlc-v2/config.toml found" branch would print to stdout and
    // call process.exit(0) — either would be a visible side effect here.

    const result = spawnSync(
      'node',
      ['-e', `require(${JSON.stringify(SCRIPT_PATH)}); process.stdout.write('required-ok');`],
      { cwd: dir, encoding: 'utf8' },
    );

    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout, 'required-ok');
    assert.equal(result.stderr, '');

    const tags = execSync('git tag --list', { cwd: dir, encoding: 'utf8' }).trim();
    assert.equal(tags, '');
  });

  test('exports RETAG_SCRIPT_VERSION and isReleaseBumpCommit', () => {
    const mod = require(SCRIPT_PATH);
    assert.equal(mod.RETAG_SCRIPT_VERSION, 9);
    assert.equal(typeof mod.isReleaseBumpCommit, 'function');
  });
});

describe('isReleaseBumpCommit', () => {
  const { isReleaseBumpCommit } = require(SCRIPT_PATH);

  test('matches a release-on-main version-bump subject', () => {
    assert.equal(isReleaseBumpCommit('chore(release): 1.2.3'), true);
  });

  test('matches a promote-release subject', () => {
    assert.equal(isReleaseBumpCommit('chore(release): promote v1.2.3'), true);
  });

  test('does not match an unrelated conventional-commit subject', () => {
    assert.equal(isReleaseBumpCommit('fix: correct off-by-one error'), false);
  });

  test('does not match when the prefix has no separating space', () => {
    assert.equal(isReleaseBumpCommit('chore(release):1.2.3'), false);
  });

  test('does not match a substring occurrence mid-subject', () => {
    assert.equal(isReleaseBumpCommit('revert "chore(release): 1.2.3"'), false);
  });

  test('returns false for null/undefined/empty input', () => {
    assert.equal(isReleaseBumpCommit(null), false);
    assert.equal(isReleaseBumpCommit(undefined), false);
    assert.equal(isReleaseBumpCommit(''), false);
  });
});

describe('retag-release.cjs skips retagging on a release-bump HEAD commit', () => {
  test('logs the skip message and exits 0 without creating or moving a tag', () => {
    const dir = mkTmpDir('retag-release-bump-skip-');
    initRepo(dir);
    writeConfig(dir);
    execSync('git add -A', { cwd: dir });
    execSync('git commit -q -m "chore: add config"', { cwd: dir });
    commit(dir, 'chore(release): 1.2.3');

    const result = spawnSync('node', [SCRIPT_PATH], { cwd: dir, encoding: 'utf8' });

    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /HEAD is a release-bump commit \("chore\(release\): 1\.2\.3"\)\. Skipping retag\./);

    const tags = execSync('git tag --list', { cwd: dir, encoding: 'utf8' }).trim();
    assert.equal(tags, '');
  });

  test('does not skip when HEAD is an ordinary commit', () => {
    const dir = mkTmpDir('retag-release-no-skip-');
    initRepo(dir);
    writeConfig(dir);
    execSync('git add -A', { cwd: dir });
    execSync('git commit -q -m "chore: add config"', { cwd: dir });
    commit(dir, 'feat: add a thing');

    const result = spawnSync('node', [SCRIPT_PATH], { cwd: dir, encoding: 'utf8' });

    assert.equal(result.status, 0, result.stderr);
    // Falls through past the release-bump check to the (unrelated) "no
    // existing tags" skip further down main() — the release-bump message
    // specifically must not appear.
    assert.doesNotMatch(result.stdout, /release-bump commit/);
    assert.match(result.stdout, /No existing tags found \(tag path\)\. Skipping retag\./);
  });
});
