/**
 * The change gate of the Site workflow (.github/workflows/site.yml).
 * A pull request runs the site checks only when it changes a path that feeds
 * the site. CHANGELOG.md is not a site path: the next push to main rebuilds
 * the release notes.
 */

const SITE_PREFIXES = ['docs/', 'site/'];
const SITE_FILES = ['README.md', '.github/workflows/site.yml'];

/**
 * @param {string} path a repository path with forward slashes, as git prints it
 * @returns {boolean} true for docs/**, README.md, site/** and .github/workflows/site.yml
 */
export function isSitePath(path) {
  return SITE_FILES.includes(path) || SITE_PREFIXES.some((prefix) => path.startsWith(prefix));
}

/**
 * @param {string[]} paths the changed paths of a pull request
 * @returns {'site=true'|'site=false'} the line for $GITHUB_OUTPUT
 */
export function siteChanged(paths) {
  return paths.some(isSitePath) ? 'site=true' : 'site=false';
}
