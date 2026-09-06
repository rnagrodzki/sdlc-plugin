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
 * Reads: .claude/version.json  (sdlc versioning config)
 * Modes:
 *   "file" — version read from a version file (package.json, plugin.json, etc.)
 *   "tag"  — version derived from the latest git tag (no version file)
 *
 * Exit codes: 0 = success / no-op, 1 = error
 *
 * Uses only Node.js built-in modules. No npm install required.
 */

'use strict';

/** @version 5 — retag script version. Bump when behavior changes (e.g. .cjs rename for ESM compat). */
const RETAG_SCRIPT_VERSION = 5;

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

// ---------------------------------------------------------------------------
// Config (self-contained — no external lib dependency)
// ---------------------------------------------------------------------------

/**
 * Read the version section from .sdlc/config.json, falling back to legacy
 * .claude/sdlc.json (with stderr deprecation warning), and finally to legacy
 * .claude/version.json. CI scripts run in read-only context — they never
 * call verifyAndMigrate (issue #232).
 */
function readVersionConfig(repoRoot) {
  // Primary: .sdlc/config.json → .version (issue #231)
  const newPath = path.join(repoRoot, '.sdlc', 'config.json');
  if (fs.existsSync(newPath)) {
    try {
      const config = JSON.parse(fs.readFileSync(newPath, 'utf8'));
      return config.version || null;
    } catch (err) {
      process.stderr.write(`Error parsing .sdlc/config.json: ${err.message}\n`);
      process.exit(1);
    }
  }

  // Fallback: legacy .claude/sdlc.json
  const legacyUnifiedPath = path.join(repoRoot, '.claude', 'sdlc.json');
  if (fs.existsSync(legacyUnifiedPath)) {
    process.stderr.write(`Deprecation: .claude/sdlc.json is the legacy project-config path. Run /setup-sdlc --migrate to relocate.\n`);
    try {
      const config = JSON.parse(fs.readFileSync(legacyUnifiedPath, 'utf8'));
      return config.version || null;
    } catch (err) {
      process.stderr.write(`Error parsing .claude/sdlc.json: ${err.message}\n`);
      process.exit(1);
    }
  }

  // Legacy fallback: .claude/version.json
  const legacyPath = path.join(repoRoot, '.claude', 'version.json');
  if (fs.existsSync(legacyPath)) {
    try {
      return JSON.parse(fs.readFileSync(legacyPath, 'utf8'));
    } catch (err) {
      process.stderr.write(`Error parsing .claude/version.json: ${err.message}\n`);
      process.exit(1);
    }
  }

  return null;
}

// ---------------------------------------------------------------------------
// Version resolution
// ---------------------------------------------------------------------------

function resolveTagFromFile(config, repoRoot) {
  const versionFilePath = path.join(repoRoot, config.versionFile);
  if (!fs.existsSync(versionFilePath)) {
    process.stderr.write(`Version file not found: ${config.versionFile}\n`);
    process.exit(1);
  }

  const content = fs.readFileSync(versionFilePath, 'utf8');
  let version = null;

  if (config.fileType === 'package.json' || config.fileType === 'plugin.json') {
    try {
      version = JSON.parse(content).version || null;
    } catch (err) {
      process.stderr.write(`Error parsing ${config.versionFile}: ${err.message}\n`);
      process.exit(1);
    }
  } else if (config.fileType === 'cargo.toml' || config.fileType === 'pyproject.toml') {
    const match = content.match(/^\s*version\s*=\s*"([^"]+)"/m);
    version = match ? match[1] : null;
  } else if (config.fileType === 'pubspec.yaml') {
    const match = content.match(/^\s*version\s*:\s*(\S+)/m);
    version = match ? match[1] : null;
  } else {
    // version-file: plain text
    version = content.trim().split('\n')[0].trim() || null;
  }

  if (!version) {
    process.stderr.write(`Could not read version from ${config.versionFile}\n`);
    process.exit(1);
  }

  const prefix = config.tagPrefix || '';
  return `${prefix}${version}`;
}

function resolveTagFromTags(config, repoRoot) {
  const prefix = config.tagPrefix || '';
  const out = exec('git tag --list --sort=-v:refname', { cwd: repoRoot });
  if (!out) return null;

  const tags = out.split('\n').filter(t => {
    const rest = prefix ? t.startsWith(prefix) ? t.slice(prefix.length) : null : t;
    return rest && /^\d+\.\d+\.\d+/.test(rest);
  });

  return tags.length > 0 ? tags[0] : null;
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

  execOrThrow(`git push origin "refs/tags/${tag}"`, { cwd: repoRoot });

  const headSha = exec('git rev-parse --short HEAD', { cwd: repoRoot });
  console.log(`Tag ${tag} now points to HEAD (${headSha}).`);
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

function main() {
  // KEEP: CI script invoked at repo root — do not change to resolveSdlcRoot()
  const repoRoot = process.cwd();

  const config = readVersionConfig(repoRoot);
  if (!config) {
    console.log('No .claude/version.json found. Skipping retag.');
    process.exit(0);
  }

  let tag;
  if (config.mode === 'tag') {
    tag = resolveTagFromTags(config, repoRoot);
    if (!tag) {
      console.log('No existing tags found (tag mode). Skipping retag.');
      process.exit(0);
    }
  } else {
    // "file" mode (default)
    tag = resolveTagFromFile(config, repoRoot);
  }

  console.log(`Expected tag: ${tag}`);
  retagOnHead(tag, repoRoot);

  // Non-blocking changelog advisory — errors here must never fail the script
  try {
    if (config.changelog === true) {
      const changelogFile = config.changelogFile || 'CHANGELOG.md';
      const changelogPath = path.resolve(repoRoot, changelogFile);
      const prefix = config.tagPrefix || '';
      const version = prefix && tag.startsWith(prefix) ? tag.slice(prefix.length) : tag;

      if (fs.existsSync(changelogPath)) {
        const content = fs.readFileSync(changelogPath, 'utf8');
        const headingRe = new RegExp(`^##\\s+\\[${version.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\]`, 'm');
        if (!headingRe.test(content)) {
          process.stdout.write(
            `⚠  Changelog advisory: no entry found for ${tag} in ${changelogFile}.\n` +
            `   Run /version-sdlc --changelog on the main branch to add or verify the entry.\n`
          );
        }
      } else {
        process.stdout.write(
          `⚠  Changelog advisory: ${changelogFile} not found but changelog: true in config.\n` +
          `   Run /version-sdlc --changelog on the main branch to create it.\n`
        );
      }
    }
  } catch (_) {
    // changelog check failure must never affect exit code
  }
}

main();

module.exports = { RETAG_SCRIPT_VERSION };
