'use strict';

/**
 * End-to-end tests for promote-release.cjs bump delivery (deliverBump):
 * version.method "push" / "push-with-secret" / "pr", the non-fatal
 * auto-merge path, and the GH013 ruleset-rejection hint.
 *
 * Each test runs the real script (`node promote-release.cjs patch`) in a
 * temp clone of a real bare remote. git is real; `gh` is a fake executable
 * prepended to PATH that records its argv and never touches GitHub.
 * Uses Node's built-in test runner (node --test) — no npm install required.
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execSync, spawnSync } = require('node:child_process');

const { RULESET_PUSH_HINT } = require('../promote-release.cjs');

const SCRIPT = path.join(__dirname, '..', 'promote-release.cjs');
const PR_URL = 'https://github.com/example/repo/pull/42';

function git(cmd, cwd) {
  return execSync(`git ${cmd}`, { cwd, encoding: 'utf8', stdio: 'pipe' }).trim();
}

/** Fake gh: logs argv, prints a PR URL for `pr create`, can fail `pr merge`. */
function writeFakeGh(baseDir, logPath) {
  const binDir = path.join(baseDir, 'bin');
  fs.mkdirSync(binDir);
  const script = `#!/bin/sh
echo "$@" >> "${logPath}"
case "$1 $2" in
  "pr create") echo "${PR_URL}"; exit 0 ;;
  "pr merge") if [ -n "$FAKE_GH_MERGE_FAIL" ]; then echo "auto-merge is not allowed" >&2; exit 1; fi; exit 0 ;;
  "release view") exit 1 ;;
esac
exit 0
`;
  fs.writeFileSync(path.join(binDir, 'gh'), script, 'utf8');
  fs.chmodSync(path.join(binDir, 'gh'), 0o755);
  return binDir;
}

function installRulesetHook(bareDir) {
  const hookPath = path.join(bareDir, 'hooks', 'pre-receive');
  fs.writeFileSync(hookPath, `#!/bin/sh
while read oldval newval ref; do
  case "$ref" in
    refs/heads/*)
      echo "remote: error: GH013: Repository rule violations found for $ref." >&2
      echo "remote: - Changes must be made through a pull request." >&2
      exit 1
      ;;
  esac
done
exit 0
`, 'utf8');
  fs.chmodSync(hookPath, 0o755);
}

/**
 * Bare remote + working clone with stable tag v1.0.0 and RC tag
 * v1.0.1-rc1 (both pushed), configured with the given version.method.
 */
function setup(method) {
  const baseDir = fs.mkdtempSync(path.join(os.tmpdir(), 'promote-delivery-'));
  const bareDir = path.join(baseDir, 'origin.git');
  const repoDir = path.join(baseDir, 'work');
  git(`init --bare -q "${bareDir}"`, baseDir);
  git('symbolic-ref HEAD refs/heads/main', bareDir);
  git(`clone -q "${bareDir}" "${repoDir}"`, baseDir);
  git('checkout -q -B main', repoDir);
  git('config user.email "test@example.com"', repoDir);
  git('config user.name "Test"', repoDir);
  git('config commit.gpgsign false', repoDir);
  git('config tag.gpgsign false', repoDir);

  fs.mkdirSync(path.join(repoDir, '.sdlc-v2'));
  const methodLine = method ? `method = "${method}"\n` : '';
  fs.writeFileSync(path.join(repoDir, '.sdlc-v2', 'config.toml'),
    `[version]\n${methodLine}\n[version.tag]\nprefix = "v"\n\n` +
    `[version.versionFile]\nenabled = true\npath = "package.json"\nfileType = "package.json"\n`, 'utf8');
  fs.writeFileSync(path.join(repoDir, 'package.json'), '{\n  "version": "1.0.0"\n}\n', 'utf8');
  git('add .', repoDir);
  git('commit -q -m "initial"', repoDir);
  git('tag -a v1.0.0 -m "v1.0.0"', repoDir);
  fs.writeFileSync(path.join(repoDir, 'feature.txt'), 'x', 'utf8');
  git('add feature.txt', repoDir);
  git('commit -q -m "feat: x"', repoDir);
  git('tag -a v1.0.1-rc1 -m "rc notes"', repoDir);
  git('push -q origin main --tags', repoDir);

  const rcSha = git('rev-parse HEAD', repoDir);
  const logPath = path.join(baseDir, 'gh.log');
  const binDir = writeFakeGh(baseDir, logPath);
  return { baseDir, bareDir, repoDir, rcSha, logPath, binDir };
}

