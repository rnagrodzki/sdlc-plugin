/**
 * DOM glue for the sdlc dashboard page. Every decision (status text,
 * sorting, glyphs, stop request, tab and filter state) is a call into
 * window.sdlcView (view.js). Snapshot text goes into the page through
 * textContent only, never innerHTML.
 */
'use strict';

// Event wiring and page state only. The DOM builders, the tab title, and the
// elapsed-time refresh are in render.js (window.sdlcRender). A DOM builder and
// tickElapsed take the document as their first argument; scopedTitle takes
// view first and reads no document.
var view = window.sdlcView;
var draw = window.sdlcRender;

var TAB_KEYS = ['ArrowLeft', 'ArrowRight', 'Home', 'End'];

// The tab title as served. A waiting count goes in front of it.
var BASE_TITLE = document.title;

// How often the elapsed time of each wait banner refreshes, in ms.
var ELAPSED_TICK_MS = 1000;

// State kept across render(). collapsed and selected hold the user's
// choice only; a pipeline with no entry follows the view.js default.
var ui = {
  tab: view.loadTab(storage()),
  scope: view.loadRepoFilter(storage()),
  collapsed: Object.create(null), // pipelineKey -> bool
  // sectionKey -> true when the user closed that tile, false when the user
  // opened it. A tile with no entry follows its default: step and issues
  // tiles open, the session tile closed.
  closed: Object.create(null),
  selected: Object.create(null), // pipelineKey -> station index
  focusKey: null,
  scrollTop: 0,
  lastSnapshot: null,
  source: null,
  hash: null, // a pipeline hash that waits for the first snapshot
  // The detail key of the open detail viewer, or null. The viewer reads its
  // item again from each snapshot, so it never holds a row element.
  detail: null,
  detailSig: '', // JSON of the item the viewer shows, so an equal item rebuilds nothing
  detailSeq: 0, // grows on each open and close; a late learning body of an older open is dropped
};
// The filter and sort state of the tabs: historyOutcome, historySort, and more.
// render() never resets it, so a data refresh keeps the choice.
Object.assign(ui, view.defaultUi());

// The title of the detail viewer when its item is not in the snapshot any more.
var GONE_TEXT = 'This item is no longer open.';

// The questions of the confirm dialog, by the steps of view.confirmSteps.
var CONFIRM_PROMPT = {
  archive: function (runId) {
    return 'Archive run ' + runId + '?';
  },
  stalled: function () {
    return 'The run stalled. Archive removes its resume point.';
  },
  clear: function () {
    return 'Clear cache files?';
  },
};

// The confirm flow that is on screen, or null:
// { prompts, step, label, run, busy, returnKey }.
var confirmFlow = null;

// The text of the confirm dialog when flow.run() throws.
var CONFIRM_FAILED_TEXT = 'The request could not be sent. Close this dialog and try again.';

// Blocks of the last render: { key, pipeline, node }, in feed order.
var blocks = [];

var stage = document.querySelector('.stage');
var tabs = {
  pipelines: { tab: byId('tab-pipelines'), panel: byId('panel-pipelines'), count: byId('n-pipelines') },
  activity: { tab: byId('tab-activity'), panel: byId('panel-activity'), count: byId('n-activity') },
  history: { tab: byId('tab-history'), panel: byId('panel-history'), count: byId('n-history') },
  preplans: { tab: byId('tab-preplans'), panel: byId('panel-preplans'), count: byId('n-preplans') },
};

function storage() {
  // window.localStorage can throw (file://, blocked storage). view.js
  // treats a storage that throws as empty.
  try {
    return window.localStorage;
  } catch (e) {
    return null;
  }
}

function byId(id) {
  return document.getElementById(id);
}

function replaceChildren(parent, nodes) {
  parent.replaceChildren.apply(parent, nodes);
}

function reducedMotion() {
  return !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches);
}

function setConnection(state) {
  byId('conn').setAttribute('data-state', state);
  byId('conn-text').textContent = view.connectionText(state);
}

