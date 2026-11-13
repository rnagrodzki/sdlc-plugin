import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { checkDist, runCheck } from './dist-check.mjs';

const BASE = '/sdlc-plugin';
const page = (body) => `<!doctype html><html><head><title>t</title></head><body>${body}</body></html>`;

// Runs checkDist with only the options a test sets. Each test names its own pages and files.
function check({ pages, files = [], requiredRoutes = [], skillSteps = new Map() }) {
  return checkDist(new Map(Object.entries(pages)), new Set(files), { base: BASE, requiredRoutes, skillSteps });
}

const kinds = (findings) => findings.map((f) => f.kind);

describe('checkDist: broken-link', () => {
  test('reports a link to a page that is not in dist', () => {
    const findings = check({
      pages: { '/': page('<a href="/sdlc-plugin/docs/nope/">x</a>') },
      files: ['index.html'],
    });
    assert.deepEqual(findings, [{ route: '/', kind: 'broken-link', detail: '/sdlc-plugin/docs/nope/' }]);
  });

  test('accepts links to pages and files that exist', () => {
    const findings = check({
      pages: {
        '/': page(
          '<a href="/sdlc-plugin/docs/">a</a><a href="/sdlc-plugin/docs">b</a><a href="/sdlc-plugin/">c</a>' +
            '<link rel="sitemap" href="/sdlc-plugin/sitemap-index.xml"><a href="/sdlc-plugin/docs/#top">d</a>',
        ),
        '/docs/': page(''),
      },
      files: ['index.html', 'docs/index.html', 'sitemap-index.xml'],
    });
    assert.deepEqual(findings, []);
  });

  test('reports a link under the root that has no base', () => {
    const findings = check({
      pages: { '/': page('<a href="/docs/">x</a>') },
      files: ['index.html', 'docs/index.html'],
    });
    assert.deepEqual(kinds(findings), ['broken-link']);
    assert.match(findings[0].detail, /outside base/);
  });

  test('resolves a relative link against the route of the page', () => {
    const files = ['index.html', 'skills/index.html', 'skills/ship/index.html'];
    const pages = (href) => ({ '/skills/ship/': page(`<a href="${href}">x</a>`), '/skills/': page('') });
    assert.deepEqual(check({ pages: pages('../'), files }), []);
    assert.deepEqual(kinds(check({ pages: pages('../gone/'), files })), ['broken-link']);
  });

  test('decodes entities and percent signs before the lookup', () => {
    const findings = check({
      pages: { '/': page('<a href="/sdlc-plugin/a%20b/?x=1&amp;y=2">x</a>') },
      files: ['index.html', 'a b/index.html'],
    });
    assert.deepEqual(findings, []);
  });

  test('ignores links inside comments and script bodies', () => {
    const findings = check({
      pages: { '/': page('<!-- <a href="/sdlc-plugin/x/"> --><script>var s = \'<a href="/sdlc-plugin/y/">\';</script>') },
      files: ['index.html'],
    });
    assert.deepEqual(findings, []);
  });

  test('reports one line when the same broken link repeats on a page', () => {
    const findings = check({
      pages: { '/': page('<a href="/sdlc-plugin/x/">a</a><a href="/sdlc-plugin/x/">b</a>') },
      files: ['index.html'],
    });
    assert.equal(findings.length, 1);
  });
});

describe('checkDist: broken-anchor', () => {
  const files = ['index.html', 'docs/index.html'];

  test('reports an anchor that no id of the target page matches', () => {
    const findings = check({
      pages: { '/': page('<a href="/sdlc-plugin/docs/#missing">x</a>'), '/docs/': page('<h2 id="here">h</h2>') },
      files,
    });
    assert.deepEqual(findings, [{ route: '/', kind: 'broken-anchor', detail: '/sdlc-plugin/docs/#missing' }]);
  });

  test('reports an anchor on the same page that has no id', () => {
    const findings = check({ pages: { '/': page('<a href="#nope">x</a>') }, files });
    assert.deepEqual(kinds(findings), ['broken-anchor']);
  });

  test('accepts anchors that match an id or a name', () => {
    const findings = check({
      pages: {
        '/': page('<a href="/sdlc-plugin/docs/#here">x</a><a href="#self">y</a><a href="#">z</a><h2 id="self">s</h2>'),
        '/docs/': page('<h2 id="here">h</h2><a name="old"></a>'),
      },
      files,
    });
    assert.deepEqual(findings, []);
  });

  test('does not check an anchor on a file that is not a page', () => {
    const findings = check({
      pages: { '/': page('<a href="/sdlc-plugin/a.pdf#page=2">x</a>') },
      files: ['index.html', 'a.pdf'],
    });
    assert.deepEqual(findings, []);
  });
});

