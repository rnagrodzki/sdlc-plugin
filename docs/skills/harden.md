# /harden

Analyze a pipeline failure and propose user-approved edits to the project's
hardening surfaces — plan/execute guardrails, review dimensions, or Copilot
instructions — so the same class of failure is caught earlier next time.
Strengthen-only: it never relaxes or removes an existing rule.

## When to use

- A pipeline step (`/plan`, `/execute`, `/review`, ...) just failed and you
  want to prevent the same failure from recurring.
- You want to turn a one-off mistake into a durable guardrail or review
  dimension instead of just fixing the immediate symptom.
- Another skill's failure-handling menu offered a "harden" option and you
  picked it.

## Syntax

    /harden --failure-text <text> --skill <name> [options]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--failure-text <string>` | The failure to analyze, as free text. Required unless `--from-issue` is used; mutually exclusive with it. | — |
| `--from-issue <num>` | Analyze a GitHub issue instead of inline text. Mutually exclusive with `--failure-text`. | — |
| `--skill <name>` | Name of the skill that failed. Always required. | — |
| `--step <s>` | Step name within the failing skill, if known. | — |
| `--operation <op>` | Operation being performed when the failure occurred. | — |
| `--exit-code <code>` | Exit code or HTTP status from the failure, if any. | — |
| `--error-type <type>` | Category of error, if known. | — |
| `--user-intent <text>` | What the user was trying to accomplish. | — |
| `--args-string <text>` | The invocation arguments that led to the failure. | — |
| `--auto` | Accept every proposal without asking, and list each one under "Auto-accepted" in the output. Two callers pass it: [`/received-review --auto`](received-review.md), when its Step 11.6 dispatches `/harden` (subagent dispatch, where `AskUserQuestion` is not available), and [`/ship --auto`](ship.md)'s own `harden` step, which forwards the flag because the run is unattended. Does not change what may be proposed: still strengthen-only, and each edit is still validated and reverted on failure. Custom instructions still apply (see [Custom instructions](#custom-instructions)). Never files an `error-report` issue. Not valid with `--from-learnings`. | off |

## Custom instructions

`/harden` can follow project guidance that you write in the
`[harden.instructions]` table of `.sdlc-v2/config.toml`. Each list holds
plain-text instructions for one proposal surface. The orchestrator reads them
from the manifest when it drafts proposals. Edit this table by hand.
[`/setup`](setup.md#config-templates) does not ask about it.

```toml
[harden.instructions]
plan-guardrails = ["Prefer error severity for CI rules."]
```

| Key | Proposals it shapes | Default | Limit |
|-----|---------------------|---------|-------|
| `plan-guardrails` | Plan guardrails | `[]` | 10 items, 1024 characters each |
| `execute-guardrails` | Execute guardrails | `[]` | 10 items, 1024 characters each |
| `review-dimensions` | Review dimensions | `[]` | 10 items, 1024 characters each |
| `copilot-instructions` | Copilot instructions | `[]` | 10 items, 1024 characters each |

The four lists are independent. A missing table, a missing key and an empty
list all mean "no instructions". The limits apply to each list. `/harden`
counts the characters of an item before it trims the item, and it ignores an
item that is blank after the trim. The config schema requires at least 1
character in each item.

**To change the instructions:** edit the lists in `.sdlc-v2/config.toml`, save
the file, then run `/harden` again. `/harden` reads the table at the start of
each run. The config template at `plugins/sdlc/templates/config.toml` shows
the same four keys.

**What `/harden` prints:** after it loads the failure, `/harden` prints each
list, one line for each surface, or `Custom harden instructions: none
configured.` when all four lists are empty. In `--from-learnings` mode it
prints the block once, after the first entry that loads.

**Rules that custom instructions never change:**

- Strengthen-only. An instruction cannot make `/harden` relax or remove a rule.
- Approval prompts. An instruction cannot add or skip an approval prompt.
- `--auto`. An instruction cannot turn `--auto` on or off. In an `--auto` run
  the instructions still apply, and the summary shows
  `Custom instructions: <N> configured (config.toml [harden.instructions])`,
  where `<N>` is the total number of items in the four lists.
- Target files. Each proposal is still limited to the files of its surface.

**An invalid table stops the run.** An unknown key, a value that is not a list,
more than 10 items, an item that is not a string and an item over 1024
characters each give an error that names the key and a suggested fix. `/harden`
does not offer `error-report` for this error, because the cause is the config
and not the plugin. Fix the file and run `/harden` again. A `config.toml` that
`/harden` cannot read or parse also stops the run. In `--from-learnings` mode
the first such error stops the whole triage run, and `/harden` removes no
learnings entry.

## Examples

**Analyze a failure from `/plan`:**

    /harden --failure-text "Step 5 reviewer loop exceeded max retries without converging" --skill plan --step "Step 5" --operation "reviewer-loop"

Loads the failure context, classifies it as user-code, plugin-defect, or
ambiguous, and — for user-code or ambiguous failures — proposes guardrail or
review-dimension edits you can apply, skip, or cancel one at a time.

**Analyze a failure already filed as a GitHub issue:**

    /harden --from-issue 42 --skill execute

Fetches the issue body and (if labeled `mcp-failure`) treats it as a
pre-classified plugin defect, skipping straight to the `error-report` route.

## Structured output

`/harden`'s preparation step returns four fields: `manifestPath`, `mode`,
`customInstructions` (the `[harden.instructions]` lists, one list for each
proposal surface) and `next` (the step after the call). The orchestrator
reads the full manifest at `manifestPath` to build proposals. The manifest
holds the failure, the surfaces and the same `customInstructions` map.

## Learnings stats

`learnings_log` has a `"stats"` action that summarizes
`.sdlc-v2/learnings/log.md` instead of returning it verbatim: total entry
count, counts by category (inferred from each entry's branch tag, e.g.
`feat`/`fix`) and by skill, the most-repeated recurring `Rule: ...` lessons
(`topPatterns`, capped at 10, sorted by count then recency), and how many of
the most recent 20 entries were failure-tagged (`recentFailures`). It never
errors on a missing or empty log — every count comes back zero instead.

## Skill recommendations

`/harden` mines `learnings_log`'s stats for recurring lesson patterns and
surfaces each one that has recurred at least 3 times as a candidate
skill/guardrail recommendation, with a coarse advisory priority: `high` (seen
6+ times), `medium` (4-5 times), or `low` (3 times). These are advisory data
only — as of this writing, the harden orchestrator prompt still loops over
the four pre-existing surfaces (plan guardrails, execute guardrails, review
dimensions, Copilot instructions) and does not yet read or act on
`skillRecommendations`, so treat this surface as informational until that
consumption gap is closed.

## Related skills

- [/error-report](../../plugins/sdlc/skills/error-report/SKILL.md) — Where
  `/harden` routes failures it classifies as plugin defects.
- [/setup](setup.md) — Author the initial guardrails and review dimensions
  that `/harden` later strengthens.
- [/review](review.md) — Review dimensions are one of the surfaces `/harden`
  can add to or strengthen.
- [/ship](ship.md) — Its opt-in `harden` step invokes `/harden` once per
  cluster of review findings.
- [/received-review](received-review.md) — Its Step 11.6 invokes `/harden`
  for clusters of review findings when `/ship`'s `harden` step is not
  configured.
- [/plan](plan.md) and [/execute](execute.md) — Their guardrails are two of
  the surfaces `/harden` can strengthen.

## Tips and gotchas

- **Load State is mandatory.** `/harden` always starts by calling
  `prepare_orchestrator` (mode `harden`) to load the failure context and surface manifest. This
  must happen before any other tool call (unless invoked with
  `--from-learnings`, which first reads the log with `learnings_log` and then
  calls `prepare_orchestrator` once per entry, config-version check included).
  The same call loads the [custom instructions](#custom-instructions), and an
  invalid `[harden.instructions]` table stops the run here.
- **Strengthen-only.** `/harden` never proposes relaxing or removing an
  existing rule — every proposal adds or tightens a guardrail, dimension, or
  instruction. Custom instructions do not change this.
- **Guardrail proposals are checked before they are written.** A
  `plan`/`execute` guardrail proposal is validated in memory against the
  current config first; if the check finds a problem (e.g. a description over
  1024 bytes), `/harden` repairs it automatically — shortening the text, or
  splitting it into independent guardrails like `<id>-1`, `<id>-2` — and
  checks again, up to 2 repair rounds. Only a clean check is written to
  `config.toml`, and the write is validated once more on disk as a final
  safety net. One finding gets a different repair: when a proposal lowers the
  severity of a guardrail that is already on disk (`severity lowered from
  error to warning`), `/harden` sets the old severity back once and checks
  again. If that finding remains, `/harden` skips the proposal with no prompt,
  with or without `--auto`. Review dimensions and Copilot instructions still use the
  original write-first-then-revert-on-failure flow, since they have no
  equivalent in-memory check.
- **Nothing is written without approval.** Each proposal is presented
  individually with a patch preview; you choose apply, skip, or cancel per
  proposal. Cancelling stops the whole run without applying later proposals.
  The one exception is `--auto`: the caller passed it on purpose, so every
  proposal is applied without a prompt and listed under "Auto-accepted". See
  the `/ship` note below for the callers that do this unattended.
- **`--failure-text` and `--from-issue` are mutually exclusive.** Provide
  exactly one, not both.
- **`/ship` calls `/harden` in two ways.** When `harden` is in
  `ship.steps`, `/ship`'s own `harden` step (after `archive-openspec`, before `pr`)
  clusters the review findings, invokes `/harden` once per cluster (capped at
  5), and commits the edits as a separate commit. It passes `--auto` when
  `/ship` runs with `--auto`; otherwise it asks once per cluster, and
  `/harden` asks per proposal. With the step configured, `/ship` passes
  `--no-harden` to [`/received-review`](received-review.md), so hardening runs
  once. Without the step, `/ship --auto` still reaches `/harden` through
  `/received-review`'s Step 11.6, which dispatches `harden --auto` for each
  cluster. Either unattended path edits your project's guardrails and review
  dimensions with no confirmation prompt. Each edit is still strengthen-only,
  still schema-validated, still reverted if validation fails, and listed under
  "Auto-accepted" in the output — but the point stands: an unattended
  `/ship --auto` can change your project's guardrails.
- **New review dimensions get a Copilot mirror automatically.** When a
  proposal adds a brand-new review dimension, `/harden` generates the
  matching Copilot instructions file for you — existing dimensions are not
  retroactively mirrored.
