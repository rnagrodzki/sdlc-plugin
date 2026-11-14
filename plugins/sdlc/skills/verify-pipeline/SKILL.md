---
name: verify-pipeline
description: "Use this skill to analyze a failed CI run on a PR and either apply a minimal fix or emit a proposal. Dispatched by ship's verify-pipeline step when a poll comes back failed under automation.mode: auto, or invoked standalone via /verify-pipeline --pr <N>. Arguments: [--pr <number>] [--logs <path-or-string>] [--auto] [--no-instructions]. Use --no-instructions to skip the verify-pipeline instructions of the config; ship always passes it. Triggers on: analyze CI failure, fix failing checks, post-PR CI verification, verify-pipeline."
user-invocable: true
argument-hint: "[--pr <number>] [--logs <path-or-string>] [--auto] [--no-instructions]"
model: sonnet
---

# Verify Pipeline (SDLC)

Analyze failed CI logs, classify the root cause via the `verify_pipeline_classify` tool, and either apply a minimal in-place fix or emit a proposal as a single JSON line on stdout. This is a one-shot classify/fix skill — it does not poll. Polling for CI status lives in `ship`'s own "After pr — verify-pipeline" inline step (see [`/ship`](../ship/SKILL.md)); by the time this skill runs, a failure has already been observed.

**Announce at start:** "I'm using verify-pipeline (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

**Communication style:** Follow the `sdlc communication style` block in session context for every explanation, status line, summary, and AskUserQuestion text in this skill. Do not apply it to commit messages, PR bodies, review comments, or Jira text: they follow their own templates and config.

---

## Step 1: CONSUME — parse args, load logs (R1, R6)

Parse `--pr <N>`, `--logs <path-or-string>`, `--auto`, `--no-instructions` from `$ARGUMENTS`.

`--no-instructions` is independent of `--auto` and `--pr`: each flag applies. It skips Step 1b and Step 5. Ship passes `--no-instructions` on each dispatch, so a ship run prints only the JSON verdict line.

If both `--pr` and `--logs` are missing, emit `{"status":"abort","reason":"--pr or --logs required"}` and stop (E1).

If `--logs` is provided: when the value is a filesystem path, read its contents; otherwise treat the value as the log text inline. This is how ship dispatches this skill under `automation.mode: auto` — it passes the `checks_raw` text from its own `poll_await({target: "pipeline"})` poll (the tab-separated check-name/state list, not full CI log output; see Gotchas below).

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

## Step 1b: Resolve instructions

Skip this step when `--no-instructions` was passed. Otherwise, before Step 2, run:

```
instructions_resolve({ key: "verify-pipeline" }) → INSTRUCTIONS
```

Keep `INSTRUCTIONS.instructions` for Step 5. The list is empty when the config has none.

**On tool error (a DataError for bad config content, or an InfraError):** print the error message and its suggestion, then stop before Step 2. Print no JSON verdict line. No classification ran. The user fixes the config and runs `/verify-pipeline` again.

## Step 2: CLASSIFY — invoke the classification tool (R2)

Call the classification tool with the resolved log text:

```
verify_pipeline_classify({logs: LOGS[, check_name: NAME, conclusion: CONCLUSION]})
```

