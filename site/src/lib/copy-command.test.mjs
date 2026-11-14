import test from 'node:test';
import assert from 'node:assert/strict';
import { copyCommand } from './copy-command.mjs';

const TEXT = '/plugin marketplace add rnagrodzki/sdlc-plugin\n/plugin install sdlc@sdlc-plugin';

test('returns copied and writes the exact text when the clipboard accepts it', async () => {
  const written = [];
  const clipboard = { writeText: async (t) => { written.push(t); } };
  let selected = 0;
  const result = await copyCommand(TEXT, { clipboard, selectText: () => { selected += 1; } });
  assert.equal(result, 'copied');
  assert.deepEqual(written, [TEXT]);
  assert.equal(selected, 0);
});

test('returns selected and selects the text when there is no clipboard API', async () => {
  let selected = 0;
  const result = await copyCommand(TEXT, { clipboard: undefined, selectText: () => { selected += 1; } });
  assert.equal(result, 'selected');
  assert.equal(selected, 1);
});

test('treats a clipboard with no writeText as no clipboard API', async () => {
  let selected = 0;
  const result = await copyCommand(TEXT, { clipboard: {}, selectText: () => { selected += 1; } });
  assert.equal(result, 'selected');
  assert.equal(selected, 1);
});

test('returns failed when the clipboard rejects the text', async () => {
  const clipboard = { writeText: async () => { throw new Error('NotAllowedError'); } };
  const result = await copyCommand(TEXT, { clipboard, selectText: () => {} });
  assert.equal(result, 'failed');
});

test('returns failed when the clipboard throws at once', async () => {
  const clipboard = { writeText: () => { throw new Error('SecurityError'); } };
  assert.equal(await copyCommand(TEXT, { clipboard }), 'failed');
});

test('returns failed when there is no clipboard API and the selection throws', async () => {
  const result = await copyCommand(TEXT, { selectText: () => { throw new Error('no selection'); } });
  assert.equal(result, 'failed');
});

test('returns failed when there is neither a clipboard API nor a select function', async () => {
  assert.equal(await copyCommand(TEXT, {}), 'failed');
});
