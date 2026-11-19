/**
 * The Setup tab of the sdlc dashboard page: one form for the plugin config of
 * one repo. The form is built from the field descriptors, so a new config
 * option gets a field with no change here.
 *
 * The field list, the config values, and the state of the delegated sections
 * come from the dashboard API. setup-view.js converts the API answers to the
 * shape that this file reads.
 *
 * Text goes into the page through textContent only, never innerHTML.
 */
(function () {
  'use strict';

  var doc = document;
  var data = { sections: [], values: {}, delegated: {} };

  var REPO_KEY = 'sdlc-dashboard-setup-repo';
  // The effect of each option of a field, by '<section id>/<field name>'.
  var help = {};
  var sv = window.sdlcSetupView;
  var deps = null;
  var CLIP = 90;

  var TARGETS = [
    { id: 'project', label: 'This project', file: '.sdlc-v2/local.toml' },
    { id: 'user', label: 'All my projects', file: '~/.sdlc/local.toml' },
  ];
  var SOURCE_FILE = { config: '.sdlc-v2/config.toml', project: '.sdlc-v2/local.toml', user: '~/.sdlc/local.toml' };
  var FILE_FILTERS = [
    { id: 'any', label: 'Any file', title: 'Show the fields of every file' },
    { id: 'config', label: 'config.toml', title: '.sdlc-v2/config.toml · team file, in git' },
    { id: 'project', label: 'local.toml', title: '.sdlc-v2/local.toml · this project, not in git' },
    { id: 'user', label: '~/.sdlc/local.toml', title: '~/.sdlc/local.toml · all my projects' },
    { id: 'default', label: 'Default', title: 'No value in any file. The default applies.' },
    { id: 'other', label: 'Other files', title: 'Files outside the config: review dimensions, templates, openspec/config.yaml' },
  ];

  var RUNNING = ['running', 'in progress', 'in_progress'];

  var formSections = data.sections.filter(function (sec) {
    return !sec.delegatedTo;
  });
  // The guardrail sections: the form lists and edits each guardrail.
  var GR_IDS = ['plan-guardrails', 'execution-guardrails'];
  var grSections = data.sections.filter(function (sec) {
    return GR_IDS.indexOf(sec.id) !== -1;
  });
  var delegSections = data.sections.filter(function (sec) {
    return !!sec.delegatedTo && GR_IDS.indexOf(sec.id) === -1;
  });
  var GR_KEBAB = /^[a-z][a-z0-9]*(-[a-z0-9]+)*$/;
  var GR_MAX_BYTES = 1024;

  var ui = {
    panel: null,
    repos: [],
    root: null,
    sig: '', // the repo list of the last build
    filter: '',
    show: 'all', // 'all' | 'changed' | 'unset'
    file: 'any', // 'any' | 'config' | 'project' | 'user': the file filter of the bar
    note: '', // the result line of the save bar
    noteState: '',
    refs: null,
    load: '', // '' | 'loading' | 'error' | the state of GET /api/setup-state
    error: '', // the text of the error notice
    configError: '', // the configError of the state answer
    req: 0, // the number of the last state request
    fields: null, // the promise of the field list
    grOpen: {}, // the open guardrail rows, by '<section id>#<uid>'
    grSev: {}, // the severity filter of each guardrail section: 'all' | 'error' | 'warning'
    grExpanded: {}, // true for a guardrail section with an open list. Lists start closed.
  };
  // The form state of each repo, by root. A repo switch keeps unsaved changes.
  var byRoot = Object.create(null);

  // --- Small helpers -----------------------------------------------------------

  function h(tag, cls, text) {
    var node = doc.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function add(parent) {
    for (var i = 1; i < arguments.length; i++) {
      if (arguments[i]) parent.appendChild(arguments[i]);
    }
    return parent;
  }

  function button(cls, text) {
    var node = h('button', cls, text);
    node.type = 'button';
    return node;
  }

  function same(a, b) {
    return JSON.stringify(a) === JSON.stringify(b);
  }

  function clone(value) {
    return value === undefined ? undefined : JSON.parse(JSON.stringify(value));
  }

  function storage() {
    try {
      return window.localStorage;
    } catch (e) {
      return null;
    }
  }


  function isLocal(sec) {
    return /local\.toml$/.test(sec.configFile);
  }

  function keyPath(sec, field) {
    return sec.configPath + '.' + field.name;
  }

  function fieldKey(sec, field) {
    return sec.id + '/' + field.name;
  }

  // The effect of one option of a field: { text, before, after }, or null
  // when the data has none.
  function optionHelp(sec, field, opt) {
    var entry = (help[fieldKey(sec, field)] || {})[opt];
    if (!entry) return null;
    return typeof entry === 'string' ? { text: entry } : entry;
  }

  function clip(text) {
    return text.length > CLIP ? text.slice(0, CLIP) + '…' : text;
  }

  // The text of one value in a meta line or in the review dialog.
  function show(value) {
    if (value === undefined || value === null) return 'not set';
    if (value === true) return 'on';
    if (value === false) return 'off';
    if (Array.isArray(value)) return value.length ? clip(value.join(', ')) : 'none';
    if (value === '') return 'empty text';
    return clip(String(value));
  }

  function hasDefault(field) {
    return field.default !== null && field.default !== undefined && field.default !== '';
  }

  function plural(n, word) {
    return n + ' ' + word + (n === 1 ? '' : 's');
  }

  // --- State -------------------------------------------------------------------

  // A step set is a table of step -> bool in the file. The form holds the
  // list of the steps that are on, in run order.
  function normalize(field, value) {
    if (field.type === 'flag-set' && value && typeof value === 'object' && !Array.isArray(value)) {
      return (field.options || []).filter(function (opt) {
        return value[opt] === true;
      });
    }
    return value;
  }

  function freshState(withValues) {
    var st = { base: {}, draft: {}, source: {}, baseTarget: {}, target: {}, errors: {} };
    formSections.forEach(function (sec) {
      var values = withValues ? data.values[sec.id] || {} : {};
      var seen = {};
      st.base[sec.id] = {};
      st.source[sec.id] = {};
      sec.fields.forEach(function (field) {
        var entry = values[field.name];
        if (!entry) return;
        st.base[sec.id][field.name] = normalize(field, entry.value);
        st.source[sec.id][field.name] = entry.source;
        seen[entry.source] = true;
      });
      st.draft[sec.id] = clone(st.base[sec.id]);
      if (isLocal(sec)) {
        var target = seen.project ? 'project' : seen.user ? 'user' : sec.defaultTarget || 'project';
        st.baseTarget[sec.id] = target;
        st.target[sec.id] = target;
      }
    });
    // Guardrails: base is the list on disk, draft is the edited list. A draft
    // entry has { id, severity, description, isNew, removed }.
    st.grBase = {};
    st.gr = {};
    grSections.forEach(function (sec) {
      st.grBase[sec.id] = withValues ? grLoad(sec) : [];
      st.gr[sec.id] = grFresh(st.grBase[sec.id]);
    });
    return st;
  }

  // The guardrail list of a section from the state field guardrails
  // (key: the config path, plan.guardrails or execute.guardrails).
  function grLoad(sec) {
    var all = data.guardrails || {};
    return (all[sec.configPath] || []).map(function (g) {
      return { id: g.id, severity: g.severity, description: g.description };
    });
  }

  function grFresh(base) {
    return base.map(function (g) {
      return { id: g.id, severity: g.severity, description: g.description, isNew: false, removed: false };
    });
  }

  function cur() {
    if (!byRoot[ui.root]) byRoot[ui.root] = freshState(true);
    return byRoot[ui.root];
  }

  function repoOf(root) {
    for (var i = 0; i < ui.repos.length; i++) {
      if (ui.repos[i].root === root) return ui.repos[i];
    }
    return null;
  }

  function isRunning() {
    var repo = repoOf(ui.root);
    return !!repo && (repo.pipelines || []).some(function (p) {
      return p && RUNNING.indexOf(p.status) !== -1;
    });
  }

  // A stalled pipeline can continue. It then reads the saved config.
  function isStalled() {
    var repo = repoOf(ui.root);
    return !isRunning() && !!repo && (repo.pipelines || []).some(function (p) {
      return p && p.status === 'stalled';
    });
  }

  // The steps that are on in the ship section, saved or not.
  function activeSteps() {
    var sec = null;
    var field = null;
    formSections.forEach(function (s) {
      s.fields.forEach(function (f) {
        if (s.id === 'ship' && f.name === 'steps') {
          sec = s;
          field = f;
        }
      });
    });
    if (!field) return null;
    var value = cur().draft[sec.id][field.name];
    return value === undefined ? field.default || [] : value;
  }

  function validate(field, value) {
    if (field.type !== 'number' || typeof value !== 'number') return '';
    if (value !== value || Math.floor(value) !== value) return 'Enter a whole number.';
    if (typeof field.min === 'number' && value < field.min) return 'The minimum is ' + field.min + '.';
    if (typeof field.max === 'number' && value > field.max) return 'The maximum is ' + field.max + '.';
    return '';
  }

  // The file a section writes to.
  function fileOf(sec, target) {
    if (!isLocal(sec)) return sec.configFile;
    return target === 'user' ? TARGETS[1].file : TARGETS[0].file;
  }

  // Every unsaved change: one entry for each changed value.
  function listChanges() {
    var st = cur();
    var out = [];
    formSections.forEach(function (sec) {
      var file = fileOf(sec, st.target[sec.id]);
      sec.fields.forEach(function (field) {
        var from = st.base[sec.id][field.name];
        var to = st.draft[sec.id][field.name];
        if (same(from, to)) return;
        out.push({ sec: sec, field: field, file: file, key: keyPath(sec, field), from: show(from), to: show(to) });
      });
    });
    grSections.forEach(function (sec) {
      st.gr[sec.id].forEach(function (g) {
        var state = grState(sec, g);
        if (state === 'set') return;
        var base = grBase(sec, g);
        var key = sec.configPath + '.' + (g.id || '(no id)');
        var to = state === 'removed' ? 'removed' : state === 'new' ? 'new · ' + g.severity : grEditText(base, g);
        out.push({ sec: sec, guardrail: g, file: sec.configFile, key: key, from: base ? base.severity : 'not set', to: to });
      });
    });
    return out;
  }

  // --- Guardrails ---------------------------------------------------------------

  // The disk entry of a guardrail, or null for a new one.
  function grBase(sec, g) {
    return g.isNew ? null : grBaseOf(sec, g.id);
  }

  function grBaseOf(sec, id) {
    var list = cur().grBase[sec.id];
    for (var i = 0; i < list.length; i++) {
      if (list[i].id === id) return list[i];
    }
    return null;
  }

  // 'set' | 'changed' | 'new' | 'removed'
  function grState(sec, g) {
    if (g.isNew) return g.removed ? 'set' : 'new';
    if (g.removed) return 'removed';
    var base = grBaseOf(sec, g.id);
    return base && base.severity === g.severity && base.description === g.description ? 'set' : 'changed';
  }

  function grEditText(base, g) {
    var parts = [];
    if (base.severity !== g.severity) parts.push(g.severity);
    if (base.description !== g.description) parts.push('description changed');
    return parts.join(' · ');
  }

  function grBytes(text) {
    return new TextEncoder().encode(text || '').length;
  }

  // The error of one guardrail, or ''. The rules follow the guardrails
  // validator: kebab-case unique id, severity error or warning, a description
  // of 1 to 1024 bytes.
  function grError(sec, g) {
    if (g.removed) return '';
    if (g.isNew) {
      if (!g.id) return 'Add an id, e.g. no-ci-bypass.';
      if (!GR_KEBAB.test(g.id)) return 'Use lowercase words joined by single hyphens, e.g. no-ci-bypass.';
      var dup = cur().gr[sec.id].some(function (o) {
        return o !== g && !o.removed && o.id === g.id;
      });
      if (dup) return 'Another guardrail of this list has this id. Pick a new id.';
    }
    if (!g.description.trim()) return 'Add a description that states what the guardrail enforces.';
    var bytes = grBytes(g.description);
    if (bytes > GR_MAX_BYTES) {
      return 'The description has ' + bytes + ' bytes. The maximum is ' + GR_MAX_BYTES + '. Shorten it, or split it into two guardrails.';
    }
    return '';
  }

  // grRowError is the text that the row shows: the local check first, then the server error.
  function grRowError(sec, g) {
    return grError(sec, g) || cur().errors[sec.id + '/' + g.id] || '';
  }

  function grErrorCount() {
    var n = 0;
    grSections.forEach(function (sec) {
      cur().gr[sec.id].forEach(function (g) {
        if (grError(sec, g)) n++;
      });
    });
    return n;
  }

  function grKey(sec, g) {
    return sec.id + '#' + g.uid;
  }

  var grUid = 0;
  function grUidOf(g) {
    if (!g.uid) g.uid = ++grUid;
    return g.uid;
  }

  // One guardrail row. Collapsed: severity, id, state, and two lines of the
  // description. Open: the severity choice, the description editor, the byte
  // count, and the id for a new guardrail.
  function grRow(sec, g) {
    grUidOf(g);
    var key = grKey(sec, g);
    var open = !!ui.grOpen[key];
    var state = grState(sec, g);
    var base = grBase(sec, g);
    var row = h('li', 'setup-gr-row');
    var head = h('div', 'setup-gr-head');
    var sev = h('span', 'setup-gr-sev', g.severity.toUpperCase());
    var idNode = h('code', 'setup-gr-id', g.id || 'new guardrail');
    var tag = h('span', 'setup-gr-tag', state === 'set' ? '' : state.toUpperCase());
    var toggle = grIconBtn(open ? 'close' : 'edit', open ? 'Close the editor' : 'Edit ' + (g.id || 'the new guardrail'));
    var remove = grIconBtn(g.removed ? 'undo' : 'trash', g.removed ? 'Restore ' + g.id : 'Remove ' + (g.id || 'the new guardrail'));
    var desc = h('p', 'setup-gr-desc', g.description || 'No description yet.');

    row.setAttribute('data-state', state);
    row.setAttribute('data-sev', g.severity);
    row.setAttribute('data-open', String(open));
    tag.hidden = state === 'set';
    toggle.setAttribute('aria-expanded', String(open));
    toggle.addEventListener('click', function () {
      ui.grOpen[key] = !open;
      grRedrawRow(sec, g, toggle);
    });
    remove.addEventListener('click', function () {
      if (g.isNew && !g.removed) {
        // A new guardrail that the user removes goes away at once.
        var list = cur().gr[sec.id];
        list.splice(list.indexOf(g), 1);
        grRedraw(sec);
        refreshChrome();
        return;
      }
      g.removed = !g.removed;
      if (g.removed) ui.grOpen[key] = false;
      grRedrawRow(sec, g);
      refreshChrome();
    });
    desc.addEventListener('click', function () {
      if (open || g.removed) return;
      ui.grOpen[key] = true;
      grRedrawRow(sec, g);
    });
    // An open row puts the severity choice in the place of the badge, so the
    // value and its control are in one place.
    add(head, open && !g.removed ? grSevSeg(sec, g) : sev, idNode, tag, h('span', 'spacer'), g.removed ? null : toggle, remove);
    add(row, head);

    if (!open) {
      row.appendChild(desc);
    } else {
      row.appendChild(grEditor(sec, g, base, row));
    }
    var err = grRowError(sec, g);
    var errNode = h('p', 'setup-f-error', err);
    errNode.setAttribute('role', 'alert');
    errNode.hidden = !err;
    row.appendChild(errNode);
    row.classList.toggle('has-error', !!err);
    ui.refs.gr[key] = { row: row, sec: sec, g: g, err: errNode };
    return row;
  }

  // An icon button of a guardrail row. The icon is a CSS mask (setup.css,
  // --icon-<name>), so it takes the text color. The label is for screen
  // readers and the tooltip.
  function grIconBtn(name, label) {
    var b = button('setup-gr-icon setup-gr-icon-' + name);
    b.setAttribute('aria-label', label);
    b.title = label;
    return b;
  }

  var GR_SEV_HELP = {
    error: 'error: a plan or a wave that breaks it fails.',
    warning: 'warning: a break gives a warning only.',
  };

  // The severity choice of an open row. It sits in the row head.
  function grSevSeg(sec, g) {
    var seg = h('div', 'setup-seg setup-gr-sevseg');
    seg.setAttribute('role', 'radiogroup');
    seg.setAttribute('aria-label', 'Severity of ' + (g.id || 'the new guardrail'));
    var buttons = ['error', 'warning'].map(function (s) {
      var b = button('', s.toUpperCase());
      b.setAttribute('role', 'radio');
      b.setAttribute('data-sev', s);
      b.setAttribute('aria-checked', String(g.severity === s));
      b.title = GR_SEV_HELP[s];
      b.addEventListener('click', function () {
        if (g.severity === s) return;
        g.severity = s;
        grRedrawRow(sec, g, null, s);
        refreshChrome();
      });
      seg.appendChild(b);
      return b;
    });
    radioKeys(seg, buttons, function (b) { b.click(); });
    rove(buttons);
    return seg;
  }

  function grEditor(sec, g, base, row) {
    var box = h('div', 'setup-gr-edit');
    if (g.isNew) {
      var idLabel = h('label', 'setup-gr-label', 'ID');
      var idInput = h('input', 'setup-input setup-gr-idin');
      idInput.value = g.id;
      idInput.placeholder = 'no-ci-bypass';
      idInput.spellcheck = false;
      idInput.addEventListener('input', function () {
        g.id = idInput.value.trim();
        row.querySelector('.setup-gr-id').textContent = g.id || 'new guardrail';
        grRefreshRowMeta(sec, g);
      });
      add(box, add(idLabel, idInput));
    }
    if (base && base.severity === 'error' && g.severity === 'warning') {
      box.appendChild(h('p', 'setup-gr-lower', 'This lowers the severity from error to warning. /sdlc:harden never lowers a severity.'));
    }

    var area = h('textarea', 'setup-gr-text');
    area.value = g.description;
    area.rows = Math.min(10, Math.max(3, Math.ceil(g.description.length / 90)));
    area.setAttribute('aria-label', 'Description of ' + (g.id || 'the new guardrail'));
    var meta = h('div', 'setup-gr-meta');
    area.addEventListener('input', function () {
      g.description = area.value;
      grRefreshRowMeta(sec, g);
    });
    box.appendChild(add(h('label', 'setup-gr-label', 'DESCRIPTION'), area));
    box.appendChild(meta);
    ui.grMeta = ui.grMeta || {};
    ui.grMeta[grKey(sec, g)] = meta;
    grFillMeta(sec, g, base, meta);
    return box;
  }

  function grFillMeta(sec, g, base, meta) {
    var bytes = grBytes(g.description);
    var count = h('span', 'setup-gr-bytes', bytes + ' / ' + GR_MAX_BYTES + ' bytes');
    count.setAttribute('data-over', String(bytes > GR_MAX_BYTES));
    var parts = [count];
    if (base && (base.description !== g.description || base.severity !== g.severity)) {
      var undo = button('setup-link setup-gr-undo', 'Undo changes');
      undo.title = 'Puts back the saved severity and description, and closes the editor';
      undo.addEventListener('click', function () {
        g.description = base.description;
        g.severity = base.severity;
        // The row has nothing left to edit, so the editor closes.
        ui.grOpen[grKey(sec, g)] = false;
        grRedrawRow(sec, g, true);
        refreshChrome();
      });
      parts.push(undo);
    }
    meta.replaceChildren.apply(meta, parts);
  }

  // Updates the state tag, the byte count and the error of a row while the
  // user types, with no rebuild, so the text box keeps the focus.
  function grRefreshRowMeta(sec, g) {
    var key = grKey(sec, g);
    var ref = ui.refs.gr[key];
    if (!ref) return;
    var state = grState(sec, g);
    var tag = ref.row.querySelector('.setup-gr-tag');
    tag.textContent = state === 'set' ? '' : state.toUpperCase();
    tag.hidden = state === 'set';
    ref.row.setAttribute('data-state', state);
    var err = grRowError(sec, g);
    ref.err.textContent = err;
    ref.err.hidden = !err;
    ref.row.classList.toggle('has-error', !!err);
    if (ui.grMeta && ui.grMeta[key]) grFillMeta(sec, g, grBase(sec, g), ui.grMeta[key]);
    grRefreshHead(sec);
    refreshChrome();
  }

  function grRedrawRow(sec, g, focusLike, sevFocus) {
    var ref = ui.refs.gr[grKey(sec, g)];
    if (!ref) return grRedraw(sec);
    var next = grRow(sec, g);
    ref.row.replaceWith(next);
    if (sevFocus) {
      var b = next.querySelector('.setup-seg [aria-checked="true"]');
      if (b) b.focus();
    } else if (focusLike) {
      var t = next.querySelector('.setup-gr-head .setup-gr-icon');
      if (t) t.focus();
    }
    grRefreshHead(sec);
    applyVisibility();
  }

  function grRedraw(sec) {
    var sref = ui.refs.grSec[sec.id];
    sref.list.replaceChildren.apply(
      sref.list,
      cur().gr[sec.id].map(function (g) {
        return grRow(sec, g);
      })
    );
    grRefreshHead(sec);
    applyVisibility();
  }

  // The severity chips and the counts of a guardrail section.
  function grRefreshHead(sec) {
    var sref = ui.refs.grSec[sec.id];
    if (!sref) return;
    var list = cur().gr[sec.id].filter(function (g) {
      return !g.removed;
    });
    var n = { all: list.length, error: 0, warning: 0 };
    list.forEach(function (g) {
      n[g.severity]++;
    });
    Object.keys(sref.sevChips).forEach(function (s) {
      sref.sevChips[s].count.textContent = String(n[s]);
      sref.sevChips[s].chip.setAttribute('aria-pressed', String((ui.grSev[sec.id] || 'all') === s));
    });
  }

  // The two ways to change the guardrails: in this form, or with the
  // assistant, which scans the code and proposes guardrails. Always visible.
  function grWays(sec) {
    var ways = h('div', 'setup-gr-ways');
    var form = h('div', 'setup-gr-way');
    var llm = h('div', 'setup-gr-way');
    var command = '/sdlc:setup --only ' + sec.id;
    var cmdRow = h('div', 'setup-cmd-row');
    var copy = button('setup-copy', 'Copy');
    copy.setAttribute('aria-label', 'Copy the command ' + command);
    copy.addEventListener('click', function () {
      var done = function (text) {
        copy.textContent = text;
        window.setTimeout(function () {
          copy.textContent = 'Copy';
        }, 1500);
      };
      if (!navigator.clipboard) return done('Not copied');
      navigator.clipboard.writeText(command).then(
        function () {
          done('Copied');
        },
        function () {
          done('Not copied');
        }
      );
    });
    add(cmdRow, h('code', 'setup-cmd', command), copy);
    add(
      form,
      h('p', 'setup-gr-way-title', 'IN THIS FORM'),
      h('p', 'setup-gr-way-text', 'Add, edit or remove each guardrail yourself. The save writes config.toml.')
    );
    add(
      llm,
      h('p', 'setup-gr-way-title', 'WITH THE ASSISTANT'),
      h('p', 'setup-gr-way-text', 'The assistant scans the code and proposes guardrails for you to accept. Run the command in Claude Code in the repo.'),
      cmdRow
    );
    return add(ways, form, llm);
  }

  function buildGr(sec) {
    var card = h('section', 'setup-sec setup-gr list-panel');
    var tools = h('div', 'setup-gr-tools');
    var chips = h('span', 'chips');
    var shown = h('span', 'setup-gr-shown');
    var addBtn = button('setup-btn', '+ Add guardrail');
    var toggle = button('setup-btn setup-gr-toggle');
    toggle.setAttribute('aria-controls', 'setup-gr-list-' + sec.id);
    toggle.addEventListener('click', function () {
      ui.grExpanded[sec.id] = !ui.grExpanded[sec.id];
      applyVisibility();
      if (!ui.grExpanded[sec.id]) card.scrollIntoView({ block: 'start', behavior: 'auto' });
    });
    var list = h('ol', 'setup-gr-list');
    var sevChips = {};

    card.id = 'setup-sec-' + sec.id;
    [['all', 'All'], ['error', 'Error'], ['warning', 'Warning']].forEach(function (pair) {
      var chip = button('chip', pair[1] + ' ');
      var count = h('span', 'chip-n');
      chip.appendChild(count);
      chip.addEventListener('click', function () {
        ui.grSev[sec.id] = pair[0];
        ui.grExpanded[sec.id] = true;
        grRefreshHead(sec);
        applyVisibility();
      });
      sevChips[pair[0]] = { chip: chip, count: count };
      chips.appendChild(chip);
    });
    addBtn.addEventListener('click', function () {
      var g = { id: '', severity: 'error', description: '', isNew: true, removed: false };
      cur().gr[sec.id].unshift(g);
      grUidOf(g);
      ui.grOpen[grKey(sec, g)] = true;
      ui.grSev[sec.id] = 'all';
      ui.grExpanded[sec.id] = true;
      grRedraw(sec);
      refreshChrome();
      var input = list.querySelector('.setup-gr-idin');
      if (input) input.focus();
    });
    list.id = 'setup-gr-list-' + sec.id;
    add(tools, chips, h('span', 'spacer'), shown, toggle, addBtn);
    add(card, buildHead(sec, sec.configFile), h('p', 'setup-purpose', sec.purpose), grWays(sec), tools, list);
    ui.refs.grSec[sec.id] = { node: card, list: list, shown: shown, sevChips: sevChips, toggle: toggle };
    cur().gr[sec.id].forEach(function (g) {
      list.appendChild(grRow(sec, g));
    });
    grRefreshHead(sec);
    return card;
  }

  // Arrow keys, Home, and End move the choice of a group with one choice.
  function radioKeys(group, buttons, pick) {
    group.addEventListener('keydown', function (event) {
      var at = buttons.indexOf(doc.activeElement);
      var step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[event.key];
      var next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : step ? (at + step + buttons.length) % buttons.length : -1;
      if (at === -1 || next === -1) return;
      event.preventDefault();
      pick(buttons[next]);
      buttons[next].focus();
    });
  }

  // Only the checked button of a group with one choice is in the tab order.
  function rove(buttons) {
    var on = buttons.filter(function (b) {
      return b.getAttribute('aria-checked') === 'true';
    })[0] || buttons[0];
    buttons.forEach(function (b) {
      b.tabIndex = b === on ? 0 : -1;
    });
  }

  // --- Controls ------------------------------------------------------------------
  // Each control is { node, set(value, unset), focus() }. unset is true when
  // the file has no value and the control shows the default.

  // A switch, with the effect of on and of off below it.
  function switchControl(sec, field, id, onChange) {
    var node = h('div', 'setup-bool');
    var toggle = button('setup-switch');
    var text = h('span', 'setup-switch-text');
    var lines = h('div', 'setup-bool-help');
    var on = false;
    var rows = {};
    toggle.id = id;
    toggle.setAttribute('role', 'switch');
    add(toggle, h('span', 'setup-switch-track'), text);
    toggle.addEventListener('click', function () {
      onChange(!on);
    });
    ['on', 'off'].forEach(function (state) {
      var info = optionHelp(sec, field, state);
      if (!info) return;
      rows[state] = h('p');
      add(rows[state], h('b', '', state), doc.createTextNode(info.text));
      lines.appendChild(rows[state]);
    });
    add(node, toggle, lines.firstChild ? lines : null);
    return {
      node: node,
      set: function (value) {
        on = value === true;
        toggle.setAttribute('aria-checked', String(on));
        text.textContent = on ? 'on' : 'off';
        if (rows.on) rows.on.classList.toggle('current', on);
        if (rows.off) rows.off.classList.toggle('current', !on);
      },
      focus: function () {
        toggle.focus();
      },
    };
  }

  // A list of the options of a field. Each row shows the option and its
  // effect on the plugin. mode is 'one' (one option), 'many' (a set of
  // options), or 'steps' (a set of pipeline steps, in run order).
  function choiceControl(sec, field, id, onChange, mode) {
    var node = h('div', 'setup-choices ' + mode);
    var options = field.options || [];
    var picked = [];
    node.setAttribute('role', mode === 'one' ? 'radiogroup' : 'group');
    node.setAttribute('aria-labelledby', id + '-label');
    var buttons = options.map(function (opt, index) {
      var row = button('setup-choice');
      var info = optionHelp(sec, field, opt);
      row.setAttribute('role', mode === 'one' ? 'radio' : 'checkbox');
      row.setAttribute('data-opt', opt);
      add(
        row,
        h('span', 'setup-choice-mark', mode === 'steps' ? String(index + 1) : ''),
        h('span', 'setup-choice-name', opt),
        h('span', 'setup-choice-text', info ? info.text : '')
      );
      if (info && info.after) {
        var sample = h('span', 'setup-choice-ex');
        add(sample, h('b', '', 'Before'), h('span', '', info.before), h('b', '', 'After'), h('span', '', info.after));
        row.appendChild(sample);
      }
      row.addEventListener('click', function () {
        if (mode === 'one') {
          onChange(opt);
          return;
        }
        onChange(
          options.filter(function (o) {
            return o === opt ? picked.indexOf(o) === -1 : picked.indexOf(o) !== -1;
          })
        );
      });
      node.appendChild(row);
      return row;
    });
    if (mode === 'one') radioKeys(node, buttons, function (row) { onChange(row.getAttribute('data-opt')); });
    return {
      node: node,
      set: function (value) {
        picked = mode === 'one' ? [value] : Array.isArray(value) ? value : [];
        buttons.forEach(function (row) {
          row.setAttribute('aria-checked', String(picked.indexOf(row.getAttribute('data-opt')) !== -1));
        });
        if (mode === 'one') rove(buttons);
      },
      focus: function () {
        var on = node.querySelector('[aria-checked="true"]') || buttons[0];
        if (on) on.focus();
      },
    };
  }

  function textControl(field, id, onChange) {
    var input = h('input', 'setup-input');
    input.type = 'text';
    input.id = id;
    input.spellcheck = false;
    input.autocomplete = 'off';
    input.placeholder = hasDefault(field) ? String(field.default) : 'not set';
    input.addEventListener('input', function () {
      onChange(input.value);
    });
    return {
      node: input,
      set: function (value, unset) {
        var want = unset ? '' : String(value);
        if (input.value !== want) input.value = want;
      },
      focus: function () {
        input.focus();
      },
    };
  }

  function numberControl(field, id, onChange) {
    var input = h('input', 'setup-input');
    input.type = 'number';
    input.id = id;
    input.step = '1';
    if (typeof field.min === 'number') input.min = String(field.min);
    if (typeof field.max === 'number') input.max = String(field.max);
    input.placeholder = hasDefault(field) ? String(field.default) : 'not set';
    input.addEventListener('input', function () {
      onChange(input.value === '' ? undefined : Number(input.value));
    });
    return {
      node: input,
      set: function (value, unset) {
        var want = unset || value === undefined ? '' : String(value);
        if (input.value === want) return;
        // The typed text stays when it is the same number in another form.
        if (want !== '' && input.value !== '' && Number(input.value) === value) return;
        input.value = want;
      },
      focus: function () {
        input.focus();
      },
    };
  }

  // A list of free text items: one row for each item.
  function listControl(field, id, onChange) {
    var node = h('div', 'setup-list');
    var rows = h('div', 'setup-list-rows');
    var addBtn = button('setup-list-add', 'Add item');
    var items = [];
    node.setAttribute('role', 'group');
    node.setAttribute('aria-labelledby', id + '-label');

    function clean() {
      return items
        .map(function (item) {
          return item.trim();
        })
        .filter(Boolean);
    }

    function drawRows(focusIndex) {
      rows.replaceChildren();
      items.forEach(function (item, index) {
        var row = h('div', 'setup-list-row');
        var input = h('textarea', 'setup-input');
        var del = button('setup-list-del', '×');
        input.rows = 1;
        input.value = item;
        input.spellcheck = false;
        input.setAttribute('aria-label', field.label + ', item ' + (index + 1));
        del.setAttribute('aria-label', 'Remove item ' + (index + 1));
        input.addEventListener('input', function () {
          items[index] = input.value;
          onChange(clean());
        });
        input.addEventListener('keydown', function (event) {
          if (event.key !== 'Enter') return;
          event.preventDefault();
          items.splice(index + 1, 0, '');
          drawRows(index + 1);
        });
        del.addEventListener('click', function () {
          items.splice(index, 1);
          drawRows(Math.min(index, items.length - 1));
          onChange(clean());
        });
        add(rows, add(row, input, del));
        if (index === focusIndex) input.focus();
      });
    }

    addBtn.addEventListener('click', function () {
      items.push('');
      drawRows(items.length - 1);
    });
    add(node, rows, addBtn);
    return {
      node: node,
      set: function (value, unset) {
        var want = unset || !Array.isArray(value) ? [] : value;
        // Rows with no text stay while the user types in them.
        if (same(clean(), want)) return;
        items = clone(want);
        drawRows(-1);
      },
      focus: function () {
        addBtn.focus();
      },
    };
  }

  // The fields that show a list of options: 'one', 'many', or 'steps'.
  var CHOICE_MODE = { enum: 'one', 'multi-enum': 'many', 'multi-select': 'many', 'flag-set': 'steps' };

  function controlFor(sec, field, id, onChange) {
    if (CHOICE_MODE[field.type]) return choiceControl(sec, field, id, onChange, CHOICE_MODE[field.type]);
    switch (field.type) {
      case 'boolean':
        return switchControl(sec, field, id, onChange);
      case 'number':
        return numberControl(field, id, onChange);
      case 'list':
        return listControl(field, id, onChange);
      default:
        return textControl(field, id, onChange);
    }
  }

  // --- Fields --------------------------------------------------------------------

  function setValue(sec, field, value) {
    var st = cur();
    var key = fieldKey(sec, field);
    var base = st.base[sec.id][field.name];
    // An empty list in place of no value is no change.
    if (Array.isArray(value) && !value.length && base === undefined && field.type !== 'flag-set') value = undefined;
    if (value === undefined) delete st.draft[sec.id][field.name];
    else st.draft[sec.id][field.name] = value;
    delete st.errors[key];
    ui.note = '';
    refreshField(key);
    if (sec.id === 'ship' && field.name === 'steps') applyVisibility();
    refreshChrome();
  }

  function buildField(sec, field) {
    var key = fieldKey(sec, field);
    var id = 'setup-f-' + sec.id + '-' + field.name.replace(/\./g, '-');
    var choices = !!CHOICE_MODE[field.type];
    var wide = choices || field.type === 'list';
    var row = h('div', 'setup-field' + (wide ? ' wide' : ''));
    var text = h('div', 'setup-f-text');
    var label = h('label', 'setup-f-label', field.label);
    var ctl = h('div', 'setup-f-ctl');
    var box = h('div', 'setup-control');
    var meta = h('div', 'setup-f-meta');
    var error = h('p', 'setup-f-error');
    var control = controlFor(sec, field, id, function (value) {
      setValue(sec, field, value);
    });
    var optionText = Object.keys(help[key] || {})
      .map(function (opt) {
        return opt + ' ' + optionHelp(sec, field, opt).text;
      })
      .join(' ');

    label.id = id + '-label';
    if (control.node.id === id || control.node.querySelector('#' + id)) label.htmlFor = id;
    row.setAttribute('data-key', key);
    add(text, label, h('code', 'setup-f-key', keyPath(sec, field)), h('p', 'setup-f-desc', field.description));

    // A field with a list of options shows the effect of each option, so its
    // examples add nothing. Any other field shows its examples with no click.
    if (!optionText && (field.examples || []).length) {
      var examples = h('ul', 'setup-f-examples');
      field.examples.forEach(function (example) {
        examples.appendChild(h('li', '', example));
      });
      add(text, h('p', 'setup-f-examples-title', 'EXAMPLES'), examples);
    }
    if (field.details) {
      var more = button('setup-link setup-f-more', 'More detail');
      var moreBox = h('div', 'setup-f-more-box');
      more.setAttribute('aria-expanded', 'false');
      moreBox.hidden = true;
      moreBox.appendChild(h('p', '', field.details));
      more.addEventListener('click', function () {
        moreBox.hidden = !moreBox.hidden;
        more.setAttribute('aria-expanded', String(!moreBox.hidden));
      });
      add(text, more, moreBox);
    }

    error.hidden = true;
    error.setAttribute('role', 'alert');
    error.id = id + '-error';
    (control.node.id === id ? control.node : control.node.querySelector('#' + id) || control.node).setAttribute('aria-describedby', error.id);
    add(box, control.node);
    var over = h('div', 'setup-over');
    over.hidden = true;
    add(ctl, box, meta, over, error);
    add(row, text, ctl);

    ui.refs.fields[key] = {
      sec: sec,
      field: field,
      row: row,
      control: control,
      meta: meta,
      over: over,
      error: error,
      hay: (field.label + ' ' + keyPath(sec, field) + ' ' + field.description + ' ' + sec.id + ' ' + optionText).toLowerCase(),
    };
    return row;
  }

  function rangeText(field) {
    var min = typeof field.min === 'number';
    var max = typeof field.max === 'number';
    if (min && max) return 'Range: ' + field.min + ' to ' + field.max;
    if (min) return 'Minimum: ' + field.min;
    if (max) return 'Maximum: ' + field.max;
    return '';
  }

  // Draws the state of one field: the control, the meta line, and the error.
  function refreshField(key) {
    var ref = ui.refs.fields[key];
    if (!ref) return;
    var st = cur();
    var sec = ref.sec;
    var field = ref.field;
    var base = st.base[sec.id][field.name];
    var value = st.draft[sec.id][field.name];
    var unset = value === undefined;
    var changed = !same(base, value);
    var parts = [];

    ref.control.set(unset ? field.default : value, unset);
    ref.row.setAttribute('data-state', changed ? 'changed' : unset ? 'unset' : 'set');

    if (changed) {
      parts.push(h('span', 'was', 'Was: ' + show(base)));
      var undo = button('setup-link', 'Undo');
      undo.addEventListener('click', function () {
        setValue(sec, field, clone(base));
        ref.control.focus();
      });
      parts.push(undo);
    }
    if (unset) {
      parts.push(h('span', '', hasDefault(field) ? 'Not set. Default: ' + show(field.default) : 'Not set. No default.'));
    } else {
      if (hasDefault(field)) parts.push(h('span', '', 'Default: ' + show(field.default)));
      var source = st.source[sec.id][field.name];
      if (!changed && isLocal(sec) && source && source !== st.target[sec.id]) {
        parts.push(h('span', '', 'Value from ' + SOURCE_FILE[source]));
      }
      if (base !== undefined || !changed) {
        var clear = button('setup-link', 'Use default');
        clear.title = 'Removes the key from the file. The default applies.';
        clear.addEventListener('click', function () {
          setValue(sec, field, undefined);
          ref.control.focus();
        });
        parts.push(clear);
      }
    }
    if (rangeText(field)) parts.push(h('span', '', rangeText(field)));
    ref.meta.replaceChildren.apply(ref.meta, parts);

    // The values of other files that the shown value overrides.
    var overs = overridesOf(sec, field);
    var lines = Object.keys(overs).map(function (file) {
      var line = h('p', 'setup-over-line');
      var same_ = same(overs[file], value);
      var why = sv.overrideText(field.type, same_, SOURCE_FILE[valueFile(sec, field)]);
      line.setAttribute('data-file', file);
      line.classList.toggle('is-merged', !why.struck);
      add(
        line,
        h('code', '', SOURCE_FILE[file]),
        h('span', 'setup-over-val', show(overs[file])),
        h('span', 'setup-over-why', why.text)
      );
      return line;
    });
    ref.over.replaceChildren.apply(ref.over, lines);
    ref.over.hidden = !lines.length;

    var message = validate(field, value) || st.errors[key] || '';
    ref.error.textContent = message;
    ref.error.hidden = !message;
    ref.row.classList.toggle('has-error', !!message);
  }

  // --- Sections --------------------------------------------------------------------

  // Fields in one group share a key prefix (tag.enabled, tag.prefix) or a step
  // condition. A prefix with one field stays in the group with no name.
  function groupFields(sec) {
    var prefixCount = {};
    sec.fields.forEach(function (field) {
      var dot = field.name.indexOf('.');
      if (dot > 0) prefixCount[field.name.slice(0, dot)] = (prefixCount[field.name.slice(0, dot)] || 0) + 1;
    });
    var groups = [];
    sec.fields.forEach(function (field) {
      var dot = field.name.indexOf('.');
      var prefix = dot > 0 && prefixCount[field.name.slice(0, dot)] > 1 ? field.name.slice(0, dot) : '';
      var id = field.whenStep ? 'when:' + field.whenStep : prefix;
      var last = groups[groups.length - 1];
      if (!last || last.id !== id) {
        last = { id: id, when: field.whenStep || '', prefix: field.whenStep ? '' : prefix, fields: [] };
        groups.push(last);
      }
      last.fields.push(field);
    });
    return groups;
  }

  function fileNote(sec) {
    return isLocal(sec) ? 'personal file, not in git' : 'team file, in git';
  }

  function usedBy(sec) {
    var list = sec.consumedBy || [];
    if (list.length > 4) return 'read by ' + list.length + ' skills';
    return (
      'read by ' +
      list
        .map(function (name) {
          return '/' + name;
        })
        .join(' ')
    );
  }

  function buildHead(sec, fileText) {
    var head = h('header', 'setup-sec-head');
    var file = h('span', 'setup-file');
    var used = h('span', 'setup-used', usedBy(sec));
    add(file, h('code', '', fileText), doc.createTextNode(isLocal(sec) || !sec.delegatedTo ? ' · ' + fileNote(sec) : ''));
    used.title = (sec.consumedBy || []).join(', ');
    add(head, h('h3', 'setup-sec-name', sec.label), file);
    if (!sec.optional) head.appendChild(h('span', 'setup-tag', 'REQUIRED'));
    return add(head, used);
  }

  function buildTarget(sec) {
    var box = h('div', 'setup-target');
    var seg = h('div', 'setup-seg');
    var note = h('span', 'setup-target-note');
    seg.setAttribute('role', 'radiogroup');
    seg.setAttribute('aria-label', 'Save target of ' + sec.label);
    var buttons = TARGETS.map(function (target) {
      var b = button('', target.label);
      b.setAttribute('role', 'radio');
      b.setAttribute('data-target', target.id);
      b.addEventListener('click', function () {
        cur().target[sec.id] = target.id;
        ui.note = '';
        sec.fields.forEach(function (field) {
          refreshField(fieldKey(sec, field));
        });
        refreshChrome();
      });
      seg.appendChild(b);
      return b;
    });
    radioKeys(seg, buttons, function (b) { b.click(); });
    add(box, h('span', 'setup-target-label', 'SAVE TO'), seg, note);
    return { node: box, buttons: buttons, note: note };
  }

  function buildSection(sec) {
    var card = h('section', 'setup-sec list-panel');
    var ref = { node: card, groups: [], target: null, note: h('p', 'setup-hidden-note') };
    card.id = 'setup-sec-' + sec.id;
    add(card, buildHead(sec, sec.configFile), h('p', 'setup-purpose', sec.purpose));
    if (isLocal(sec)) {
      ref.target = buildTarget(sec);
      card.appendChild(ref.target.node);
    }
    groupFields(sec).forEach(function (group) {
      var node = h('div', 'setup-group');
      var rows = [];
      if (group.when) node.appendChild(h('h4', 'setup-group-name', 'step ' + group.when));
      else if (group.prefix) node.appendChild(h('h4', 'setup-group-name', sec.configPath + '.' + group.prefix));
      group.fields.forEach(function (field) {
        node.appendChild(buildField(sec, field));
        rows.push(ui.refs.fields[fieldKey(sec, field)]);
      });
      ref.groups.push({ node: node, when: group.when, rows: rows });
      card.appendChild(node);
    });
    ref.note.hidden = true;
    add(card, ref.note, h('div', 'setup-sec-end'));
    ui.refs.sections[sec.id] = ref;
    return card;
  }

  // The state line of a section that the form cannot edit.
  function delegState(sec) {
    var d = data.delegated[sec.id] || {};
    switch (sec.id) {
      case 'commit':
      case 'pr':
        return d.configured
          ? { on: true, text: plural(d.keys, 'rule') + ' set', short: String(d.keys) }
          : { on: false, text: 'Not set. /' + sec.id + ' uses its defaults.', short: 'not set' };
      case 'pr-labels':
        return d.mode ? { on: true, text: 'Mode: ' + d.mode, short: d.mode } : { on: false, text: 'Mode: off (default)', short: 'off' };
      case 'review-dimensions':
        return d.count
          ? { on: true, text: plural(d.count, 'dimension') + ' installed', short: String(d.count) }
          : { on: false, text: 'No dimensions. /review has nothing to check.', short: 'none' };
      case 'pr-template':
      case 'plan-template':
        return d.exists
          ? { on: true, text: 'Template file present', short: 'present' }
          : { on: false, text: 'No template file. The built-in template applies.', short: 'built-in' };
      case 'plan-guardrails':
      case 'execution-guardrails':
        return d.count
          ? { on: true, text: plural(d.count, 'guardrail'), short: String(d.count) }
          : { on: false, text: 'No guardrails', short: 'none' };
      case 'openspec-block':
        return d.present ? { on: true, text: 'Managed block present', short: 'present' } : { on: false, text: 'No managed block', short: 'none' };
      default:
        return { on: false, text: 'State not known', short: '' };
    }
  }

  function buildDeleg(sec) {
    var card = h('section', 'setup-sec setup-deleg list-panel');
    var state = delegState(sec);
    var stateRow = h('div', 'setup-deleg-state');
    var stateText = h('span', 'setup-deleg-text');
    var cmdRow = h('div', 'setup-cmd-row');
    var command = '/sdlc:setup --only ' + sec.id;
    var copy = button('setup-copy', 'Copy');
    var purpose = h('p', 'setup-purpose', sec.purpose);

    card.id = 'setup-sec-' + sec.id;
    purpose.title = sec.purpose;
    stateRow.setAttribute('data-state', state.on ? 'set' : 'unset');
    add(stateRow, h('span', 'setup-lamp'), stateText);
    stateText.textContent = state.text;
    copy.setAttribute('aria-label', 'Copy the command ' + command);
    copy.addEventListener('click', function () {
      var done = function (text) {
        copy.textContent = text;
        window.setTimeout(function () {
          copy.textContent = 'Copy';
        }, 1500);
      };
      if (!navigator.clipboard) return done('Not copied');
      navigator.clipboard.writeText(command).then(
        function () {
          done('Copied');
        },
        function () {
          done('Not copied');
        }
      );
    });
    add(cmdRow, h('code', 'setup-cmd', command), copy);
    add(card, buildHead(sec, (sec.filesModified || [sec.configFile])[0]), purpose, stateRow, cmdRow);
    ui.refs.deleg[sec.id] = {
      node: card,
      hay: (sec.id + ' ' + sec.purpose + ' ' + command).toLowerCase(),
    };
    return card;
  }

  // --- Page parts ------------------------------------------------------------------

  function buildBar() {
    var bar = h('div', 'setup-bar');
    var chips = h('span', 'chips');
    var search = h('input', 'setup-search');
    var views = h('span', 'chips');

    bar.setAttribute('role', 'group');
    bar.setAttribute('aria-label', 'Setup controls');
    ui.repos.forEach(function (repo) {
      var chip = button('chip', repo.name);
      chip.title = repo.root;
      chip.setAttribute('aria-pressed', String(repo.root === ui.root));
      chip.addEventListener('click', function () {
        if (repo.root === ui.root) return;
        ui.root = repo.root;
        ui.note = '';
        var store = storage();
        try {
          if (store) store.setItem(REPO_KEY, repo.root);
        } catch (e) {
          // The saved repo is a convenience only.
        }
        loadState();
      });
      chips.appendChild(chip);
    });

    search.type = 'search';
    search.placeholder = 'Filter fields';
    search.value = ui.filter;
    search.setAttribute('aria-label', 'Filter fields by name, key, or description');
    search.addEventListener('input', function () {
      ui.filter = search.value;
      applyVisibility();
    });

    ui.refs.views = {};
    [
      ['all', 'All'],
      ['changed', 'Changed'],
      ['unset', 'Not set'],
    ].forEach(function (pair) {
      var chip = button('chip', pair[1] + ' ');
      var count = h('span', 'chip-n');
      chip.appendChild(count);
      chip.addEventListener('click', function () {
        ui.show = pair[0];
        applyVisibility();
        refreshChrome();
      });
      ui.refs.views[pair[0]] = { chip: chip, count: count };
      views.appendChild(chip);
    });

    // The file filter: shows only the fields whose value is in one file, or
    // only the fields with no value (Default). A section that needs the
    // assistant counts as one item. The chip counts add up to Any file.
    var files = h('span', 'chips setup-file-chips');
    files.setAttribute('role', 'group');
    files.setAttribute('aria-label', 'Show only the values of one file');
    ui.refs.files = {};
    FILE_FILTERS.forEach(function (f) {
      var chip = button('chip', f.label + ' ');
      var count = h('span', 'chip-n');
      chip.title = f.title;
      chip.setAttribute('data-file', f.id);
      chip.appendChild(count);
      chip.addEventListener('click', function () {
        ui.file = f.id;
        applyVisibility();
        refreshChrome();
      });
      ui.refs.files[f.id] = { chip: chip, count: count };
      files.appendChild(chip);
    });

    return add(
      bar,
      h('span', 'filter-label', 'REPO'),
      chips,
      h('span', 'sep'),
      search,
      views,
      h('span', 'sep'),
      // The label and its chips wrap as one unit, so the label never stays alone at the end of a row.
      add(h('span', 'setup-bar-group'), h('span', 'filter-label', 'FILE'), files)
    );
  }

  // The file of the value that a field shows: the save file for a changed
  // value, the source file for a value from disk, 'default' for no value.
  function valueFile(sec, field) {
    var st = cur();
    var value = st.draft[sec.id][field.name];
    if (value === undefined) return 'default';
    if (!same(st.base[sec.id][field.name], value)) return isLocal(sec) ? st.target[sec.id] : 'config';
    return st.source[sec.id][field.name] || (isLocal(sec) ? st.target[sec.id] : 'config');
  }

  // The values of the other files for one field: { file: value } for each
  // file that sets the field but loses to the shown value. The per-file
  // values come from the state field layers (key '<section id>.<field name>').
  function overridesOf(sec, field) {
    var all = data.layers || {};
    var mine = all[sec.id + '.' + field.name] || {};
    var shown = valueFile(sec, field);
    var out = {};
    Object.keys(mine).forEach(function (file) {
      if (file !== shown) out[file] = mine[file];
    });
    return out;
  }

  // True when the field has a value in the file: the shown value or a value
  // that the shown value overrides.
  function inFile(sec, field, file) {
    return valueFile(sec, field) === file || Object.prototype.hasOwnProperty.call(overridesOf(sec, field), file);
  }

  // The file group of a section that needs the assistant: 'default' when the
  // section has no value, 'config' for a section in config.toml (commit, pr,
  // pr labels, guardrails), 'other' for a section in its own file.
  function delegFile(sec) {
    if (!delegState(sec).on) return 'default';
    return sec.configFile === SOURCE_FILE.config ? 'config' : 'other';
  }

  function buildNotes() {
    var box = h('div', 'setup-notes');
    var warn = h('p', 'setup-warn');
    warn.setAttribute('role', 'status');
    add(warn, h('b', '', 'PIPELINE RUNS'), h('span', '', 'A pipeline runs in this repo. A save can change how its next steps run.'));
    ui.refs.warn = warn;
    var stall = h('p', 'setup-warn');
    var conf = h('p', 'setup-warn');
    var err = h('p', 'setup-warn');
    stall.setAttribute('role', 'status');
    add(stall, h('b', '', 'PIPELINE STALLED'), h('span', '', 'A pipeline is stalled in this repo. A save can change how its next steps run.'));
    add(conf, h('b', '', 'CONFIG PROBLEM'), h('span', '', ui.configError));
    conf.hidden = ui.load !== 'ready' || !ui.configError;
    err.setAttribute('role', 'alert');
    add(err, h('b', '', 'ERROR'), h('span', '', ui.error));
    err.hidden = !ui.error;
    ui.refs.stall = stall;
    return add(box, warn, stall, conf, err);
  }

  function railItem(sec, onClick) {
    var item = button('setup-rail-item');
    var meta = h('span', 'setup-rail-meta');
    add(item, h('span', 'setup-lamp'), h('span', 'setup-rail-name', sec.label), meta);
    item.addEventListener('click', onClick);
    ui.refs.rail[sec.id] = { node: item, meta: meta };
    return item;
  }

  function buildBody() {
    var body = h('div', 'setup-body');
    var rail = h('nav', 'setup-rail');
    var main = h('div', 'setup-main');
    var formList = h('div', 'setup-rail-list');
    var delegList = h('div', 'setup-rail-list');
    var grList = h('div', 'setup-rail-list');
    var grid = h('div', 'setup-deleg-grid');
    var delegHead = h('div', 'setup-deleg-head');
    var none = h('p', 'setup-none', 'No field matches the filter.');

    rail.setAttribute('aria-label', 'Config sections');
    function jump(sec) {
      return function () {
        var node = doc.getElementById('setup-sec-' + sec.id);
        if (node) node.scrollIntoView({ block: 'start', behavior: 'auto' });
      };
    }
    formSections.forEach(function (sec) {
      formList.appendChild(railItem(sec, jump(sec)));
      main.appendChild(buildSection(sec));
    });
    grSections.forEach(function (sec) {
      // A click in the index only scrolls. The list stays closed.
      grList.appendChild(railItem(sec, jump(sec)));
      main.appendChild(buildGr(sec));
    });
    add(
      delegHead,
      h('h3', '', 'Sections that need the assistant'),
      h(
        'p',
        '',
        'These sections need a scan of the code or generated text. The dashboard cannot do that. Run the command in Claude Code in the repo.'
      )
    );
    delegSections.forEach(function (sec) {
      var state = delegState(sec);
      var item = railItem(sec, jump(sec));
      item.setAttribute('data-state', state.on ? 'set' : 'unset');
      ui.refs.rail[sec.id].meta.textContent = state.short;
      delegList.appendChild(item);
      grid.appendChild(buildDeleg(sec));
    });
    ui.refs.delegHead = delegHead;
    ui.refs.delegTitle = h('p', 'setup-rail-title', 'NEED THE ASSISTANT');
    ui.refs.none = none;
    ui.refs.grTitle = h('p', 'setup-rail-title', 'GUARDRAILS');
    add(rail, h('p', 'setup-rail-title', 'FORM SECTIONS'), formList, ui.refs.grTitle, grList, ui.refs.delegTitle, delegList);
    add(main, delegHead, grid, none);
    return add(body, rail, main);
  }

  function buildSaveBar() {
    var bar = h('div', 'setup-savebar');
    var count = h('span', 'setup-save-count');
    var files = h('span', 'setup-save-files');
    var note = h('span', 'setup-save-note');
    var discard = button('setup-btn', 'Discard changes');
    var save = button('setup-save');
    save.id = 'setup-save';
    note.tabIndex = -1;
    save.textContent = 'Review and save';
    discard.addEventListener('click', function () {
      var st = cur();
      st.draft = clone(st.base);
      st.target = clone(st.baseTarget);
      grSections.forEach(function (sec) {
        st.gr[sec.id] = grFresh(st.grBase[sec.id]);
      });
      ui.grOpen = {};
      st.errors = {};
      ui.note = '';
      refreshAll();
    });
    save.addEventListener('click', openReview);
    ui.refs.save = { bar: bar, count: count, files: files, note: note, discard: discard, save: save };
    return add(bar, h('span', 'setup-lamp'), count, files, h('span', 'spacer'), note, discard, save);
  }

  function buildNoConfig() {
    var box = h('div', 'setup-empty list-panel');
    var text = h('p');
    var create = button('setup-save');
    var hint = h('p');
    add(
      text,
      doc.createTextNode('The files '),
      h('code', '', '.sdlc-v2/config.toml'),
      doc.createTextNode(' and '),
      h('code', '', '.sdlc-v2/local.toml'),
      doc.createTextNode(' do not exist in this repo. Create them with the default values. Then change the values in this form.')
    );
    create.id = 'setup-create';
    create.textContent = 'Create config';
    create.addEventListener('click', function () {
      var root = ui.root;
      create.disabled = true;
      deps.post(sv.createRequest(deps.pageToken(), root)).then(function (res) {
        if (root !== ui.root) return;
        if (res.status === 200 && res.body && res.body.ok) {
          ui.note = 'Config created with the default values.';
          ui.noteState = 'set';
          loadState(true).then(focusNote);
          return;
        }
        ui.error = 'Not created. ' + sv.answerText(res.status, res.body);
        build();
      });
    });
    add(
      hint,
      doc.createTextNode('For detected values and for the sections that need the assistant, run '),
      h('code', '', '/sdlc:setup'),
      doc.createTextNode(' in the repo.')
    );
    return add(box, h('h3', '', 'This repo has no sdlc config yet'), text, create, hint);
  }

  function buildLegacy() {
    var box = h('div', 'setup-empty list-panel');
    var text = h('p');
    var hint = h('p');
    add(text, doc.createTextNode('This repo has the old config file '), h('code', '', '.sdlc-v2/config.json'),
      doc.createTextNode('. The form needs '), h('code', '', '.sdlc-v2/config.toml'), doc.createTextNode('.'));
    add(hint, doc.createTextNode('To convert the old file, run '), h('code', '', '/sdlc:setup --migrate'),
      doc.createTextNode(' in the repo.'));
    return add(box, h('h3', '', 'This repo has an old sdlc config'), text, hint);
  }

  // --- Refresh -----------------------------------------------------------------------

  // Shows and hides fields: the text filter, the Changed and Not set views,
  // and the fields that apply only when a step is on.
  function applyVisibility() {
    if (!ui.refs || !ui.refs.none) return;
    var query = ui.filter.trim().toLowerCase();
    var narrowed = !!query || ui.show !== 'all' || ui.file !== 'any';
    var active = activeSteps();
    var total = 0;

    formSections.forEach(function (sec) {
      var ref = ui.refs.sections[sec.id];
      var visible = 0;
      var offSteps = [];
      var offCount = 0;
      ref.groups.forEach(function (group) {
        var off = !!group.when && !!active && active.indexOf(group.when) === -1;
        var shown = 0;
        group.rows.forEach(function (row) {
          var state = row.row.getAttribute('data-state');
          var match =
            (!query || row.hay.indexOf(query) !== -1) &&
            (ui.show === 'all' || ui.show === state) &&
            (ui.file === 'any' || inFile(row.sec, row.field, ui.file));
          row.row.hidden = !match || off;
          if (match && !off) shown++;
          if (match && off) offCount++;
        });
        if (off && offSteps.indexOf(group.when) === -1) offSteps.push(group.when);
        group.node.hidden = shown === 0;
        visible += shown;
      });
      ref.note.hidden = offCount === 0;
      ref.note.textContent =
        plural(offCount, 'field') +
        (offCount === 1 ? ' is' : ' are') +
        ' hidden. ' +
        (offCount === 1 ? 'It applies' : 'They apply') +
        ' when ' +
        (offSteps.length === 1 ? 'the step ' + offSteps[0] + ' is on.' : 'these steps are on: ' + offSteps.join(', ') + '.');
      ref.node.hidden = narrowed && visible === 0;
      ui.refs.rail[sec.id].node.hidden = ref.node.hidden;
      total += visible;
    });

    var grShownSecs = 0;
    grSections.forEach(function (sec) {
      var sref = ui.refs.grSec[sec.id];
      var list = cur().gr[sec.id];
      var sevPick = ui.grSev[sec.id] || 'all';
      var file = list.length ? 'config' : 'default';
      var fileOk = ui.file === 'any' || ui.file === file;
      var shown = 0;
      list.forEach(function (g) {
        var ref = ui.refs.gr[grKey(sec, g)];
        if (!ref) return;
        var state = grState(sec, g);
        var hay = (g.id + ' ' + g.description).toLowerCase();
        var match =
          fileOk &&
          ui.show !== 'unset' &&
          (ui.show === 'all' || state !== 'set') &&
          (sevPick === 'all' || g.severity === sevPick) &&
          (!query || hay.indexOf(query) !== -1);
        ref.row.hidden = !match;
        if (match) shown++;
      });
      // A closed list opens by itself while the search or the Changed view
      // narrows it, so a match is never hidden.
      var forced = !!query || ui.show === 'changed';
      var open = !!ui.grExpanded[sec.id] || forced;
      sref.list.hidden = !open;
      sref.toggle.hidden = forced;
      sref.toggle.textContent = open ? 'Hide guardrails' : 'Show ' + plural(list.length, 'guardrail');
      sref.toggle.setAttribute('aria-expanded', String(open));
      sref.node.setAttribute('data-open', String(open));
      sref.shown.hidden = !open;
      sref.shown.textContent = 'Showing ' + shown + ' of ' + list.length;
      sref.node.hidden = !fileOk || ui.show === 'unset' || (narrowed && shown === 0 && sevPick === 'all');
      ui.refs.rail[sec.id].node.hidden = sref.node.hidden;
      if (!sref.node.hidden) grShownSecs++;
      total += shown;
    });
    ui.refs.grTitle.hidden = grShownSecs === 0;

    var delegShown = 0;
    delegSections.forEach(function (sec) {
      var ref = ui.refs.deleg[sec.id];
      var match =
        ui.show === 'all' && (ui.file === 'any' || delegFile(sec) === ui.file) && (!query || ref.hay.indexOf(query) !== -1);
      ref.node.hidden = !match;
      ui.refs.rail[sec.id].node.hidden = !match;
      if (match) delegShown++;
    });
    ui.refs.delegHead.hidden = delegShown === 0;
    ui.refs.delegTitle.hidden = delegShown === 0;
    ui.refs.none.hidden = total + delegShown > 0;
  }

  // Draws the parts that follow the whole form: the view counts, the section
  // index, the save targets, the tab count, the warning, and the save bar.
  function refreshChrome() {
    var badge = doc.getElementById('n-setup');
    if (!ui.refs || !ui.refs.save) {
      if (badge) badge.textContent = '';
      return;
    }
    var st = cur();
    var changes = listChanges();
    var total = 0;
    var unset = 0;
    var errors = 0;
    var perSource = { any: 0, config: 0, project: 0, user: 0, default: 0, other: 0 };
    // A section that needs the assistant counts as one item in its file group.
    delegSections.forEach(function (sec) {
      perSource.any++;
      perSource[delegFile(sec)]++;
    });

    formSections.forEach(function (sec) {
      var changed = 0;
      var set = 0;
      sec.fields.forEach(function (field) {
        var ref = ui.refs.fields[fieldKey(sec, field)];
        var state = ref.row.getAttribute('data-state');
        var vf = valueFile(sec, field);
        total++;
        perSource.any++;
        perSource[vf]++;
        Object.keys(overridesOf(sec, field)).forEach(function (file) {
          perSource[file]++;
        });
        if (state === 'changed') changed++;
        if (state === 'unset') unset++;
        if (st.draft[sec.id][field.name] !== undefined) set++;
        if (!ref.error.hidden) errors++;
      });
      var rail = ui.refs.rail[sec.id];
      rail.node.setAttribute('data-state', changed ? 'changed' : set ? 'set' : 'unset');
      rail.meta.textContent = changed ? changed + ' changed' : set + '/' + sec.fields.length;

      var target = ui.refs.sections[sec.id].target;
      if (target) {
        target.buttons.forEach(function (b) {
          b.setAttribute('aria-checked', String(b.getAttribute('data-target') === st.target[sec.id]));
        });
        rove(target.buttons);
        target.node.setAttribute('data-state', 'set');
        target.note.textContent = fileOf(sec, st.target[sec.id]);
      }
    });

    grSections.forEach(function (sec) {
      var list = cur().gr[sec.id];
      var changed = list.filter(function (g) {
        return grState(sec, g) !== 'set';
      }).length;
      var live = list.filter(function (g) {
        return !g.removed;
      }).length;
      var rail = ui.refs.rail[sec.id];
      rail.node.setAttribute('data-state', changed ? 'changed' : live ? 'set' : 'unset');
      rail.meta.textContent = changed ? changed + ' changed' : String(live);
      // A guardrail section counts as one item of its file.
      perSource.any++;
      perSource[list.length ? 'config' : 'default']++;
    });
    errors += grErrorCount();

    ui.refs.views.all.count.textContent = String(total);
    ui.refs.views.changed.count.textContent = String(changes.length);
    ui.refs.views.unset.count.textContent = String(unset);
    Object.keys(ui.refs.views).forEach(function (name) {
      ui.refs.views[name].chip.setAttribute('aria-pressed', String(ui.show === name));
    });
    var setupPanel = doc.getElementById('panel-setup');
    if (setupPanel) setupPanel.setAttribute('data-file', ui.file);
    Object.keys(ui.refs.files).forEach(function (name) {
      ui.refs.files[name].count.textContent = String(perSource[name]);
      // The counts of the two local files include overridden values.
      ui.refs.files[name].chip.setAttribute('aria-pressed', String(ui.file === name));
    });

    if (badge) badge.textContent = changes.length ? String(changes.length) : '';
    ui.refs.warn.hidden = !isRunning();
    ui.refs.stall.hidden = !isStalled();

    var save = ui.refs.save;
    var perFile = {};
    changes.forEach(function (c) {
      perFile[c.file] = (perFile[c.file] || 0) + 1;
    });
    save.files.replaceChildren.apply(
      save.files,
      Object.keys(perFile).map(function (file) {
        var chip = h('span', 'setup-save-file', file + ' ');
        chip.appendChild(h('b', '', String(perFile[file])));
        return chip;
      })
    );
    save.bar.hidden = !changes.length && !ui.note;
    save.count.textContent = changes.length ? plural(changes.length, 'unsaved change') : '';
    save.count.hidden = !changes.length;
    save.discard.hidden = !changes.length;
    save.save.hidden = !changes.length;
    save.save.disabled = errors > 0 && !ui.note;
    save.note.textContent = errors && !ui.note ? plural(errors, 'item') + (errors === 1 ? ' has' : ' have') + ' an error. Correct it before the save.' : ui.note;
    save.bar.setAttribute('data-state', errors || ui.noteState === 'error' ? 'error' : changes.length ? 'changed' : 'set');
    if (!ui.note) ui.noteState = '';
  }

  function refreshAll() {
    Object.keys(ui.refs.fields).forEach(refreshField);
    grSections.forEach(function (sec) {
      if (ui.refs.grSec[sec.id]) grRedraw(sec);
    });
    applyVisibility();
    refreshChrome();
  }

  // --- Save ----------------------------------------------------------------------------

  function openReview() {
    var dialog = doc.getElementById('setup-review');
    var body = doc.getElementById('setup-review-body');
    var ok = doc.getElementById('setup-review-ok');
    var changes = listChanges();
    var repo = repoOf(ui.root);
    var files = [];
    var nodes = [];

    doc.getElementById('setup-review-title').textContent =
      'Save ' + plural(changes.length, 'change') + ' to ' + (repo ? repo.name : 'the repo') + '?';
    if (isRunning()) {
      var warn = h('p', 'setup-warn');
      add(warn, h('b', '', 'PIPELINE RUNS'), h('span', '', 'A pipeline runs in this repo. A save can change how its next steps run.'));
      nodes.push(warn);
    } else if (isStalled()) {
      var stall = h('p', 'setup-warn');
      add(stall, h('b', '', 'PIPELINE STALLED'), h('span', '', 'A pipeline is stalled in this repo. A save can change how its next steps run.'));
      nodes.push(stall);
    }
    changes.forEach(function (c) {
      if (files.indexOf(c.file) === -1) files.push(c.file);
    });
    files.forEach(function (file) {
      var title = h('p', 'setup-review-file', file + ' ');
      var table = h('table');
      title.appendChild(h('span', '', file.indexOf('config.toml') !== -1 ? '· team file, in git' : '· personal file, not in git'));
      changes.forEach(function (c) {
        if (c.file !== file) return;
        var row = h('tr');
        var value = h('td');
        add(value, h('span', 'from', c.from), h('span', 'arrow', '→'), h('span', 'to', c.to));
        var keyCell = h('td', '', c.key);
        add(row, keyCell, value);
        table.appendChild(row);
      });
      nodes.push(title, table);
    });
    body.replaceChildren.apply(body, nodes);
    ok.textContent = 'Save';
    dialog.showModal();
    doc.getElementById('setup-review-title').focus();
  }

  function save() {
    var st = cur();
    var root = ui.root;
    var changes = listChanges();
    doc.getElementById('setup-review').close();
    if (!changes.length) return;
    ui.refs.save.save.disabled = true;
    deps.post(sv.saveRequest(deps.pageToken(), root, formSections, st)).then(function (res) {
      var out = sv.saveNote(res.status, res.body, changes.length);
      if (root !== ui.root) {
        // The user selected another repo. The files changed, so the old form state goes.
        if (out.reload) delete byRoot[root];
        return;
      }
      ui.note = out.note;
      ui.noteState = out.noteState;
      if (out.reload) {
        loadState(true).then(focusNote);
        return;
      }
      out.errors.forEach(function (e) {
        st.errors[e.section + '/' + e.field] = e.message;
      });
      refreshAll();
      var first = out.errors[0] && ui.refs.fields[out.errors[0].section + '/' + out.errors[0].field];
      if (first) first.control.focus();
      else focusNote();
    });
  }

  // --- Build ---------------------------------------------------------------------------

  function build() {
    var panel = ui.panel;
    if (!panel) return;
    ui.refs = { fields: {}, sections: {}, deleg: {}, rail: {}, gr: {}, grSec: {} };
    var kids = [];
    if (!ui.repos.length) {
      kids.push(h('p', 'setup-none', 'No repo is registered. Run an sdlc skill in a repo. Then the repo shows here.'));
    } else {
      kids.push(buildBar(), buildNotes());
      if (ui.load === 'ready') kids.push(buildBody(), buildSaveBar());
      else if (ui.load === 'no-config') kids.push(buildNoConfig());
      else if (ui.load === 'legacy-config') kids.push(buildLegacy());
    }
    panel.replaceChildren.apply(panel, kids);
    if (ui.refs.save) refreshAll();
    else {
      refreshChrome();
      if (ui.refs.warn) ui.refs.warn.hidden = !isRunning();
      if (ui.refs.stall) ui.refs.stall.hidden = !isStalled();
    }
  }

  function pickRoot(roots, scope) {
    var saved = null;
    try {
      saved = storage().getItem(REPO_KEY);
    } catch (e) {
      saved = null;
    }
    if (saved && roots.indexOf(saved) !== -1) return saved;
    if (scope && scope.size === 1) {
      var only = Array.from(scope)[0];
      if (roots.indexOf(only) !== -1) return only;
    }
    return roots[0] || null;
  }

  // --- Dashboard API ---------------------------------------------------------

  function init(d) {
    deps = d;
  }

  // The field list is the same for each repo. The page loads it one time.
  function loadFields() {
    if (ui.fields) return ui.fields;
    ui.fields = deps.fetchJSON(sv.FIELDS_URL).then(function (res) {
      if (res.status !== 200 || !res.body) {
        ui.fields = null;
        throw new Error(sv.answerText(res.status, res.body));
      }
      data.sections = sv.draftSections(res.body);
      help = sv.helpTable(data.sections);
      formSections = data.sections.filter(function (sec) {
        return !sec.delegatedTo;
      });
      grSections = data.sections.filter(function (sec) {
        return GR_IDS.indexOf(sec.id) !== -1;
      });
      delegSections = data.sections.filter(function (sec) {
        return !!sec.delegatedTo && GR_IDS.indexOf(sec.id) === -1;
      });
    });
    return ui.fields;
  }

  // Loads the state of the selected repo, then draws the tab. The script drops
  // an answer for a repo that is no longer selected. fresh is true after a save
  // or a create: the form stays on the page until the answer arrives, and the
  // answer replaces the form state of the repo.
  function loadState(fresh) {
    var root = ui.root;
    var req = ++ui.req;
    ui.error = '';
    if (!fresh) {
      ui.load = 'loading';
      build();
    }
    return loadFields()
      .then(function () {
        return deps.fetchJSON(sv.stateURL(root));
      })
      .then(function (res) {
        if (req !== ui.req) return;
        if (res.status !== 200 || !res.body) {
          ui.load = 'error';
          ui.error = 'The config of this repo did not load. ' + sv.answerText(res.status, res.body);
        } else {
          var state = sv.draftState(res.body);
          data.values = state.values;
          data.delegated = state.delegated;
          data.layers = state.layers;
          data.guardrails = state.guardrails;
          ui.configError = res.body.configError || '';
          ui.load = res.body.state;
          if (fresh) delete byRoot[root];
        }
        build();
      }, function (e) {
        if (req !== ui.req) return;
        ui.load = 'error';
        ui.error = 'The field list did not load. ' + e.message;
        build();
      });
  }

  function focusNote() {
    if (ui.refs && ui.refs.save) ui.refs.save.note.focus();
  }

  /**
   * Draws the Setup tab. A draw with the same list of repos keeps the form as
   * it is, so a snapshot never drops the focus or a typed value.
   * @param {Element} panel the panel of the Setup tab
   * @param {Array<object>} repos snapshot repos
   * @param {Set<string>} scope the repo filter of the other tabs
   */
  function draw(panel, repos, scope) {
    ui.panel = panel;
    ui.repos = repos || [];
    var roots = ui.repos.map(function (repo) {
      return repo.root;
    });
    var sig = JSON.stringify(
      ui.repos.map(function (repo) {
        return [repo.root, repo.name];
      })
    );
    if (!deps) return;
    if (roots.indexOf(ui.root) === -1) {
      ui.root = pickRoot(roots, scope);
      ui.sig = sig;
      if (ui.root) loadState();
      else build();
      return;
    }
    if (sig === ui.sig && panel.firstChild) return;
    ui.sig = sig;
    build();
  }

  /**
   * Takes each snapshot, for the pipeline warning.
   * @param {Array<object>} repos snapshot repos
   */
  function live(repos) {
    ui.repos = repos || [];
    if (ui.refs && ui.refs.warn) ui.refs.warn.hidden = !isRunning();
    if (ui.refs && ui.refs.stall) ui.refs.stall.hidden = !isStalled();
  }

  // The section index follows the scroll position.
  function spy() {
    if (!ui.refs || !ui.refs.rail || !ui.panel || ui.panel.hidden) return;
    var stage = doc.querySelector('.stage');
    var line = stage.getBoundingClientRect().top + 110;
    var current = null;
    // Page order, not field-list order: the last section above the line wins.
    formSections.concat(grSections, delegSections).forEach(function (sec) {
      var node = doc.getElementById('setup-sec-' + sec.id);
      if (node && !node.hidden && node.getBoundingClientRect().top <= line) current = sec.id;
    });
    Object.keys(ui.refs.rail).forEach(function (id) {
      var node = ui.refs.rail[id].node;
      if (id === current) node.setAttribute('aria-current', 'true');
      else node.removeAttribute('aria-current');
    });
  }

  var spyFrame = 0;
  doc.querySelector('.stage').addEventListener(
    'scroll',
    function () {
      if (spyFrame) return;
      spyFrame = window.requestAnimationFrame(function () {
        spyFrame = 0;
        spy();
      });
    },
    { passive: true }
  );

  doc.getElementById('setup-review-ok').addEventListener('click', save);
  doc.getElementById('setup-review-cancel').addEventListener('click', function () {
    doc.getElementById('setup-review').close();
  });

  window.sdlcSetup = { init: init, draw: draw, live: live };
})();
