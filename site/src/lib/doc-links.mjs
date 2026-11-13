// Maps repository Markdown paths to site routes and rewrites links in docs files.
// `base` and `repoBlobUrl` come from site.config.mjs.
import path from 'node:path';

export class DocLinkError extends Error {
  /** @type {'missing-target'|'route-clash'} */
  code;

  /**
   * @param {'missing-target'|'route-clash'} code
   * @param {string} message
   */
  constructor(code, message) {
    super(message);
    this.name = 'DocLinkError';
    this.code = code;
  }
}

const SCHEME = /^[a-z][a-z0-9+.-]*:/i;

function trimBase(base) {
  return base.replace(/\/+$/, '');
}

// A path has the shape of a published page when the site builds a page for it.
function isPublishable(repoPath) {
  if (repoPath === 'README.md' || repoPath === 'CHANGELOG.md') return true;
  return (
    repoPath.startsWith('docs/') &&
    repoPath.endsWith('.md') &&
    !repoPath.startsWith('docs/internal/')
  );
}

// Returns the page kind and the route path (no base, no slashes at the ends).
function classify(repoPath) {
  if (repoPath === 'README.md') return { kind: 'docs-index', tail: '' };
  if (repoPath === 'CHANGELOG.md') return { kind: 'releases', tail: '' };
  if (repoPath === 'docs/skills/README.md') return { kind: 'skills-index', tail: '' };
  if (!isPublishable(repoPath)) {
    throw new Error(`"${repoPath}" is not a published Markdown path`);
  }
  const skill = /^docs\/skills\/([^/]+)\.md$/.exec(repoPath);
  if (skill) return { kind: 'skill', tail: skill[1].toLowerCase() };
  // docs/index.md and docs/<dir>/index.md take the route of their folder.
  let tail = repoPath.slice('docs/'.length, -'.md'.length);
  if (tail === 'index') tail = '';
  else if (tail.endsWith('/index')) tail = tail.slice(0, -'/index'.length);
  return { kind: tail === '' ? 'docs-index' : 'doc', tail: tail.toLowerCase() };
}

/**
 * @returns {string} the site route of a published path, lower case, with a trailing slash
 */
export function routeFor(repoPath, { base }) {
  const b = trimBase(base);
  const { kind, tail } = classify(repoPath);
  switch (kind) {
    case 'docs-index':
      return `${b}/docs/`;
    case 'skills-index':
      return `${b}/skills/`;
    case 'skill':
      return `${b}/skills/${tail}/`;
    case 'releases':
      return `${b}/releases/`;
    default:
      return `${b}/docs/${tail}/`;
  }
}

/**
 * @returns {'docs-index'|'skills-index'|'skill'|'doc'|'releases'} the page kind of a published path.
 * Tasks 21 and 23 filter the collection entries with it.
 */
export function pageKind(repoPath) {
  return classify(repoPath).kind;
}

/**
 * @returns {Map<string,string>} route -> repoPath. Throws 'route-clash' when two paths give one route
 */
export function assertUniqueRoutes(repoPaths, { base }) {
  const routes = new Map();
  for (const repoPath of repoPaths) {
    const route = routeFor(repoPath, { base });
    const other = routes.get(route);
    if (other !== undefined) {
      throw new DocLinkError(
        'route-clash',
        `"${other}" and "${repoPath}" both map to the route ${route}. Rename or remove one of the files.`,
      );
    }
    routes.set(route, repoPath);
  }
  return routes;
}

/**
 * @returns {string} site URL or GitHub URL
 */
export function resolveDocHref(href, { fromPath, publishedPaths, base, repoBlobUrl }) {
  // Same-page anchors, absolute URLs (https:, mailto:, ...), protocol-relative and root-relative links stay.
  if (href === '' || href.startsWith('#') || href.startsWith('/') || SCHEME.test(href)) {
    return href;
  }

  const suffixAt = href.search(/[?#]/);
  const rawPath = suffixAt === -1 ? href : href.slice(0, suffixAt);
  const suffix = suffixAt === -1 ? '' : href.slice(suffixAt);

  let decoded = rawPath;
  try {
    decoded = decodeURIComponent(rawPath);
  } catch {
    // keep the raw text when it holds a bad escape sequence
  }

  const target = path.posix.normalize(path.posix.join(path.posix.dirname(fromPath), decoded));
  const missing = () =>
    new DocLinkError('missing-target', `Link "${href}" in ${fromPath} points to "${target}", which is not a published file.`);

  if (target === '..' || target.startsWith('../')) throw missing();

  const published = publishedPaths instanceof Set ? publishedPaths : new Set(publishedPaths);
  if (published.has(target)) return routeFor(target, { base }) + suffix;
  if (isPublishable(target)) throw missing();
  return `${repoBlobUrl}/${target}${suffix}`;
}

function walk(node, visit) {
  visit(node);
  if (Array.isArray(node.children)) {
    for (const child of node.children) walk(child, visit);
  }
}

/**
 * Remark plugin: rewrites each link node of a docs file with resolveDocHref.
 * fromPath = path.relative(repoRoot, file.path), so links resolve from the repository path.
 */
export function remarkDocLinks({ publishedPaths, base, repoBlobUrl, repoRoot }) {
  const published = new Set(publishedPaths);
  return (tree, file) => {
    if (!file || !file.path) throw new Error('remarkDocLinks: the file has no path');
    const fromPath = path.relative(repoRoot, file.path).split(path.sep).join('/');
    walk(tree, (node) => {
      // `link` is [text](url). `definition` is the [ref]: url line of a reference link.
      if ((node.type === 'link' || node.type === 'definition') && typeof node.url === 'string') {
        node.url = resolveDocHref(node.url, { fromPath, publishedPaths: published, base, repoBlobUrl });
      }
    });
  };
}
