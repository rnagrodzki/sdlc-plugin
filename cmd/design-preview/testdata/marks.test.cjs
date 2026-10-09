'use strict';

/**
 * Tests for the design preview marks script (cmd/design-preview/marks.js).
 * Uses Node's built-in test runner (node --test), like the dashboard
 * view.test.cjs. The pure model functions are required directly. designDep
 * reads globalThis.__designDeps, so the tests set the state without a browser.
 * The browser bootstrap runs in a vm context with a small fake document.
 */

const { test, describe, afterEach } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const MARKS_PATH = path.join(__dirname, '../marks.js');
const marks = require('../marks.js');

const DASH = '—';

function okState(dependencies) {
  return { ok: true, dependencies };
}

const STATE = okState([
  { id: 'D1', kind: 'data', need: 'Queue wait for each run.', elements: ['.wait'], sample: '4m 12s' },
  { id: 'D3', kind: 'data', need: 'Run owner.', elements: [] },
]);

describe('isMissing', () => {
  test('is true for null, undefined, empty string and NaN', () => {
    for (const v of [null, undefined, '', NaN]) {
      assert.equal(marks.isMissing(v), true, String(v));
    }
  });

  test('is false for 0, false, a blank string, an object and an array', () => {
    for (const v of [0, false, ' ', 'x', {}, []]) {
      assert.equal(marks.isMissing(v), false, JSON.stringify(v));
    }
  });
});

describe('readState', () => {
  test('parses the design-deps tag text', () => {
    const text = JSON.stringify({ ok: true, dependencies: [{ id: 'D1', kind: 'data', need: 'n' }] });
    assert.deepEqual(marks.readState(text), JSON.parse(text));
  });

  test('keeps the error of a state that the server marked bad', () => {
    const text = JSON.stringify({ ok: false, error: 'deps.json: not found' });
    assert.deepEqual(marks.readState(text), { ok: false, error: 'deps.json: not found' });
  });

  test('gives { ok: false, error } for bad JSON', () => {
    const state = marks.readState('{not json');
    assert.equal(state.ok, false);
    assert.match(state.error, /^design-deps: not valid JSON: /);
  });

  test('gives { ok: false, error } for empty text and for a missing tag', () => {
    assert.equal(marks.readState('').ok, false);
    assert.equal(marks.readState(undefined).ok, false);
  });

  test('gives { ok: false, error } for JSON that is not an object', () => {
    for (const text of ['null', '[]', '"x"', '3']) {
      const state = marks.readState(text);
      assert.equal(state.ok, false, text);
      assert.equal(typeof state.error, 'string', text);
    }
  });

  test('gives { ok: false, error } when ok is true and dependencies is not an array', () => {
    const state = marks.readState('{"ok":true,"dependencies":{}}');
    assert.equal(state.ok, false);
    assert.match(state.error, /dependencies must be an array/);
  });
});

describe('resolveDep', () => {
  test('live value present: no mark, no label, live value', () => {
    assert.deepEqual(marks.resolveDep(STATE, 'D1', '9m'), { text: '9m', mark: null, label: '' });
  });

  test('live value 0 is a live value', () => {
    assert.deepEqual(marks.resolveDep(STATE, 'D1', 0), { text: '0', mark: null, label: '' });
  });

  test('empty value, id known: missing mark, label with the id, sample as text', () => {
    assert.deepEqual(marks.resolveDep(STATE, 'D1', ''), {
      text: '4m 12s',
      mark: 'missing',
      label: 'missing D1',
    });
  });

  test('every empty value kind gives the missing mark', () => {
    for (const v of [null, undefined, '', NaN]) {
      const r = marks.resolveDep(STATE, 'D1', v);
      assert.equal(r.mark, 'missing', String(v));
      assert.equal(r.text, '4m 12s', String(v));
    }
  });

  test('empty value, id known, no sample: dash as text', () => {
    assert.deepEqual(marks.resolveDep(STATE, 'D3', undefined), {
      text: DASH,
      mark: 'missing',
      label: 'missing D3',
    });
  });

  test('a sample that is not a string is shown as JSON text', () => {
    const state = okState([
      { id: 'D4', kind: 'data', need: 'n', sample: 42 },
      { id: 'D5', kind: 'data', need: 'n', sample: { a: 1 } },
    ]);
    assert.equal(marks.resolveDep(state, 'D4', null).text, '42');
    assert.equal(marks.resolveDep(state, 'D5', null).text, '{"a":1}');
  });

  test('id unknown, live value: unknown mark, label with the id, live value as text', () => {
    assert.deepEqual(marks.resolveDep(STATE, 'D9', 'abc'), {
      text: 'abc',
      mark: 'unknown',
      label: 'unknown dependency D9',
    });
  });

  test('id unknown, empty value: unknown mark and dash as text', () => {
    assert.deepEqual(marks.resolveDep(STATE, 'D9', ''), {
      text: DASH,
      mark: 'unknown',
      label: 'unknown dependency D9',
    });
  });

  test('a bad state knows no id', () => {
    for (const state of [{ ok: false, error: 'x' }, null, undefined]) {
      const r = marks.resolveDep(state, 'D1', 'abc');
      assert.equal(r.mark, 'unknown');
      assert.equal(r.text, 'abc');
    }
  });
});

