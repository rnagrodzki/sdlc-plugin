import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseSteps } from './steps-table.mjs';

// Enforces docs/internal/skill-page-style.md, sections "Page shape" and "Reference pages":
// the heading order, the section limits and the ## Steps table of each overview page.

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const skillsDir = path.join(repoRoot, 'docs/skills');

const MAX_LINES = 150;
const MAX_LEAD_WORDS = 25;
const MAX_EXAMPLE_LINES = 15;

// Order of the H2 headings of an overview page.
const H2_ORDER = ['Why use it', 'Example', 'What you get', 'Steps', 'More examples', 'Reference', 'Related skills'];
const H2_REQUIRED = ['Why use it', 'Example', 'What you get', 'Steps', 'Related skills'];

// Extra H2 headings that a page keeps on purpose. Each one has a reason.
// An extra H2 sits after "Steps" and before "Related skills" unless the page order says otherwise.
const EXTRA_H2 = {
  // Go test pin: internal/skillcheck/skillcheck_preplan_test.go reads the table under "## Flags".
  preplan: ['Flags'],
  // Pinned inbound link: docs/skills/harden/custom-instructions.md links to setup.md#config-templates.
  setup: ['Config templates'],
};

/** Split a page into lines with a flag for lines inside a fenced code block. */
function scan(markdown) {
  const lines = markdown.split('\n');
  let fence = null;
  return lines.map((text, index) => {
    const m = /^\s*(`{3,}|~{3,})/.exec(text);
    let inFence = fence !== null;
    if (m) {
      if (fence === null) {
        fence = m[1][0];
        inFence = true;
      } else if (m[1][0] === fence) {
        fence = null;
      }
    }
    const h = !inFence && !m ? /^(#{1,6})\s+(.*?)\s*#*\s*$/.exec(text) : null;
    return { text, index, inFence, heading: h ? { level: h[1].length, title: h[2] } : null };
  });
}

/**
 * Check the shape of one overview page.
 * @param {string} name skill name, for example "plan"
 * @param {string} markdown page text
 * @param {{ referenceFiles?: string[], extraH2?: string[] }} [opts] referenceFiles: file names in docs/skills/<name>/
 * @returns {string[]} problems, empty when the page is correct
 */
export function checkOverviewPage(name, markdown, opts = {}) {
  const { referenceFiles = [], extraH2 = [] } = opts;
  const problems = [];
  const lines = scan(markdown);

  // 3. Length.
  const total = markdown.replace(/\n$/, '').split('\n').length;
  if (total > MAX_LINES) problems.push(`page has ${total} lines, limit is ${MAX_LINES}`);

  // 1. H1.
  const h1s = lines.filter((l) => l.heading?.level === 1);
  const wantH1 = `/sdlc:${name}`;
  if (h1s.length !== 1) {
    problems.push(`page has ${h1s.length} H1 headings, expected exactly 1`);
  } else if (h1s[0].heading.title !== wantH1) {
    problems.push(`H1 is "# ${h1s[0].heading.title}", expected "# ${wantH1}"`);
  }

  // 2. H2 order.
  const h2s = lines.filter((l) => l.heading?.level === 2);
  const titles = h2s.map((l) => l.heading.title);
  for (const req of H2_REQUIRED) {
    if (!titles.includes(req)) problems.push(`required H2 "## ${req}" is missing`);
  }
  const unknown = titles.filter((t) => !H2_ORDER.includes(t) && !extraH2.includes(t));
  for (const t of unknown) problems.push(`unexpected H2 "## ${t}" (allow it in EXTRA_H2 only for a Go test pin or an inbound link)`);
  const known = titles.filter((t) => H2_ORDER.includes(t));
  const sorted = [...known].sort((a, b) => H2_ORDER.indexOf(a) - H2_ORDER.indexOf(b));
  if (known.join('|') !== sorted.join('|')) {
    problems.push(`H2 order is [${known.join(', ')}], expected [${sorted.join(', ')}]`);
  }
  const dups = known.filter((t, i) => known.indexOf(t) !== i);
  for (const t of new Set(dups)) problems.push(`H2 "## ${t}" appears more than one time`);

  // "### Run facts" follows "## Steps".
  const stepsAt = h2s.find((l) => l.heading.title === 'Steps');
  if (stepsAt) {
    const nextH2 = h2s.find((l) => l.index > stepsAt.index);
    const end = nextH2 ? nextH2.index : lines.length;
    const inSteps = lines.filter((l) => l.index > stepsAt.index && l.index < end && l.heading?.level === 3);
    if (!inSteps.some((l) => l.heading.title === 'Run facts')) {
      problems.push('"### Run facts" is not under "## Steps"');
    }
  }

  // 4. Lead sentence.
  if (h1s.length === 1) {
    const from = h1s[0].index + 1;
    const to = h2s.length ? h2s[0].index : lines.length;
    const lead = lines.slice(from, to).map((l) => l.text).join(' ').replace(/\s+/g, ' ').trim();
    const words = lead ? lead.split(' ').length : 0;
    if (words === 0) problems.push('no lead sentence between the H1 and the first H2');
    else if (words > MAX_LEAD_WORDS) problems.push(`lead has ${words} words, limit is ${MAX_LEAD_WORDS}`);
  }

  // 5. Example section.
  const exampleAt = h2s.find((l) => l.heading.title === 'Example');
  if (exampleAt) {
    const nextH2 = h2s.find((l) => l.index > exampleAt.index);
    const body = lines.slice(exampleAt.index + 1, nextH2 ? nextH2.index : lines.length);
    while (body.length && body[0].text.trim() === '') body.shift();
    while (body.length && body[body.length - 1].text.trim() === '') body.pop();
    if (body.length > MAX_EXAMPLE_LINES) {
      problems.push(`## Example has ${body.length} lines, limit is ${MAX_EXAMPLE_LINES}`);
    }
    const opens = body.filter((l) => /^\s*(`{3,}|~{3,})/.test(l.text)).length;
    if (Math.ceil(opens / 2) > 1) problems.push(`## Example has ${Math.ceil(opens / 2)} fenced code blocks, limit is 1`);
  }

  // 6. Steps and Run facts tables.
  try {
    parseSteps(markdown);
  } catch (err) {
    problems.push(`Steps table: ${err.message}`);
  }

  // 7. Every reference page is linked from the overview page.
  for (const file of referenceFiles) {
    const stem = file.replace(/\.md$/, '');
    const linked = new RegExp(`\\]\\([^)]*\\b${name}/${stem.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(\\.md)?(#[^)]*)?\\)`, 'i').test(markdown)
      || new RegExp(`\\]\\(\\.?/?${stem.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(\\.md)?(#[^)]*)?\\)`, 'i').test(markdown);
    if (!linked) problems.push(`reference page ${name}/${file} is not linked from the overview page`);
  }

  return problems;
}

/** Check one reference page: it has an H1 and is not empty. */
export function checkReferencePage(markdown) {
  const problems = [];
  if (markdown.trim() === '') return ['file is empty'];
  if (!scan(markdown).some((l) => l.heading?.level === 1)) problems.push('no H1 heading');
  return problems;
}

const overviews = fs
  .readdirSync(skillsDir)
  .filter((f) => f.endsWith('.md') && f !== 'README.md')
  .sort();

describe('skill page shape: overview pages', () => {
  test('docs/skills/ holds the overview pages', () => {
    assert.ok(overviews.length > 0, 'no overview page found');
  });

  for (const file of overviews) {
    const name = file.replace(/\.md$/, '');
    test(`docs/skills/${file}`, () => {
      const dir = path.join(skillsDir, name);
      const referenceFiles = fs.existsSync(dir) ? fs.readdirSync(dir).filter((f) => f.endsWith('.md')).sort() : [];
      const markdown = fs.readFileSync(path.join(skillsDir, file), 'utf8');
      const problems = checkOverviewPage(name, markdown, { referenceFiles, extraH2: EXTRA_H2[name] ?? [] });
      assert.deepEqual(problems, [], `docs/skills/${file}:\n  - ${problems.join('\n  - ')}`);
    });
  }
});

describe('skill page shape: reference pages', () => {
  for (const file of overviews) {
    const name = file.replace(/\.md$/, '');
    const dir = path.join(skillsDir, name);
    if (!fs.existsSync(dir)) continue;
    for (const ref of fs.readdirSync(dir).filter((f) => f.endsWith('.md')).sort()) {
      test(`docs/skills/${name}/${ref}`, () => {
        const problems = checkReferencePage(fs.readFileSync(path.join(dir, ref), 'utf8'));
        assert.deepEqual(problems, [], `docs/skills/${name}/${ref}:\n  - ${problems.join('\n  - ')}`);
      });
    }
  }
});

describe('skill page shape: checker reports bad pages', () => {
  const steps = [
    '| Step | Actor | What it does | Hands over | Optional | When |',
    '|---|---|---|---|---|---|',
    '| One | tool | Does a thing | Result | no | Always |',
    '',
    '### Run facts',
    '',
    '| Fact | Value |',
    '|---|---|',
    '| Starts | You run it |',
    '| Ends | It stops |',
    '| Asks the user | No |',
    '| Resume | No |',
  ].join('\n');
  const good = [
    '# /sdlc:demo',
    '',
    'Run a demo and get a result.',
    '',
    '## Why use it',
    '',
    'Because.',
    '',
    '## Example',
    '',
    '```',
    '/sdlc:demo',
    '```',
    '',
    '## What you get',
    '',
    '- A result',
    '',
    '## Steps',
    '',
    steps,
    '',
    '## Related skills',
    '',
    '- `/sdlc:plan`',
    '',
  ].join('\n');

  test('a correct synthetic page has no problem', () => {
    assert.deepEqual(checkOverviewPage('demo', good), []);
  });

  test('wrong heading order', () => {
    const bad = good.replace('## Why use it', '## @@A').replace('## Example', '## Why use it').replace('## @@A', '## Example');
    const problems = checkOverviewPage('demo', bad);
    assert.ok(problems.some((p) => p.startsWith('H2 order')), problems.join('\n'));
  });

  test('over 150 lines', () => {
    const bad = good.replace('Because.', Array(160).fill('Because.').join('\n\n'));
    const problems = checkOverviewPage('demo', bad);
    assert.ok(problems.some((p) => /lines, limit is 150/.test(p)), problems.join('\n'));
  });

  test('bad Steps table', () => {
    const bad = good.replace('| One | tool |', '| One | robot |');
    const problems = checkOverviewPage('demo', bad);
    assert.ok(problems.some((p) => p.startsWith('Steps table:')), problems.join('\n'));
  });
});
