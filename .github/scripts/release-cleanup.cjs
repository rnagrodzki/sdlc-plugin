#!/usr/bin/env node
/**
 * release-cleanup.cjs
 * CI script: deletes old GitHub Releases (and their binary assets) to free
 * storage, keeping only the most recent ones.
 *
 * Deletes the Release object only — never passes `--cleanup-tag` to
 * `gh release delete`, so git tags are never removed. Drafts are left alone
 * (not real releases yet, no assets to reclaim from a scheduled sweep).
 *
 * Usage:
 *   node .github/scripts/release-cleanup.cjs [keepCount]
 *   keepCount defaults to 10.
 *
 * Requires the `gh` CLI, authenticated (GH_TOKEN/GITHUB_TOKEN in env).
 * Uses only Node.js built-in modules. No npm install required.
 */

'use strict';

const { execFileSync } = require('node:child_process');

const DEFAULT_KEEP_COUNT = 10;

/**
 * Pick which releases to delete: sort by createdAt (newest first), keep the
 * first keepCount, return the rest. Drafts are excluded from both the kept
 * and deleted sets — they carry no published assets to reclaim.
 * @param {{tagName: string, createdAt: string, isDraft: boolean}[]} releases
 * @param {number} keepCount
 * @returns {{tagName: string, createdAt: string, isDraft: boolean}[]}
 */
function selectReleasesToDelete(releases, keepCount) {
  const published = releases.filter(r => !r.isDraft);
  const sorted = [...published].sort((a, b) => new Date(b.createdAt) - new Date(a.createdAt));
  return sorted.slice(keepCount);
}

function listReleases() {
  const out = execFileSync(
    'gh', ['release', 'list', '--json', 'tagName,createdAt,isDraft', '--limit', '1000'],
    { encoding: 'utf8' }
  );
  return JSON.parse(out);
}

function deleteRelease(tagName) {
  execFileSync('gh', ['release', 'delete', tagName, '--yes'], { stdio: 'inherit' });
}

function main() {
  const keepCount = process.argv[2] ? Number(process.argv[2]) : DEFAULT_KEEP_COUNT;
  if (!Number.isInteger(keepCount) || keepCount < 0) {
    process.stderr.write(`Invalid keepCount: ${process.argv[2]}\n`);
    process.exit(1);
  }

  const releases = listReleases();
  const toDelete = selectReleasesToDelete(releases, keepCount);

  if (toDelete.length === 0) {
    console.log(`${releases.length} release(s) found, keeping ${keepCount}. Nothing to delete.`);
    return;
  }

  console.log(`${releases.length} release(s) found, keeping ${keepCount}. Deleting ${toDelete.length}:`);
  for (const r of toDelete) {
    console.log(`  ${r.tagName} (created ${r.createdAt})`);
    deleteRelease(r.tagName);
  }
  console.log('Done. Git tags were not touched.');
}

// Only run when executed directly (`node release-cleanup.cjs`) — requiring
// this file as a module (e.g. from tests) must not trigger a live CI run.
if (require.main === module) { main(); }

module.exports = { selectReleasesToDelete, DEFAULT_KEEP_COUNT };
