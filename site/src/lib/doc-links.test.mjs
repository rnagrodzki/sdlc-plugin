import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  DocLinkError,
  assertUniqueRoutes,
  pageKind,
  remarkDocLinks,
  resolveDocHref,
  routeFor,
  skillOf,
  skillsShapeErrors,
} from './doc-links.mjs';
import { listPublished } from './published-paths.mjs';

const base = '/sdlc-plugin';
const repoBlobUrl = 'https://github.com/rnagrodzki/sdlc-plugin/blob/main';
const publishedPaths = [
  'README.md',
  'CHANGELOG.md',
  'docs/getting-started.md',
  'docs/versioning.md',
  'docs/skills/README.md',
  'docs/skills/plan.md',
  'docs/skills/ship.md',
  'docs/skills/ship/flags.md',
  'docs/skills/ship/state-and-trace.md',
];
const ctx = { publishedPaths, base, repoBlobUrl };

const routeRows = [
  ['README.md', '/sdlc-plugin/docs/'],
  ['docs/skills/README.md', '/sdlc-plugin/skills/'],
  ['docs/skills/plan.md', '/sdlc-plugin/skills/plan/'],
  ['docs/skills/ship/flags.md', '/sdlc-plugin/skills/ship/flags/'],
  ['docs/skills/ship/State-And-Trace.md', '/sdlc-plugin/skills/ship/state-and-trace/'],
  ['docs/getting-started.md', '/sdlc-plugin/docs/getting-started/'],
  ['docs/guides/Deep.md', '/sdlc-plugin/docs/guides/deep/'],
  ['CHANGELOG.md', '/sdlc-plugin/releases/'],
];

describe('routeFor', () => {
  for (const [repoPath, route] of routeRows) {
    test(`${repoPath} -> ${route}`, () => {
      assert.equal(routeFor(repoPath, { base }), route);
    });
  }

  test('folds an index file into the route of its folder', () => {
    assert.equal(routeFor('docs/index.md', { base }), '/sdlc-plugin/docs/');
    assert.equal(routeFor('docs/guides/index.md', { base }), '/sdlc-plugin/docs/guides/');
  });

  test('accepts a base with a trailing slash', () => {
    assert.equal(routeFor('CHANGELOG.md', { base: '/sdlc-plugin/' }), '/sdlc-plugin/releases/');
  });

  test('throws for a path that is not published', () => {
    assert.throws(() => routeFor('docs/internal/smoke-test.md', { base }), /not a published/);
    assert.throws(() => routeFor('plugins/sdlc/README.md', { base }), /not a published/);
  });

  test('throws for a page nested deeper than a reference page, with the file name', () => {
    assert.throws(() => routeFor('docs/skills/ship/deep/x.md', { base }), /docs\/skills\/ship\/deep\/x\.md.*reference page/);
  });
});

describe('skillOf', () => {
  test('returns the skill of an overview page and of a reference page', () => {
    assert.equal(skillOf('docs/skills/ship.md'), 'ship');
    assert.equal(skillOf('docs/skills/ship/flags.md'), 'ship');
  });

  test('throws for another page', () => {
    assert.throws(() => skillOf('docs/getting-started.md'), /not a skill/);
  });
});

describe('skillsShapeErrors', () => {
  test('accepts the README, overview pages and reference pages with an overview page', () => {
    assert.deepEqual(skillsShapeErrors(publishedPaths), []);
  });

  test('reports a reference page without an overview page, with the file name', () => {
    const errors = skillsShapeErrors(['docs/skills/ship.md', 'docs/skills/nope/x.md']);
    assert.equal(errors.length, 1);
    assert.equal(errors[0].repoPath, 'docs/skills/nope/x.md');
    assert.equal(errors[0].reason, 'no-overview');
    assert.match(errors[0].message, /"docs\/skills\/nope\/x\.md" has no overview page "docs\/skills\/nope\.md"/);
  });

  test('reports a page nested deeper than a reference page', () => {
    const errors = skillsShapeErrors(['docs/skills/ship.md', 'docs/skills/ship/deep/x.md', 'docs/guides/a/b.md']);
    assert.deepEqual(
      errors.map((e) => [e.repoPath, e.reason]),
      [['docs/skills/ship/deep/x.md', 'bad-path']],
    );
  });
});

