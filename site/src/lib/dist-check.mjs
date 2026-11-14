// Scans the built site (dist/) for broken links, broken media, leftover Markdown
// links, missing pages and missing step text. Astro does not check links.
// The pure functions take a map of pages and injected file reads, so tests need no disk.
import path from 'node:path';
import { listPublished } from './published-paths.mjs';
import { pageKind, routeFor, skillsShapeErrors } from './doc-links.mjs';
import { parseSteps, StepsTableError } from './steps-table.mjs';

/** @typedef {{ route: string, kind: 'broken-link'|'broken-anchor'|'broken-media'|'leftover-md-link'|'missing-route'|'missing-step-text'|'bad-steps-table'|'bad-skills-page', detail: string }} Finding */

const ORIGIN = 'http://dist.invalid';
const SCHEME = /^[a-z][a-z0-9+.-]*:/i;
const ENTITIES = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ' };

function decodeEntities(text) {
  return text.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (match, entity) => {
    if (entity[0] === '#') {
      const code = entity[1].toLowerCase() === 'x' ? parseInt(entity.slice(2), 16) : parseInt(entity.slice(1), 10);
      return Number.isInteger(code) && code <= 0x10ffff ? String.fromCodePoint(code) : match;
    }
    return ENTITIES[entity.toLowerCase()] ?? match;
  });
}

function safeDecode(text) {
  try {
    return decodeURIComponent(text);
  } catch {
    return text;
  }
}

// Removes comments and the bodies of script and style elements. Their text holds no links.
function stripNoise(html) {
  return html
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<(script|style)\b([^>]*)>[\s\S]*?<\/\1\s*>/gi, '<$1$2></$1>');
}

