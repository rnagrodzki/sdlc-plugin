# Ship Pipeline — Reference

On-demand companion for `ship/SKILL.md` (implements R-progressive-disclosure). Reference material consulted only when the relevant situation arises (a failure, an unexpected behavior, end-of-pipeline learning capture). Read the relevant section on its trigger; never preemptively.

## Error Recovery (R-progressive-disclosure)

> **Flow**: detect → diagnose → auto-recover (retry once if transient) → escalate to user for persistent failures.

| Error | Recovery | Invoke error-report? |
|-------|----------|---------------------------|
| Sub-skill Agent dispatch fails | Show the error from the sub-skill's result, stop the pipeline, save state for `--resume` | Delegated — the sub-skill handles its own error reporting |
| `gh auth status` fails | Stop at Step loop item 5. Tell the user to run `gh auth login` | No — user setup |
| `git add -A -- ':!.sdlc-v2/'` fails | Show the error, stop the pipeline | No — user action needed |
| `gh`/network error during an inline step (verify-pipeline, await-remote-review, archive) | `poll_await({target:"pipeline"|"remote_review", ...})` already returns a stepper `error` status on transient `gh` failures — log it and re-probe on the next turn rather than aborting immediately | No — transient |
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

- Deviate from the fixed per-step dispatch rules in the Steps section. Unlike the source skill, `ship_prepare`'s output carries no computed `dispatchMode`/`model`/`isolation`/`invocation` per step — this port's SKILL.md itself is the single source of truth for which steps are Agent-dispatched, at which model, and with which args. Do not invent a `dispatchMode` value or dispatch an Agent-dispatched step (execute, commit, review, received-review, commit-fixes, version, pr) via the Skill tool — always the Agent tool, always with the model fixed in that section.
- Forward `--auto` to sub-skills that do not support it — see each step's own entry in the Steps section of the main skill for exactly which steps accept it (`commit`, `received-review`, `commit-fixes`, `version`, plus the conditional `verify-pipeline`/`received-review` dispatches inside the `verify-pipeline` and `await-remote-review` inline steps; not `execute`, `review`, `pr`, `verify-openspec`, `archive-openspec`, `learnings-commit`).
- Automatically resolve review findings — received-review is always interactive unless `--auto`/`automation.mode: unattended` was explicitly forwarded.
- Run pipeline steps in parallel — the pipeline is strictly sequential.
- Delete the state file on failure — it is needed for `--resume`.
- Proceed past a failed sub-skill — stop, save state, inform the user.
- Skip pipeline steps that were marked "will run" in the pipeline plan. The pipeline plan is a contract with the user. If a step was planned to run and the user confirmed the pipeline, it MUST run. The LLM does not have authority to skip planned steps based on its own assessment of change complexity or risk. Only `ship_prepare`'s resolved `steps`/`sources` and the auto-skip rules documented in the main skill control which steps run.
- Add `--steps`/`steps` composition not present in the user's original invocation or `.sdlc-v2/local.json`. Pipeline composition derives from `ship_prepare`'s `steps`/`sources` output — CLI `--steps` > `--quick` > config `ship.steps[]` > built-in defaults. Legacy `--preset` and `--skip` are hard-removed; `ship_prepare` has no fields for them.
- Dispatch a pipeline-step Agent without `model:` fixed in that step's Steps-section entry. Omitting it defaults the Agent to opus.
- Pass `isolation: "worktree"` (or any other `isolation` value) to any Agent dispatch from this skill (R-agent-isolation-script-driven). ship never creates an Agent SDK worktree — workspace isolation for the `execute` step is a plain `git checkout -b` run by this skill's own prose before dispatch (see SKILL.md's Step loop, item 3). Adding `isolation: "worktree"` creates a `.claude/worktrees/agent-<id>` path that conflicts with `.sdlc-v2/` anchoring and breaks state-file resolution. (Mirrors the same constraint already documented in `execute/SKILL.md`.)
- Ignore a cleanup contract violation. If `ship_state{action:"cleanup-pipeline"}` reports `currentRun.valid === false`, the pipeline contract was violated (a `steps[]` entry — tracked or inline, any configured step — is `in_progress`, or `pending` without a `condition`). Surface the violation and leave the state file un-stamped — `failed` never trips this check, only `in_progress` and condition-less `pending` do (see `state-format.md`'s Lifecycle: Cleanup).
- Call `ship_state{action:"decide"}` and assume it terminal-izes a step. `decide` only appends to `decisions[]` — it never writes `steps[].status`. An inline-kind step configured in `flags.steps` still needs `begin-step` → `complete-step` (or `skip`/`fail`) like any tracked step, or it sits `pending` forever and blocks both the next tracked step's `begin-step` (R-b1) and the final cleanup contract check. See `state-format.md`'s "Every configured step needs a terminal status."
- Call `ship_prepare` to "start fresh" without checking for a `resumeBriefing` first. `ship_prepare`'s init sequence unconditionally deletes (best-effort) any other `ship-<slug>-*.json` file for the same branch as a side effect of writing the new one (`internal/state.Write`'s prune-on-write). If `ship_state{action:"read"}` returned a `resumeBriefing`, a prior run is genuinely in flight and calling `ship_prepare` destroys its state irrecoverably — confirm with the user before doing so (see SKILL.md's Decisions & gates).
- Skip the post-version ancestry HARD GATE. `verify_tag_ancestry({tag: NEW_TAG})` is the only safeguard against a tag landing on an orphaned commit. The gate is a no-op when `NEW_TAG` was never captured (version step skipped or not yet run) — do not pre-empt it by skipping it when you believe the version step succeeded on the right branch.
- Treat `automation.mode: unattended`/`--auto` as license to skip the version step's manual tag-and-push pause. `version_apply` does not create or push a git tag (see `version/SKILL.md`'s own Gotchas) — there is no tool in this port that does. Even in full auto mode, this pipeline must pause after the version step completes and ask the user to create and push the release tag before it can call `ship_verify_side_effect({step:"version", expected:"tag"})`. Do not fabricate a passing side-effect check, and do not silently skip the check and proceed to `pr`.
- End your response turn between pipeline steps. Each step is part of a single dispatch loop. After every tool call result, check whether the current step is complete and proceed to the next action. Do not wait for a user message to continue.
- Interpret a tool-call result as a natural stopping point. Processing a `ship_state`/Bash/TodoWrite result is not the end of the pipeline — it is one action in a multi-action step. Continue to the next action immediately.
- Treat the `PostToolUse` hook's `additionalContext` reminder as optional or advisory. When a step is `in_progress`, `pipeline-continue` emits a mandatory continuation signal — this is a directive you MUST act on, not an FYI. Complete the stated next action (dispatch the step's Agent / record its result) before ending your response turn. The `stop-pipeline-continue` Stop hook enforces this for `in_progress` steps regardless of `--auto`.
- Call `ship_state{action:"begin-step"|"complete-step"|"start"|"complete"|"skip"|"fail"}` with `step:"received-review"` or `step:"commit-fixes"`. Those two names are never members of `flags.steps` (they are review-verdict-triggered conditionals — see `config-format.md`), so `ship_prepare`'s scaffold never creates a `steps[]` entry for them, and the tool returns `step %q not found in state`. Record their progress with `ship_state{action:"decide", step, detail:{text}}` instead — this is the **only** exception to the rule below.
- Skip a configured inline step's `begin-step`/`complete-step` pair (`verify-openspec`, `archive-openspec`, `verify-pipeline`, `await-remote-review`, `learnings-commit`, when present in `flags.steps`). Despite the `kind:"inline"` label (meaning "no sub-skill Agent — do the work in this skill's own prose"), these names get a real `pending` `steps[]` entry exactly like tracked steps, and `decide` never changes that entry's status. Skipping the lifecycle calls leaves the entry `pending` forever, which blocks a later tracked step's `begin-step` (R-b1) and the terminal cleanup contract check. Treat `kind` as a dispatch-style signal only, never as a lifecycle exemption.

