#!/usr/bin/env node
/**
 * verify-release-intent.cjs
 * CI script: pre-merge PR check that validates release intent when a PR
 * carries a release:<level>[-rc] label.
 *
 * Designed to be copied into user projects under `.github/scripts/`.
 *
 * Usage (GitHub Actions — runs on pull_request events):
 *   node .github/scripts/verify-release-intent.cjs
 *
 * Reads: .sdlc/config.json  (sdlc versioning config)
 *
 * Validates:
 *   - <!-- release-notes-start --> / <!-- release-notes-end --> markers
 *     exist in the PR body and bracket non-empty release notes.
 *   - <!-- release-level:<level> --> marker matches the release:<level>
 *     label.
 *   - For release:<level>-rc labels, a <!-- release-pre:rc --> marker is
 *     present.
 *   - The configured version file exists and is parseable (mode "file").
 *   - The computed target version (or next RC number) is not already
 *     tagged on the remote.
 *
 * These markers are injected into the PR body by pr_apply when
 * releaseLevel is set (see internal/tools/pr.go, prReleaseInjectMarkers).
 * This check exists to catch manual PR-body edits (or a hand-applied
 * release:* label with no markers at all) that would otherwise silently
 * break the post-merge release-on-main.cjs script.
 *
 * Exit codes: 0 = success / no-op (no release:* label), 1 = validation
 * failure (diagnostics written to stderr).
 *
 * Uses only Node.js built-in modules + gh CLI. No npm install required.
 */

'use strict';

/** @version 1 — verify-release-intent script version. Bump when behavior changes. */
const VERIFY_RELEASE_INTENT_SCRIPT_VERSION = 1;

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

// Collected validation failures, reported together at the end so a PR
// author sees every problem in one CI run instead of fixing them one at a
// time across repeated pushes.
const failures = [];
function fail(message) {
  failures.push(message);
}

// ---------------------------------------------------------------------------
// Config (self-contained — no external lib dependency; same fallback chain
// as sibling CI scripts: .sdlc/config.json -> .claude/sdlc.json (legacy,
// deprecated) -> .claude/version.json (legacy). CI scripts run in
// read-only context — they never call verifyAndMigrate (issue #232).
// ---------------------------------------------------------------------------

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
    process.stderr.write(`Deprecation: .claude/sdlc.json is the legacy project-config path. Run /setup --migrate to relocate.\n`);
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
// PR discovery
// ---------------------------------------------------------------------------

/**
 * Resolve the PR number for this run. Prefers the pull_request event
 * payload (GITHUB_EVENT_PATH) over gh's own branch-based auto-detection:
 * actions/checkout on a pull_request event leaves HEAD detached at
 * refs/pull/<n>/merge, which has no branch name for `gh pr view` to infer
 * a PR from. Falls back to a PR_NUMBER env var for manual/local runs.
 */
function resolvePRNumber() {
  const eventPath = process.env.GITHUB_EVENT_PATH;
  if (eventPath && fs.existsSync(eventPath)) {
    try {
      const event = JSON.parse(fs.readFileSync(eventPath, 'utf8'));
      if (event.pull_request && event.pull_request.number) {
        return event.pull_request.number;
      }
    } catch (_) {
      // fall through to env fallback
    }
  }
  if (process.env.PR_NUMBER) {
    const n = parseInt(process.env.PR_NUMBER, 10);
    if (!isNaN(n)) return n;
  }
  return null;
}

/**
 * Parse a release:* label into { level, isRC }, or null when it doesn't
 * match the release:(major|minor|patch)(-rc)? shape.
 */
function parseReleaseLabel(label) {
  const m = label.match(/^release:(major|minor|patch)(-rc)?$/);
  if (!m) return null;
  return { level: m[1], isRC: !!m[2] };
}

/**
 * Scan a gh pr view labels array ([{name: "..."}]) for the first label
 * matching release:(major|minor|patch)(-rc)?. Returns
 * { label, level, isRC } or null.
 */