describe('checkDist: broken-media', () => {
  test('reports a src and a poster that name no file', () => {
    const findings = check({
      pages: { '/': page('<img src="/sdlc-plugin/a.png"><video poster="/sdlc-plugin/p.jpg" src="/sdlc-plugin/v.mp4"></video>') },
      files: ['index.html', 'v.mp4'],
    });
    assert.deepEqual(
      findings.map((f) => [f.kind, f.detail]),
      [
        ['broken-media', '/sdlc-plugin/a.png'],
        ['broken-media', '/sdlc-plugin/p.jpg'],
      ],
    );
  });

  test('accepts a src and a poster that name a file', () => {
    const findings = check({
      pages: { '/': page('<img src="/sdlc-plugin/a.png"><video poster="/sdlc-plugin/p.jpg"></video><script src="/sdlc-plugin/_astro/m.js"></script>') },
      files: ['index.html', 'a.png', 'p.jpg', '_astro/m.js'],
    });
    assert.deepEqual(findings, []);
  });
});

describe('checkDist: leftover-md-link', () => {
  test('reports a link that still names a Markdown file', () => {
    const findings = check({
      pages: { '/': page('<a href="skills/ship.md">x</a><a href="/sdlc-plugin/docs/a.md#b">y</a>') },
      files: ['index.html'],
    });
    assert.deepEqual(kinds(findings), ['leftover-md-link', 'leftover-md-link']);
  });

  test('accepts a link that names a page', () => {
    const findings = check({
      pages: { '/': page('<a href="/sdlc-plugin/skills/ship/">x</a>') },
      files: ['index.html', 'skills/ship/index.html'],
    });
    assert.deepEqual(findings, []);
  });
});

describe('checkDist: missing-route', () => {
  test('reports a required route that has no page', () => {
    const findings = check({ pages: { '/': page('') }, files: ['index.html'], requiredRoutes: ['/', '/skills/'] });
    assert.deepEqual(findings, [{ route: '/skills/', kind: 'missing-route', detail: 'no page in dist' }]);
  });

  test('accepts when every required route has a page', () => {
    const findings = check({
      pages: { '/': page(''), '/skills/': page('') },
      files: ['index.html', 'skills/index.html'],
      requiredRoutes: ['/', '/skills/'],
    });
    assert.deepEqual(findings, []);
  });
});

describe('checkDist: missing-step-text', () => {
  const route = '/skills/ship/';
  const steps = new Map([[route, ['Plan', 'Run `execute`']]]);

  test('reports a step name that the HTML does not hold as text', () => {
    const findings = check({
      pages: { [route]: page('<ol><li>Plan</li></ol>') },
      skillSteps: steps,
    });
    assert.deepEqual(findings, [{ route, kind: 'missing-step-text', detail: 'Run `execute`' }]);
  });

  test('does not count a step name that sits only in an attribute or a script', () => {
    const findings = check({
      pages: { [route]: page('<li title="Plan">x</li><script>var a = "Plan";</script>') },
      skillSteps: new Map([[route, ['Plan']]]),
    });
    assert.deepEqual(kinds(findings), ['missing-step-text']);
  });

  test('accepts a page that holds each step name, also across inline tags', () => {
    const findings = check({
      pages: { [route]: page('<li>Plan</li><li>Run <code>execute</code></li>') },
      skillSteps: steps,
    });
    assert.deepEqual(findings, []);
  });
});

describe('checkDist: skip cases', () => {
  test('skips external links, mail links and a src outside the base', () => {
    const findings = check({
      pages: {
        '/': page(
          '<a href="https://example.com/">a</a><a href="mailto:a@b.c">b</a><a href="//cdn.example.com/x">c</a>' +
            '<img src="https://cdn.example.com/x.png"><img src="/other/x.png"><img src="data:image/gif;base64,AAAA">',
        ),
      },
      files: ['index.html'],
    });
    assert.deepEqual(findings, []);
  });

  test('skips a skill with an empty step list', () => {
    const findings = check({
      pages: { '/skills/ship/': page('') },
      skillSteps: new Map([['/skills/ship/', []]]),
    });
    assert.deepEqual(findings, []);
  });
});

// ---- runCheck ---------------------------------------------------------------

const SKILL_MD = (steps = ['Plan'], facts = true) =>
  [
    '# Ship',
    '',
    '## Steps',
    '',
    '| Step | Actor | What it does | Hands over | Optional | When |',
    '|---|---|---|---|---|---|',
    ...steps.map((s) => `| ${s} | skill | Does it | out | no | always |`),
    '',
    ...(facts
      ? ['### Run facts', '', '| Fact | Value |', '|---|---|', '| Starts | a |', '| Ends | b |', '| Asks the user | c |', '| Resume | d |']
      : []),
    '',
    '## Notes',
    '',
  ].join('\n');

function dirent(name, kind) {
  return { name, isDirectory: () => kind === 'dir', isFile: () => kind === 'file' };
}