function runPromote(ctx, extraEnv = {}) {
  const res = spawnSync('node', [SCRIPT, 'patch'], {
    cwd: ctx.repoDir,
    encoding: 'utf8',
    env: {
      ...process.env,
      PATH: `${ctx.binDir}${path.delimiter}${process.env.PATH}`,
      GITHUB_REF_NAME: 'main',
      FAKE_GH_MERGE_FAIL: '',
      ...extraEnv,
    },
  });
  const ghLog = fs.existsSync(ctx.logPath) ? fs.readFileSync(ctx.logPath, 'utf8') : '';
  return { ...res, ghLog };
}

function remoteRef(bareDir, ref) {
  try {
    return git(`rev-parse --verify -q "${ref}^{commit}"`, bareDir);
  } catch (_) {
    return null;
  }
}

function remoteVersion(bareDir, ref) {
  return JSON.parse(git(`show "${ref}:package.json"`, bareDir)).version;
}

describe('deliverBump — push methods', () => {
  for (const method of ['push', 'push-with-secret', null]) {
    test(`method=${method || '(unset)'} pushes the bump to main and tags the RC SHA`, () => {
      const ctx = setup(method);
      const r = runPromote(ctx);
      assert.equal(r.status, 0, r.stderr);
      assert.equal(remoteVersion(ctx.bareDir, 'refs/heads/main'), '1.0.1');
      assert.equal(remoteRef(ctx.bareDir, 'refs/heads/release/v1.0.1'), null);
      assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.0.1'), ctx.rcSha);
      assert.doesNotMatch(r.ghLog, /pr create/);
    });
  }
});

describe('deliverBump — pr method', () => {
  test('pushes to release/<tag>, leaves main unchanged, opens a no-release PR with auto-merge', () => {
    const ctx = setup('pr');
    const mainBefore = remoteRef(ctx.bareDir, 'refs/heads/main');
    const r = runPromote(ctx);
    assert.equal(r.status, 0, r.stderr);

    assert.equal(remoteRef(ctx.bareDir, 'refs/heads/main'), mainBefore);
    assert.equal(remoteVersion(ctx.bareDir, 'refs/heads/release/v1.0.1'), '1.0.1');
    assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.0.1'), ctx.rcSha);

    const lines = r.ghLog.split('\n');
    const create = lines.find((l) => l.startsWith('pr create'));
    assert.ok(create, r.ghLog);
    assert.match(create, /--base main/);
    assert.match(create, /--head release\/v1\.0\.1/);
    assert.match(create, /--label no-release/);
    const merge = lines.find((l) => l.startsWith('pr merge'));
    assert.equal(merge, `pr merge ${PR_URL} --auto --squash --delete-branch`);
  });

  test('auto-merge failure is non-fatal and logs a warning naming the PR', () => {
    const ctx = setup('pr');
    const r = runPromote(ctx, { FAKE_GH_MERGE_FAIL: '1' });
    assert.equal(r.status, 0, r.stderr);
    assert.match(r.stdout, new RegExp(`WARNING: auto-merge not enabled for ${PR_URL.replace(/[.]/g, '\\.')}`));
    assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.0.1'), ctx.rcSha);
    assert.match(r.ghLog, /release create v1\.0\.1/);
  });
});

describe('deliverBump — GH013 ruleset rejection', () => {
  test('bump push rejected by a ruleset exits 1 with RULESET_PUSH_HINT and pushes no tag', () => {
    const ctx = setup('push');
    installRulesetHook(ctx.bareDir);
    const r = runPromote(ctx);
    assert.equal(r.status, 1);
    assert.ok(r.stderr.includes(RULESET_PUSH_HINT), r.stderr);
    assert.match(r.stderr, /GH013/);
    assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.0.1'), null);
    assert.doesNotMatch(r.ghLog, /release create/);
  });
});
