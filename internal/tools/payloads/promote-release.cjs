#!/usr/bin/env node
/**
 * promote-release.cjs
 * CI script: promotes the latest release-candidate (RC) tag for a target
 * version to a final release — no rebuild, no new commit at the tagged SHA.
 *
 * Designed to be copied into user projects under `.github/scripts/`.
 *
 * Usage (GitHub Actions — workflow_dispatch):
 *   node .github/scripts/promote-release.cjs patch
 *   (level: major | minor | patch)
 *
 * Reads: .sdlc-v2/config.toml  (sdlc versioning config)
 *
 * Flow:
 *   1. Resolve the target version by discovering the active RC series
 *      (highest base version with "-rcN" tags) and bumping the latest
 *      stable tag by the requested level (major | minor | patch).
 *   2. git fetch --tags --force.
 *   3. Find the latest RC tag matching <target>-rc* (highest RC number).
 *   4. Error if no RC exists, or if the final tag already exists.
 *   5. Resolve the RC tag's commit SHA — this exact commit becomes the
 *      final release. The final tag is created AT THE RC's SHA, not at
 *      HEAD: "promote", don't rebuild. What was tested as the RC is what
 *      ships. See DECISIONS below for why this departs from a literal
 *      reading of the step list.
 *   6. Bump the version file to the target version (format-preserving) and
 *      prepend CHANGELOG.md with notes aggregated from ALL RC GitHub Releases
 *      for the target version (deduplicated, labeled per-RC).
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
 *   - GoReleaser attaching binaries on the pushed final tag was addressed by
 *     adding a dispatch bridge (Step 13) that explicitly runs release.yml at
 *     the final tag. GitHub Actions does not cascade-trigger workflows from
 *     GITHUB_TOKEN-authenticated pushes, so this explicit dispatch via `gh
 *     workflow run` is the solution. The dispatch trigger is allowed even
 *     with GITHUB_TOKEN (proven by release-dispatch.yml:57).
 *
 * Exit codes: 0 = success, 1 = error
 *
 * Uses only Node.js built-in modules + gh CLI. No npm install required.
 */

'use strict';

/** @version 7 — promote-release script version. Bump when behavior changes. */
const PROMOTE_RELEASE_SCRIPT_VERSION = 7;

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
    fail(`Error parsing .sdlc-v2/config.toml: ${err.message}`);
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

/**
 * Lenient semver parse: strips a leading "v" and any pre-release/build
 * metadata suffix, returning just the numeric core. Used for bump
 * arithmetic and comparisons, where the input may be a stable version or
 * an RC series base version (never a "-rcN" string itself).
 */
function parseSemver(s) {
  s = s.replace(/^v/, '');
  const core = s.split('-')[0];
  const parts = core.split('.');
  if (parts.length !== 3) return null;
  const nums = parts.map(Number);
  if (nums.some(isNaN)) return null;
  return { major: nums[0], minor: nums[1], patch: nums[2] };
}

/**
 * Bump a X.Y.Z version string by the given level, returning the new
 * version string (no prefix). Exits via fail() on an invalid version or
 * an unrecognized level.
 */
function bumpSemver(version, level) {
  const sv = parseSemver(version);
  if (!sv) fail(`Invalid semver: ${version}`);
  switch (level) {
    case 'major': return `${sv.major + 1}.0.0`;
    case 'minor': return `${sv.major}.${sv.minor + 1}.0`;
    case 'patch': return `${sv.major}.${sv.minor}.${sv.patch + 1}`;
    default: fail(`Unknown bump level: ${level}`);
  }
}

/**
 * Find the most recent non-RC (final) semver tag by scanning git tags
 * newest-first and returning the first one matching <prefix>X.Y.Z.
 * Returns the full tag string (with prefix), or null if none exists.
 */
function findLatestStableTag(repoRoot, tagPrefix) {
  const out = execOrThrow('git tag --list --sort=-v:refname', { cwd: repoRoot });
  if (!out) return null;
  for (const t of out.split('\n')) {
    if (!t.trim() || t.includes('-rc')) continue;
    let v = t;
    if (tagPrefix && v.startsWith(tagPrefix)) {
      v = v.slice(tagPrefix.length);
    } else if (tagPrefix) {
      continue;
    }
    if (/^\d+\.\d+\.\d+$/.test(v)) return t; // return full tag string
  }
  return null;
}

