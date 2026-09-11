'use strict';

/**
 * Tests for release-on-main.cjs v7 — nested config shape, per-path
 * idempotency, changelog helpers, tag state checks, and per-path
 * independence (phase 3 runs regardless of phase 2 outcome).
 * Uses Node's built-in test runner (node --test) — no npm install required.
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execSync } = require('node:child_process');

const {
  readVersionConfig,
  runRelease,
  prependChangelog,
  checkTagState,
  pushFilesViaPR,
  changelogHeadingExists,
  findLastFinalTag,
  collectNotesSinceTag,
  aggregateNotesByCategory,
} = require('../release-on-main.cjs');

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
 * it exits 1.
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

/**
 * Installs a fake `gh` CLI on PATH that always prints `stdout` (a raw
 * string, typically JSON) to its own stdout and exits 0, regardless of
 * arguments. Unlike withFakeGh, this lets tests control what `gh` "returns"
 * so functions that JSON.parse gh's output (like collectNotesSinceTag) can
 * be tested without a real GitHub API call.
 */
function withFakeGhOutput(logPath, stdout, fn) {
  const binDir = mkTmpDir('fake-gh-output-bin-');
  const ghPath = path.join(binDir, 'gh');
  const script = `#!/bin/sh
echo "$@" >> "${logPath}"
cat <<'GHOUT'
${stdout}
GHOUT
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

/**
 * Write a minimal .sdlc-v2/config.json in the given directory.
 */
function writeConfig(dir, versionSection) {
  const sdlcDir = path.join(dir, '.sdlc-v2');
  fs.mkdirSync(sdlcDir, { recursive: true });
  fs.writeFileSync(
    path.join(sdlcDir, 'config.json'),
    JSON.stringify({ version: versionSection }, null, 2),
    'utf8'
  );
}

/**
 * Run readVersionConfig in a subprocess to test process.exit behavior.
 * Returns { status, stderr } — status is the exit code, stderr is the
 * error output.
 */
function runConfigInSubprocess(dir) {
  const script = `
    const { readVersionConfig } = require('${path.resolve(__dirname, '..', 'release-on-main.cjs').replace(/'/g, "\\'")}');
    const result = readVersionConfig('${dir.replace(/'/g, "\\'")}');
    process.stdout.write(JSON.stringify(result));
  `;
  try {
    const stdout = execSync(`node -e "${script.replace(/"/g, '\\"')}"`, {
      encoding: 'utf8',
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    return { status: 0, stdout, stderr: '' };
  } catch (err) {
    return { status: err.status, stdout: '', stderr: (err.stderr || '').toString() };
  }
}

/**
 * Create a bare remote repo with a pre-receive hook that blocks branch
 * pushes (exits 1 on refs/heads/*) but allows tag pushes. Returns the
 * path to the bare repo.
 */
function createBareWithProtectedBranches(dir) {
  const bareDir = path.join(dir, 'origin.git');
  execSync(`git init --bare -q "${bareDir}"`);
  const hookDir = path.join(bareDir, 'hooks');
  fs.mkdirSync(hookDir, { recursive: true });
  const hookScript = `#!/bin/sh
while read oldval newval ref; do
  case "$ref" in
    refs/heads/*) echo "protected branch" >&2; exit 1;;
  esac
done
exit 0
`;
  const hookPath = path.join(hookDir, 'pre-receive');
  fs.writeFileSync(hookPath, hookScript, 'utf8');
  fs.chmodSync(hookPath, 0o755);
  return bareDir;
}

/**
 * Set up a repo with a bare origin that has branch protection.
 * Returns { repoDir, bareDir, mergeSha }.
 */
