'use strict';

/**
 * Tests for the dashboard_card MCP App view (internal/tools/dashboard_card.html).
 * Uses Node's built-in test runner (node --test), like view.test.cjs. The test
 * runs the inline script of the view in a Node vm context with a fake host:
 * a fake window.parent that records each posted message, a fake card element,
 * and fake timers.
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const HTML = path.join(__dirname, '..', '..', '..', 'tools', 'dashboard_card.html');
const RUN_ID = 'ship-feat-x-20261116T175538Z';
const STOPPED = 'Stopped — resume with --resume';

// load runs the view script with a fake host and returns the host handles.
// documentElement, when given, is the fake page element that the view
// measures for ui/notifications/size-changed.
function load(documentElement) {
  const html = fs.readFileSync(HTML, 'utf8');
  const m = html.match(/<script>([\s\S]*?)<\/script>/);
  assert.ok(m, 'the view has one inline script');

  const posted = [];
  const timers = new Map();
  let nextTimer = 1;
  const winListeners = {};
  const root = {
    innerHTML: '',
    listeners: {},
    addEventListener(type, fn) { this.listeners[type] = fn; },
  };
  const parent = {
    // JSON round trip: the messages become objects of this realm.
    postMessage(msg) { posted.push(JSON.parse(JSON.stringify(msg))); },
  };
  const ctx = {
    window: { parent, addEventListener(type, fn) { winListeners[type] = fn; } },
    document: { documentElement, getElementById: (id) => (id === 'card' ? root : null) },
    setTimeout(fn, ms) { const id = nextTimer++; timers.set(id, { fn, ms }); return id; },
    clearTimeout(id) { timers.delete(id); },
  };
  vm.createContext(ctx);
  vm.runInContext(m[1], ctx);

  return {
    posted,
    timers,
    root,
    deliver(msg, source) { winListeners.message({ data: msg, source: source === undefined ? parent : source }); },
    fireTimer() {
      const [id, t] = [...timers][0];
      timers.delete(id);
      t.fn();
    },
    click(action) {
      root.listeners.click({ target: { getAttribute: (n) => (n === 'data-action' ? action : null) } });
    },
    methods() { return posted.map((p) => p.method); },
    calls() { return posted.filter((p) => p.method === 'tools/call'); },
  };
}

// flush lets the promise callbacks of the view run.
const flush = () => new Promise((resolve) => setImmediate(resolve));

// card returns a card of the dashboard_card tool with the given changes.
function card(changes) {
  return Object.assign({
    summary: 's',
    runId: RUN_ID,
    kind: 'ship',
    branch: 'feat/x',
    status: 'running',
    final: false,
    startedAt: '2026-11-16T17:55:38Z',
    completedAt: null,
    steps: [{ name: 'execute', status: 'completed' }],
    issues: {},
    next: 'n',
  }, changes);
}

// result wraps a card as a successful tool result.
function result(out) {
  return { content: [{ type: 'text', text: '# dashboard_card — ok' }], structuredContent: out };
}

// start answers ui/initialize and returns the host after the handshake.
async function start() {
  const h = load();
  const init = h.posted[0];
  h.deliver({ jsonrpc: '2.0', id: init.id, result: { protocolVersion: '2026-01-26', hostInfo: { name: 'fake', version: '0' }, hostCapabilities: {}, hostContext: {} } });
  await flush();
  return h;
}

// toolResult sends the first tool result of the card.
function toolResult(h, out) {
  h.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: result(out) });
}

// reply answers the last tools/call request.
async function reply(h, msg) {
  const call = h.calls().at(-1);
  h.deliver(Object.assign({ jsonrpc: '2.0', id: call.id }, msg));
  await flush();
}

// polling returns a host whose view polls a running card.
async function polling() {
  const h = await start();
  toolResult(h, card());
  assert.equal(h.timers.size, 1);
  return h;
}

describe('call order', () => {
  test('follows the sequence diagram', async () => {
    const h = load();
    assert.deepEqual(h.posted[0], {
      jsonrpc: '2.0',
      id: h.posted[0].id,
      method: 'ui/initialize',
      params: { protocolVersion: '2026-01-26', appInfo: { name: 'sdlc-dashboard-card', version: '1.0.0' }, appCapabilities: {} },
    });
    h.deliver({ jsonrpc: '2.0', id: h.posted[0].id, result: {} });
    await flush();
    assert.deepEqual(h.posted[1], { jsonrpc: '2.0', method: 'ui/notifications/initialized' });

    toolResult(h, card());
    assert.match(h.root.innerHTML, new RegExp(RUN_ID));
    assert.deepEqual([...h.timers.values()].map((t) => t.ms), [15000]);

    h.fireTimer();
    assert.deepEqual(h.calls().at(-1).params, { name: 'dashboard_card', arguments: { runId: RUN_ID } });
    await reply(h, { result: result(card({ steps: [{ name: 'commit', status: 'in_progress' }] })) });
    assert.match(h.root.innerHTML, /commit/);
    assert.deepEqual(h.methods(), ['ui/initialize', 'ui/notifications/initialized', 'tools/call']);
  });

  test('each draw is followed by ui/notifications/size-changed when the page can be measured', async () => {
    const page = { scrollWidth: 320, scrollHeight: 180 };
    const h = load(page);
    h.deliver({ jsonrpc: '2.0', id: h.posted[0].id, result: {} });
    await flush();
    toolResult(h, card());
    assert.deepEqual(h.methods(), ['ui/initialize', 'ui/notifications/initialized', 'ui/notifications/size-changed']);
    assert.deepEqual(h.posted[2], { jsonrpc: '2.0', method: 'ui/notifications/size-changed', params: { width: 320, height: 180 } });

    page.scrollHeight = 240;
    h.fireTimer();
    await reply(h, { result: result(card({ status: 'completed' })) });
    assert.deepEqual(h.methods().slice(3), ['tools/call', 'ui/notifications/size-changed']);
    assert.deepEqual(h.posted.at(-1).params, { width: 320, height: 240 });
  });

  test('ignores a message that does not come from the host', async () => {
    const h = await start();
    h.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: result(card()) }, {});
    assert.equal(h.root.innerHTML, '');
    assert.equal(h.timers.size, 0);
  });

  test('a failed ui/initialize shows the host error and sends no initialized notification', async () => {
    const h = load();
    h.deliver({ jsonrpc: '2.0', id: h.posted[0].id, error: { code: -32601, message: 'not supported' } });
    await flush();
    assert.equal(h.root.innerHTML, '<p class="note">Card host error: not supported</p>');
    assert.deepEqual(h.methods(), ['ui/initialize']);
  });

  test('an isError tool-result shows the error and starts no timer', async () => {
    const h = await start();
    const text = '# dashboard_card — error (domain)\n\n## What happened\nrun ' + RUN_ID + ' belongs to another T3 thread\n';
    h.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: { isError: true, content: [{ type: 'text', text }] } });
    assert.equal(h.root.innerHTML, '<p class="note">Card error: run ' + RUN_ID + ' belongs to another T3 thread</p>');
    assert.equal(h.timers.size, 0);
    h.click('refresh');
    assert.equal(h.calls().length, 0, 'no runId, so Refresh sends nothing');
  });
});

describe('refresh states', () => {
  test('waiting, tool-result with final true: a running status shows as stopped', async () => {
    const h = await start();
    toolResult(h, card({ final: true }));
    assert.match(h.root.innerHTML, new RegExp('class="status stopped">' + STOPPED + '<'));
    assert.match(h.root.innerHTML, new RegExp('Completed: ' + STOPPED));
    assert.equal(h.timers.size, 0);
  });

  test('waiting, tool-result with status running: draw and start a 15 s timer', async () => {
    const h = await start();
    toolResult(h, card());
    assert.match(h.root.innerHTML, /class="status running">Running</);
    assert.deepEqual([...h.timers.values()].map((t) => t.ms), [15000]);
  });

  test('waiting, tool-result with another status: draw and stop', async () => {
    const h = await start();
    toolResult(h, card({ status: 'completed', completedAt: '2026-11-16T18:10:00Z' }));
    assert.match(h.root.innerHTML, /class="status completed">Completed</);
    assert.match(h.root.innerHTML, /Completed: 2026-11-16T18:10:00Z/);
    assert.equal(h.timers.size, 0);
  });

  test('polling, timer: tools/call with the same runId', async () => {
    const h = await polling();
    h.fireTimer();
    assert.equal(h.calls().length, 1);
    assert.deepEqual(h.calls()[0].params, { name: 'dashboard_card', arguments: { runId: RUN_ID } });
  });

  test('polling, result with status running: draw and keep polling', async () => {
    const h = await polling();
    h.fireTimer();
    await reply(h, { result: result(card({ branch: 'feat/next' })) });
    assert.match(h.root.innerHTML, /Branch: feat\/next/);
    assert.deepEqual([...h.timers.values()].map((t) => t.ms), [15000]);
  });

  test('polling, result with another status: draw and stop the timer', async () => {
    const h = await polling();
    h.fireTimer();
    await reply(h, { result: result(card({ status: 'failed' })) });
    assert.match(h.root.innerHTML, /class="status failed">Failed</);
    assert.equal(h.timers.size, 0);
  });

  test('polling, call error: show the first line and stop the timer', async () => {
    const h = await polling();
    h.fireTimer();
    await reply(h, { error: { code: -32603, message: 'host gone\nmore text' } });
    assert.match(h.root.innerHTML, /<p class="note">Refresh stopped: host gone<\/p>/);
    assert.match(h.root.innerHTML, new RegExp(RUN_ID), 'the last card stays');
    assert.equal(h.timers.size, 0);
  });

  test('polling, isError result: show the first message line and stop the timer', async () => {
    const h = await polling();
    h.fireTimer();
    const text = '# dashboard_card — error (domain)\n\n## What happened\nrun ' + RUN_ID + ' not found\n\n## Do this\nUse the stateFile.\n';
    await reply(h, { result: { isError: true, content: [{ type: 'text', text }] } });
    assert.match(h.root.innerHTML, new RegExp('Refresh stopped: run ' + RUN_ID + ' not found<'));
    assert.equal(h.timers.size, 0);
  });

  test('Refresh button: one tools/call, then the timer rules', async () => {
    const h = await start();
    toolResult(h, card({ status: 'completed' }));
    h.click('refresh');
    h.click('refresh');
    assert.equal(h.calls().length, 1, 'a second click during a call sends nothing');
    await reply(h, { result: result(card()) });
    assert.deepEqual([...h.timers.values()].map((t) => t.ms), [15000]);
  });

  test('Refresh button on an end card keeps final and starts no timer', async () => {
    const h = await start();
    toolResult(h, card({ final: true }));
    h.click('refresh');
    assert.deepEqual(h.calls()[0].params, { name: 'dashboard_card', arguments: { runId: RUN_ID, final: true } });
    await reply(h, { result: result(card({ final: true })) });
    assert.match(h.root.innerHTML, new RegExp(STOPPED));
    assert.equal(h.timers.size, 0);
  });
});

describe('empty and absent values', () => {
  test('steps, issues, completedAt and t3ThreadId', async () => {
    const h = await start();
    toolResult(h, card({ steps: [], issues: {}, completedAt: null }));
    assert.match(h.root.innerHTML, /<p class="empty">No steps yet\.<\/p>/);
    assert.match(h.root.innerHTML, /<p class="empty">No issues\.<\/p>/);
    assert.match(h.root.innerHTML, /Completed: Running</);
    assert.doesNotMatch(h.root.innerHTML, /T3 thread/);
  });

  test('filled values show the thread chip and issue counts in key order', async () => {
    const h = await start();
    toolResult(h, card({ t3ThreadId: 'thread-a', issues: { medium: 2, high: 1 } }));
    assert.match(h.root.innerHTML, /<span class="chip thread">T3 thread thread-a<\/span>/);
    assert.match(h.root.innerHTML, /<span class="chip">high 1<\/span><span class="chip">medium 2<\/span>/);
  });
});

describe('escaping', () => {
  test('state text and error text are escaped', async () => {
    const h = await start();
    toolResult(h, card({
      branch: '<b>x</b>',
      status: 'odd"status',
      steps: [{ name: '<i>s</i>', status: 'x"y' }],
    }));
    const out = h.root.innerHTML;
    assert.match(out, /Branch: &lt;b&gt;x&lt;\/b&gt;/);
    assert.match(out, /&lt;i&gt;s&lt;\/i&gt;/);
    assert.match(out, /class="status unknown">Odd&quot;status</);
    assert.match(out, /<li class="">/);
    assert.doesNotMatch(out, /<b>|<i>/);

    h.click('refresh');
    await reply(h, { error: { message: '<script>bad' } });
    assert.match(h.root.innerHTML, /Refresh stopped: &lt;script&gt;bad/);
  });
});
