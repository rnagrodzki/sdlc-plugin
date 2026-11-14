# Learnings

Read this page when you want to turn the lessons of earlier runs into rules, or
when you want to know where the learnings counts and the skill recommendations
come from.

## Learnings triage

    /sdlc:harden --from-learnings

`/sdlc:harden` reads `.sdlc-v2/learnings/log.md` and triages each entry that it
did not write itself (entries with the header `## YYYY-MM-DD — harden:` are
skipped). It needs no `--skill`: it reads the skill name from the header of
each entry. It is not valid with `--auto`, because it deletes learnings entries
and needs a person.

- **One orchestrator pass for each entry.** `/sdlc:harden` loads each entry as a
  failure and classifies it. Then it presents the proposals of each entry
  with the normal approval prompt: user-code entries first, then ambiguous,
  then plugin-defect.
- **The custom instructions and the `Pre-flight findings:` line print once**,
  after the first entry that loads. Every entry reads the same config.
- **A pre-flight repair or a dead-reference proposal shows once.** Each entry
  drafts the same proposals for them, so `/sdlc:harden` shows each one with the
  first entry only.
- **Applied entries are removed.** An entry with at least one applied
  proposal is removed from the log, in one call at the end. A pre-flight
  repair or a dead-reference proposal does not count, because it does not
  fix the entry.
- **Cancel removes nothing.** A **cancel** answer ends the run. No learnings
  entry is removed, and `/sdlc:harden` writes no learnings entry of its own.
- **A config error stops the whole run.** An invalid `[harden.instructions]`
  table, a config version error, or a `config.toml` that cannot be read stops
  the triage at the first entry. No entry is removed.

## Learnings stats

The `learnings_log` tool has a `stats` action. It summarizes
`.sdlc-v2/learnings/log.md` and does not return the full log. The summary
counts the recorded lessons by category and by skill:

- The total number of entries.
- The number of entries for each category. The category comes from the branch
  tag of the entry, for example `feat` or `fix`.
- The number of entries for each skill.
- `topPatterns`: the `Rule: ...` lessons that recur most. At most 10, sorted
  by count, then by recency.
- `recentFailures`: the number of the 20 most recent entries that have a
  failure tag.

A missing or empty log is not an error. Each count is then zero.

## Skill recommendations

`/sdlc:harden` reads the stats of `learnings_log` and looks for lessons that
recur. A lesson that recurs 3 times or more becomes a skill recommendation, a
candidate for a skill or a guardrail, with a priority:

| Priority | Times seen |
|----------|------------|
| `high` | 6 or more |
| `medium` | 4 or 5 |
| `low` | 3 |

A recommendation becomes a proposal, unless an existing rule on one of the four
surfaces (plan guardrails, execute guardrails, review dimensions, Copilot
instructions) already covers it. The proposal goes through the same approval
prompt as any other proposal. A recommendation is never an edit of its own: it
is evidence for a proposal on one of those four surfaces.

## Structured output

`/sdlc:harden`'s preparation step returns five fields: `manifestPath`, `mode`,
`customInstructions` (the `[harden.instructions]` lists, one list for each
proposal surface), `preflightFindingCount` (the number of [pre-flight
findings](./auto-mode-and-edits.md#pre-flight-findings)) and `next` (the step
after the call). The orchestrator reads the full manifest at `manifestPath` to
build proposals. The manifest holds the failure, the surfaces, the pre-flight
findings, the dead references and the same `customInstructions` map.

`/sdlc:harden` always starts by calling `prepare_orchestrator` (mode `harden`)
to load the failure context and surface manifest. This must happen before any
other tool call (unless invoked with `--from-learnings`, which first reads the
log with `learnings_log` and then calls `prepare_orchestrator` once per entry,
config-version check included). The same call loads the [custom
instructions](./custom-instructions.md), and an invalid `[harden.instructions]`
table stops the run here. A [pre-flight
finding](./auto-mode-and-edits.md#pre-flight-findings) does not stop the run:
`/sdlc:harden` prints the count, and it repairs the findings first.
