import test from 'node:test';
import assert from 'node:assert/strict';
import { docMeta, tocEntries } from './doc-meta.mjs';

test('docMeta reads the H1 without inline marks', () => {
  assert.equal(docMeta('# The `sdlc` **plugin**\n\nText.', 'x').title, 'The sdlc plugin');
});

test('docMeta uses the fallback title when the page has no H1', () => {
  const meta = docMeta('First block. Second sentence.\n\n## Part', 'docs/a.md');
  assert.equal(meta.title, 'docs/a.md');
  assert.equal(meta.lead, 'First block.');
});

test('docMeta takes the first sentence of the first paragraph as the lead', () => {
  const meta = docMeta('# Title\n\nInstall the plugin, then\nrun `setup`. A second sentence.\n\nNext paragraph.', 'x');
  assert.equal(meta.lead, 'Install the plugin, then run setup.');
  assert.equal(meta.leadIsIntro, false);
});

test('docMeta does not end the sentence at a dot inside a file name', () => {
  const meta = docMeta('# Title\n\nUse this instead of [`getting-started.md`](getting-started.md) for a clone. More.', 'x');
  assert.equal(meta.lead, 'Use this instead of getting-started.md for a clone.');
});

test('docMeta reads the lead from a block quote', () => {
  const meta = docMeta('# Dashboard\n\n> This document describes the dashboard: one web\n> page. It has more text.\n', 'x');
  assert.equal(meta.lead, 'This document describes the dashboard: one web page.');
});

test('docMeta marks a one-sentence block with no link as the complete intro', () => {
  assert.equal(docMeta('# Title\n\nThis guide explains the versions.\n\n## Part', 'x').leadIsIntro, true);
});

test('docMeta keeps a one-sentence block with a link in the page', () => {
  assert.equal(docMeta('# Title\n\nSee [the guide](guide.md) first.\n', 'x').leadIsIntro, false);
});

test('docMeta gives no lead when the first block is not text', () => {
  for (const block of ['## Part\n\nText.', '- item', '1. item', '| a | b |', '```sh\nls\n```']) {
    const meta = docMeta(`# Title\n\n${block}\n`, 'x');
    assert.equal(meta.lead, '', block);
    assert.equal(meta.leadIsIntro, false, block);
  }
});

test('docMeta keeps a block with no sentence end as the lead', () => {
  assert.equal(docMeta('# Title\n\nA line with no end mark\n', 'x').lead, 'A line with no end mark');
});

test('tocEntries keeps the h2 and h3 headings in page order', () => {
  const headings = [
    { depth: 1, slug: 'title', text: 'Title' },
    { depth: 2, slug: 'install', text: 'Install' },
    { depth: 3, slug: 'fetch', text: 'Fetch' },
    { depth: 4, slug: 'deep', text: 'Deep' },
    { depth: 2, slug: 'usage', text: 'Usage' },
  ];
  assert.deepEqual(
    tocEntries(headings).map((h) => h.slug),
    ['install', 'fetch', 'usage'],
  );
});

test('tocEntries with maxDepth 2 keeps only the h2 headings', () => {
  const headings = [
    { depth: 2, slug: 'v2', text: '2.0' },
    { depth: 3, slug: 'rc-1', text: 'RC 1' },
    { depth: 2, slug: 'v1', text: '1.0' },
  ];
  assert.deepEqual(
    tocEntries(headings, { maxDepth: 2 }).map((h) => h.slug),
    ['v2', 'v1'],
  );
});

test('tocEntries gives an empty list for a page with no h2', () => {
  assert.deepEqual(tocEntries([{ depth: 1, slug: 'title', text: 'Title' }]), []);
});
