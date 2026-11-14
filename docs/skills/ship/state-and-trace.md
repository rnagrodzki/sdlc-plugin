# State, summary and run report

Read this page when a `/sdlc:ship` run stopped and you want to continue it. It also shows why a run chose a value and what it reports at the end. A run records all of this with no extra flag or setup.

## Example

```
/sdlc:ship --resume
```

Continues the run of this branch from its saved progress, also in a new session. You do not pass `--plan` again.

## State and Resolution Trace

Every `/sdlc:ship` run reads its saved state before doing anything else — even
when `--resume` isn't passed — because a stale in-flight run can exist from
a prior session. What happens next depends on what it finds:

- **No saved state for this branch** (the read reports that no state file
  exists — this is not an error), or a finished run, or a run where no step
  ever started: a fresh run starts.
- **A run still in flight, with `--resume` or without `--auto`:** ship
  shows where that run stopped and continues it. A plain `/sdlc:ship` resumes
  too.
- **A run still in flight, with `--auto` but no `--resume`:** ship shows
  where that run stopped and asks whether to resume it or start fresh. It
  asks even under `--auto`, because starting fresh deletes the old run's
  state file and it cannot be recovered.

The saved state file (one per branch, under `.sdlc-v2/`)
records, alongside the resolved pipeline flags:

- **`sources`** — a per-flag resolution trace: which precedence tier won for
  each resolved flag. Values include `"cli"`, `"local"` (a `[ship]` value
  from a local.toml file), `"quick"`, `"default"`, and, for the bump flag
  specifically, `"config (version.preRelease)"` and
  `"config (version.preReleasePolicy enforced over cli)"` when
  `preReleasePolicy: "always-rc"` overrides an explicit `--bump`. Useful for
  answering "why did ship pick this bump/quality level?" after the fact.
- **`versionCfg`** — a snapshot of the `[version]` config section as read
  at flag-resolution time, for diagnosing version/bump issues without
  needing to reconstruct what the config looked like when the run started.
- **`binaryVersion`** — the sdlc binary's build metadata (plugin version,
  commit, build time), so a state file can be traced back to the binary
  that produced it.
- **`reportData`** — step counts (total/completed/pending/skipped/failed),
  duration, decisions, deferred-finding count, bump provenance, the
  self-healing record (`healing`: fixed findings, fix progress of each
  finding, and harden runs, `{}` when none were recorded), and the review
  ledger (below), computed on the fly each time state is read (not
  persisted to disk). This lets the final pipeline report be rendered from
  already-computed values instead of re-deriving them from raw step/decision
  arrays.

None of this requires extra flags or setup — it's recorded (or derived)
automatically by every run and read back automatically on resume.

## End-of-run summary

After the last step, ship prints the step table, the decisions log and the
deferred findings. Four parts of that summary are worth knowing about:

- **Review ledger.** When the review step ran, the summary closes with one
  ledger that accounts for *every* finding the review produced — how many
  were fixed by `received-review`, and how many were deferred, grouped by the
  reason each was deferred with:

      Review ledger: 14 = 9 fixed + 5 deferred (below-threshold: 3, needs-direction: 2)
      Run /sdlc:deferred to act on the 5 deferred finding(s).

  Every number here is read from the saved state's review ledger, never
  recomputed by ship itself. The numbers are meant to add up. When they
  don't, ship prints a second line instead of quietly adjusting the total:

      UNACCOUNTED: 2 finding(s) have no fix or deferral record

  An unbalanced ledger is a reporting bug worth seeing. A deferred finding that
  the `harden` fix pass fixes counts as fixed, not as deferred.

- **Review gaps.** When the review skips a dimension (the worker stalled twice
  or never checked in) or names a worker that may still be running, ship
  records each one as a decision before the review step completes. The
  decisions log in this summary lists them.

- **OpenSpec change still active.** When the run names an OpenSpec change and
  `openspec/changes/<change>/` still exists at run end, the decisions log
  holds one more entry: `OpenSpec change <change> is still active in
  openspec/changes/<change>/. Archive it with openspec archive <change>.`
  The entry belongs to the `archive-openspec` step.

- **Deferred follow-ups.** After the summary, ship checks for deferred items
  that are still open — review findings that were saved rather than fixed,
  plus any issue drafts the execute step raised. If there are none, nothing is
  printed. If there are, it offers the same triage flow
  [/sdlc:deferred](../deferred.md) runs: file GitHub issues for the ones worth
  filing, resolve the rest, or leave them for later.
  - A `harden-pending` item (rule edits that `/sdlc:harden --auto` held back)
    is not filed and not resolved. For it, ship prints the `/sdlc:harden`
    command that runs the rule edits and the `/sdlc:deferred --resolve <id>`
    command to run after it. The item stays open.
  - With `--auto`, the open items are still listed but the triage question is
    not asked — they stay open for a later run.
  - The execute-step issue drafts are the exception: they are offered even
    under `--auto`, because creating a GitHub issue always needs your
    approval.
  - Nothing is lost either way — every deferred item is already written to
    disk when it is created.

## Run report

Near the end of a run, ship calls the tool for a run report. One call
composes the report from ship state, self-healing records, this run's execute
state, CLI evidence, user input, and learnings, then renders and writes it.
Ship never renders or writes the report itself — the tool always does both.

**Where it goes.** The report is gated by the project's `automation.report`
config (`enabled`, and `format`: `"json"` or `"md"`, default `"md"`).

- When enabled, the tool persists it under
  `.sdlc-v2/reports/ship-<runId>-report.{json,md}` in the main worktree (not
  wherever the session happens to be running from, e.g. a linked worktree).
  Ship prints that path as the last line of that step's output.
- Disabled reporting (`automation.report.enabled = false`) skips this step
  silently — nothing is written.

**When it is written.** The write happens before the terminal cleanup step
runs. Cleanup first copies the plan explorer summary into ship state, then
deletes the linked plan run that the report still needs to read.

**What it covers.**

- Per-wave task outcomes and step timings.
- CLI evidence, and drift/error/warning counts.
- Guardrail hits.
- Any deferred findings or pending issue drafts.
- The review ledger and a Review waves table.
- Self-healing changes: the fixes and hardening this run recorded, plus the
  result of the harden step, with its own commit and the fix-pass commit when
  they ran.
- The linked plan's planning time, from its start to its last edit. This ends
  at the last time the plan file was modified, not at the moment the plan was
  accepted, so it reflects time actually spent on the plan.
- Every prompt you typed and every question you answered while the run was
  active, so a correction or a choice made mid-run is visible after the run
  ends. An old or injected turn (for example an editor notification) is
  filtered out of that list and never counted.

**The Review waves table.** It has one row for each dimension the review
planned, with its wave, status, findings, and duration. It is read from the
review run that carries this ship run's ID. When there is no such review run,
the run plans no dimensions, or the review run cannot be read, the section is
one line that gives the reason.

**The Markdown layout.** The Markdown report opens with a Summary table that
holds every run metric in one place. The sections below it show counts instead
of one line per command, and list only the findings and failures a person must
act on.
