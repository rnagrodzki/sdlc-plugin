import test, { describe } from 'node:test';
import assert from 'node:assert/strict';
import { parseSteps, parseTableUnder, remarkStripSteps, StepsTableError } from './steps-table.mjs';

const HEAD = '| Step | Actor | What it does | Hands over | Optional | When |\n|---|---|---|---|---|---|\n';
const FACTS = [
  '### Run facts',
  '',
  '| Fact | Value |',
  '|---|---|',
  '| Starts | The user runs `/sdlc:execute`. |',
  '| Ends | The report is shown. |',
  '| Asks the user | When a task fails. |',
  '| Resume | `--resume` |',
].join('\n');

const SAMPLE = [
  '# execute',
  '',
  '## Flags',
  '',
  '| Flag | Meaning |',
  '|---|---|',
  '| `--resume` | Resume. |',
  '',
  '## Steps',
  '',
  HEAD +
    '| execute | agent | Runs the plan wave by wave. | Changed files | no | always |\n' +
    '| harden | skill | Proposes guardrail edits. | Edits | yes | in the steps list |',
  '',
  FACTS,
  '',
  '## Notes',
  '',
  'Text.',
  '',
].join('\n');

function stepsDoc({ rows = '', facts = FACTS } = {}) {
  return `## Steps\n\n${HEAD}${rows}\n\n${facts}\n`;
}

const ROW = '| execute | agent | Runs. | Files | no | always |\n';

function codeOf(fn) {
  try {
    fn();
  } catch (err) {
    assert.ok(err instanceof StepsTableError, `expected StepsTableError, got ${err}`);
    return err.code;
  }
  assert.fail('expected a throw');
}

describe('parseSteps', () => {
  test('returns steps and facts for the sample', () => {
    const { steps, facts } = parseSteps(SAMPLE);
    assert.deepEqual(steps, [
      {
        name: 'execute',
        actor: 'agent',
        does: 'Runs the plan wave by wave.',
        handsOver: 'Changed files',
        optional: false,
        when: 'always',
      },
      {
        name: 'harden',
        actor: 'skill',
        does: 'Proposes guardrail edits.',
        handsOver: 'Edits',
        optional: true,
        when: 'in the steps list',
      },
    ]);
    assert.deepEqual(facts, {
      starts: 'The user runs `/sdlc:execute`.',
      ends: 'The report is shown.',
      asks: 'When a task fails.',
      resume: '`--resume`',
    });
  });

  test('error no-section when ## Steps is absent', () => {
    assert.equal(codeOf(() => parseSteps('# Title\n\n## Flags\n')), 'no-section');
  });

  test('error bad-header when a column is wrong', () => {
    const md = stepsDoc({ rows: ROW }).replace('| Hands over |', '| Gives |');
    assert.equal(codeOf(() => parseSteps(md)), 'bad-header');
  });

  test('error bad-header when a row has the wrong cell count', () => {
    assert.equal(codeOf(() => parseSteps(stepsDoc({ rows: '| execute | agent | Runs. |\n' }))), 'bad-header');
  });

  test('error bad-actor for an actor outside the 4 values', () => {
    const md = stepsDoc({ rows: ROW.replace('| agent |', '| robot |') });
    assert.equal(codeOf(() => parseSteps(md)), 'bad-actor');
  });

  test('error bad-optional for a value other than yes or no', () => {
    const md = stepsDoc({ rows: ROW.replace('| no |', '| maybe |') });
    assert.equal(codeOf(() => parseSteps(md)), 'bad-optional');
  });

  test('error no-facts when ### Run facts is absent', () => {
    const md = `## Steps\n\n${HEAD}${ROW}`;
    assert.equal(codeOf(() => parseSteps(md)), 'no-facts');
  });

  test('error no-facts when one of the 4 labels is absent', () => {
    const facts = FACTS.replace('| Resume | `--resume` |', '');
    assert.equal(codeOf(() => parseSteps(stepsDoc({ rows: ROW, facts }))), 'no-facts');
  });

  test('error duplicate-key for two steps with one name', () => {
    assert.equal(codeOf(() => parseSteps(stepsDoc({ rows: ROW + ROW }))), 'duplicate-key');
  });

  test('error duplicate-key for two Run facts rows with one label', () => {
    const facts = FACTS + '\n| Starts | Again. |';
    assert.equal(codeOf(() => parseSteps(stepsDoc({ rows: ROW, facts }))), 'duplicate-key');
  });

  test('a Run facts label outside the 4 labels is ignored', () => {
    const facts = FACTS + '\n| Extra | Ignored. |';
    const { facts: got } = parseSteps(stepsDoc({ rows: ROW, facts }));
    assert.deepEqual(Object.keys(got).sort(), ['asks', 'ends', 'resume', 'starts']);
  });

  test('header and separator only gives steps: []', () => {
    const { steps } = parseSteps(stepsDoc());
    assert.deepEqual(steps, []);
  });

  test('Run facts table on the last line with no final newline', () => {
    const md = stepsDoc({ rows: ROW }).trimEnd();
    assert.equal(md.endsWith('`--resume` |'), true);
    assert.equal(parseSteps(md).facts.resume, '`--resume`');
  });

  test('a ## Steps heading inside a code fence is not a section', () => {
    const md = '```md\n## Steps\n```\n';
    assert.equal(codeOf(() => parseSteps(md)), 'no-section');
  });

  test('an escaped pipe stays inside a cell', () => {
    const md = stepsDoc({ rows: '| execute | agent | a \\| b | Files | no | always |\n' });
    assert.equal(parseSteps(md).steps[0].does, 'a | b');
  });
});

