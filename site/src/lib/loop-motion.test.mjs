import test from 'node:test';
import assert from 'node:assert/strict';
import { loopMotion } from './loop-motion.mjs';

// One case for each row of the loopMotion table of the plan (Task 22).
// "any" rows are expanded to each value.
const cases = [
  // reducedMotion true, tabHidden any, onScreen any -> stop
  [{ reducedMotion: true, tabHidden: false, onScreen: false }, 'stop'],
  [{ reducedMotion: true, tabHidden: false, onScreen: true }, 'stop'],
  [{ reducedMotion: true, tabHidden: true, onScreen: false }, 'stop'],
  [{ reducedMotion: true, tabHidden: true, onScreen: true }, 'stop'],
  // reducedMotion false, tabHidden true, onScreen any -> pause
  [{ reducedMotion: false, tabHidden: true, onScreen: false }, 'pause'],
  [{ reducedMotion: false, tabHidden: true, onScreen: true }, 'pause'],
  // reducedMotion false, tabHidden false, onScreen false -> pause
  [{ reducedMotion: false, tabHidden: false, onScreen: false }, 'pause'],
  // reducedMotion false, tabHidden false, onScreen true -> run
  [{ reducedMotion: false, tabHidden: false, onScreen: true }, 'run'],
];

for (const [env, want] of cases) {
  test(`loopMotion ${JSON.stringify(env)} -> ${want}`, () => {
    assert.equal(loopMotion(env), want);
  });
}
