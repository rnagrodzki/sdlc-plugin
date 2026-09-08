#!/usr/bin/env node
/**
 * check-changelog.cjs
 * CI script: validates that CHANGELOG.md contains an entry for the current version.
 *
 * Only runs when `changelog: true` is set in `.sdlc-v2/config.json`.
 * Designed to be copied into user projects under `.github/scripts/`.
 *
 * Usage (GitHub Actions — runs on push to main or in a PR check):
 *   node .github/scripts/check-changelog.cjs
 *
 * Reads: .sdlc-v2/config.json  (sdlc versioning config)
 * Modes:
 *   "file" — version read from a version file (package.json, plugin.json, etc.)
 *   "tag"  — version derived from the latest git tag (no version file)
 *
 * Exit codes: 0 = pass / skipped, 1 = validation failure, 2 = script error
 *
 * Uses only Node.js built-in modules. No npm install required.
 */

'use strict';

/** @version 5 — check-changelog script version. Bump when behavior changes. */
const CHECK_CHANGELOG_SCRIPT_VERSION = 5;

const fs   = require('node:fs');
const path = require('node:path');
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

// ---------------------------------------------------------------------------
// Config (self-contained — no external lib dependency)
// ---------------------------------------------------------------------------

/**
 * Read the version section from .sdlc-v2/config.json. CI script runs in
 * read-only context — never calls verifyAndMigrate. A repo still on a
 * legacy config layout must run `migrate` first; this script does not
 * fall back to any legacy path.
 */
function readVersionConfig(repoRoot) {
  const currentPath = path.join(repoRoot, '.sdlc-v2', 'config.json');
  if (!fs.existsSync(currentPath)) return null;
  try {
    const config = JSON.parse(fs.readFileSync(currentPath, 'utf8'));
    return config.version || null;
  } catch (_) {
    return null;
  }
}

// ---------------------------------------------------------------------------
// Version resolution
// ---------------------------------------------------------------------------

function resolveVersionFromFile(config, repoRoot) {
  const versionFilePath = path.join(repoRoot, config.versionFile);
  if (!fs.existsSync(versionFilePath)) {
    process.stderr.write(`Warning: version file not found: ${config.versionFile}\n`);
    return null;
  }

  const content = fs.readFileSync(versionFilePath, 'utf8');
  const fileType = (config.fileType || '').toLowerCase();

  if (fileType === 'package.json' || fileType === 'plugin.json') {
    try {
      return JSON.parse(content).version || null;
    } catch (_) {
      process.stderr.write(`Warning: could not parse ${config.versionFile} as JSON\n`);
      return null;
    }
  } else if (fileType === 'cargo.toml' || fileType === 'pyproject.toml') {
    const match = content.match(/^version\s*=\s*"([^"]+)"/m);
    return match ? match[1] : null;
  } else if (fileType === 'pubspec.yaml') {
    const match = content.match(/^version:\s*(.+)$/m);
    return match ? match[1].trim() : null;
  } else {
    // Plain text files: VERSION, version.txt, etc.
    return content.trim() || null;
  }
}

function resolveVersionFromTags(repoRoot) {
  const out = exec('git tag --list --sort=-v:refname', { cwd: repoRoot });
  if (!out) return null;

  const tags = out.split('\n');
  const semverTag = tags.find(t => /^v?\d+\.\d+\.\d+/.test(t));
  if (!semverTag) return null;

  // Strip leading 'v' prefix to get a bare version number
  return semverTag.replace(/^v/, '');
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

function main() {
  // KEEP: CI script invoked at repo root — do not change to resolveSdlcRoot()
  const repoRoot = process.cwd();

  // Step 0: Branch gate — only validate on the main branch.
  // On feature branches and PRs, the changelog entry doesn't exist yet
  // (release-on-main.cjs creates it at merge time), so validation would
  // always fail. Skip silently.
  const currentBranch = (
    process.env.GITHUB_REF_NAME ||
    exec('git rev-parse --abbrev-ref HEAD', { cwd: repoRoot })
  );
  if (currentBranch && currentBranch !== 'main' && currentBranch !== 'master') {
    console.log(`Branch "${currentBranch}" is not main — skipping changelog check.`);
    process.exit(0);
  }
  // Also skip on pull_request events (the check would fire against the PR
  // branch, not main).
  if (process.env.GITHUB_EVENT_NAME === 'pull_request') {
    console.log('pull_request event — skipping changelog check (validated on main only).');
    process.exit(0);
  }

  // Step 1: Read config — exit 0 silently if not present or unparseable
  const config = readVersionConfig(repoRoot);
  if (!config) {
    process.exit(0);
  }

  // Step 1b: Only validate when changelog is explicitly enabled
  if (config.changelog !== true) {
    process.exit(0);
  }

  // Step 2: Determine current version
  let version = null;

  if (config.mode === 'file') {
    version = resolveVersionFromFile(config, repoRoot);
  } else if (config.mode === 'tag') {
    version = resolveVersionFromTags(repoRoot);
  } else {
    // Treat unknown/missing mode the same as 'file' (graceful fallback)
    version = resolveVersionFromFile(config, repoRoot);
  }

  if (!version) {
    process.stderr.write(`Warning: could not determine current version. Skipping changelog check.\n`);
    process.exit(0);
  }

  // Step 3: Read changelog file
  const changelogFile = config.changelogFile || 'CHANGELOG.md';
  const changelogPath = path.join(repoRoot, changelogFile);

  if (!fs.existsSync(changelogPath)) {
    console.log(
      `FAIL: changelog: true in config but ${changelogFile} does not exist. ` +
      `Run /version --changelog to create it.`
    );
    process.exit(1);
  }

  const changelogContent = fs.readFileSync(changelogPath, 'utf8');

  // Step 4: Check for heading "## [<version>]"
  const escapedVersion = version.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const entryRegex = new RegExp(`^##\\s*\\[${escapedVersion}\\]`, 'm');

  if (entryRegex.test(changelogContent)) {
    console.log(`PASS: changelog entry found for v${version}`);
    process.exit(0);
  } else {
    console.log(`FAIL: no changelog entry for v${version} in ${changelogFile}`);
    console.log(`::error file=${changelogFile}::No changelog entry found for v${version}. Run /version --changelog on the main branch to add the missing entry.`);
    process.exit(1);
  }
}

try {
  main();
} catch (err) {
  process.stderr.write(`Unexpected error in check-changelog.cjs: ${err.message}\n${err.stack}\n`);
  process.exit(2);
}

module.exports = { CHECK_CHANGELOG_SCRIPT_VERSION };