function setupRepoWithProtectedOrigin() {
  const baseDir = mkTmpDir('release-independence-');
  const bareDir = createBareWithProtectedBranches(baseDir);

  const repoDir = path.join(baseDir, 'work');
  // Clone from bare so origin is set up
  execSync(`git clone -q "${bareDir}" "${repoDir}"`);
  execSync('git config user.email "test@example.com"', { cwd: repoDir });
  execSync('git config user.name "Test"', { cwd: repoDir });

  // Create initial commit and push to set up origin/main
  fs.writeFileSync(path.join(repoDir, 'file.txt'), 'x', 'utf8');
  execSync('git add file.txt', { cwd: repoDir });
  execSync('git commit -q -m "initial"', { cwd: repoDir });
  // Push before adding hook — hook blocks branch pushes
  // But we already cloned from bare, so we need to push initial commit
  // Temporarily remove the hook
  const hookPath = path.join(bareDir, 'hooks', 'pre-receive');
  const hookContent = fs.readFileSync(hookPath, 'utf8');
  fs.unlinkSync(hookPath);
  execSync('git push -q origin main', { cwd: repoDir });
  // Reinstall hook
  fs.writeFileSync(hookPath, hookContent, 'utf8');
  fs.chmodSync(hookPath, 0o755);

  // Write version file
  fs.writeFileSync(path.join(repoDir, 'package.json'), JSON.stringify({ version: '1.0.0' }), 'utf8');
  execSync('git add package.json', { cwd: repoDir });
  execSync('git commit -q -m "add package.json"', { cwd: repoDir });
  // Push this too (temporarily remove hook again)
  fs.unlinkSync(hookPath);
  execSync('git push -q origin main', { cwd: repoDir });
  fs.writeFileSync(hookPath, hookContent, 'utf8');
  fs.chmodSync(hookPath, 0o755);

  const mergeSha = execSync('git rev-parse HEAD', { cwd: repoDir, encoding: 'utf8' }).trim();

  return { repoDir, bareDir, mergeSha };
}

// ---------------------------------------------------------------------------
// readVersionConfig — nested config shape
// ---------------------------------------------------------------------------

describe('readVersionConfig', () => {
  test('parses valid nested config', () => {
    const dir = mkTmpDir('release-config-');
    writeConfig(dir, {
      method: 'push',
      tag: { enabled: true, prefix: 'v' },
      versionFile: { enabled: true, path: 'package.json', fileType: 'package.json' },
      changelog: { enabled: false },
    });

    const config = readVersionConfig(dir);
    assert.equal(config.method, 'push');
    assert.equal(config.tag.enabled, true);
    assert.equal(config.tag.prefix, 'v');
    assert.equal(config.versionFile.enabled, true);
    assert.equal(config.versionFile.path, 'package.json');
    assert.equal(config.versionFile.fileType, 'package.json');
    assert.equal(config.changelog.enabled, false);
    assert.equal(config.changelog.file, 'CHANGELOG.md');
  });

  test('defaults missing sub-objects to {enabled: false}', () => {
    const dir = mkTmpDir('release-config-');
    writeConfig(dir, {
      tag: { enabled: true, prefix: '' },
      // versionFile and changelog omitted
    });

    const config = readVersionConfig(dir);
    assert.equal(config.versionFile.enabled, false);
    assert.equal(config.changelog.enabled, false);
    assert.equal(config.method, 'push');
  });

  test('defaults method to push', () => {
    const dir = mkTmpDir('release-config-');
    writeConfig(dir, {
      tag: { enabled: true },
    });

    const config = readVersionConfig(dir);
    assert.equal(config.method, 'push');
  });

  test('defaults changelog.file to CHANGELOG.md when enabled', () => {
    const dir = mkTmpDir('release-config-');
    writeConfig(dir, {
      tag: { enabled: true },
      changelog: { enabled: true },
    });

    const config = readVersionConfig(dir);
    assert.equal(config.changelog.enabled, true);
    assert.equal(config.changelog.file, 'CHANGELOG.md');
  });

  test('returns null when no config file exists', () => {
    const dir = mkTmpDir('release-config-');
    const config = readVersionConfig(dir);
    assert.equal(config, null);
  });

  test('returns null when version section is absent', () => {
    const dir = mkTmpDir('release-config-');
    const sdlcDir = path.join(dir, '.sdlc-v2');
    fs.mkdirSync(sdlcDir, { recursive: true });
    fs.writeFileSync(path.join(sdlcDir, 'config.json'), '{}', 'utf8');

    const config = readVersionConfig(dir);
    assert.equal(config, null);
  });
});

