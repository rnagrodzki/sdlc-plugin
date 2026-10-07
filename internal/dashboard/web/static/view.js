/**
 * Pure page logic for the sdlc dashboard. No DOM access — every function
 * takes plain data in and returns plain data or strings out, so the same
 * file runs unmodified as a browser script (window.sdlcView) and under
 * Node's test runner (module.exports). app.js wires these onto the DOM.
 *
 * Step status glyphs mirror the `steps[].status` enum of
 * plugins/sdlc/schemas/ship-state.schema.json — keep both in sync.
 */
(function (root) {
  'use strict';

  var STEP_GLYPHS = {
    completed: { glyph: '●', token: 'signal-green' }, // ●
    skipped: { glyph: '○', token: 'rail' }, // ○
    in_progress: { glyph: '◉', token: 'signal-amber' }, // ◉
    failed: { glyph: '✕', token: 'signal-red' }, // ✕
    pending: { glyph: '○', token: 'rail' }, // ○
  };

  // The 4 pipeline statuses a run can have (design.md "Pipeline status").
  // Second copy of internal/tools/dashboard_snapshot.go's pipeline status
  // constants — keep both in sync.
  var PIPELINE_TONES = {
    failed: 'signal-red',
    stalled: 'signal-amber',
    running: 'signal-green',
    completed: 'rail',
  };

  // Sort priority: failed first, then stalled, then running, then completed.
  var PIPELINE_SORT_ORDER = ['failed', 'stalled', 'running', 'completed'];

  var EMPTY_TEXT = 'Nothing is running. Start /sdlc:ship in any repo and it shows here.';

  var CONNECTION_TEXT = {
    open: '',
    snapshot: '',
    error: 'Reconnecting…',
    stopped: 'Server stopped. Run /sdlc:dashboard to start it again.',
    'stop-failed': 'The server did not stop. Run /sdlc:dashboard --stop.',
  };

  var OPEN_GROUPS_KEY = 'sdlc-dashboard-open-groups';

  /**
   * @param {string} status one of steps[].status in ship-state.schema.json
   * @returns {{glyph: string, token: string}}
   */
  function stepGlyph(status) {
    return STEP_GLYPHS[status] || { glyph: '○', token: 'rail' };
  }

  /**
   * @param {string} status one of the 4 pipeline statuses
   * @returns {string} 'signal-green' | 'signal-amber' | 'signal-red' | 'rail'
   */
  function pipelineTone(status) {
    return PIPELINE_TONES[status];
  }

  /**
   * @param {Array<{status: string}>} list
   * @returns {Array} a new array, sorted failed, stalled, running, completed
   */
  function sortPipelines(list) {
    var copy = (list || []).slice();
    copy.sort(function (a, b) {
      var ai = PIPELINE_SORT_ORDER.indexOf(a && a.status);
      var bi = PIPELINE_SORT_ORDER.indexOf(b && b.status);
      if (ai === -1) ai = PIPELINE_SORT_ORDER.length;
      if (bi === -1) bi = PIPELINE_SORT_ORDER.length;
      return ai - bi;
    });
    return copy;
  }

  /**
   * Cuts text to at most `max` Unicode code points ("runes"), appending
   * '…' when the text was longer.
   * @param {string} text
   * @param {number} [max]
   * @returns {string}
   */
  function cutText(text, max) {
    if (!text) return '';
    var limit = typeof max === 'number' ? max : 120;
    var chars = Array.from(text);
    if (chars.length <= limit) return text;
    return chars.slice(0, limit).join('') + '…';
  }

  /**
   * @param {{repos?: Array<{pipelines?: Array}>}} snapshot
   * @returns {string} the empty-page text, or '' when a pipeline exists
   */
  function emptyText(snapshot) {
    var repos = (snapshot && snapshot.repos) || [];
    for (var i = 0; i < repos.length; i++) {
      var pipelines = (repos[i] && repos[i].pipelines) || [];
      if (pipelines.length > 0) return '';
    }
    return EMPTY_TEXT;
  }

  /**
   * @param {string} event 'open' | 'error' | 'snapshot' | 'stopped' | 'stop-failed'
   * @returns {string} status banner text, '' when nothing should show
   */
  function connectionText(event) {
    return CONNECTION_TEXT[event] || '';
  }

  /**
   * @param {string} token the server's X-Sdlc-Token for this start
   * @returns {{url: string, init: object}|null} null when token is ''
   */
  function stopRequest(token) {
    if (!token) return null;
    return {
      url: '/api/stop',
      init: {
        method: 'POST',
        headers: { 'X-Sdlc-Token': token },
      },
    };
  }

  /**
   * @param {number} status HTTP status, or 0 for a network error
   * @returns {string} 'stopped' | 'stop-failed'
   */
  function stopResult(status) {
    return status === 202 ? 'stopped' : 'stop-failed';
  }

  /**
   * @param {string} repoRoot
   * @param {string} worktree
   * @returns {string} '' for the main worktree, else its last path part
   */
  function worktreeLabel(repoRoot, worktree) {
    if (!worktree || worktree === repoRoot) return '';
    var parts = worktree.split('/').filter(function (part) {
      return part.length > 0;
    });
    return parts.length > 0 ? parts[parts.length - 1] : '';
  }

  /**
   * @param {{getItem: function(string): (string|null)}} storage
   * @returns {Set<string>} repo roots whose group is open; '' on any error
   */
  function loadOpenGroups(storage) {
    try {
      var raw = storage.getItem(OPEN_GROUPS_KEY);
      if (!raw) return new Set();
      var parsed = JSON.parse(raw);
      if (!Array.isArray(parsed)) return new Set();
      return new Set(parsed);
    } catch (e) {
      return new Set();
    }
  }

  /**
   * @param {{setItem: function(string, string): void}} storage
   * @param {Set<string>} set repo roots whose group is open
   * @returns {void}
   */
  function saveOpenGroups(storage, set) {
    try {
      storage.setItem(OPEN_GROUPS_KEY, JSON.stringify(Array.from(set)));
    } catch (e) {
      // Storage can be unavailable (private mode, quota) — the open/closed
      // state is a convenience, not a requirement.
    }
  }

  var view = {
    stepGlyph: stepGlyph,
    pipelineTone: pipelineTone,
    sortPipelines: sortPipelines,
    cutText: cutText,
    emptyText: emptyText,
    connectionText: connectionText,
    stopRequest: stopRequest,
    stopResult: stopResult,
    worktreeLabel: worktreeLabel,
    loadOpenGroups: loadOpenGroups,
    saveOpenGroups: saveOpenGroups,
  };

  if (typeof module === 'object' && module.exports) {
    module.exports = view;
  } else {
    root.sdlcView = view;
  }
})(this);
