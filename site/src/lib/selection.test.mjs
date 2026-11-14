import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { related, nodeFor, skillNodeNames } from './selection.mjs';
import { parseSteps, parseTableUnder } from './steps-table.mjs';

const shipSteps = ['execute', 'commit', 'review', 'harden', 'pr', 'learnings-commit'];
const handovers = [
  { From: 'preplan', To: 'plan' }, // 0
  { From: 'plan', To: 'ship' }, // 1
  { From: 'plan', To: 'execute' }, // 2
  { From: 'harden', To: 'plan' }, // 3
  { From: 'openspec-save', To: 'ship' }, // 4
  { From: 'ship', To: 'review' }, // 5
  { From: 'ship', To: 'harden' }, // 6
  { From: 'dashboard', To: 'plan' }, // 7
];
const skills = [
  { name: 'preplan', role: 'pipeline' },
  { name: 'plan', role: 'pipeline' },
  { name: 'openspec-save', role: 'around' },
  { name: 'review', role: 'pipeline' },
  { name: 'ship', role: 'pipeline' },
  { name: 'harden', role: 'pipeline' },
  { name: 'dashboard', role: 'around' },
];
const data = { shipSteps, handovers, skills };

// One test for each row of the `related` table of the plan (Task 22).
test('ship step: the step, the step before it and the step after it; no rows', () => {
  assert.deepEqual(related('step:review', data), {
    nodes: ['step:commit', 'step:review', 'step:harden'],
    rows: [],
  });
});

test('ship label: every ship step; rows whose From or To is ship', () => {
  assert.deepEqual(related('ship', data), {
    nodes: shipSteps.map((s) => `step:${s}`),
    rows: [1, 4, 5, 6],
  });
});

test('harden lane: the lane, the last ship step and skill:plan; rows of harden', () => {
  assert.deepEqual(related('lane:harden', data), {
    nodes: ['lane:harden', 'step:learnings-commit', 'skill:plan'],
    rows: [3, 6],
  });
});

test('pipeline skill: the skill and each skill in its handover rows', () => {
  assert.deepEqual(related('skill:plan', data), {
    nodes: ['skill:plan', 'skill:preplan', 'ship', 'step:execute', 'lane:harden', 'skill:dashboard'],
    rows: [0, 1, 2, 3, 7],
  });
});

test('around skill (the dashboard panel included): the skill only; its rows', () => {
  assert.deepEqual(related('skill:dashboard', data), { nodes: ['skill:dashboard'], rows: [7] });
  assert.deepEqual(related('skill:openspec-save', data), { nodes: ['skill:openspec-save'], rows: [4] });
});

test('unknown id, or an id with no known prefix: no nodes and no rows', () => {
  assert.deepEqual(related('x', data), { nodes: [], rows: [] });
  assert.deepEqual(related('review', data), { nodes: [], rows: [] });
  assert.deepEqual(related('lane:other', data), { nodes: [], rows: [] });
  assert.deepEqual(related(undefined, data), { nodes: [], rows: [] });
});

test('clash: step:harden and lane:harden return different nodes', () => {
  assert.notDeepEqual(related('step:harden', data).nodes, related('lane:harden', data).nodes);
  assert.deepEqual(related('step:harden', data).nodes, ['step:review', 'step:harden', 'step:pr']);
});

// One test for each row of the `related` edge-case table.
test('edge: step:execute (the first ship step) has no step before it', () => {
  assert.deepEqual(related('step:execute', data).nodes, ['step:execute', 'step:commit']);
});

test('edge: step:learnings-commit (the last ship step) has no step after it', () => {
  assert.deepEqual(related('step:learnings-commit', data).nodes, ['step:pr', 'step:learnings-commit']);
});

test('edge: step:nope, a known prefix with an unknown name', () => {
  assert.deepEqual(related('step:nope', data), { nodes: [], rows: [] });
});

test('edge: skill:nope, a known prefix with an unknown name', () => {
  assert.deepEqual(related('skill:nope', data), { nodes: [], rows: [] });
});

test('edge: lane:harden with no ship steps gives the lane and skill:plan only', () => {
  assert.deepEqual(related('lane:harden', { ...data, shipSteps: [] }).nodes, ['lane:harden', 'skill:plan']);
});

// The skill-to-node rule of the plan.
test('nodeFor maps harden, ship, a ship step and any other skill', () => {
  assert.equal(nodeFor('harden', shipSteps), 'lane:harden');
  assert.equal(nodeFor('ship', shipSteps), 'ship');
  assert.equal(nodeFor('execute', shipSteps), 'step:execute');
  assert.equal(nodeFor('openspec-save', shipSteps), 'skill:openspec-save');
});

test('a skill that is a ship step has no skill node', () => {
  assert.deepEqual(related('skill:review', data), { nodes: [], rows: [] });
  assert.deepEqual(skillNodeNames(data), ['preplan', 'plan', 'openspec-save', 'dashboard']);
});

test('shipSteps can be Step objects of parseSteps', () => {
  const objects = shipSteps.map((name) => ({ name }));
  assert.deepEqual(related('step:review', { ...data, shipSteps: objects }), related('step:review', data));
});

// The real tables: each id that `related` returns exists on the map.
test('the repository tables give only map ids that exist', () => {
  const here = path.dirname(fileURLToPath(import.meta.url));
  const docs = path.join(here, '../../../docs/skills');
  const steps = parseSteps(fs.readFileSync(path.join(docs, 'ship.md'), 'utf8')).steps.map((s) => s.name);
  const readme = fs.readFileSync(path.join(docs, 'README.md'), 'utf8');
  const realSkills = parseTableUnder(readme, '## Skill map').map((r) => ({ name: r.Skill, role: r.Role }));
  const realHandovers = parseTableUnder(readme, '## Handovers');
  const real = { shipSteps: steps, handovers: realHandovers, skills: realSkills };
  const ids = new Set([
    'ship',
    'lane:harden',
    ...steps.map((s) => `step:${s}`),
    ...skillNodeNames(real).map((s) => `skill:${s}`),
  ]);
  for (const id of ids) {
    const { nodes, rows } = related(id, real);
    for (const node of nodes) assert.ok(ids.has(node), `${id} returns ${node}, which is not on the map`);
    for (const row of rows) assert.ok(row >= 0 && row < realHandovers.length);
  }
  assert.equal(related('ship', real).nodes.length, steps.length);
});