## Gotchas (R-progressive-disclosure)

**Staging gap after execute.** `execute` creates and modifies files but does not stage them. ship must run `git add -A -- ':!.sdlc-v2/'` between execute and commit. Missing this produces an empty commit.

**Verdict detection is text-based.** Parse the conversation for a line matching `Verdict: <VERDICT>`. The review orchestrator always emits this. If the conversation is compacted between review and verdict parsing, the verdict may be lost — treat a missing verdict as APPROVED WITH NOTES and warn the user.

**received-review supports `--auto`.** When forwarded, both its consent prompt and its reply/resolve prompt are skipped. "Will fix" items are auto-implemented and their threads auto-resolved via in-thread replies. "Disagree"/"won't fix" items are displayed but not auto-implemented; their threads are replied to but left open for the reviewer. Critique gates and verification still run. Without `--auto`, the pipeline pauses for human approval at both gates.

**Double commit is intentional.** The feature commit (step 2) and the review-fix commit (step 5) are separate `commit_apply` calls. This keeps feature work and review fixes distinct in git history. Do not squash them.

**Version step never creates or pushes a tag.** `version_apply` bumps the version file and writes the changelog only — see `version/SKILL.md`'s own Gotchas ("Tag and push are manual"). This port's version-step completion must pause for the user to create and push `NEW_TAG` by hand before the pipeline can verify the side effect and proceed. This is the single largest behavioral divergence from the source skill, which trusted its own `version.js` to tag and push automatically — see the main skill's Decisions & gates, "Manual tag-and-push pause."