function connect() {
  ui.source = new EventSource('/api/events');
  ui.source.addEventListener('open', function () {
    setConnection('open');
  });
  ui.source.addEventListener('error', function () {
    setConnection('error');
  });
  ui.source.addEventListener('snapshot', function (event) {
    setConnection('snapshot');
    render(JSON.parse(event.data));
  });
}

// --- Scroll and focus kept across a rebuild ----------------------------------

function saveScroll() {
  ui.scrollTop = stage.scrollTop;
}

function restoreScroll() {
  stage.scrollTop = ui.scrollTop;
}

// A key that names the same control in the next rebuild, or null.
function focusKeyOf(node) {
  if (!node || !node.closest) return null;
  var block = node.closest('[data-key]');
  var prefix = block ? block.getAttribute('data-key') + '\n' : '';
  if (node.hasAttribute('data-root')) return 'chip\n' + node.getAttribute('data-root');
  // A row that opens the detail viewer, and an Archive button, are rebuilt on each snapshot too.
  if (node.hasAttribute('data-detail')) return 'detail\n' + node.getAttribute('data-detail');
  if (node.hasAttribute('data-archive')) {
    return 'archive\n' + node.getAttribute('data-repo') + '\n' + node.getAttribute('data-archive');
  }
  if (node.hasAttribute('data-hist-outcome')) return 'hist-outcome\n' + node.getAttribute('data-hist-outcome');
  if (node.hasAttribute('data-hist-sort')) return 'hist-sort\n' + node.getAttribute('data-hist-sort');
  if (node.hasAttribute('data-act-priority')) return 'act-priority\n' + node.getAttribute('data-act-priority');
  if (node.hasAttribute('data-pp-status')) return 'pp-status\n' + node.getAttribute('data-pp-status');
  if (node.hasAttribute('data-kind')) {
    return 'delete\n' + node.getAttribute('data-kind') + '\n' + node.getAttribute('data-repo') + '\n' + node.getAttribute('data-label');
  }
  if (node.hasAttribute('data-station')) return prefix + 'station\n' + node.getAttribute('data-station');
  if (node.classList.contains('fold-btn')) return prefix + 'fold';
  var tile = node.closest('[data-section]');
  if (tile && block) return prefix + 'tile\n' + tile.getAttribute('data-section');
  return null;
}

function saveFocus() {
  ui.focusKey = focusKeyOf(document.activeElement);
}

function restoreFocus() {
  if (!ui.focusKey) return;
  var active = document.activeElement;
  if (active && active !== document.body && document.contains(active)) return;
  var candidates = document.querySelectorAll(
    '[data-root], [data-station], .fold-btn, [data-section] > summary, [data-detail], [data-archive], ' +
      '[data-hist-outcome], [data-hist-sort], [data-act-priority], [data-pp-status], [data-kind]'
  );
  for (var i = 0; i < candidates.length; i++) {
    if (focusKeyOf(candidates[i]) === ui.focusKey) {
      candidates[i].focus({ preventScroll: true });
      return;
    }
  }
}

// --- Render -------------------------------------------------------------------

// The saved scope without roots the snapshot no longer has, so a stale root
// cannot hide every repo while no chip shows as pressed.
function activeScope(repos) {
  var scope = new Set();
  repos.forEach(function (repo) {
    if (ui.scope.has(repo.root)) scope.add(repo.root);
  });
  return scope;
}

function isCollapsed(key, pipeline) {
  return key in ui.collapsed ? ui.collapsed[key] : view.defaultCollapsed(pipeline);
}

function selectedIndex(key, pipeline) {
  var steps = pipeline.steps || [];
  var index = ui.selected[key];
  return typeof index === 'number' && index < steps.length ? index : view.defaultStationIndex(steps);
}

// The IANA time zone of the browser; undefined lets Intl use the local zone.
function timeZone() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch (e) {
    return undefined;
  }
}

// Sets the tab title: "(N) " in front of the base title when N pipelines of
// the repos in scope wait for a person.
function syncTitle(repos, scope) {
  document.title = draw.scopedTitle(view, BASE_TITLE, repos, scope);
}

