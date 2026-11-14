# Flags and gotchas

Read this page when you need a flag of `/sdlc:plan`, when the skill asks about OpenSpec, or when a plan run stops and you want to know the cause.

```
/sdlc:plan --auto --spec user-onboarding
```

This command plans from the change `openspec/changes/user-onboarding/` and
skips the questions that have a safe default.

## Syntax

```
/sdlc:plan [options] [spec-file-path...]
```

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `[spec-file-path...]` | Path to a requirements or spec file to plan from. A path into `openspec/changes/<name>/` selects that change, same as `--spec <name>`. Two or more paths must all be preplan topic files. Put a path with spaces in double quotes. `--spec` with two or more paths stops the plan. | none |
| `--auto` | Skip interactive prompts. Picks conservative defaults. Two prompts stay: a Gate A `CRITICAL` verdict still blocks, and the per-finding questions at the review-loop limit are still asked (only the harden offer is left out). `--auto` (or a session with no AskUserQuestion) takes Conclude at every conclude question: at the Mid-run and After handoff points, and at the Step 6.5 and 6.6 gates. At a gate it fixes the findings and runs the gate again, 3 tries, then it takes Stop (`plan_mark` `stopped`) and reports the findings. | off |
| `--spec [<change-name>]` | Opts into OpenSpec and skips the gate-check question. With a name: plan from that existing change (`openspec/changes/<change-name>/`). Without one: use the branch-matched change, else ask which active change to use, or offer to create a new one. | off |
| `--from-openspec <name>` | Deprecated alias of `--spec <name>`, kept for one release. Prints a deprecation notice. | none |