function findReleaseLabel(labels) {
  if (!Array.isArray(labels)) return null;
  for (const lbl of labels) {
    const name = typeof lbl === 'string' ? lbl : lbl.name;
    if (!name) continue;
    const parsed = parseReleaseLabel(name);
    if (parsed) return { label: name, ...parsed };
  }
  return null;
}

// ---------------------------------------------------------------------------
// PR body marker extraction (mirrors internal/tools/pr.go's
// prReleaseInjectMarkers and release-on-main.cjs's extractNotesFromBody)
// ---------------------------------------------------------------------------

/**
 * Extract release notes bracketed by the release-notes markers.
 * Returns null when the markers themselves are missing/malformed, ''
 * when the markers are present but bracket nothing, or the trimmed notes
 * text otherwise.
 */
function extractNotesFromBody(body) {
  if (!body) return null;
  const startTag = '<!-- release-notes-start -->';
  const endTag = '<!-- release-notes-end -->';
  const si = body.indexOf(startTag);
  const ei = body.indexOf(endTag);
  if (si < 0 || ei < 0 || ei <= si) return null;

  let notes = body.slice(si + startTag.length, ei).trim();
  // Strip the "## [version]" heading that prReleaseInjectMarkers writes —
  // only the prose notes matter for the non-empty check.
  notes = notes.replace(/^##\s*\[[^\]]+\]\s*\n*/, '').trim();
  return notes;
}

/**
 * Extract the level named by the <!-- release-level:<level> --> marker,
 * or null when the marker is missing/malformed.
 */
function extractLevelMarker(body) {
  if (!body) return null;
  const m = body.match(/<!-- release-level:(major|minor|patch) -->/);
  return m ? m[1] : null;
}

/**
 * Check for the <!-- release-pre:rc --> marker in the body.
 */
function hasPreReleaseMarker(body) {
  return !!(body && body.includes('<!-- release-pre:rc -->'));
}

// ---------------------------------------------------------------------------
// Version resolution (same patterns as release-on-main.cjs / pr.go's
// prReleaseComputeIntent)
// ---------------------------------------------------------------------------

function readVersionFromFile(config, repoRoot) {
  if (!config.versionFile) {
    return { error: 'config.versionFile is not set and mode is not "tag".' };
  }

  const versionFilePath = path.join(repoRoot, config.versionFile);
  if (!fs.existsSync(versionFilePath)) {
    return { error: `Version file not found: ${config.versionFile}` };
  }

  const content = fs.readFileSync(versionFilePath, 'utf8');
  const fileType = (config.fileType || '').toLowerCase();
  let version = null;

  if (fileType === 'package.json' || fileType === 'plugin.json') {
    try {
      version = JSON.parse(content).version || null;
    } catch (err) {
      return { error: `Error parsing ${config.versionFile}: ${err.message}` };
    }
  } else if (fileType === 'cargo.toml' || fileType === 'pyproject.toml') {
    const match = content.match(/^\s*version\s*=\s*"([^"]+)"/m);
    version = match ? match[1] : null;
  } else if (fileType === 'pubspec.yaml') {
    const match = content.match(/^\s*version\s*:\s*(\S+)/m);
    version = match ? match[1] : null;
  } else {
    // version-file: plain text
    version = content.trim().split('\n')[0].trim() || null;
  }

  if (!version) {
    return { error: `Could not read version from ${config.versionFile}` };
  }
  return { version };
}

function highestSemverTag(repoRoot, tagPrefix) {
  const out = exec('git tag --list --sort=-v:refname', { cwd: repoRoot });
  if (!out) return null;

  for (const t of out.split('\n')) {
    let v = t;
    if (tagPrefix && v.startsWith(tagPrefix)) {
      v = v.slice(tagPrefix.length);
    } else if (tagPrefix) {
      continue; // doesn't match prefix
    }
    v = v.replace(/^v/, '');
    if (/^\d+\.\d+\.\d+$/.test(v)) return v;
  }
  return null;
}

function parseSemver(s) {
  s = s.replace(/^v/, '');
  const core = s.split('-')[0];
  const parts = core.split('.');
  if (parts.length !== 3) return null;
  const nums = parts.map(Number);
  if (nums.some(isNaN)) return null;
  return { major: nums[0], minor: nums[1], patch: nums[2] };
}