describe('readVersionConfig — old shape rejection', () => {
  test('rejects config with string versionFile', () => {
    const dir = mkTmpDir('release-config-');
    writeConfig(dir, {
      versionFile: 'package.json',
      tag: { enabled: true },
    });

    const result = runConfigInSubprocess(dir);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /old flat shape/);
  });

  test('rejects config with mode field', () => {
    const dir = mkTmpDir('release-config-');
    writeConfig(dir, {
      mode: 'file',
      tag: { enabled: true },
      versionFile: { enabled: true, path: 'package.json', fileType: 'package.json' },
    });

    const result = runConfigInSubprocess(dir);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /old flat shape/);
  });

  test('rejects config with changelogMethod', () => {
    const dir = mkTmpDir('release-config-');
    writeConfig(dir, {
      changelogMethod: 'push',
      tag: { enabled: true },
      versionFile: { enabled: true, path: 'package.json', fileType: 'package.json' },
    });

    const result = runConfigInSubprocess(dir);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /old flat shape/);
  });

  test('rejects config with boolean changelog', () => {
    const dir = mkTmpDir('release-config-');
    writeConfig(dir, {
      changelog: true,
      tag: { enabled: true },
    });

    const result = runConfigInSubprocess(dir);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /old flat shape/);
  });

  test('rejects config with neither tag nor versionFile enabled', () => {
    const dir = mkTmpDir('release-config-');
    writeConfig(dir, {
      tag: { enabled: false },
      versionFile: { enabled: false },
    });

    const result = runConfigInSubprocess(dir);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /at least one/);
  });
});

// ---------------------------------------------------------------------------
// Changelog helpers
// ---------------------------------------------------------------------------

