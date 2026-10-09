/**
 * marks.js - dependency marks for the dashboard design preview.
 *
 * The preview program inserts this script into <head> of the draft page, after
 * the <script id="design-deps" type="application/json"> tag. In a browser it
 * reads that tag, defines window.designDep, shows a panel of the recorded
 * dependencies and shows a banner when the dashboard is not running.
 * In Node (tests) it only exports the pure functions.
 *
 * Draft pages call designDep(id, value, element) where they show data that the
 * real dashboard snapshot may not give yet. An empty value gets a "missing"
 * mark. An id that is not in dependencies.json gets an "unknown" mark.
 */
(function () {
  'use strict';

  var EMPTY_TEXT = '—';
  var BANNER_DOWN = 'Dashboard not running. Run /sdlc:dashboard, then reload.';
  var STATUS_URL = '/__design/status';

  // isMissing is true for null, undefined, '' and NaN. 0 and false are values.
  function isMissing(v) {
    return v === null || v === undefined || v === '' || (typeof v === 'number' && v !== v);
  }

  // toText turns a value or a sample into display text.
  function toText(v) {
    if (typeof v === 'string') return v;
    if (v !== null && typeof v === 'object') return JSON.stringify(v);
    return String(v);
  }

  // readState parses the text of the design-deps tag.
  // Good input gives { ok: true, dependencies: [...] }. Anything else gives
  // { ok: false, error }.
  function readState(text) {
    var parsed;
    try {
      parsed = JSON.parse(text);
    } catch (e) {
      return { ok: false, error: 'design-deps: not valid JSON: ' + e.message };
    }
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return { ok: false, error: 'design-deps: expected a JSON object' };
    }
    if (parsed.ok === true) {
      if (!Array.isArray(parsed.dependencies)) {
        return { ok: false, error: 'design-deps: dependencies must be an array' };
      }
      return parsed;
    }
    return {
      ok: false,
      error: typeof parsed.error === 'string' && parsed.error ? parsed.error : 'design-deps: no dependency data',
    };
  }

  // resolveDep decides the mark, the label and the display text for one value.
  //   live value present     -> mark null,      label '',                          text live value
  //   empty, id known        -> mark 'missing', label 'missing <id>',              text sample or the dash
  //   id unknown             -> mark 'unknown', label 'unknown dependency <id>',   text live value or the dash
  // When the state is not ok, no id is known.
  function resolveDep(state, id, v) {
    var deps = state && state.ok && Array.isArray(state.dependencies) ? state.dependencies : [];
    var dep = null;
    for (var i = 0; i < deps.length; i++) {
      if (deps[i] && deps[i].id === id) {
        dep = deps[i];
        break;
      }
    }
    var missing = isMissing(v);
    if (!dep) {
      return {
        text: missing ? EMPTY_TEXT : toText(v),
        mark: 'unknown',
        label: 'unknown dependency ' + id,
      };
    }
    if (!missing) {
      return { text: toText(v), mark: null, label: '' };
    }
    return {
      text: isMissing(dep.sample) ? EMPTY_TEXT : toText(dep.sample),
      mark: 'missing',
      label: 'missing ' + id,
    };
  }

  // designDep is the call that draft pages make. It reads the state from
  // globalThis.__designDeps, marks el when it is given, and returns the text.
  // The style rule shows data-design-label with ::after.
  function designDep(id, v, el) {
    var r = resolveDep(globalThis.__designDeps, id, v);
    if (el) {
      el.classList.remove('design-missing', 'design-unknown');
      delete el.dataset.designMark;
      delete el.dataset.designLabel;
      if (r.mark) {
        el.classList.add('design-' + r.mark);
        el.dataset.designMark = r.mark;
        el.dataset.designLabel = r.label;
      }
    }
    return r.text;
  }

  // panelModel turns the state into the content of the dependency panel.
  function panelModel(state) {
    var title = 'Design dependencies';
    if (!state || !state.ok) {
      var error = state && state.error ? state.error : 'design-deps: no dependency data';
      return { title: title, rows: [], empty: null, error: error };
    }
    var deps = Array.isArray(state.dependencies) ? state.dependencies : [];
    var rows = deps.map(function (d) {
      return {
        id: d.id,
        kind: d.kind,
        need: d.need,
        sample: isMissing(d.sample) ? '' : toText(d.sample),
      };
    });
    return { title: title, rows: rows, empty: rows.length === 0 ? 'No dependencies yet' : null, error: null };
  }

  // bannerModel gives the banner text, or null when no banner is needed.
  // Only the exact status "not running" shows the banner.
  function bannerModel(status) {
    if (status && status.dashboard === 'not running') return BANNER_DOWN;
    return null;
  }

  var STYLE = [
    '.design-missing, .design-unknown { outline: 2px dashed #d97706; outline-offset: 2px; }',
    '.design-unknown { outline-color: #dc2626; }',
    '.design-missing::after, .design-unknown::after {',
    '  content: attr(data-design-label); margin-left: 0.5em; padding: 1px 6px; border-radius: 3px;',
    '  font: 600 11px/1.4 system-ui, sans-serif; color: #fff; background: #b45309; vertical-align: middle; }',
    '.design-unknown::after { background: #b91c1c; }',
    '#design-panel { position: fixed; right: 12px; bottom: 12px; z-index: 2147483646; max-width: 360px;',
    '  max-height: 50vh; overflow: auto; padding: 8px 10px; border: 1px solid #888; border-radius: 6px;',
    '  background: #fff; color: #111; font: 12px/1.4 system-ui, sans-serif; box-shadow: 0 2px 8px rgba(0,0,0,0.25); }',
    '#design-panel ul { margin: 6px 0 0; padding-left: 16px; }',
    '#design-panel .design-panel-error { margin-top: 6px; color: #b91c1c; }',
    '#design-banner { position: fixed; top: 0; left: 0; right: 0; z-index: 2147483647; padding: 6px 12px;',
    '  background: #b91c1c; color: #fff; font: 600 13px/1.4 system-ui, sans-serif; text-align: center; }',
  ].join('\n');

  function make(tag, props) {
    var node = document.createElement(tag);
    if (props) {
      if (props.id) node.id = props.id;
      if (props.className) node.className = props.className;
      if (props.text !== undefined) node.textContent = props.text;
    }
    return node;
  }

  function renderPanel(model) {
    var panel = make('aside', { id: 'design-panel' });
    panel.appendChild(make('strong', { text: model.title }));
    if (model.error) {
      panel.appendChild(make('div', { className: 'design-panel-error', text: model.error }));
    } else if (model.empty) {
      panel.appendChild(make('div', { text: model.empty }));
    } else {
      var list = make('ul');
      model.rows.forEach(function (row) {
        var text = row.id + ' (' + row.kind + '): ' + row.need;
        if (row.sample) text += ' [sample: ' + row.sample + ']';
        list.appendChild(make('li', { text: text }));
      });
      panel.appendChild(list);
    }
    document.body.appendChild(panel);
  }

  function renderBanner(text) {
    document.body.appendChild(make('div', { id: 'design-banner', text: text }));
  }

  // Bootstrap: runs only in a browser.
  if (typeof window !== 'undefined') {
    if (!document.getElementById('design-marks-style')) {
      var style = make('style', { id: 'design-marks-style', text: STYLE });
      document.head.appendChild(style);
    }
    var tag = document.getElementById('design-deps');
    window.__designDeps = readState(tag ? tag.textContent : '');
    window.designDep = designDep;
    document.addEventListener('DOMContentLoaded', function () {
      renderPanel(panelModel(window.__designDeps));
      if (typeof fetch !== 'function') return;
      fetch(STATUS_URL)
        .then(function (res) {
          return res.json();
        })
        .then(function (status) {
          var text = bannerModel(status);
          if (text) renderBanner(text);
        })
        .catch(function () {
          // The status route is unreachable: show no banner.
        });
    });
  }

  if (typeof module !== 'undefined') {
    module.exports = {
      isMissing: isMissing,
      readState: readState,
      resolveDep: resolveDep,
      designDep: designDep,
      panelModel: panelModel,
      bannerModel: bannerModel,
    };
  }
})();
