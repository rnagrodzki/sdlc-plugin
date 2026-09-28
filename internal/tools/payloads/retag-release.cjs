#!/usr/bin/env node
/**
 * retag-release.cjs
 * CI script: ensures the current version's git tag points to HEAD on the main branch.
 *
 * Fixes orphaned tags that result from squash-merging a release branch:
 * the tag is created on the feature branch before merge, then becomes
 * unreachable from main after squash. This script moves it to HEAD.
 *
 * Usage (GitHub Actions — runs on push to main):
 *   node .github/scripts/retag-release.cjs
 *
 * Reads: .sdlc-v2/config.toml  (sdlc versioning config)
 * Version source:
 *   `versionFile.enabled: true`  — version read from the configured version
 *     file (package.json, plugin.json, etc.)
 *   `versionFile.enabled: false` (or unset) — version derived from the
 *     latest git tag (no version file)
 *
 * Exit codes: 0 = success / no-op, 1 = error
 *
 * Uses only Node.js built-in modules. No npm install required.
 */

'use strict';

/** @version 9 — retag script version. Bump when behavior changes. */
const RETAG_SCRIPT_VERSION = 9;

const fs   = require('node:fs');
const path = require('node:path');
const os   = require('node:os');
const { execSync } = require('node:child_process');

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function exec(cmd, opts = {}) {
  try {
    return execSync(cmd, { encoding: 'utf8', ...opts }).trim();
  } catch (_) {
    return null;
  }
}

function execOrThrow(cmd, opts = {}) {
  return execSync(cmd, { encoding: 'utf8', stdio: 'pipe', ...opts }).trim();
}

/**
 * Build the hint shown when a push is rejected by a branch/tag ruleset.
 * Kept byte-for-byte identical (copy-pasted, not imported — payloads are
 * standalone scripts) in promote-release.cjs and release-on-main.cjs.
 *   secretName — configured version.pushAuth.secretName, i.e. the secret the
 *                scaffolded workflow actually reads (default RELEASE_TOKEN).
 *   tagPush    — true when the rejected ref is a tag. method = "pr" only
 *                reroutes the version-bump commit, so it is not offered then.
 */
function rulesetPushHint({ secretName, tagPush } = {}) {
  const secret = secretName || 'RELEASE_TOKEN';
  const lines = [
    'Push rejected by a branch/tag ruleset: the pushing identity is not on its bypass list',
    '(GITHUB_TOKEN can never bypass rulesets).',
    'If a GitHub App or PAT is already configured: add that App or user to the bypass list of',
    'every ruleset covering this ref, and check that the PAT has not expired.',
    'Otherwise fix one of:',
    '  1. Set repo variable RELEASE_APP_CLIENT_ID + secret RELEASE_APP_PRIVATE_KEY for a GitHub App',
    '     that is on the ruleset bypass list.',
    `  2. Set secret ${secret} to a fine-grained PAT of a user on the bypass list.`,
  ];
  if (!tagPush) lines.push('  3. Use version.method = "pr" (release commits go through a PR).');
  lines.push('Docs: https://github.com/rnagrodzki/sdlc-plugin/blob/main/docs/versioning.md#protected-branches-and-rulesets');
  return lines.join('\n');
}

/** @returns {string|null} hint when stderr is a ruleset rejection, else null */
function classifyPushError(stderr, hintOpts) {
  return /GH013|Repository rule violations|protected branch/i.test(String(stderr || ''))
    ? rulesetPushHint(hintOpts) : null;
}

/**
 * Tolerant read of version.pushAuth.secretName from .sdlc-v2/config.toml.
 * Returns '' on any failure — it only words an error hint, so it must never
 * throw or exit on the error path.
 */
function readPushAuthSecretName(repoRoot) {
  try {
    const raw = parseSimpleToml(fs.readFileSync(path.join(repoRoot, '.sdlc-v2', 'config.toml'), 'utf8'));
    const s = raw.version && raw.version.pushAuth && raw.version.pushAuth.secretName;
    return typeof s === 'string' ? s : '';
  } catch (_) {
    return '';
  }
}

