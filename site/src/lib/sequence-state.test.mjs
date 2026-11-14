import test from 'node:test';
import assert from 'node:assert/strict';
import {
  nextState,
  visibleSteps,
  formatDuration,
  shareText,
  actorLabel,
  inlineParts,
} from './sequence-state.mjs';

const steps = [
  { name: 'execute', optional: false },
  { name: 'harden', optional: true },
  { name: 'review', optional: false },
  { name: 'verify-pipeline', optional: true },
];
const allOptional = [
  { name: 'harden', optional: true },
  { name: 'verify-pipeline', optional: true },
];

/** @returns {import('./sequence-state.mjs').SeqState} */
const base = (over = {}) => ({ selected: 'execute', showOptional: true, replaying: false, reducedMotion: false, ...over });

// One test for each row of the nextState table of the Task 21 contract.

test('select: a visible step becomes the selected step', () => {
  assert.deepEqual(nextState(base(), { type: 'select', name: 'harden' }, steps), base({ selected: 'harden' }));
});

test('select: an unknown step, or an optional step while optional steps are hidden, changes nothing', () => {
  const s = base();
  assert.equal(nextState(s, { type: 'select', name: 'nope' }, steps), s);
  const hidden = base({ showOptional: false });
  assert.equal(nextState(hidden, { type: 'select', name: 'harden' }, steps), hidden);
});

test('toggle-optional: hiding optional steps while an optional step is selected selects the first required step', () => {
  const s = base({ selected: 'verify-pipeline' });
  assert.deepEqual(nextState(s, { type: 'toggle-optional' }, steps), base({ selected: 'execute', showOptional: false }));
});

test('toggle-optional: any other state flips showOptional and keeps the selection', () => {
  assert.deepEqual(nextState(base({ selected: 'review' }), { type: 'toggle-optional' }, steps), base({ selected: 'review', showOptional: false }));
  assert.deepEqual(nextState(base({ showOptional: false }), { type: 'toggle-optional' }, steps), base({ showOptional: true }));
});

test('replay: with motion allowed it starts the replay at the first visible step', () => {
  assert.deepEqual(nextState(base({ selected: 'review' }), { type: 'replay' }, steps), base({ selected: 'execute', replaying: true }));
});

test('replay: with reduced motion it changes nothing', () => {
  const s = base({ selected: 'review', reducedMotion: true });
  assert.equal(nextState(s, { type: 'replay' }, steps), s);
});

test('replay-done: always ends the replay', () => {
  assert.deepEqual(nextState(base({ replaying: true }), { type: 'replay-done' }, steps), base());
  assert.deepEqual(nextState(base(), { type: 'replay-done' }, steps), base());
});

test('motion: reduced true sets reducedMotion and stops the replay', () => {
  assert.deepEqual(
    nextState(base({ replaying: true }), { type: 'motion', reduced: true }, steps),
    base({ reducedMotion: true, replaying: false }),
  );
});

test('motion: reduced false clears reducedMotion only', () => {
  assert.deepEqual(nextState(base({ reducedMotion: true }), { type: 'motion', reduced: false }, steps), base());
});

test('edge: an action that selects a first step selects nothing when no step matches', () => {
  assert.deepEqual(nextState(base({ selected: 'x' }), { type: 'replay' }, []), base({ selected: '', replaying: true }));
  const hidden = base({ selected: 'x', showOptional: false });
  assert.deepEqual(nextState(hidden, { type: 'replay' }, allOptional), { ...hidden, selected: '', replaying: true });
});

test('edge: toggle-optional with each step optional and showOptional true hides them all and selects nothing', () => {
  const s = base({ selected: 'harden' });
  assert.deepEqual(nextState(s, { type: 'toggle-optional' }, allOptional), base({ selected: '', showOptional: false }));
});

test('nextState does not change the input state, and an unknown action changes nothing', () => {
  const s = Object.freeze(base());
  nextState(s, { type: 'select', name: 'review' }, steps);
  nextState(s, { type: 'toggle-optional' }, steps);
  assert.equal(nextState(s, /** @type {any} */ ({ type: 'other' }), steps), s);
});

test('visibleSteps drops the optional steps only when they are hidden', () => {
  assert.deepEqual(visibleSteps(steps, true).map((s) => s.name), ['execute', 'harden', 'review', 'verify-pipeline']);
  assert.deepEqual(visibleSteps(steps, false).map((s) => s.name), ['execute', 'review']);
});

test('formatDuration matches the reference format', () => {
  assert.equal(formatDuration(8342000), '2h 19m 2s');
  assert.equal(formatDuration(556000), '9m 16s');
  assert.equal(formatDuration(3600000), '1h 0m 0s');
  assert.equal(formatDuration(2000), '2s');
  assert.equal(formatDuration(200), '<1s');
  assert.equal(formatDuration(0), '0s');
});

test('shareText gives the percent, the under-1% line and the no-time line', () => {
  assert.equal(shareText(25, 100), '25% of the run');
  assert.equal(shareText(1, 1000), 'Under 1% of the run');
  assert.equal(shareText(0, 1000), 'This step has no measured time');
  assert.equal(shareText(5, 0), 'This step has no measured time');
});

test('actorLabel maps each actor of the Steps table', () => {
  assert.deepEqual(['user', 'tool', 'agent', 'skill'].map(actorLabel), ['You', 'Tool', 'Agent', 'Skill']);
});

test('inlineParts splits code and strong text and keeps HTML as text', () => {
  assert.deepEqual(inlineParts('Run `/ship --plan <path>`. Choose **Skip release**.'), [
    { kind: 'text', value: 'Run ' },
    { kind: 'code', value: '/ship --plan <path>' },
    { kind: 'text', value: '. Choose ' },
    { kind: 'strong', value: 'Skip release' },
    { kind: 'text', value: '.' },
  ]);
  assert.deepEqual(inlineParts('plain <b>'), [{ kind: 'text', value: 'plain <b>' }]);
  assert.deepEqual(inlineParts(''), []);
});
