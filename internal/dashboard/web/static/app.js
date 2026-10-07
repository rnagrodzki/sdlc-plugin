/**
 * DOM glue for the sdlc dashboard page. Every decision (status text,
 * sorting, glyphs, stop request, open groups) is a call into
 * window.sdlcView (view.js). Snapshot text goes into the page through
 * textContent only, never innerHTML.
 */
'use strict';

var view = window.sdlcView;
var source = null;
var lastSnapshot = { repos: [] };
var selectedKey = '';
var pipelinesByKey = new Map();
var focusTargets = new Map();
var openGroups = view.loadOpenGroups(storage());

function storage() {
  // window.localStorage can throw (file://, blocked storage). view.js
  // treats a null storage as empty.
  try {
    return window.localStorage;
  } catch (e) {
    return null;
  }
}

function byId(id) {
  return document.getElementById(id);
}

function el(tag, className, text) {
  var node = document.createElement(tag);
  node.className = className || '';
  node.textContent = text == null ? '' : String(text);
  return node;
}

function tone(token) {
  return 'var(--' + token + ')';
}

function clock(at) {
  var date = new Date(at);
  return isNaN(date.getTime()) ? '' : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function setConnection(state) {
  byId('conn').setAttribute('data-state', state);
  byId('conn-text').textContent = view.connectionText(state);
}

function connect() {
  source = new EventSource('/api/events');
  source.addEventListener('open', function () {
    setConnection('open');
  });
  source.addEventListener('error', function () {
    setConnection('error');
  });
  source.addEventListener('snapshot', function (event) {
    setConnection('snapshot');
    render(JSON.parse(event.data));
  });
}

function render(snapshot) {
  lastSnapshot = snapshot || { repos: [] };
  var repos = lastSnapshot.repos || [];
  var active = document.activeElement;
  var focusKey = active ? active.getAttribute('data-key') : null;

  pipelinesByKey = new Map();
  focusTargets = new Map();

  var groups = byId('groups');
  groups.replaceChildren();
  repos.forEach(function (repo) {
    groups.appendChild(renderRepo(repo));
  });

  var emptyText = view.emptyText(lastSnapshot);
  var empty = byId('empty');
  empty.textContent = emptyText;
  empty.hidden = !emptyText;

  renderTotals(repos);
  renderLearnings(repos);
  renderDeferred(repos);
  renderDetail(Boolean(emptyText));

  var target = focusTargets.get(focusKey);
  if (target) target.focus();
}

function renderTotals(repos) {
  var counts = { running: 0, stalled: 0, failed: 0 };
  repos.forEach(function (repo) {
    (repo.pipelines || []).forEach(function (p) {
      counts[p.status] = (counts[p.status] || 0) + 1;
    });
  });
  byId('totals').textContent =
    counts.running + ' running · ' + counts.stalled + ' stalled · ' + counts.failed + ' failed';
}

function renderRepo(repo) {
  var pipelines = view.sortPipelines(repo.pipelines);
  var group = el('details', 'group');
  group.open = openGroups.has(repo.root);
  group.addEventListener('toggle', function () {
    group.open ? openGroups.add(repo.root) : openGroups.delete(repo.root);
    view.saveOpenGroups(storage(), openGroups);
  });

  var summary = el('summary');
  var groupKey = 'repo\n' + repo.root;
  summary.setAttribute('data-key', groupKey);
  summary.title = repo.root;
  focusTargets.set(groupKey, summary);
  var lamp = el('span', 'lamp');
  lamp.setAttribute('aria-hidden', 'true');
  lamp.style.color = tone(view.pipelineTone((pipelines[0] || {}).status));
  lamp.hidden = pipelines.length === 0;
  summary.append(el('span', 'group-name sign', repo.name), el('span', 'count sign', pipelines.length), lamp);
  group.appendChild(summary);

  var error = el('p', 'group-error', repo.error);
  error.hidden = !repo.error;
  group.appendChild(error);

  var list = el('ul', 'pipes');
  pipelines.forEach(function (p) {
    var key = repo.root + '\n' + p.id;
    pipelinesByKey.set(key, { repo: repo, pipeline: p });

    var button = el('button', 'pipe');
    button.type = 'button';
    button.setAttribute('data-key', key);
    button.setAttribute('aria-current', key === selectedKey ? 'true' : 'false');
    button.title = p.kind + ' ' + p.branch + ' (' + p.status + ')';
    focusTargets.set(key, button);

    var pipeLamp = el('span', 'lamp');
    pipeLamp.setAttribute('aria-hidden', 'true');
    pipeLamp.style.color = tone(view.pipelineTone(p.status));
    button.append(
      el('span', 'pipe-kind sign', p.kind),
      el('span', 'pipe-branch', view.cutText(p.branch, 48)),
      worktreeTag(repo, p),
      pipeLamp
    );
    button.addEventListener('click', function () {
      selectedKey = key;
      render(lastSnapshot);
    });

    var item = el('li');
    item.appendChild(button);
    list.appendChild(item);
  });
  group.appendChild(list);
  return group;
}

function worktreeTag(repo, p) {
  var label = view.worktreeLabel(repo.root, p.worktree);
  var tag = el('span', 'wt', label);
  tag.title = p.worktree || '';
  tag.hidden = !label;
  return tag;
}

function renderDetail(pageEmpty) {
  var picked = pipelinesByKey.get(selectedKey);
  var body = byId('detail-body');
  body.replaceChildren();
  byId('detail-hint').hidden = Boolean(picked) || pageEmpty;
  if (picked) body.appendChild(renderPipeline(picked.pipeline, picked.repo));
}

function renderPipeline(p, repo) {
  var box = el('article', 'pipeline');

  var head = el('header', 'pipeline-head');
  var status = el('span', 'pipeline-status sign', p.status);
  status.setAttribute('data-status', p.status);
  status.style.color = tone(view.pipelineTone(p.status));
  head.append(
    el('span', 'pipeline-kind sign', p.kind),
    el('span', 'pipeline-branch sign', p.branch),
    worktreeTag(repo, p),
    status
  );
  box.appendChild(head);

  var progress = p.progress || {};
  box.appendChild(
    el('p', 'progress', [progress.done + '/' + progress.total, progress.label].filter(Boolean).join(' · '))
  );

  var stations = el('ol', 'stations');
  stations.setAttribute('aria-label', 'Steps');
  (p.steps || []).forEach(function (step) {
    var glyph = view.stepGlyph(step.status);
    var station = el('li', 'station');
    station.setAttribute('data-status', step.status);
    station.title = step.name + ': ' + step.status;
    var mark = el('span', 'station-glyph', glyph.glyph);
    mark.style.color = tone(glyph.token);
    mark.setAttribute('aria-hidden', 'true');
    var name = el('span', 'station-name sign', step.name);
    name.setAttribute('aria-label', step.name + ', ' + step.status);
    station.append(mark, name);
    stations.appendChild(station);
  });
  box.appendChild(stations);

  var panels = el('div', 'panels');

  var issuesBox = el('section', 'issues-box');
  var issues = p.issues || [];
  issuesBox.appendChild(el('h2', 'sign', 'Issues (' + issues.length + ')'));
  var issueList = el('ul', 'issues');
  issues.forEach(function (issue) {
    var item = el('li', 'issue');
    item.title = issue.text;
    var glyph = el('span', 'issue-glyph', view.stepGlyph('failed').glyph);
    glyph.setAttribute('aria-hidden', 'true');
    var text = el('span', 'issue-text', issue.source + ': ' + view.cutText(issue.text, 200));
    text.appendChild(el('span', 'issue-severity', issue.severity));
    item.append(glyph, text);
    issueList.appendChild(item);
  });
  issuesBox.appendChild(issueList);
  panels.appendChild(issuesBox);

  var sessionsBox = el('section', 'sessions-box');
  var sessions = (repo.sessions || []).filter(function (s) {
    return s.branch === p.branch;
  });
  sessionsBox.appendChild(el('h2', 'sign', 'Sessions (' + sessions.length + ')'));
  sessions.forEach(function (s) {
    sessionsBox.appendChild(renderSession(s));
  });
  panels.appendChild(sessionsBox);

  box.appendChild(panels);
  return box;
}

function renderSession(s) {
  var box = el('div', 'session');
  box.setAttribute('data-active', String(Boolean(s.active)));

  var counts = s.counts || {};
  var head = el('div', 'session-head');
  var lamp = el('span', 'session-lamp', '●');
  lamp.setAttribute('aria-hidden', 'true');
  var id = el('span', 'session-id', view.cutText(s.id, 8));
  id.title = s.id;
  head.append(
    lamp,
    id,
    el(
      'span',
      'session-counts sign',
      counts.prompts + ' prompts · ' + counts.commands + ' commands · ' + counts.mcpCalls + ' MCP calls'
    )
  );
  box.appendChild(head);

  var timeline = el('ol', 'timeline');
  (s.timeline || []).forEach(function (event) {
    var item = el('li', 'event');
    item.setAttribute('data-kind', event.kind);
    item.title = event.text;
    item.append(
      el('span', 'event-at', clock(event.at)),
      el('span', 'event-kind', event.kind),
      el('span', 'event-text', view.cutText(event.text, 160))
    );
    timeline.appendChild(item);
  });
  box.appendChild(timeline);
  return box;
}

function renderLearnings(repos) {
  var list = byId('learnings-list');
  list.replaceChildren();
  var total = 0;
  repos.forEach(function (repo) {
    (repo.learnings || []).forEach(function (entry) {
      total += 1;
      var item = el('li', '', view.cutText(entry.heading, 120));
      item.title = entry.heading;
      item.appendChild(
        el('span', 'extra-meta', [repo.name, entry.date, entry.branch, entry.runId].filter(Boolean).join(' · '))
      );
      list.appendChild(item);
    });
  });
  byId('learnings-count').textContent = '(' + total + ')';
}

function renderDeferred(repos) {
  var list = byId('deferred-list');
  list.replaceChildren();
  var total = 0;
  repos.forEach(function (repo) {
    (repo.deferred || []).forEach(function (entry) {
      total += 1;
      var item = el('li');
      item.title = entry.description;
      var priority = el('span', 'priority sign', entry.priority);
      priority.setAttribute('data-priority', entry.priority);
      item.append(priority, document.createTextNode(view.cutText(entry.description, 140)));
      item.appendChild(el('span', 'extra-meta', [repo.name, entry.id].filter(Boolean).join(' · ')));
      list.appendChild(item);
    });
  });
  byId('deferred-count').textContent = '(' + total + ')';
}

function stopServer() {
  var meta = document.querySelector('meta[name="sdlc-token"]');
  var request = view.stopRequest(meta ? meta.getAttribute('content') : '');
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
        if (source) source.close();
        byId('stop-btn').disabled = true;
      }
      setConnection(result);
    });
}

function init() {
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
  connect();
}

init();
