# Ship Pipeline — Reference

On-demand companion for `ship/SKILL.md` (implements R-progressive-disclosure). Reference material consulted only when the relevant situation arises (a failure, an unexpected behavior, end-of-pipeline learning capture). Read the relevant section on its trigger; never preemptively.

## Error Recovery (R-progressive-disclosure)

> **Flow**: detect → diagnose → auto-recover (retry once if transient) → escalate to user for persistent failures.

| Error | Recovery | Invoke error-report? |
|-------|----------|---------------------------|
| Sub-skill Agent dispatch fails | Show the error from the sub-skill's result, stop the pipeline, save state for `--resume` | Delegated — the sub-skill handles its own error reporting |
| `gh auth status` fails | Stop at validation (Step 3). Tell the user to run `gh auth login` | No — user setup |
| `git add -A -- ':!.sdlc-v2/'` fails | Show the error, stop the pipeline | No — user action needed |
| `gh`/network error during an inline step (verify-pipeline, await-remote-review, archive) | The polling tools (`verify_pipeline_await`, `await_remote_review`) already return a stepper `error` status on transient `gh` failures — log it and re-probe on the next turn rather than aborting immediately | No — transient |
| `ship_state` write fails (`InfraError`) | Warn and continue where safe; if it happens inside `begin-step`/`complete-step`, stop and surface the error — state persistence is not optional for resume correctness | No |
| Resume state file corrupt or missing | `ship_state{action:"read"}` will report the failure; treat as "no prior run" and start fresh | No |
| Review verdict unparseable | Treat as APPROVED WITH NOTES, warn the user, defer all findings | No |
| Sub-skill Agent times out | Stop the pipeline, save state, inform the user to `--resume` | No — transient |

**Resume instruction format** (printed on step failure after retries exhausted or on any unrecoverable step error):
```
Step <N> (<name>) failed: <error summary>
State saved to: <state file path>
To resume: /ship --resume
```

Each sub-skill has its own error recovery. ship does not duplicate their recovery logic — it catches pipeline-level failures (sequencing, state, context) and delegates skill-level failures to the skill itself.

## DO NOT (R-progressive-disclosure)

