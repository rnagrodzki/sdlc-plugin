---
name: skill-cross-reference-consistency
description: When a skill, MCP tool, or sub-flow changes, verify every other skill/doc referencing it (See Also links, delegatedTo targets, pass-through flags, tool descriptions) stays accurate and non-contradictory.
triggers:
  - "plugins/sdlc/skills/**"
  - "internal/tools/**"
  - "internal/mcpserver/**"
  - "plugins/sdlc/agents/**"
severity: high
---

## What to check

- **See Also / delegated references.** A path or `delegatedTo` id another
  file points to must still exist, and its behavior/arguments must still
  match what the referencing text claims.
- **Pass-through flags.** If skill A's text says "pass through `--add` if
  present" to skill B, verify skill B's own Arguments table actually
  documents `--add`.
- **Same-tool cross-skill claims.** When two or more skills describe the
  same MCP tool's behavior, fields, or side effects, the descriptions
  must agree with each other AND with the tool's current Go source — not
  just be individually plausible.
- **Shared config keys.** When multiple SKILL.md files read or write the
  same config path (e.g. `plan.guardrails`, `pr.labels`), every
  describing skill's claim about that path's shape or ownership must be
  consistent with the others — no skill assuming a shape another skill's
  write contradicts.
- **Rename/removal completeness.** Before treating a skill or tool rename
  as complete, grep the whole `plugins/sdlc/skills/` tree for every prior
  name. A stale reference in an unrelated skill file is a contradiction,
  not a leftover formatting issue.
- **Command/MCP surface parity.** When a skill's prose claims a step
  "runs via `<tool>_<action>`" or lists an MCP tool as covering a step,
  verify that tool is actually registered (`internal/skillcheck`
  registries) and its action/behavior matches the claim — do not accept
  a plausible-sounding tool name without checking it exists.
- **Callee output consumed by the caller.** When a skill dispatches a
  sub-skill, a background worker, or an MCP tool whose returned fields
  carry a signal the caller must act on (an unstopped-worker report, a
  stop-failure list, a blocking verdict), the caller must name the step
  that reads each such field and state its action: stop, retry, or
  disclose to the user. A returned signal with no consuming step in the
  caller is a finding.
- **Go-side step labels.** A `next` hint, `Suggestion`, or error string in
  Go that names a SKILL.md step number or label (for example `7c2 and 7d`)
  must match that SKILL.md's current heading. Grep the heading and compare
  both the number and the label. A Go string that says `7c2 and d` where the
  SKILL.md says `7c2 and 7d` is a finding.
- **Instruction parity with returned text.** A prohibition, a required order or a recovery step in a SKILL.md or docs page (for example 'do not edit the file by hand') must agree with the `Next`, `Suggestion` and warning text that the dispatched tool returns for the same situation. Read the Go strings and the SKILL.md step side by side. When one text forbids what the other tells the caller to do, or names a different recovery step, that is a finding, because the caller follows the text it reads last. This extends the Go-side step labels bullet above, which compares step numbers and labels but not instructions.
- **Row kinds and part states read by another skill.** When one skill writes a stored file or table that another skill or tool reads as input (for example a topic file that the plan skill reads as its requirements file), list every row kind or marker the writer can record: a prefix such as `Dropped:` or `Moved:`, a status value, a kept or drop note. Also list every part the writer can leave empty or out: a section that a custom template omits, a field that a soft-gate handoff leaves blank, a line such as a cut line that is absent. For each kind and each such state, read each reader (the reading skill's step, its tool and its docs page) and state how it treats a row of that kind or a part in that state. A reader that treats a marker row as an ordinary live item (a requirement, a proposal or a counted row) is a finding. A reader with no rule for an empty or absent part is a finding. A reader that cites rows by number must map each number to the item it stands for, and a number with no mapping is a finding. A fallback that records an item as another kind (for example a non-goal as a requirement item) is a finding, because every later reader acts on it as the wrong kind. A reader page that lists fewer kinds than the writer records is a finding too. The writer and each reader state the same rule for each kind and each state.
- **One fact, one printed line.** When a SKILL.md step tells the model to print a text block that a tool returns (a card text, a summary, a `Next`) and also tells it to print its own line about a fact that the block already states (for example a server state or a run status), read the tool's block and the skill line side by side. A block that ends with one value and a second line that states another, or two lines that state the same value, is a finding: the user sees a contradiction or a repeat. The skill prints the block as it is and adds a line only for a fact that the block lacks, or the tool carries the fact and the skill adds nothing. Read each state in which the tool's text can end (such as a failed probe) and confirm that the skill line does not contradict it. Example: a text block that ended with `Dashboard: not running`, followed by a second `Dashboard` line from the skill.
- **Same call, same print rules.** When two or more skills (for example ship and execute) call the same tool and print its result in the same way (a card text, a warning line, a summary), list each rule that one of them states for that print: a style or format exemption for the block, a fence and label, a cap, a gate flag, the exits and modes in which it shows. Read each other calling skill and confirm that it states the same rule, or states why it differs. A rule that one skill has and a sibling that prints the same result lacks (for example a style exemption for a verbatim text block) is a finding, because the sibling falls back to the general style rule on the block. This extends Same-tool cross-skill claims, which compares the behavior of the tool, to the rules for printing its result.
- **One claim, every copy.** A claim worded as a limit or an absolute about what a tool action or another skill does or does not do (for example 'ensure on a running server starts nothing', or a claim with only, never or always) is checked against the tool source and against the skill or docs page that owns the behavior, not only against the file that states it. Grep `plugins/sdlc/skills/`, `docs/` and `openspec/specs/` for the key phrase of the claim and list each copy (for example in ship, execute and dashboard). Each copy is true in every state in which the action runs (a server that already runs, a server that is not running, a failed probe, a retry), or it names the exception beside the claim. A copy that stays overbroad after another copy was corrected is a finding. This extends Same-tool cross-skill claims, which compares the descriptions of one tool between skills, to an absolute claim and each of its copies.

## What NOT to flag

- A skill intentionally overriding or extending another's default
  behavior via a documented flag — that is not a contradiction, only an
  unsynced default.
- Differences in wording or detail level that don't change the
  substantive claim (e.g. one skill briefly mentions a tool, another
  documents it fully) — flag only actual factual contradictions, not
  verbosity differences.
- Link reachability (HTTP/GitHub/Jira URLs) — that is `links_validate`'s
  job, not this dimension's.

## Cross-references

- `skill-wiring-consistency.md` — single-skill-to-tool parameter wiring
  within one dispatch.
- `skill-prose-accuracy.md` — a single skill's prose vs its own
  implementation.
- `skill-flow-soundness.md` — a single skill's internal flow shape.
- This dimension is the only one checking skill-to-skill and
  skill-to-tool consistency across the whole tree, not within one file.