describe('pageKind', () => {
  const rows = [
    ['README.md', 'docs-index'],
    ['docs/index.md', 'docs-index'],
    ['docs/skills/README.md', 'skills-index'],
    ['docs/skills/plan.md', 'skill'],
    ['docs/skills/ship/flags.md', 'skill-ref'],
    ['docs/getting-started.md', 'doc'],
    ['docs/guides/deep/page.md', 'doc'],
    ['CHANGELOG.md', 'releases'],
  ];
  for (const [repoPath, kind] of rows) {
    test(`${repoPath} is ${kind}`, () => {
      assert.equal(pageKind(repoPath), kind);
    });
  }

  test('throws for an internal path', () => {
    assert.throws(() => pageKind('docs/internal/x.md'), /not a published/);
  });
});

describe('assertUniqueRoutes', () => {
  test('throws route-clash for README.md with docs/index.md', () => {
    assert.throws(
      () => assertUniqueRoutes(['README.md', 'docs/index.md'], { base }),
      (err) => err instanceof DocLinkError && err.code === 'route-clash',
    );
  });

  test('throws route-clash for docs/A.md with docs/a.md', () => {
    assert.throws(
      () => assertUniqueRoutes(['docs/A.md', 'docs/a.md'], { base }),
      (err) => err instanceof DocLinkError && err.code === 'route-clash' && /docs\/A\.md/.test(err.message),
    );
  });

  test('returns the route map for the real published paths', () => {
    const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
    const paths = listPublished(repoRoot, fs);
    const routes = assertUniqueRoutes(paths, { base });
    assert.equal(routes.size, paths.length);
    assert.equal(routes.get('/sdlc-plugin/docs/'), 'README.md');
    assert.equal(routes.get('/sdlc-plugin/releases/'), 'CHANGELOG.md');
  });
});

describe('resolveDocHref', () => {
  const rows = [
    ['README.md', 'docs/getting-started.md', '/sdlc-plugin/docs/getting-started/'],
    ['docs/getting-started.md', '../README.md', '/sdlc-plugin/docs/'],
    ['docs/getting-started.md', 'skills/README.md', '/sdlc-plugin/skills/'],
    ['docs/getting-started.md', 'skills/plan.md', '/sdlc-plugin/skills/plan/'],
    ['docs/skills/plan.md', '../versioning.md', '/sdlc-plugin/docs/versioning/'],
    ['docs/versioning.md', '../CHANGELOG.md', '/sdlc-plugin/releases/'],
    ['docs/skills/README.md', '../internal/smoke-test.md', `${repoBlobUrl}/docs/internal/smoke-test.md`],
    ['README.md', 'plugins/sdlc/skills/plan/SKILL.md', `${repoBlobUrl}/plugins/sdlc/skills/plan/SKILL.md`],
    ['docs/versioning.md', '../LICENSE', `${repoBlobUrl}/LICENSE`],
    // An overview page and its reference pages, in both directions.
    ['docs/skills/ship.md', './ship/flags.md', '/sdlc-plugin/skills/ship/flags/'],
    ['docs/skills/ship.md', 'ship/state-and-trace.md#trace', '/sdlc-plugin/skills/ship/state-and-trace/#trace'],
    ['docs/skills/ship/flags.md', '../ship.md', '/sdlc-plugin/skills/ship/'],
    ['docs/skills/ship/flags.md', './state-and-trace.md', '/sdlc-plugin/skills/ship/state-and-trace/'],
    ['docs/skills/ship/flags.md', '../plan.md#anchor', '/sdlc-plugin/skills/plan/#anchor'],
    ['docs/skills/ship/flags.md', '../../versioning.md', '/sdlc-plugin/docs/versioning/'],
  ];
  for (const [fromPath, href, expected] of rows) {
    test(`${fromPath}: ${href}`, () => {
      assert.equal(resolveDocHref(href, { ...ctx, fromPath }), expected);
    });
  }

  test('keeps the #anchor and the query', () => {
    assert.equal(
      resolveDocHref('skills/plan.md#step-2', { ...ctx, fromPath: 'docs/getting-started.md' }),
      '/sdlc-plugin/skills/plan/#step-2',
    );
    assert.equal(
      resolveDocHref('../internal/x.md?plain=1#L3', { ...ctx, fromPath: 'docs/skills/README.md' }),
      `${repoBlobUrl}/docs/internal/x.md?plain=1#L3`,
    );
  });

  test('decodes an escaped path before it looks up the target', () => {
    assert.equal(
      resolveDocHref('get%2Dstarted.md', { ...ctx, publishedPaths: ['docs/get-started.md'], fromPath: 'docs/versioning.md' }),
      '/sdlc-plugin/docs/get-started/',
    );
  });

  test('keeps external links, mailto links and same-page anchors', () => {
    for (const href of ['https://example.com/a.md', 'http://example.com', 'mailto:a@b.co', '#top', '//cdn.example.com/x', '']) {
      assert.equal(resolveDocHref(href, { ...ctx, fromPath: 'README.md' }), href);
    }
  });

  test('throws missing-target for a missing docs file', () => {
    assert.throws(
      () => resolveDocHref('nope.md', { ...ctx, fromPath: 'docs/getting-started.md' }),
      (err) => err instanceof DocLinkError && err.code === 'missing-target' && /docs\/nope\.md/.test(err.message),
    );
  });

  test('throws missing-target for a link that leaves the repository', () => {
    assert.throws(
      () => resolveDocHref('../../x.md', { ...ctx, fromPath: 'docs/getting-started.md' }),
      (err) => err instanceof DocLinkError && err.code === 'missing-target',
    );
  });

  test('accepts a Set of published paths', () => {
    assert.equal(
      resolveDocHref('versioning.md', { ...ctx, publishedPaths: new Set(publishedPaths), fromPath: 'docs/getting-started.md' }),
      '/sdlc-plugin/docs/versioning/',
    );
  });
});