/**
 * Like execOrThrow, but for `git push` calls: rewrites a ruleset-rejected
 * push's error message to lead with the ruleset hint before rethrowing.
 */
function execPushOrThrow(cmd, opts = {}) {
  try {
    return execOrThrow(cmd, opts);
  } catch (err) {
    const hint = classifyPushError(err.stderr, {
      secretName: readPushAuthSecretName(opts.cwd || process.cwd()),
      tagPush: /refs\/tags\//.test(cmd),
    });
    if (hint) throw new Error(`${hint}\n\n${String(err.stderr).trim()}`);
    throw err;
  }
}

// ---------------------------------------------------------------------------
// Config (self-contained — no external lib dependency)
// ---------------------------------------------------------------------------

/**
 * Minimal TOML reader for the fixed `.sdlc-v2/config.toml` schema. Only
 * supports what that schema actually uses: `[table]` / `[table.sub]`
 * headers and `key = value` lines where value is a double- or
 * single-quoted string, `true`/`false`, or a bare number — no arrays,
 * inline tables, or multi-line strings. Not a general-purpose TOML parser;
 * scoped to the known-fixed `[version]` table shape this script reads.
 */
function parseTomlValue(raw) {
  let s = raw.trim();
  if (s.startsWith('"')) {
    let out = '';
    for (let i = 1; i < s.length; i++) {
      const c = s[i];
      if (c === '\\' && i + 1 < s.length) {
        const n = s[i + 1];
        out += n === 'n' ? '\n' : n === 't' ? '\t' : n;
        i++;
        continue;
      }
      if (c === '"') break;
      out += c;
    }
    return out;
  }
  if (s.startsWith("'")) {
    const end = s.indexOf("'", 1);
    return end >= 0 ? s.slice(1, end) : s.slice(1);
  }
  const hashIdx = s.indexOf('#');
  if (hashIdx >= 0) s = s.slice(0, hashIdx).trim();
  if (s === 'true') return true;
  if (s === 'false') return false;
  if (s !== '' && !Number.isNaN(Number(s))) return Number(s);
  return s;
}

function parseSimpleToml(content) {
  const root = {};
  let current = root;
  for (const rawLine of content.split('\n')) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    const tableMatch = line.match(/^\[([A-Za-z0-9_.-]+)\]$/);
    if (tableMatch) {
      current = root;
      for (const part of tableMatch[1].split('.')) {
        if (typeof current[part] !== 'object' || current[part] === null || Array.isArray(current[part])) {
          current[part] = {};
        }
        current = current[part];
      }
      continue;
    }
    const kvMatch = line.match(/^([A-Za-z0-9_-]+)\s*=\s*(.+)$/);
    if (!kvMatch) continue;
    current[kvMatch[1]] = parseTomlValue(kvMatch[2]);
  }
  return root;
}

/**
 * Read the version section from .sdlc-v2/config.toml. CI script runs in
 * read-only context — never calls verifyAndMigrate. A repo still on a
 * legacy config layout must run `migrate` first; this script does not
 * fall back to any legacy path.
 */
function readVersionConfig(repoRoot) {
  const currentPath = path.join(repoRoot, '.sdlc-v2', 'config.toml');
  if (!fs.existsSync(currentPath)) return null;
  try {
    const root = parseSimpleToml(fs.readFileSync(currentPath, 'utf8'));
    return root.version || null;
  } catch (err) {
    process.stderr.write(`Error parsing .sdlc-v2/config.toml: ${err.message}\n`);
    process.exit(1);
  }
}

// ---------------------------------------------------------------------------
// Version resolution
// ---------------------------------------------------------------------------