- Deviate from the fixed per-step dispatch table in Step 2. Unlike the source skill, `ship_prepare`'s output carries no computed `dispatchMode`/`model`/`isolation`/`invocation` per step — this port's SKILL.md itself is the single source of truth for which steps are Agent-dispatched, at which model, and with which args. Do not invent a `dispatchMode` value or dispatch a scaffolded step (execute, commit, review, received-review, commit-fixes, version, pr) via the Skill tool — always the Agent tool, always with the model fixed in that table.
- Skip the critique step (Step 3) even when all checks seem obvious.
- Forward `--auto` to sub-skills that do not support it (see the `--auto` Mode Audit table in the main skill).
- Automatically resolve review findings — received-review is always interactive unless `--auto`/`automation.mode: unattended` was explicitly forwarded.
- Run pipeline steps in parallel — the pipeline is strictly sequential.
- Delete the state file on failure — it is needed for `--resume`.
- Proceed past a failed sub-skill — stop, save state, inform the user.
- Skip pipeline steps that were marked "will run" in the pipeline plan. The pipeline plan is a contract with the user. If a step was planned to run and the user confirmed the pipeline, it MUST run. The LLM does not have authority to skip planned steps based on its own assessment of change complexity or risk. Only `ship_prepare`'s resolved `steps`/`sources` and the auto-skip rules documented in the main skill control which steps run.
- Add `--steps`/`steps` composition not present in the user's original invocation or `.sdlc-v2/local.json`. Pipeline composition derives from `ship_prepare`'s `steps`/`sources` output — CLI `--steps` > `--quick` > config `ship.steps[]` > built-in defaults. Legacy `--preset` and `--skip` are hard-removed; `ship_prepare` has no fields for them.
- Dispatch a scaffolded pipeline-step Agent without `model:` fixed in the Step 2 table. Omitting it defaults the Agent to opus.
- Pass `isolation: "worktree"` (or any other `isolation` value) to any Agent dispatch from this skill (R-agent-isolation-script-driven). ship never creates an Agent SDK worktree — workspace isolation for the `execute` step is a plain `git checkout -b` run by this skill's own prose before dispatch (see "Pre-execute workspace auto-detection"). Adding `isolation: "worktree"` creates a `.claude/worktrees/agent-<id>` path that conflicts with `.sdlc-v2/` anchoring and breaks state-file resolution. (Mirrors the same constraint already documented in `execute/SKILL.md`.)
- Ignore a cleanup contract violation. If `ship_state{action:"cleanup-pipeline"}` reports `currentRun.valid === false`, the pipeline contract was violated (a scaffolded step is `in_progress`, or `pending` without a `condition`). Surface the violation and leave the state file in place — do not force-delete it.
- Skip the post-version ancestry HARD GATE. `verify_tag_ancestry({tag: NEW_TAG})` is the only safeguard against a tag landing on an orphaned commit. The gate is a no-op when `NEW_TAG` was never captured (version step skipped or not yet run) — do not pre-empt it by skipping it when you believe the version step succeeded on the right branch.
- Treat `automation.mode: unattended`/`--auto` as license to skip the version step's manual tag-and-push pause. `version_apply` does not create or push a git tag (see `version/SKILL.md`'s own Gotchas) — there is no tool in this port that does. Even in full auto mode, this pipeline must pause after the version step completes and ask the user to create and push the release tag before it can call `ship_verify_side_effect({step:"version", expected:"tag"})`. Do not fabricate a passing side-effect check, and do not silently skip the check and proceed to `pr`.
- End your response turn between pipeline steps. Each step is part of a single dispatch loop. After every tool call result, check whether the current step is complete and proceed to the next action. Do not wait for a user message to continue.
- Interpret a tool-call result as a natural stopping point. Processing a `ship_state`/Bash/TodoWrite result is not the end of the pipeline — it is one action in a multi-action step. Continue to the next action immediately.
- Treat the `PostToolUse` hook's `additionalContext` reminder as optional or advisory. When a step is `in_progress`, `pipeline-continue` emits a mandatory continuation signal — this is a directive you MUST act on, not an FYI. Complete the stated next action (dispatch the step's Agent / record its result) before ending your response turn. The `stop-pipeline-continue` Stop hook enforces this for `in_progress` steps regardless of `--auto`.
- Call an inline step's progress `ship_state{action:"start"|"begin-step"|"complete"|"complete-step"|"skip"|"fail"}` with one of the five inline step names (`verify-openspec`, `archive-openspec`, `verify-pipeline`, `await-remote-review`, `learnings-commit`). None of those five names exist in `steps[]` — the tool returns `step %q not found in state` for any of them under those six actions. Record inline-step progress with `ship_state{action:"decide", step, detail:{text}}` instead (see `state-format.md`).

## Gotchas (R-progressive-disclosure)

**Staging gap after execute.** `execute` creates and modifies files but does not stage them. ship must run `git add -A -- ':!.sdlc-v2/'` between execute and commit. Missing this produces an empty commit.

**Verdict detection is text-based.** Parse the conversation for a line matching `Verdict: <VERDICT>`. The review orchestrator always emits this. If the conversation is compacted between review and verdict parsing, the verdict may be lost — treat a missing verdict as APPROVED WITH NOTES and warn the user.

**received-review supports `--auto`.** When forwarded, both its consent prompt and its reply/resolve prompt are skipped. "Will fix" items are auto-implemented and their threads auto-resolved via in-thread replies. "Disagree"/"won't fix" items are displayed but not auto-implemented; their threads are replied to but left open for the reviewer. Critique gates and verification still run. Without `--auto`, the pipeline pauses for human approval at both gates.

**Double commit is intentional.** The feature commit (step 2) and the review-fix commit (step 5) are separate `commit_apply` calls. This keeps feature work and review fixes distinct in git history. Do not squash them.

**Version step never creates or pushes a tag.** `version_apply` bumps the version file and writes the changelog only — see `version/SKILL.md`'s own Gotchas ("Tag and push are manual"). This port's version-step completion must pause for the user to create and push `NEW_TAG` by hand before the pipeline can verify the side effect and proceed. This is the single largest behavioral divergence from the source skill, which trusted its own `version.js` to tag and push automatically — see the main skill's "After execute — version" section.

**Config file is optional.** The pipeline runs on `ShipBuiltInDefaults` when no `ship` section exists in `.sdlc-v2/local.json`. Do not error on a missing config — `ship_prepare` already handles this.