function bumpSemver(version, level) {
  const sv = parseSemver(version);
  if (!sv) return null;
  switch (level) {
    case 'major': return `${sv.major + 1}.0.0`;
    case 'minor': return `${sv.major}.${sv.minor + 1}.0`;
    case 'patch': return `${sv.major}.${sv.minor}.${sv.patch + 1}`;
    default: return null;
  }
}

/**
 * Find the next RC number for a given base version by scanning existing
 * tags. Format: <prefix><base>-rc<N> (no dot), e.g. v1.3.0-rc1, v1.3.0-rc2.
 */
function findNextRCNumber(repoRoot, tagPrefix, targetBase) {
  const out = exec('git tag --list', { cwd: repoRoot });
  if (!out) return 1;

  const needle = `${tagPrefix}${targetBase}-rc`;
  let maxRC = 0;
  for (const t of out.split('\n')) {
    if (!t.startsWith(needle)) continue;
    const suffix = t.slice(needle.length);
    const n = parseInt(suffix, 10);
    if (!isNaN(n) && n > maxRC) maxRC = n;
  }
  return maxRC + 1;
}

/**
 * Exact tag-existence check (mirrors gitx.TagExists's
 * `git rev-parse --verify refs/tags/<name>`), not a glob-matching
 * `git tag --list <pattern>` which could partial-match unrelated tags.
 * Stderr is redirected to /dev/null because "tag not found" is the
 * expected, common-case result (the target version usually isn't
 * released yet) — without this, every passing run would print a spurious
 * "fatal: ..." line to the CI log.
 */