// Rewrites the elapsed time of every wait banner from its data-asked value.
// A banner with an unreadable time keeps its text.
function tickElapsed() {
  draw.tickElapsed(document, view, Date.now());
}

// The review dimension cards sit in columns of near equal height. The
// observer fires when a grid first has a width (its tile opens) and when the
// width changes, and the packing needs the real card heights.
var dimObserver =
  typeof ResizeObserver === 'function'
    ? new ResizeObserver(function (entries) {
        entries.forEach(function (entry) {
          draw.packDimensionGrid(entry.target, view);
        });
      })
    : null;

function watchDimensionGrids() {
  if (!dimObserver) return;
  dimObserver.disconnect();
  Array.prototype.forEach.call(byId('feed').querySelectorAll('.dim-grid'), function (grid) {
    dimObserver.observe(grid);
  });
}

function renderFeed(snapshot, repos, scope, now, tz) {
  var nodes = [];
  var pipelines = [];
  var owner = new Map();
  blocks = [];

  repos.forEach(function (repo) {
    if (!view.inScope(scope, repo.root)) return;
    if (repo.error) nodes.push(draw.emptyState(document, 'repo-error', repo));
    (repo.warnings || []).forEach(function (w) {
      nodes.push(draw.emptyState(document, 'repo-warning', { name: repo.name, warning: w }));
    });
    (repo.pipelines || []).forEach(function (p) {
      pipelines.push(p);
      owner.set(p, repo);
    });
  });

  view.feedOrder(pipelines).forEach(function (p) {
    var repo = owner.get(p);
    var key = view.pipelineKey(repo, p);
    var node = draw.pipelineBlock(document, view, repo, p, {
      collapsed: isCollapsed(key, p),
      selected: selectedIndex(key, p),
      closed: ui.closed,
      now: now,
      tz: tz,
    });
    blocks.push({ key: key, pipeline: p, node: node });
    nodes.push(node);
  });

  if (blocks.length === 0) {
    var empty = view.emptyText(snapshot);
    nodes.push(empty ? draw.emptyState(document, 'no-pipelines', empty) : draw.emptyState(document, 'none-in-scope'));
  }
  replaceChildren(byId('feed'), nodes);
  watchDimensionGrids();
}

// Draws one snapshot: the tab title, the header counts, the filter chips, the
// tab counts, and the four panels. The scroll and focus survive the rebuild.
function render(snapshot) {
  ui.lastSnapshot = snapshot || { repos: [] };
  var repos = ui.lastSnapshot.repos || [];
  var scope = activeScope(repos);

  saveScroll();
  saveFocus();

  // A filter change calls render too, so the title follows the filter.
  syncTitle(repos, scope);
  replaceChildren(byId('totals'), draw.headerTotals(document, view, repos));
  replaceChildren(byId('filter-chips'), draw.filterChips(document, view, view.repoChips(repos), scope));

  var counts = view.scopedCounts(repos, scope);
  view.TAB_NAMES.forEach(function (name) {
    tabs[name].count.textContent = String(counts[name]);
  });

  var now = Date.now();
  var tz = timeZone();
  renderFeed(ui.lastSnapshot, repos, scope, now, tz);
  replaceChildren(tabs.preplans.panel, [draw.preplanPanel(document, view, repos, scope, ui)]);
  replaceChildren(tabs.activity.panel, [draw.activityPanel(document, view, repos, scope, ui)]);
  replaceChildren(tabs.history.panel, [draw.historyTable(document, view, repos, scope, now, tz, ui)]);
  syncToggleAll();

  restoreFocus();
  restoreScroll();
  refreshDetail();

  if (ui.hash !== null) {
    var hash = ui.hash;
    ui.hash = null;
    applyHash(hash);
  }
}

// --- Tabs, filter, blocks, stations -------------------------------------------

