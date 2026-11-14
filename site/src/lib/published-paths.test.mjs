import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { listPublished } from './published-paths.mjs';

// A fake file system: each key is an absolute directory, each value lists its entries.
function fakeFs(tree) {
  const dirent = (name, kind) => ({
    name,
    isDirectory: () => kind === 'dir',
    isFile: () => kind === 'file',
  });
  return {
    readdirSync(dir) {
      if (!(dir in tree)) throw new Error(`ENOENT ${dir}`);
      return tree[dir].map(([name, kind]) => dirent(name, kind));
    },
  };
}

describe('listPublished', () => {
  test('returns README.md, CHANGELOG.md and docs Markdown, sorted, without docs/internal', () => {
    const root = '/repo';
    const fake = fakeFs({
      '/repo': [
        ['README.md', 'file'],
        ['CHANGELOG.md', 'file'],
        ['AGENTS.md', 'file'],
        ['docs', 'dir'],
        ['plugins', 'dir'],
      ],
      '/repo/docs': [
        ['getting-started.md', 'file'],
        ['logo.png', 'file'],
        ['skills', 'dir'],
        ['internal', 'dir'],
      ],
      '/repo/docs/skills': [
        ['README.md', 'file'],
        ['commit.md', 'file'],
      ],
      '/repo/docs/internal': [['plan-architecture.md', 'file']],
    });
    assert.deepEqual(listPublished(root, fake), [
      'CHANGELOG.md',
      'README.md',
      'docs/getting-started.md',
      'docs/skills/README.md',
      'docs/skills/commit.md',
    ]);
  });

  test('skips a root file that is not there and a missing docs folder', () => {
    const fake = fakeFs({ '/repo': [['README.md', 'file']] });
    assert.deepEqual(listPublished('/repo', fake), ['README.md']);
  });

  test('walks nested docs folders', () => {
    const fake = fakeFs({
      '/repo': [['docs', 'dir']],
      '/repo/docs': [['guides', 'dir']],
      '/repo/docs/guides': [['deep', 'dir'], ['a.md', 'file']],
      '/repo/docs/guides/deep': [['b.md', 'file']],
    });
    assert.deepEqual(listPublished('/repo', fake), ['docs/guides/a.md', 'docs/guides/deep/b.md']);
  });

  test('works with the real node:fs on this repository', () => {
    const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
    const paths = listPublished(repoRoot, fs);
    assert.ok(paths.includes('README.md'));
    assert.ok(paths.includes('CHANGELOG.md'));
    assert.ok(paths.includes('docs/skills/README.md'));
    assert.ok(paths.every((p) => !p.startsWith('docs/internal/')));
  });
});
