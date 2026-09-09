#!/usr/bin/env node
/**
 * release-on-main.cjs
 * CI script: creates a release (tag + GitHub Release) when a PR with a
 * release:* label is merged to main.
 *
 * Designed to be copied into user projects under `.github/scripts/`.
 *
 * Usage (GitHub Actions — runs on push to main):
 *   node .github/scripts/release-on-main.cjs
 *
 * Reads: .sdlc-v2/config.json  (sdlc versioning config)
 *
 * Two flows:
 *   Direct release — bumps version file, creates final tag + GitHub Release,
 *     then prepends CHANGELOG (best-effort — a changelog failure is logged
 *     and does not undo or block the tag/release, which already landed).
 *   RC release     — creates RC tag (v1.3.0-rc1) + GitHub pre-release, then
 *     prepends CHANGELOG with the RC entry (same best-effort ordering). Does
 *     NOT bump version file.
 *
 * Exit codes: 0 = success / no-op (no release label), 1 = error
 *
 * Uses only Node.js built-in modules + gh CLI. No npm install required.
 */

'use strict';

/** @version 5 — release-on-main script version. Bump when behavior changes. */
const RELEASE_ON_MAIN_SCRIPT_VERSION = 5;

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
  } catch (err) {
    process.stderr.write(`Error parsing .sdlc-v2/config.json: ${err.message}\n`);
    process.exit(1);
  }
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
  if (!config.versionFile) {
    process.stderr.write('config.versionFile is not set and mode is not "tag".\n');
    process.exit(1);
  }

  const versionFilePath = path.join(repoRoot, config.versionFile);
  if (!fs.existsSync(versionFilePath)) {
    process.stderr.write(`Version file not found: ${config.versionFile}\n`);
    process.exit(1);
  }

  const content = fs.readFileSync(versionFilePath, 'utf8');
  const fileType = (config.fileType || '').toLowerCase();
  let version = null;

  if (fileType === 'package.json' || fileType === 'plugin.json') {
    try {
      version = JSON.parse(content).version || null;
    } catch (err) {
      process.stderr.write(`Error parsing ${config.versionFile}: ${err.message}\n`);
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
    process.stderr.write(`Could not read version from ${config.versionFile}\n`);
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
  const versionFilePath = path.join(repoRoot, config.versionFile);
  const content = fs.readFileSync(versionFilePath, 'utf8');
  const fileType = (config.fileType || '').toLowerCase();
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
    process.stderr.write(`Warning: version pattern not matched in ${config.versionFile}; file unchanged.\n`);
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

// ---------------------------------------------------------------------------
// Changelog delivery (PR-based — a direct push to a protected default
// branch is rejected by GitHub rulesets, which cannot grant
// github-actions[bot] a bypass; see docs/versioning.md "Branch Protection &
// Release Workflow")
// ---------------------------------------------------------------------------

/**
 * Push the changelog commit on HEAD to a dedicated `changelog/<tagName>`
 * branch and open a PR back into `branch`, instead of pushing directly.
 * Rulesets/classic protection only guard the default branch, so pushing the
 * changelog branch always succeeds; the PR then lands through the repo's
 * normal merge path. Its merge does not re-trigger a new release: this
 * script's own idempotency check (tagState === 'reachable', see above) skips
 * the run because the tag it would create already exists and is reachable
 * from the merged HEAD. Labeled "no-release" as a human-facing signal only
 * (no script currently gates on this label — it just documents intent for
 * anyone reviewing the PR list). Auto-merge is requested so the changelog
 * lands without manual action once required checks pass; if auto-merge
 * isn't enabled the PR is simply left open for manual merge (non-fatal —
 * the caller already wraps this in a try/catch since the tag/release must
 * never be blocked by changelog delivery).
 */
function pushChangelogViaPR(repoRoot, branch, tagName) {
  const changelogBranch = `changelog/${tagName}`;
  execOrThrow(`git push origin HEAD:${changelogBranch}`, { cwd: repoRoot });

  const body = `Auto-generated changelog update for ${tagName}.\n\nThis PR was created by the release workflow.`;
  withTmpFile(body, (tmpPath) => {
    execOrThrow(
      `gh pr create --base "${branch}" --head "${changelogBranch}" ` +
      `--title "chore(release): changelog for ${tagName}" ` +
      `--body-file "${tmpPath}" ` +
      `--label "no-release"`,
      { cwd: repoRoot }
    );
  });
  console.log(`Changelog PR opened: ${changelogBranch} -> ${branch}`);

  try {
    execOrThrow(`gh pr merge "${changelogBranch}" --auto --squash --delete-branch`, { cwd: repoRoot });
    console.log('Auto-merge enabled for changelog PR.');
  } catch (_) {
    console.log('Auto-merge not available — changelog PR exists, merge manually.');
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
// Main
// ---------------------------------------------------------------------------

function main() {
  // KEEP: CI script invoked at repo root — do not change to resolveSdlcRoot()
  const repoRoot = process.cwd();

  // Step 1: Read version config.
  const config = readVersionConfig(repoRoot);
  if (!config) {
    console.log('No version config found. Skipping release.');
    process.exit(0);
  }

  // Step 2: Find the merged PR that triggered this push.
  const pr = findMergedPR(repoRoot);
  if (!pr) {
    console.log('No merged PR found for HEAD. Skipping release.');
    process.exit(0);
  }

  // Step 3: Check for a release:* label.
  const releaseLabel = findReleaseLabel(pr.labels);
  if (!releaseLabel) {
    console.log(`PR #${pr.number} has no release:* label. Skipping release.`);
    process.exit(0);
  }

  console.log(`PR #${pr.number} merged with label: ${releaseLabel}`);

  // Step 4: Parse label and extract metadata.
  const { level, isRC } = parseReleaseLabel(releaseLabel);
  const isRCRelease = isRC || hasPreReleaseMarker(pr.body);
  const notes = extractNotesFromBody(pr.body);

  // Step 5: Resolve current version.
  const tagPrefix = config.tagPrefix || '';
  let currentVersion;
  if (config.mode === 'tag') {
    currentVersion = highestSemverTag(repoRoot, tagPrefix);
    if (!currentVersion) {
      // No tags yet — start from 0.0.0 so the bump produces a valid first version.
      currentVersion = '0.0.0';
    }
  } else {
    currentVersion = readVersionFromFile(config, repoRoot);
  }

  // Step 6: Compute the new version by bumping from the file version.
  // Unlike prReleaseComputeIntent (which uses max(file, highestTag) to prevent
  // collisions at PR-creation time), this post-merge script uses the file
  // version alone. This guarantees idempotency: a re-run on the same merge
  // commit always computes the same target tag, so the existence check (below)
  // correctly short-circuits.
  const bumped = bumpSemver(currentVersion, level);

  if (isRCRelease) {
    // ----- RC release flow -----
    const rcNum = findNextRCNumber(repoRoot, tagPrefix, bumped);
    const rcVersion = `${bumped}-rc${rcNum}`;
    const rcTag = `${tagPrefix}${rcVersion}`;

    console.log(`RC release: ${rcTag} (base ${currentVersion} → ${bumped}, RC #${rcNum})`);

    // Idempotency: if this RC tag already exists and is reachable, skip.
    const tagState = checkTagState(rcTag, repoRoot);
    if (tagState === 'reachable') {
      console.log(`Tag ${rcTag} already exists and is reachable from HEAD. Nothing to do.`);
      process.exit(0);
    }
    if (tagState === 'unreachable') {
      process.stderr.write(`Tag ${rcTag} exists but is NOT reachable from HEAD. Refusing to overwrite.\n`);
      process.exit(1);
    }

    // RC: do NOT bump version file. Create the tag first — the tag is the
    // release; CHANGELOG below is best-effort and must never block it.
    const tagMessage = notes || `Release ${rcTag}`;
    withTmpFile(tagMessage, (tmpPath) => {
      execOrThrow(`git tag -a "${rcTag}" -F "${tmpPath}" HEAD`, { cwd: repoRoot });
    });
    execOrThrow(`git push origin "refs/tags/${rcTag}"`, { cwd: repoRoot });
    console.log(`Tag ${rcTag} created and pushed.`);

    // Create GitHub pre-release.
    withTmpFile(notes || `Pre-release ${rcTag}`, (tmpPath) => {
      execOrThrow(`gh release create "${rcTag}" --title "${rcTag}" --notes-file "${tmpPath}" --prerelease`, { cwd: repoRoot });
    });
    console.log(`GitHub pre-release created for ${rcTag}.`);

    // Prepend CHANGELOG if enabled — after the tag and pre-release already
    // exist, so a changelog failure never costs the release.
    if (config.changelog === true) {
      try {
        const changelogFile = config.changelogFile || 'CHANGELOG.md';
        prependChangelog(repoRoot, changelogFile, rcVersion, notes);
        console.log(`Changelog updated: ${changelogFile} (RC entry ${rcVersion})`);

        execOrThrow(`git add "${changelogFile}"`, { cwd: repoRoot });
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
          const commitMsg = `chore(release): changelog for ${rcTag}`;
          withTmpFile(commitMsg, (tmpPath) => {
            execOrThrow(`git commit -F "${tmpPath}"`, { cwd: repoRoot });
          });
          const branch = process.env.GITHUB_REF_NAME || 'main';
          pushChangelogViaPR(repoRoot, branch, rcTag);
        } else {
          console.log('No staged changes after changelog write — files already at target.');
        }
      } catch (err) {
        process.stderr.write(`Changelog update failed, continuing (tag ${rcTag} already released): ${err.message}\n`);
      }
    }
  } else {
    // ----- Direct release flow -----
    const newVersion = bumped;
    const newTag = `${tagPrefix}${newVersion}`;

    console.log(`Direct release: ${newTag} (base ${currentVersion} → ${newVersion})`);

    // Idempotency: if the final tag already exists and is reachable, skip.
    const tagState = checkTagState(newTag, repoRoot);
    if (tagState === 'reachable') {
      console.log(`Tag ${newTag} already exists and is reachable from HEAD. Nothing to do.`);
      process.exit(0);
    }
    if (tagState === 'unreachable') {
      process.stderr.write(`Tag ${newTag} exists but is NOT reachable from HEAD. Refusing to overwrite.\n`);
      process.exit(1);
    }

    // Step 7: Write version to file (skip in tag-only mode) and commit it —
    // this must land before the tag so the tag matches the file's version.
    const versionFilesToAdd = [];
    if (config.mode !== 'tag' && config.versionFile) {
      writeVersionToFile(config, repoRoot, newVersion);
      versionFilesToAdd.push(config.versionFile);
      console.log(`Version file updated: ${config.versionFile} → ${newVersion}`);
    }

    // Step 8: Commit and push the version file (only if it changed).
    if (versionFilesToAdd.length > 0) {
      for (const f of versionFilesToAdd) {
        execOrThrow(`git add "${f}"`, { cwd: repoRoot });
      }

      // Guard: only commit if there are staged changes.
      let hasStagedChanges;
      try {
        execSync('git diff --cached --quiet', { cwd: repoRoot, stdio: 'pipe' });
        hasStagedChanges = false;
      } catch (err) {
        if (err.status === 1) {
          hasStagedChanges = true;
        } else {
          process.stderr.write(`git diff --cached --quiet failed (exit ${err.status}): ${err.message}\n`);
          process.exit(1);
        }
      }
      if (hasStagedChanges) {
        const commitMsg = `chore(release): ${newVersion}`;
        withTmpFile(commitMsg, (tmpPath) => {
          execOrThrow(`git commit -F "${tmpPath}"`, { cwd: repoRoot });
        });

        // Push to the branch — actions/checkout may leave HEAD detached.
        const branch = process.env.GITHUB_REF_NAME || 'main';
        execOrThrow(`git push origin HEAD:${branch}`, { cwd: repoRoot });
        console.log(`Committed and pushed version bump to ${branch}.`);
      } else {
        console.log('No staged changes after version write — files already at target.');
      }
    }

    // Step 9: Create tag — the release itself. Must happen unconditionally
    // once the version state above is settled; CHANGELOG (Step 11) is
    // best-effort and must never block it.
    const tagMessage = notes || `Release ${newTag}`;
    withTmpFile(tagMessage, (tmpPath) => {
      execOrThrow(`git tag -a "${newTag}" -F "${tmpPath}" HEAD`, { cwd: repoRoot });
    });
    execOrThrow(`git push origin "refs/tags/${newTag}"`, { cwd: repoRoot });
    console.log(`Tag ${newTag} created and pushed.`);

    // Step 10: Create GitHub release.
    withTmpFile(notes || `Release ${newTag}`, (tmpPath) => {
      execOrThrow(`gh release create "${newTag}" --title "${newTag}" --notes-file "${tmpPath}"`, { cwd: repoRoot });
    });
    console.log(`GitHub release created for ${newTag}.`);

    // Step 11: Prepend CHANGELOG if enabled — after the tag and release
    // already exist, so a changelog failure never costs the release.
    if (config.changelog === true) {
      try {
        const changelogFile = config.changelogFile || 'CHANGELOG.md';
        prependChangelog(repoRoot, changelogFile, newVersion, notes);
        console.log(`Changelog updated: ${changelogFile}`);

        execOrThrow(`git add "${changelogFile}"`, { cwd: repoRoot });
        let changelogStaged;
        try {
          execSync('git diff --cached --quiet', { cwd: repoRoot, stdio: 'pipe' });
          changelogStaged = false;
        } catch (err) {
          if (err.status === 1) {
            changelogStaged = true;
          } else {
            throw new Error(`git diff --cached --quiet failed (exit ${err.status}): ${err.message}`);
          }
        }
        if (changelogStaged) {
          const commitMsg = `chore(release): changelog for ${newTag}`;
          withTmpFile(commitMsg, (tmpPath) => {
            execOrThrow(`git commit -F "${tmpPath}"`, { cwd: repoRoot });
          });
          const branch = process.env.GITHUB_REF_NAME || 'main';
          pushChangelogViaPR(repoRoot, branch, newTag);
        } else {
          console.log('No staged changes after changelog write — files already at target.');
        }
      } catch (err) {
        process.stderr.write(`Changelog update failed, continuing (tag ${newTag} already released): ${err.message}\n`);
      }
    }
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
  prependChangelog,
  checkTagState,
  pushChangelogViaPR,
};