function selectTab(name, focus) {
  if (!tabs[name]) return;
  var changed = name !== ui.tab;
  view.TAB_NAMES.forEach(function (t) {
    var on = t === name;
    tabs[t].tab.setAttribute('aria-selected', String(on));
    tabs[t].tab.setAttribute('tabindex', on ? '0' : '-1');
    tabs[t].panel.hidden = !on;
  });
  ui.tab = name;
  view.saveTab(storage(), name);
  syncToggleAll();
  if (changed) stage.scrollTop = 0;
  if (focus) tabs[name].tab.focus();
}

// root '' is the All chip: it clears the scope. Any other root toggles.
function applyScope(root) {
  if (!root) {
    ui.scope.clear();
  } else if (ui.scope.has(root)) {
    ui.scope.delete(root);
  } else {
    ui.scope.add(root);
  }
  view.saveRepoFilter(storage(), ui.scope);
  render(ui.lastSnapshot);
}

function blockOf(key) {
  for (var i = 0; i < blocks.length; i++) {
    if (blocks[i].key === key) return blocks[i];
  }
  return null;
}

function anyOpen() {
  return blocks.some(function (b) {
    return !b.node.classList.contains('collapsed');
  });
}

function syncToggleAll() {
  var button = byId('toggle-all');
  button.textContent = view.toggleAllLabel(anyOpen());
  button.hidden = ui.tab !== 'pipelines' || blocks.length === 0;
}

function setCollapsed(key, collapsed) {
  var block = blockOf(key);
  if (!block) return;
  ui.collapsed[key] = collapsed;
  block.node.classList.toggle('collapsed', collapsed);
  block.node.querySelector('.fold-btn').setAttribute('aria-expanded', String(!collapsed));
  syncToggleAll();
}

// The tile of a step: a node with data-section set to the step name inside
// the block's .step-detail.
function tileOf(node, name) {
  var tiles = node.querySelectorAll('.step-detail [data-section]');
  for (var i = 0; i < tiles.length; i++) {
    if (tiles[i].getAttribute('data-section') === name) return tiles[i];
  }
  return null;
}

// Marks station index and its tile. With reveal, it also expands the
// block, opens the tile, and scrolls the tile into view.
function selectStation(key, index, reveal) {
  var block = blockOf(key);
  if (!block) return;
  var steps = block.pipeline.steps || [];
  if (index < 0 || index >= steps.length) return;
  ui.selected[key] = index;

  var stations = block.node.querySelectorAll('.station');
  for (var i = 0; i < stations.length; i++) {
    stations[i].classList.toggle('selected', i === index);
  }
  var name = steps[index].name;
  var tiles = block.node.querySelectorAll('.step-detail [data-section]');
  for (var j = 0; j < tiles.length; j++) {
    tiles[j].classList.toggle('selected', tiles[j].getAttribute('data-section') === name);
  }

  if (!reveal) return;
  setCollapsed(key, false);
  var tile = tileOf(block.node, name);
  if (!tile) return;
  if (tile.tagName === 'DETAILS') tile.open = true;
  delete ui.closed[view.sectionKey(key, name)];
  tile.scrollIntoView({ block: 'nearest', behavior: reducedMotion() ? 'auto' : 'smooth' });
}

// '#activity', '#history' and '#preplans' pick a tab. '#<pipeline id>[/<n>]'
// scrolls to the first block with that id and selects station n.
function applyHash(hash) {
  var target = view.parseHash(hash, view.TAB_NAMES);
  if (!target) return;
  if (target.tab) {
    selectTab(target.tab);
    return;
  }
  if (!ui.lastSnapshot) {
    ui.hash = hash;
    return;
  }
  var block = null;
  for (var i = 0; i < blocks.length; i++) {
    if (blocks[i].pipeline.id === target.pipelineId) {
      block = blocks[i];
      break;
    }
  }
  if (!block) return;
  selectTab('pipelines');
  block.node.scrollIntoView({ block: 'start', behavior: 'auto' });
  if (target.station !== null) selectStation(block.key, target.station, true);
}

// --- Stop flow ------------------------------------------------------------------

// The token the server put in the page. '' when the page has none.
function pageToken() {
  var meta = document.querySelector('meta[name="sdlc-token"]');
  return meta ? meta.getAttribute('content') : '';
}

