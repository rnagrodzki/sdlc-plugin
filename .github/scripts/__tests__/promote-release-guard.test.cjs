'use strict';

/**
 * Tests for promote-release.cjs safety guards:
 *   - resolvePromotionTarget: the target must not be below the active RC series.
 *   - RELEASE_BRANCH / GITHUB_REF_NAME branch check and SAFE_REF allowlist.
 *   - Release workflow dispatch only when .github/workflows/release.yml exists.
 *   - LEVEL env as the level source, with the argv fallback.
 *
 * End-to-end cases run the real script in a temp clone of a real bare
 * remote. git is real; `gh` is a fake executable prepended to PATH that
 * records its argv and never touches GitHub.
 * Uses Node's built-in test runner (node --test) — no npm install required.
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execSync, spawnSync } = require('node:child_process');

const { resolvePromotionTarget, SAFE_REF } = require('../promote-release.cjs');

const SCRIPT = path.join(__dirname, '..', 'promote-release.cjs');

function git(cmd, cwd) {
  return execSync(`git ${cmd}`, { cwd, encoding: 'utf8', stdio: 'pipe' }).trim();
}

/** Fake gh: logs argv; `release view` fails so notes fall back to the tag message. */
function writeFakeGh(baseDir, logPath) {
  const binDir = path.join(baseDir, 'bin');
  fs.mkdirSync(binDir);
  fs.writeFileSync(path.join(binDir, 'gh'), `#!/bin/sh
echo "$@" >> "${logPath}"
case "$1 $2" in
  "release view") exit 1 ;;
esac
exit 0
`, 'utf8');
  fs.chmodSync(path.join(binDir, 'gh'), 0o755);
  return binDir;
}

/**
 * Bare remote + working clone with stable tag v1.4.9 and RC tag
 * v1.4.10-rc1 (both pushed). withReleaseYml adds a committed
 * .github/workflows/release.yml.
 */
function setup({ withReleaseYml = false } = {}) {
  const baseDir = fs.mkdtempSync(path.join(os.tmpdir(), 'promote-guard-'));
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
  fs.writeFileSync(path.join(repoDir, '.sdlc-v2', 'config.toml'),
    '[version]\nmethod = "push"\n\n[version.tag]\nprefix = "v"\n\n' +
    '[version.versionFile]\nenabled = true\npath = "package.json"\nfileType = "package.json"\n', 'utf8');
  fs.writeFileSync(path.join(repoDir, 'package.json'), '{\n  "version": "1.4.9"\n}\n', 'utf8');
  if (withReleaseYml) {
    fs.mkdirSync(path.join(repoDir, '.github', 'workflows'), { recursive: true });
    fs.writeFileSync(path.join(repoDir, '.github', 'workflows', 'release.yml'), 'name: Release\n', 'utf8');
  }
  git('add .', repoDir);
  git('commit -q -m "initial"', repoDir);
  git('tag -a v1.4.9 -m "v1.4.9"', repoDir);
  fs.writeFileSync(path.join(repoDir, 'feature.txt'), 'x', 'utf8');
  git('add feature.txt', repoDir);
  git('commit -q -m "fix: x"', repoDir);
  git('tag -a v1.4.10-rc1 -m "rc notes"', repoDir);
  git('push -q origin main --tags', repoDir);

  const rcSha = git('rev-parse HEAD', repoDir);
  const logPath = path.join(baseDir, 'gh.log');
  const binDir = writeFakeGh(baseDir, logPath);
  return { baseDir, bareDir, repoDir, rcSha, logPath, binDir };
}

