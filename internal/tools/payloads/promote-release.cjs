#!/usr/bin/env node
/**
 * promote-release.cjs
 * CI script: promotes the latest release-candidate (RC) tag for a target
 * version to a final release — no rebuild, no new commit at the tagged SHA.
 *
 * Designed to be copied into user projects under `.github/scripts/`.
 *
 * Usage (GitHub Actions — workflow_dispatch):
 *   node .github/scripts/promote-release.cjs v1.3.0
 *
 * Reads: .sdlc-v2/config.json  (sdlc versioning config)
 *
 * Flow:
 *   1. Resolve target tag from CLI arg (e.g. "v1.3.0").
 *   2. git fetch --tags.
 *   3. Find the latest RC tag matching <target>-rc* (highest RC number).
 *   4. Error if no RC exists, or if the final tag already exists.
 *   5. Resolve the RC tag's commit SHA — this exact commit becomes the
 *      final release. The final tag is created AT THE RC's SHA, not at
 *      HEAD: "promote", don't rebuild. What was tested as the RC is what
 *      ships. See DECISIONS below for why this departs from a literal
 *      reading of the step list.
 *   6. Bump the version file to the target version (format-preserving) and
 *      prepend CHANGELOG.md with notes pulled from the RC's GitHub Release.
 *      This bump is committed to the CURRENT branch HEAD (which may have
 *      advanced past the RC) — it is bookkeeping, not part of the tagged
 *      release commit. The tagged commit's version file therefore still
 *      reads the pre-promotion version; that is inherent to promoting an
 *      already-built RC rather than rebuilding at HEAD.
 *   7. Commit + push the bump BEFORE creating the tag/release, so a push
 *      failure (branch protection, non-fast-forward) aborts before the
 *      irreversible tag + GitHub Release are created.
 *   8. Create the final annotated tag at the RC's SHA, push it, and create
 *      a non-pre-release GitHub Release.
 *
 * DECISIONS (flagged for review):
 *   - Tag target: the fact sheet's step 11 reads "git tag -a v1.3.0 -m
 *     <notes> HEAD", but the Acceptance Criteria explicitly requires
 *     "Creates final tag v1.3.0 at same commit as latest RC". Those two
 *     are only consistent if "HEAD" is read as pseudocode shorthand for
 *     "the commit being released", not literal `git rev-parse HEAD`. This
 *     script follows the Acceptance Criteria (the more specific, testable
 *     requirement) and tags the RC's SHA directly.
 *   - Missing version config is treated as a hard error (exit 1), unlike
 *     sibling push-triggered scripts (retag-release.cjs,
 *     release-on-main.cjs, check-changelog.cjs) which no-op with exit 0
 *     when unconfigured. Those scripts run as passive gates on every push;
 *     this one is a human-dispatched action that cannot do its job
 *     (version bump, changelog) without versionFile/tagPrefix — silently
 *     "succeeding" with nothing done would be a false positive.
 *   - GoReleaser attaching binaries on the pushed final tag depends on the
 *     tag push actually firing `on: push: tags:` workflows. Pushes
 *     authenticated with the default `secrets.GITHUB_TOKEN` do NOT trigger
 *     other workflow runs (GitHub's recursive-workflow guard). This script
 *     does not work around that — same caveat already disclosed by Task 7
 *     for release-on-main.cjs, deferred to the scaffold_ci wiring task.
 *
 * Exit codes: 0 = success, 1 = error
 *
 * Uses only Node.js built-in modules + gh CLI. No npm install required.
 */

'use strict';

/** @version 2 — promote-release script version. Bump when behavior changes. */
const PROMOTE_RELEASE_SCRIPT_VERSION = 3;

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
  const tmp = path.join(os.tmpdir(), `promote-release-${Date.now()}-${Math.random().toString(36).slice(2)}.txt`);
  try {
    fs.writeFileSync(tmp, content, 'utf8');
    return fn(tmp);
  } finally {
    try { fs.unlinkSync(tmp); } catch (_) {}
  }
}

function fail(msg) {
  process.stderr.write(`${msg}\n`);
  process.exit(1);
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
    fail(`Error parsing .sdlc-v2/config.json: ${err.message}`);
  }
}