function stopServer() {
  var request = view.stopRequest(pageToken());
  var status = request
    ? fetch(request.url, request.init).then(function (response) {
        return response.status;
      })
    : Promise.resolve(0);
  return status
    .catch(function () {
      return 0;
    })
    .then(function (code) {
      var result = view.stopResult(code);
      if (result === 'stopped') {
        if (ui.source) ui.source.close();
        byId('stop-btn').disabled = true;
      }
      setConnection(result);
    });
}

// --- Requests ---------------------------------------------------------------------

// Reads one response as { status, body }. A response with no JSON has a null
// body. A network error has status 0 and a null body.
function fetchJSON(url, init) {
  return fetch(url, init)
    .then(function (response) {
      return response.json().then(
        function (body) {
          return { status: response.status, body: body };
        },
        function () {
          return { status: response.status, body: null };
        }
      );
    })
    .catch(function () {
      return { status: 0, body: null };
    });
}

// Sends a request of view.archiveRequest, view.clearRequest, or view.deleteRequest. A null request
// (the page has no token) gives the same result as a network error.
function post(request) {
  return request ? fetchJSON(request.url, request) : Promise.resolve({ status: 0, body: null });
}

// --- Detail viewer ----------------------------------------------------------------

function metaValue(item, label) {
  var pairs = item.meta || [];
  for (var i = 0; i < pairs.length; i++) {
    if (pairs[i][0] === label) return pairs[i][1];
  }
  return '';
}

// Puts the text in the text part of the open viewer, if it has one.
function setDetailText(text) {
  var node = byId('detail-content').querySelector('.detail-text');
  if (node) node.textContent = text;
}

// Fills the viewer. #detail-title is the one visible title, the focus target,
// and the name of the dialog, so the heading that detailBody builds is dropped
// before the body is mounted. A null item shows GONE_TEXT and no body.
function showDetail(item) {
  byId('detail-title').textContent = item ? item.title : GONE_TEXT;
  if (!item) {
    replaceChildren(byId('detail-content'), []);
    return;
  }
  var body = draw.detailBody(document, item);
  var heading = body.querySelector('.detail-heading');
  if (heading) body.removeChild(heading);
  replaceChildren(byId('detail-content'), [body]);
}

// The body of a learning is not in the snapshot. GET /api/learning returns it.
function loadLearning(item) {
  var seq = ui.detailSeq;
  var url = view.learningUrl(item.repo, metaValue(item, 'Date'), item.title);
  if (!url) {
    setDetailText('The body of this learning cannot be loaded.');
    return;
  }
  fetchJSON(url).then(function (result) {
    if (seq !== ui.detailSeq) return;
    if (result.status === 200 && result.body && result.body.found) {
      setDetailText(result.body.body);
    } else if (result.status === 200) {
      setDetailText('The body of this learning was not found.');
    } else {
      setDetailText(view.errorText(result.body, 'The learning cannot be loaded (HTTP ' + result.status + ').'));
    }
  });
}

// Reads the item of the open viewer again from the last snapshot. An item
// that did not change rebuilds nothing, so a learning body stays on screen.
function refreshDetail(force) {
  if (ui.detail === null) return;
  var item = view.detailItem(ui.lastSnapshot, ui.detail);
  var sig = JSON.stringify(item);
  if (!force && sig === ui.detailSig) return;
  ui.detailSig = sig;
  showDetail(item);
  if (item && item.kind === 'learning') loadLearning(item);
}

function openDetail(key) {
  var dialog = byId('detail-dialog');
  ui.detail = key;
  ui.detailSeq++;
  refreshDetail(true);
  if (!dialog.open) dialog.showModal();
  byId('detail-title').focus();
}

// Runs when the viewer closed, by Close or by Esc. The focus goes to the
// rebuilt row with the same key, else to the Activity tab button.
function closeDetail() {
  var key = ui.detail || '';
  ui.detail = null;
  ui.detailSig = '';
  ui.detailSeq++;
  var row = key ? document.querySelector(view.closeFocusSelector(key)) : null;
  if (row) row.focus();
  if (!row || document.activeElement !== row) {
    document.querySelector(view.closeFocusSelector('')).focus();
  }
}