describe('remarkDocLinks', () => {
  const repoRoot = '/work/repo';
  const link = (url) => ({ type: 'link', url, children: [{ type: 'text', value: 'x' }] });
  const run = (tree, relPath) => {
    const transform = remarkDocLinks({ ...ctx, repoRoot });
    transform(tree, { path: path.join(repoRoot, relPath) });
    return tree;
  };

  test('rewrites link and definition nodes, also nested ones', () => {
    const tree = {
      type: 'root',
      children: [
        { type: 'paragraph', children: [link('plan.md#a'), link('https://example.com'), link('#same')] },
        { type: 'list', children: [{ type: 'listItem', children: [{ type: 'paragraph', children: [link('../internal/smoke-test.md')] }] }] },
        { type: 'definition', identifier: 'ref', url: '../versioning.md' },
      ],
    };
    run(tree, 'docs/skills/README.md');
    const [para, list, def] = tree.children;
    assert.deepEqual(
      para.children.map((n) => n.url),
      ['/sdlc-plugin/skills/plan/#a', 'https://example.com', '#same'],
    );
    assert.equal(list.children[0].children[0].children[0].url, `${repoBlobUrl}/docs/internal/smoke-test.md`);
    assert.equal(def.url, '/sdlc-plugin/docs/versioning/');
  });

  test('resolves links from the repository path of the file, not the file name', () => {
    // From docs/skills/plan.md, "../versioning.md" is docs/versioning.md.
    // A resolver that used only the file name or the repo root would miss it.
    const tree = { type: 'root', children: [link('../versioning.md')] };
    run(tree, 'docs/skills/plan.md');
    assert.equal(tree.children[0].url, '/sdlc-plugin/docs/versioning/');
  });

  test('throws DocLinkError for a missing target', () => {
    const tree = { type: 'root', children: [link('missing.md')] };
    assert.throws(() => run(tree, 'docs/getting-started.md'), (err) => err instanceof DocLinkError && err.code === 'missing-target');
  });

  test('throws when the file has no path', () => {
    const transform = remarkDocLinks({ ...ctx, repoRoot });
    assert.throws(() => transform({ type: 'root', children: [] }, {}), /no path/);
  });
});
