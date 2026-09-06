---
name: verify-pipeline-sdlc
description: "Use this skill to analyze a failed CI run on a PR and either apply a minimal fix or emit a proposal. Dispatched by ship-sdlc's verify-pipeline step when a poll comes back failed under automation.mode: auto, or invoked standalone via /verify-pipeline-sdlc --pr <N>. Triggers on: analyze CI failure, fix failing checks, post-PR CI verification, verify-pipeline."
user-invocable: true
argument-hint: "[--pr <number>] [--logs <path-or-string>] [--auto]"
model: sonnet
---

# Verify Pipeline (SDLC)

Analyze failed CI logs, classify the root cause via the `verify_pipeline_classify` tool, and either apply a minimal in-place fix or emit a proposal as a single JSON line on stdout. This is a one-shot classify/fix skill — it does not poll. Polling for CI status lives in `ship-sdlc`'s own "After pr — verify-pipeline" inline step (see [`/ship-sdlc`](../ship-sdlc/SKILL.md)); by the time this skill runs, a failure has already been observed.

**Announce at start:** "I'm using verify-pipeline-sdlc (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

---

## Step 1: CONSUME — parse args, load logs (R1, R6)

Parse `--pr <N>`, `--logs <path-or-string>`, `--auto` from `$ARGUMENTS`.

If both `--pr` and `--logs` are missing, emit `{"status":"abort","reason":"--pr or --logs required"}` and stop (E1).

If `--logs` is provided: when the value is a filesystem path, read its contents; otherwise treat the value as the log text inline. This is how ship-sdlc dispatches this skill under `automation.mode: auto` — it passes the `checks_raw` text from its own `verify_pipeline_await` poll (the tab-separated check-name/state list, not full CI log output; see Gotchas below).

If `--logs` is omitted but `--pr` is present (R6), resolve logs directly with `gh` (this port has no `fetchFailedCheckLogs` equivalent — see Gotchas):

```bash
gh pr checks "$PR_NUMBER"
```

Find the first row whose state is a failing one (`fail`, `failure`, `cancelled`, `action_required`, `timed_out`). Its link column contains a `/actions/runs/<runId>` URL. Extract `<runId>` and fetch its failure log:

```bash
gh run view "<runId>" --log-failed
```

Use that output as the log text for Step 2. If no failing row is found, or the link has no `runs/<id>` segment, emit `{"status":"abort","reason":"no failed check found"}` and stop.

If `gh` is unauthenticated and logs cannot be resolved, emit `{"status":"abort","reason":"gh not authenticated"}` and stop (E2).

## Step 2: CLASSIFY — invoke the classification tool (R2)

Call the classification tool with the resolved log text:

```
verify_pipeline_classify({logs: LOGS[, check_name: NAME, conclusion: CONCLUSION]})
```

`check_name`/`conclusion` are optional passthrough context — pass them when known (e.g. from a `verify_pipeline_await` `failed_checks` entry's `name`/`state`) so the tool echoes them back for correlation; they do not affect classification.

Read the JSON result: `{"check_name": "...", "conclusion": "...", "category": "<one of seven>", "signals": [...]}`.

The seven categories are: `lint`, `test-failure`, `type-error`, `build-error`, `dependency`, `infra`, `unknown` (R2).

## Step 3: PROPOSE OR APPLY (R3, R4, R9)

Routing by category:

- **`lint`, `test-failure`, `type-error`**: Actionable. When `--auto` is set (R9), use the `Edit` tool to apply the minimal fix (R3): correct the lint violation, fix the failing assertion, add the missing import or correct the type annotation. Do NOT scaffold abstractions or refactor.
- **`build-error`, `dependency`, `infra`**: Non-trivial. Emit a proposal regardless of `--auto` (R4) — these typically require human judgement.
- **`unknown`**: fall through to `proposal` verdict with the raw log excerpt as `summary` (E3).

When NOT running with `--auto`, ALWAYS emit a proposal (no automatic edits) regardless of category (R4, R9).

> **C1 prohibition:** never run `git commit`, `git push`, `git tag`, or any other state-changing git or `gh` command from this skill. C2: never modify files outside the project root.
>
> **Fix:** if a fix is warranted, edit source files only, then hand back to the dispatcher (`fix-applied`) — the dispatcher (ship-sdlc, via `commit-sdlc`) owns committing and pushing.
> **Why:** this skill's only contract with its caller is the Step 4 JSON verdict; committing here would race ship-sdlc's own commit step and break the single-writer assumption the pipeline relies on.

## Step 4: VERDICT — single JSON line on stdout (R5)

Emit exactly one of:

```json
{"status":"fix-applied","filesChanged":["path/a","path/b"],"summary":"<one-line summary>"}
{"status":"proposal","summary":"<diagnosis>","suggestedPatch":"<diff-or-prose>"}
{"status":"abort","reason":"<reason>"}
```

The single JSON line is the contract with the parent dispatcher (ship-sdlc) — anything else on stdout breaks the verdict parser. Logs and progress go to stderr.

## What's Next

When `fix-applied`: ship-sdlc's verify-pipeline step dispatches `commit-sdlc` to commit the fix. Pushing is a manual pause in ship-sdlc — no tool in this port pushes — then polling resumes via `verify_pipeline_await` (R7 — this skill MUST NOT commit itself).

When `proposal`: the user (interactive) or ship-sdlc (logging) reads the proposal and decides whether to apply.

When `abort`: ship-sdlc treats this as a skip-with-warning and proceeds to `await-remote-review`.

## Gotchas

- **No `fetchFailedCheckLogs` port.** The source's `lib/git.js::fetchFailedCheckLogs` helper (structured GitHub Actions log fetch with line-count truncation) has no Go equivalent — there is no tool for it, and it is intentionally out of this port's scope. The standalone `--pr` path above compensates with a direct `gh run view --log-failed` shell-out. The ship-sdlc-dispatched `--auto` path compensates differently: it receives `checks_raw` (the raw `gh pr checks` text ship-sdlc's own poll already captured), which lists failing check *names*, not their log *content*. Classification against `checks_raw` alone is coarser than classification against real log output — expect more `unknown` verdicts on that path than the source skill produced.
- **`verify_pipeline_classify` is not a stepper tool.** Unlike `verify_pipeline_await`, it returns a plain `{category, signals}` payload on every call — there is no `pending` status to loop on here.

## See Also

- [`/ship-sdlc`](../ship-sdlc/SKILL.md) — invokes this skill from its verify-pipeline step under `automation.mode: auto`
- [`/commit-sdlc`](../commit-sdlc/SKILL.md) — invoked by ship-sdlc after this skill returns `fix-applied`