/** Run the script. args = argv after the script path; env overrides win. */
function runPromote(ctx, args, extraEnv = {}) {
  const res = spawnSync('node', [SCRIPT, ...args], {
    cwd: ctx.repoDir,
    encoding: 'utf8',
    env: {
      ...process.env,
      PATH: `${ctx.binDir}${path.delimiter}${process.env.PATH}`,
      GITHUB_REF_NAME: 'main',
      LEVEL: '',
      RELEASE_BRANCH: '',
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

describe('resolvePromotionTarget', () => {
  test('level that lands on the series returns targetBase', () => {
    assert.deepEqual(resolvePromotionTarget('1.4.9', '1.4.10', 'patch', 'v'), { targetBase: '1.4.10' });
    assert.deepEqual(resolvePromotionTarget('1.4.9', '1.5.0', 'minor', 'v'), { targetBase: '1.5.0' });
    assert.deepEqual(resolvePromotionTarget('1.4.9', '2.0.0', 'major', ''), { targetBase: '2.0.0' });
  });

  test('higher-than-series level returns the target and a notice', () => {
    const r = resolvePromotionTarget('1.4.9', '1.4.10', 'minor', 'v');
    assert.equal(r.targetBase, '1.5.0');
    assert.equal(r.error, undefined);
    assert.match(r.notice, /v1\.5\.0, above the active RC series 1\.4\.10/);
  });

  test('major above series returns a notice', () => {
    const r = resolvePromotionTarget('0.3.3', '0.3.4', 'major', 'v');
    assert.equal(r.targetBase, '1.0.0');
    assert.equal(r.error, undefined);
    assert.match(r.notice, /v1\.0\.0, above the active RC series 0\.3\.4/);
  });

  test('minor above series names the target tag and the series in the notice', () => {
    const r = resolvePromotionTarget('0.3.3', '0.3.4', 'minor', 'v');
    assert.equal(r.targetBase, '0.4.0');
    assert.match(r.notice, /v0\.4\.0/);
    assert.match(r.notice, /0\.3\.4/);
  });

  test('series not above stable: nothing to promote', () => {
    for (const series of ['0.3.1', '0.3.2']) {
      const r = resolvePromotionTarget('0.3.2', series, 'patch', 'v');
      assert.equal(r.targetBase, undefined);
      assert.ok(r.error.startsWith('Nothing to promote:'), r.error);
    }
  });

  test('lower-than-series level is an error that names the matching level', () => {
    const r = resolvePromotionTarget('1.4.9', '2.0.0', 'patch', 'v');
    assert.match(r.error, /Chosen level "patch" produces v1\.4\.10, but the active RC series is 2\.0\.0\. use level "major"/);
  });

  test('lower-than-series level names the minor level when minor reaches the series', () => {
    // A series equal to the minor target, and a series between the patch and
    // the minor target: minor is the first level that reaches both.
    for (const series of ['1.5.0', '1.4.12']) {
      const r = resolvePromotionTarget('1.4.9', series, 'patch', 'v');
      assert.match(r.error, /use level "minor"$/, r.error);
    }
  });

  test('no level reaches the series: error says so', () => {
    const r = resolvePromotionTarget('1.4.9', '3.0.0', 'patch', 'v');
    assert.equal(r.error,
      'Chosen level "patch" produces v1.4.10, but the active RC series is 3.0.0. no level reaches 3.0.0 from 1.4.9');
  });

  test('no stable tag (0.0.0) promotes the first series', () => {
    assert.deepEqual(resolvePromotionTarget('0.0.0', '0.1.0', 'minor', 'v'), { targetBase: '0.1.0' });
  });
});

describe('SAFE_REF', () => {
  test('accepts normal branch names, rejects shell metacharacters', () => {
    for (const ok of ['main', 'release/1.x', 'feat_a-b.c']) assert.ok(SAFE_REF.test(ok), ok);
    for (const bad of ['main$(id)', 'a b', 'a;b', 'a"b', "a'b", 'a`b', '']) assert.ok(!SAFE_REF.test(bad), bad);
  });
});

describe('promote-release — target guard (end to end)', () => {
  test('stable v1.4.9, series 1.4.10, level minor tags v1.5.0 at the RC commit', () => {
    const ctx = setup();
    const r = runPromote(ctx, ['minor']);
    assert.equal(r.status, 0, r.stderr);
    assert.ok(r.stdout.includes('NOTICE:'), r.stdout);
    assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.5.0'), ctx.rcSha);
    assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.4.10'), null);
    const remotePackageJson = git('show main:package.json', ctx.bareDir);
    assert.match(remotePackageJson, /"version":\s*"1\.5\.0"/, remotePackageJson);
  });

  test('level patch tags v1.4.10 at the RC commit', () => {
    const ctx = setup();
    const r = runPromote(ctx, ['patch']);
    assert.equal(r.status, 0, r.stderr);
    assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.4.10'), ctx.rcSha);
  });

  test('second promotion of the same series exits 1 and pushes no tag', () => {
    const ctx = setup();
    const first = runPromote(ctx, ['patch']);
    assert.equal(first.status, 0, first.stderr);
    const mainAfterFirst = remoteRef(ctx.bareDir, 'refs/heads/main');
    const second = runPromote(ctx, ['patch']);
    assert.equal(second.status, 1, second.stdout);
    assert.ok(second.stderr.includes('Nothing to promote'), second.stderr);
    assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.4.11'), null);
    assert.equal(remoteRef(ctx.bareDir, 'refs/heads/main'), mainAfterFirst);
  });
});

describe('promote-release — branch checks', () => {
  test('RELEASE_BRANCH=main dispatched from feat/x exits 1 with no push', () => {
    const ctx = setup();
    const mainBefore = remoteRef(ctx.bareDir, 'refs/heads/main');
    const r = runPromote(ctx, ['patch'], { RELEASE_BRANCH: 'main', GITHUB_REF_NAME: 'feat/x' });
    assert.equal(r.status, 1, r.stdout);
    assert.match(r.stderr, /promote-release must run from "main"/);
    assert.equal(remoteRef(ctx.bareDir, 'refs/heads/main'), mainBefore);
    assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.4.10'), null);
    assert.equal(r.ghLog, '');
  });

  test('RELEASE_BRANCH with shell metacharacters exits 1 with no push', () => {
    const ctx = setup();
    const mainBefore = remoteRef(ctx.bareDir, 'refs/heads/main');
    const r = runPromote(ctx, ['patch'], { RELEASE_BRANCH: 'main$(id)', GITHUB_REF_NAME: 'main$(id)' });
    assert.equal(r.status, 1, r.stdout);
    assert.match(r.stderr, /invalid branch name/);
    assert.equal(remoteRef(ctx.bareDir, 'refs/heads/main'), mainBefore);
    assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.4.10'), null);
    assert.equal(r.ghLog, '');
  });
});

describe('promote-release — release workflow dispatch', () => {
  test('no release.yml: no dispatch, logs the skip', () => {
    const ctx = setup();
    const r = runPromote(ctx, ['patch']);
    assert.equal(r.status, 0, r.stderr);
    assert.doesNotMatch(r.ghLog, /workflow run/);
    assert.match(r.stdout, /skipping binary build dispatch/);
  });

  test('with release.yml: exactly one dispatch at the final tag', () => {
    const ctx = setup({ withReleaseYml: true });
    const r = runPromote(ctx, ['patch']);
    assert.equal(r.status, 0, r.stderr);
    const calls = r.ghLog.split('\n').filter((l) => l.startsWith('workflow run release.yml'));
    assert.equal(calls.length, 1, r.ghLog);
    assert.match(calls[0], /--ref v1\.4\.10/);
    assert.match(r.stdout, /Dispatched Release workflow at v1\.4\.10/);
  });
});

describe('promote-release — level source', () => {
  test('LEVEL=patch with no argv promotes', () => {
    const ctx = setup();
    const r = runPromote(ctx, [], { LEVEL: 'patch' });
    assert.equal(r.status, 0, r.stderr);
    assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.4.10'), ctx.rcSha);
  });

  test('argv level works when LEVEL is unset', () => {
    const ctx = setup();
    const env = { ...process.env };
    delete env.LEVEL;
    const res = spawnSync('node', [SCRIPT, 'patch'], {
      cwd: ctx.repoDir,
      encoding: 'utf8',
      env: { ...env, PATH: `${ctx.binDir}${path.delimiter}${process.env.PATH}`, GITHUB_REF_NAME: 'main', RELEASE_BRANCH: '' },
    });
    assert.equal(res.status, 0, res.stderr);
    assert.equal(remoteRef(ctx.bareDir, 'refs/tags/v1.4.10'), ctx.rcSha);
  });

  test('no LEVEL and no argv prints usage and exits 1', () => {
    const ctx = setup();
    const r = runPromote(ctx, []);
    assert.equal(r.status, 1);
    assert.match(r.stderr, /Usage: node promote-release\.cjs <level>/);
  });
});