// --- Confirm dialog: archive and clear -------------------------------------------

// Sets the text of a node. A '\n' becomes a line break; every line goes in as a text node.
function setLines(node, text) {
  var nodes = [];
  text.split('\n').forEach(function (line, index) {
    if (index > 0) nodes.push(document.createElement('br'));
    nodes.push(document.createTextNode(line));
  });
  replaceChildren(node, nodes);
}

// Opens the confirm dialog with the first question of the flow. Each Confirm
// click moves to the next question. The last Confirm click calls flow.run(),
// which returns a promise of the text to show, or of null when the flow is
// done and the dialog closes.
//   flow = { prompts: [string], label: string, run: function(): Promise<string|null>,
//            returnKey?: string }
// returnKey is the focusKeyOf of the control that opened the dialog. A control
// that the next snapshot rebuilds cannot take the focus back from the browser,
// so the close handler finds the rebuilt control by this key.
function confirmAction(flow) {
  if (flow.prompts.length === 0) return;
  var ok = byId('confirm-ok');
  var cancel = byId('confirm-cancel');
  confirmFlow = {
    prompts: flow.prompts,
    step: 0,
    label: flow.label,
    run: flow.run,
    busy: false,
    returnKey: flow.returnKey || null,
  };
  ok.textContent = flow.label;
  ok.hidden = false;
  ok.disabled = false;
  cancel.textContent = 'Cancel';
  cancel.disabled = false;
  setLines(byId('confirm-text'), flow.prompts[0]);
  byId('confirm-dialog').showModal();
}

// A click on Confirm: the next question, or the request. The second click of a
// double click (event.detail > 1) is not a new answer: it must not confirm the
// stalled question that the first click just showed. Enter and Space give 0.
// The mouse press of that second click put the focus back on Confirm, so the
// focus goes to Cancel again.
function confirmStep(event) {
  if (event && event.detail > 1) {
    if (confirmFlow && !confirmFlow.busy) byId('confirm-cancel').focus();
    return;
  }
  var flow = confirmFlow;
  if (!flow || flow.busy) return;
  var ok = byId('confirm-ok');
  var cancel = byId('confirm-cancel');
  if (flow.step + 1 < flow.prompts.length) {
    flow.step++;
    setLines(byId('confirm-text'), flow.prompts[flow.step]);
    // The focus leaves Confirm, so a repeated Enter or Space cannot answer this question.
    cancel.focus();
    return;
  }
  flow.busy = true;
  ok.disabled = true;
  cancel.disabled = true;
  setLines(byId('confirm-text'), 'Working...');
  // The promise constructor turns a throw of run() into a rejection.
  new Promise(function (resolve) {
    resolve(flow.run());
  })
    .then(function (text) {
      flow.busy = false;
      if (confirmFlow !== flow) return;
      if (text === null) {
        // Done. The row of the run goes away with the next snapshot, so the
        // focus goes to the tab that is on screen.
        byId('confirm-dialog').close();
        tabs[ui.tab].tab.focus();
        return;
      }
      showConfirmResult(text);
    })
    .catch(function () {
      // run() failed. Clear the busy state, so Esc and Close work again.
      flow.busy = false;
      if (confirmFlow !== flow) return;
      showConfirmResult(CONFIRM_FAILED_TEXT);
    });
}

// Shows the end text of a flow. Only Close is left.
function showConfirmResult(text) {
  var ok = byId('confirm-ok');
  var cancel = byId('confirm-cancel');
  setLines(byId('confirm-text'), text);
  ok.hidden = true;
  ok.disabled = false;
  cancel.disabled = false;
  cancel.textContent = 'Close';
  cancel.focus();
}