function resolveTagFromFile(config, repoRoot) {
  const vf = config.versionFile || {};
  if (!vf.path) {
    process.stderr.write('config.versionFile.path is not set.\n');
    process.exit(1);
  }
  const versionFilePath = path.join(repoRoot, vf.path);
  if (!fs.existsSync(versionFilePath)) {
    process.stderr.write(`Version file not found: ${vf.path}\n`);
    process.exit(1);
  }

  const content = fs.readFileSync(versionFilePath, 'utf8');
  let version = null;

  if (vf.fileType === 'package.json' || vf.fileType === 'plugin.json') {
    try {
      version = JSON.parse(content).version || null;
    } catch (err) {
      process.stderr.write(`Error parsing ${vf.path}: ${err.message}\n`);
      process.exit(1);
    }
  } else if (vf.fileType === 'cargo.toml' || vf.fileType === 'pyproject.toml') {
    const match = content.match(/^\s*version\s*=\s*"([^"]+)"/m);
    version = match ? match[1] : null;
  } else if (vf.fileType === 'pubspec.yaml') {
    const match = content.match(/^\s*version\s*:\s*(\S+)/m);
    version = match ? match[1] : null;
  } else {
    // version-file: plain text
    version = content.trim().split('\n')[0].trim() || null;
  }

  if (!version) {
    process.stderr.write(`Could not read version from ${vf.path}\n`);
    process.exit(1);
  }

  const prefix = config.tag?.prefix || '';
  return `${prefix}${version}`;
}

function resolveTagFromTags(config, repoRoot) {
  const prefix = config.tag?.prefix || '';
  const out = exec('git tag --list --sort=-v:refname', { cwd: repoRoot });
  if (!out) return null;

  const tags = out.split('\n').filter(t => {
    const rest = prefix ? t.startsWith(prefix) ? t.slice(prefix.length) : null : t;
    return rest && /^\d+\.\d+\.\d+/.test(rest);
  });

  return tags.length > 0 ? tags[0] : null;
}

/** True when HEAD is a release-bump commit made by release-on-main/promote-release. */
function isReleaseBumpCommit(subject) {
  return /^chore\(release\):\s/.test(String(subject || ''));
}

// ---------------------------------------------------------------------------
// Tag operations
// ---------------------------------------------------------------------------

function getTagCommit(tag, repoRoot) {
  return exec(`git rev-parse "${tag}^{commit}" 2>/dev/null`, { cwd: repoRoot, shell: true });
}

function isAncestor(commit, repoRoot) {
  // Returns true if commit is an ancestor of (or equal to) HEAD
  // exit code 0 = ancestor, 1 = not ancestor
  try {
    execSync(`git merge-base --is-ancestor "${commit}" HEAD`, { cwd: repoRoot, stdio: 'pipe' });
    return true;
  } catch (_) {
    return false;
  }
}

/**
 * Read the message body of an existing annotated tag.
 * Returns null if the tag doesn't exist or has no message.
 * @param {string} tag
 * @param {string} repoRoot
 * @returns {string|null}
 */
function getTagMessage(tag, repoRoot) {
  const msg = exec(`git tag -l --format='%(contents)' "${tag}"`, { cwd: repoRoot, shell: true });
  return msg ? msg.trim() : null;
}