const TREE = {
  '/repo': [dirent('README.md', 'file'), dirent('CHANGELOG.md', 'file'), dirent('docs', 'dir')],
  '/repo/docs': [dirent('skills', 'dir')],
  '/repo/docs/skills': [dirent('README.md', 'file'), dirent('ship.md', 'file')],
};

const DIST_FILES = ['index.html', 'docs/index.html', 'skills/index.html', 'releases/index.html', 'skills/ship/index.html'];

// Builds the deps of runCheck. Each argument replaces the part of the default build that it names.
function deps({ md = {}, html = {}, files = DIST_FILES, listFilesError } = {}) {
  const contents = {
    '/repo/docs/skills/ship.md': SKILL_MD(),
    '/repo/docs/skills/README.md': '# Skills\n\nNo Steps table here.\n',
    '/dist/index.html': page(''),
    '/dist/docs/index.html': page(''),
    '/dist/skills/index.html': page(''),
    '/dist/releases/index.html': page(''),
    '/dist/skills/ship/index.html': page('<li>Plan</li>'),
    ...Object.fromEntries(Object.entries(md).map(([k, v]) => [`/repo/${k}`, v])),
    ...Object.fromEntries(Object.entries(html).map(([k, v]) => [`/dist/${k}`, v])),
  };
  return {
    distDir: '/dist',
    repoRoot: '/repo',
    base: BASE,
    listFiles: () => {
      if (listFilesError) throw new Error(listFilesError);
      return files;
    },
    readFile: (file) => {
      if (!(file in contents)) throw new Error(`ENOENT: ${file}`);
      return contents[file];
    },
    readdirSync: (dir) => TREE[dir],
  };
}

describe('runCheck', () => {
  test('returns exit code 0 and no line when dist is clean', () => {
    assert.deepEqual(runCheck(deps()), { exitCode: 0, lines: [] });
  });

  test('returns exit code 1 and one line for each finding', () => {
    const result = runCheck(
      deps({
        html: { 'skills/ship/index.html': page('<a href="/sdlc-plugin/nope/">x</a>') },
        files: DIST_FILES.filter((f) => f !== 'releases/index.html'),
      }),
    );
    assert.equal(result.exitCode, 1);
    assert.deepEqual(result.lines, [
      '/releases/ missing-route no page in dist',
      '/skills/ship/ broken-link /sdlc-plugin/nope/',
      '/skills/ship/ missing-step-text Plan',
    ]);
  });

  test('requires the pages of the fixed routes and of each published path', () => {
    const result = runCheck(deps({ files: ['index.html'], html: {} }));
    assert.deepEqual(
      result.lines.filter((l) => l.includes(' missing-route ')).map((l) => l.split(' ')[0]).sort(),
      ['/docs/', '/releases/', '/skills/', '/skills/ship/'],
    );
  });

  test('reports a skill page whose Steps table is bad, and does not crash', () => {
    const result = runCheck(deps({ md: { 'docs/skills/ship.md': SKILL_MD(['Plan'], false) } }));
    assert.equal(result.exitCode, 1);
    assert.deepEqual(result.lines, ['/skills/ship/ bad-steps-table no-facts']);
  });

  test('gives no bad-steps-table finding for docs/skills/README.md', () => {
    const result = runCheck(deps());
    assert.ok(!result.lines.some((l) => l.includes('bad-steps-table')));
    assert.ok(!result.lines.some((l) => l.startsWith('/skills/ ')));
  });

  test('passes a skill page only when its HTML holds each step name', () => {
    const md = { 'docs/skills/ship.md': SKILL_MD(['Plan', 'Review']) };
    assert.deepEqual(runCheck(deps({ md })).lines, ['/skills/ship/ missing-step-text Review']);
    const html = { 'skills/ship/index.html': page('<li>Plan</li><li>Review</li>') };
    assert.deepEqual(runCheck(deps({ md, html })), { exitCode: 0, lines: [] });
  });

  test('returns exit code 2 and the file name when a dist page cannot be read', () => {
    const result = runCheck(deps({ files: [...DIST_FILES, 'extra/index.html'] }));
    assert.equal(result.exitCode, 2);
    assert.equal(result.lines.length, 1);
    assert.match(result.lines[0], /^read error \/dist\/extra\/index\.html: ENOENT/);
  });

  test('returns exit code 2 and the file name when a Markdown file cannot be read', () => {
    const d = deps();
    const readFile = d.readFile;
    d.readFile = (file) => {
      if (file === '/repo/docs/skills/ship.md') throw new Error('EACCES');
      return readFile(file);
    };
    const result = runCheck(d);
    assert.deepEqual(result, { exitCode: 2, lines: ['read error /repo/docs/skills/ship.md: EACCES'] });
  });

  test('returns exit code 2 when dist cannot be listed', () => {
    const result = runCheck(deps({ listFilesError: 'ENOENT: no dist' }));
    assert.deepEqual(result, { exitCode: 2, lines: ['read error /dist: ENOENT: no dist'] });
  });
});
