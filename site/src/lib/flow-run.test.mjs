import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isRunDone, nextAt, partState } from './flow-run.mjs';

const PARTS = 5; // 4 workflow steps and the learn loop

const states = (at) => Array.from({ length: PARTS }, (_, i) => partState(i, at));

test('each part is idle before a run', () => {
  assert.deepEqual(states(-1), ['idle', 'idle', 'idle', 'idle', 'idle']);
});

test('one part runs, the parts before it are done and the parts after it are idle', () => {
  assert.deepEqual(states(0), ['run', 'idle', 'idle', 'idle', 'idle']);
  assert.deepEqual(states(2), ['done', 'done', 'run', 'idle', 'idle']);
  assert.deepEqual(states(4), ['done', 'done', 'done', 'done', 'run']);
});

test('each part is done at the end of the run', () => {
  assert.deepEqual(states(PARTS), ['done', 'done', 'done', 'done', 'done']);
});

test('nextAt moves one part and stops at the done state', () => {
  assert.equal(nextAt(-1, PARTS), 0);
  assert.equal(nextAt(3, PARTS), 4);
  assert.equal(nextAt(4, PARTS), PARTS);
  assert.equal(nextAt(PARTS, PARTS), PARTS);
});

test('isRunDone is true only when each part is done', () => {
  assert.equal(isRunDone(-1, PARTS), false);
  assert.equal(isRunDone(4, PARTS), false);
  assert.equal(isRunDone(PARTS, PARTS), true);
});
