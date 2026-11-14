# /sdlc:plan

Turn a spec, a topic file or a plain description into a reviewed plan of ordered tasks that `/sdlc:execute` can run.

## Why use it

A large change that starts with no plan misses steps, and its risks show late.
`/sdlc:plan` explores the codebase, splits the work into ordered tasks and checks the result before you approve it.
You get one plan file. `/sdlc:execute` or `/sdlc:ship` runs that file.

## Example

```
/sdlc:plan docs/requirements/auth-redesign.md
```

The skill reads the spec file, asks its discovery questions, explores the codebase and writes a plan file.

- Each task has a complexity, a risk, its dependencies, its files and its acceptance criteria.
- The review lanes, the link check and the format check ran on the plan.
- The last message gives the plan path and offers to run `/sdlc:ship` or `/sdlc:execute` with it.

A small change (2-3 files) gets a lightweight plan: no exploration and no review loop.

## What you get

- **Tasks an agent can run:** each task names its files, complexity, risk, dependencies and acceptance criteria.
- **Problems found before any code:** review lanes, gate checks, a review loop and two hard gates (links, format) check the plan.
- **Any starting point:** plan from a description, a spec file, preplan topic files or an OpenSpec change.
- **A plan written for your reader:** set the audience, the writing standard, the tone and the visual density once.
- **No lost run after a compaction:** the skill reloads its saved checkpoint and continues at the saved step.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0: Mode and setup | skill | Detects plan mode, gathers the requirements, runs the OpenSpec gate check, routes the plan by scope and writes the plan file skeleton. | The plan file skeleton and the routing | no | always |
| Step 1: Requirements and exploration | agent | Asks the discovery questions and explores the codebase with background explorers. | The recorded requirements and findings | no | always. A lightweight plan (2-3 files) skips the exploration |
| Step 2: Decompose into tasks | skill | Splits the requirements into tasks with complexity, risk and dependencies. | The task list in the plan file | no | always |
| Step 3: Critique the plan | agent | Checks the plan with the gate checks and the review lanes. | A list of issues | no | always |
| Step 4: Improve and approve | skill | Fixes the issues of Step 3 and shows the plan to the user. | The revised plan | no | always |
| Step 5: Review loop | agent | Reviews the plan with several lenses in rounds until it has no blocking issue. | The review verdict and the Verification Scorecard | yes | full pipeline only. A lightweight plan skips it |
| Step 6: Apply review fixes | skill | Fixes each blocking issue of the review. In the OpenSpec Create flow it also authors the change files before Step 6.5. | The fixed plan | no | the review found blocking issues, or the plan uses the OpenSpec Create flow |
| Step 6.5: Link verification | tool | Checks every URL in the plan with `links_validate`. This is a hard gate. | The link check result | no | always |
| Step 6.6: Format validation | tool | Checks the plan structure with `validate`. This is a hard gate. | The format check result | no | always |
| Step 7: Handoff | skill | Marks the run done, prints the timing and the style report, and offers to run `/ship` or `/execute`. | The plan file path | no | always |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/plan`, from a spec file, `--spec`, or a description of the requirements. |
| Ends | After Step 7. The plan file is final and the skill offers to run `/ship` or `/execute` with it. |
| Asks the user | When no requirements are given, at the OpenSpec gate check, at a scope split, when the plan is shown, and at the end. With `--auto`, a `CRITICAL` Gate A verdict still blocks, and the per-finding questions at the review-loop limit still ask. |
| Resume | No resume flag. After a context compaction the skill reloads the run from its saved checkpoint and continues at the saved step. |

## More examples

**Describe the change in the chat**

```
/sdlc:plan
```

The skill asks what you want to implement. It plans from your answer.

**Plan from an OpenSpec change**

```
/sdlc:plan --spec user-onboarding
```

The skill loads the proposal, the delta specs and the task list from `openspec/changes/user-onboarding/` and builds the plan around them.

**Plan several preplan topics in one run**

```
/sdlc:plan .sdlc-v2/preplan/2026-10-12-auth-flow.md .sdlc-v2/preplan/2026-10-12-session-store.md
```

The skill reads each topic file in argument order and numbers the requirements across all files.
When two files contradict each other, it asks one question for each conflict.

## Reference

- [Flags and gotchas](./plan/flags-and-gotchas.md): every flag, the scope routing, the OpenSpec rules and the Step 7 timing report.
- [Preplan topic file](./plan/topic-file.md): how the plan reads each section of a topic file.
- [Conclude, stop and recover a run](./plan/conclude-and-recovery.md): the conclude question, the stop rule and the recovery after a compaction.
- [Plan writing style](./plan/writing-style.md): the style settings, the STE rules, the limits and the custom plan instructions.

## Related skills

- [/sdlc:preplan](preplan.md): shape an idea into a topic file before the plan run.
- [/sdlc:verify-requirements](verify-requirements.md): check the requirements before a plan run.
- [/sdlc:execute](execute.md): take the plan file and implement it wave by wave.
- [/sdlc:ship](ship.md): run the plan file through the full pipeline, with `/sdlc:execute` as the first step.
- [/sdlc:openspec-save](openspec-save.md): save the staged OpenSpec change as its own branch, commit and PR before you ship the code.