The conclude question and its points are in
[Conclude, stop and recover a run](./conclude-and-recovery.md#conclude-question).
The topic file rules are in [Preplan topic file](./topic-file.md).

## Plan file and handoff

- **Plan files are saved to disk.** The plan is written to a file. You pass
  that file path to `/execute` or `/ship` later — they do not pick it up
  automatically.
- **Plan mode vs. normal mode.** `/sdlc:plan` is the designated skill of
  plan mode, the built-in planning mode of Claude Code. In plan mode, the
  skill writes to the designated plan file path. In normal mode, it creates
  a file and tells you where it is.
- **The hand-off menu can offer `openspec-save`.** When the plan file header
  has an `**OpenSpec-Staging:**` line, the menu also lists `openspec-save`
  (`/sdlc:openspec-save --plan <path>`). It saves the OpenSpec change as its own
  PR before you ship the code. Without that header line, the menu has no such
  entry.
- **`plan_prepare` always runs first, but it only resumes state when asked.**
  The skill's first action is always a `plan_prepare(...)` call. By default it
  starts a fresh run. Pass `resume: true` and the `runId` to reuse that run instead —
  the skill does this automatically after a compaction (see
  [Recovery after compaction](./conclude-and-recovery.md#recovery-after-compaction)).

## Scope routing

The size of the change sets how much of the pipeline runs.

| Scope | Normal mode | Plan mode |
|---|---|---|
| 1 file, clear change | No plan. The skill tells you that no plan is needed | Lightweight plan |
| 2-3 files, clear scope | Lightweight plan: no exploration and no review loop | Lightweight plan |
| 4+ files or unclear scope | Full pipeline with multi-agent exploration (Steps 1 to 7) | Full pipeline |

When the requirements cover independent subsystems, the skill recommends a
split into separate plans and asks you: one plan or a split. `--auto` does
not skip this question.

## OpenSpec

- **OpenSpec is opt-in, but a feature-shaped request without `--spec` may
  still get asked.** `--spec <name>` (or a spec path into
  `openspec/changes/<name>/`, or the deprecated `--from-openspec <name>`)
  opts in directly and skips the question. Without it: a non-functional
  request (refactor, config, docs, etc.) just gets a one-line hint; a
  feature-shaped request uses the branch-matched change silently if one
  exists, otherwise it triggers a gate question — **Create OpenSpec change**
  / **Use existing change** / **Skip OpenSpec**.
- **Gate A (Intake Audit) can block decomposition.** When the plan is built
  from an existing OpenSpec change that has a requirements inventory, an
  audit agent checks the change's proposal, delta specs, tasks, and design
  before tasks are written. A `CRITICAL` verdict stops the skill before
  decomposition: you either fix the change's artifacts and re-run, or
  override and proceed, which records the override in an
  `## Intake Audit Caveats` section of the plan. `--auto` does not bypass a
  `CRITICAL` verdict. `WARNING` or `SUGGESTION` findings are added to that
  same section and planning continues. Gate A does not run for a plan
  without OpenSpec, or for a change the skill creates (Create OpenSpec
  change). That change is authored from the reviewed plan at the end of
  Step 6. When you reject the handoff with feedback and then pick Conclude,
  a plan with an `**OpenSpec-Staging:**` line authors the change again. Every
  plan then runs the link and format checks again. After 3 failed tries the
  skill shows the findings and ends.
- **The OpenSpec stage refuses an incomplete or busy change.** `openspec_stage`
  needs one file for each artifact that the OpenSpec schema lists. A missing
  artifact gives `valid: false` and a `missing artifact <outputPath>` line in
  `validateOutput`, so the skill fixes the artifacts and stages again. Each
  stage takes the lock `.sdlc-v2/openspec-staging/<change>.lock`, writes into a
  temp folder and renames it into place. A second stage of the same change
  while the lock is younger than 10 minutes gets `staging is locked by another
  stage`. Wait for the first stage to finish, or delete the lock file when no
  stage runs.
- **PF15 refuses the handoff when OpenSpec authoring is not finished.** The
  skill passes `handoff:true` at the Step 6.6 format gate and in the rerun
  after a rejected handoff (every plan, after you pick Conclude). PF15 finds a `**OpenSpec-Create:**` line with no
  `**OpenSpec-Staging:**` line, an Appendix that still starts with `[TBD`, and a
  staged change with a missing `stage.json`, a missing `validatedAt` or a
  missing staged file. Each finding has a `fix` text. Follow it, then the skill
  runs the gate again. No finding means Step 7 runs. A plan with no OpenSpec
  header line passes PF15.

### Staging a created change can stop the skill

The skill stages the authored files with `openspec_stage`. These exits apply:

- `staged by another plan`: another plan file staged this change name.
  If the error names this plan file under another path, the skill calls
  again with that path. Otherwise the skill asks you whether the other
  plan is abandoned, and on yes deletes its staging directory and stages
  again (under `--auto` it does not ask and does not delete). Otherwise it
  picks a new change name, rewrites the
  `**Source:**`, `**OpenSpec-Create:**` and `**OpenSpec-Staging:**`
  header lines, and authors the change again. A refused call does not
  count as a try.
- After 3 such refusals, the skill shows the error and stops with no plan
  handoff. Delete the staging directory of the abandoned plan, or choose
  a change name that no plan uses, then run the skill again.
- After 5 `openspec_stage` tries that return `valid: false`, the skill
  shows the OpenSpec CLI output and stops with no plan handoff.
- On a resume at or after Step 6.5, the skill stages the files of the
  change again from the staging folder of the main worktree. Only when
  that folder is missing does it use the staging folder of the active
  worktree (an older plugin staged there). When the skill cannot find the
  main worktree, it uses only the folder of the active worktree and
  prints a notice. If both folders are missing, the skill prints
  `Staging folder of <name> not found under <MAIN_ROOT> or <ACTIVE_ROOT>.
  The change is authored again from the plan.` In this message, `<name>`
  is the change name, `<MAIN_ROOT>` is the path of the main worktree and
  `<ACTIVE_ROOT>` is the path of the active worktree. The skill fills in
  the real values. It then authors the change again from the plan and
  stages it, with no step for you to do.
- On that resume, a `valid: false` result follows the 5-try rule above,
  and the resume call counts as the first try. Any other error stops the
  skill with no plan handoff. Fix the cause that the error names, then
  run the skill again.

## Checks and reviews

- **Step 3 runs 5 review lanes and 2 gate checks.** The lanes are
  `static-structural`, `content-coverage`, `guardrail-compliance`,
  `dimension-coverage` (G17) and `style-compliance`. There is no
  file-existence lane: the gate checks cover its gates. The gate checks start
  no agent:
  - `go-gates` calls `validate({action: "plan_gates"})` in the same message
    as the lanes. It checks G3, G4, G8, G9, G10 and G12 by count and lookup,
    plus the G18 floor (a Contract with no code block, table or diagram).
    G8 and G9 are warnings; the other gates are errors.
  - `inline-checks` is the main session's own check of G5, G6, G13 and G15,
    before the lane dispatch.
- **The validate actions refuse a large plan file.** `plan_format`,
  `plan_style` and `plan_gates` refuse a plan file over 8 MiB with an error
  that names the size and the limit.

The style checks (PF13, PF14 and the G22 lane) are in
[Plan writing style](./writing-style.md#how-the-check-works).

### Step 5 review loop

- The loop runs up to **5 rounds** (`plan_prepare`'s `reviewLoop.maxRounds`).
  If round 5 finds blocking issues, the skill runs its fix pass, records the
  round, and then asks one question for each open finding: accepted,
  rejected, or stop. It stores the answers with `plan_mark` `review-outcome`
  and does not start a 6th round.
- A plan with open findings is handed off only when no answer is stop. An
  accepted finding gets a row in `## Deviations & assumptions`. A stop
  answer ends the run with no hand-off. On a stop answer, the skill offers
  harden in interactive mode.
- With no open finding, the skill asks nothing and continues. With more than
  200 open findings, or when `AskUserQuestion` is unavailable, it asks
  nothing and stops with no hand-off.
- `--auto` does not skip these questions; only the harden offer is left out.
- Two calls write the `stopped` marker: the `review-outcome` call with a
  `stop` answer (same write), and the `plan_mark` `stopped` call of the
  conclude stop rule. A repeat keeps the first stop time.
- Each review round is recorded with `plan_mark` `review-round` for the
  dashboard. A failed record call does not stop the skill. Each record also
  lists the blocking findings of the round as `{id, fixed}`.
- A plan with fewer than 8 tasks uses one reviewer, on the model that did
  not write the plan. The skill still sends the output of that reviewer
  through `merge_results` as one lens named `all`, so the record has the
  merged status and the blocking count.
- The dimension-coverage lane (G17) runs in round 1 only.
- Step 3 stores the `G14` and `G22` issues, without the synthetic lane
  issues, as the evidence item `S3-guardrail-findings`. The round 1 record
  adds them with `fixed: true`.

## Timing report

Step 7 prints a timing report, then one line for each warning. All times
are wall clock, UTC. Example:

```
Step 7: Timing (wall clock, UTC)
  Total: 42m (2026-05-09T13:30:00Z to 2026-05-09T14:12:00Z)
  Steps: 0 2m · 1 9m · 2 6m · 3 r1 7m · 4 r1 5m · 5 r1 8m · 6 r1 4m · 7 1m
  Slowest agents: lane-content-coverage-r1 6m 12s · explore-api 4m 3s · lens-risk-r1 3m 40s
```

- **Total** runs from the skill start (`planIntegrity.skillInvoked`) to the
  `done` mark, in minutes. It includes the time you spend answering
  questions. It is not the plan timing in the run history, which ends at
  the last edit of the plan file.
- **Steps** gives each step from its first checkpoint to the first
  checkpoint of the next step, in minutes. Step 0 runs from the skill start
  to its checkpoint. The last step ends at the `done` mark. A label
  `<step> r<n>` is round `n` of that step.
- **Slowest agents** gives the 3 longest agents, from the first to the last
  evidence write of each agent, in minutes and seconds. It leaves out the
  orchestrator (`main`), an agent that still runs, and an agent with no
  start time. The gate checks are not agents.
- Data that is absent or bad prints `unknown`. A run that started before
  this version has no step times and no agent start times, so those lines
  print `unknown` or `none`.
- The plan state keeps at most 64 step times: one for the first checkpoint
  of each step and round. When the list holds 64 entries, the report adds
  the line "Steps: first 64 checkpoints only".

## Handoff report

Before it offers the next skill, Step 7 also prints these lines:

- **Scorecard pointer**, only when the plan file has a
  `## Verification Scorecard` section (Step 5 ran):
  `Verification Scorecard: <verdict line> — see ## Verification Scorecard in the plan for details.`
- **Dispatch report**, two lines from the `dispatch-status` evidence item.
  The lane names come from the last lane merge:

  ```
  Step 7: Lane failures: <lanes or none>. Lanes not returned: <lanes or none>. Lanes skipped: <lanes or none>.
  Step 7: Done inline (agent not returned): <agents or none>. Agents not stopped: <agents or none>.
  ```

  When the item cannot be read, or its body is not a JSON object, each value
  prints `unknown`.
- **Style report**: a table of the plan sections (words, prose, long
  sentences, jargon, status), then the banned phrases, the STE hits, the
  diagram contrast hits and the warnings.
- **Instruction self-check table**, only when `local.toml` holds style
  instructions. Each row names one instruction and the evidence in the plan
  file. The skill fixes each "no" row before it continues.