describe('designDep', () => {
  function fakeEl() {
    const classes = new Set();
    return {
      dataset: {},
      classes,
      classList: {
        add: (...names) => names.forEach((n) => classes.add(n)),
        remove: (...names) => names.forEach((n) => classes.delete(n)),
      },
    };
  }

  afterEach(() => {
    delete globalThis.__designDeps;
  });

  test('live value: returns the value and sets no mark and no class', () => {
    globalThis.__designDeps = STATE;
    const el = fakeEl();
    assert.equal(marks.designDep('D1', '9m', el), '9m');
    assert.equal(el.dataset.designMark, undefined);
    assert.equal(el.dataset.designLabel, undefined);
    assert.equal(el.classes.size, 0);
  });

  test('missing: returns the sample, sets the mark, the label and the class', () => {
    globalThis.__designDeps = STATE;
    const el = fakeEl();
    assert.equal(marks.designDep('D1', '', el), '4m 12s');
    assert.equal(el.dataset.designMark, 'missing');
    assert.equal(el.dataset.designLabel, 'missing D1');
    assert.deepEqual([...el.classes], ['design-missing']);
  });

  test('unknown: returns the live value, sets the mark, the label and the class', () => {
    globalThis.__designDeps = STATE;
    const el = fakeEl();
    assert.equal(marks.designDep('D9', 'abc', el), 'abc');
    assert.equal(el.dataset.designMark, 'unknown');
    assert.equal(el.dataset.designLabel, 'unknown dependency D9');
    assert.deepEqual([...el.classes], ['design-unknown']);
  });

  test('a repeat call on the same element replaces the earlier mark', () => {
    globalThis.__designDeps = STATE;
    const el = fakeEl();
    marks.designDep('D1', '', el);
    marks.designDep('D9', 'abc', el);
    assert.deepEqual([...el.classes], ['design-unknown']);
    marks.designDep('D1', '9m', el);
    assert.equal(el.classes.size, 0);
    assert.equal(el.dataset.designMark, undefined);
    assert.equal(el.dataset.designLabel, undefined);
  });

  test('with no element it only returns the text', () => {
    globalThis.__designDeps = STATE;
    assert.equal(marks.designDep('D3', null), DASH);
  });

  test('with no state every id is unknown', () => {
    const el = fakeEl();
    assert.equal(marks.designDep('D1', 'abc', el), 'abc');
    assert.equal(el.dataset.designMark, 'unknown');
  });
});

describe('panelModel', () => {
  test('gives "No dependencies yet" for an empty list', () => {
    const m = marks.panelModel(okState([]));
    assert.equal(m.empty, 'No dependencies yet');
    assert.deepEqual(m.rows, []);
    assert.equal(m.error, null);
    assert.equal(typeof m.title, 'string');
  });

  test('gives one row for each record', () => {
    const m = marks.panelModel(STATE);
    assert.equal(m.empty, null);
    assert.equal(m.error, null);
    assert.deepEqual(m.rows, [
      { id: 'D1', kind: 'data', need: 'Queue wait for each run.', sample: '4m 12s' },
      { id: 'D3', kind: 'data', need: 'Run owner.', sample: '' },
    ]);
  });

  test('gives the error for a bad file', () => {
    const m = marks.panelModel({ ok: false, error: 'dependencies.json: not found' });
    assert.equal(m.error, 'dependencies.json: not found');
    assert.deepEqual(m.rows, []);
    assert.equal(m.empty, null);
  });

  test('gives an error when there is no state at all', () => {
    const m = marks.panelModel(undefined);
    assert.equal(typeof m.error, 'string');
    assert.ok(m.error.length > 0);
    assert.deepEqual(m.rows, []);
  });
});