// A click on an Archive button of a pipeline row. A stalled run asks a second
// question; only that second Confirm sends confirmStalled: true.
function archiveRun(button) {
  var runId = button.getAttribute('data-archive');
  var repo = button.getAttribute('data-repo');
  var steps = view.confirmSteps('archive', button.getAttribute('data-status'));
  var stalled = steps.indexOf('stalled') !== -1;
  confirmAction({
    prompts: steps.map(function (step) {
      return CONFIRM_PROMPT[step](runId);
    }),
    label: 'Archive',
    // The next snapshot rebuilds this button, so the dialog cannot return the focus to it.
    returnKey: focusKeyOf(button),
    run: function () {
      return post(view.archiveRequest(pageToken(), repo, runId, stalled)).then(function (result) {
        var outcome = view.archiveResultText(result.status, result.body);
        return outcome.ok ? null : outcome.text;
      });
    },
  });
}

// A click on the bin button of a preplan, deferred, or learning row. One
// question, then the request. On success the row goes away with the next
// snapshot; on an error the dialog shows the message and the suggestion.
function deleteItem(button) {
  var target = view.deleteKeyFromDataset(button.dataset);
  if (!target) return;
  confirmAction({
    prompts: [view.deletePrompt(target)],
    label: 'Delete',
    // The next snapshot rebuilds this button, so the dialog cannot return the focus to it.
    returnKey: focusKeyOf(button),
    run: function () {
      return post(view.deleteRequest(pageToken(), target.kind, target.repo, target.key)).then(function (result) {
        var outcome = view.deleteResultText(result.status, result.body);
        return outcome.ok ? null : outcome.text;
      });
    },
  });
}

// A click on Clear cache. It clears the repos of the last snapshot, one after
// another, and shows the freed size and the error of each repo that failed.
function clearCache() {
  confirmAction({
    prompts: view.confirmSteps('clear').map(function (step) {
      return CONFIRM_PROMPT[step]();
    }),
    label: 'Clear',
    run: function () {
      var token = pageToken();
      var repos = (ui.lastSnapshot && ui.lastSnapshot.repos) || [];
      var results = [];
      return repos
        .reduce(function (chain, repo) {
          if (!repo || !repo.root) return chain;
          return chain.then(function () {
            return post(view.clearRequest(token, repo.root)).then(function (result) {
              results.push({ repo: repo.root, status: result.status, body: result.body });
            });
          });
        }, Promise.resolve())
        .then(function () {
          return view.clearResultText(results);
        });
    },
  });
}

// --- Wiring ---------------------------------------------------------------------

