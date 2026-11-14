import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { benefits } from '../data/benefits.mjs';
import { customizations, customizeNote, followUps } from '../data/customize.mjs';
import { features } from '../data/features.mjs';
import { pillars } from '../data/pillars.mjs';
import { learnLoop, workflow } from '../data/workflow.mjs';
import { listPublished } from './published-paths.mjs';
import { ROLES, readSkillMap, skillHref, skillMap } from './skill-map.mjs';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const skills = readSkillMap(path.join(repoRoot, 'docs/skills'));
const skillNames = skills.map((s) => s.name);
const published = new Set(listPublished(repoRoot, fs));

/** The repository path of a `doc` value, with no #anchor. */
const docPath = (doc) => doc.split('#')[0];

test('the skill map of the docs has skills of each role', () => {
  for (const role of ROLES) {
    assert.ok(skills.some((s) => s.role === role), `no skill with the role ${role}`);
  }
  assert.equal(new Set(skillNames).size, skillNames.length, 'a skill is in the map twice');
});

test('skillMap stops on a README with no Skill map table', () => {
  assert.throws(() => skillMap('## Skill map\n\nNo table here.\n'), /no table under "## Skill map"/);
  assert.throws(() => skillMap('# Skills\n\nNo heading here.\n'), /heading "## Skill map" not found/);
});

test('skillMap stops on a role the site does not show', () => {
  const readme = '## Skill map\n\n| Skill | Role | Summary |\n|---|---|---|\n| plan | helper | Plans. |\n';
  assert.throws(() => skillMap(readme), /skill "plan" has the role "helper"/);
});

test('skillHref gives the route of a skill page', () => {
  assert.equal(skillHref('plan', '/sdlc-plugin'), '/sdlc-plugin/skills/plan/');
  assert.equal(skillHref('plan', '/sdlc-plugin/'), '/sdlc-plugin/skills/plan/');
});

test('each benefit has a title, a text and 1 or more skills of the skill map', () => {
  for (const benefit of benefits) {
    assert.ok(benefit.title && benefit.text, `a benefit has no title or no text: ${JSON.stringify(benefit)}`);
    assert.ok(benefit.skills.length > 0, `"${benefit.title}" names no skill`);
    for (const name of benefit.skills) {
      assert.ok(skillNames.includes(name), `"${benefit.title}" names the skill "${name}", which is not in the skill map`);
    }
  }
});

test('each pillar has points and 1 or more skills of the skill map', () => {
  for (const pillar of pillars) {
    assert.ok(pillar.id && pillar.kicker && pillar.title && pillar.lead, `a pillar has no id, no kicker, no title or no lead: ${JSON.stringify(pillar)}`);
    assert.ok(pillar.points.length > 0, `"${pillar.title}" has no point`);
    assert.ok(pillar.skills.length > 0, `"${pillar.title}" names no skill`);
    for (const name of pillar.skills) {
      assert.ok(skillNames.includes(name), `"${pillar.title}" names the skill "${name}", which is not in the skill map`);
    }
  }
  assert.equal(new Set(pillars.map((p) => p.id)).size, pillars.length, 'two pillars have the same id');
});

test('each feature slug of a pillar is an entry of features.mjs', () => {
  const slugs = features.map((f) => f.slug);
  for (const pillar of pillars) {
    if (pillar.feature) {
      assert.ok(slugs.includes(pillar.feature), `"${pillar.title}" names the feature "${pillar.feature}"`);
    }
  }
});

test('the follow-up block has points, samples, skills of the skill map and a published doc', () => {
  assert.ok(followUps.kicker && followUps.title && followUps.lead && followUps.docLabel, 'the follow-up block has no kicker, no title, no lead or no doc label');
  assert.ok(followUps.points.length > 0, 'the follow-up block has no point');
  assert.ok(followUps.samples.length > 0, 'the follow-up block has no sample');
  for (const sample of followUps.samples) {
    assert.ok(sample.file && sample.who, `a sample has no file or no "who": ${JSON.stringify(sample)}`);
    assert.match(sample.code, /^\[\[instructions\.[a-z-]+\]\]$/m, `the sample ${sample.file} has no [[instructions.<key>]] header`);
  }
  assert.ok(followUps.skills.length > 0, 'the follow-up block names no skill');
  for (const name of followUps.skills) {
    assert.ok(skillNames.includes(name), `the follow-up block names the skill "${name}", which is not in the skill map`);
  }
  assert.ok(published.has(docPath(followUps.doc)), `the follow-up block names the doc "${followUps.doc}", which is not a published file`);
  assert.ok(
    features.some((f) => f.slug === followUps.feature),
    `the follow-up block names the feature "${followUps.feature}"`,
  );
});

