#!/usr/bin/env node
/**
 * release-on-main.cjs
 * CI script: creates a release (tag + GitHub Release) when a PR with a
 * release:* label is merged to main. Three independently toggleable paths
 * (tag, versionFile, changelog) share only the bump policy.
 *
 * Designed to be copied into user projects under `.github/scripts/`.
 *
 * Usage (GitHub Actions — runs on push to main):
 *   node .github/scripts/release-on-main.cjs
 *
 * Reads: .sdlc-v2/config.json  (sdlc versioning config)
 *
 * Config shape (nested sub-objects under version):
 *   version.tag       { enabled, prefix }
 *   version.versionFile  { enabled, path, fileType }
 *   version.changelog { enabled, file }
 *   version.method    "push" | "pr" (governs file-writing paths)
 *
 * 4-phase execution:
 *   Phase 1: Read-only — config, PR, label, metadata, version resolution
 *   Phase 2: File writes — versionFile + changelog share one commit (skip for RC)
 *   Phase 3: Tag — create tag + GitHub Release (NEVER blocked by phase 2)
 *   Phase 4: PR delivery — open PR when method=="pr" (skip for RC)
 *   EXIT: per-path status, exit 1 if any failed
 *
 * Tag creation never depends on file-write success. Each path checks
 * idempotency independently — no early return from the whole script.
 *
 * Decision: RC releases skip phase 2 entirely — no changelog or version
 * file writes. This differs from v6 which delivered RC changelog entries.
 *
 * Exit codes: 0 = success / no-op (no release label), 1 = any path failed
 *
 * Uses only Node.js built-in modules + gh CLI. No npm install required.
 */

'use strict';

/** @version 7 — release-on-main script version. Bump when behavior changes. */
const RELEASE_ON_MAIN_SCRIPT_VERSION = 7;

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
 * Write content to a temp file, execute fn(tmpPath), then clean up.
 * Avoids shell injection by never interpolating user content into commands.
 */
function withTmpFile(content, fn) {
  const tmp = path.join(os.tmpdir(), `release-on-main-${Date.now()}-${Math.random().toString(36).slice(2)}.txt`);
  try {
    fs.writeFileSync(tmp, content, 'utf8');
    return fn(tmp);
  } finally {
    try { fs.unlinkSync(tmp); } catch (_) {}
  }
}

// ---------------------------------------------------------------------------
// Config (self-contained — no external lib dependency)
// ---------------------------------------------------------------------------

/**
 * Read the version section from .sdlc-v2/config.json and validate the
 * nested config shape. Old flat config shape triggers a hard error.
 *
 * Returns the validated config object with normalized sub-objects, or null
 * if no config file exists.
 */
function readVersionConfig(repoRoot) {
  const currentPath = path.join(repoRoot, '.sdlc-v2', 'config.json');
  if (!fs.existsSync(currentPath)) return null;
  let config;
  try {
    const raw = JSON.parse(fs.readFileSync(currentPath, 'utf8'));
    config = raw.version;
  } catch (err) {
    process.stderr.write(`Error parsing .sdlc-v2/config.json: ${err.message}\n`);
    process.exit(1);
  }
  if (!config) return null;

  // Reject old flat config shape — no backward compat, no migration.
  if (typeof config.versionFile === 'string' ||
      'mode' in config ||
      'changelogMethod' in config ||
      typeof config.changelog === 'boolean' ||
      'rcAutoContinue' in config) {
    process.stderr.write(
      'Error: version config uses the old flat shape. ' +
      'Run `/setup --only version` to migrate to the new nested format.\n'
    );
    process.exit(1);
  }

  // Normalize sub-objects — missing sub-object = {enabled: false}.
  const tag = config.tag && typeof config.tag === 'object'
    ? { enabled: !!config.tag.enabled, prefix: config.tag.prefix || '' }
    : { enabled: false, prefix: '' };

  const versionFile = config.versionFile && typeof config.versionFile === 'object'
    ? { enabled: !!config.versionFile.enabled, path: config.versionFile.path || '', fileType: config.versionFile.fileType || '' }
    : { enabled: false, path: '', fileType: '' };

  const changelog = config.changelog && typeof config.changelog === 'object'
    ? { enabled: !!config.changelog.enabled, file: config.changelog.file || 'CHANGELOG.md' }
    : { enabled: false, file: 'CHANGELOG.md' };

  // Validate: at least one of tag or versionFile must be enabled.
  if (!tag.enabled && !versionFile.enabled) {
    process.stderr.write('Error: at least one of version.tag.enabled or version.versionFile.enabled must be true.\n');
    process.exit(1);
  }

  const method = config.method || 'push';

  return {
    preRelease: config.preRelease || '',
    preReleasePolicy: config.preReleasePolicy || 'continue-rc',
    method,
    tag,
    versionFile,
    changelog,
  };
}