describe('parseTableUnder', () => {
  test('throws no-section when the heading is absent', () => {
    assert.equal(codeOf(() => parseTableUnder('# A\n', '## Missing')), 'no-section');
  });

  test('returns the rows when the table is on the last line with no final newline', () => {
    const md = '## Flags\n\n| Flag | Meaning |\n|---|---|\n| a | b |';
    assert.deepEqual(parseTableUnder(md, '## Flags'), [{ Flag: 'a', Meaning: 'b' }]);
  });

  test('returns [] for a header and a separator with no body row', () => {
    const md = '## Flags\n\n| Flag | Meaning |\n|---|---|\n';
    assert.deepEqual(parseTableUnder(md, '## Flags'), []);
  });

  test('uses the first of two equal headings', () => {
    const md = '## T\n\n| A |\n|---|\n| first |\n\n## T\n\n| A |\n|---|\n| second |\n';
    assert.deepEqual(parseTableUnder(md, '## T'), [{ A: 'first' }]);
  });

  test('stops at the next heading and returns [] when no table comes first', () => {
    const md = '## A\n\ntext\n\n## B\n\n| X |\n|---|\n| 1 |\n';
    assert.deepEqual(parseTableUnder(md, '## A'), []);
  });

  test('matches the heading level', () => {
    const md = '### T\n\n| A |\n|---|\n| 1 |\n';
    assert.equal(codeOf(() => parseTableUnder(md, '## T')), 'no-section');
  });
});

describe('remarkStripSteps', () => {
  const h = (depth, text) => ({ type: 'heading', depth, children: [{ type: 'text', value: text }] });
  const p = (text) => ({ type: 'paragraph', children: [{ type: 'text', value: text }] });
  const table = { type: 'table', children: [] };
  const run = (children, path) => {
    const tree = { type: 'root', children };
    remarkStripSteps()(tree, { path });
    return tree.children;
  };

  test('removes ## Steps with its ### Run facts table and keeps the rest', () => {
    const out = run(
      [h(2, 'Flags'), table, h(2, 'Steps'), table, h(3, 'Run facts'), table, h(2, 'Notes'), p('x')],
      '/repo/docs/skills/ship.md',
    );
    assert.deepEqual(out, [h(2, 'Flags'), table, h(2, 'Notes'), p('x')]);
  });

  test('removes the section when it is the last in the file', () => {
    const out = run([h(2, 'Flags'), table, h(2, 'Steps'), table, h(3, 'Run facts'), table], '/repo/docs/skills/ship.md');
    assert.deepEqual(out, [h(2, 'Flags'), table]);
  });

  test('changes nothing in a page with no ## Steps', () => {
    const children = [h(2, 'Flags'), table];
    assert.deepEqual(run(children.slice(), '/repo/docs/skills/ship.md'), children);
  });

  test('changes nothing in a file outside docs/skills/', () => {
    const children = [h(2, 'Steps'), table, h(3, 'Run facts'), table];
    assert.deepEqual(run(children.slice(), '/repo/docs/getting-started.md'), children);
  });
});