test('each [[instructions.<key>]] header of a sample is a key of docs/instructions.md', () => {
  const reference = fs.readFileSync(path.join(repoRoot, 'docs/instructions.md'), 'utf8');
  const keysTable = reference.slice(reference.indexOf('\n## Keys\n'), reference.indexOf('\n## Entry fields\n'));
  const keys = [...keysTable.matchAll(/^\| `([a-z-]+)` \|/gm)].map((m) => m[1]);
  assert.equal(keys.length, 13, 'the Keys table of docs/instructions.md has 13 keys');
  for (const sample of followUps.samples) {
    for (const [, key] of sample.code.matchAll(/^\[\[instructions\.([a-z-]+)\]\]$/gm)) {
      assert.ok(keys.includes(key), `the sample ${sample.file} uses the key "${key}", which is not in the Keys table`);
    }
  }
});

test('each customization has a title, a text, skills of the skill map and published docs', () => {
  assert.ok(customizations.length > 0, 'the customization section has no entry');
  for (const item of customizations) {
    assert.ok(item.title && item.text, `a customization has no title or no text: ${JSON.stringify(item)}`);
    assert.ok(item.skills.length > 0, `"${item.title}" names no skill`);
    for (const name of item.skills) {
      assert.ok(skillNames.includes(name), `"${item.title}" names the skill "${name}", which is not in the skill map`);
    }
    assert.ok(item.docs.length > 0, `"${item.title}" names no doc`);
    for (const link of item.docs) {
      assert.ok(link.label, `"${item.title}" has a doc with no label`);
      assert.ok(published.has(docPath(link.doc)), `"${item.title}" names the doc "${link.doc}", which is not a published file`);
    }
  }
  assert.equal(new Set(customizations.map((c) => c.title)).size, customizations.length, 'two customizations have the same title');
});

test('the customization note has a text and published docs', () => {
  assert.ok(customizeNote.text, 'the customization note has no text');
  assert.ok(customizeNote.docs.length > 0, 'the customization note names no doc');
  for (const link of customizeNote.docs) {
    assert.ok(link.label, 'the customization note has a doc with no label');
    assert.ok(published.has(docPath(link.doc)), `the customization note names the doc "${link.doc}", which is not a published file`);
  }
});

test('each workflow step is a skill of the skill map, with a command of that skill', () => {
  assert.ok(workflow.length > 0, 'the workflow has no step');
  for (const step of workflow) {
    assert.ok(skillNames.includes(step.skill), `the workflow names the skill "${step.skill}", which is not in the skill map`);
    assert.ok(step.command.startsWith(`/sdlc:${step.skill}`), `the command "${step.command}" is not a command of ${step.skill}`);
    assert.ok(step.title && step.does && step.result, `the step ${step.skill} has no title, no "does" or no "result"`);
    for (const name of step.runs ?? []) {
      assert.ok(skillNames.includes(name), `the step ${step.skill} runs the skill "${name}", which is not in the skill map`);
    }
  }
  assert.equal(new Set(workflow.map((s) => s.skill)).size, workflow.length, 'a skill is in the workflow twice');
});

test('the learn loop names a skill of the skill map and returns to a workflow step', () => {
  assert.ok(skillNames.includes(learnLoop.skill), `the learn loop names the skill "${learnLoop.skill}"`);
  assert.ok(learnLoop.text, 'the learn loop has no text');
  assert.ok(
    workflow.some((step) => step.skill === learnLoop.to),
    `the learn loop returns to "${learnLoop.to}", which is not a workflow step`,
  );
});