// ---------------------------------------------------------------------------
// PR discovery
// ---------------------------------------------------------------------------

/**
 * Find the merged PR that introduced HEAD, and extract its labels and body.
 * Returns null when no merged PR matches HEAD's SHA.
 */
function findMergedPR(repoRoot) {
  const headSha = exec('git rev-parse HEAD', { cwd: repoRoot });
  if (!headSha) {
    process.stderr.write('Could not determine HEAD SHA.\n');
    process.exit(1);
  }

  const raw = exec(
    `gh pr list --state merged --search "${headSha}" --json number,body,labels --limit 1`,
    { cwd: repoRoot }
  );
  if (!raw) return null;

  let prs;
  try {
    prs = JSON.parse(raw);
  } catch (_) {
    return null;
  }

  return prs.length > 0 ? prs[0] : null;
}

/**
 * Extract the release label from PR labels.
 * Labels are objects: [{name: "release:minor"}, ...].
 * Returns the matched label string or null.
 */
function findReleaseLabel(labels) {
  if (!Array.isArray(labels)) return null;
  const re = /^release:(major|minor|patch)(-rc)?$/;
  for (const lbl of labels) {
    const name = typeof lbl === 'string' ? lbl : lbl.name;
    if (name && re.test(name)) return name;
  }
  return null;
}

/**
 * Parse the release label into { level, isRC }.
 * E.g. "release:minor-rc" → { level: "minor", isRC: true }
 */
function parseReleaseLabel(label) {
  const m = label.match(/^release:(major|minor|patch)(-rc)?$/);
  return { level: m[1], isRC: !!m[2] };
}

// ---------------------------------------------------------------------------
// PR body marker extraction
// ---------------------------------------------------------------------------

/**
 * Extract release notes from PR body markers injected by pr_apply.
 * Markers: <!-- release-notes-start --> ... <!-- release-notes-end -->
 * The notes block may start with a "## [version]" heading — strip it,
 * because we recompute the version at merge time.
 */
