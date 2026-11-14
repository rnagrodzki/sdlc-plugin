import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { isSitePath, siteChanged } from './site-paths.mjs';

const CASES = [
  ['internal/tools/plan.go', false],
  ['docs/skills/ship.md', true],
  ['README.md', true],
  ['docs/README.md.bak', true],
  ['site/src/pages/index.astro', true],
  ['.github/workflows/site.yml', true],
  ['.github/workflows/ci.yml', false],
  ['CHANGELOG.md', false],
  ['docsx/a.md', false],
  ['sites/a.md', false],
  ['site-notes.md', false],
];

for (const [path, expected] of CASES) {
  test(`siteChanged([${path}]) is site=${expected}`, () => {
    assert.equal(isSitePath(path), expected);
    assert.equal(siteChanged([path]), `site=${expected}`);
  });
}

test('no changed file is site=false', () => {
  assert.equal(siteChanged([]), 'site=false');
});

test('one site path among other paths is site=true', () => {
  assert.equal(siteChanged(['internal/tools/plan.go', 'CHANGELOG.md', 'docs/skills/ship.md']), 'site=true');
});

const SCRIPT = fileURLToPath(new URL('../../scripts/site-changed.mjs', import.meta.url));
const runScript = (input) => execFileSync(process.execPath, [SCRIPT], { input, encoding: 'utf8' }).trim();

test('site-changed.mjs prints site=true for a site path on stdin', () => {
  assert.equal(runScript('internal/tools/plan.go\nsite/astro.config.mjs\n'), 'site=true');
});

test('site-changed.mjs prints site=false for no site path on stdin', () => {
  assert.equal(runScript('internal/tools/plan.go\nCHANGELOG.md\n'), 'site=false');
});

test('site-changed.mjs prints site=false for an empty diff', () => {
  assert.equal(runScript('\n'), 'site=false');
});
