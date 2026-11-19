'use strict';

/**
 * Tests for the Setup tab adapter (internal/dashboard/web/static/setup-view.js).
 * Uses Node's built-in test runner (node --test), like view.test.cjs. The
 * adapter has no DOM access, so it is required directly here.
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');

const sv = require('../static/setup-view.js');

describe('exports', () => {
  test('setup-view.js exports the 13 names', () => {
    const names = ['FIELDS_URL', 'stateURL', 'draftSections', 'draftState', 'overrideText', 'guardrailChanges',
      'helpTable', 'saveBody', 'saveRequest', 'createRequest', 'answerText', 'saveNote', 'plural'];
    assert.deepEqual(Object.keys(sv).sort(), names.slice().sort());
  });
});

describe('FIELDS_URL / stateURL', () => {
  test('FIELDS_URL is the fields route', () => {
    assert.equal(sv.FIELDS_URL, '/api/setup-fields');
  });

  test('stateURL encodes the repo', () => {
    assert.equal(sv.stateURL('/a b/repo'), '/api/setup-state?repo=%2Fa%20b%2Frepo');
  });
});

describe('plural', () => {
  test('adds s unless n is 1', () => {
    assert.equal(sv.plural(0, 'change'), '0 changes');
    assert.equal(sv.plural(1, 'change'), '1 change');
    assert.equal(sv.plural(2, 'field'), '2 fields');
  });
});

describe('draftSections', () => {
  test('copies each key and renames whenStepInActiveSteps to whenStep', () => {
    const input = {
      sections: [{
        id: 'ship', label: 'ship', configFile: '.sdlc-v2/local.toml', configPath: 'ship',
        defaultTarget: 'project', optional: true,
        fields: [
          { name: 'bump', type: 'enum', options: ['patch'], default: 'patch', details: 'd' },
          { name: 'quick', type: 'flag-set', whenStepInActiveSteps: 'review' },
        ],
      }],
    };
    const out = sv.draftSections(input);
    assert.deepEqual(out, [{
      id: 'ship', label: 'ship', configFile: '.sdlc-v2/local.toml', configPath: 'ship',
      defaultTarget: 'project', optional: true,
      fields: [
        { name: 'bump', type: 'enum', options: ['patch'], default: 'patch', details: 'd' },
        { name: 'quick', type: 'flag-set', whenStep: 'review' },
      ],
    }]);
  });

  test('does not change the input', () => {
    const input = { sections: [{ id: 's', fields: [{ name: 'f', whenStepInActiveSteps: 'x' }] }] };
    sv.draftSections(input);
    assert.equal(input.sections[0].fields[0].whenStepInActiveSteps, 'x');
    assert.equal('whenStep' in input.sections[0].fields[0], false);
  });

  test('gives an empty list for an empty answer', () => {
    assert.deepEqual(sv.draftSections({}), []);
    assert.deepEqual(sv.draftSections(null), []);
  });
});

describe('draftState', () => {
  test('keeps values as they are', () => {
    const values = { ship: { bump: { value: 'patch', source: 'user' } } };
    assert.deepEqual(sv.draftState({ values, delegated: [] }).values, values);
  });

  test('converts each delegated row to the draft shape', () => {
    const out = sv.draftState({
      values: {},
      delegated: [
        { id: 'commit', set: true, count: 2 },
        { id: 'pr', set: false },
        { id: 'pr-labels', set: true, mode: 'rules' },
        { id: 'review-dimensions', set: true, count: 7 },
        { id: 'plan-guardrails', set: false },
        { id: 'execution-guardrails', set: true, count: 3 },
        { id: 'pr-template', set: true },
        { id: 'plan-template', set: false },
        { id: 'openspec-block', set: true },
      ],
    });
    assert.deepEqual(out.delegated, {
      commit: { configured: true, keys: 2 },
      pr: { configured: false, keys: 0 },
      'pr-labels': { mode: 'rules' },
      'review-dimensions': { count: 7 },
      'plan-guardrails': { count: 0 },
      'execution-guardrails': { count: 3 },
      'pr-template': { exists: true },
      'plan-template': { exists: false },
      'openspec-block': { present: true },
    });
  });

  test('an empty pr-labels mode gives null', () => {
    assert.deepEqual(sv.draftState({ delegated: [{ id: 'pr-labels', set: false, mode: '' }] }).delegated,
      { 'pr-labels': { mode: null } });
    assert.deepEqual(sv.draftState({ delegated: [{ id: 'pr-labels', set: false }] }).delegated,
      { 'pr-labels': { mode: null } });
  });

  test('a state that is not ready gives empty data', () => {
    assert.deepEqual(sv.draftState({ state: 'no-config', values: {}, delegated: [] }),
      { values: {}, delegated: {}, layers: {}, guardrails: {} });
  });

  test('passes layers and guardrails through with the keys of the answer', () => {
    const layers = { 'ship.bump': { project: 'minor', user: 'patch' } };
    const guardrails = {
      'plan.guardrails': [{ id: 'dry', severity: 'warning', description: 'Keep code DRY.' }],
      'execute.guardrails': [],
    };
    const out = sv.draftState({ values: {}, delegated: [], layers, guardrails });
    assert.deepEqual(out.layers, layers);
    assert.deepEqual(out.guardrails, guardrails);
    assert.deepEqual(Object.keys(out.guardrails), ['plan.guardrails', 'execute.guardrails']);
  });

  test('a missing or null layers and guardrails give {}', () => {
    const out = sv.draftState({ values: {}, delegated: [], layers: null });
    assert.deepEqual(out.layers, {});
    assert.deepEqual(out.guardrails, {});
    assert.deepEqual(sv.draftState(undefined).guardrails, {});
  });
});

describe('overrideText', () => {
  test('a flag-set field gives merged with and no strike', () => {
    assert.deepEqual(sv.overrideText('flag-set', false, 'local.toml'), { text: 'merged with local.toml', struck: false });
    assert.deepEqual(sv.overrideText('flag-set', true, 'local.toml'), { text: 'merged with local.toml', struck: false });
  });

  test('another type with another value gives overridden by and a strike', () => {
    assert.deepEqual(sv.overrideText('enum', false, '~/.sdlc/local.toml'),
      { text: 'overridden by ~/.sdlc/local.toml', struck: true });
  });

  test('another type with the same value gives same value and a strike', () => {
    assert.deepEqual(sv.overrideText('string', true, 'local.toml'),
      { text: 'same value · overridden by local.toml', struck: true });
  });
});

describe('guardrailChanges', () => {
  const g = (id, severity, description) => ({ id, severity, description });
  const row = (id, severity, description, isNew, removed) => ({ id, severity, description, isNew, removed });

  test('an unchanged disk row gives no change', () => {
    assert.deepEqual(sv.guardrailChanges(
      { 'plan-guardrails': [g('a', 'error', 'one')] },
      { 'plan-guardrails': [row('a', 'error', 'one', false, false)] }), {});
  });

  test('a disk row with other values gives change with both values', () => {
    assert.deepEqual(sv.guardrailChanges(
      { 'execution-guardrails': [g('a', 'error', 'one')] },
      { 'execution-guardrails': [row('a', 'warning', 'two', false, false)] }),
    { 'execution-guardrails': [{ id: 'a', op: 'change', severity: 'warning', description: 'two' }] });
  });

  test('a removed disk row gives remove', () => {
    assert.deepEqual(sv.guardrailChanges(
      { 'plan-guardrails': [g('a', 'error', 'one')] },
      { 'plan-guardrails': [row('a', 'error', 'one', false, true)] }),
    { 'plan-guardrails': [{ id: 'a', op: 'remove' }] });
  });

  test('a new row gives add with both values', () => {
    assert.deepEqual(sv.guardrailChanges(
      { 'plan-guardrails': [] },
      { 'plan-guardrails': [row('b', 'error', 'New rule.', true, false)] }),
    { 'plan-guardrails': [{ id: 'b', op: 'add', severity: 'error', description: 'New rule.' }] });
  });

  test('a removed disk row plus a new row with the same id give one change', () => {
    assert.deepEqual(sv.guardrailChanges(
      { 'plan-guardrails': [g('a', 'error', 'one')] },
      { 'plan-guardrails': [row('a', 'error', 'one', false, true), row('a', 'warning', 'two', true, false)] }),
    { 'plan-guardrails': [{ id: 'a', op: 'change', severity: 'warning', description: 'two' }] });
  });

  test('a removed new row gives no change', () => {
    assert.deepEqual(sv.guardrailChanges(
      { 'plan-guardrails': [] },
      { 'plan-guardrails': [row('b', 'error', 'x', true, true)] }), {});
  });

  test('example: add, change and remove in two sections; a section with no change gives no key', () => {
    const base = {
      'plan-guardrails': [g('dry', 'error', 'Keep code DRY.'), g('old-rule', 'error', 'Old rule.')],
      'execution-guardrails': [g('x', 'error', 'X.')],
    };
    const draft = {
      'plan-guardrails': [
        row('dry', 'warning', 'Keep code DRY.', false, false),
        row('old-rule', 'error', 'Old rule.', false, true),
        row('new-rule', 'error', 'New rule.', true, false),
      ],
      'execution-guardrails': [row('x', 'error', 'X.', false, false)],
    };
    assert.deepEqual(sv.guardrailChanges(base, draft), {
      'plan-guardrails': [
        { id: 'dry', op: 'change', severity: 'warning', description: 'Keep code DRY.' },
        { id: 'old-rule', op: 'remove' },
        { id: 'new-rule', op: 'add', severity: 'error', description: 'New rule.' },
      ],
    });
  });

  test('no draft gives {}', () => {
    assert.deepEqual(sv.guardrailChanges({}, {}), {});
    assert.deepEqual(sv.guardrailChanges(undefined, undefined), {});
  });
});

describe('helpTable', () => {
  test('gives one entry for each option effect, keyed by section and field', () => {
    const table = sv.helpTable([{
      id: 'ship',
      fields: [
        { name: 'bump', type: 'enum', effects: [
          { option: 'patch', text: 'P', before: '1.2.3', after: '1.2.4' },
          { option: 'minor', text: 'M' },
        ] },
        { name: 'none', type: 'string' },
      ],
    }]);
    assert.deepEqual(table, {
      'ship/bump': {
        patch: { text: 'P', before: '1.2.3', after: '1.2.4' },
        minor: { text: 'M' },
      },
    });
  });

  test('the options yes and no of a boolean become on and off', () => {
    const table = sv.helpTable([{
      id: 'version',
      fields: [{ name: 'tag.enabled', type: 'boolean', effects: [
        { option: 'yes', text: 'Y' }, { option: 'no', text: 'N' },
      ] }],
    }]);
    assert.deepEqual(table, { 'version/tag.enabled': { on: { text: 'Y' }, off: { text: 'N' } } });
  });

  test('keeps yes and no of a field that is not a boolean', () => {
    const table = sv.helpTable([{
      id: 's', fields: [{ name: 'f', type: 'enum', effects: [{ option: 'yes', text: 'Y' }] }],
    }]);
    assert.deepEqual(Object.keys(table['s/f']), ['yes']);
  });
});

describe('saveBody', () => {
  const sections = [
    { id: 'ship', configFile: '.sdlc-v2/local.toml', fields: [{ name: 'bump' }, { name: 'auto' }] },
    { id: 'version', configFile: '.sdlc-v2/config.toml', fields: [{ name: 'tag.prefix' }] },
  ];
  function state() {
    return {
      base: { ship: { bump: 'patch' }, version: { 'tag.prefix': 'v' } },
      draft: { ship: { bump: 'patch' }, version: { 'tag.prefix': 'v' } },
      target: { ship: 'project' },
      baseTarget: { ship: 'project' },
    };
  }

  test('example: a changed value gives a set entry and the target', () => {
    const st = state();
    st.draft.ship.bump = 'minor';
    assert.deepEqual(sv.saveBody('/repo', sections, st), {
      repo: '/repo',
      changes: [{ section: 'ship', field: 'bump', op: 'set', value: 'minor' }],
      targets: { ship: 'project' },
    });
  });

  test('no change gives no entry and no target', () => {
    assert.deepEqual(sv.saveBody('/repo', sections, state()), { repo: '/repo', changes: [], targets: {} });
  });

  test('a change of the Save to switch with no field change gives no change', () => {
    const st = state();
    st.target.ship = 'user';
    assert.deepEqual(sv.saveBody('/repo', sections, st), { repo: '/repo', changes: [], targets: {} });
  });

  test('a new value for a field with no base value gives a set entry', () => {
    const st = state();
    st.draft.ship.auto = true;
    assert.deepEqual(sv.saveBody('/repo', sections, st).changes,
      [{ section: 'ship', field: 'auto', op: 'set', value: true }]);
  });

  test('a removed draft value (Use default) gives a reset entry', () => {
    const st = state();
    delete st.draft.ship.bump;
    assert.deepEqual(sv.saveBody('/repo', sections, st).changes,
      [{ section: 'ship', field: 'bump', op: 'reset' }]);
  });

  test('compares arrays and objects by value', () => {
    const secs = [{ id: 'ship', configFile: '.sdlc-v2/local.toml', fields: [{ name: 'steps' }] }];
    const st = { base: { ship: { steps: ['a', 'b'] } }, draft: { ship: { steps: ['a', 'b'] } }, target: { ship: 'user' } };
    assert.deepEqual(sv.saveBody('/r', secs, st).changes, []);
    st.draft.ship.steps = ['a'];
    assert.deepEqual(sv.saveBody('/r', secs, st).changes,
      [{ section: 'ship', field: 'steps', op: 'set', value: ['a'] }]);
  });

  test('a guardrail change adds the guardrails key', () => {
    const st = state();
    st.grBase = { 'plan-guardrails': [{ id: 'old', severity: 'error', description: 'd' }] };
    st.gr = { 'plan-guardrails': [{ id: 'old', severity: 'error', description: 'd', isNew: false, removed: true }] };
    assert.deepEqual(sv.saveBody('/repo', sections, st), {
      repo: '/repo',
      changes: [],
      targets: {},
      guardrails: { 'plan-guardrails': [{ id: 'old', op: 'remove' }] },
    });
  });

  test('no guardrail change gives no guardrails key', () => {
    const st = state();
    st.grBase = { 'plan-guardrails': [{ id: 'a', severity: 'error', description: 'd' }] };
    st.gr = { 'plan-guardrails': [{ id: 'a', severity: 'error', description: 'd', isNew: false, removed: false }] };
    assert.equal('guardrails' in sv.saveBody('/repo', sections, st), false);
  });

  test('a body with form changes only is byte-equal to the body with no guardrail state', () => {
    const before = state();
    before.draft.ship.bump = 'minor';
    const after = state();
    after.draft.ship.bump = 'minor';
    after.grBase = { 'plan-guardrails': [{ id: 'a', severity: 'error', description: 'd' }] };
    after.gr = { 'plan-guardrails': [{ id: 'a', severity: 'error', description: 'd', isNew: false, removed: false }] };
    assert.equal(JSON.stringify(sv.saveBody('/repo', sections, after)),
      JSON.stringify({ repo: '/repo', changes: [{ section: 'ship', field: 'bump', op: 'set', value: 'minor' }],
        targets: { ship: 'project' } }));
    assert.equal(JSON.stringify(sv.saveBody('/repo', sections, after)),
      JSON.stringify(sv.saveBody('/repo', sections, before)));
  });

  test('a config.toml section gets no target', () => {
    const st = state();
    st.draft.version['tag.prefix'] = 'rel-';
    st.target.version = 'project';
    const body = sv.saveBody('/repo', sections, st);
    assert.equal(body.changes.length, 1);
    assert.deepEqual(body.targets, {});
  });
});

describe('saveRequest / createRequest', () => {
  const sections = [{ id: 'ship', configFile: '.sdlc-v2/local.toml', fields: [{ name: 'bump' }] }];
  const st = { base: { ship: { bump: 'patch' } }, draft: { ship: { bump: 'minor' } }, target: { ship: 'user' } };

  test('saveRequest returns null when the token or repo is empty', () => {
    assert.equal(sv.saveRequest('', '/r', sections, st), null);
    assert.equal(sv.saveRequest('t', '', sections, st), null);
  });

  test('saveRequest returns a JSON POST with the token header', () => {
    const req = sv.saveRequest('tok', '/r', sections, st);
    assert.equal(req.method, 'POST');
    assert.equal(req.url, '/api/setup-save');
    assert.deepEqual(req.headers, { 'X-Sdlc-Token': 'tok', 'Content-Type': 'application/json' });
    assert.deepEqual(JSON.parse(req.body), {
      repo: '/r',
      changes: [{ section: 'ship', field: 'bump', op: 'set', value: 'minor' }],
      targets: { ship: 'user' },
    });
  });

  test('createRequest returns null when the token or repo is empty', () => {
    assert.equal(sv.createRequest('', '/r'), null);
    assert.equal(sv.createRequest('t', ''), null);
  });

  test('createRequest returns a JSON POST with the token header', () => {
    assert.deepEqual(sv.createRequest('tok', '/r'), {
      method: 'POST',
      url: '/api/setup-create',
      headers: { 'X-Sdlc-Token': 'tok', 'Content-Type': 'application/json' },
      body: '{"repo":"/r"}',
    });
  });
});

describe('answerText', () => {
  test('an error object gives the message and the suggestion', () => {
    assert.equal(sv.answerText(409, { error: { message: 'Bad state.', suggestion: 'Reload.' } }), 'Bad state. Reload.');
  });

  test('an error object with no suggestion gives the message only', () => {
    assert.equal(sv.answerText(500, { error: { message: 'Boom.', suggestion: '' } }), 'Boom.');
    assert.equal(sv.answerText(500, { error: { message: 'Boom.' } }), 'Boom.');
  });

  test('an errors list gives the entries with a space between them', () => {
    assert.equal(sv.answerText(200, { ok: false, errors: ['one.', 'two.'] }), 'one. two.');
  });

  test('a null body gives the no-answer text', () => {
    assert.equal(sv.answerText(0, null), 'The dashboard server gave no answer (HTTP 0).');
    assert.equal(sv.answerText(502, null), 'The dashboard server gave no answer (HTTP 502).');
  });

  test('another body gives the unreadable text', () => {
    assert.equal(sv.answerText(200, { ok: true }),
      'The dashboard server gave an answer that the page cannot read (HTTP 200).');
    assert.equal(sv.answerText(200, { errors: [] }),
      'The dashboard server gave an answer that the page cannot read (HTTP 200).');
  });
});

describe('saveNote', () => {
  test('200 with ok true', () => {
    assert.deepEqual(sv.saveNote(200, { ok: true, written: [], failed: [] }, 1),
      { note: 'Saved 1 change.', noteState: 'set', errors: [], reload: true });
    assert.equal(sv.saveNote(200, { ok: true }, 3).note, 'Saved 3 changes.');
  });

  test('200 with ok false lists each failed section', () => {
    const out = sv.saveNote(200, {
      ok: false,
      written: [{ section: 'ship', file: '/r/.sdlc-v2/local.toml' }],
      failed: [
        { section: 'version', file: '/r/.sdlc-v2/config.toml', message: 'disk full' },
        { section: 'jira', file: '/r/.sdlc-v2/config.toml', message: 'locked' },
      ],
    }, 3);
    assert.deepEqual(out, {
      note: 'Saved 1 of 3 sections. Not saved: version (/r/.sdlc-v2/config.toml). disk full'
        + ' Not saved: jira (/r/.sdlc-v2/config.toml). locked',
      noteState: 'error',
      errors: [],
      reload: true,
    });
  });

  test('200 with ok false adds the suggestion of a failed section and next', () => {
    const out = sv.saveNote(200, {
      ok: false,
      written: [],
      failed: [
        { section: 'ship', file: '/r/.sdlc-v2/local.toml', message: 'would drop comments', suggestion: 'Edit it by hand.' },
      ],
      next: 'Edit section ship in .sdlc-v2/local.toml by hand, then run setup again for that section.',
    }, 1);
    assert.equal(out.note, 'Saved 0 of 1 sections. Not saved: ship (/r/.sdlc-v2/local.toml). would drop comments'
      + ' Edit it by hand. Edit section ship in .sdlc-v2/local.toml by hand, then run setup again for that section.');
  });

  test('400 SETUP_INVALID gives the field errors', () => {
    const fields = [{ section: 'ship', field: 'bump', path: 'ship.bump', message: 'bad' }];
    assert.deepEqual(sv.saveNote(400, { error: { code: 'SETUP_INVALID', fields } }, 2), {
      note: 'Not saved. The config check refused 1 field. No file changed.',
      noteState: 'error',
      errors: fields,
      reload: false,
    });
    assert.equal(sv.saveNote(400, { error: { code: 'SETUP_INVALID', fields: [{}, {}] } }, 2).note,
      'Not saved. The config check refused 2 fields. No file changed.');
  });

  test('413 BODY_TOO_LARGE', () => {
    assert.deepEqual(sv.saveNote(413, { error: { code: 'BODY_TOO_LARGE' } }, 9), {
      note: 'Not saved. The save is larger than the server accepts. Save fewer fields or guardrails at one time.',
      noteState: 'error',
      errors: [],
      reload: false,
    });
  });

  test('any other status gives Not saved and the answer text', () => {
    assert.deepEqual(sv.saveNote(409, { error: { code: 'SETUP_NOT_READY', message: 'State is no-config.', suggestion: 'Create the config.' } }, 1), {
      note: 'Not saved. State is no-config. Create the config.',
      noteState: 'error',
      errors: [],
      reload: false,
    });
    assert.equal(sv.saveNote(0, null, 1).note, 'Not saved. The dashboard server gave no answer (HTTP 0).');
    assert.equal(sv.saveNote(400, { error: { code: 'BAD_REQUEST', message: 'repo is required.' } }, 1).note,
      'Not saved. repo is required.');
  });
});