// ---------------------------------------------------------------------------
// Semver helpers
// ---------------------------------------------------------------------------

/**
 * Strict semver parse for a promotion target: exactly X.Y.Z, no pre-release
 * or build metadata suffix. A target with a "-rc"/"-anything" suffix is not
 * a valid promotion target — RCs are the source, not the destination.
 */
function parseStrictSemver(s) {
  const m = /^(\d+)\.(\d+)\.(\d+)$/.exec(s);
  if (!m) return null;
  return { major: Number(m[1]), minor: Number(m[2]), patch: Number(m[3]) };
}

// ---------------------------------------------------------------------------
// Version file read/write (same pattern as sibling scripts)
// ---------------------------------------------------------------------------

function writeVersionToFile(config, repoRoot, newVer) {
  const versionFilePath = path.join(repoRoot, config.versionFile);
  const content = fs.readFileSync(versionFilePath, 'utf8');
  const fileType = (config.fileType || '').toLowerCase();
  let updated;

  if (fileType === 'package.json' || fileType === 'plugin.json') {
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
 * Prepend a changelog entry, unless an entry for this version already
 * exists (idempotency guard — a re-run after a partial failure must not
 * duplicate the heading).
 */
function prependChangelogIfMissing(repoRoot, changelogFile, version, notes) {
  const clPath = path.join(repoRoot, changelogFile);
  const escaped = version.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const headingRe = new RegExp(`^##\\s+\\[${escaped}\\]`, 'm');

  if (fs.existsSync(clPath)) {
    const existing = fs.readFileSync(clPath, 'utf8');
    if (headingRe.test(existing)) {
      console.log(`Changelog already has an entry for ${version}; skipping prepend.`);
      return false;
    }
  }

  const date = new Date().toISOString().slice(0, 10);
  const header = `## [${version}] - ${date}`;
  const section = header + '\n\n' + (notes || '').replace(/\n+$/, '') + '\n';

  if (!fs.existsSync(clPath)) {
    fs.writeFileSync(clPath, '# Changelog\n\n' + section, 'utf8');
    return true;
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
  return true;
}

/**
 * Remove all "## [<targetBase>-rcN] ..." sections (heading through to the
 * next "## " heading or EOF) from the CHANGELOG, so the collapsed final
 * entry doesn't duplicate content already recorded per-RC. No-op if the
 * file doesn't exist or no matching RC entries are found.
 */
function stripRCEntries(repoRoot, changelogFile, tagPrefix, targetBase) {
  const clPath = path.join(repoRoot, changelogFile);
  if (!fs.existsSync(clPath)) return;

  const content = fs.readFileSync(clPath, 'utf8');
  const escapedBase = targetBase.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const rcSectionRe = new RegExp(
    `^##\\s+\\[${escapedBase}-rc\\d+\\][^\\n]*\\n(?:(?!^##\\s+\\[)[\\s\\S])*`,
    'gm'
  );

  const stripped = content.replace(rcSectionRe, '');
  if (stripped === content) return;

  // Collapse any blank-line runs left behind by the removed sections.
  const cleaned = stripped.replace(/\n{3,}/g, '\n\n');
  fs.writeFileSync(clPath, cleaned, 'utf8');
}

// ---------------------------------------------------------------------------
// RC lookup
// ---------------------------------------------------------------------------

/**
 * Find the latest RC tag for a target base version by scanning existing
 * tags for <prefix><base>-rc<N> and returning the one with the highest N.
 * Returns null if no RC tag exists.
 */
function findLatestRCTag(repoRoot, tagPrefix, targetBase) {
  const out = exec('git tag --list', { cwd: repoRoot });
  if (!out) return null;

  const needle = `${tagPrefix}${targetBase}-rc`;
  let bestTag = null;
  let bestNum = -1;
  for (const t of out.split('\n')) {
    if (!t.startsWith(needle)) continue;
    const n = parseInt(t.slice(needle.length), 10);
    if (!isNaN(n) && n > bestNum) {
      bestNum = n;
      bestTag = t;
    }
  }
  return bestTag;
}

/**
 * Find ALL RC tags for a target base version by scanning existing tags for
 * <prefix><base>-rc<N>, sorted ascending by RC number.
 * Returns [] if no RC tag exists.
 */
function findAllRCTags(repoRoot, tagPrefix, targetBase) {
  const out = exec('git tag --list', { cwd: repoRoot });
  if (!out) return [];

  const needle = `${tagPrefix}${targetBase}-rc`;
  const tags = [];
  for (const t of out.split('\n')) {
    if (!t.startsWith(needle)) continue;
    const n = parseInt(t.slice(needle.length), 10);
    if (!isNaN(n)) tags.push({ tag: t, num: n });
  }
  tags.sort((a, b) => a.num - b.num);
  return tags.map((t) => t.tag);
}

/**
 * Read the message body of an existing annotated tag.
 * Returns null if the tag doesn't exist or has no message.
 */
function getTagMessage(tag, repoRoot) {
  const msg = exec(`git tag -l --format='%(contents)' "${tag}"`, { cwd: repoRoot, shell: true });
  return msg ? msg.trim() : null;
}

/**
 * Read release notes for the RC tag: prefer the RC's GitHub Release body,
 * fall back to the annotated tag's message, fall back to a generic note.
 * Strips a stale "## [version]" heading if present (we recompute it).
 */
function readRCNotes(rcTag, repoRoot) {
  const body = exec(`gh release view "${rcTag}" --json body --jq .body`, { cwd: repoRoot });
  let notes = body;
  if (!notes) {
    notes = getTagMessage(rcTag, repoRoot);
  }
  if (!notes) {
    return `Release ${rcTag}`;
  }
  return notes.replace(/^##\s*\[[^\]]+\]\s*\n*/, '').trim() || `Release ${rcTag}`;
}

/**
 * Aggregate release notes from every RC tag for a target version.
 * Deduplicates identical note blocks (exact string match after trim) so a
 * re-merged PR (e.g. after revert+re-land) doesn't duplicate its notes
 * across RCs. Each surviving block is labeled with its RC number.
 * Returns '' when rcTags is empty.
 */
function readAllRCNotes(rcTags, repoRoot) {
  const seen = new Set();
  const blocks = [];
  for (const tag of rcTags) {
    const trimmed = (readRCNotes(tag, repoRoot) || '').trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    const rcNumMatch = tag.match(/-rc(\d+)$/);
    const rcLabel = rcNumMatch ? `RC ${rcNumMatch[1]}` : tag;
    blocks.push(`### ${rcLabel}\n\n${trimmed}`);
  }
  return blocks.join('\n\n');
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

function main() {
  // KEEP: CI script invoked at repo root — do not change to resolveSdlcRoot()
  const repoRoot = process.cwd();

  // Step 1: Read target version from workflow input.
  const targetTag = (process.argv[2] || '').trim();
  if (!targetTag) {
    fail('Usage: node promote-release.cjs <version>  (e.g. v1.3.0)');
  }

  const config = readVersionConfig(repoRoot);
  if (!config) {
    fail('No version config found (.sdlc-v2/config.json). Cannot promote release without versionFile/tagPrefix.');
  }

  const tagPrefix = config.tagPrefix || '';
  if (tagPrefix && !targetTag.startsWith(tagPrefix)) {
    fail(`Invalid version format: ${targetTag} (expected prefix "${tagPrefix}")`);
  }
  const targetBase = tagPrefix ? targetTag.slice(tagPrefix.length) : targetTag;

  if (!parseStrictSemver(targetBase)) {
    fail(`Invalid version format: ${targetTag} (expected <prefix>X.Y.Z, no pre-release suffix)`);
  }

  // Step 2: Fetch tags.
  execOrThrow('git fetch --tags', { cwd: repoRoot });

  // Step 3/4: Find the latest RC tag for the target version.
  const rcTag = findLatestRCTag(repoRoot, tagPrefix, targetBase);
  if (!rcTag) {
    fail(`No RC tags found for ${targetTag}`);
  }
  console.log(`Latest RC for ${targetTag}: ${rcTag}`);

  // Step 5: Error if the final tag already exists.
  const existing = exec(`git rev-parse "${targetTag}^{commit}" 2>/dev/null`, { cwd: repoRoot, shell: true });
  if (existing) {
    fail('Final tag already exists');
  }

  // Step 6: Resolve the RC tag's commit SHA — the final tag will point here.
  const rcSha = exec(`git rev-parse "${rcTag}^{commit}" 2>/dev/null`, { cwd: repoRoot, shell: true });
  if (!rcSha) {
    fail(`Could not resolve commit for RC tag ${rcTag}`);
  }
  console.log(`RC ${rcTag} -> ${rcSha}`);

  // Step 7/8: Aggregate release notes from ALL RC GitHub Releases for this
  // target — not just the latest RC — so multi-RC cycles don't lose notes.
  const allRCTags = findAllRCTags(repoRoot, tagPrefix, targetBase);
  const notes = readAllRCNotes(allRCTags, repoRoot);

  // Step 7: Bump version file (format-preserving), on current branch HEAD.
  // Gate matches release-on-main.cjs: skip in tag-only mode (no version file
  // to bump).
  const filesToAdd = [];
  if (config.mode !== 'tag' && config.versionFile) {
    writeVersionToFile(config, repoRoot, targetBase);
    filesToAdd.push(config.versionFile);
    console.log(`Version file updated: ${config.versionFile} -> ${targetBase}`);
  }

  // Step 9: Strip per-RC CHANGELOG entries for this target, then prepend the
  // single collapsed final entry with the aggregated notes.
  // Gate matches release-on-main.cjs: only when config.changelog === true.
  if (config.changelog === true) {
    const changelogFile = config.changelogFile || 'CHANGELOG.md';
    stripRCEntries(repoRoot, changelogFile, tagPrefix, targetBase);
    if (prependChangelogIfMissing(repoRoot, changelogFile, targetBase, notes)) {
      filesToAdd.push(changelogFile);
      console.log(`Changelog updated: ${changelogFile} (RC entries collapsed)`);
    }
  }

  // Step 10: git add + commit + push (bump lands on the branch, BEFORE the
  // tag/release are created, so a rejected push aborts before anything
  // irreversible happens).
  if (filesToAdd.length > 0) {
    for (const f of filesToAdd) {
      execOrThrow(`git add "${f}"`, { cwd: repoRoot });
    }

    const hasStagedChanges = exec('git diff --cached --quiet', { cwd: repoRoot }) === null;
    if (hasStagedChanges) {
      const commitMsg = `chore(release): promote ${targetTag}`;
      withTmpFile(commitMsg, (tmpPath) => {
        execOrThrow(`git commit -F "${tmpPath}"`, { cwd: repoRoot });
      });

      const branch = process.env.GITHUB_REF_NAME || 'main';
      execOrThrow(`git push origin HEAD:${branch}`, { cwd: repoRoot });
      console.log(`Committed and pushed version bump to ${branch}.`);
    } else {
      console.log('No staged changes after version write — files already at target.');
    }
  }

  // Step 11: Create the final annotated tag AT THE RC'S SHA (not HEAD — see
  // DECISIONS in the file header) and push it.
  withTmpFile(notes, (tmpPath) => {
    execOrThrow(`git tag -a "${targetTag}" -F "${tmpPath}" "${rcSha}"`, { cwd: repoRoot });
  });
  execOrThrow(`git push origin "refs/tags/${targetTag}"`, { cwd: repoRoot });
  console.log(`Tag ${targetTag} created at ${rcSha} and pushed.`);

  // Step 12: Create the final (non-pre-release) GitHub Release.
  withTmpFile(notes, (tmpPath) => {
    execOrThrow(`gh release create "${targetTag}" --title "${targetTag}" --notes-file "${tmpPath}"`, { cwd: repoRoot });
  });
  console.log(`GitHub release created for ${targetTag}.`);
}

// Only run when executed directly (`node promote-release.cjs <version>`) —
// requiring this file as a module (e.g. from tests) must not trigger a live
// CI run (it would otherwise exit the process immediately on a missing arg).
if (require.main === module) {
  try {
    main();
  } catch (err) {
    process.stderr.write(`Unexpected error in promote-release.cjs: ${err.message}\n${err.stack}\n`);
    process.exit(1);
  }
}

module.exports = {
  PROMOTE_RELEASE_SCRIPT_VERSION,
  findAllRCTags,
  readAllRCNotes,
  stripRCEntries,
  prependChangelogIfMissing,
};
