# Ship Pipeline — Entry-Mode Handlers

On-demand companion for `ship/SKILL.md` (implements R-progressive-disclosure). These handlers short-circuit the pipeline — they run instead of the normal `ship it` flow. Read this file only when the corresponding flag is passed; never preemptively.

## --init-config handler (R16)

If `--init-config` was passed:

**Redirect only.** Tell the user: "Run `/setup` instead — it covers ship configuration as part of unified project setup." Then stop. No pipeline execution.

There is no walkthrough fallback in this port. The source skill's `ship-init.js` interactive-walkthrough script (steps multi-select, bump type, auto, threshold, workspace isolation, optional `--quick` profile) has no Go equivalent — `/setup` is the only supported path to `.sdlc/local.json` configuration. Do not attempt to reconstruct the walkthrough inline even if the user insists on `--init-config`; redirect every time.

## --gc handler (R39, issue #223)

If `--gc` (with optional `--ttl-days <N>`) was passed, call the `ship_prepare` tool with the GC short-circuit and stop — no pipeline composition:

```
ship_prepare({gc: true[, ttlDays: N]})
```

This is a distinct GC path from `ship_state({action: "gc"})` (used elsewhere for state-file-only garbage collection): `ship_prepare`'s GC branch additionally sweeps stale Explore-mode tempdirs (`exploreTempdirs` in the report), which the `ship_state` GC action does not. Do not substitute one for the other.

Read the tool's output. `action` will be `"gc"`; `report` contains:

```json
{
  "ttlDays": 14,
  "ship": {"deleted": [...], "kept": [...]},
  "execute": {"deleted": [...], "kept": [...]},
  "plan": {"deleted": [...], "kept": [...]},
  "commit": {"deleted": [...], "kept": [...]},
  "exploreTempdirs": {"deleted": [...], "kept": [...]}
}
```

Print each bucket's deleted and kept entries:
```
[ship]            deleted: ship-deletedbranch-20240101T000000Z.json
[ship]            kept:    ship-main-20260505T120000Z.json
[execute]         deleted: (none)
[exploreTempdirs] deleted: (none)
```

Unlike the source script, this report does not carry a per-file prune reason (e.g. "stale+branch-gone" vs "ttl-fresh") — the underlying Go GC sweep reports flat deleted/kept lists per bucket only. Do not fabricate a reason string; list files without one.

If `errors` is non-empty, display them. Then stop. Do not proceed to step 1b. The pipeline does not run.

## Dry-run mode (R15, R59)

If `--dry-run`, display the full pipeline table and stop. The steps shown below reflect `ShipBuiltInDefaults` (patch bump, threshold high, rebase on) — substitute the actual merged `flags.steps`/`flags.auto`/`flags.draft` from `ship_prepare`'s output when they differ:

```
Ship Pipeline (dry run)
────────────────────────────────────────────────────────────────
Step  Skill                 Status       Args              Pause?
────────────────────────────────────────────────────────────────
1     execute          will run     (none)             no
2     commit           will run     --auto            no
3     review           will run     (none)             no
4     received-review  conditional  (if crit/high)    YES
5     commit (fixes)   conditional  --auto            no
6     version          skipped      —                 —
7     pr               will run     --draft            no
────────────────────────────────────────────────────────────────
Review threshold: critical or high findings trigger fix loop
Interactive pauses: received-review (if triggered)
```

Do not proceed to pipeline execution after printing this table.