describe('prependChangelog', () => {
  test('prepends entry to existing changelog', () => {
    const dir = mkTmpDir('release-on-main-changelog-');
    fs.writeFileSync(path.join(dir, 'CHANGELOG.md'), '# Changelog\n\n', 'utf8');

    prependChangelog(dir, 'CHANGELOG.md', '0.0.1', 'Fixed a bug.');

    const content = fs.readFileSync(path.join(dir, 'CHANGELOG.md'), 'utf8');
    assert.match(content, /## \[0\.0\.1\] - \d{4}-\d{2}-\d{2}/);
    assert.match(content, /Fixed a bug\./);
  });

  test('second entry prepends above first', () => {
    const dir = mkTmpDir('release-on-main-changelog-');
    prependChangelog(dir, 'CHANGELOG.md', '0.0.1', 'First release.');
    prependChangelog(dir, 'CHANGELOG.md', '0.0.2', 'Second release.');

    const content = fs.readFileSync(path.join(dir, 'CHANGELOG.md'), 'utf8');
    const idx1 = content.indexOf('## [0.0.1]');
    const idx2 = content.indexOf('## [0.0.2]');
    assert.ok(idx1 > -1 && idx2 > -1, 'both entries present');
    assert.ok(idx2 < idx1, '0.0.2 heading appears above 0.0.1 heading');
  });

  test('creates CHANGELOG if missing', () => {
    const dir = mkTmpDir('release-on-main-changelog-');
    const clPath = prependChangelog(dir, 'CHANGELOG.md', '0.0.1', 'Notes.');

    assert.ok(fs.existsSync(clPath));
    const content = fs.readFileSync(clPath, 'utf8');
    assert.match(content, /^# Changelog/);
    assert.match(content, /## \[0\.0\.1\]/);
  });
});

describe('changelogHeadingExists', () => {
  test('returns true when heading exists', () => {
    const dir = mkTmpDir('release-on-main-changelog-');
    prependChangelog(dir, 'CHANGELOG.md', '1.2.3', 'Notes.');
    assert.equal(changelogHeadingExists(dir, 'CHANGELOG.md', '1.2.3'), true);
  });

  test('returns false when heading does not exist', () => {
    const dir = mkTmpDir('release-on-main-changelog-');
    prependChangelog(dir, 'CHANGELOG.md', '1.2.3', 'Notes.');
    assert.equal(changelogHeadingExists(dir, 'CHANGELOG.md', '9.9.9'), false);
  });

  test('returns false when file does not exist', () => {
    const dir = mkTmpDir('release-on-main-changelog-');
    assert.equal(changelogHeadingExists(dir, 'CHANGELOG.md', '1.0.0'), false);
  });
});

// ---------------------------------------------------------------------------
// checkTagState
// ---------------------------------------------------------------------------

describe('checkTagState', () => {
  test('reports reachable for a tag on HEAD', () => {
    const dir = mkTmpDir('release-on-main-tagstate-');
    initGitRepo(dir);
    execSync('git tag -a v0.0.1 -m "v0.0.1"', { cwd: dir });

    assert.equal(checkTagState('v0.0.1', dir), 'reachable');
  });

  test('reports missing for a tag that does not exist', () => {
    const dir = mkTmpDir('release-on-main-tagstate-');
    initGitRepo(dir);

    assert.equal(checkTagState('v9.9.9', dir), 'missing');
  });
});

// ---------------------------------------------------------------------------
// pushFilesViaPR
// ---------------------------------------------------------------------------

describe('pushFilesViaPR (PR-based file delivery)', () => {
  test('pushes branch and opens PR with no-release label', () => {
    const dir = mkTmpDir('release-on-main-pr-');
    initGitRepo(dir);
    execSync('git remote add origin .', { cwd: dir });

    const logPath = path.join(mkTmpDir('release-on-main-pr-log-'), 'gh.log');
    withFakeGh(logPath, null, () => {
      pushFilesViaPR(dir, 'main', 'release/v1.2.3', 'chore(release): 1.2.3');
    });

    const log = fs.readFileSync(logPath, 'utf8');
    assert.match(log, /pr create/);
    assert.match(log, /--base main/);
    assert.match(log, /--head release\/v1\.2\.3/);
    assert.match(log, /--title chore\(release\): 1\.2\.3/);
    assert.match(log, /--label no-release/);
    assert.match(log, /pr merge/);
    assert.match(log, /--auto/);
  });

  test('does not throw when gh pr merge (auto-merge) is unavailable', () => {
    const dir = mkTmpDir('release-on-main-pr-');
    initGitRepo(dir);
    execSync('git remote add origin .', { cwd: dir });

    const logPath = path.join(mkTmpDir('release-on-main-pr-log-'), 'gh.log');
    assert.doesNotThrow(() => {
      withFakeGh(logPath, 'pr merge', () => {
        pushFilesViaPR(dir, 'main', 'release/v9.9.9', 'chore(release): 9.9.9');
      });
    });

    const log = fs.readFileSync(logPath, 'utf8');
    assert.match(log, /pr create/, 'PR creation must still happen even though merge will fail');
    assert.match(log, /pr merge/, 'merge is attempted even though it fails');
  });
});

// ---------------------------------------------------------------------------
// Per-path independence — phase 3 runs regardless of phase 2
// ---------------------------------------------------------------------------

describe('runRelease — per-path independence', () => {
  test('tag succeeds when push-method branch push fails (push method)', () => {
    const { repoDir, bareDir, mergeSha } = setupRepoWithProtectedOrigin();

    const ghLogPath = path.join(mkTmpDir('release-indep-log-'), 'gh.log');

    const config = {
      method: 'push',
      tag: { enabled: true, prefix: 'v' },
      versionFile: { enabled: true, path: 'package.json', fileType: 'package.json' },
      changelog: { enabled: false, file: 'CHANGELOG.md' },
    };

    const pathStatus = withFakeGh(ghLogPath, null, () => {
      return runRelease({
        repoRoot: repoDir,
        config,
        newVersion: '1.0.1',
        newTag: 'v1.0.1',
        isRCRelease: false,
        notes: 'Test release notes.',
        branch: 'main',
        mergeSha,
      });
    });

    // Phase 2 failed — branch push blocked by pre-receive hook
    assert.equal(pathStatus.versionFile, 'failed', 'versionFile should fail because push is blocked');

    // Phase 3 succeeded — tag push is allowed (hook only blocks refs/heads/*)
    assert.equal(pathStatus.tag, 'ok', 'tag should succeed despite phase 2 failure');

    // Verify tag exists on bare origin and points to mergeSha
    const tagSha = execSync(
      `git rev-parse "v1.0.1^{commit}"`,
      { cwd: bareDir, encoding: 'utf8' }
    ).trim();
    assert.equal(tagSha, mergeSha, 'tag should point to mergeSha (not a local bump commit)');

    // Verify gh release create was called
    const ghLog = fs.readFileSync(ghLogPath, 'utf8');
    assert.match(ghLog, /release create v1\.0\.1/, 'GitHub Release should be created');
  });

  test('tag targets mergeSha when PR method has no prior release commit', () => {
    const dir = mkTmpDir('release-pr-target-');
    initGitRepo(dir);

    // Add a "remote" that accepts pushes (simple self-referencing)
    execSync('git remote add origin .', { cwd: dir });

    // Write version file
    fs.writeFileSync(path.join(dir, 'package.json'), JSON.stringify({ version: '1.0.0' }), 'utf8');
    execSync('git add package.json', { cwd: dir });
    execSync('git commit -q -m "add pkg"', { cwd: dir });

    const mergeSha = execSync('git rev-parse HEAD', { cwd: dir, encoding: 'utf8' }).trim();

    const ghLogPath = path.join(mkTmpDir('release-pr-log-'), 'gh.log');

    const config = {
      method: 'pr',
      tag: { enabled: true, prefix: 'v' },
      versionFile: { enabled: true, path: 'package.json', fileType: 'package.json' },
      changelog: { enabled: false, file: 'CHANGELOG.md' },
    };

    const pathStatus = withFakeGh(ghLogPath, null, () => {
      return runRelease({
        repoRoot: dir,
        config,
        newVersion: '1.0.1',
        newTag: 'v1.0.1',
        isRCRelease: false,
        notes: 'PR release.',
        branch: 'main',
        mergeSha,
      });
    });

    assert.equal(pathStatus.tag, 'ok');
    assert.equal(pathStatus.versionFile, 'ok');

    // Tag should point to mergeSha — in PR method, HEAD after phase 2
    // is a local bump commit; tag resolution falls back to mergeSha
    // because bumpCommitSHA is null (no push in PR method phase 2).
    const tagSha = execSync('git rev-parse "v1.0.1^{commit}"', { cwd: dir, encoding: 'utf8' }).trim();
    assert.equal(tagSha, mergeSha, 'tag must point to mergeSha, not local bump commit HEAD');
  });

  test('RC release skips phase 2 entirely and skips GitHub Release creation', () => {
    const dir = mkTmpDir('release-rc-');
    initGitRepo(dir);
    execSync('git remote add origin .', { cwd: dir });

    const mergeSha = execSync('git rev-parse HEAD', { cwd: dir, encoding: 'utf8' }).trim();
    const ghLogPath = path.join(mkTmpDir('release-rc-log-'), 'gh.log');

    const config = {
      method: 'push',
      tag: { enabled: true, prefix: 'v' },
      versionFile: { enabled: true, path: 'package.json', fileType: 'package.json' },
      changelog: { enabled: true, file: 'CHANGELOG.md' },
    };

    const pathStatus = withFakeGh(ghLogPath, null, () => {
      return runRelease({
        repoRoot: dir,
        config,
        newVersion: '1.0.1-rc1',
        newTag: 'v1.0.1-rc1',
        isRCRelease: true,
        notes: 'RC notes.',
        branch: 'main',
        mergeSha,
      });
    });

    // RC: both file paths skipped
    assert.equal(pathStatus.versionFile, 'skipped');
    assert.equal(pathStatus.changelog, 'skipped');
    assert.equal(pathStatus.tag, 'ok');

    // Tag pushed, but no GitHub Release created for RC
    const tagSha = execSync('git rev-parse "v1.0.1-rc1^{commit}"', { cwd: dir, encoding: 'utf8' }).trim();
    assert.equal(tagSha, mergeSha, 'RC tag must still be created and point to mergeSha');
    assert.ok(!fs.existsSync(ghLogPath), 'gh should not be invoked at all for an RC release');
  });
});

// ---------------------------------------------------------------------------
// Changelog aggregation helpers
// ---------------------------------------------------------------------------

describe('aggregateNotesByCategory', () => {
  test('merges categories from multiple PRs', () => {
    const input = [
      '### Added\n- Feature A',
      '### Added\n- Feature B\n\n### Fixed\n- Bug C',
    ];
    const result = aggregateNotesByCategory(input);
    assert.match(result, /### Added/);
    assert.match(result, /- Feature A/);
    assert.match(result, /- Feature B/);
    assert.match(result, /### Fixed/);
    assert.match(result, /- Bug C/);
  });

  test('deduplicates identical items', () => {
    const input = [
      '### Added\n- Feature A',
      '### Added\n- Feature A',
    ];
    const result = aggregateNotesByCategory(input);
    const addedCount = (result.match(/- Feature A/g) || []).length;
    assert.equal(addedCount, 1, 'identical items should not be duplicated');
  });

  test('handles notes without category headings', () => {
    const input = ['- Raw bullet point'];
    const result = aggregateNotesByCategory(input);
    assert.match(result, /- Raw bullet point/);
  });

  test('handles empty notes list', () => {
    const result = aggregateNotesByCategory([]);
    assert.equal(result, '');
  });

  test('handles null/undefined notes', () => {
    const result = aggregateNotesByCategory(null);
    assert.equal(result, '');
  });

  test('groups items by category and sorts', () => {
    const input = [
      '### Fixed\n- Bug Z',
      '### Added\n- Feature M',
      '### Changed\n- Behavior X',
    ];
    const result = aggregateNotesByCategory(input);
    const addedIdx = result.indexOf('### Added');
    const changedIdx = result.indexOf('### Changed');
    const fixedIdx = result.indexOf('### Fixed');
    assert.ok(addedIdx < changedIdx && changedIdx < fixedIdx, 'categories should be ordered');
  });
});

describe('findLastFinalTag', () => {
  test('returns the latest non-RC semver tag', () => {
    const dir = mkTmpDir('release-findtag-');
    initGitRepo(dir);
    execSync('git tag v1.0.0', { cwd: dir });
    execSync('git tag v1.1.0-rc1', { cwd: dir });
    execSync('git tag v1.0.1', { cwd: dir });
    execSync('git tag v2.0.0-rc2', { cwd: dir });

    const result = findLastFinalTag(dir, 'v');
    assert.equal(result, '1.0.1', 'should return latest final tag (not RC)');
  });

  test('returns null when no final tags exist', () => {
    const dir = mkTmpDir('release-findtag-');
    initGitRepo(dir);
    execSync('git tag v1.0.0-rc1', { cwd: dir });
    execSync('git tag v1.0.0-rc2', { cwd: dir });

    const result = findLastFinalTag(dir, 'v');
    assert.equal(result, null, 'should return null when only RC tags exist');
  });

  test('respects tag prefix', () => {
    const dir = mkTmpDir('release-findtag-');
    initGitRepo(dir);
    execSync('git tag release-1.0.0', { cwd: dir });
    execSync('git tag release-1.1.0-rc1', { cwd: dir });
    execSync('git tag other-2.0.0', { cwd: dir });

    const result = findLastFinalTag(dir, 'release-');
    assert.equal(result, '1.0.0', 'should only consider tags with the given prefix');
  });

  test('returns null when no tags exist', () => {
    const dir = mkTmpDir('release-findtag-');
    initGitRepo(dir);

    const result = findLastFinalTag(dir, 'v');
    assert.equal(result, null);
  });
});

describe('collectNotesSinceTag', () => {
  test('extracts notes from PR bodies using release-notes markers, falls back to title when absent', () => {
    const dir = mkTmpDir('release-collectnotes-');
    initGitRepo(dir);
    execSync('git tag v1.0.0', { cwd: dir });

    const prData = [
      {
        number: 1,
        title: 'PR One title',
        body: '<!-- release-notes-start -->\n### Added\n- Feature A\n<!-- release-notes-end -->',
      },
      {
        number: 2,
        title: 'PR Two title',
        body: 'just a description, no markers',
      },
    ];

    const logPath = path.join(mkTmpDir('release-collectnotes-log-'), 'gh.log');
    const result = withFakeGhOutput(logPath, JSON.stringify(prData), () =>
      collectNotesSinceTag(dir, 'v1.0.0')
    );

    assert.equal(result.length, 2);
    assert.match(result[0], /### Added/);
    assert.match(result[0], /- Feature A/);
    assert.equal(result[1], '- PR Two title');
  });

  test('returns empty array when the tag does not exist', () => {
    const dir = mkTmpDir('release-collectnotes-noexist-');
    initGitRepo(dir);
    // Do NOT create the tag

    const result = collectNotesSinceTag(dir, 'v9.9.9-does-not-exist');
    assert.deepEqual(result, []);
  });

  test('returns empty array when gh pr list returns no PRs', () => {
    const dir = mkTmpDir('release-collectnotes-empty-');
    initGitRepo(dir);
    execSync('git tag v1.0.0', { cwd: dir });

    const logPath = path.join(mkTmpDir('release-collectnotes-empty-log-'), 'gh.log');
    const result = withFakeGhOutput(logPath, '[]', () =>
      collectNotesSinceTag(dir, 'v1.0.0')
    );

    assert.deepEqual(result, []);
  });

  test('returns empty array for a falsy tag', () => {
    const result1 = collectNotesSinceTag('/any/dir', '');
    const result2 = collectNotesSinceTag('/any/dir', null);

    assert.deepEqual(result1, []);
    assert.deepEqual(result2, []);
  });
});
