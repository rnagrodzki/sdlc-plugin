'use strict';

/**
 * Tests for RC note aggregation and CHANGELOG collapse added to
 * promote-release.cjs. Uses Node's built-in test runner (node --test) — no
 * npm install required, consistent with the script's own "Node built-ins
 * only" design. `gh` is stubbed via a fake executable prepended to PATH so
 * readAllRCNotes can be exercised without a real GitHub connection.
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execSync } = require('node:child_process');

const {
  findAllRCTags,
  readAllRCNotes,
  stripRCEntries,
  prependChangelogIfMissing,
  findActiveRCSeries,
  findLatestStableTag,
  bumpSemver,
  parseSemver,
} = require('../promote-release.cjs');

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
 * Installs a fake `gh` CLI on PATH for the duration of `fn`, resolving
 * `gh release view <tag> --json body --jq .body` from `notesByTag`.
 * Restores PATH afterward.
 */
function withFakeGh(notesByTag, fn) {
  const binDir = mkTmpDir('fake-gh-bin-');
  const ghPath = path.join(binDir, 'gh');
  const cases = Object.entries(notesByTag)
    .map(([tag, notes]) => `    ${tag}) printf '%s' ${JSON.stringify(notes)} ;;`)
    .join('\n');
  const script = `#!/bin/sh
if [ "$1 $2" = "release view" ]; then
  case "$3" in
${cases}
    *) printf '' ;;
  esac
fi
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

describe('findAllRCTags', () => {
  test('returns sorted RC tags', () => {
    const dir = mkTmpDir('promote-rctags-');
    initGitRepo(dir);
    execSync('git tag v0.0.1-rc2', { cwd: dir });
    execSync('git tag v0.0.1-rc1', { cwd: dir });
    execSync('git tag v0.0.1-rc10', { cwd: dir });
    execSync('git tag v0.0.2-rc1', { cwd: dir }); // different base — must be excluded

    assert.deepEqual(findAllRCTags(dir, 'v', '0.0.1'), [
      'v0.0.1-rc1',
      'v0.0.1-rc2',
      'v0.0.1-rc10',
    ]);
  });

  test('returns empty array when no RCs exist', () => {
    const dir = mkTmpDir('promote-rctags-');
    initGitRepo(dir);

    assert.deepEqual(findAllRCTags(dir, 'v', '0.0.1'), []);
  });
});

describe('readAllRCNotes', () => {
  test('aggregates notes from multiple RCs', () => {
    const dir = mkTmpDir('promote-notes-');
    withFakeGh(
      { 'v0.0.1-rc1': 'First RC notes.', 'v0.0.1-rc2': 'Second RC notes.' },
      () => {
        const notes = readAllRCNotes(['v0.0.1-rc1', 'v0.0.1-rc2'], dir);
        assert.match(notes, /### RC 1[\s\S]*First RC notes\./);
        assert.match(notes, /### RC 2[\s\S]*Second RC notes\./);
      }
    );
  });

  test('deduplicates identical notes', () => {
    const dir = mkTmpDir('promote-notes-');
    withFakeGh(
      { 'v0.0.1-rc1': 'Same notes.', 'v0.0.1-rc2': 'Same notes.' },
      () => {
        const notes = readAllRCNotes(['v0.0.1-rc1', 'v0.0.1-rc2'], dir);
        const occurrences = notes.split('Same notes.').length - 1;
        assert.equal(occurrences, 1);
      }
    );
  });

  test('returns empty string for an empty tag list', () => {
    const dir = mkTmpDir('promote-notes-');
    assert.equal(readAllRCNotes([], dir), '');
  });
});

describe('stripRCEntries', () => {
  test('removes all RC entries for target version', () => {
    const dir = mkTmpDir('promote-strip-');
    const changelog =
      '# Changelog\n\n' +
      '## [0.0.1-rc2] - 2026-09-09\n\n### RC 2\n\nSecond notes.\n\n' +
      '## [0.0.1-rc1] - 2026-09-08\n\n### RC 1\n\nFirst notes.\n\n' +
      '## [0.0.0] - 2026-09-01\n\nOlder release.\n';
    fs.writeFileSync(path.join(dir, 'CHANGELOG.md'), changelog, 'utf8');

    stripRCEntries(dir, 'CHANGELOG.md', 'v', '0.0.1');

    const result = fs.readFileSync(path.join(dir, 'CHANGELOG.md'), 'utf8');
    assert.doesNotMatch(result, /## \[0\.0\.1-rc\d+\]/);
    assert.match(result, /## \[0\.0\.0\]/);
    assert.match(result, /Older release\./);
  });

  test('preserves entries for other versions', () => {
    const dir = mkTmpDir('promote-strip-');
    const changelog =
      '# Changelog\n\n' +
      '## [0.0.2-rc1] - 2026-09-09\n\nUnrelated RC.\n\n' +
      '## [0.0.1-rc1] - 2026-09-08\n\nTarget RC.\n';
    fs.writeFileSync(path.join(dir, 'CHANGELOG.md'), changelog, 'utf8');

    stripRCEntries(dir, 'CHANGELOG.md', 'v', '0.0.1');

    const result = fs.readFileSync(path.join(dir, 'CHANGELOG.md'), 'utf8');
    assert.match(result, /## \[0\.0\.2-rc1\]/);
    assert.doesNotMatch(result, /## \[0\.0\.1-rc1\]/);
  });

  test('no-op when no RC entries are present', () => {
    const dir = mkTmpDir('promote-strip-');
    const changelog = '# Changelog\n\n## [0.0.0] - 2026-09-01\n\nOlder release.\n';
    fs.writeFileSync(path.join(dir, 'CHANGELOG.md'), changelog, 'utf8');

    stripRCEntries(dir, 'CHANGELOG.md', 'v', '0.0.1');

    const result = fs.readFileSync(path.join(dir, 'CHANGELOG.md'), 'utf8');
    assert.equal(result, changelog);
  });

  test('no-op when the CHANGELOG file does not exist', () => {
    const dir = mkTmpDir('promote-strip-');
    assert.doesNotThrow(() => stripRCEntries(dir, 'CHANGELOG.md', 'v', '0.0.1'));
  });
});

describe('promotion CHANGELOG collapse', () => {
  test('collapses RC entries into a single final entry with aggregated notes, no RC entries remain', () => {
    const dir = mkTmpDir('promote-collapse-');
    const changelog =
      '# Changelog\n\n' +
      '## [0.0.1-rc2] - 2026-09-09\n\n### RC 2\n\nSecond RC notes.\n\n' +
      '## [0.0.1-rc1] - 2026-09-08\n\n### RC 1\n\nFirst RC notes.\n';
    fs.writeFileSync(path.join(dir, 'CHANGELOG.md'), changelog, 'utf8');

    stripRCEntries(dir, 'CHANGELOG.md', 'v', '0.0.1');
    const aggregated = '### RC 1\n\nFirst RC notes.\n\n### RC 2\n\nSecond RC notes.';
    const wrote = prependChangelogIfMissing(dir, 'CHANGELOG.md', '0.0.1', aggregated);

    assert.equal(wrote, true);
    const result = fs.readFileSync(path.join(dir, 'CHANGELOG.md'), 'utf8');
    assert.doesNotMatch(result, /## \[0\.0\.1-rc\d+\]/);
    const finalCount = (result.match(/## \[0\.0\.1\]/g) || []).length;
    assert.equal(finalCount, 1);
    assert.match(result, /First RC notes\./);
    assert.match(result, /Second RC notes\./);
  });

  test('idempotency — re-run does not duplicate the final entry', () => {
    const dir = mkTmpDir('promote-collapse-');
    fs.writeFileSync(path.join(dir, 'CHANGELOG.md'), '# Changelog\n\n', 'utf8');

    const first = prependChangelogIfMissing(dir, 'CHANGELOG.md', '0.0.1', 'Notes.');
    const second = prependChangelogIfMissing(dir, 'CHANGELOG.md', '0.0.1', 'Notes.');

    assert.equal(first, true);
    assert.equal(second, false);
    const result = fs.readFileSync(path.join(dir, 'CHANGELOG.md'), 'utf8');
    const count = (result.match(/## \[0\.0\.1\]/g) || []).length;
    assert.equal(count, 1);
  });

  test('single-RC case still works (no regression)', () => {
    const dir = mkTmpDir('promote-collapse-single-');
    const changelog = '# Changelog\n\n## [0.0.1-rc1] - 2026-09-08\n\nOnly RC notes.\n';
    fs.writeFileSync(path.join(dir, 'CHANGELOG.md'), changelog, 'utf8');

    stripRCEntries(dir, 'CHANGELOG.md', 'v', '0.0.1');
    prependChangelogIfMissing(dir, 'CHANGELOG.md', '0.0.1', 'Only RC notes.');

    const result = fs.readFileSync(path.join(dir, 'CHANGELOG.md'), 'utf8');
    assert.doesNotMatch(result, /## \[0\.0\.1-rc1\]/);
    assert.match(result, /## \[0\.0\.1\]/);
    assert.match(result, /Only RC notes\./);
  });
});

describe('parseSemver', () => {
  test('parses X.Y.Z', () => {
    assert.deepEqual(parseSemver('1.2.3'), { major: 1, minor: 2, patch: 3 });
  });

  test('parses vX.Y.Z (strips leading v)', () => {
    assert.deepEqual(parseSemver('v1.2.3'), { major: 1, minor: 2, patch: 3 });
  });

  test('parses X.Y.Z-rc1 (strips pre-release suffix)', () => {
    assert.deepEqual(parseSemver('1.2.3-rc1'), { major: 1, minor: 2, patch: 3 });
  });

  test('returns null for invalid input', () => {
    assert.equal(parseSemver('not-a-version'), null);
    assert.equal(parseSemver('1.2'), null);
    assert.equal(parseSemver('1.2.3.4'), null);
  });
});

describe('bumpSemver', () => {
  test('major increments and zeroes minor+patch', () => {
    assert.equal(bumpSemver('1.2.3', 'major'), '2.0.0');
  });

  test('minor increments and zeroes patch', () => {
    assert.equal(bumpSemver('1.2.3', 'minor'), '1.3.0');
  });

  test('patch increments', () => {
    assert.equal(bumpSemver('1.2.3', 'patch'), '1.2.4');
  });
});

describe('findLatestStableTag', () => {
  test('returns highest non-RC semver tag', () => {
    const dir = mkTmpDir('promote-stable-');
    initGitRepo(dir);
    execSync('git tag v0.0.1', { cwd: dir });
    execSync('git tag v0.0.2', { cwd: dir });
    execSync('git tag v0.1.0', { cwd: dir });

    assert.equal(findLatestStableTag(dir, 'v'), 'v0.1.0');
  });

  test('ignores RC tags', () => {
    const dir = mkTmpDir('promote-stable-');
    initGitRepo(dir);
    execSync('git tag v0.0.1', { cwd: dir });
    execSync('git tag v0.0.2-rc1', { cwd: dir });

    assert.equal(findLatestStableTag(dir, 'v'), 'v0.0.1');
  });

  test('returns null when no tags exist', () => {
    const dir = mkTmpDir('promote-stable-');
    initGitRepo(dir);

    assert.equal(findLatestStableTag(dir, 'v'), null);
  });
});

describe('findActiveRCSeries', () => {
  test('returns highest base version series', () => {
    const dir = mkTmpDir('promote-rcseries-');
    initGitRepo(dir);
    execSync('git tag v0.0.1-rc1', { cwd: dir });
    execSync('git tag v0.1.0-rc1', { cwd: dir });

    const series = findActiveRCSeries(dir, 'v');
    assert.equal(series.baseVersion, '0.1.0');
    assert.deepEqual(series.tags, ['v0.1.0-rc1']);
  });

  test('handles multiple RC series (picks highest)', () => {
    const dir = mkTmpDir('promote-rcseries-');
    initGitRepo(dir);
    execSync('git tag v0.0.1-rc1', { cwd: dir });
    execSync('git tag v0.0.1-rc2', { cwd: dir });
    execSync('git tag v0.0.2-rc1', { cwd: dir });

    const series = findActiveRCSeries(dir, 'v');
    assert.equal(series.baseVersion, '0.0.2');
    assert.deepEqual(series.tags, ['v0.0.2-rc1']);
  });

  test('returns null when no RC tags exist', () => {
    const dir = mkTmpDir('promote-rcseries-');
    initGitRepo(dir);
    execSync('git tag v0.0.1', { cwd: dir });

    assert.equal(findActiveRCSeries(dir, 'v'), null);
  });

  test('handles single RC tag', () => {
    const dir = mkTmpDir('promote-rcseries-');
    initGitRepo(dir);
    execSync('git tag v0.0.1-rc1', { cwd: dir });

    const series = findActiveRCSeries(dir, 'v');
    assert.deepEqual(series, { baseVersion: '0.0.1', tags: ['v0.0.1-rc1'] });
  });
});

describe('cross-level RC promotion scenario', () => {
  test('findActiveRCSeries returns the patch RC series alongside an existing stable tag', () => {
    const dir = mkTmpDir('promote-crosslevel-');
    initGitRepo(dir);
    execSync('git tag v1.0.0', { cwd: dir });
    execSync('git tag v1.0.1-rc1', { cwd: dir });
    execSync('git tag v1.0.1-rc2', { cwd: dir });

    const series = findActiveRCSeries(dir, 'v');
    assert.deepEqual(series, { baseVersion: '1.0.1', tags: ['v1.0.1-rc1', 'v1.0.1-rc2'] });
  });
});
