/**
 * DOM builders for the sdlc dashboard page. Each builder takes the document
 * as its first argument, so the same file runs as a browser script
 * (window.sdlcRender) and under Node's test runner (module.exports) with a
 * fake document. Snapshot text goes into the page through textContent only.
 *
 * The builders use only createElement, setAttribute, appendChild and the
 * textContent, className, hidden and open properties. tickElapsed also uses
 * querySelectorAll and getAttribute. Colours come from class names in
 * app.css, never from inline styles. Event listeners live in app.js.
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

  // The mark of a run that waits for a person: the banner glyph and the
  // glyph of the current station.
  var ATTENTION_GLYPH = '◈';

  /**
   * Time from the start of a wait to now, for the banner of a waiting run.
   * @param {object} view
   * @param {string} askedAt RFC 3339 timestamp of the question
   * @param {Date|number} now
   * @returns {string} '26s', '4m 12s', or '1h 03m'; '' for a bad timestamp
   */
  function elapsedText(view, askedAt, now) {
    var asked = Date.parse(askedAt);
    if (isNaN(asked)) return '';
    var nowMs = typeof now === 'number' ? now : now.getTime();
    // A clock that runs behind the host must not give a negative time.
    return view.formatElapsed(Math.max(0, nowMs - asked));
  }

  /**
   * The wait banner of a pipeline: the bar `◈ WAITING ON YOU · <elapsed>`,
   * then the line `<header>: "<text>"`. The line has only the parts that
   * exist, and it is left out when the header and the text are both empty.
   * The elapsed span keeps the question time in data-asked, so a timer can
   * refresh it without a new snapshot.
   * @param {Document} doc
   * @param {object} view
   * @param {{askedAt?: string, header?: string, text?: string}} attention
   * @param {Date|number} now
   * @returns {Array<Element|null>} div.attn-bar, then div.attn-text, or null
   *   when the header and the text are both empty
   */
  function attentionRows(doc, view, attention, now) {
    var glyph = el(doc, 'span', 'attn-glyph', ATTENTION_GLYPH);
    glyph.setAttribute('aria-hidden', 'true');
    var elapsed = el(doc, 'span', 'attn-elapsed', elapsedText(view, attention.askedAt, now));
    elapsed.setAttribute('data-asked', attention.askedAt || '');
    // The timer rewrites this text each second; a screen reader must not read each change.
    elapsed.setAttribute('aria-live', 'off');
    var bar = append(el(doc, 'div', 'attn-bar'), [glyph, el(doc, 'span', '', ' WAITING ON YOU · '), elapsed]);

    var quoted = attention.text ? '"' + attention.text + '"' : '';
    var line = attention.header && quoted ? attention.header + ': ' + quoted : attention.header || quoted;
    return [bar, line ? el(doc, 'div', 'attn-text', line) : null];
  }

  /**
   * The browser tab title for the repos in scope: view.pageTitle over the
   * repos that view.inScope accepts.
   * @param {object} view window.sdlcView
   * @param {string} base title without a count
   * @param {Array<{root: string}>} repos every repo of the snapshot
   * @param {Set<string>} scope selected repo roots; empty means every repo
   * @returns {string} base, or "(N) " and base when N pipelines of the repos in scope wait for a person
   */
  function scopedTitle(view, base, repos, scope) {
    return view.pageTitle(
      base,
      repos.filter(function (repo) {
        return view.inScope(scope, repo.root);
      })
    );
  }

  /**
   * Rewrites the text of every `.attn-elapsed` node in doc from its
   * data-asked value and now. A node whose data-asked is unreadable keeps its
   * text.
   * @param {Document} doc
   * @param {object} view window.sdlcView
   * @param {Date|number} now
   */
  function tickElapsed(doc, view, now) {
    var nodes = doc.querySelectorAll('.attn-elapsed');
    for (var i = 0; i < nodes.length; i++) {
      var text = elapsedText(view, nodes[i].getAttribute('data-asked'), now);
      if (text) nodes[i].textContent = text;
    }
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

  // Pipeline statuses whose row can be archived. A running row cannot.
  var ARCHIVABLE = { completed: true, failed: true, stalled: true };

  /**
   * The Archive button of a pipeline row. The click handler in app.js reads
   * the three data attributes and asks for a confirm before it sends the
   * request.
   * @param {Document} doc
   * @param {{root: string}} repo
   * @param {{id: string, status: string}} pipeline
   * @returns {Element|null} button.archive-btn, or null for a running row
   */
  function archiveButton(doc, repo, pipeline) {
    if (!pipeline.id || !Object.prototype.hasOwnProperty.call(ARCHIVABLE, pipeline.status)) return null;
    var button = el(doc, 'button', 'archive-btn', 'Archive');
    button.setAttribute('type', 'button');
    button.setAttribute('data-archive', pipeline.id);
    button.setAttribute('data-repo', repo.root);
    button.setAttribute('data-status', pipeline.status);
    return button;
  }

  /**
   * Head of a pipeline block: lamp, kind, branch, repo, issue chip (only with
   * issues), status word, the Archive button (only for a completed, failed,
   * or stalled pipeline), and the `details N` toggle. A pipeline with an
   * attention has the class `waiting` on its lamp.
   * @param {Document} doc
   * @param {object} view
   * @param {{root: string, name: string}} repo
   * @param {{kind: string, branch: string, worktree: string, status: string, issues?: Array, attention?: object}} pipeline
   * @param {boolean} collapsed
   * @param {number} tiles the tile count of the block (view.tileCount)
   * @returns {Element} header.pipe-head
   */
  function blockHead(doc, view, repo, pipeline, collapsed, tiles) {
    var head = el(doc, 'header', 'pipe-head');

    var lamp = el(doc, 'span', 'lamp ' + pipeline.status + (pipeline.attention ? ' waiting' : ''));
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

    var archive = archiveButton(doc, repo, pipeline);
    if (archive) side.appendChild(archive);

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
   * a plain div with no click action. The current station of a pipeline with
   * an attention shows `◈` in place of its status glyph.
   * @param {Document} doc
   * @param {object} view
   * @param {{steps?: Array<{name: string, status: string, detail?: object}>, attention?: object}} pipeline
   * @param {number} selectedIndex index of the marked station
   * @returns {Element} div.track
   */
  function stationTrack(doc, view, pipeline, selectedIndex) {
    var steps = (pipeline && pipeline.steps) || [];
    var waiting = !!(pipeline && pipeline.attention);
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
      var glyphText = waiting && step.status === 'in_progress' ? ATTENTION_GLYPH : view.stepGlyph(step.status).glyph;
      var glyph = el(doc, 'span', 'glyph ' + step.status, glyphText);
      glyph.setAttribute('aria-hidden', 'true');
      var label = el(doc, 'span', step.status === 'in_progress' ? 'label current' : 'label', view.stationLabel(step.name));
      append(station, [wire, glyph, label]);
      // A div has no accessible name of its own: the status goes in hidden text.
      if (!section) station.appendChild(el(doc, 'span', 'sr-only', ', ' + statusWords(step.status)));

      track.appendChild(station);
    }
    return track;
  }

  // --- Tiles ------------------------------------------------------------------

  // Lamp classes of app.css by colour token of view.js.
  var LAMP_BY_TONE = {
    'signal-green': 'completed',
    'signal-amber': 'running',
    'signal-red': 'failed',
    rail: 'stalled',
  };

  // Explorer statuses, which are not step statuses.
  var LAMP_BY_STATUS = { done: 'completed', running: 'running', unreadable: 'failed' };

  /**
   * @param {object} view
   * @param {string} status a step, task, dimension, or explorer status
   * @returns {string} completed | running | failed | stalled
   */
  function lampClass(view, status) {
    if (Object.prototype.hasOwnProperty.call(LAMP_BY_STATUS, status)) return LAMP_BY_STATUS[status];
    return LAMP_BY_TONE[view.stepGlyph(status).token] || 'stalled';
  }

  function lamp(doc, cls) {
    var node = el(doc, 'span', 'lamp ' + cls);
    node.setAttribute('aria-hidden', 'true');
    return node;
  }

  /**
   * A tile is open when the user's choice says so, else by its default.
   * @param {Object<string, boolean>} closed sectionKey -> true (user closed) or false (user opened)
   * @param {string} key a sectionKey value
   * @param {boolean} openByDefault
   * @returns {boolean}
   */
  function tileOpen(closed, key, openByDefault) {
    if (closed && Object.prototype.hasOwnProperty.call(closed, key)) return !closed[key];
    return openByDefault;
  }

  /**
   * One tile: details[data-section] with a summary of chevron, lamp, name,
   * and meta text.
   * @param {Document} doc
   * @param {string} cls class names of the details element
   * @param {string} section the data-section value
   * @param {string} lampCls
   * @param {string} meta
   * @param {boolean} open
   * @returns {Element} details
   */
  function tile(doc, cls, section, lampCls, meta, open) {
    var details = el(doc, 'details', cls);
    details.setAttribute('data-section', section);
    details.open = !!open;
    var chev = el(doc, 'span', 'chev', '▸');
    chev.setAttribute('aria-hidden', 'true');
    var summary = append(el(doc, 'summary', 'sec-head'), [
      chev,
      lamp(doc, lampCls),
      el(doc, 'span', 'sec-name', section),
      el(doc, 'span', 'sec-meta', meta),
    ]);
    details.appendChild(summary);
    return details;
  }

  function taskRow(doc, view, task) {
    var row = append(el(doc, 'div', 'task-row'), [lamp(doc, lampClass(view, task.status)), el(doc, 'span', 'task-id', task.id)]);
    if (task.name) {
      var name = el(doc, 'span', 'task-name', task.name);
      name.setAttribute('title', task.name);
      row.appendChild(name);
    }
    return row;
  }

  // The commit badge of a wave head, or null for a wave that has no commit state.
  function commitBadge(doc, view, wave, commitWaves) {
    var state = view.waveCommitState(wave, commitWaves);
    if (!state) return null;
    if (state === 'off') return el(doc, 'span', 'commit-badge', 'commits off');
    if (state === 'committed') {
      var sha = String(wave.committedSha);
      var badge = el(doc, 'span', 'commit-badge committed', 'committed ' + sha.slice(0, 7));
      badge.setAttribute('title', 'Wave commit ' + sha);
      return badge;
    }
    return el(doc, 'span', state === 'due' ? 'commit-badge pending-commit' : 'commit-badge', 'not committed');
  }

  /**
   * Waves: one block for each wave (`wave N`, `done/total`, commit state,
   * task rows), then the queued tasks. A planned wave that has not started
   * (status `pending`) is a block with its tasks and no commit state. The
   * queued block appears only when `queued` is not empty.
   * @param {Document} doc
   * @param {object} view
   * @param {{waves?: Array, queued?: Array}} detail
   * @param {{commitWaves?: boolean}} pipeline
   * @returns {Element} div.waves
   */
  function wavesBody(doc, view, detail, pipeline) {
    var out = el(doc, 'div', 'waves');
    (detail.waves || []).forEach(function (wave) {
      var tasks = wave.tasks || [];
      var counts = view.taskCounts(tasks);
      var head = append(el(doc, 'div', 'wave-head', 'wave ' + wave.number), [
        el(doc, 'span', 'wave-count', counts.done + '/' + counts.total),
        commitBadge(doc, view, wave, pipeline && pipeline.commitWaves),
      ]);
      var block = append(el(doc, 'div', 'wave-block'), [head]);
      tasks.forEach(function (task) {
        block.appendChild(taskRow(doc, view, task));
      });
      out.appendChild(block);
    });
    var queued = detail.queued || [];
    if (queued.length > 0) {
      var queuedBlock = append(el(doc, 'div', 'wave-block'), [
        append(el(doc, 'div', 'wave-head', 'queued'), [el(doc, 'span', 'wave-count', queued.length)]),
      ]);
      queued.forEach(function (task) {
        queuedBlock.appendChild(taskRow(doc, view, task));
      });
      out.appendChild(queuedBlock);
    }
    return out;
  }

  /**
   * The meta text of a dimension row: the run state while the dimension has
   * not finished, `skipped · <reason>` for a skipped one, else the finding
   * count.
   * @param {{status: string, findings?: number, reason?: string}} dim
   * @returns {string}
   */
  function dimensionMeta(dim) {
    if (dim.status === 'in_progress') return 'running';
    if (dim.status === 'pending') return 'queued';
    if (dim.status === 'skipped') return dim.reason ? 'skipped · ' + dim.reason : 'skipped';
    return plural(dim.findings || 0, 'finding', 'findings');
  }

  /**
   * One dimension row: lamp, name, and meta text. A count above 0 is amber.
   * @param {Document} doc
   * @param {object} view
   * @param {{name: string, status: string, findings?: number, reason?: string}} dim
   * @returns {Element} div.dim-row
   */
  function dimensionRow(doc, view, dim) {
    var meta = el(doc, 'span', (dim.findings || 0) > 0 ? 'dim-meta has-findings' : 'dim-meta', dimensionMeta(dim));
    return append(el(doc, 'div', 'dim-row dim-cols'), [lamp(doc, lampClass(view, dim.status)), el(doc, 'span', 'dim-name', dim.name), meta]);
  }

  /**
   * The detail key of one finding row. The index is the position of the
   * finding in its list, the same position view.detailItem reads.
   * @param {object} view
   * @param {{id?: string}} [pipeline]
   * @param {string} [dimension] review dimension name
   * @param {number} index
   * @returns {string} '' when the pipeline id or the dimension is missing
   */
  function findingKey(view, pipeline, dimension, index) {
    return view.detailKey('finding', null, { pipeline: pipeline && pipeline.id, dimension: dimension, index: index });
  }

  /**
   * A button that opens the detail viewer. The key goes in data-detail; a row
   * with no key has no data-detail, so a click on it opens nothing.
   * @param {Document} doc
   * @param {string} className
   * @param {string} key a view.detailKey value
   * @returns {Element} button
   */
  function detailButton(doc, className, key) {
    var button = el(doc, 'button', className);
    button.setAttribute('type', 'button');
    if (key) button.setAttribute('data-detail', key);
    return button;
  }

  /**
   * One finding of a review dimension: lamp by severity, text, and severity.
   * Findings of a ship review and of a standalone review use this row.
   * @param {Document} doc
   * @param {object} view
   * @param {{text: string, severity: string, file?: string, line?: string}} finding
   * @param {string} key view.detailKey('finding', ...); '' leaves out data-detail
   * @returns {Element} button.dim-row.dim-find
   */
  function findingRow(doc, view, finding, key) {
    var text = el(doc, 'span', 'dim-name', finding.text);
    var location = view.issueLocation(finding);
    if (location) text.setAttribute('title', location);
    return append(detailButton(doc, 'dim-row dim-find', key), [
      lamp(doc, LAMP_BY_TONE[view.severityTone(finding.severity)] || 'stalled'),
      text,
      el(doc, 'span', 'dim-meta', finding.severity),
    ]);
  }

  /**
   * A dimension row, then its finding rows in one div.dim-findings. A
   * dimension with no finding row gives the dimension row alone.
   * @returns {Array<Element>}
   */
  function dimensionBlock(doc, view, dim, pipeline) {
    var nodes = [dimensionRow(doc, view, dim)];
    var items = dim.findingItems || [];
    if (items.length > 0) {
      var list = el(doc, 'div', 'dim-findings');
      items.forEach(function (finding, i) {
        if (finding) list.appendChild(findingRow(doc, view, finding, findingKey(view, pipeline, dim.name, i)));
      });
      nodes.push(list);
    }
    return nodes;
  }

  /**
   * Dimensions: the review totals line, the review plan totals line, then
   * the dimension rows, each followed by its finding rows. Rows without a
   * wave come first as a flat list. Rows with a wave sit under one `Wave N`
   * heading for each wave, lowest first, in the order the rows arrive. The
   * lamp keeps the run state.
   * @param {Document} doc
   * @param {object} view
   * @param {{dimensions?: Array, reviewTotals?: object, reviewPlan?: object}} detail
   * @param {{id?: string}} [pipeline] the pipeline of the step, for the finding keys
   * @returns {Element} div
   */
  function dimensionsBody(doc, view, detail, pipeline) {
    var out = el(doc, 'div', '');
    if (detail.reviewTotals) out.appendChild(el(doc, 'div', 'round-sum', view.reviewTotalsText(detail.reviewTotals)));
    if (detail.reviewPlan) out.appendChild(el(doc, 'div', 'round-sum', view.reviewPlanText(detail.reviewPlan)));
    var flat = [];
    var groups = {};
    var numbers = [];
    (detail.dimensions || []).forEach(function (dim) {
      if (!(dim.wave > 0)) {
        flat.push(dim);
        return;
      }
      if (!groups[dim.wave]) {
        groups[dim.wave] = [];
        numbers.push(dim.wave);
      }
      groups[dim.wave].push(dim);
    });
    numbers.sort(function (a, b) {
      return a - b;
    });
    flat.forEach(function (dim) {
      append(out, dimensionBlock(doc, view, dim, pipeline));
    });
    numbers.forEach(function (number) {
      out.appendChild(el(doc, 'div', 'wave-head', 'Wave ' + number));
      groups[number].forEach(function (dim) {
        append(out, dimensionBlock(doc, view, dim, pipeline));
      });
    });
    return out;
  }

  // Findings listed under each explorer; the rest is the `N more` line.
  var EXPLORER_SAMPLES = 2;

  /**
   * Explorers: name, `N findings`, the first findings, and `N more`. A ref
   * is text, never a link, even when it is a URL. A ship plan step also
   * lists its review rounds: when `detail.rounds` is not empty, the rounds
   * table (roundsBody) follows under a `review rounds` heading, outside the
   * explorer grid.
   * @param {Document} doc
   * @param {object} view
   * @param {{explorers?: Array, rounds?: Array}} detail
   * @returns {Element} div.waves, or a div that holds div.waves and the rounds
   *   table when `detail.rounds` is not empty
   */
  function explorersBody(doc, view, detail) {
    var out = el(doc, 'div', 'waves');
    (detail.explorers || []).forEach(function (explorer) {
      var head = append(el(doc, 'div', 'wave-head'), [
        lamp(doc, lampClass(view, explorer.status)),
        el(doc, 'span', '', explorer.name),
        el(doc, 'span', 'count-badge', plural(explorer.total || 0, 'finding', 'findings')),
      ]);
      var block = append(el(doc, 'div', 'wave-block'), [head]);
      var shown = (explorer.findings || []).slice(0, EXPLORER_SAMPLES);
      shown.forEach(function (finding) {
        var text = el(doc, 'span', 'find-text', finding.summary);
        text.setAttribute('title', finding.summary || '');
        var row = append(el(doc, 'div', 'find-row'), [text]);
        if (finding.ref) row.appendChild(el(doc, 'span', 'find-ref', finding.ref));
        block.appendChild(row);
      });
      var more = view.explorerMore(explorer, shown.length);
      if (more > 0) block.appendChild(el(doc, 'div', 'find-more', more + ' more'));
      out.appendChild(block);
    });
    if ((detail.rounds || []).length === 0) return out;
    return append(el(doc, 'div', ''), [out, el(doc, 'div', 'wave-head', 'review rounds'), roundsBody(doc, view, detail)]);
  }

  function strongLine(doc, cls, parts) {
    var line = el(doc, 'div', cls);
    parts.forEach(function (part) {
      line.appendChild(part.strong ? el(doc, 'strong', '', part.text) : el(doc, 'span', '', part.text));
    });
    return line;
  }

  // The count with its unit word: the singular when n is 1.
  function countWord(n, one, many) {
    return ' ' + (n === 1 ? one : many);
  }

  /**
   * The totals line of the review rounds: `N iterations · V violations · F
   * fixes`, with `(sum)` at the end when the totals are sums of the per-round
   * counts and not counts of distinct findings. The server computes the
   * totals; the page only shows them.
   * @param {Document} doc
   * @param {{iterations: number, violations: number, fixes: number, distinct: boolean}} totals
   * @returns {Element} div.round-sum
   */
  function roundTotalsLine(doc, totals) {
    var iterations = totals.iterations || 0;
    var violations = totals.violations || 0;
    var fixes = totals.fixes || 0;
    return strongLine(doc, 'round-sum', [
      { strong: true, text: iterations },
      { text: countWord(iterations, 'iteration', 'iterations') + ' · ' },
      { strong: true, text: violations },
      { text: countWord(violations, 'violation', 'violations') + ' · ' },
      { strong: true, text: fixes },
      { text: countWord(fixes, 'fix', 'fixes') + (totals.distinct ? '' : ' (sum)') },
    ]);
  }

  /**
   * One answered finding of a plan review: `<choice> · <id> · <text> — <reason>`.
   * The ` — <reason>` part is left out when the reason is empty.
   * @param {Document} doc
   * @param {{id: string, text: string, choice: string, reason: string}} outcome
   * @returns {Element} div.find-row
   */
  function outcomeRow(doc, outcome) {
    var line = outcome.choice + ' · ' + outcome.id + ' · ' + outcome.text + (outcome.reason ? ' — ' + outcome.reason : '');
    var text = el(doc, 'span', 'find-text', line);
    text.setAttribute('title', line);
    return append(el(doc, 'div', 'find-row'), [text]);
  }

  /**
   * Rounds: the totals line from `roundTotals` (no line when the server sent
   * none), the `REPAIR LIMIT REACHED` flag, one row for each answered finding,
   * a head row, then one row for each round with one chip for each lens
   * verdict. The page does not add up the per-round counts.
   * @param {Document} doc
   * @param {object} view
   * @param {{rounds?: Array, roundTotals?: object, repairLimit?: boolean, outcomes?: Array}} detail
   * @returns {Element} div
   */
  function roundsBody(doc, view, detail) {
    var rounds = detail.rounds || [];
    var out = el(doc, 'div', '');
    if (detail.roundTotals) out.appendChild(roundTotalsLine(doc, detail.roundTotals));
    if (detail.repairLimit) {
      out.appendChild(append(el(doc, 'div', 'lens-chips'), [el(doc, 'span', 'lens-chip issues', 'REPAIR LIMIT REACHED')]));
    }
    (detail.outcomes || []).forEach(function (outcome) {
      out.appendChild(outcomeRow(doc, outcome));
    });
    out.appendChild(
      append(el(doc, 'div', 'round-row head'), [
        el(doc, 'span', '', 'round'),
        el(doc, 'span', '', 'issues found'),
        el(doc, 'span', '', 'fixed'),
        el(doc, 'span', '', 'lens verdicts'),
      ])
    );
    rounds.forEach(function (r) {
      var chips = el(doc, 'span', 'lens-chips');
      (r.lenses || []).forEach(function (lens) {
        var ok = lens.verdict === 'Approved';
        chips.appendChild(el(doc, 'span', ok ? 'lens-chip ok' : 'lens-chip issues', lens.name + ' · ' + (ok ? 'approved' : 'issues')));
      });
      out.appendChild(
        append(el(doc, 'div', 'round-row'), [
          el(doc, 'span', 'round-n', 'round ' + r.n),
          el(doc, 'span', '', r.found ? r.found : 'none'),
          el(doc, 'span', '', r.found ? r.fixed || 0 : '–'),
          chips,
        ])
      );
    });
    return out;
  }

  /**
   * Findings of one review dimension, one findingRow for each. A standalone
   * review step is one dimension, so the step name is the dimension name in
   * the finding keys.
   * @param {Document} doc
   * @param {object} view
   * @param {{findings?: Array}} detail
   * @param {{id?: string}} [pipeline] the pipeline of the step, for the finding keys
   * @param {{name?: string}} [step] the step of the detail
   * @returns {Element} div, or p.generic-line `No findings.`
   */
  function findingsBody(doc, view, detail, pipeline, step) {
    var findings = detail.findings || [];
    if (findings.length === 0) return el(doc, 'p', 'generic-line', 'No findings.');
    var out = el(doc, 'div', '');
    findings.forEach(function (finding, i) {
      out.appendChild(findingRow(doc, view, finding, findingKey(view, pipeline, step && step.name, i)));
    });
    return out;
  }

  /**
   * Guardrails of a plan setup station, on one line:
   * `N guardrails loaded (E error, W warning)`, or
   * `No plan guardrails configured` when none is loaded.
   * @param {Document} doc
   * @param {object} view
   * @param {{guardrails?: {total: number, error: number, warning: number}}} detail
   * @returns {Element} p.generic-line
   */
  function guardrailsBody(doc, view, detail) {
    var counts = detail.guardrails || {};
    var total = counts.total || 0;
    if (total === 0) return el(doc, 'p', 'generic-line', 'No plan guardrails configured');
    return el(
      doc,
      'p',
      'generic-line',
      plural(total, 'guardrail', 'guardrails') + ' loaded (' + (counts.error || 0) + ' error, ' + (counts.warning || 0) + ' warning)'
    );
  }

  /**
   * The result of a step, as one text line.
   * @param {Document} doc
   * @param {object} view
   * @param {{result?: string}} detail
   * @returns {Element} p.generic-line
   */
  function resultBody(doc, view, detail) {
    return el(doc, 'p', 'generic-line', detail.result);
  }

  // Body builder by detail kind: (doc, view, detail, pipeline, step) -> Element.
  var TILE_BODIES = {
    waves: wavesBody,
    dimensions: dimensionsBody,
    explorers: explorersBody,
    rounds: roundsBody,
    findings: findingsBody,
    guardrails: guardrailsBody,
    result: resultBody,
  };

  /**
   * The tile of one step with a section.
   * @param {Document} doc
   * @param {object} view
   * @param {{commitWaves?: boolean}} pipeline
   * @param {{name: string, status: string, detail: object}} step
   * @param {number} index the station index of the step
   * @param {boolean} open
   * @param {boolean} selected true for the step of the marked station
   * @returns {Element} details.step-sec with data-section set to the step name
   */
  function stepTile(doc, view, pipeline, step, index, open, selected) {
    var detail = step.detail || {};
    var cls = 'step-sec' + (view.isWideSection(detail) ? ' wide' : '') + (selected ? ' selected' : '');
    var details = tile(doc, cls, step.name, lampClass(view, step.status), view.sectionMeta(step), open);
    var body = TILE_BODIES[detail.kind];
    if (body) details.appendChild(body(doc, view, detail, pipeline, step));
    return details;
  }

  /**
   * One tile for each step with a section, in track order. A step with no
   * section gives no tile. Step tiles are open unless the user closed them.
   * @param {Document} doc
   * @param {object} view
   * @param {{steps?: Array}} pipeline
   * @param {{key: string, closed?: Object<string, boolean>, selected?: number}} state
   *   key is the pipelineKey of the block
   * @returns {Array<Element>}
   */
  function stepTiles(doc, view, pipeline, state) {
    var steps = (pipeline && pipeline.steps) || [];
    var out = [];
    for (var i = 0; i < steps.length; i++) {
      if (!view.hasSection(steps[i])) continue;
      var open = tileOpen(state && state.closed, view.sectionKey(state && state.key, steps[i].name), true);
      out.push(stepTile(doc, view, pipeline, steps[i], i, open, !!state && state.selected === i));
    }
    return out;
  }

  /**
   * The issues tile: `N open`, then one row for each issue: severity chip,
   * rationale, and location. The stalled issue shows the age of the last
   * update in place of a location. Each row is a button whose data-detail
   * key is the pipeline id and the row index.
   * @param {Document} doc
   * @param {object} view
   * @param {{issues?: Array, updatedAt?: string}} pipeline
   * @param {boolean} open
   * @param {Date|number} now
   * @param {string} [tz] IANA time zone; the local zone when absent
   * @returns {Element|null} null when the pipeline has no issues
   */
  function issuesTile(doc, view, pipeline, open, now, tz) {
    var issues = (pipeline && pipeline.issues) || [];
    if (issues.length === 0) return null;
    var hot = issues.some(function (issue) {
      return issue.severity === 'critical' || issue.severity === 'high';
    });
    var details = tile(doc, 'step-sec span2', 'issues', hot ? 'failed' : 'running', issues.length + ' open', open);
    issues.forEach(function (issue, i) {
      var main = append(el(doc, 'div', 'issue-main'), [el(doc, 'div', 'issue-text', issue.text)]);
      var location = view.issueLocation(issue);
      if (issue.source === 'pipeline') {
        var age = view.relativeWhen(pipeline.updatedAt, now, tz);
        location = age ? 'last update ' + age : '';
      }
      if (location) main.appendChild(el(doc, 'div', 'issue-path', location));
      var row = detailButton(doc, 'issue-row', view.detailKey('issue', null, { pipeline: pipeline.id, index: i }));
      details.appendChild(append(row, [el(doc, 'span', 'sev sev-' + issue.severity, issue.severity), main]));
    });
    return details;
  }

  // Mark of the largest command group in the group table.
  var MAJORITY_GLYPH = '◆';

  // 0.67 -> '67%'. The server rounds the share to 2 decimals.
  function sharePercent(share) {
    return Math.round((share || 0) * 100) + '%';
  }

  /**
   * The command group table of a session: one row for each group with the
   * label, the count, the share, and a mark on the majority group. The
   * server sends the groups largest first and sets `majority`; the page
   * does not sort or count.
   * @param {Document} doc
   * @param {Array<{label: string, count: number, share: number, majority: boolean}>} groups
   * @returns {Element|null} table.cmd-groups, or null for no groups
   */
  function commandGroupTable(doc, groups) {
    var list = groups || [];
    if (list.length === 0) return null;
    var headRow = el(doc, 'tr', '');
    ['command', 'count', 'share', 'majority'].forEach(function (name) {
      headRow.appendChild(el(doc, 'th', '', name));
    });
    var body = el(doc, 'tbody', '');
    list.forEach(function (group) {
      var mark = el(doc, 'td', 'cg-mark');
      if (group.majority) {
        var glyph = el(doc, 'span', 'cg-glyph', MAJORITY_GLYPH);
        glyph.setAttribute('aria-hidden', 'true');
        append(mark, [glyph, el(doc, 'span', 'sr-only', 'yes')]);
      }
      body.appendChild(
        append(el(doc, 'tr', group.majority ? 'cg-row majority' : 'cg-row'), [
          el(doc, 'td', 'cg-label', group.label),
          el(doc, 'td', 'cg-count', group.count),
          el(doc, 'td', 'cg-share', sharePercent(group.share)),
          mark,
        ])
      );
    });
    return append(el(doc, 'table', 'cmd-groups'), [append(el(doc, 'thead', ''), [headRow]), body]);
  }

  /**
   * The session tile: closed unless the user opened it. Counts in the
   * summary; open, the short id, the command group table (only when the
   * session ran commands), then one row for each timeline event.
   * @param {Document} doc
   * @param {object} view
   * @param {{id: string, active?: boolean, counts?: object, commandGroups?: Array, timeline?: Array}|null} session
   * @param {boolean} open
   * @param {string} [tz] IANA time zone; the local zone when absent
   * @returns {Element|null} null when there is no session
   */
  function sessionTile(doc, view, session, open, tz) {
    if (!session) return null;
    var counts = session.counts || {};
    var meta = [
      plural(counts.prompts || 0, 'prompt', 'prompts'),
      plural(counts.commands || 0, 'command', 'commands'),
      plural(counts.mcpCalls || 0, 'mcp call', 'mcp calls'),
    ].join(' · ');
    var details = tile(doc, 'step-sec span2', 'session', session.active ? 'running' : 'stalled', meta, open);
    var id = el(doc, 'div', 'sec-id', 'id ' + view.shortId(session.id));
    id.setAttribute('title', session.id || '');
    append(details, [id, commandGroupTable(doc, session.commandGroups)]);
    (session.timeline || []).forEach(function (event) {
      details.appendChild(
        append(el(doc, 'div', 'timeline-row'), [
          el(doc, 'span', 't-time', view.clockLabel(event.at, tz)),
          el(doc, 'span', 't-kind', event.kind),
          el(doc, 'span', 't-text', event.text),
        ])
      );
    });
    return details;
  }

  /**
   * One pipeline block: the wait banner (only with an attention), the head,
   * then the track panel with the track and div.step-detail: the step tiles,
   * the issues tile, and the session tile. A block with an attention has the
   * class `attn`.
   * @param {Document} doc
   * @param {object} view
   * @param {{root: string, name: string, sessions?: Array}} repo
   * @param {object} pipeline
   * @param {{collapsed: boolean, selected: number, closed?: Object<string, boolean>, now?: (Date|number), tz?: string}} state
   *   closed holds the user's tile choices by sectionKey: true closed, false opened
   * @returns {Element} article.pipe-block with data-key and data-id
   */
  function pipelineBlock(doc, view, repo, pipeline, state) {
    var collapsed = !!(state && state.collapsed);
    var selected = state && typeof state.selected === 'number' ? state.selected : view.defaultStationIndex(pipeline.steps);
    var closed = (state && state.closed) || {};
    var now = state && state.now != null ? state.now : Date.now();
    var tz = state && state.tz;
    var session = view.pickSession(pipeline, repo.sessions);
    var key = view.pipelineKey(repo, pipeline);

    var cls = 'pipe-block' + (collapsed ? ' collapsed' : '') + (pipeline.attention ? ' attn' : '');
    var block = el(doc, 'article', cls);
    block.setAttribute('data-key', key);
    block.setAttribute('data-id', pipeline.id);

    var detail = append(el(doc, 'div', 'step-detail'), stepTiles(doc, view, pipeline, { key: key, closed: closed, selected: selected }));
    append(detail, [
      issuesTile(doc, view, pipeline, tileOpen(closed, view.sectionKey(key, 'issues'), true), now, tz),
      sessionTile(doc, view, session, tileOpen(closed, view.sectionKey(key, 'session'), false), tz),
    ]);

    var panel = el(doc, 'div', 'track-panel');
    var wrap = append(el(doc, 'div', 'detail-wrap'), [detail]);
    append(panel, [stationTrack(doc, view, pipeline, selected), wrap]);

    // The wait banner comes first, so a collapsed block still shows it.
    if (pipeline.attention) append(block, attentionRows(doc, view, pipeline.attention, now));
    return append(block, [blockHead(doc, view, repo, pipeline, collapsed, view.tileCount(pipeline, session)), panel]);
  }

  /**
   * The History tab: `Finished runs (n)` and one table of the runs of the
   * repos in scope, newest first. Rows do not open.
   * @param {Document} doc
   * @param {object} view
   * @param {Array} repos
   * @param {Set<string>} scope
   * @param {Date|number} now
   * @param {string} [tz] IANA time zone; the local zone when absent
   * @returns {Element} section.list-panel.hist-panel
   */
  function historyTable(doc, view, repos, scope, now, tz) {
    var runs = [];
    (repos || []).forEach(function (repo) {
      if (!view.inScope(scope, repo.root)) return;
      (repo.history || []).forEach(function (run) {
        runs.push({ repo: repo, run: run });
      });
    });
    // ISO timestamps sort in time order. Array.prototype.sort is stable.
    runs.sort(function (a, b) {
      var x = a.run.endedAt || '';
      var y = b.run.endedAt || '';
      return x < y ? 1 : x > y ? -1 : 0;
    });

    var panel = el(doc, 'section', 'list-panel hist-panel');
    var heading = el(doc, 'h2', 'list-title', 'Finished runs ');
    heading.appendChild(el(doc, 'span', 'n', '(' + runs.length + ')'));
    panel.appendChild(heading);
    if (runs.length === 0) return append(panel, [emptyState(doc, 'no-history')]);

    var headRow = el(doc, 'tr', '');
    ['outcome', 'kind', 'branch', 'repo', 'finished', 'duration'].forEach(function (name) {
      headRow.appendChild(el(doc, 'th', '', name));
    });
    var body = el(doc, 'tbody', '');
    runs.forEach(function (item) {
      var run = item.run;
      var glyph = el(doc, 'span', '', view.outcomeGlyph(run.outcome));
      glyph.setAttribute('aria-hidden', 'true');
      var outcome = append(el(doc, 'span', 'out ' + run.outcome), [glyph, el(doc, 'span', '', run.outcome)]);
      var repoCell = el(doc, 'td', 'h-repo', item.repo.name);
      repoCell.setAttribute('title', item.repo.root);
      var when = el(doc, 'td', 'h-when', view.relativeWhen(run.endedAt, now, tz));
      when.setAttribute('title', run.endedAt || '');
      body.appendChild(
        append(el(doc, 'tr', ''), [
          append(el(doc, 'td', ''), [outcome]),
          el(doc, 'td', 'h-kind', run.kind),
          el(doc, 'td', 'h-branch', run.branch),
          repoCell,
          when,
          el(doc, 'td', 'h-dur', view.formatDuration(run.durationMs)),
        ])
      );
    });
    var table = append(el(doc, 'table', 'hist'), [append(el(doc, 'thead', ''), [headRow]), body]);
    return append(panel, [table]);
  }

  /**
   * Header counts over every repo; the repo filter does not apply.
   * @param {Document} doc
   * @param {object} view
   * @param {Array} repos
   * @returns {Array<Element>} four spans: running, stalled, failed, waiting
   */
  function headerTotals(doc, view, repos) {
    var counts = view.headerCounts(repos);
    return [
      ['c-run', 'running', counts.running],
      ['c-stall', 'stalled', counts.stalled],
      ['c-fail', 'failed', counts.failed],
      ['c-wait', 'waiting', counts.waiting],
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

  // One activity row, a button: the list keeps the first 200 characters, and
  // the detail viewer shows the full text.
  function activityRow(doc, view, chip, text, meta, key) {
    var main = el(doc, 'div', 'act-main');
    var body = el(doc, 'div', 'act-text', view.cutText(text, 200));
    append(main, [body, el(doc, 'div', 'act-meta', meta)]);
    return append(detailButton(doc, 'act-row', key), [chip, main]);
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
   * both over the repos in scope. Each row is a button with a data-detail
   * key (view.detailKey) that opens the item in the detail viewer.
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
            metaLine([repo.name, item.id]),
            view.detailKey('deferred', item)
          )
        );
      });
      (repo.learnings || []).forEach(function (item) {
        learnings.push(
          activityRow(
            doc,
            view,
            el(doc, 'span', 'sev', 'learning'),
            item.heading,
            metaLine([repo.name, item.branch]),
            view.detailKey('learning', item)
          )
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
   * The body of the detail viewer for one item of view.detailItem: the title,
   * the metadata list, and the full text. Every value goes in through
   * textContent. The text keeps its line breaks through the class
   * `detail-text` in app.css, never through an inline style.
   * @param {Document} doc
   * @param {{title?: string, text?: string, meta?: Array<Array<string>>}} item
   *   meta is a list of [label, value] pairs
   * @returns {Element} div.detail-body: h2.detail-heading (tabindex -1, so
   *   a script can focus it), dl.detail-meta (left out when meta is empty),
   *   and div.detail-text (always there, so a learning body can fill it
   *   later)
   */
  function detailBody(doc, item) {
    var it = item || {};
    var heading = el(doc, 'h2', 'detail-heading', it.title);
    heading.setAttribute('tabindex', '-1');
    var pairs = it.meta || [];
    var meta = null;
    if (pairs.length > 0) {
      meta = el(doc, 'dl', 'detail-meta');
      pairs.forEach(function (pair) {
        append(meta, [el(doc, 'dt', '', pair[0]), el(doc, 'dd', '', pair[1])]);
      });
    }
    return append(el(doc, 'div', 'detail-body'), [heading, meta, el(doc, 'div', 'detail-text', it.text)]);
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
    elapsedText: elapsedText,
    attentionRows: attentionRows,
    scopedTitle: scopedTitle,
    tickElapsed: tickElapsed,
    pipelineBlock: pipelineBlock,
    headerTotals: headerTotals,
    activityPanel: activityPanel,
    emptyState: emptyState,
    TILE_BODIES: TILE_BODIES,
    stepTile: stepTile,
    stepTiles: stepTiles,
    wavesBody: wavesBody,
    dimensionsBody: dimensionsBody,
    explorersBody: explorersBody,
    roundsBody: roundsBody,
    findingsBody: findingsBody,
    findingRow: findingRow,
    commandGroupTable: commandGroupTable,
    detailBody: detailBody,
    guardrailsBody: guardrailsBody,
    resultBody: resultBody,
    issuesTile: issuesTile,
    sessionTile: sessionTile,
    historyTable: historyTable,
  };

  if (typeof module === 'object' && module.exports) {
    module.exports = api;
  } else {
    root.sdlcRender = api;
  }
})(this);
