import test from 'node:test';
import assert from 'node:assert/strict';
import { contrastRatio, checkPairs } from './contrast.mjs';

const round2 = (n) => Math.round(n * 100) / 100;

test('white on black is 21', () => {
  assert.equal(contrastRatio('#FFFFFF', '#000000'), 21);
});

test('same color on itself is 1', () => {
  assert.equal(contrastRatio('#E6E2DA', '#E6E2DA'), 1);
});

test('known pairs match to 2 decimals', () => {
  assert.equal(round2(contrastRatio('#5A5247', '#E6E2DA')), 5.95);
  assert.equal(round2(contrastRatio('#8F4A08', '#E6E2DA')), 5.16);
});

test('the ratio does not depend on the argument order of light and dark', () => {
  assert.equal(contrastRatio('#000000', '#FFFFFF'), 21);
});

test('3-digit hex expands', () => {
  assert.equal(contrastRatio('#fff', '#000'), 21);
});

test('rgba() is mixed over the background before the ratio', () => {
  // 50% black over white mixes to rgb(127.5, 127.5, 127.5).
  const mixed = contrastRatio('rgba(0, 0, 0, 0.5)', '#FFFFFF');
  assert.ok(Math.abs(mixed - contrastRatio('#808080', '#FFFFFF')) < 0.05);
  assert.ok(mixed < 21);
  assert.equal(contrastRatio('rgba(255, 255, 255, 1)', '#000000'), 21);
  assert.equal(contrastRatio('rgba(255, 255, 255, 0)', '#000000'), 1);
});

test('rgba() with a mix equals the same opaque color', () => {
  const bg = '#E6E2DA';
  // 40% of #5A5247 over #E6E2DA, each channel rounded to the nearest integer.
  const opaque = '#' + [[0x5a, 0xe6], [0x52, 0xe2], [0x47, 0xda]]
    .map(([f, b]) => Math.round(f * 0.4 + b * 0.6).toString(16).padStart(2, '0'))
    .join('');
  const viaAlpha = contrastRatio('rgba(90, 82, 71, 0.4)', bg);
  assert.ok(Math.abs(viaAlpha - contrastRatio(opaque, bg)) < 0.02);
});

test('invalid input throws', () => {
  assert.throws(() => contrastRatio('red', '#FFFFFF'));
  assert.throws(() => contrastRatio('#FFFFFF', 'rgba(0, 0, 0, 0.5)'));
  assert.throws(() => contrastRatio('rgba(0, 0, 0, 2)', '#FFFFFF'));
});

test('checkPairs returns each failing pair with label and ratio', () => {
  const failures = checkPairs([
    { fg: '#000000', bg: '#FFFFFF', min: 4.5, label: 'passes' },
    { fg: '#CCCCCC', bg: '#FFFFFF', min: 4.5, label: 'fails text' },
    { fg: '#DDDDDD', bg: '#FFFFFF', min: 3, label: 'fails control' },
  ]);
  assert.deepEqual(
    failures.map((f) => [f.label, f.min]),
    [
      ['fails text', 4.5],
      ['fails control', 3],
    ],
  );
  assert.equal(failures[0].ratio, contrastRatio('#CCCCCC', '#FFFFFF'));
  assert.equal(checkPairs([]).length, 0);
});

// Colors on white, found by search: ratios sit just either side of each limit.
test('boundary at min 4.5: 4.49 fails, 4.50 passes', () => {
  const below = { fg: '#ef0101', bg: '#FFFFFF', min: 4.5, label: 'below' };
  const above = { fg: '#ee0606', bg: '#FFFFFF', min: 4.5, label: 'above' };
  assert.equal(round2(contrastRatio(below.fg, below.bg)), 4.49);
  assert.equal(round2(contrastRatio(above.fg, above.bg)), 4.5);
  assert.deepEqual(checkPairs([below, above]).map((f) => f.label), ['below']);
});

test('boundary at min 3: 2.99 fails, 3.00 passes', () => {
  const below = { fg: '#ff5e5e', bg: '#FFFFFF', min: 3, label: 'below' };
  const above = { fg: '#f96363', bg: '#FFFFFF', min: 3, label: 'above' };
  assert.equal(round2(contrastRatio(below.fg, below.bg)), 2.99);
  assert.equal(round2(contrastRatio(above.fg, above.bg)), 3);
  assert.deepEqual(checkPairs([below, above]).map((f) => f.label), ['below']);
});
