// The flat list of the docs pages, for the page list of DocLayout and the index at /docs/,
// and the page list of one skill, for the reference pages of that skill.
import path from 'node:path';
import { getCollection } from 'astro:content';
import { BASE } from '../../site.config.mjs';
import { pageKind, routeFor, skillOf } from './doc-links.mjs';
import { docMeta } from './doc-meta.mjs';

export interface DocsPage {
  title: string; // the H1 of the docs file
  lead: string; // the first sentence of the docs file, '' when it has none
  href: string; // the route, with the base
}

/** The docs file a new reader starts with. It is the first page of the list. */
const FIRST = 'docs/getting-started.md';

/** @returns the pages of kind `doc`: the first page, then the others in the order of their titles */
export async function docsPages(): Promise<DocsPage[]> {
  const repoRoot = path.resolve('..');
  return (await getCollection('docs'))
    .flatMap((entry) => {
      if (!entry.filePath) return [];
      // entry.filePath is relative to site/.
      const repoPath = path.relative(repoRoot, path.resolve(entry.filePath)).split(path.sep).join('/');
      if (pageKind(repoPath) !== 'doc') return [];
      const { title, lead } = docMeta(entry.body ?? '', repoPath);
      return [{ repoPath, title, lead, href: routeFor(repoPath, { base: BASE }) }];
    })
    .sort((a, b) => Number(b.repoPath === FIRST) - Number(a.repoPath === FIRST) || a.title.localeCompare(b.title))
    .map(({ title, lead, href }) => ({ title, lead, href }));
}

/**
 * @returns the page list of one skill: the overview page first (title Overview),
 * then its reference pages in the order of their titles
 */
export async function skillPages(skill: string /* lower case, as in the route */): Promise<DocsPage[]> {
  const repoRoot = path.resolve('..');
  const pages = (await getCollection('docs')).flatMap((entry) => {
    if (!entry.filePath) return [];
    // entry.filePath is relative to site/.
    const repoPath = path.relative(repoRoot, path.resolve(entry.filePath)).split(path.sep).join('/');
    const kind = pageKind(repoPath);
    if ((kind !== 'skill' && kind !== 'skill-ref') || skillOf(repoPath).toLowerCase() !== skill) return [];
    const { title, lead } = docMeta(entry.body ?? '', repoPath);
    return [{ kind, title: kind === 'skill' ? 'Overview' : title, lead, href: routeFor(repoPath, { base: BASE }) }];
  });
  return pages
    .sort((a, b) => Number(b.kind === 'skill') - Number(a.kind === 'skill') || a.title.localeCompare(b.title))
    .map(({ title, lead, href }) => ({ title, lead, href }));
}

/** The two pages outside /docs/ that the page list also names. */
export const MORE_PAGES: DocsPage[] = [
  { title: 'Skills', lead: 'One page for each skill, with its command and its steps.', href: `${BASE}/skills/` },
  { title: 'Release notes', lead: 'What changed in each version, the newest version first.', href: `${BASE}/releases/` },
];