/**
 * Find the active RC series: groups all "<prefix>X.Y.Z-rcN" tags by their
 * base version and returns the tags for the highest base version.
 * Returns { baseVersion, tags } (tags sorted ascending by RC number), or
 * null if no RC tags exist at all.
 */
function findActiveRCSeries(repoRoot, tagPrefix) {
  const out = execOrThrow('git tag --list', { cwd: repoRoot });
  if (!out) return null;
  const rcPattern = /-rc(\d+)$/;
  const series = {};  // baseVersion -> [{tag, num}]
  for (const t of out.split('\n')) {
    if (!t.trim()) continue;
    let v = t;
    if (tagPrefix && v.startsWith(tagPrefix)) {
      v = v.slice(tagPrefix.length);
    } else if (tagPrefix) {
      continue;
    }
    const m = rcPattern.exec(v);
    if (!m) continue;
    const base = v.slice(0, v.length - m[0].length);
    if (!/^\d+\.\d+\.\d+$/.test(base)) continue;
    if (!series[base]) series[base] = [];
    series[base].push({ tag: t, num: parseInt(m[1], 10) });
  }
  let best = null;
  for (const [base, tags] of Object.entries(series)) {
    if (!best || semverGreater(base, best.baseVersion)) {
      tags.sort((a, b) => a.num - b.num);
      best = { baseVersion: base, tags: tags.map(t => t.tag) };
    }
  }
  return best;
}

/**
 * True when semver `a` is strictly greater than semver `b`. Either input
 * failing to parse is treated as "not greater" (false), never throws.
 */
function semverGreater(a, b) {
  const sa = parseSemver(a);
  const sb = parseSemver(b);
  if (!sa || !sb) return false;
  if (sa.major !== sb.major) return sa.major > sb.major;
  if (sa.minor !== sb.minor) return sa.minor > sb.minor;
  return sa.patch > sb.patch;
}

// ---------------------------------------------------------------------------
// Version file read/write (same pattern as sibling scripts)
// ---------------------------------------------------------------------------

