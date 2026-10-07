/**
 * DOM builders for the sdlc dashboard page. Each builder takes the document
 * as its first argument, so the same file runs as a browser script
 * (window.sdlcRender) and under Node's test runner (module.exports) with a
 * fake document. Snapshot text goes into the page through textContent only.
 *
 * The builders use only createElement, setAttribute, appendChild and the
 * textContent, className, hidden and open properties. Colours come from
 * class names in app.css, never from inline styles. Event listeners live in
 * app.js.
 */
(function (root) {
  'use strict';

  var EMPTY_TEXTS = {
    'none-in-scope': 'No pipelines for the selected repos.',
    'no-deferred': 'No open deferred items.',
    'no-learnings': 'No learnings today.',
    'no-history': 'No finished runs for the selected repos.',
  };

  /**
   * @param {Document} doc
   * @param {string} tag
   * @param {string} [className]
   * @param {*} [text] set through textContent only; null or undefined sets nothing
   * @returns {Element}
   */
  function el(doc, tag, className, text) {
    var node = doc.createElement(tag);
    if (className) node.className = className;
    if (text != null) node.textContent = String(text);
    return node;
  }

  function append(parent, children) {
    for (var i = 0; i < children.length; i++) {
      if (children[i]) parent.appendChild(children[i]);
    }
    return parent;
  }

  // 'in_progress' reads as 'in progress' in an accessible name.
  function statusWords(status) {
    return String(status || '').split('_').join(' ');
  }

  function plural(n, one, many) {
    return n + ' ' + (n === 1 ? one : many);
  }

  /**
   * The repo filter: `All`, then one toggle button for each repo chip.
   * @param {Document} doc
   * @param {object} view window.sdlcView
   * @param {Array<{root: string, name: string, count: number, hasFail: boolean}>} chips view.repoChips output
   * @param {Set<string>} scope selected repo roots; empty means every repo
   * @returns {Array<Element>} buttons; data-root is '' for `All`
   */
  function filterChips(doc, view, chips, scope) {
    var list = chips || [];
    var none = !scope || scope.size === 0;
    var total = 0;
    for (var i = 0; i < list.length; i++) total += list[i].count;

    function chip(label, root, count, hasFail, pressed) {
      var button = el(doc, 'button', 'chip', label);
      button.setAttribute('type', 'button');
      button.setAttribute('data-root', root);
      button.setAttribute('aria-pressed', String(pressed));
      if (root) button.setAttribute('title', root);
      button.appendChild(el(doc, 'span', hasFail ? 'chip-n has-fail' : 'chip-n', count));
      return button;
    }

    var out = [chip('All', '', total, false, none)];
    for (var j = 0; j < list.length; j++) {
      var c = list[j];
      var button = chip(c.name, c.root, c.count, c.hasFail, !none && scope.has(c.root));
      button.setAttribute(
        'aria-label',
        c.name + ', ' + plural(c.count, 'pipeline', 'pipelines') + (c.hasFail ? ', has a failed run' : '')
      );
      out.push(button);
    }
    return out;
  }

  /**
   * Head of a pipeline block: lamp, kind, branch, repo, issue chip (only with
   * issues), status word, and the `details N` toggle.
   * @param {Document} doc
   * @param {object} view
   * @param {{root: string, name: string}} repo
   * @param {{kind: string, branch: string, worktree: string, status: string, issues?: Array}} pipeline
   * @param {boolean} collapsed
   * @param {number} tiles the tile count of the block (view.tileCount)
   * @returns {Element} header.pipe-head
   */
  function blockHead(doc, view, repo, pipeline, collapsed, tiles) {
    var head = el(doc, 'header', 'pipe-head');

    var lamp = el(doc, 'span', 'lamp ' + pipeline.status);
    lamp.setAttribute('aria-hidden', 'true');

    var branch = el(doc, 'h2', 'pipe-branch', pipeline.branch);
    var worktree = view.worktreeLabel(repo.root, pipeline.worktree);
    branch.setAttribute('title', worktree ? pipeline.branch + ' · worktree ' + worktree : pipeline.branch);

    var side = el(doc, 'span', 'pipe-side');
    var repoName = el(doc, 'span', 'pipe-repo', repo.name);
    repoName.setAttribute('title', repo.root);
    side.appendChild(repoName);

    var issues = (pipeline.issues || []).length;
    if (issues > 0) side.appendChild(el(doc, 'span', 'issue-chip', plural(issues, 'issue', 'issues')));

    side.appendChild(el(doc, 'span', 'pipe-status ' + pipeline.status, pipeline.status));

    var fold = el(doc, 'button', 'fold-btn');
    fold.setAttribute('type', 'button');
    fold.setAttribute('aria-expanded', String(!collapsed));
    fold.setAttribute('title', 'Show or hide the step details of this pipeline');
    var chev = el(doc, 'span', 'chev', '▸');
    chev.setAttribute('aria-hidden', 'true');
    append(fold, [chev, el(doc, 'span', 'fold-label', 'details'), el(doc, 'span', 'fold-count', tiles)]);
    side.appendChild(fold);

    return append(head, [lamp, el(doc, 'span', 'pipe-kind', view.kindLabel(pipeline.kind)), branch, side]);
  }

  /**
   * The station track. A step with a section is a button with data-station
   * (its index) and data-section (its step name). A step with no section is
   * a plain div with no click action.
   * @param {Document} doc
   * @param {object} view
   * @param {{steps?: Array<{name: string, status: string, detail?: object}>}} pipeline
   * @param {number} selectedIndex index of the marked station
   * @returns {Element} div.track
   */
  function stationTrack(doc, view, pipeline, selectedIndex) {
    var steps = (pipeline && pipeline.steps) || [];
    var track = el(doc, 'div', 'track');
    track.setAttribute('role', 'group');
    track.setAttribute('aria-label', 'Steps');

    for (var i = 0; i < steps.length; i++) {
      var step = steps[i];
      var section = view.hasSection(step);
      var name = step.name + ', ' + statusWords(step.status);
      var cls = 'station' + (section ? ' has-section' : '') + (i === selectedIndex ? ' selected' : '');
      var station = el(doc, section ? 'button' : 'div', cls);
      station.setAttribute('title', name);
      if (section) {
        station.setAttribute('type', 'button');
        station.setAttribute('data-station', String(i));
        station.setAttribute('data-section', step.name);
        station.setAttribute('aria-label', name);
      }

      var lit = i > 0 && steps[i - 1].status === 'completed';
      var wire = el(doc, 'span', lit ? 'wire lit' : 'wire');
      wire.setAttribute('aria-hidden', 'true');
      var glyph = el(doc, 'span', 'glyph ' + step.status, view.stepGlyph(step.status).glyph);
      glyph.setAttribute('aria-hidden', 'true');
      var label = el(doc, 'span', step.status === 'in_progress' ? 'label current' : 'label', view.stationLabel(step.name));
      append(station, [wire, glyph, label]);
      // A div has no accessible name of its own: the status goes in hidden text.
      if (!section) station.appendChild(el(doc, 'span', 'sr-only', ', ' + statusWords(step.status)));

      track.appendChild(station);
    }
    return track;
  }

  /**
   * One pipeline block: head, then the track panel with the track and an
   * empty div.step-detail for the tiles.
   * @param {Document} doc
   * @param {object} view
   * @param {{root: string, name: string, sessions?: Array}} repo
   * @param {object} pipeline
   * @param {{collapsed: boolean, selected: number}} state
   * @returns {Element} article.pipe-block with data-key and data-id
   */
  function pipelineBlock(doc, view, repo, pipeline, state) {
    var collapsed = !!(state && state.collapsed);
    var selected = state && typeof state.selected === 'number' ? state.selected : view.defaultStationIndex(pipeline.steps);
    var session = view.pickSession(pipeline, repo.sessions);

    var block = el(doc, 'article', collapsed ? 'pipe-block collapsed' : 'pipe-block');
    block.setAttribute('data-key', view.pipelineKey(repo, pipeline));
    block.setAttribute('data-id', pipeline.id);

    var panel = el(doc, 'div', 'track-panel');
    var wrap = el(doc, 'div', 'detail-wrap');
    wrap.appendChild(el(doc, 'div', 'step-detail'));
    append(panel, [stationTrack(doc, view, pipeline, selected), wrap]);

    return append(block, [blockHead(doc, view, repo, pipeline, collapsed, view.tileCount(pipeline, session)), panel]);
  }

  /**
   * Header counts over every repo; the repo filter does not apply.
   * @param {Document} doc
   * @param {object} view
   * @param {Array} repos
   * @returns {Array<Element>} three spans: running, stalled, failed
   */
  function headerTotals(doc, view, repos) {
    var counts = view.headerCounts(repos);
    return [
      ['c-run', 'running', counts.running],
      ['c-stall', 'stalled', counts.stalled],
      ['c-fail', 'failed', counts.failed],
    ].map(function (row) {
      var span = el(doc, 'span', row[0], row[1] + ' · ');
      span.appendChild(el(doc, 'strong', '', row[2]));
      return span;
    });
  }

  function listPanel(doc, title, count) {
    var panel = el(doc, 'section', 'list-panel');
    var heading = el(doc, 'h2', 'list-title', title + ' ');
    heading.appendChild(el(doc, 'span', 'n', '(' + count + ')'));
    panel.appendChild(heading);
    return panel;
  }

  function activityRow(doc, view, chip, text, meta) {
    var main = el(doc, 'div', 'act-main');
    var body = el(doc, 'div', 'act-text', view.cutText(text, 200));
    body.setAttribute('title', text || '');
    append(main, [body, el(doc, 'div', 'act-meta', meta)]);
    return append(el(doc, 'div', 'act-row'), [chip, main]);
  }

  function metaLine(parts) {
    return parts
      .filter(function (part) {
        return !!part;
      })
      .join(' · ');
  }

  /**
   * The Activity tab: `Open deferred (n)` first, then `Learnings today (n)`,
   * both over the repos in scope.
   * @param {Document} doc
   * @param {object} view
   * @param {Array} repos
   * @param {Set<string>} scope
   * @returns {Element} div.act-grid
   */
  function activityPanel(doc, view, repos, scope) {
    var deferred = [];
    var learnings = [];
    (repos || []).forEach(function (repo) {
      if (!view.inScope(scope, repo.root)) return;
      (repo.deferred || []).forEach(function (item) {
        deferred.push(
          activityRow(
            doc,
            view,
            el(doc, 'span', 'sev sev-' + item.priority, item.priority),
            item.description,
            metaLine([repo.name, item.id])
          )
        );
      });
      (repo.learnings || []).forEach(function (item) {
        learnings.push(
          activityRow(doc, view, el(doc, 'span', 'sev', 'learning'), item.heading, metaLine([repo.name, item.branch]))
        );
      });
    });

    var deferredPanel = listPanel(doc, 'Open deferred', deferred.length);
    if (deferred.length > 0) {
      deferredPanel.appendChild(append(el(doc, 'div', 'list'), deferred));
      var hint = el(doc, 'p', 'hint', 'Triage these with ');
      hint.appendChild(el(doc, 'code', '', '/sdlc:deferred'));
      deferredPanel.appendChild(hint);
    } else {
      deferredPanel.appendChild(emptyState(doc, 'no-deferred'));
    }

    var learningsPanel = listPanel(doc, 'Learnings today', learnings.length);
    learningsPanel.appendChild(
      learnings.length > 0 ? append(el(doc, 'div', 'list'), learnings) : emptyState(doc, 'no-learnings')
    );

    return append(el(doc, 'div', 'act-grid'), [deferredPanel, learningsPanel]);
  }

  /**
   * One empty-state line.
   * @param {Document} doc
   * @param {string} kind no-pipelines | none-in-scope | repo-error | no-deferred | no-learnings | no-history
   * @param {string|{name: string, error: string}} [detail] the text for no-pipelines
   *   (view.emptyText), the repo for repo-error; other kinds ignore it
   * @returns {Element} p.generic-line with data-empty set to kind
   */
  function emptyState(doc, kind, detail) {
    var text = EMPTY_TEXTS[kind] || '';
    if (kind === 'no-pipelines') text = detail || '';
    if (kind === 'repo-error') text = 'Cannot read ' + ((detail && detail.name) || '') + ': ' + ((detail && detail.error) || '');
    var line = el(doc, 'p', 'generic-line', text);
    line.setAttribute('data-empty', kind);
    return line;
  }

  var api = {
    el: el,
    filterChips: filterChips,
    blockHead: blockHead,
    stationTrack: stationTrack,
    pipelineBlock: pipelineBlock,
    headerTotals: headerTotals,
    activityPanel: activityPanel,
    emptyState: emptyState,
  };

  if (typeof module === 'object' && module.exports) {
    module.exports = api;
  } else {
    root.sdlcRender = api;
  }
})(this);
