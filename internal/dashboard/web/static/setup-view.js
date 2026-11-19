/**
 * Page adapter for the Setup tab of the sdlc dashboard. It converts the
 * answers of the setup routes to the data shape that setup.js reads, and it
 * builds the change requests and the result texts of a save. No DOM access:
 * the same file runs unmodified as a browser script (window.sdlcSetupView)
 * and under Node's test runner (module.exports).
 *
 * The URLs and JSON keys follow the routes of internal/dashboard/web
 * (setup_routes.go): GET /api/setup-fields, GET /api/setup-state,
 * POST /api/setup-save, POST /api/setup-create.
 */
(function (root) {
  'use strict';

  // Node loads view.js as a module. In the browser, view.js runs first and sets root.sdlcView.
  var view = (typeof module === 'object' && module.exports) ? require('./view.js') : root.sdlcView;

  var FIELDS_URL = '/api/setup-fields';
  var SAVE_URL = '/api/setup-save';
  var CREATE_URL = '/api/setup-create';

  /**
   * The URL that reads the state of one repo.
   * @param {string} repo repo root
   * @returns {string}
   */
  function stateURL(repo) {
    return '/api/setup-state?repo=' + encodeURIComponent(repo);
  }

  /**
   * Same as plural() of the draft script (setup.js).
   * @param {number} n
   * @param {string} word
   * @returns {string}
   */
  function plural(n, word) {
    return n + ' ' + word + (n === 1 ? '' : 's');
  }

  function same(a, b) {
    return JSON.stringify(a) === JSON.stringify(b);
  }

  function isLocal(sec) {
    return /local\.toml$/.test(sec.configFile || '');
  }

  function copyKeys(from) {
    var out = {};
    Object.keys(from).forEach(function (key) {
      out[key] = from[key];
    });
    return out;
  }

  /**
   * The field list of the API as data.sections of the draft script. Each key
   * is copied. The one exception is whenStepInActiveSteps of a field, which
   * the draft reads as whenStep.
   * @param {{sections: object[]}} fieldsOut answer of GET /api/setup-fields
   * @returns {object[]}
   */
  function draftSections(fieldsOut) {
    var sections = (fieldsOut && fieldsOut.sections) || [];
    return sections.map(function (sec) {
      var out = copyKeys(sec);
      out.fields = (sec.fields || []).map(function (field) {
        var f = copyKeys(field);
        if (Object.prototype.hasOwnProperty.call(f, 'whenStepInActiveSteps')) {
          f.whenStep = f.whenStepInActiveSteps;
          delete f.whenStepInActiveSteps;
        }
        return f;
      });
      return out;
    });
  }

  /**
   * The state of the API as the values, the value of each file, the guardrail
   * lists and the delegated state of the draft script. A delegated row with
   * no count gives the count 0. layers and guardrails are the keys of the
   * answer as they are, and {} when the answer has none.
   * @param {object} stateOut answer of GET /api/setup-state
   * @returns {{values: object, delegated: object, layers: object, guardrails: object}}
   */
  function draftState(stateOut) {
    var delegated = {};
    ((stateOut && stateOut.delegated) || []).forEach(function (row) {
      var count = row.count || 0;
      switch (row.id) {
        case 'commit':
        case 'pr':
          delegated[row.id] = { configured: row.set === true, keys: count };
          break;
        case 'pr-labels':
          delegated[row.id] = { mode: row.mode ? row.mode : null };
          break;
        case 'review-dimensions':
        case 'plan-guardrails':
        case 'execution-guardrails':
          delegated[row.id] = { count: count };
          break;
        case 'pr-template':
        case 'plan-template':
          delegated[row.id] = { exists: row.set === true };
          break;
        case 'openspec-block':
          delegated[row.id] = { present: row.set === true };
          break;
        default:
          break;
      }
    });
    return {
      values: (stateOut && stateOut.values) || {},
      delegated: delegated,
      layers: (stateOut && stateOut.layers) || {},
      guardrails: (stateOut && stateOut.guardrails) || {},
    };
  }

  /**
   * The text after the value of a file that another file wins over. The
   * runtime merges a flag-set field key by key, so that text is not struck.
   * @param {string} fieldType type of the field, for example 'flag-set'
   * @param {boolean} sameValue true when the value equals the value of the file that wins
   * @param {string} winnerFileName name of the file that wins, for example 'local.toml'
   * @returns {{text: string, struck: boolean}}
   */
  function overrideText(fieldType, sameValue, winnerFileName) {
    if (fieldType === 'flag-set') {
      return { text: 'merged with ' + winnerFileName, struck: false };
    }
    if (sameValue) {
      return { text: 'same value \u00b7 overridden by ' + winnerFileName, struck: true };
    }
    return { text: 'overridden by ' + winnerFileName, struck: true };
  }

  /**
   * The guardrail changes between the disk lists and the rows of the form, as
   * the guardrails field of POST /api/setup-save:
   * - a row that is not new gives change when its values differ from the disk
   *   row, and remove when it is removed;
   * - a new row that is not removed gives add, or one change when a removed
   *   disk row has the same id (the server takes one change for each id);
   * - a new row that is removed gives nothing.
   * A row that is not new and has no disk row counts as a new row.
   * @param {Object<string, {id: string, severity: string, description: string}[]>} base
   *   section id -> disk list
   * @param {Object<string, {id: string, severity: string, description: string,
   *   isNew: boolean, removed: boolean}[]>} draft section id -> rows of the form
   * @returns {Object<string, {id: string, op: 'add'|'change'|'remove',
   *   severity?: string, description?: string}[]>} only sections with a change; {} when none
   */
  function guardrailChanges(base, draft) {
    var out = {};
    Object.keys(draft || {}).forEach(function (section) {
      var rows = draft[section] || [];
      var disk = Object.create(null);
      ((base && base[section]) || []).forEach(function (g) {
        disk[g.id] = g;
      });
      var list = [];
      var removedAt = Object.create(null);
      var added = [];
      rows.forEach(function (row) {
        var old = disk[row.id];
        if (row.isNew || !old) {
          if (!row.removed) added.push(row);
          return;
        }
        if (row.removed) {
          removedAt[row.id] = list.length;
          list.push({ id: row.id, op: 'remove' });
        } else if (old.severity !== row.severity || old.description !== row.description) {
          list.push({ id: row.id, op: 'change', severity: row.severity, description: row.description });
        }
      });
      added.forEach(function (row) {
        var entry = { id: row.id, op: 'add', severity: row.severity, description: row.description };
        if (row.id in removedAt) {
          entry.op = 'change';
          list[removedAt[row.id]] = entry;
          delete removedAt[row.id];
        } else {
          list.push(entry);
        }
      });
      if (list.length) out[section] = list;
    });
    return out;
  }

  /**
   * The effect of each option, as the draft script reads it. The options yes
   * and no of a boolean field become on and off.
   * @param {object[]} sections sections of the field list
   * @returns {Object<string, Object<string, {text: string, before?: string, after?: string}>>}
   *   '<section id>/<field name>' -> option -> effect
   */
  function helpTable(sections) {
    var table = {};
    (sections || []).forEach(function (sec) {
      (sec.fields || []).forEach(function (field) {
        if (!field.effects || !field.effects.length) return;
        var entry = {};
        field.effects.forEach(function (effect) {
          var option = effect.option;
          if (field.type === 'boolean') {
            if (option === 'yes') option = 'on';
            else if (option === 'no') option = 'off';
          }
          var out = { text: effect.text };
          if (effect.before) out.before = effect.before;
          if (effect.after) out.after = effect.after;
          entry[option] = out;
        });
        table[sec.id + '/' + field.name] = entry;
      });
    });
    return table;
  }

  /**
   * The body of POST /api/setup-save: one entry for each changed field, the
   * target file of each local section with a change, and the guardrail
   * changes. The key guardrails is there only when a guardrail list changed.
   * @param {string} repo repo root
   * @param {object[]} sections the form sections of the draft
   * @param {{base: object, draft: object, target: object, grBase?: object, gr?: object}} st
   *   form state of the repo; grBase and gr are the base and draft of guardrailChanges
   * @returns {{repo: string, changes: object[], targets: Object<string, string>,
   *   guardrails?: object}}
   */
  function saveBody(repo, sections, st) {
    var changes = [];
    var targets = {};
    (sections || []).forEach(function (sec) {
      var base = (st.base && st.base[sec.id]) || {};
      var draft = (st.draft && st.draft[sec.id]) || {};
      var count = changes.length;
      (sec.fields || []).forEach(function (field) {
        var was = base[field.name];
        var now = draft[field.name];
        if (same(was, now)) return;
        if (now === undefined) {
          changes.push({ section: sec.id, field: field.name, op: 'reset' });
        } else {
          changes.push({ section: sec.id, field: field.name, op: 'set', value: now });
        }
      });
      if (changes.length > count && isLocal(sec) && st.target && st.target[sec.id]) {
        targets[sec.id] = st.target[sec.id];
      }
    });
    var body = { repo: repo, changes: changes, targets: targets };
    var gr = guardrailChanges(st.grBase || {}, st.gr || {});
    if (Object.keys(gr).length) body.guardrails = gr;
    return body;
  }

  /**
   * The request that saves the changes of one repo. Use it as
   * `fetch(req.url, req)`.
   * @returns {{method: string, url: string, headers: object, body: string}|null}
   *   null when token or repo is ''
   */
  function saveRequest(token, repo, sections, st) {
    if (!token || !repo) return null;
    return view.guardedPost(token, SAVE_URL, saveBody(repo, sections, st));
  }

  /**
   * The request that creates the config of one repo. Use it as
   * `fetch(req.url, req)`.
   * @returns {{method: string, url: string, headers: object, body: string}|null}
   *   null when token or repo is ''
   */
  function createRequest(token, repo) {
    if (!token || !repo) return null;
    return view.guardedPost(token, CREATE_URL, { repo: repo });
  }

  /**
   * The text of a failed answer.
   * @param {number} status HTTP status; 0 for no answer
   * @param {*} body parsed JSON body, or null
   * @returns {string}
   */
  function answerText(status, body) {
    if (body && typeof body === 'object') {
      if (body.error && typeof body.error === 'object') {
        var text = String(body.error.message || '');
        if (body.error.suggestion) text += ' ' + body.error.suggestion;
        return text;
      }
      if (Array.isArray(body.errors) && body.errors.length) {
        return body.errors.join(' ');
      }
    }
    if (body === null || body === undefined) {
      return 'The dashboard server gave no answer (HTTP ' + status + ').';
    }
    return 'The dashboard server gave an answer that the page cannot read (HTTP ' + status + ').';
  }

  /**
   * The note, the note state, and the field errors that follow a save.
   * @param {number} status HTTP status
   * @param {*} body parsed JSON body, or null
   * @param {number} count number of changes that the save sent
   * @returns {{note: string, noteState: string, errors: object[], reload: boolean}}
   */
  function saveNote(status, body, count) {
    var obj = body && typeof body === 'object' ? body : null;
    if (status === 200 && obj && obj.ok === true) {
      return { note: 'Saved ' + plural(count, 'change') + '.', noteState: 'set', errors: [], reload: true };
    }
    if (status === 200 && obj && obj.ok === false) {
      var written = (obj.written || []).length;
      var failed = obj.failed || [];
      var parts = ['Saved ' + written + ' of ' + (written + failed.length) + ' sections.'];
      failed.forEach(function (f) {
        parts.push('Not saved: ' + f.section + ' (' + f.file + '). ' + f.message + (f.suggestion ? ' ' + f.suggestion : ''));
      });
      if (obj.next) {
        parts.push(obj.next);
      }
      return { note: parts.join(' '), noteState: 'error', errors: [], reload: true };
    }
    var code = obj && obj.error && typeof obj.error === 'object' ? obj.error.code : '';
    if (status === 400 && code === 'SETUP_INVALID') {
      var fields = Array.isArray(obj.error.fields) ? obj.error.fields : [];
      return {
        note: 'Not saved. The config check refused ' + plural(fields.length, 'field') + '. No file changed.',
        noteState: 'error',
        errors: fields,
        reload: false,
      };
    }
    if (status === 413 && code === 'BODY_TOO_LARGE') {
      return {
        note: 'Not saved. The save is larger than the server accepts. Save fewer fields or guardrails at one time.',
        noteState: 'error',
        errors: [],
        reload: false,
      };
    }
    return { note: 'Not saved. ' + answerText(status, body), noteState: 'error', errors: [], reload: false };
  }

  var api = {
    FIELDS_URL: FIELDS_URL,
    stateURL: stateURL,
    draftSections: draftSections,
    draftState: draftState,
    overrideText: overrideText,
    guardrailChanges: guardrailChanges,
    helpTable: helpTable,
    saveBody: saveBody,
    saveRequest: saveRequest,
    createRequest: createRequest,
    answerText: answerText,
    saveNote: saveNote,
    plural: plural,
  };

  if (typeof module === 'object' && module.exports) {
    module.exports = api;
  } else {
    root.sdlcSetupView = api;
  }
})(this);