**Step-set validation matters.** An unrecognized value in `--steps`/`ship.steps[]` (e.g. `reviw`) is rejected by `ship_prepare`. The single source of truth for step composition is `ship_prepare`'s `steps`/`sources` output. Legacy `--preset`/`--skip` are hard-removed — `ship_prepare` has no input fields for them.

**`.sdlc/` must be gitignored.** The `.sdlc/` directory holds developer-local config (`local.json`) and ephemeral pipeline state (`execution/`). This port's `--init-config` entry mode redirects to `/setup` rather than writing `.gitignore` itself (see `entry-modes.md`). If `.sdlc/` is not gitignored, the staging command (`git add -A -- ':!.sdlc-v2/'`) provides a fallback exclusion, but the gitignore remains the primary defense.

**Pipeline plan is binding.** The pipeline table displayed in Step 4 and confirmed by the user is a contract. Step statuses (`will_run`, `skipped`, `conditional`) come from `ship_prepare`'s resolved flags — the LLM follows them, it does not override them. A step marked `will_run` must be dispatched. This mirrors the source incident where a review step was skipped because the LLM judged the changes "just docs/config" — the pipeline's value is precisely in catching cases where the developer thinks changes are low-risk but review disagrees.

**State files are tool-managed.** Use `ship_state` (and `execute_state` for the execute sub-pipeline) for every state read/write. Never hand-write JSON to `.sdlc-v2/execution/`. See `state-format.md` for the exact schema and the `steps[]` scaffolding gap (only 7 of the 13 known step names get a tracked entry).

**No Agent SDK worktrees.** ship isolates the `execute` step with a plain `git checkout -b <branch>` run by this skill itself, then dispatches `execute` without `--branch` so its own Step 1 sees a non-default current branch and yields `continue`. There is no `EnterWorktree`/`ExitWorktree` tool use anywhere in this pipeline, and no `isolation: "worktree"` on any Agent dispatch.

**Rebase happens after all commits, before version.** This ensures the release tag lands on a commit that can merge cleanly. If rebase conflicts, the pipeline pauses — the user resolves in place and resumes.

**Rebase is skipped when the default branch is already an ancestor.** `git merge-base --is-ancestor` is a fast check; no fetch/rebase overhead when the branch is already up to date.

**Auto mode does not auto-resume without `--resume`.** When `auto` is set but `resume` is not, the pipeline starts fresh even if a state file exists for the current branch. This prevents accidental continuation from stale state. The state file is preserved (not deleted) so the user can explicitly `--resume` later.

**Sub-skill dispatch is context-isolated.** Every sub-skill step (including `execute`) is Agent-dispatched so it loads its own SKILL.md in its own context and returns only a structured result. ship's own context receives that structured data, not the sub-skill's definition. `execute` bounds its own context impact by dispatching one Agent per wave-task/task-cluster rather than per task; its Step-9 structured result is what this pipeline consumes to continue.

**`sources` tracks provenance.** `ship_prepare`'s `sources` map records, per resolved flag, which tier supplied the value: `"cli"`, `"quick"`, `"config"`, or `"default"` (see `config-format.md`'s Merge Precedence). Use it to explain to the user why a step is running or skipped, instead of re-deriving the reason yourself.

**No `workspace` config field, no worktree mode.** Unlike the source skill, this port's `ship_prepare` reads no `ship.workspace` config field and has no worktree-isolation path — every ship run isolates `execute` with a feature branch. If you need `execute`'s own worktree mode, invoke `/execute --workspace worktree` standalone, outside this pipeline.

## Learning Capture (R-progressive-disclosure)

After completing the pipeline, call `learnings_log({action: "append", entry: "## YYYY-MM-DD — ship: <brief summary>\n<what was learned>"})`, covering:

- Review verdicts that surprised (threshold too aggressive or too lenient)
- Sub-skills that failed in unexpected ways during chaining
- Config combinations that produced unintended pipeline shapes
- Cases where the manual tag-and-push pause (see the version step) was confusing or where the ancestry gate caught a real mistake

The tool resolves the MAIN git worktree's log regardless of which worktree this pipeline is
isolating `execute` in, and `.sdlc-v2/learnings/` is gitignored — this entry is never
committed (see the `learnings-commit` step in [`SKILL.md`](SKILL.md)).