function extractNotesFromBody(body) {
  if (!body) return '';
  const startTag = '<!-- release-notes-start -->';
  const endTag = '<!-- release-notes-end -->';
  const si = body.indexOf(startTag);
  const ei = body.indexOf(endTag);
  if (si < 0 || ei < 0 || ei <= si) return '';

  let notes = body.slice(si + startTag.length, ei).trim();

  // Strip stale version heading (## [x.y.z] or ## [x.y.z-rcN]) that
  // prReleaseInjectMarkers may have inserted — we regenerate it.
  notes = notes.replace(/^##\s*\[[^\]]+\]\s*\n*/, '').trim();
  return notes;
}

/**
 * Check for the <!-- release-pre:rc --> marker in the body.
 */
function hasPreReleaseMarker(body) {
  return body && body.includes('<!-- release-pre:rc -->');
}

// ---------------------------------------------------------------------------
// Version resolution (same patterns as sibling scripts)
// ---------------------------------------------------------------------------

function readVersionFromFile(config, repoRoot) {
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
  const fileType = (vf.fileType || '').toLowerCase();
  let version = null;

  if (fileType === 'package.json' || fileType === 'plugin.json') {
    try {
      version = JSON.parse(content).version || null;
    } catch (err) {
      process.stderr.write(`Error parsing ${vf.path}: ${err.message}\n`);
      process.exit(1);
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
    process.stderr.write(`Could not read version from ${vf.path}\n`);
    process.exit(1);
  }

  return version;
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

// ---------------------------------------------------------------------------
// Semver helpers
// ---------------------------------------------------------------------------

function parseSemver(s) {
  s = s.replace(/^v/, '');
  // Strip pre-release suffix for core comparison.
  const core = s.split('-')[0];
  const parts = core.split('.');
  if (parts.length !== 3) return null;
  const nums = parts.map(Number);
  if (nums.some(isNaN)) return null;
  return { major: nums[0], minor: nums[1], patch: nums[2] };
}

function semverGreater(a, b) {
  const sa = parseSemver(a);
  const sb = parseSemver(b);
  if (!sa || !sb) return false;
  if (sa.major !== sb.major) return sa.major > sb.major;
  if (sa.minor !== sb.minor) return sa.minor > sb.minor;
  return sa.patch > sb.patch;
}

function bumpSemver(version, level) {
  const sv = parseSemver(version);
  if (!sv) {
    process.stderr.write(`Invalid semver: ${version}\n`);
    process.exit(1);
  }
  switch (level) {
    case 'major': return `${sv.major + 1}.0.0`;
    case 'minor': return `${sv.major}.${sv.minor + 1}.0`;
    case 'patch': return `${sv.major}.${sv.minor}.${sv.patch + 1}`;
    default:
      process.stderr.write(`Unknown bump level: ${level}\n`);
      process.exit(1);
  }
}

// ---------------------------------------------------------------------------
// Version file write (regex-based, format-preserving for top-level "version")
// ---------------------------------------------------------------------------

/**
 * Write a new version into the version file, preserving surrounding formatting.
 * Only the first match of the version pattern is replaced.
 */
function writeVersionToFile(config, repoRoot, newVer) {
  const vf = config.versionFile || {};
  const versionFilePath = path.join(repoRoot, vf.path);
  const content = fs.readFileSync(versionFilePath, 'utf8');
  const fileType = (vf.fileType || '').toLowerCase();
  let updated;

  if (fileType === 'package.json' || fileType === 'plugin.json') {
    // Regex-based: replace the first "version": "..." keeping surrounding whitespace.
    updated = content.replace(
      /("version"\s*:\s*")([^"]*?)(")/,
      `$1${newVer}$3`
    );
  } else if (fileType === 'cargo.toml' || fileType === 'pyproject.toml') {
    updated = content.replace(
      /(^\s*version\s*=\s*")([^"]*?)(")/m,
      `$1${newVer}$3`
    );
  } else if (fileType === 'pubspec.yaml') {
    updated = content.replace(
      /(^\s*version\s*:\s*)(\S+)/m,
      `$1${newVer}`
    );
  } else {
    // Plain text version file — replace entire content.
    updated = newVer + '\n';
  }

  if (updated === content && !content.includes(newVer)) {
    process.stderr.write(`Warning: version pattern not matched in ${vf.path}; file unchanged.\n`);
  }
  fs.writeFileSync(versionFilePath, updated, 'utf8');
}

// ---------------------------------------------------------------------------
// Changelog
// ---------------------------------------------------------------------------

/**
 * Prepend a changelog entry. Format matches internal/version/changelog.go:
 *   ## [version] - YYYY-MM-DD
 *
 *   notes
 *
 * Creates the file with a "# Changelog" heading if it doesn't exist.
 */
function prependChangelog(repoRoot, changelogFile, version, notes) {
  const clPath = path.join(repoRoot, changelogFile);
  const date = new Date().toISOString().slice(0, 10);
  const header = `## [${version}] - ${date}`;
  const section = header + '\n\n' + (notes || '').replace(/\n+$/, '') + '\n';

  if (!fs.existsSync(clPath)) {
    fs.writeFileSync(clPath, '# Changelog\n\n' + section, 'utf8');
    return clPath;
  }

  const old = fs.readFileSync(clPath, 'utf8');
  let content;
  if (old.startsWith('# ')) {
    const idx = old.indexOf('\n');
    if (idx < 0) {
      content = old + '\n\n' + section;
    } else {
      const rest = old.slice(idx).replace(/^\n+/, '');
      content = old.slice(0, idx) + '\n\n' + section + '\n' + rest;
    }
  } else {
    content = section + '\n' + old;
  }
  fs.writeFileSync(clPath, content, 'utf8');
  return clPath;
}

/**
 * Check if a changelog heading for the given version already exists.
 */
function changelogHeadingExists(repoRoot, changelogFile, version) {
  const clPath = path.join(repoRoot, changelogFile);
  if (!fs.existsSync(clPath)) return false;
  const content = fs.readFileSync(clPath, 'utf8');
  return content.includes(`## [${version}]`);
}

// ---------------------------------------------------------------------------
// PR-based file delivery — pushes staged commit on HEAD to a dedicated
// branch and opens a PR back into the release branch.
// ---------------------------------------------------------------------------

/**
 * Push HEAD to `prBranch` on origin and open a PR into `baseBranch` with
 * the given title. Auto-merge is requested; non-fatal if unavailable.
 */
function pushFilesViaPR(repoRoot, baseBranch, prBranch, prTitle) {
  execOrThrow(`git push origin HEAD:${prBranch}`, { cwd: repoRoot });

  const body = `Auto-generated file updates for ${prTitle}.\n\nThis PR was created by the release workflow.`;
  withTmpFile(body, (tmpPath) => {
    execOrThrow(
      `gh pr create --base "${baseBranch}" --head "${prBranch}" ` +
      `--title "${prTitle}" ` +
      `--body-file "${tmpPath}" ` +
      `--label "no-release"`,
      { cwd: repoRoot }
    );
  });
  console.log(`Release PR opened: ${prBranch} -> ${baseBranch}`);

  try {
    execOrThrow(`gh pr merge "${prBranch}" --auto --squash --delete-branch`, { cwd: repoRoot });
    console.log('Auto-merge enabled for release PR.');
  } catch (_) {
    console.log('Auto-merge not available — release PR exists, merge manually.');
  }
}

// ---------------------------------------------------------------------------
// RC helpers
// ---------------------------------------------------------------------------

/**
 * Find the next RC number for a given base version by scanning existing tags.
 * Format: <prefix><base>-rc<N> (no dot), e.g. v1.3.0-rc1, v1.3.0-rc2.
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

// ---------------------------------------------------------------------------
// Idempotency
// ---------------------------------------------------------------------------

/**
 * Check if a tag already exists and is reachable from HEAD (ancestor or equal).
 * Returns 'reachable' | 'unreachable' | 'missing'.
 */
function checkTagState(tag, repoRoot) {
  const tagCommit = exec(`git rev-parse "${tag}^{commit}" 2>/dev/null`, { cwd: repoRoot, shell: true });
  if (!tagCommit) return 'missing';

  // Check if tag commit is an ancestor of (or equal to) HEAD.
  try {
    execSync(`git merge-base --is-ancestor "${tagCommit}" HEAD`, { cwd: repoRoot, stdio: 'pipe' });
    return 'reachable';
  } catch (_) {
    // Also check the reverse: HEAD is an ancestor of tag commit (re-run after
    // version-bump commit was made).
    try {
      const headSha = execSync('git rev-parse HEAD', { cwd: repoRoot, encoding: 'utf8', stdio: 'pipe' }).trim();
      execSync(`git merge-base --is-ancestor "${headSha}" "${tagCommit}"`, { cwd: repoRoot, stdio: 'pipe' });
      return 'reachable';
    } catch (_) {
      return 'unreachable';
    }
  }
}

// ---------------------------------------------------------------------------
// Tag target resolution
// ---------------------------------------------------------------------------

/**
 * Resolve the SHA to tag. Priority:
 *   1. bumpCommitSHA — SHA of the chore(release) commit pushed in phase 2
 *   2. grep origin — find existing chore(release) commit on origin (re-run)
 *   3. mergeSha — the merge commit that triggered this workflow
 *
 * Never uses git rev-parse HEAD at phase 3 time — that could be a local
 * bump commit not yet on origin.
 */
function resolveTagTarget(bumpCommitSHA, branch, version, mergeSha, repoRoot) {
  if (bumpCommitSHA) return bumpCommitSHA;

  // Look for a chore(release) commit already on origin.
  const grepSha = exec(
    `git log "origin/${branch}" --grep="chore(release): ${version}" --format=%H -1`,
    { cwd: repoRoot }
  );
  if (grepSha) return grepSha;

  return mergeSha;
}

// ---------------------------------------------------------------------------
// Phases 2–4: the release executor (extracted for testability)
// ---------------------------------------------------------------------------

/**
 * Execute phases 2–4 of the release flow. Returns per-path status map.
 * Nothing inside this function calls process.exit — errors are caught
 * per-path and recorded as 'failed'.
 *
 * @param {Object} opts
 * @param {string} opts.repoRoot
 * @param {Object} opts.config - validated config from readVersionConfig
 * @param {string} opts.newVersion - e.g. "1.0.1" or "1.0.1-rc2"
 * @param {string} opts.newTag - e.g. "v1.0.1"
 * @param {boolean} opts.isRCRelease
 * @param {string} opts.notes - release notes text
 * @param {string} opts.branch - target branch (e.g. "main")
 * @param {string} opts.mergeSha - SHA of the merge commit (captured before phase 2)
 * @returns {{ versionFile: string, changelog: string, tag: string }}
 */
function runRelease({ repoRoot, config, newVersion, newTag, isRCRelease, notes, branch, mergeSha }) {
  // Per-path status: 'pending' for enabled paths, 'skipped' for disabled.
  const pathStatus = {
    versionFile: config.versionFile.enabled && !isRCRelease ? 'pending' : 'skipped',
    changelog: config.changelog.enabled && !isRCRelease ? 'pending' : 'skipped',
    tag: config.tag.enabled ? 'pending' : 'skipped',
  };

  // Check for existing release commit on origin — if found, phase 2 is a
  // no-op and we use that SHA as tag target (handles re-run after partial
  // failure where push succeeded but tag did not).
  let releaseCommitSHA = null;
  if (!isRCRelease) {
    releaseCommitSHA = exec(
      `git log "origin/${branch}" --grep="chore(release): ${newVersion}" --format=%H -1`,
      { cwd: repoRoot }
    );
    if (releaseCommitSHA) {
      console.log(`Found existing release commit on origin: ${releaseCommitSHA.slice(0, 8)}`);
      // File writes already landed — skip phase 2.
      if (pathStatus.versionFile === 'pending') pathStatus.versionFile = 'skipped';
      if (pathStatus.changelog === 'pending') pathStatus.changelog = 'skipped';
    }
  }

  // =========================================================================
  // PHASE 2: File writes (skip for RC; skip if release commit already exists)
  // =========================================================================

  let bumpCommitSHA = null;

  if (isRCRelease) {
    console.log('RC release: skipping phase 2 (file writes).');
  } else if (releaseCommitSHA) {
    console.log('Release commit already on origin: skipping phase 2 (file writes).');
  } else if (config.method === 'push') {
    // --- Push method: write files, commit, push to main ---
    try {
      // Version file — write unconditionally; git detects no-op via staging.
      if (pathStatus.versionFile === 'pending') {
        writeVersionToFile(config, repoRoot, newVersion);
        execOrThrow(`git add "${config.versionFile.path}"`, { cwd: repoRoot });
        console.log(`Version file updated: ${config.versionFile.path} -> ${newVersion}`);
        pathStatus.versionFile = 'ok';
      }

      // Changelog
      if (pathStatus.changelog === 'pending') {
        const changelogFile = config.changelog.file;
        if (changelogHeadingExists(repoRoot, changelogFile, newVersion)) {
          console.log(`Changelog heading for ${newVersion} already exists — skipping.`);
          pathStatus.changelog = 'skipped';
        } else {
          prependChangelog(repoRoot, changelogFile, newVersion, notes);
          execOrThrow(`git add "${changelogFile}"`, { cwd: repoRoot });
          console.log(`Changelog updated: ${changelogFile}`);
          pathStatus.changelog = 'ok';
        }
      }

      // Commit and push if there are staged changes.
      let hasStagedChanges;
      try {
        execSync('git diff --cached --quiet', { cwd: repoRoot, stdio: 'pipe' });
        hasStagedChanges = false;
      } catch (err) {
        if (err.status === 1) {
          hasStagedChanges = true;
        } else {
          throw new Error(`git diff --cached --quiet failed (exit ${err.status}): ${err.message}`);
        }
      }

      if (hasStagedChanges) {
        const commitMsg = `chore(release): ${newVersion}`;
        withTmpFile(commitMsg, (tmpPath) => {
          execOrThrow(`git commit -F "${tmpPath}"`, { cwd: repoRoot });
        });
        execOrThrow(`git push origin HEAD:${branch}`, { cwd: repoRoot });
        bumpCommitSHA = exec('git rev-parse HEAD', { cwd: repoRoot });
        console.log(`Committed and pushed release commit to ${branch}.`);
      } else {
        console.log('No staged changes after file writes — files already at target.');
        // Writes produced no diff: mark as skipped (idempotent).
        if (pathStatus.versionFile === 'ok') pathStatus.versionFile = 'skipped';
        if (pathStatus.changelog === 'ok') pathStatus.changelog = 'skipped';
      }
    } catch (err) {
      process.stderr.write(`Phase 2 (push) failed: ${err.message}\n`);
      for (const p of ['versionFile', 'changelog']) {
        if (pathStatus[p] === 'pending' || pathStatus[p] === 'ok') pathStatus[p] = 'failed';
      }
    }
  } else if (config.method === 'pr') {
    // --- PR method: write files locally, commit (push in phase 4) ---
    try {
      let anyFileWritten = false;

      // Version file
      if (pathStatus.versionFile === 'pending') {
        writeVersionToFile(config, repoRoot, newVersion);
        execOrThrow(`git add "${config.versionFile.path}"`, { cwd: repoRoot });
        console.log(`Version file updated: ${config.versionFile.path} -> ${newVersion}`);
        pathStatus.versionFile = 'ok';
        anyFileWritten = true;
      }

      // Changelog
      if (pathStatus.changelog === 'pending') {
        const changelogFile = config.changelog.file;
        if (changelogHeadingExists(repoRoot, changelogFile, newVersion)) {
          console.log(`Changelog heading for ${newVersion} already exists — skipping.`);
          pathStatus.changelog = 'skipped';
        } else {
          prependChangelog(repoRoot, changelogFile, newVersion, notes);
          execOrThrow(`git add "${changelogFile}"`, { cwd: repoRoot });
          console.log(`Changelog updated: ${changelogFile}`);
          pathStatus.changelog = 'ok';
          anyFileWritten = true;
        }
      }

      // Commit locally (push happens in phase 4).
      if (anyFileWritten) {
        const commitMsg = `chore(release): ${newVersion}`;
        withTmpFile(commitMsg, (tmpPath) => {
          execOrThrow(`git commit -F "${tmpPath}"`, { cwd: repoRoot });
        });
        console.log('Local commit created for PR delivery.');
      }
    } catch (err) {
      process.stderr.write(`Phase 2 (pr) failed: ${err.message}\n`);
      for (const p of ['versionFile', 'changelog']) {
        if (pathStatus[p] === 'pending' || pathStatus[p] === 'ok') pathStatus[p] = 'failed';
      }
    }
  }

  // =========================================================================
  // PHASE 3: Tag — NEVER blocked by phase 2 failures
  // =========================================================================

  if (pathStatus.tag === 'pending') {
    try {
      const tagState = checkTagState(newTag, repoRoot);
      if (tagState === 'reachable') {
        console.log(`Tag ${newTag} already exists and is reachable. Skipping.`);
        pathStatus.tag = 'skipped';
      } else if (tagState === 'unreachable') {
        process.stderr.write(`Tag ${newTag} exists but is NOT reachable from HEAD. Refusing to overwrite.\n`);
        pathStatus.tag = 'failed';
      } else {
        // Tag is missing — create it.
        const tagTarget = resolveTagTarget(bumpCommitSHA, branch, newVersion, mergeSha, repoRoot);
        const tagMessage = notes || `Release ${newTag}`;
        withTmpFile(tagMessage, (tmpPath) => {
          execOrThrow(`git tag -a "${newTag}" -F "${tmpPath}" "${tagTarget}"`, { cwd: repoRoot });
        });
        execOrThrow(`git push origin "refs/tags/${newTag}"`, { cwd: repoRoot });
        console.log(`Tag ${newTag} created at ${tagTarget.slice(0, 8)} and pushed.`);

        if (!isRCRelease) {
          // Create GitHub Release only for final releases.
          withTmpFile(notes || `Release ${newTag}`, (tmpPath) => {
            execOrThrow(
              `gh release create "${newTag}" --title "${newTag}" --notes-file "${tmpPath}"`,
              { cwd: repoRoot }
            );
          });
          console.log(`GitHub release created for ${newTag}.`);
        } else {
          console.log(`RC release: skipping GitHub Release creation for ${newTag}.`);
        }
        pathStatus.tag = 'ok';
      }
    } catch (err) {
      process.stderr.write(`Phase 3 (tag) failed: ${err.message}\n`);
      pathStatus.tag = 'failed';
    }
  }

  // =========================================================================
  // PHASE 4: PR delivery — open PR when method=="pr" (skip for RC)
  // =========================================================================

  if (!isRCRelease && config.method === 'pr' &&
      (pathStatus.versionFile === 'ok' || pathStatus.changelog === 'ok')) {
    try {
      const releaseBranch = `release/${newTag}`;
      pushFilesViaPR(repoRoot, branch, releaseBranch, `chore(release): ${newVersion}`);
    } catch (err) {
      process.stderr.write(`Phase 4 (PR delivery) failed: ${err.message}\n`);
      // Mark file-writing paths as failed since delivery failed.
      for (const p of ['versionFile', 'changelog']) {
        if (pathStatus[p] === 'ok') pathStatus[p] = 'failed';
      }
    }
  }

  // =========================================================================
  // EXIT: log per-path status, treat leftover 'pending' as 'failed'
  // =========================================================================

  for (const p of ['versionFile', 'changelog', 'tag']) {
    if (pathStatus[p] === 'pending') pathStatus[p] = 'failed';
  }

  console.log(`Path status: tag=${pathStatus.tag} versionFile=${pathStatus.versionFile} changelog=${pathStatus.changelog}`);
  return pathStatus;
}

// ---------------------------------------------------------------------------
// Main — phase 1 + orchestration
// ---------------------------------------------------------------------------

function main() {
  // KEEP: CI script invoked at repo root — do not change to resolveSdlcRoot()
  const repoRoot = process.cwd();
  const branch = process.env.GITHUB_REF_NAME || 'main';

  // =========================================================================
  // PHASE 1: Read-only — config, PR, label, metadata, version resolution
  // =========================================================================

  const config = readVersionConfig(repoRoot);
  if (!config) {
    console.log('No version config found. Skipping release.');
    process.exit(0);
  }

  const pr = findMergedPR(repoRoot);
  if (!pr) {
    console.log('No merged PR found for HEAD. Skipping release.');
    process.exit(0);
  }

  const releaseLabel = findReleaseLabel(pr.labels);
  if (!releaseLabel) {
    console.log(`PR #${pr.number} has no release:* label. Skipping release.`);
    process.exit(0);
  }

  console.log(`PR #${pr.number} merged with label: ${releaseLabel}`);

  const { level, isRC } = parseReleaseLabel(releaseLabel);
  const isRCRelease = isRC || hasPreReleaseMarker(pr.body);
  const notes = extractNotesFromBody(pr.body);

  // Resolve current version.
  const tagPrefix = config.tag.prefix;
  let currentVersion;
  if (!config.versionFile.enabled) {
    // Tag-only mode: derive version from existing tags.
    currentVersion = highestSemverTag(repoRoot, tagPrefix);
    if (!currentVersion) currentVersion = '0.0.0';
  } else {
    currentVersion = readVersionFromFile(config, repoRoot);
  }

  const bumped = bumpSemver(currentVersion, level);

  // Capture merge SHA before any local commits change HEAD.
  const mergeSha = exec('git rev-parse HEAD', { cwd: repoRoot });

  // RC: compute RC-specific version and tag.
  let newVersion, newTag;
  if (isRCRelease) {
    const rcNum = findNextRCNumber(repoRoot, tagPrefix, bumped);
    newVersion = `${bumped}-rc${rcNum}`;
    newTag = `${tagPrefix}${newVersion}`;
    console.log(`RC release: ${newTag} (base ${currentVersion} -> ${bumped}, RC #${rcNum})`);
  } else {
    newVersion = bumped;
    newTag = `${tagPrefix}${newVersion}`;
    console.log(`Direct release: ${newTag} (base ${currentVersion} -> ${newVersion})`);
  }

  // =========================================================================
  // Phases 2–4: execute release
  // =========================================================================

  const pathStatus = runRelease({
    repoRoot, config, newVersion, newTag, isRCRelease, notes, branch, mergeSha,
  });

  const anyFailed = Object.values(pathStatus).some(s => s === 'failed');
  if (anyFailed) {
    process.stderr.write('One or more release paths failed.\n');
    process.exit(1);
  }
}

// Only run when executed directly (`node release-on-main.cjs`) — requiring
// this file as a module (e.g. from tests) must not trigger a live CI run.
if (require.main === module) {
  try {
    main();
  } catch (err) {
    process.stderr.write(`Unexpected error in release-on-main.cjs: ${err.message}\n${err.stack}\n`);
    process.exit(1);
  }
}

module.exports = {
  RELEASE_ON_MAIN_SCRIPT_VERSION,
  readVersionConfig,
  runRelease,
  prependChangelog,
  checkTagState,
  pushFilesViaPR,
  changelogHeadingExists,
};
