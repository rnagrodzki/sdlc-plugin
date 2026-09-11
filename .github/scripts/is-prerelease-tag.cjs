#!/usr/bin/env node
/**
 * is-prerelease-tag.cjs
 * CI helper: prints "true" if the given tag has a semver pre-release suffix,
 * "false" otherwise.
 *
 * Mirrors the logic in internal/gitx/gitx.go (semverTagPreRe) — a tag like
 * v1.2.3-rc.1 is a pre-release; v1.2.3 is not.
 *
 * Usage:
 *   node .github/scripts/is-prerelease-tag.cjs <tag>
 *
 * Exit code is always 0. Stdout is "true" or "false".
 *
 * Uses only Node.js built-in modules. No npm install required.
 */

'use strict';

const tag = (process.argv[2] || '').trim();
if (!tag) {
  process.stderr.write('Usage: is-prerelease-tag.cjs <tag>\n');
  process.exit(1);
}

// Strip optional leading 'v' and the M.M.P core — anything left means pre-release.
const remainder = tag.replace(/^v?\d+\.\d+\.\d+/, '');
console.log(remainder ? 'true' : 'false');