// Wires the event listeners, starts the one timer for the wait banners, and
// opens the event stream.
function init() {
  view.TAB_NAMES.forEach(function (name, index) {
    var tab = tabs[name].tab;
    tab.addEventListener('click', function () {
      selectTab(name);
    });
    tab.addEventListener('keydown', function (event) {
      if (TAB_KEYS.indexOf(event.key) === -1) return;
      event.preventDefault();
      selectTab(view.TAB_NAMES[view.nextTabIndex(index, event.key, view.TAB_NAMES.length)], true);
    });
  });

  byId('filter-chips').addEventListener('click', function (event) {
    var chip = event.target.closest('[data-root]');
    if (chip) applyScope(chip.getAttribute('data-root'));
  });

  byId('toggle-all').addEventListener('click', function () {
    var collapse = anyOpen();
    blocks.forEach(function (b) {
      setCollapsed(b.key, collapse);
    });
  });

  var feed = byId('feed');
  feed.addEventListener('click', function (event) {
    var block = event.target.closest('[data-key]');
    if (!block) return;
    var key = block.getAttribute('data-key');
    var station = event.target.closest('[data-station]');
    if (station) {
      selectStation(key, Number(station.getAttribute('data-station')), true);
    } else if (event.target.closest('.fold-btn')) {
      setCollapsed(key, !block.classList.contains('collapsed'));
    }
  });
  // toggle does not bubble, so the feed listens in the capture phase.
  feed.addEventListener(
    'toggle',
    function (event) {
      var tile = event.target;
      if (!tile || tile.tagName !== 'DETAILS' || !tile.hasAttribute('data-section')) return;
      var block = tile.closest('[data-key]');
      if (!block) return;
      // Kept as true or false, so a tile closed by default (session)
      // stays open after the next snapshot once the user opened it.
      var key = view.sectionKey(block.getAttribute('data-key'), tile.getAttribute('data-section'));
      ui.closed[key] = !tile.open;
    },
    true
  );

  // A row button with data-detail opens the detail viewer; Enter on the
  // button fires the same click. The rows are in the feed and in the
  // Activity panel, so the listener is on the stage.
  stage.addEventListener('click', function (event) {
    var row = event.target.closest('[data-detail]');
    if (row) openDetail(row.getAttribute('data-detail'));
  });
  feed.addEventListener('click', function (event) {
    var button = event.target.closest('.archive-btn');
    if (button) archiveRun(button);
  });
  // An outcome chip filters the History table. A sort control sorts it
  // (view.nextHistorySort). saveFocus runs before the redraw, so the clicked
  // control keeps the focus.
  tabs.history.panel.addEventListener('click', function (event) {
    var chip = event.target.closest('[data-hist-outcome]');
    var header = event.target.closest('[data-hist-sort]');
    if (chip) {
      ui.historyOutcome = chip.getAttribute('data-hist-outcome');
    } else if (header) {
      ui.historySort = view.nextHistorySort(ui.historySort, header.getAttribute('data-hist-sort'));
    } else {
      return;
    }
    render(ui.lastSnapshot);
  });
  // A priority chip filters the Open deferred list. A chip has no
  // data-detail, so the stage listener opens no viewer for it.
  // A bin button (data-kind) asks to delete its item.
  tabs.activity.panel.addEventListener('click', function (event) {
    var del = event.target.closest('[data-kind]');
    if (del) return deleteItem(del);
    var chip = event.target.closest('[data-act-priority]');
    if (!chip) return;
    ui.deferredPriority = chip.getAttribute('data-act-priority');
    render(ui.lastSnapshot);
  });
  // A status chip filters the Preplans table.
  tabs.preplans.panel.addEventListener('click', function (event) {
    var del = event.target.closest('[data-kind]');
    if (del) return deleteItem(del);
    var chip = event.target.closest('[data-pp-status]');
    if (!chip) return;
    ui.preplanStatus = chip.getAttribute('data-pp-status');
    render(ui.lastSnapshot);
  });

  window.addEventListener('hashchange', function () {
    applyHash(location.hash);
  });

  var detailDialog = byId('detail-dialog');
  byId('detail-close').addEventListener('click', function () {
    detailDialog.close();
  });
  // Close and Esc both end in a close event, so one handler returns the focus.
  detailDialog.addEventListener('close', closeDetail);

  var confirmDialog = byId('confirm-dialog');
  byId('clear-btn').addEventListener('click', clearCache);
  byId('confirm-ok').addEventListener('click', confirmStep);
  byId('confirm-cancel').addEventListener('click', function () {
    confirmDialog.close();
  });
  // Esc cannot close the dialog while a request is on its way.
  confirmDialog.addEventListener('cancel', function (event) {
    if (confirmFlow && confirmFlow.busy) event.preventDefault();
  });
  confirmDialog.addEventListener('close', function () {
    var flow = confirmFlow;
    confirmFlow = null;
    // The control that opened the dialog may be rebuilt, and then the browser cannot
    // return the focus to it. The focus stays on the Cancel button of the closed dialog
    // until the browser moves it to the body, so leave that button first. restoreFocus
    // does nothing when the focus is on another control, as after a done flow, which
    // moves the focus to the tab.
    if (flow && flow.returnKey) {
      var active = document.activeElement;
      if (active && confirmDialog.contains(active)) active.blur();
      ui.focusKey = flow.returnKey;
      restoreFocus();
    }
  });

  var dialog = byId('stop-dialog');
  byId('stop-btn').addEventListener('click', function () {
    dialog.showModal();
  });
  byId('stop-cancel').addEventListener('click', function () {
    dialog.close();
  });
  byId('stop-confirm').addEventListener('click', function () {
    dialog.close();
    stopServer();
  });

  window.setInterval(tickElapsed, ELAPSED_TICK_MS);

  selectTab(ui.tab);
  applyHash(location.hash);
  connect();
}

init();