**Config file is optional.** The pipeline runs on `ShipBuiltInDefaults` when no `ship` section exists in `.sdlc-v2/local.json`. Do not error on a missing config — `ship_prepare` already handles this.

**Step-set validation matters.** An unrecognized value in `--steps`/`ship.steps[]` (e.g. `reviw`) is rejected by `ship_prepare`. The single source of truth for step composition is `ship_prepare`'s `steps`/`sources` output. Legacy `--preset`/`--skip` are hard-removed — `ship_prepare` has no input fields for them.

**`.sdlc-v2/` must be gitignored.** The `.sdlc-v2/` directory holds developer-local config (`local.json`) and ephemeral pipeline state (`execution/`). This port's `--init-config` entry mode redirects to `/setup` rather than writing `.gitignore` itself (see `entry-modes.md`). If `.sdlc-v2/` is not gitignored, the staging command (`git add -A -- ':!.sdlc-v2/'`) provides a fallback exclusion, but the gitignore remains the primary defense.

**Pipeline plan is binding.** The pipeline table displayed and confirmed at Step loop item 6 is a contract. Step statuses (`will_run`, `skipped`, `conditional`) come from `ship_prepare`'s resolved flags — the LLM follows them, it does not override them. A step marked `will_run` must be dispatched. This mirrors the source incident where a review step was skipped because the LLM judged the changes "just docs/config" — the pipeline's value is precisely in catching cases where the developer thinks changes are low-risk but review disagrees.

**State files are tool-managed.** Use `ship_state` (and `execute_state` for the execute sub-pipeline) for every state read/write. Never hand-write JSON to `.sdlc-v2/execution/`. See `state-format.md` for the exact schema — `steps[]` gets one entry per name in `flags.steps`; only `received-review`/`commit-fixes` never get an entry (see the DO NOT list above).

**No Agent SDK worktrees.** ship isolates the `execute` step with a plain `git checkout -b <branch>` run by this skill itself, then dispatches `execute` without `--branch` so its own Step 1 sees a non-default current branch and yields `continue`. There is no `EnterWorktree`/`ExitWorktree` tool use anywhere in this pipeline, and no `isolation: "worktree"` on any Agent dispatch.

**Rebase happens after all commits, before version.** This ensures the release tag lands on a commit that can merge cleanly. If rebase conflicts, the pipeline pauses — the user resolves in place and resumes.

**Rebase is skipped when the default branch is already an ancestor.** `git merge-base --is-ancestor` is a fast check; no fetch/rebase overhead when the branch is already up to date.

**Auto mode does not auto-resume without `--resume`.** When `auto` is set but `resume` is not, the pipeline starts fresh even if a state file exists for the current branch. This prevents accidental continuation from stale state. **Caution:** starting fresh calls `ship_prepare`, whose init sequence deletes any other same-branch `ship-*.json` file as a side effect — if that prior file was still resumable (a real `resumeBriefing`, not a stamped-complete run), starting fresh destroys it. Confirm with the user first (see SKILL.md's Decisions & gates).

**Cleanup stamps, it does not delete.** `ship_state{action:"cleanup"|"cleanup-pipeline"}` marks a terminal run with `pipelineStatus:"completed"`/`pipelineCompletedAt` and writes it back — the state file stays on disk (readable, prunable only by GC's TTL) instead of being removed. `force:true` on `cleanup-pipeline` skips the contract check *and* skips the stamp (`{"cleaned":false,"preservedReason":"force"}`) — it does not force a stamp through.

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