const TAG_RE = /<([a-zA-Z][^\s/>]*)((?:"[^"]*"|'[^']*'|[^>"'])*)>/g;
const ATTR_RE = /([^\s"'<>/=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?/g;

/** @returns {{ tag: string, attrs: Map<string,string> }[]} */
function readTags(html) {
  const tags = [];
  for (const [, tag, rest] of html.matchAll(TAG_RE)) {
    const attrs = new Map();
    for (const [, name, dq, sq, bare] of rest.matchAll(ATTR_RE)) {
      const key = name.toLowerCase();
      if (!attrs.has(key)) attrs.set(key, decodeEntities(dq ?? sq ?? bare ?? ''));
    }
    tags.push({ tag: tag.toLowerCase(), attrs });
  }
  return tags;
}

function textOf(strippedHtml) {
  return decodeEntities(strippedHtml.replace(/<[^>]*>/g, '')).replace(/\s+/g, ' ');
}

function trimBase(base) {
  return base.replace(/\/+$/, '');
}

/**
 * Resolves a link value against the page route.
 * Returns null for a value that the scan skips: empty, external, or mail.
 * @returns {{ pathname: string, hash: string, inside: boolean, inner: string } | null}
 */
function resolveLink(raw, route, base) {
  const value = raw.trim();
  if (value === '' || SCHEME.test(value) || value.startsWith('//')) return null;
  let url;
  try {
    url = new URL(value, `${ORIGIN}${base}${route}`);
  } catch {
    return null;
  }
  const pathname = safeDecode(url.pathname);
  const inside = base === '' || pathname === base || pathname.startsWith(`${base}/`);
  const inner = inside ? pathname.slice(base.length) || '/' : pathname;
  return { pathname, hash: safeDecode(url.hash.slice(1)), inside, inner };
}

/** @returns {string | undefined} the dist-relative file that a dist-inner path names */
function findFile(inner, files) {
  const rel = inner.replace(/^\//, '');
  const candidates = inner.endsWith('/') ? [`${rel}index.html`] : [rel, `${rel}/index.html`, `${rel}.html`];
  return candidates.find((c) => files.has(c));
}

const routeOfFile = (file) => `/${file}`.replace(/\/index\.html$/, '/');

/**
 * @param {Map<string,string>} pages route -> HTML
 * @param {Set<string>} files dist-relative paths of all files
 * @param {{ base: string, requiredRoutes: Iterable<string>, skillSteps: Map<string,string[]> }} options
 * @returns {Finding[]}
 */
export function checkDist(pages, files, { base, requiredRoutes, skillSteps }) {
  const b = trimBase(base);
  /** @type {Finding[]} */
  const findings = [];

  for (const route of requiredRoutes) {
    if (!pages.has(route)) findings.push({ route, kind: 'missing-route', detail: 'no page in dist' });
  }

  const stripped = new Map();
  const tagsByRoute = new Map();
  for (const [route, html] of pages) {
    const clean = stripNoise(html);
    stripped.set(route, clean);
    tagsByRoute.set(route, readTags(clean));
  }

  const idCache = new Map();
  const idsOf = (route) => {
    if (!idCache.has(route)) {
      const ids = new Set();
      for (const { attrs } of tagsByRoute.get(route)) {
        if (attrs.has('id')) ids.add(attrs.get('id'));
        if (attrs.has('name')) ids.add(attrs.get('name'));
      }
      idCache.set(route, ids);
    }
    return idCache.get(route);
  };

  for (const route of pages.keys()) {
    const seen = new Set();
    const add = (kind, detail) => {
      const key = `${kind}\u0000${detail}`;
      if (seen.has(key)) return;
      seen.add(key);
      findings.push({ route, kind, detail });
    };

    for (const { attrs } of tagsByRoute.get(route)) {
      if (attrs.has('href')) {
        const href = attrs.get('href');
        const link = resolveLink(href, route, b);
        if (link) {
          if (link.pathname.toLowerCase().endsWith('.md')) {
            add('leftover-md-link', href);
          } else if (!link.inside) {
            add('broken-link', `${href} (outside base ${b})`);
          } else {
            const file = findFile(link.inner, files);
            if (!file) {
              add('broken-link', href);
            } else if (link.hash && link.hash !== 'top' && file.endsWith('.html')) {
              const target = pages.get(routeOfFile(file));
              if (target !== undefined && !idsOf(routeOfFile(file)).has(link.hash)) add('broken-anchor', href);
            }
          }
        }
      }

      for (const name of ['src', 'poster']) {
        if (!attrs.has(name)) continue;
        const value = attrs.get(name);
        const media = resolveLink(value, route, b);
        // A value outside the base is not a file of this build, so the scan skips it.
        if (media && media.inside && !findFile(media.inner, files)) add('broken-media', value);
      }
    }
  }

  for (const [route, names] of skillSteps) {
    const clean = stripped.get(route);
    if (clean === undefined) continue; // missing-route reports this page
    const text = textOf(clean);
    for (const name of names) {
      const wanted = name.replace(/`/g, '').replace(/\s+/g, ' ').trim();
      if (!text.includes(wanted)) findings.push({ route, kind: 'missing-step-text', detail: name });
    }
  }

  return findings;
}

class ReadError extends Error {}

/**
 * @param {{ distDir: string, repoRoot: string, base: string,
 *   listFiles: (dir: string) => Iterable<string>,
 *   readFile: (file: string) => string,
 *   readdirSync: Parameters<typeof listPublished>[1]['readdirSync'] }} deps
 * @returns {{ exitCode: 0|1|2, lines: string[] }}
 */
export function runCheck({ distDir, repoRoot, base, listFiles, readFile, readdirSync }) {
  const read = (file) => {
    try {
      return readFile(file);
    } catch (err) {
      throw new ReadError(`read error ${file}: ${err.message}`);
    }
  };

  try {
    let files;
    try {
      files = new Set([...listFiles(distDir)].sort());
    } catch (err) {
      throw new ReadError(`read error ${distDir}: ${err.message}`);
    }

    const pages = new Map();
    for (const file of files) {
      if (file.endsWith('.html')) pages.set(routeOfFile(file), read(path.join(distDir, file)));
    }

    let published;
    try {
      published = listPublished(repoRoot, { readdirSync });
    } catch (err) {
      throw new ReadError(`read error ${repoRoot}: ${err.message}`);
    }

    // A page under docs/skills/ with a bad shape has no route (routeFor throws for it), so the
    // scan reports it once and leaves it out of the other checks.
    const shapeErrors = skillsShapeErrors(published);
    const badShape = new Set(shapeErrors.map((e) => e.repoPath));
    const shapeFindings = shapeErrors.map(({ repoPath, reason }) => ({
      route: '/skills/',
      kind: /** @type {const} */ ('bad-skills-page'),
      detail: reason === 'no-overview' ? `${repoPath} (no overview page)` : `${repoPath} (not a skill overview or reference page)`,
    }));
    published = published.filter((p) => !badShape.has(p));

    const requiredRoutes = new Set(['/', '/skills/', '/docs/', '/releases/']);
    for (const p of published) requiredRoutes.add(routeFor(p, { base: '' }));

    const skillSteps = new Map();
    const stepFindings = [];
    for (const p of published) {
      if (pageKind(p) !== 'skill') continue;
      const route = routeFor(p, { base: '' });
      try {
        skillSteps.set(route, parseSteps(read(path.join(repoRoot, p))).steps.map((s) => s.name));
      } catch (err) {
        if (!(err instanceof StepsTableError)) throw err;
        stepFindings.push({ route, kind: 'bad-steps-table', detail: err.code });
      }
    }

    const findings = [...shapeFindings, ...checkDist(pages, files, { base, requiredRoutes, skillSteps }), ...stepFindings];
    return {
      exitCode: findings.length > 0 ? 1 : 0,
      lines: findings.map((f) => `${f.route} ${f.kind} ${f.detail}`),
    };
  } catch (err) {
    if (err instanceof ReadError) return { exitCode: 2, lines: [err.message] };
    throw err;
  }
}
