/**
 * Pure page logic for the sdlc dashboard. No DOM access — every function
 * takes plain data in and returns plain data or strings out, so the same
 * file runs unmodified as a browser script (window.sdlcView) and under
 * Node's test runner (module.exports). The page scripts wire these onto the
 * DOM.
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
    stalled: 'rail',
    running: 'signal-amber',
    completed: 'signal-green',
  };

  // Short station labels for the 12 ship step names of the `steps[].name`
  // enum of ship-state.schema.json. Other names show unchanged.
  var STATION_LABELS = {
    execute: 'exec',
    commit: 'commit',
    review: 'review',
    harden: 'harden',
    'received-review': 'rcv-rev',
    'commit-fixes': 'fixes',
    'verify-openspec': 'spec-chk',
    'archive-openspec': 'archive',
    pr: 'pr',
    'verify-pipeline': 'ci',
    'await-remote-review': 'remote',
    'learnings-commit': 'learn',
  };

  var KIND_LABELS = { execute: 'exec' };

  var SEVERITY_TONES = {
    critical: 'signal-red',
    high: 'signal-red',
    medium: 'signal-amber',
    low: 'rail',
    info: 'rail',
  };

  var OUTCOME_GLYPHS = { success: '✓', failure: '✕', partial: '◐' };

  var EMPTY_TEXT = 'Nothing is running. Start /sdlc:ship in any repo and it shows here.';

  var CONNECTION_TEXT = {
    open: '',
    snapshot: '',
    error: 'Reconnecting…',
    stopped: 'Server stopped. Run /sdlc:dashboard to start it again.',
    'stop-failed': 'The server did not stop. Run /sdlc:dashboard --stop.',
  };

  var TAB_KEY = 'sdlc-dashboard-tab';
  var REPO_FILTER_KEY = 'sdlc-dashboard-repo-filter';
  var TAB_NAMES = ['pipelines', 'activity', 'history'];

  // When true, a completed pipeline block starts collapsed.
  var COLLAPSE_FINISHED = true;

  var MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

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

  // --- Step sections -------------------------------------------------------

  /**
   * A step gets a section only when it has sub-progress (a detail object).
   * @param {{detail?: object}} step
   * @returns {boolean}
   */
  function hasSection(step) {
    return !!(step && step.detail);
  }

  /**
   * @param {{detail?: {kind: string}}} step
   * @returns {string} the detail kind, '' when the step has no detail
   */
  function sectionKind(step) {
    return hasSection(step) ? step.detail.kind || '' : '';
  }

  function plural(n, one, many) {
    return n + ' ' + (n === 1 ? one : many);
  }

  function countCompleted(list) {
    var n = 0;
    for (var i = 0; i < list.length; i++) {
      if (list[i] && list[i].status === 'completed') n++;
    }
    return n;
  }

  /**
   * One-line summary of a step section. Queued tasks (planned, wave not
   * started) count in the task total; with no wave at all the summary is
   * the queued count. A dimensions section counts done dimensions against
   * the planned dimensions of the review plan when it has one. A guardrails
   * section reads the guardrail count. A
   * result section reads the text before the first colon of the result.
   * @param {{detail?: object}} step
   * @returns {string} '' when the step has no section
   */
  function sectionMeta(step) {
    if (!hasSection(step)) return '';
    var d = step.detail;
    switch (d.kind) {
      case 'waves': {
        var waves = d.waves || [];
        var queued = d.queued || [];
        if (waves.length === 0) return queued.length + ' queued';
        var tasks = [];
        for (var i = 0; i < waves.length; i++) {
          tasks = tasks.concat(waves[i].tasks || []);
        }
        var counts = taskCounts(tasks);
        var text = counts.done + '/' + (counts.total + queued.length) + ' tasks done';
        return waves.length > 1 ? waves.length + ' waves · ' + text : text;
      }
      case 'dimensions': {
        var dims = d.dimensions || [];
        // The plan size when the review run planned dimensions, else the rows listed.
        var planned = d.reviewPlan && d.reviewPlan.dimensionsPlanned > 0 ? d.reviewPlan.dimensionsPlanned : dims.length;
        var dimText = countCompleted(dims) + '/' + planned + ' dimensions done';
        if (d.reviewTotals) dimText += ' · ' + plural(d.reviewTotals.found, 'finding', 'findings');
        return dimText;
      }
      case 'explorers': {
        var explorers = d.explorers || [];
        var total = 0;
        for (var j = 0; j < explorers.length; j++) total += explorers[j].total || 0;
        return plural(explorers.length, 'area', 'areas') + ' · ' + plural(total, 'finding', 'findings');
      }
      case 'rounds':
        return (d.rounds || []).length + ' of ' + (d.maxRounds || 0) + ' rounds';
      case 'findings': {
        var n = (d.findings || []).length;
        return n === 0 ? 'no findings' : plural(n, 'finding', 'findings');
      }
      case 'guardrails':
        return plural((d.guardrails && d.guardrails.total) || 0, 'guardrail', 'guardrails');
      case 'result':
        // The text before the first colon: `nothing to commit: execute committed 2 wave commit(s)` gives `nothing to commit`.
        return String(d.result || '').split(':')[0].trim();
      default:
        return '';
    }
  }

  /**
   * A section takes a full row when it holds its own columns: 2 or more
   * waves, explorers, or review rounds. One wave stays a column.
   * @param {{kind: string, waves?: Array}} detail
   * @returns {boolean}
   */
  function isWideSection(detail) {
    if (!detail) return false;
    if (detail.kind === 'waves') return (detail.waves || []).length >= 2;
    return detail.kind === 'explorers' || detail.kind === 'rounds';
  }

  function firstIndex(steps, match) {
    for (var i = 0; i < steps.length; i++) {
      if (match(steps[i])) return i;
    }
    return -1;
  }

  /**
   * The station selected on first view: the first running step, else the
   * first failed step, else the first step with a section, else 0.
   * @param {Array<{status: string, detail?: object}>} steps
   * @returns {number}
   */
  function defaultStationIndex(steps) {
    var list = steps || [];
    var checks = [
      function (s) {
        return s && s.status === 'in_progress';
      },
      function (s) {
        return s && s.status === 'failed';
      },
      hasSection,
    ];
    for (var i = 0; i < checks.length; i++) {
      var index = firstIndex(list, checks[i]);
      if (index !== -1) return index;
    }
    return 0;
  }

  /**
   * Tiles of a pipeline block: one for each step with a section, one for
   * the issues when there are any, one for the session when there is one.
   * @param {{steps?: Array, issues?: Array}} pipeline
   * @param {object|null} session
   * @returns {number}
   */
  function tileCount(pipeline, session) {
    var steps = (pipeline && pipeline.steps) || [];
    var n = 0;
    for (var i = 0; i < steps.length; i++) {
      if (hasSection(steps[i])) n++;
    }
    if (pipeline && pipeline.issues && pipeline.issues.length > 0) n++;
    if (session) n++;
    return n;
  }

  /**
   * Compares two pipelines by start time, newest first. A pipeline with an
   * empty or unreadable startedAt sorts after every pipeline that has one.
   * @param {{startedAt?: string}} a
   * @param {{startedAt?: string}} b
   * @returns {number} negative when a comes first, positive when b comes
   *   first, 0 when both have the same start time or neither has one
   */
  function compareStartedDesc(a, b) {
    var timeA = parseTime(a && a.startedAt);
    var timeB = parseTime(b && b.startedAt);
    if (!timeA && !timeB) return 0;
    if (!timeA) return 1;
    if (!timeB) return -1;
    return timeB.getTime() - timeA.getTime();
  }

  /**
   * Feed order: 5 groups — waiting for a person (attention), running,
   * failed, stalled, completed. Inside a group the newest startedAt comes
   * first, an empty startedAt comes last, and equal pipelines keep their
   * input order. The pipeline objects are the input objects, not copies.
   * @param {Array<{status: string, attention?: object, startedAt?: string}>} pipelines
   * @returns {Array} a new array
   */
  function feedOrder(pipelines) {
    var RANK = { running: 1, failed: 2, stalled: 3, completed: 4 };
    function rank(p) {
      return p && p.attention ? 0 : RANK[p && p.status] || 1;
    }
    return (pipelines || [])
      .map(function (p, i) {
        return { p: p, i: i };
      })
      .sort(function (a, b) {
        return rank(a.p) - rank(b.p) || compareStartedDesc(a.p, b.p) || a.i - b.i;
      })
      .map(function (x) {
        return x.p;
      });
  }

  /**
   * @param {{status: string}} pipeline
   * @returns {boolean} true when the block starts collapsed
   */
  function defaultCollapsed(pipeline) {
    return COLLAPSE_FINISHED && !!pipeline && pipeline.status === 'completed';
  }

  // --- Execute and review details ------------------------------------------

  /**
   * Commit state of one wave. A wave that has not started (status
   * `pending`) has no commit state, whatever commitWaves says. Otherwise
   * only commitWaves === false means "off"; an absent value behaves as on.
   * "due" needs at least one task, all done.
   * @param {{status?: string, committedSha?: string, tasks?: Array}} wave
   * @param {boolean|undefined} commitWaves
   * @returns {string} '' | 'off' | 'committed' | 'due' | 'not-committed'
   */
  function waveCommitState(wave, commitWaves) {
    if (wave && wave.status === 'pending') return '';
    if (commitWaves === false) return 'off';
    if (wave && wave.committedSha) return 'committed';
    var counts = taskCounts((wave && wave.tasks) || []);
    if (counts.total > 0 && counts.done === counts.total) return 'due';
    return 'not-committed';
  }

  /**
   * @param {Array<{status: string}>} tasks
   * @returns {{done: number, total: number}} done counts completed tasks
   */
  function taskCounts(tasks) {
    var list = tasks || [];
    return { done: countCompleted(list), total: list.length };
  }

  /**
   * @param {{found: number, fixed: number, deferred: number, unaccounted: number}} t
   * @returns {string}
   */
  function reviewTotalsText(t) {
    if (!t) return '';
    return (
      plural(t.found, 'finding', 'findings') +
      ' · ' + t.fixed + ' fixed · ' + t.deferred + ' deferred · ' + t.unaccounted + ' unaccounted'
    );
  }

  /**
   * The run totals line of a review plan: how many waves and dimensions ran
   * out of those planned, and how many never started.
   * @param {{wavesPlanned: number, wavesRun: number, dimensionsPlanned: number, dimensionsRun: number, neverStarted: number}} p
   * @returns {string} '' when there is no plan
   */
  function reviewPlanText(p) {
    if (!p) return '';
    return (
      'waves ' + p.wavesRun + '/' + p.wavesPlanned + ' run · dimensions ' +
      p.dimensionsRun + '/' + p.dimensionsPlanned + ' run · ' + p.neverStarted + ' never started'
    );
  }

  /**
   * @param {{total: number}} explorer
   * @param {number} shown findings already listed
   * @returns {number} findings not listed, never below 0
   */
  function explorerMore(explorer, shown) {
    var total = (explorer && explorer.total) || 0;
    return Math.max(0, total - (shown || 0));
  }

  // --- Labels ---------------------------------------------------------------

  /**
   * @param {string} kind pipeline kind
   * @returns {string} 'exec' for execute, else the kind unchanged
   */
  function kindLabel(kind) {
    return KIND_LABELS[kind] || kind;
  }

  /**
   * @param {string} name ship step name
   * @returns {string} the short station label, else the name unchanged
   */
  function stationLabel(name) {
    return Object.prototype.hasOwnProperty.call(STATION_LABELS, name) ? STATION_LABELS[name] : name;
  }

  /**
   * @param {boolean} anyOpen true when any detail is open
   * @returns {string}
   */
  function toggleAllLabel(anyOpen) {
    return anyOpen ? 'Collapse all details' : 'Expand all details';
  }

  /**
   * @param {string} sev issue severity
   * @returns {string} color token; unknown severities get 'rail'
   */
  function severityTone(sev) {
    return SEVERITY_TONES[sev] || 'rail';
  }

  /**
   * @param {{file?: string, line?: string, ref?: string}} issue
   * @returns {string} 'file:line', 'file', the ref, or ''
   */
  function issueLocation(issue) {
    if (!issue) return '';
    if (issue.file) return issue.line ? issue.file + ':' + issue.line : issue.file;
    return issue.ref || '';
  }

  /**
   * @param {string} outcome history_record outcome
   * @returns {string}
   */
  function outcomeGlyph(outcome) {
    return OUTCOME_GLYPHS[outcome] || '·';
  }

  /**
   * @param {string} id
   * @returns {string} the first 8 characters plus '…' when longer
   */
  function shortId(id) {
    if (!id) return '';
    return id.length > 8 ? id.slice(0, 8) + '…' : id;
  }

  // --- Repo chips and counts -------------------------------------------------

  /**
   * @param {Array<{root: string, name: string, pipelines?: Array}>} repos
   * @returns {Array<{root: string, name: string, count: number, hasFail: boolean}>}
   *   one chip for each repo, in snapshot order
   */
  function repoChips(repos) {
    return (repos || []).map(function (repo) {
      var pipelines = repo.pipelines || [];
      return {
        root: repo.root,
        name: repo.name,
        count: pipelines.length,
        hasFail: pipelines.some(function (p) {
          return p && p.status === 'failed';
        }),
      };
    });
  }

  /**
   * An empty (or missing) scope means every repo is in scope.
   * @param {Set<string>} scope selected repo roots
   * @param {string} root
   * @returns {boolean}
   */
  function inScope(scope, root) {
    if (!scope || scope.size === 0) return true;
    return scope.has(root);
  }

  /**
   * Tab counts over the repos in scope. Activity is learnings plus
   * deferred items.
   * @param {Array} repos
   * @param {Set<string>} scope
   * @returns {{pipelines: number, activity: number, history: number}}
   */
  function scopedCounts(repos, scope) {
    var counts = { pipelines: 0, activity: 0, history: 0 };
    (repos || []).forEach(function (repo) {
      if (!inScope(scope, repo.root)) return;
      counts.pipelines += (repo.pipelines || []).length;
      counts.activity += (repo.learnings || []).length + (repo.deferred || []).length;
      counts.history += (repo.history || []).length;
    });
    return counts;
  }

  /**
   * Header counts over all repos; the repo filter does not apply. The
   * running, stalled, and failed counts follow the pipeline status. The
   * waiting count is the number of pipelines that have an attention.
   * @param {Array} repos
   * @returns {{running: number, stalled: number, failed: number, waiting: number}}
   */
  function headerCounts(repos) {
    var counts = { running: 0, stalled: 0, failed: 0, waiting: 0 };
    (repos || []).forEach(function (repo) {
      (repo.pipelines || []).forEach(function (p) {
        if (!p) return;
        if (p.status === 'running' || p.status === 'stalled' || p.status === 'failed') {
          counts[p.status]++;
        }
        if (p.attention) counts.waiting++;
      });
    });
    return counts;
  }

  /**
   * Browser tab title. A leading "(N) " shows the number of pipelines that
   * wait for a person in the given repos; with none, the title is base.
   * @param {string} base title without a count
   * @param {Array} repos the repos in scope
   * @returns {string}
   */
  function pageTitle(base, repos) {
    var waiting = 0;
    (repos || []).forEach(function (repo) {
      (repo.pipelines || []).forEach(function (p) {
        if (p && p.attention) waiting++;
      });
    });
    return waiting > 0 ? '(' + waiting + ') ' + base : base;
  }

  // --- Time ------------------------------------------------------------------

  /**
   * @param {number} ms
   * @returns {string} '26s', '12m', or '1h 02m'; '' for a bad value
   */
  function formatDuration(ms) {
    if (typeof ms !== 'number' || !isFinite(ms) || ms < 0) return '';
    var seconds = Math.floor(ms / 1000);
    if (seconds < 60) return seconds + 's';
    var minutes = Math.floor(seconds / 60);
    if (minutes < 60) return minutes + 'm';
    var hours = Math.floor(minutes / 60);
    var rest = minutes % 60;
    return hours + 'h ' + (rest < 10 ? '0' : '') + rest + 'm';
  }

  /**
   * Like formatDuration, but shows seconds below 1 hour. From 1 hour and
   * for a bad value it returns what formatDuration returns.
   * @param {number} ms
   * @returns {string} '26s', '4m 12s', '1h 03m'; '' for a bad value
   */
  function formatElapsed(ms) {
    if (typeof ms !== 'number' || !isFinite(ms) || ms < 0 || ms >= 3600000) return formatDuration(ms);
    var s = Math.floor(ms / 1000);
    return s < 60 ? s + 's' : Math.floor(s / 60) + 'm ' + (s % 60) + 's';
  }

  function dateParts(date, tz) {
    var formatter = new Intl.DateTimeFormat('en-US', {
      timeZone: tz,
      month: 'numeric',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    });
    var parts = {};
    formatter.formatToParts(date).forEach(function (part) {
      parts[part.type] = part.value;
    });
    return parts;
  }

  function parseTime(iso) {
    if (!iso) return null;
    var date = new Date(iso);
    return isNaN(date.getTime()) ? null : date;
  }

  /**
   * @param {string} iso timestamp
   * @param {string} tz IANA time zone, for example 'UTC'
   * @returns {string} 'HH:MM' in tz; '' for a bad timestamp
   */
  function clockLabel(iso, tz) {
    var date = parseTime(iso);
    if (!date) return '';
    var parts = dateParts(date, tz);
    return parts.hour + ':' + parts.minute;
  }

  /**
   * Under 60 s 'just now', under 60 min 'Nm ago', under 24 h 'Nh ago',
   * else 'Mon D HH:MM' in tz. A time in the future reads 'just now'.
   * @param {string} iso timestamp
   * @param {Date|number} now
   * @param {string} tz IANA time zone, for example 'UTC'
   * @returns {string} '' for a bad timestamp
   */
  function relativeWhen(iso, now, tz) {
    var date = parseTime(iso);
    if (!date) return '';
    var diff = (typeof now === 'number' ? now : now.getTime()) - date.getTime();
    if (diff < 60e3) return 'just now';
    if (diff < 3600e3) return Math.floor(diff / 60e3) + 'm ago';
    if (diff < 86400e3) return Math.floor(diff / 3600e3) + 'h ago';
    var parts = dateParts(date, tz);
    return MONTHS[Number(parts.month) - 1] + ' ' + parts.day + ' ' + parts.hour + ':' + parts.minute;
  }

  // --- Sessions, keys, navigation ----------------------------------------------

  /**
   * The session of a pipeline: the one whose id is the pipeline sessionId,
   * else the same-branch session with the newest lastSeen (ISO strings sort
   * in time order), else null.
   * @param {{sessionId?: string, branch?: string}} pipeline
   * @param {Array<{id: string, branch: string, lastSeen: string}>} sessions
   * @returns {object|null}
   */
  function pickSession(pipeline, sessions) {
    var list = sessions || [];
    if (!pipeline) return null;
    if (pipeline.sessionId) {
      for (var i = 0; i < list.length; i++) {
        if (list[i].id === pipeline.sessionId) return list[i];
      }
    }
    var best = null;
    for (var j = 0; j < list.length; j++) {
      var s = list[j];
      if (!pipeline.branch || s.branch !== pipeline.branch) continue;
      if (!best || (s.lastSeen || '') > (best.lastSeen || '')) best = s;
    }
    return best;
  }

  /**
   * Arrow keys move one tab and wrap; Home and End go to the ends; any other
   * key keeps the index.
   * @param {number} index
   * @param {string} key KeyboardEvent.key
   * @param {number} count number of tabs
   * @returns {number}
   */
  function nextTabIndex(index, key, count) {
    if (count <= 0) return index;
    switch (key) {
      case 'ArrowLeft':
        return (index - 1 + count) % count;
      case 'ArrowRight':
        return (index + 1) % count;
      case 'Home':
        return 0;
      case 'End':
        return count - 1;
      default:
        return index;
    }
  }

  /**
   * @param {{root: string}} repo
   * @param {{id: string}} pipeline
   * @returns {string} repo root and pipeline id, joined by a newline
   */
  function pipelineKey(repo, pipeline) {
    return repo.root + '\n' + pipeline.id;
  }

  /**
   * @param {string} key a pipelineKey value
   * @param {string} name step name, or 'issues' or 'session'
   * @returns {string}
   */
  function sectionKey(key, name) {
    return key + '/' + name;
  }

  /**
   * '#<tab>' selects a tab when the name is in tabs. '#<pipeline id>/<n>'
   * selects a pipeline and station n; '#<pipeline id>' a pipeline only. A
   * part after the last '/' that is not a whole number stays in the id.
   * @param {string} hash location.hash
   * @param {Array<string>} tabs
   * @returns {{tab: string}|{pipelineId: string, station: (number|null)}|null}
   */
  function parseHash(hash, tabs) {
    var raw = (hash || '').replace(/^#/, '');
    if (!raw) return null;
    try {
      raw = decodeURIComponent(raw);
    } catch (e) {
      // Keep the raw text when it is not valid URI encoding.
    }
    if ((tabs || []).indexOf(raw) !== -1) return { tab: raw };
    var slash = raw.lastIndexOf('/');
    if (slash > 0 && /^\d+$/.test(raw.slice(slash + 1))) {
      return { pipelineId: raw.slice(0, slash), station: Number(raw.slice(slash + 1)) };
    }
    return { pipelineId: raw, station: null };
  }

  // --- Storage -----------------------------------------------------------------

  /**
   * @param {{getItem: function(string): (string|null)}} storage
   * @returns {string} the saved tab; 'pipelines' for an unknown value or an error
   */
  function loadTab(storage) {
    try {
      var value = storage.getItem(TAB_KEY);
      return TAB_NAMES.indexOf(value) !== -1 ? value : TAB_NAMES[0];
    } catch (e) {
      return TAB_NAMES[0];
    }
  }

  /**
   * @param {{setItem: function(string, string): void}} storage
   * @param {string} tab
   * @returns {void}
   */
  function saveTab(storage, tab) {
    try {
      storage.setItem(TAB_KEY, tab);
    } catch (e) {
      // Storage can be unavailable (private mode, quota) — the tab is a
      // convenience, not a requirement.
    }
  }

  /**
   * @param {{getItem: function(string): (string|null)}} storage
   * @returns {Set<string>} selected repo roots; empty on any error
   */
  function loadRepoFilter(storage) {
    try {
      var raw = storage.getItem(REPO_FILTER_KEY);
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
   * @param {Set<string>} set selected repo roots
   * @returns {void}
   */
  function saveRepoFilter(storage, set) {
    try {
      storage.setItem(REPO_FILTER_KEY, JSON.stringify(Array.from(set)));
    } catch (e) {
      // Storage can be unavailable (private mode, quota) — the filter is a
      // convenience, not a requirement.
    }
  }

  var view = {
    TAB_KEY: TAB_KEY,
    REPO_FILTER_KEY: REPO_FILTER_KEY,
    TAB_NAMES: TAB_NAMES,
    COLLAPSE_FINISHED: COLLAPSE_FINISHED,
    stepGlyph: stepGlyph,
    pipelineTone: pipelineTone,
    cutText: cutText,
    emptyText: emptyText,
    connectionText: connectionText,
    stopRequest: stopRequest,
    stopResult: stopResult,
    worktreeLabel: worktreeLabel,
    hasSection: hasSection,
    sectionKind: sectionKind,
    sectionMeta: sectionMeta,
    isWideSection: isWideSection,
    defaultStationIndex: defaultStationIndex,
    tileCount: tileCount,
    compareStartedDesc: compareStartedDesc,
    feedOrder: feedOrder,
    defaultCollapsed: defaultCollapsed,
    waveCommitState: waveCommitState,
    taskCounts: taskCounts,
    reviewTotalsText: reviewTotalsText,
    reviewPlanText: reviewPlanText,
    explorerMore: explorerMore,
    kindLabel: kindLabel,
    stationLabel: stationLabel,
    repoChips: repoChips,
    inScope: inScope,
    scopedCounts: scopedCounts,
    headerCounts: headerCounts,
    pageTitle: pageTitle,
    toggleAllLabel: toggleAllLabel,
    severityTone: severityTone,
    issueLocation: issueLocation,
    outcomeGlyph: outcomeGlyph,
    formatDuration: formatDuration,
    formatElapsed: formatElapsed,
    relativeWhen: relativeWhen,
    clockLabel: clockLabel,
    shortId: shortId,
    pickSession: pickSession,
    nextTabIndex: nextTabIndex,
    pipelineKey: pipelineKey,
    sectionKey: sectionKey,
    parseHash: parseHash,
    loadTab: loadTab,
    saveTab: saveTab,
    loadRepoFilter: loadRepoFilter,
    saveRepoFilter: saveRepoFilter,
  };

  if (typeof module === 'object' && module.exports) {
    module.exports = view;
  } else {
    root.sdlcView = view;
  }
})(this);