function tagExists(repoRoot, tag) {
  return exec(`git rev-parse --verify "refs/tags/${tag}" 2>/dev/null`, { cwd: repoRoot }) !== null;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

function reportAndExit() {
  if (failures.length > 0) {
    process.stderr.write('\nverify-release-intent: FAILED\n');
    for (const f of failures) {
      process.stderr.write(`  - ${f}\n`);
    }
    process.exit(1);
  }
  console.log('verify-release-intent: all checks passed.');
  process.exit(0);
}

function main() {
  // KEEP: CI script invoked at repo root — do not change to resolveSdlcRoot()
  const repoRoot = process.cwd();

  const prNumber = resolvePRNumber();
  if (!prNumber) {
    console.log('Could not determine PR number (no pull_request event payload and no PR_NUMBER env var). Skipping.');
    process.exit(0);
  }

  // Step 1: Read PR labels.
  const labelsRaw = exec(`gh pr view ${prNumber} --json labels`, { cwd: repoRoot });
  if (labelsRaw === null) {
    process.stderr.write(`Could not read labels for PR #${prNumber} via gh pr view.\n`);
    process.exit(1);
  }
  let labelsData;
  try {
    labelsData = JSON.parse(labelsRaw);
  } catch (err) {
    process.stderr.write(`Could not parse gh pr view --json labels output: ${err.message}\n`);
    process.exit(1);
  }

  // Step 2: No release:* label -> skip entirely (defense-in-depth behind
  // the workflow's job-level `if:` label gate).
  const releaseLabel = findReleaseLabel(labelsData.labels);
  if (!releaseLabel) {
    console.log(`PR #${prNumber} has no release:<major|minor|patch>[-rc] label. Skipping release-intent check.`);
    process.exit(0);
  }

  console.log(`PR #${prNumber} carries label: ${releaseLabel.label}`);

  // Step 3: Read PR body.
  const bodyRaw = exec(`gh pr view ${prNumber} --json body`, { cwd: repoRoot });
  if (bodyRaw === null) {
    process.stderr.write(`Could not read body for PR #${prNumber} via gh pr view.\n`);
    process.exit(1);
  }
  let bodyData;
  try {
    bodyData = JSON.parse(bodyRaw);
  } catch (err) {
    process.stderr.write(`Could not parse gh pr view --json body output: ${err.message}\n`);
    process.exit(1);
  }
  const body = bodyData.body || '';

  // Steps 4-5: release-notes markers exist and bracket non-empty notes.
  const notes = extractNotesFromBody(body);
  if (notes === null) {
    fail(
      'Missing <!-- release-notes-start --> / <!-- release-notes-end --> markers in the PR body. ' +
      'These are injected by pr_apply when releaseLevel is set — re-run pr_apply with releaseLevel set, ' +
      'or restore the markers manually.'
    );
  } else if (notes === '') {
    fail(
      'Release notes between <!-- release-notes-start --> and <!-- release-notes-end --> are empty. ' +
      'Add release notes describing this release (pass releaseNotes to pr_apply, or edit the PR body directly).'
    );
  }

  // Step 6: release-level marker matches the label's level.
  const levelMarker = extractLevelMarker(body);
  if (!levelMarker) {
    fail('Missing <!-- release-level:<level> --> marker in the PR body.');
  } else if (levelMarker !== releaseLabel.level) {
    fail(
      `Label "${releaseLabel.label}" declares level "${releaseLabel.level}" but the PR body's ` +
      `<!-- release-level:${levelMarker} --> marker says "${levelMarker}". Re-apply the release label ` +
      'to match, or re-run pr_apply to resynchronize the markers.'
    );
  }

  // Step 7: an -rc label requires the release-pre:rc marker.
  if (releaseLabel.isRC && !hasPreReleaseMarker(body)) {
    fail(
      `Label "${releaseLabel.label}" is an RC label but the PR body is missing the ` +
      '<!-- release-pre:rc --> marker. release-on-main.cjs would not be able to confirm this is an RC release.'
    );
  }

  // Step 8: version config + version file.
  const config = readVersionConfig(repoRoot);
  if (!config) {
    fail(
      'No version config found (.sdlc/config.json ".version" section, or legacy .claude/sdlc.json / ' +
      '.claude/version.json). A release:* label requires a version config to compute the release target.'
    );
    reportAndExit();
    return;
  }

  const tagPrefix = config.tagPrefix || '';
  let currentVersion;
  if (config.mode === 'tag') {
    currentVersion = highestSemverTag(repoRoot, tagPrefix) || '0.0.0';
  } else {
    const resolved = readVersionFromFile(config, repoRoot);
    if (resolved.error) {
      fail(resolved.error);
      reportAndExit();
      return;
    }
    currentVersion = resolved.version;
  }

  // Step 9: compute the target version (or next RC) from level + current
  // version + existing tags.
  const level = releaseLabel.level;
  const bumped = bumpSemver(currentVersion, level);
  if (!bumped) {
    fail(`Could not bump current version "${currentVersion}" at level "${level}".`);
    reportAndExit();
    return;
  }

  // Step 10: fetch tags, then check the target tag doesn't already exist.
  // isRCRelease mirrors release-on-main.cjs's own derivation exactly
  // (label OR marker) so the target tag predicted here matches what
  // release-on-main.cjs will actually compute post-merge.
  exec('git fetch --tags --force', { cwd: repoRoot });

  const isRCRelease = releaseLabel.isRC || hasPreReleaseMarker(body);
  let targetVersion = bumped;
  if (isRCRelease) {
    const rcNum = findNextRCNumber(repoRoot, tagPrefix, bumped);
    targetVersion = `${bumped}-rc${rcNum}`;
  }
  const targetTag = `${tagPrefix}${targetVersion}`;

  if (tagExists(repoRoot, targetTag)) {
    fail(
      `Target tag "${targetTag}" already exists. This usually means the version was already released; ` +
      'bump the release level, or wait for a fresh base version before re-applying the release label.'
    );
  } else {
    console.log(`Target tag "${targetTag}" is available (base ${currentVersion} -> ${targetVersion}).`);
  }

  // Step 11.
  reportAndExit();
}

try {
  main();
} catch (err) {
  process.stderr.write(`Unexpected error in verify-release-intent.cjs: ${err.message}\n${err.stack}\n`);
  process.exit(1);
}

module.exports = { VERIFY_RELEASE_INTENT_SCRIPT_VERSION };
