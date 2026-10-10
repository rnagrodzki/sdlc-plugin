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
 *
 * The toggle button (always visible) or Shift+M hides all marks and the panel,
 * to show the design without them. The same control shows them again. The
 * choice is kept in localStorage across reloads. The "dashboard not running"
 * banner stays.
 */
(function () {
  'use strict';

  var EMPTY_TEXT = '—';
  var BANNER_DOWN = 'Dashboard not running. Run /sdlc:dashboard, then reload.';
  var STATUS_URL = '/__design/status';
  var MARKS_OFF_CLASS = 'design-marks-off';
  var MARKS_OFF_KEY = 'design-marks-off';
  var LABEL_HIDE = 'Hide marks (Shift+M)';
  var LABEL_SHOW = 'Show marks (Shift+M)';
  var SAMPLE_MAX = 60;

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

  // clipText cuts text to at most max characters and adds '…' when it cut.
  // The panel uses it, so a long JSON sample does not fill the panel.
  function clipText(text, max) {
    return text.length > max ? text.slice(0, max) + '…' : text;
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

  // isToggleKey is true for Shift+M typed outside a form field. It switches
  // the marks and the panel on and off, to show the design without them.
  function isToggleKey(e) {
    if (!e || e.ctrlKey || e.metaKey || e.altKey || !e.shiftKey || e.code !== 'KeyM') return false;
    var t = e.target;
    var tag = t && t.tagName ? String(t.tagName).toLowerCase() : '';
    return !(tag === 'input' || tag === 'textarea' || tag === 'select' || (t && t.isContentEditable));
  }

  var STYLE = [
    '.design-missing, .design-unknown { outline: 2px dashed #d97706; outline-offset: 2px; }',
    '.design-unknown { outline-color: #dc2626; }',
    '.design-missing::after, .design-unknown::after {',
    '  content: attr(data-design-label); margin-left: 0.5em; padding: 1px 6px; border-radius: 3px;',
    '  font: 600 11px/1.4 system-ui, sans-serif; color: #fff; background: #b45309; vertical-align: middle; }',
    '.design-unknown::after { background: #b91c1c; }',
    'html.design-marks-off .design-missing, html.design-marks-off .design-unknown { outline: none; }',
    'html.design-marks-off .design-missing::after, html.design-marks-off .design-unknown::after { content: none; }',
    'html.design-marks-off #design-panel { display: none; }',
    '#design-toggle { position: fixed; right: 12px; bottom: 12px; z-index: 2147483646; padding: 3px 10px;',
    '  border: 1px solid #888; border-radius: 12px; background: #fff; color: #111; cursor: pointer;',
    '  font: 12px/1.4 system-ui, sans-serif; box-shadow: 0 1px 4px rgba(0,0,0,0.25); }',
    '#design-panel { position: fixed; right: 12px; bottom: 48px; z-index: 2147483646; max-width: 360px;',
    '  max-height: 50vh; overflow: auto; padding: 8px 10px; border: 1px solid #888; border-radius: 6px;',
    '  background: #fff; color: #111; font: 12px/1.4 system-ui, sans-serif; box-shadow: 0 2px 8px rgba(0,0,0,0.25); }',
    '#design-panel ul { margin: 6px 0 0; padding-left: 16px; }',
    '#design-panel li { overflow-wrap: anywhere; }',
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

  function marksOff() {
    return document.documentElement.classList.contains(MARKS_OFF_CLASS);
  }

  // toggleButton is the always-visible control. It stays outside the panel,
  // because the panel hides with the marks.
  var toggleButton = null;

  function toggleLabel(off) {
    return off ? LABEL_SHOW : LABEL_HIDE;
  }

  // setMarksOff shows or hides all marks and the panel. It keeps the choice in
  // localStorage, so a reload after a draft edit keeps the same view.
  function setMarksOff(off) {
    var list = document.documentElement.classList;
    if (off) list.add(MARKS_OFF_CLASS);
    else list.remove(MARKS_OFF_CLASS);
    if (toggleButton) toggleButton.textContent = toggleLabel(off);
    try {
      window.localStorage.setItem(MARKS_OFF_KEY, off ? '1' : '0');
    } catch (e) {
      // Storage is blocked: the choice lasts until the next reload.
    }
  }

  function storedMarksOff() {
    try {
      return window.localStorage.getItem(MARKS_OFF_KEY) === '1';
    } catch (e) {
      return false;
    }
  }

  function renderToggle() {
    toggleButton = make('button', { id: 'design-toggle', text: toggleLabel(marksOff()) });
    toggleButton.type = 'button';
    toggleButton.addEventListener('click', function () {
      setMarksOff(!marksOff());
    });
    document.body.appendChild(toggleButton);
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
        if (row.sample) text += ' [sample: ' + clipText(row.sample, SAMPLE_MAX) + ']';
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
    // The class goes on <html> now, in <head>, so the first paint has no flash.
    if (storedMarksOff()) document.documentElement.classList.add(MARKS_OFF_CLASS);
    document.addEventListener('keydown', function (e) {
      if (isToggleKey(e)) setMarksOff(!marksOff());
    });
    document.addEventListener('DOMContentLoaded', function () {
      renderToggle();
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
      isToggleKey: isToggleKey,
      clipText: clipText,
      panelModel: panelModel,
      bannerModel: bannerModel,
    };
  }
})();
