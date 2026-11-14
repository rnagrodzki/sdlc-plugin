// Lists the Markdown files that the site publishes.
// astro.config.mjs and check-dist.mjs call it with node:fs.
import path from 'node:path';

const ROOT_FILES = ['README.md', 'CHANGELOG.md'];

/**
 * @param {string} repoRoot absolute path of the repository root
 * @param {{ readdirSync: (dir: string, opts: { withFileTypes: true }) => import('node:fs').Dirent[] }} fs
 * @returns {string[]} repository paths of the published Markdown files, sorted
 */
export function listPublished(repoRoot, { readdirSync }) {
  const found = [];

  const rootEntries = readdirSync(repoRoot, { withFileTypes: true });
  for (const entry of rootEntries) {
    if (entry.isFile() && ROOT_FILES.includes(entry.name)) found.push(entry.name);
  }

  const walk = (repoDir) => {
    for (const entry of readdirSync(path.join(repoRoot, repoDir), { withFileTypes: true })) {
      const repoPath = `${repoDir}/${entry.name}`;
      if (entry.isDirectory()) {
        if (repoPath !== 'docs/internal') walk(repoPath);
      } else if (entry.isFile() && entry.name.endsWith('.md')) {
        found.push(repoPath);
      }
    }
  };

  // The docs folder is optional: a missing folder means no docs pages.
  const hasDocs = rootEntries.some((e) => e.isDirectory() && e.name === 'docs');
  if (hasDocs) walk('docs');

  return found.sort();
}