function retagOnHead(tag, repoRoot) {
  const tagCommit = getTagCommit(tag, repoRoot);

  // Capture original tag message before any deletion so metadata (e.g. Type: hotfix) is preserved
  const originalMessage = tagCommit ? getTagMessage(tag, repoRoot) : null;

  if (tagCommit) {
    if (isAncestor(tagCommit, repoRoot)) {
      console.log(`Tag ${tag} is already reachable from HEAD. Nothing to do.`);
      return;
    }
    console.log(`Tag ${tag} points to ${tagCommit} (not reachable from HEAD). Moving to HEAD...`);
    // Delete remote tag first, then local
    exec(`git push origin ":refs/tags/${tag}"`, { cwd: repoRoot });
    execOrThrow(`git tag -d "${tag}"`, { cwd: repoRoot });
  } else {
    console.log(`Tag ${tag} does not exist. Creating at HEAD...`);
  }

  // Use original tag message if available (preserves metadata such as "Type: hotfix"),
  // otherwise fall back to a generic "Release <tag>" message.
  const tagMessage = originalMessage || `Release ${tag}`;
  const tmpFile = path.join(os.tmpdir(), `retag-msg-${Date.now()}.txt`);
  try {
    fs.writeFileSync(tmpFile, tagMessage, 'utf8');
    execOrThrow(`git tag -a "${tag}" -F "${tmpFile}" HEAD`, { cwd: repoRoot });
  } finally {
    try { fs.unlinkSync(tmpFile); } catch (_) {}
  }

  execPushOrThrow(`git push origin "refs/tags/${tag}"`, { cwd: repoRoot });

  const headSha = exec('git rev-parse --short HEAD', { cwd: repoRoot });
  console.log(`Tag ${tag} now points to HEAD (${headSha}).`);
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

function main() {
  // Deprecation: retag-release.cjs is superseded by release-on-main.cjs, which
  // handles tagging as part of the full post-merge release flow (version bump +
  // changelog + tag + GitHub Release). This script is kept for backward
  // compatibility with existing CI pipelines. New projects should use
  // release-on-main.cjs instead.
  process.stderr.write(
    'Deprecation: retag-release.cjs is superseded by release-on-main.cjs.\n' +
    'Run /setup --ci to scaffold the replacement workflow.\n'
  );

  // KEEP: CI script invoked at repo root — do not change to resolveSdlcRoot()
  const repoRoot = process.cwd();

  const config = readVersionConfig(repoRoot);
  if (!config) {
    console.log('No .sdlc-v2/config.toml found. Skipping retag.');
    process.exit(0);
  }

  // execOrThrow, not exec: a git failure here must surface, not read as
  // "not a release-bump commit" and fall through to retagging.
  const subject = execOrThrow('git log -1 --format=%s', { cwd: repoRoot });
  if (isReleaseBumpCommit(subject)) {
    console.log(`HEAD is a release-bump commit ("${subject}"). Skipping retag.`);
    process.exit(0);
  }

  let tag;
  if (!config.versionFile?.enabled) {
    tag = resolveTagFromTags(config, repoRoot);
    if (!tag) {
      console.log('No existing tags found (tag path). Skipping retag.');
      process.exit(0);
    }
  } else {
    tag = resolveTagFromFile(config, repoRoot);
  }

  console.log(`Expected tag: ${tag}`);
  retagOnHead(tag, repoRoot);

  // Non-blocking changelog advisory — errors here must never fail the script
  try {
    if (config.changelog?.enabled) {
      const changelogFile = config.changelog.file || 'CHANGELOG.md';
      const changelogPath = path.resolve(repoRoot, changelogFile);
      const prefix = config.tag?.prefix || '';
      const version = prefix && tag.startsWith(prefix) ? tag.slice(prefix.length) : tag;

      if (fs.existsSync(changelogPath)) {
        const content = fs.readFileSync(changelogPath, 'utf8');
        const headingRe = new RegExp(`^##\\s+\\[${version.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\]`, 'm');
        if (!headingRe.test(content)) {
          process.stdout.write(
            `⚠  Changelog advisory: no entry found for ${tag} in ${changelogFile}.\n` +
            `   Run /version --changelog on the main branch to add or verify the entry.\n`
          );
        }
      } else {
        process.stdout.write(
          `⚠  Changelog advisory: ${changelogFile} not found but changelog.enabled: true in config.\n` +
          `   Run /version --changelog on the main branch to create it.\n`
        );
      }
    }
  } catch (_) {
    // changelog check failure must never affect exit code
  }
}

// Only run when executed directly (`node retag-release.cjs`) — requiring
// this file as a module (e.g. from tests) must not trigger a live CI run.
if (require.main === module) { main(); }

module.exports = { RETAG_SCRIPT_VERSION, isReleaseBumpCommit, classifyPushError, execPushOrThrow };
