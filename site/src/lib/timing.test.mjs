import test from 'node:test';
import assert from 'node:assert/strict';
import { stepShares, timesFor } from './timing.mjs';

const steps = [
  { name: 'execute', optional: false },
  { name: 'review', optional: false },
  { name: 'harden', optional: true },
  { name: 'pr', optional: false },
];

test('a step with no time is measured: false with 0 ms', () => {
  const { parts } = stepShares(steps, { execute: 100 }, { includeOptional: true });
  const review = parts.find((p) => p.name === 'review');
  assert.deepEqual(review, { name: 'review', ms: 0, share: 0, measured: false });
  assert.equal(parts.find((p) => p.name === 'execute').measured, true);
});

test('with no measured step, totalMs and each share are 0', () => {
  const { totalMs, parts } = stepShares(steps, {}, { includeOptional: true });
  assert.equal(totalMs, 0);
  assert.deepEqual(parts.map((p) => p.share), [0, 0, 0, 0]);
});

test('with measured steps, the shares add up to 1', () => {
  const { totalMs, parts } = stepShares(steps, { execute: 300, review: 100, harden: 50, pr: 50 }, { includeOptional: true });
  assert.equal(totalMs, 500);
  assert.equal(parts.find((p) => p.name === 'execute').share, 0.6);
  assert.ok(Math.abs(parts.reduce((s, p) => s + p.share, 0) - 1) < 1e-12);
});

test('includeOptional: false drops optional steps from the parts and the total', () => {
  const { totalMs, parts } = stepShares(steps, { execute: 300, review: 100, harden: 600, pr: 100 }, { includeOptional: false });
  assert.equal(totalMs, 500);
  assert.deepEqual(parts.map((p) => p.name), ['execute', 'review', 'pr']);
  assert.ok(Math.abs(parts.reduce((s, p) => s + p.share, 0) - 1) < 1e-12);
});

test('a name that is only on the prototype is not a time', () => {
  const { parts } = stepShares([{ name: 'constructor', optional: false }], {}, { includeOptional: true });
  assert.equal(parts[0].measured, false);
});

test('timesFor returns null for a skill with no entry', () => {
  assert.equal(timesFor({}, 'ship'), null);
  assert.equal(timesFor({ plan: { version: '1', runDate: 'd', times: {} } }, 'ship'), null);
  assert.equal(timesFor({}, 'constructor'), null);
});

test('timesFor returns the entry for a skill with one', () => {
  const entry = { version: '0.4.12', runDate: '2026-11-13', times: { execute: 1 } };
  assert.equal(timesFor({ ship: entry }, 'ship'), entry);
});
