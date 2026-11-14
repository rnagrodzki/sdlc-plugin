# /sdlc:harden

Turn a pipeline failure into a rule that catches the same class of failure earlier in the next run.

## Why use it

A pipeline step fails. You fix the symptom, and the same mistake comes back in a later run.
`/sdlc:harden` reads the failure and proposes edits to your plan guardrails, execute guardrails, review dimensions or Copilot instructions.
You approve each edit, and the next run catches that class of failure earlier.

## Example

A `/sdlc:plan` run failed in its reviewer loop. Give the failure text and the name of the skill:

```text
/sdlc:harden --failure-text "Step 5 reviewer loop exceeded max retries without converging" --skill plan --step "Step 5" --operation "reviewer-loop"
```

`/sdlc:harden` classifies the failure as `user-code`, `plugin-defect` or `ambiguous`.
For a `user-code` or `ambiguous` failure, it shows each proposal with its target file, its rationale and a preview of the patch.
You answer **apply**, **skip** or **cancel** for each proposal. `/sdlc:harden` checks each edit before and after the write, and reverts a bad edit.
For a `plugin-defect` failure, it offers to file an issue with [`/sdlc:error-report`](error-report.md).

## What you get

- **Stop a failure from coming back.** Each new rule names a class of failure, not one file or one tool.
- **Keep control of your rules.** You see a preview of each edit, and you apply, skip or cancel it. Only `--auto` skips the prompt.
- **Rules only get stricter.** No rule is relaxed, and a rule is removed in three cases only: a dead reference, a covering rule, or the split of an oversize rule.
- **Lessons become rules.** `--from-learnings` triages the learnings of earlier runs and proposes guardrail edits. A lesson that recurs 3 times becomes a skill recommendation with a priority.
- **A record of each run.** In a `/sdlc:ship` run, each harden run keeps one record of its hardening in the ship state, and the dashboard shows it.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0: Parse arguments | skill | Reads the flags and checks them. It stops with an error when two inputs are given, when no input is given, or when `--auto` is used with `--from-learnings`. Gets the harden list of the `[instructions]` config, except with `--no-instructions` or `--from-learnings`. A config error stops the run before Step 1. | The failure input and the flags | no | always |
| Step 1: Load state with `prepare_orchestrator` | tool | Calls `prepare_orchestrator` in mode `harden`. It loads the failure, the hardening surfaces and the pre-flight findings into a manifest, and prints the custom instructions. | The manifest path | no | no `--from-learnings` |
| Step 1 (alternative): `--from-learnings` mode | skill | Reads the learnings log, skips the entries that harden wrote, and sends each other entry through the orchestrator. It groups the results by classification and removes the addressed entries. | The grouped results for Step 5 | yes | `--from-learnings` is given. It replaces Steps 1 to 4 |
| Step 2: Classify | skill | Prints a short preview of the failure and the classification hint. A `plugin-defect` hint from an issue with the `mcp-failure` label builds the result without the orchestrator. | The failure preview and the hint | no | no `--from-learnings` |
| Step 3: Analyze | agent | Dispatches the `harden-orchestrator` agent. The agent returns the classification and the list of proposals as one JSON object. | The classification and the proposals | no | no `--from-learnings`, and the hint is not `plugin-defect` |
| Step 4: Branch on classification | skill | Prints the classification and the proposals that were not made. It routes to Step 5, to Step 6, or to the report that no proposal is useful. | The route | no | no `--from-learnings` |
| Step 5: Present and apply | skill | Shows each proposal, asks for approval, and checks the edit before and after the write. It reverts a bad edit. With `--auto` it applies only the safe actions and stores the rest as pending deferred items. | The edits to the hardening surfaces, and the ship state record in a ship run | no | the route has at least one proposal |
| Step 6: Plugin-defect route | skill | Shows the error report payload and asks whether to run `error-report`. With `--auto` it only shows the payload. | The filed issue, or the shown payload | no | the classification is `plugin-defect` |
| Step 7: Learning capture | tool | Appends one entry to the learnings log with the classification, the counts and the route. | The learnings entry | no | always. A halt or a cancel skips it |
| Step 8: Run instructions | skill | Runs each instruction of the list, then prints `Instructions (harden): …`. | The summary line | yes | no stop, no plugin-defect route, not `--from-learnings`, and the list is not empty |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:harden` and one input: `--failure-text`, `--from-issue` or `--from-learnings`. Other skills start it after a failure. |
| Ends | After Step 8 runs the instruction list, or after Step 7 writes the learnings entry when no list runs. It ends earlier on an argument error, on a config error, on a bad orchestrator result, on a halt, or when the user cancels. |
| Asks the user | At each proposal in Step 5, at each validation failure, at the ambiguous upstream-report offer, and at the `error-report` offer in Step 6. With `--auto` it skips these prompts. An instruction asks only when its question flags allow it. |
| Resume | none |

## More examples

**Analyze a failure that is already a GitHub issue:**

```text
/sdlc:harden --from-issue 42 --skill execute
```

`/sdlc:harden` reads the issue body. An issue with the `mcp-failure` label is a plugin defect, so the run goes directly to the `error-report` offer.

**Triage the lessons of earlier runs:**

```text
/sdlc:harden --from-learnings
```

`/sdlc:harden` reads the learnings log and makes one pass for each entry that it did not write. You approve each proposal, and an entry with an applied proposal is removed from the log.

**Replay the rule edits that an `--auto` run left pending:**

```text
/sdlc:harden --failure-text "Pending rule edits: harden-pending-plan" --skill deferred
```

`/sdlc:harden` drafts each edit of the deferred item `harden-pending-plan` again. Each edit goes through the normal approval prompt. [`/sdlc:deferred`](deferred.md) prints this command for you.

## Reference

- [Flags and rule edits](./harden/auto-mode-and-edits.md): each flag, what `--auto` applies and what it holds back, what harden can remove, how each edit is checked, and the follow-up instructions.
- [Custom instructions](./harden/custom-instructions.md): project guidance for the proposals, in `.sdlc-v2/config.toml`.
- [Follow-up instructions](../instructions.md): the `[instructions]` config, the merge rule, the question rules and the summary line.
- [Learnings](./harden/learnings.md): the learnings triage, the learnings stats and the skill recommendations.
- [Ship state record](./harden/ship-state-record.md): the record that a `/sdlc:ship` run keeps of each harden run.

## Related skills

- [/sdlc:ship](ship.md): its optional `harden` step runs `/sdlc:harden` one time for each cluster of review findings.
- [/sdlc:received-review](received-review.md): its Step 11.6 runs `/sdlc:harden` for clusters of review findings when the `harden` step of `/sdlc:ship` is not configured.
- [/sdlc:error-report](error-report.md): files the issue for a failure that `/sdlc:harden` classifies as a plugin defect.
- [/sdlc:deferred](deferred.md): lists the pending rule edits that `/sdlc:harden --auto` stores, and prints the command that replays them.
- [/sdlc:setup](setup.md): writes the first guardrails and review dimensions, which `/sdlc:harden` later strengthens.
- [/sdlc:review](review.md): its review dimensions are one of the surfaces that `/sdlc:harden` edits.
- [/sdlc:plan](plan.md): its guardrails are one of the surfaces that `/sdlc:harden` edits.
- [/sdlc:execute](execute.md): its guardrails are one of the surfaces that `/sdlc:harden` edits.
