# /plan

Create an implementation plan from requirements, a spec, a design document,
or a plain description.

## When to use

- You have a feature, bug fix, or refactoring task and want a structured plan
  before writing code.
- You want to break a large task into smaller, dependency-ordered pieces that
  `/execute` can run.
- You are in **plan mode** (Claude Code's built-in planning mode) — `/plan` is
  the designated skill for that mode.
- You have an OpenSpec change and want to plan implementation from its specs.

## Syntax

    /plan [options] [spec-file-path]

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `[spec-file-path]` | Path to a requirements or spec file to plan from. A path into `openspec/changes/<name>/` selects that change, same as `--spec <name>`. | none |
| `--auto` | Skip interactive prompts. Picks conservative defaults. Two prompts stay: a Gate A `CRITICAL` verdict still blocks, and the per-finding questions at the review-loop limit are still asked (only the harden offer is left out). | off |
| `--spec [<change-name>]` | Opts into OpenSpec and skips the gate-check question. With a name: plan from that existing change (`openspec/changes/<change-name>/`). Without one: use the branch-matched change, else ask which active change to use, or offer to create a new one. | off |
| `--from-openspec <name>` | Deprecated alias of `--spec <name>`, kept for one release. Prints a deprecation notice. | none |

## Preplan topic file

A topic file from [`/preplan`](preplan.md) is a requirements file. The plan
reads its sections this way:

| Topic section | Plan section |
|---|---|
| Goal, Users and effect, Flows | requirements context |
| Problem and benefit, Risk if wrong | Context |
| Non-goals, `Smallest cut:` line | Key Decisions |
| Decisions rows | requirements |

If `Problem and benefit`, `Risk if wrong` or `Non-goals` is empty or absent
(an older topic file has none of them), the plan asks one discovery question
for each such section, except with `--auto`. With `--auto`, the plan asks no
question. It takes the narrowest reading, records it in Context or Key
Decisions, and adds a `Deviations & assumptions` row with `asked=no`. A
section that the topic file already fills answers its question, so the plan
does not ask it again. An empty `Goal`, `Users and effect` or `Flows` section
makes the requirements vague, so the plan asks its usual discovery questions.

The plan writes the smallest cut with the decision text of each picked row,
not the row number. With no `Smallest cut:` line, the smallest cut is all
proposals that are not dropped or moved.

- If the plan template has no `Context` or `Key Decisions` section, the plan
  records the same content as requirement items.
- A `Decisions` row whose decision starts with `Dropped:` or `Moved:` is not a
  requirement. The `Smallest cut:` line is not a requirement either.
- A scope option under `Non-goals` that starts with `Offered, not picked:` is
  a non-goal, not a requirement. The line `None stated.` gives the Key
  Decisions bullet "Non-goals: none stated" and no discovery question.

## Examples

**Plan from a description you type interactively:**

    /plan

The skill asks you to describe what you want to implement. It explores the
codebase, decomposes the work into tasks, and writes a plan file.

**Plan from a spec file:**

    /plan docs/requirements/auth-redesign.md

Reads the spec file and uses it as input for planning.

**Plan from an OpenSpec change:**

    /plan --spec user-onboarding

Loads the proposal, delta specs, and task list from
`openspec/changes/user-onboarding/` and builds a plan around them.
(`--from-openspec user-onboarding` still works but is deprecated — use
`--spec user-onboarding` instead.)

## Related skills

- [/execute](execute.md) — Takes the plan file this skill produces and
  implements it wave by wave.
- [/ship](ship.md) — Runs `/execute` as its first step, so it consumes plan
  files too.
- [/openspec-save](openspec-save.md) — Saves the staged OpenSpec change as
  its own branch, commit, and PR before you ship the code.

## Tips and gotchas

- **Plan files are saved to disk.** The plan is written to a file. You pass
  that file path to `/execute` or `/ship` later — they do not pick it up
  automatically.
- **The hand-off menu can offer `openspec-save`.** When the plan file header
  has an `**OpenSpec-Staging:**` line, the menu also lists `openspec-save`
  (`/sdlc:openspec-save --plan <path>`). It saves the OpenSpec change as its own
  PR before you ship the code. Without that header line, the menu has no such
  entry.
- **`plan_prepare` always runs first, but it only resumes state when asked.**
  The skill's first action is always a `plan_prepare(...)` call. By default it
  starts a fresh run. Pass `resume: true` and the `runId` to reuse that run instead —
  the skill does this automatically after a compaction (see
  [Recovery after compaction](#recovery-after-compaction) below).
- **Complexity routing matters.** If your change touches only 1 file, the skill
  tells you no plan is needed (or writes a lightweight plan in plan mode). Full
  planning with multi-agent exploration kicks in for 4+ files or unclear scope.
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
  Step 6, and authored again when you reject the plan with feedback.
- **Staging a created change can stop the skill.** The skill stages the
  authored files with `openspec_stage`. Four exits apply:
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
  - On a resume at or after Step 6.5, the skill reads the staging folder of
    the main worktree, then the folder of the active worktree (an older
    plugin staged there). If both folders are missing, the skill prints
    `Staging folder of <name> not found under <MAIN_ROOT> or <ACTIVE_ROOT>.
    Run /sdlc:plan again to author the change.` and stops with no plan
    handoff and no `openspec_stage` call. Run the skill again to author the
    change.
- **Plan mode vs. normal mode.** In plan mode, the skill writes to the
  designated plan file path. In normal mode, it creates a file and tells you
  where it is.
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
- Step 7 prints a timing report, then one line for each warning. All times
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
- The validate actions `plan_format`, `plan_style` and `plan_gates` refuse a
  plan file over 8 MiB with an error that names the size and the limit.

## Recovery after compaction

- What you see: `Active plan (post-compact): step <n>, branch <b>; plan file: <path>` in the session context, followed by a line that tells Claude to invoke the sdlc:plan skill first when the skill instructions are not in context, and a `Resume with:` line. The banner shows only for a run that was written in the last 24 hours (`state.PlanRunLiveWindow`).
- What the skill does: reloads the run with `plan_prepare` (resume mode, with the `runId` of the `Resume with:` line), reads the evidence digest, then resumes at the saved step; it does not clear the plan file.
- When the saved run is gone: if the resume call returns `no active plan run`, `plan run <runId> not found`, `plan run <runId> is done` or `plan run <runId> has no skillInvoked marker`, the skill prints "No active plan run to resume — starting a new plan.", ignores the `Active plan (post-compact):` line for the rest of the session, and starts a new plan.
- When the run disappears in the middle of a plan: if a call returns `plan run <runId> not found` (or a reuse `plan_prepare` call returns `is done` or `has no skillInvoked marker`), the skill tells you that the run state is gone. It stops the background agents that it still holds, names each agent that it could not stop, and starts a new plan with no `runId`.
- Where evidence lives: <main-worktree>/.sdlc-v2/runs/<runId>.evidence/. Ship's `cleanup-pipeline` step deletes the plan run and its evidence only when the ship run links a plan run and the ship report file exists on disk. Otherwise the GC sweep removes them after the TTL. The GC sweep keeps the newest plan state file of a branch that still exists, even past the TTL; `execute_state` gc reaps the evidence directory after the TTL.
- A second plan session on the same branch keeps its own run and its own evidence. A new run of the branch deletes an older run only when that run is not done, was not written in the last 24 hours (`state.PlanRunLiveWindow`), and has no `sessionId` that differs from the writer's.

## Plan writing style

Every plan follows a configurable writing style: who it is written for,
which writing standard it follows, its tone, and how dense its layout is.
Configure it with `/setup --only communication-style,plan-style`.

| Key | Values | Default | What it changes |
|---|---|---|---|
| `audience` | `technical`, `functional`, `executive`, `general`, `beginner` | `functional` | How much jargon the prose may carry, and what the prose explains. |
| `writingStandard` | `ste`, `plain-language`, `developer-docs`, `smart-brevity` | `plain-language` | Sentence rules and limits. `ste` turns on the strict ASD-STE100 checks below. |
| `tone` | `direct`, `neutral` | `direct` | `direct` states problems plainly and flags hedging/praise phrases; `neutral` reports facts with no opinion either way. |
| `language` | plain text (e.g. `English`) | `English` | The sentence-length limit applies only when this is English. Any other language turns it off. |
| `technicalTerms` | list of words | `[]` | Words exempt from the STE word checks. Backticked inline code is always exempt too. |
| `visualDensity` | `high`, `balanced`, `low` | `balanced` | Prose-share, paragraph-length, and list-length ceilings. Plan-only. |
| `narrativeRules` | list of rules | `[]` | Extra rules appended to the writing guide verbatim. Plan-only. |
| `instructions` | list of instructions | `[]` | Project-specific plan instructions — see [Custom plan instructions](#custom-plan-instructions) below. Plan-only. |

- `audience`, `writingStandard`, `tone`, `language`, and `technicalTerms` live
  in `[style]` and apply to every sdlc skill, not only to plans.
- `visualDensity`, `narrativeRules`, and `instructions` live in `[planStyle]`
  and apply to plans only.
- A key under the wrong section still works, with a warning (see Migration
  below).

### Same content, different settings

**By writing standard** — the writing guide's own before/after pair for each
value:

| `writingStandard` | Before | After |
|---|---|---|
| `ste` | The validator, which is responsible for checking audience values, will reject values that are unknown. | The validator checks each audience value. It rejects a value that is not in the list. |
| `plain-language` | Utilize the configuration to facilitate seamless integration across numerous environments prior to deployment. | Use the setting. It helps the tool work the same way in each environment before you deploy it. |
| `developer-docs` | The function might sometimes return an error when the input is bad. | `ValidateAudience` returns `ErrInvalidAudience` when `audience` is not in the `Audiences` list. |
| `smart-brevity` | We wanted to let you know that, after a lot of discussion, the audience check will change in the next release to catch fewer good values as errors. | Why it matters: Fewer good values will fail the audience check. The old pattern was too strict. |

**By audience** — one fact, five readers. Content: "Task 3 adds a validator
that rejects unknown audience values."

| `audience` | Output |
|---|---|
| `technical` | `loadPlanStyle` rejects unknown `audience` values with a warning. |
| `functional` | The loader rejects an unknown reader level. The plan keeps the default level and shows a warning. |
| `executive` | Bad settings no longer produce bad plans silently. No cost. No decision needed. |
| `general` | If you type a reader type the tool does not know, it tells you and uses the normal one. |
| `beginner` | The tool knows 5 reader types. If you type another one, it says so. |

**By tone** — same content, direct vs. neutral. Content: "Task 3's validator
does not scan short list items."

| `tone` | Output |
|---|---|
| `direct` | Task 3's validator skips every list item under 30 words. That is a real gap: acceptance criteria are list items, so the STE rules never ran on them. |
| `neutral` | Task 3's validator does not scan a list item under 30 words. Acceptance criteria are list items, so the STE rules do not run on them. |

**By visual density** — same content, as prose and as a table. Content:
"Task 5 chose sqlite over postgres for the local cache: sqlite needs no
server process, postgres needs one running at all times, and the cache has
no concurrent writers across machines to support."

`visualDensity=low` (prose):

Task 5 chose sqlite over postgres for the local cache. Sqlite needs no
server process. Postgres needs one running at all times. The cache has no
concurrent writers across machines, so postgres adds cost with no benefit.

`visualDensity=high` (table):

| Option | Needs a server process | Fits a local, single-writer cache |
|---|---|---|
| sqlite | No | Yes |
| postgres | Yes | No |

### Writing standard: ste

`writingStandard = "ste"` turns on strict ASD-STE100 (Simplified Technical
English) checks. The rule text and the avoid list below are a plugin
approximation of ASD-STE100, not the ASD-STE100 dictionary, which is
copyrighted.

The 20 rules:

Words
1. Use simple, common words. One word, one meaning. No synonyms.
2. Use technical names (code, commands, paths, products) exactly, as nouns or adjectives.
3. Use noun clusters of 3 words or fewer. Split a longer cluster with "of", "for", or a relative clause.
4. Do not use the words in the avoid list below.

Verbs
5. Use only the infinitive, the imperative, the simple present, the simple past, and the simple future.
6. Do not use perfect tenses ("has done", "have been").
7. Do not use "-ing" verb forms.
8. Use the past participle only as an adjective, or with "is"/"are".
9. Use the active voice. Use the passive voice only in a description, when the doer is not known.
10. Do not use phrasal verbs ("set up", "carry out", "find out").

Sentences
11. Write one instruction in each sentence.
12. Write instructions in the imperative. Put a condition first: "If X, do Y."
13. Use 20 words or fewer in an instruction sentence; 25 words or fewer in a description sentence.
14. Keep the articles "the", "a", "an".
15. Use a pronoun only when it has one clear referent. Otherwise, repeat the noun.
16. Write a cause and its effect as two sentences: "X fails. The cause is Y."

Paragraphs and layout
17. Write one topic in each paragraph. Use 6 sentences or fewer.
18. Use a vertical list for 3 or more items, steps, or conditions.
19. Start a warning with the command, then give the reason.

Punctuation and style
20. Do not use contractions, semicolons, idioms, slang, or figurative words. Explain each uncommon technical term the first time you use it.

Which rules the Go check (PF13) catches by pattern, and which the G22 review
lane judges because they need meaning:

| Rule | Checked by |
|---|---|
| 1. One word, one meaning | G22 (needs judgment) |
| 2. Technical names exactly | `technicalTerms` exemption, not a pattern check |
| 3. Noun clusters ≤ 3 words | G22 (needs judgment) |
| 4. Avoid list | PF13: `avoid-word` |
| 5. Only 5 verb forms | G22 (needs judgment) |
| 6. No perfect tenses | PF13: `perfect-tense` |
| 7. No "-ing" verb forms | PF13: `ing-form` |
| 8. Past participle placement | PF13: `passive-instruction` (in an instruction). G22: a passive description with a known doer |
| 9. Active voice | PF13: `passive-instruction` (in an instruction). G22: a passive description with a known doer |
| 10. No phrasal verbs | PF13: `phrasal-verb` (a fixed list). G22: a phrasal verb that is not on the PF13 list |
| 11. One instruction per sentence | G22 (needs judgment) |
| 12. Condition first | G22 (needs judgment) |
| 13. Sentence length | PF13: `instruction-length` / `description-length` |
| 14. Keep articles | G22 (needs judgment) |
| 15. Clear pronoun referent | G22 (needs judgment) |
| 16. Cause and effect as two sentences | G22 (needs judgment) |
| 17. Paragraph length (6 sentences) | PF13: `paragraph-length` |
| 18. Vertical list for 3+ items | G22 (needs judgment) |
| 19. Warning: command first | G22 (needs judgment) |
| 20. No contractions / semicolons | PF13: `contraction` / `semicolon`. G22: idioms, slang, figurative words, and an uncommon term with no explanation at first use |

PF13 is a deterministic hard gate: a false hit would block a correct plan, so
it checks only what a pattern can decide safely. G22 (the
`style-compliance` review lane, Step 3) judges every rule that needs
meaning, scaled to `audience`, and reviews the pattern-checked rules too, as
a second pass.

**Avoid list** (rule 4; a plugin approximation, not the ASD-STE100
dictionary):

| Avoid | Use instead |
|---|---|
| utilize | use |
| leverage | use |
| ensure | make sure |
| facilitate | help |
| perform | do |
| commence | start |
| terminate | stop |
| obtain | get |
| sufficient | enough |
| numerous | many |
| prior to | before |
| subsequent | next |
| in order to | to |
| demonstrate | show |
| modify | change |
| eliminate | remove |
| initiate | start |
| regarding | about |
| approximately | about |
| additional | more |

**Exemptions:** a word or phrase listed in `technicalTerms` (matched
case-insensitive, whole word) is masked before any STE word check runs, so
it is never flagged. Inline code (backticks) is always exempt the same
way — it is replaced with a placeholder before the checks run, so it never
reaches them.

### Mermaid diagram colors

Every Mermaid diagram in a plan marks new and changed nodes with exactly
these two `classDef` lines, copied as-is:

    classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px
    classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px

Any other `classDef` or `style` line needs a `color:` and a text-to-fill
contrast of 4.5:1 or more. No pastel fills.

- Valid: `classDef done fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px` (contrast 5.4:1).
- Invalid: `classDef new fill:#d4f7d4,stroke:#2a7a2a` — fails with "no text color" (no `color:` key at all).

### How the check works

- `validate({action:"plan_format"})` runs **PF13** (style limits) and
  **PF14** (diagram colors, whole file) on every call, with every other
  plan-format check; `final:true` adds PF9 and PF10. A PF13 or PF14
  failure blocks the plan at handoff.
- The PostToolUse hook runs **PF14** on every plan-file edit, on the
  edited text only. It does not run PF13.
- `validate({action:"plan_style"})` runs PF13 alone and always returns a
  `styleReport` — pass or fail — with the settings in effect, the numeric
  limits, per-section numbers, every banned-phrase and STE hit, the diagram
  contrast hits, and the custom instructions.
- At the end of Step 2, the skill runs a format pre-check: it calls
  `validate({action:"plan_format", final:false})`, fixes each finding, and
  calls it again, at most 3 calls. A finding that is left waits for the
  Step 6.6 format gate.
- The STE and banned-phrase scans cover the whole plan file. The density and
  readability limits cover only the measured `##` sections (the narrative
  sections of the plan template, or Context / Research Findings / Key
  Decisions / Final Shape with no template).
- The **G22** review lane judges the rules PF13 cannot check by pattern (see
  the table above).
- The Step 5 review loop runs up to **5 rounds** (`plan_prepare`'s
  `reviewLoop.maxRounds`). If round 5 finds blocking issues, the skill runs
  its fix pass, records the round, and then asks one question for each open
  finding: accepted, rejected, or stop. It stores the answers with
  `plan_mark` `review-outcome` and does not start a 6th round.
  Each review round is recorded with `plan_mark` `review-round` for the
  dashboard. A failed record call does not stop the skill. A plan with fewer than 8 tasks uses one reviewer, on the model that did not write the plan. The skill still sends the output of that
  reviewer through `merge_results` as one lens named `all`, so the record has
  the merged status and the blocking count. The dimension-coverage lane (G17)
  runs in round 1 only.
  Each record also lists the blocking findings of the round as `{id, fixed}`.
  Step 3 stores the `G14` and `G22` issues, without the synthetic lane issues, as the
  evidence item `S3-guardrail-findings`. The round 1 record adds them with
  `fixed: true`. A plan with open findings is handed off only when no answer is
  stop. An accepted finding gets a row in `## Deviations & assumptions`. A
  stop answer ends the run with no hand-off. With no open finding, the skill
  asks nothing and continues. With more than 200 open findings, or when
  `AskUserQuestion` is unavailable, it asks nothing and stops with no
  hand-off. On a stop answer, it offers harden in interactive mode.
  `--auto` does not skip these questions; only the harden offer is left out.

Sample `styleReport` (abbreviated; writingStandard=ste, visualDensity=high):

    {
      "settings": {
        "audience": "functional",
        "writingStandard": "ste",
        "tone": "direct",
        "visualDensity": "high",
        "language": "English"
      },
      "limits": {
        "maxProseShare": 0.30,
        "maxParagraphSentences": 3,
        "maxListItems": 7,
        "maxSentenceWords": 25,
        "maxInstructionWords": 20,
        "maxJargonShare": 0.15,
        "ste": true,
        "bannedPhrases": [],
        "technicalTerms": []
      },
      "sections": [
        { "name": "Context", "status": "fail", "failures": ["prose share 0.42 > 0.30"] }
      ],
      "bannedPhraseHits": [{ "phrase": "great question", "line": 12 }],
      "steHits": [{ "rule": "ing-form", "text": "starting", "line": 18 }],
      "diagramContrast": [],
      "instructions": ["always add before -> after for the changed flows"],
      "warnings": []
    }

### Limits

The 4 style enums combine into one numeric `Limits`:

| Source | Sets |
|---|---|
| `visualDensity` | `maxProseShare`, `maxParagraphSentences`, `maxListItems` |
| `writingStandard` | `maxSentenceWords`, `maxInstructionWords`, `ste` |
| `audience` | `maxJargonShare` |
| `tone` | `bannedPhrases` (populated only when `tone` is not `neutral`) |

| `visualDensity` | Max prose share | Max paragraph sentences | Max list items |
|---|---|---|---|
| `high` | 0.30 | 3 | 7 |
| `balanced` | 0.50 | 5 | 9 |
| `low` | 0.75 | 6 | 12 |

(with `ste` on, the paragraph-sentence ceiling is capped at 6 regardless of
density.)

| `writingStandard` | Max sentence words | Max instruction words | STE checks |
|---|---|---|---|
| `ste` | 25 | 20 | on |
| `plain-language` | 25 | off | off |
| `developer-docs` | 30 | off | off |
| `smart-brevity` | 20 | off | off |

| `audience` | Max jargon share |
|---|---|
| `technical` | off |
| `functional` | 0.15 |
| `executive` | 0.05 |
| `general` | 0.00 |
| `beginner` | 0.00 |

A measured section under 40 words is skipped (too little text to judge). A
sentence-length check only fails a section when more than 10% of its
sentences are over the limit — one long sentence does not fail a section.
The sentence-length limit turns off entirely when `language` is not English.

### Migration from the old settings

- `planStyle.verbosity` is no longer read. Set `visualDensity` instead
  (`high` | `balanced` | `low`); a configured `verbosity` key now only
  prints a warning.
- `audience = "mixed"` is no longer a valid value. An unrecognized value
  falls back to the default and prints a warning naming the bad value.
- `audience`, `writingStandard`, `tone`, `language`, and `technicalTerms`
  left under `[planStyle]` (their old location) still work. Each one prints
  a "moved to `[style]`" warning until you move it to `[style]`.

### Plugin-wide scope

`[style]` sets the reader level, writing standard, tone, and language for
chat, status lines, summaries, and `AskUserQuestion` text in **every sdlc
skill**, not only `/plan`. It does not apply to commit messages, PR bodies,
review comments, or Jira text — those keep their own templates and config
(`/setup commit`, `pr`, `pr-template`, `jira`).

## Custom plan instructions

- Key: `[planStyle] instructions` in `.sdlc-v2/local.toml` (one instruction per array entry).
- Where they apply: printed at plan start, passed to every subagent, repeated after compaction, checked in Step 7.