function writeVersionToFile(config, repoRoot, newVer) {
  const vf = config.versionFile || {};
  const versionFilePath = path.join(repoRoot, vf.path);
  const content = fs.readFileSync(versionFilePath, 'utf8');
  const fileType = (vf.fileType || '').toLowerCase();
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
    process.stderr.write(`Warning: version pattern not matched in ${vf.path}; file unchanged.\n`);
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
  let out;
  try {
    out = execSync('git tag --list', { encoding: 'utf8', cwd: repoRoot, stdio: 'pipe' }).trim();
  } catch (err) {
    fail(`git tag --list failed (exit ${err.status}): ${err.message}`);
  }
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
    console.log(`Warning: could not read release notes for ${rcTag} (gh release view and tag message both failed); using placeholder.`);
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

  // Step 1: Read the bump level from the workflow input.
  const level = (process.argv[2] || '').trim();
  if (!['major', 'minor', 'patch'].includes(level)) {
    fail('Usage: node promote-release.cjs <level>  (level: major | minor | patch)');
  }

  const config = readVersionConfig(repoRoot);
  if (!config) {
    fail('No version config found (.sdlc-v2/config.toml). Cannot promote release without versionFile/tagPrefix.');
  }

  const tagPrefix = config.tag?.prefix || '';

  // Step 2: Fetch tags.
  execOrThrow('git fetch --tags --force', { cwd: repoRoot });

  // Step 3/4: Discover the active RC series, then auto-resolve the target
  // version by bumping the latest stable tag by the requested level.
  const series = findActiveRCSeries(repoRoot, tagPrefix);
  if (!series) {
    fail('No RC tags found');
  }
  const stableTag = findLatestStableTag(repoRoot, tagPrefix);
  const stableVersion = stableTag
    ? (tagPrefix ? stableTag.slice(tagPrefix.length) : stableTag)
    : '0.0.0';
  const targetBase = bumpSemver(stableVersion, level);
  const targetTag = `${tagPrefix}${targetBase}`;

  // Guard against a bump level that would produce a version lower than the
  // active RC series (e.g. a stale stable tag combined with "patch" while
  // the active RC series is already a minor/major ahead).
  const rcSv = parseSemver(series.baseVersion);
  const tgtSv = parseSemver(targetBase);
  if (!rcSv || !tgtSv) fail(`Internal error: unparseable versions rc=${series.baseVersion} tgt=${targetBase}`);
  if (
    tgtSv.major < rcSv.major ||
    (tgtSv.major === rcSv.major && tgtSv.minor < rcSv.minor) ||
    (tgtSv.major === rcSv.major && tgtSv.minor === rcSv.minor && tgtSv.patch < rcSv.patch)
  ) {
    fail(`Chosen level "${level}" produces ${targetTag}, which is lower than the active RC series ${series.baseVersion}. Use a higher bump level.`);
  }

  console.log(`Active RC series: ${series.baseVersion} (${series.tags.length} RC(s))`);
  console.log(`Latest stable: ${stableTag || '(none)'}`);
  console.log(`Target version: ${targetTag}`);

  const rcTag = findLatestRCTag(repoRoot, tagPrefix, series.baseVersion);
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

  // Step 7/8: Aggregate release notes from ALL RC GitHub Releases in the
  // active RC series — not just the latest RC — so multi-RC cycles don't
  // lose notes. Uses series.baseVersion (the RC series' own version), not
  // targetBase, since RC tags/notes/changelog entries are keyed to the RC
  // series, which may differ from the auto-resolved target version.
  const allRCTags = findAllRCTags(repoRoot, tagPrefix, series.baseVersion);
  const notes = readAllRCNotes(allRCTags, repoRoot);
  console.log(`Aggregated notes from ${allRCTags.length} RC tag(s).`);

  // Step 7: Bump version file (format-preserving), on current branch HEAD.
  // Gate matches release-on-main.cjs: skip in tag-only mode (no version file
  // to bump).
  const filesToAdd = [];
  if (config.versionFile?.enabled) {
    writeVersionToFile(config, repoRoot, targetBase);
    filesToAdd.push(config.versionFile.path);
    console.log(`Version file updated: ${config.versionFile.path} -> ${targetBase}`);
  }

  // Step 9: Strip per-RC CHANGELOG entries for this target, then prepend the
  // single collapsed final entry with the aggregated notes.
  // Gate matches release-on-main.cjs: only when config.changelog.enabled is true.
  if (config.changelog?.enabled) {
    const changelogFile = config.changelog.file || 'CHANGELOG.md';
    stripRCEntries(repoRoot, changelogFile, tagPrefix, series.baseVersion);
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

    let hasStagedChanges;
    try {
      execSync('git diff --cached --quiet', { cwd: repoRoot, stdio: 'pipe' });
      hasStagedChanges = false;
    } catch (err) {
      if (err.status === 1) {
        hasStagedChanges = true;
      } else {
        fail(`git diff --cached --quiet failed (exit ${err.status}): ${err.message}`);
      }
    }
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

  // Step 13: Dispatch the Release workflow to build and attach binaries to
  // the final tag. workflow_dispatch triggers are allowed even with
  // GITHUB_TOKEN-authenticated calls (unlike push-triggered cascades).
  // Best-effort: the tag and GitHub Release already exist at this point, so
  // a dispatch failure is not fatal — binaries can be built manually by
  // re-running the Release workflow from the Actions tab.
  let dispatchOk = false;
  for (let attempt = 1; attempt <= 2; attempt++) {
    try {
      execOrThrow(
        `gh workflow run release.yml --ref "${targetTag}"`,
        { cwd: repoRoot }
      );
      dispatchOk = true;
      break;
    } catch (err) {
      if (attempt < 2) {
        console.log(`Release workflow dispatch attempt ${attempt} failed (${err.message}), retrying...`);
      } else {
        console.log(
          `WARNING: Release workflow dispatch failed after ${attempt} attempts: ${err.message}\n` +
          `The tag ${targetTag} and GitHub Release were created successfully.\n` +
          `To build binaries, manually run the Release workflow from Actions > SDLC Release for ref ${targetTag}.`
        );
      }
    }
  }
  if (dispatchOk) {
    console.log(`Dispatched Release workflow at ${targetTag}.`);
  }
}

// Only run when executed directly (`node promote-release.cjs <level>`) —
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
  findActiveRCSeries,
  findLatestStableTag,
  bumpSemver,
  parseSemver,
};
