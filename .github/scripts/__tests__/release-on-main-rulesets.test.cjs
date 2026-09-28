'use strict';

/**
 * Tests for release-on-main.cjs's GH013 / ruleset-rejection classifier
 * (classifyPushError, RULESET_PUSH_HINT) and the config.method
 * 'push-with-secret' -> 'push' normalization.
 *
 * The GH013-style rejection text always comes from a real `git push`
 * against a real bare repo with a pre-receive hook — never a hand-written
 * stderr string handed straight to classifyPushError — so these tests
 * exercise the same failure shape a real GitHub ruleset produces.
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
  pushFilesViaPR,
  classifyPushError,
  RULESET_PUSH_HINT,
} = require('../release-on-main.cjs');

function mkTmpDir(prefix) {
  return fs.mkdtempSync(path.join(os.tmpdir(), prefix));
}

function tomlScalar(value) {
  return typeof value === 'string' ? JSON.stringify(value) : String(value);
}

function writeConfig(dir, versionSection) {
  const sdlcDir = path.join(dir, '.sdlc-v2');
  fs.mkdirSync(sdlcDir, { recursive: true });
  const scalarLines = [];
  const tableBlocks = [];
  for (const [key, val] of Object.entries(versionSection)) {
    if (val !== null && typeof val === 'object' && !Array.isArray(val)) {
      const subLines = Object.entries(val).map(([k, v]) => `${k} = ${tomlScalar(v)}`);
      tableBlocks.push(`[version.${key}]\n${subLines.join('\n')}`);
    } else {
      scalarLines.push(`${key} = ${tomlScalar(val)}`);
    }
  }
  const toml = ['[version]', ...scalarLines, '', ...tableBlocks].join('\n') + '\n';
  fs.writeFileSync(path.join(sdlcDir, 'config.toml'), toml, 'utf8');
}

/**
 * Installs a fake `gh` CLI on PATH for the duration of `fn`. Logs every
 * invocation's argv to `logPath` and exits 0. Mirrors the helper in
 * release-on-main-changelog.test.cjs.
 */