`check_name`/`conclusion` are optional passthrough context — pass them when known (e.g. from a `poll_await({target: "pipeline"})` `failed_checks` entry's `name`/`state`) so the tool echoes them back for correlation; they do not affect classification.

The result is Markdown, not JSON (see `docs/internal/mcp-output-contract.md`). Read these fields from it: `category` (one of the seven below), `signals` (every matched pattern as `<prefix>:<pattern>`; `(none)` when nothing matched), and `check_name`/`conclusion`, which appear only when you passed them.

The seven categories are: `lint`, `test-failure`, `type-error`, `build-error`, `dependency`, `infra`, `unknown` (R2).

## Step 3: PROPOSE OR APPLY (R3, R4, R9)

Routing by category:

- **`lint`, `test-failure`, `type-error`**: Actionable. When `--auto` is set (R9), use the `Edit` tool to apply the minimal fix (R3): correct the lint violation, fix the failing assertion, add the missing import or correct the type annotation. Do NOT scaffold abstractions or refactor.
- **`build-error`, `dependency`, `infra`**: Non-trivial. Emit a proposal regardless of `--auto` (R4) — these typically require human judgement.
- **`unknown`**: fall through to `proposal` verdict with the raw log excerpt as `summary` (E3).

When NOT running with `--auto`, ALWAYS emit a proposal (no automatic edits) regardless of category (R4, R9).

> **C1 prohibition:** never run `git commit`, `git push`, `git tag`, or any other state-changing git or `gh` command from this skill, except an instruction from config (Step 5, standalone run only). The fix of Step 3 is never committed or pushed by this skill. C2: never modify files outside the project root, except an instruction from config (Step 5, standalone run only).
>
> **Fix:** if a fix is warranted, edit source files only, then hand back to the dispatcher (`fix-applied`) — the dispatcher (ship, via `commit`) owns committing and pushing.
> **Why:** this skill's only contract with its caller is the Step 4 JSON verdict; committing here would race ship's own commit step and break the single-writer assumption the pipeline relies on.

## Step 4: VERDICT — single JSON line on stdout (R5)

Emit exactly one of:

```json
{"status":"fix-applied","filesChanged":["path/a","path/b"],"summary":"<one-line summary>"}
{"status":"proposal","summary":"<diagnosis>","suggestedPatch":"<diff-or-prose>"}
{"status":"abort","reason":"<reason>"}
```

The single JSON line is the contract with the parent dispatcher (ship) — anything else on stdout breaks the verdict parser. Logs and progress go to stderr. The only text allowed after the JSON line is the Step 5 summary of a standalone run. Ship passes `--no-instructions`, so the ship parser never sees that summary.

## Step 5: Run instructions

Skip this step when `--no-instructions` was passed, when `INSTRUCTIONS.count` is 0, or when the verdict is `abort`. Run it only after Step 4 printed a `fix-applied` or `proposal` verdict line. Never run it before the JSON line.

Read `plugins/sdlc/skills/ship/instructions-run.md` and follow its "Loop without run state" section. The run mode is always interactive: `--auto` does not change it. Run each instruction of `INSTRUCTIONS.instructions` in order, in this session. An instruction from config may run git or gh commands. Then print the summary line on the lines after the JSON line:

```text
{"status":"fix-applied","filesChanged":["path/a"],"summary":"<one-line summary>"}
Instructions (verify-pipeline): N total, X done, Y failed, Z not run.
- verify-pipeline #1 (team) failed: <one line of cause>
```

There is no run state to write. If the list stops before its end (an interrupt), print:
`Run the remaining instructions by hand. A new run of the skill runs the whole list again unless you pass --no-instructions.`

| Exit route | List runs | Next instruction |
|---|---|---|
| resolve error (Step 1b) | no | print the error and its suggestion, stop. The user fixes the config. |
| `abort` verdict (Step 1 or Step 4) | no | the `abort` text of What's Next |
| `fix-applied` or `proposal` verdict, no `--no-instructions` | yes, after the JSON line | the summary line |
| Step 5 interrupted | partial | print the interrupt line above |

## What's Next

When `fix-applied`: ship's verify-pipeline step dispatches `commit` to commit the fix. The commit does not push, because ship does not pass `--push` after the rebase. Pushing is a manual pause in ship, then polling resumes via `poll_await({target: "pipeline"})` (R7 — this skill MUST NOT commit itself).
In a standalone run, the `Instructions (verify-pipeline): …` line follows the JSON line.

When `proposal`: the user (interactive) or ship (logging) reads the proposal and decides whether to apply.

When `abort`: ship treats this as a skip-with-warning and proceeds to `await-remote-review`.

## Gotchas

- **No `fetchFailedCheckLogs` port.** The source's `lib/git.js::fetchFailedCheckLogs` helper (structured GitHub Actions log fetch with line-count truncation) has no Go equivalent — there is no tool for it, and it is intentionally out of this port's scope. The standalone `--pr` path above compensates with a direct `gh run view --log-failed` shell-out. The ship-dispatched `--auto` path compensates differently: it receives `checks_raw` (the raw `gh pr checks` text ship's own poll already captured), which lists failing check *names*, not their log *content*. Classification against `checks_raw` alone is coarser than classification against real log output — expect more `unknown` verdicts on that path than the source skill produced.
- **`verify_pipeline_classify` is not a stepper tool.** Unlike `poll_await`, it returns the same Markdown fields (`category`, `signals`) on every call — there is no `pending` status to loop on here.

## See Also

- [`/ship`](../ship/SKILL.md) — invokes this skill from its verify-pipeline step under `automation.mode: auto`
- [`/commit`](../commit/SKILL.md) — invoked by ship after this skill returns `fix-applied`