describe('bannerModel', () => {
  test('gives the text for a dashboard that is not running', () => {
    assert.equal(
      marks.bannerModel({ dashboard: 'not running' }),
      'Dashboard not running. Run /sdlc:dashboard, then reload.'
    );
  });

  test('gives null for a dashboard that is running', () => {
    assert.equal(marks.bannerModel({ dashboard: 'running' }), null);
  });

  test('gives null for a status that it does not know', () => {
    for (const status of [undefined, null, {}, { dashboard: 'starting' }, 'not running']) {
      assert.equal(marks.bannerModel(status), null, JSON.stringify(status));
    }
  });
});

describe('browser bootstrap', () => {
  function fakeNode(tag) {
    return {
      tag,
      id: '',
      className: '',
      textContent: '',
      children: [],
      appendChild(child) {
        this.children.push(child);
        return child;
      },
    };
  }

  // runInBrowser runs marks.js in a vm context with a fake window and document.
  // statusResult is the JSON that fetch gives, or an Error for a failed fetch.
  function runInBrowser(depsText, statusResult) {
    const head = fakeNode('head');
    const body = fakeNode('body');
    const listeners = {};
    const tags = {};
    if (depsText !== null) tags['design-deps'] = { textContent: depsText };
    const document = {
      head,
      body,
      createElement: fakeNode,
      getElementById: (id) => tags[id] || head.children.find((c) => c.id === id) || null,
      addEventListener: (name, fn) => {
        listeners[name] = fn;
      },
    };
    const fetchCalls = [];
    const sandbox = {
      document,
      fetch: (url) => {
        fetchCalls.push(url);
        if (statusResult instanceof Error) return Promise.reject(statusResult);
        return Promise.resolve({ json: () => Promise.resolve(statusResult) });
      },
    };
    sandbox.window = sandbox;
    sandbox.globalThis = sandbox;
    vm.createContext(sandbox);
    vm.runInContext(fs.readFileSync(MARKS_PATH, 'utf8'), sandbox);
    return { sandbox, head, body, listeners, fetchCalls };
  }

  const settle = () => new Promise((resolve) => setImmediate(resolve));

  test('does not run when there is no window (Node)', () => {
    assert.equal(typeof window, 'undefined');
    assert.equal(globalThis.designDep, undefined);
    assert.equal(globalThis.__designDeps, undefined);
  });

  test('adds one style tag, reads the state and defines window.designDep', () => {
    const text = JSON.stringify(okState([{ id: 'D1', kind: 'data', need: 'n', sample: 's' }]));
    const { sandbox, head } = runInBrowser(text, { dashboard: 'running' });
    const styles = head.children.filter((c) => c.id === 'design-marks-style');
    assert.equal(styles.length, 1);
    for (const name of ['.design-missing', '.design-unknown', '#design-panel', '#design-banner']) {
      assert.ok(styles[0].textContent.includes(name), name);
    }
    assert.ok(styles[0].textContent.includes('attr(data-design-label)'));
    assert.equal(sandbox.__designDeps.ok, true);
    assert.equal(typeof sandbox.designDep, 'function');
  });

  test('a missing design-deps tag gives a bad state, not a crash', () => {
    const { sandbox } = runInBrowser(null, { dashboard: 'running' });
    assert.equal(sandbox.__designDeps.ok, false);
  });

  test('on DOMContentLoaded it renders the panel and no banner when the dashboard runs', async () => {
    const text = JSON.stringify(okState([{ id: 'D1', kind: 'data', need: 'Queue wait.' }]));
    const { body, listeners, fetchCalls } = runInBrowser(text, { dashboard: 'running' });
    listeners.DOMContentLoaded();
    await settle();
    assert.deepEqual(fetchCalls, ['/__design/status']);
    assert.equal(body.children.filter((c) => c.id === 'design-panel').length, 1);
    assert.equal(body.children.filter((c) => c.id === 'design-banner').length, 0);
  });

  test('on DOMContentLoaded it renders the banner when the dashboard is not running', async () => {
    const { body, listeners } = runInBrowser('[]', { dashboard: 'not running' });
    listeners.DOMContentLoaded();
    await settle();
    const banner = body.children.find((c) => c.id === 'design-banner');
    assert.equal(banner.textContent, 'Dashboard not running. Run /sdlc:dashboard, then reload.');
    // The state '[]' is bad, so the panel shows its error.
    const panel = body.children.find((c) => c.id === 'design-panel');
    assert.ok(panel.children.some((c) => c.className === 'design-panel-error'));
  });

  test('a failed status fetch shows no banner', async () => {
    const { body, listeners } = runInBrowser('{"ok":true,"dependencies":[]}', new Error('offline'));
    listeners.DOMContentLoaded();
    await settle();
    assert.equal(body.children.filter((c) => c.id === 'design-banner').length, 0);
    assert.equal(body.children.filter((c) => c.id === 'design-panel').length, 1);
  });
});