function withFakeGh(logPath, fn) {
  const binDir = mkTmpDir('fake-gh-bin-');
  const ghPath = path.join(binDir, 'gh');
  const script = `#!/bin/sh\necho "$@" >> "${logPath}"\nexit 0\n`;
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
 * Create a bare remote repo with a pre-receive hook that rejects pushes to
 * refs matching `blockGlob` ("refs/heads/*" or "refs/tags/*") with a
 * real GH013-style GitHub rulesets rejection message on stderr, and allows
 * everything else through. Mirrors createBareWithProtectedBranches in
 * release-on-main-changelog.test.cjs but with the actual GH013 message
 * GitHub emits, instead of a generic "protected branch" string.
 */
function createBareWithRulesetHook(dir, blockGlob) {
  const bareDir = path.join(dir, 'origin.git');
  execSync(`git init --bare -q "${bareDir}"`);
  const hookDir = path.join(bareDir, 'hooks');
  fs.mkdirSync(hookDir, { recursive: true });
  const hookScript = `#!/bin/sh
while read oldval newval ref; do
  case "$ref" in
    ${blockGlob})
      echo "remote: error: GH013: Repository rule violations found for refs/heads/main." >&2
      echo "remote: - Changes must be made through a pull request." >&2
      exit 1
      ;;
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
 * Clone `bareDir`, add an initial commit, and push it while the hook is
 * temporarily removed (so setup itself isn't blocked by ruleset rejection).
 * Returns { repoDir, mergeSha }.
 */
function cloneAndSeed(baseDir, bareDir) {
  const repoDir = path.join(baseDir, 'work');
  execSync(`git clone -q "${bareDir}" "${repoDir}"`);
  // Pin the branch name — git's default initial branch depends on the
  // environment's init.defaultBranch config, but the rest of this helper
  // (and the code under test) hardcodes "main".
  execSync('git checkout -b main', { cwd: repoDir });
  execSync('git config user.email "test@example.com"', { cwd: repoDir });
  execSync('git config user.name "Test"', { cwd: repoDir });

  const hookPath = path.join(bareDir, 'hooks', 'pre-receive');
  const hookContent = fs.readFileSync(hookPath, 'utf8');
  fs.unlinkSync(hookPath);

  fs.writeFileSync(path.join(repoDir, 'package.json'), JSON.stringify({ version: '1.0.0' }), 'utf8');
  execSync('git add package.json', { cwd: repoDir });
  execSync('git commit -q -m "initial"', { cwd: repoDir });
  execSync('git push -q origin main', { cwd: repoDir });

  fs.writeFileSync(hookPath, hookContent, 'utf8');
  fs.chmodSync(hookPath, 0o755);

  const mergeSha = execSync('git rev-parse HEAD', { cwd: repoDir, encoding: 'utf8' }).trim();
  return { repoDir, mergeSha };
}

// ---------------------------------------------------------------------------
// classifyPushError — driven by a real GH013 rejection from git, not a
// hand-written stderr string.
// ---------------------------------------------------------------------------

describe('classifyPushError — real GH013 rejection', () => {
  test('recognizes a real git push rejected by a GH013-style pre-receive hook', () => {
    const baseDir = mkTmpDir('release-ruleset-classify-');
    const bareDir = createBareWithRulesetHook(baseDir, 'refs/heads/*');
    const { repoDir } = cloneAndSeed(baseDir, bareDir);

    fs.writeFileSync(path.join(repoDir, 'file.txt'), 'change', 'utf8');
    execSync('git add file.txt', { cwd: repoDir });
    execSync('git commit -q -m "change"', { cwd: repoDir });

    let caughtStderr = null;
    try {
      execSync('git push origin HEAD:main', { cwd: repoDir, encoding: 'utf8', stdio: 'pipe' });
      assert.fail('push should have been rejected by the pre-receive hook');
    } catch (err) {
      caughtStderr = err.stderr;
    }

    const hint = classifyPushError(caughtStderr);
    assert.equal(hint, RULESET_PUSH_HINT, 'a real GH013 rejection must classify to RULESET_PUSH_HINT');
    assert.match(hint, /docs\/versioning\.md#protected-branches-and-rulesets/);
  });

  test('returns null for an unrelated push failure', () => {
    assert.equal(classifyPushError('remote: fatal: some unrelated error\n'), null);
    assert.equal(classifyPushError(''), null);
    assert.equal(classifyPushError(null), null);
  });
});

// ---------------------------------------------------------------------------
// pushFilesViaPR (line ~660 push site) — throws with the hint on rejection.
// ---------------------------------------------------------------------------

describe('pushFilesViaPR — ruleset-rejected branch push', () => {
  test('throws an Error whose message carries RULESET_PUSH_HINT', () => {
    const baseDir = mkTmpDir('release-ruleset-pr-');
    const bareDir = createBareWithRulesetHook(baseDir, 'refs/heads/*');
    const { repoDir } = cloneAndSeed(baseDir, bareDir);

    const logPath = path.join(mkTmpDir('release-ruleset-pr-log-'), 'gh.log');

    assert.throws(
      () => {
        withFakeGh(logPath, () => {
          pushFilesViaPR(repoDir, 'main', 'release/v1.2.3', 'chore(release): 1.2.3');
        });
      },
      (err) => {
        assert.match(err.message, /Push rejected by a branch\/tag ruleset/);
        assert.match(err.message, /docs\/versioning\.md#protected-branches-and-rulesets/);
        return true;
      }
    );
  });
});

// ---------------------------------------------------------------------------
// runRelease push-method branch push (line ~858) — Phase 2 catches the
// error internally; assert it surfaces the hint on stderr and fails the
// affected paths, while leaving tag creation (a different ref) unaffected.
// ---------------------------------------------------------------------------

describe('runRelease — ruleset-rejected branch push (push method)', () => {
  test('phase 2 failure message carries RULESET_PUSH_HINT; tag still succeeds', () => {
    const baseDir = mkTmpDir('release-ruleset-phase2-');
    const bareDir = createBareWithRulesetHook(baseDir, 'refs/heads/*');
    const { repoDir, mergeSha } = cloneAndSeed(baseDir, bareDir);

    const config = {
      method: 'push',
      tag: { enabled: true, prefix: 'v' },
      versionFile: { enabled: true, path: 'package.json', fileType: 'package.json' },
      changelog: { enabled: false, file: 'CHANGELOG.md' },
    };

    const ghLogPath = path.join(mkTmpDir('release-ruleset-phase2-log-'), 'gh.log');

    const originalWrite = process.stderr.write.bind(process.stderr);
    let captured = '';
    process.stderr.write = (chunk, ...args) => {
      captured += chunk.toString();
      return originalWrite(chunk, ...args);
    };

    let pathStatus;
    try {
      pathStatus = withFakeGh(ghLogPath, () => {
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
    } finally {
      process.stderr.write = originalWrite;
    }

    assert.equal(pathStatus.versionFile, 'failed', 'branch push is blocked by the ruleset hook');
    assert.equal(pathStatus.tag, 'ok', 'tag push targets a different ref and must still succeed');
    assert.match(captured, /Push rejected by a branch\/tag ruleset/);
    assert.match(captured, /docs\/versioning\.md#protected-branches-and-rulesets/);
  });
});

// ---------------------------------------------------------------------------
// runRelease tag push (line ~938) — block refs/tags/* instead of branches.
// ---------------------------------------------------------------------------

describe('runRelease — ruleset-rejected tag push', () => {
  test('phase 3 failure message carries RULESET_PUSH_HINT', () => {
    const baseDir = mkTmpDir('release-ruleset-phase3-');
    const bareDir = createBareWithRulesetHook(baseDir, 'refs/tags/*');
    const { repoDir, mergeSha } = cloneAndSeed(baseDir, bareDir);

    const config = {
      method: 'push',
      tag: { enabled: true, prefix: 'v' },
      versionFile: { enabled: false, path: '', fileType: '' },
      changelog: { enabled: false, file: 'CHANGELOG.md' },
    };

    const ghLogPath = path.join(mkTmpDir('release-ruleset-phase3-log-'), 'gh.log');

    const originalWrite = process.stderr.write.bind(process.stderr);
    let captured = '';
    process.stderr.write = (chunk, ...args) => {
      captured += chunk.toString();
      return originalWrite(chunk, ...args);
    };

    let pathStatus;
    try {
      pathStatus = withFakeGh(ghLogPath, () => {
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
    } finally {
      process.stderr.write = originalWrite;
    }

    assert.equal(pathStatus.tag, 'failed', 'tag push is blocked by the ruleset hook');
    assert.match(captured, /Push rejected by a branch\/tag ruleset/);
    assert.match(captured, /docs\/versioning\.md#protected-branches-and-rulesets/);
    // method = "pr" only reroutes the bump commit — never a remedy for a tag push.
    assert.doesNotMatch(captured, /method = "pr"/);
  });
});

// ---------------------------------------------------------------------------
// The hint names the secret the scaffolded workflow actually reads.
// ---------------------------------------------------------------------------

describe('ruleset hint — configured pushAuth.secretName', () => {
  test('a rejected branch push names the configured secret, not RELEASE_TOKEN', () => {
    const baseDir = mkTmpDir('release-ruleset-secret-');
    const bareDir = createBareWithRulesetHook(baseDir, 'refs/heads/*');
    const { repoDir } = cloneAndSeed(baseDir, bareDir);
    writeConfig(repoDir, { method: 'push', pushAuth: { secretName: 'MY_BOT' } });

    const logPath = path.join(mkTmpDir('release-ruleset-secret-log-'), 'gh.log');

    assert.throws(
      () => {
        withFakeGh(logPath, () => {
          pushFilesViaPR(repoDir, 'main', 'release/v1.2.3', 'chore(release): 1.2.3');
        });
      },
      (err) => {
        assert.match(err.message, /Set secret MY_BOT to a fine-grained PAT/);
        assert.doesNotMatch(err.message, /secret RELEASE_TOKEN/);
        assert.match(err.message, /already configured/);
        assert.match(err.message, /method = "pr"/, 'branch push still offers the PR route');
        return true;
      }
    );
  });
});

// ---------------------------------------------------------------------------
// config.method 'push-with-secret' normalization
// ---------------------------------------------------------------------------

describe('readVersionConfig — push-with-secret alias', () => {
  test('normalizes method "push-with-secret" to "push"', () => {
    const dir = mkTmpDir('release-config-alias-');
    writeConfig(dir, {
      method: 'push-with-secret',
      tag: { enabled: true, prefix: 'v' },
    });

    const config = readVersionConfig(dir);
    assert.equal(config.method, 'push', 'push-with-secret must normalize to push everywhere config.method is read');
  });
});
