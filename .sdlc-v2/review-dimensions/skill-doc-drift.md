---
name: skill-doc-drift
description: Detect drift between docs/skills/*.md, skill reference docs, and SKILL.md source of truth
triggers:
  - "docs/skills/**"
  - "plugins/sdlc/skills/**/*.md"
severity: medium
---

## Checklist

- Every step, tool call, and flag documented in `docs/skills/<skill>.md` matches the corresponding `plugins/sdlc/skills/<skill>/SKILL.md` — SKILL.md is the source of truth.
- A step renamed, reordered, or removed in SKILL.md is reflected in the matching `docs/skills/*.md` file (or flagged if not).
- A tool name referenced in `docs/skills/*.md` still exists and is still called at the point in SKILL.md the doc describes.
- A field name or flag documented in `docs/skills/*.md` still matches SKILL.md's current dispatch args/output field names.
- New mandatory steps added to SKILL.md (e.g. a new "Step 1: Load State" gate) are not missing from the corresponding `docs/skills/*.md` file.
- Every flag declared in a SKILL.md's front-matter `argument-hint` section (or parsed in Step 0) has a corresponding row or section in the matching `docs/skills/<skill>.md` file (e.g. in a Flags table, Arguments section, or command-line reference). A flag documented in SKILL.md but missing from docs is a finding.
- When a config-key migration or namespace rename affects multiple sections of a `docs/skills/*.md` file (e.g. Configuration section, Tips bullets, Flags table), verify **all** sections via grep for old key names and flag references, not just the obviously-related primary section. A partial sync creates contradictions within the file's own sections.
- When a SKILL.md introduces or modifies a blocking gate (e.g. a verdict classification that halts execution, prevents decomposition, or requires manual override), the corresponding `docs/skills/<skill>.md` must document: (1) what the gate blocks, (2) what conditions trigger it, (3) whether a bypass flag or option exists (`--auto`, `--force`) and what it does, or explicitly state that no bypass exists. The absence of a bypass is a fact to document, not an omission.
- When a SKILL.md change applies the same behavior (fail-fast condition, skip condition, blocking gate) to two or more steps, enumerate every step it touches by grepping SKILL.md, and confirm `docs/skills/<skill>.md` documents that behavior for each step. A documentation update covering only one sibling (e.g. documenting `verify-openspec` fail-fast-on-missing-disk but not `archive-openspec`) is a finding.
- A claim in a skill reference doc (`plugins/sdlc/skills/**/*reference.md`, `state-format.md`, `config-format.md`) about what another step or skill does (for example, which step runs the test suite, or which step writes state) matches that skill's SKILL.md step text. Cite the reference doc line and the SKILL.md step. A stale claim is a finding.
- When a SKILL.md step and `docs/skills/<skill>.md` each list a closed set (stop causes, error cases, exit states, outcomes, allowed values), the two lists have the same members. Compare them item by item: an item in one list and not in the other, or an item with a different condition, is a finding. Matching surrounding prose does not make the lists match.
- When a SKILL.md or tool change adds a value or line with a stated format (a timestamp line, a file-name pattern, an ID form, a marker line that a SKILL.md step has the skill write, such as a cut line), the SKILL.md step that defines it and the matching `docs/skills/<skill>.md` page each state that format with one literal sample. When one skill writes the line and another skill reads it, the sample shows the exact text the reader matches. A step or page that names the value but omits its format is a finding.
- Each worked example in `docs/skills/<skill>.md` follows the rules the page and SKILL.md state, and shows what sets each derived part of a value. An example that hides which run sets a value, such as a date written once at creation and kept on a later continue, is a finding.
- When a change removes a mode, flag or value, or makes two of them behave the same (for example a flag whose two modes now write the same way), grep `SKILL.md`, `docs/skills/<skill>.md`, the `argument-hint` and the tool and `jsonschema_description` text for the old name. Also grep for wording that still describes a difference between the modes. Each hit is a finding, because a reader acts on a difference that no longer exists. This extends the rename and migration bullet above to a mode that stays in the code but no longer differs.
- When a SKILL.md step adds or changes an exit (a stop, cancel or error exit, including a stop in the middle of a dialogue after a failed tool call), enumerate from SKILL.md every route that reaches it, then grep `docs/skills/<skill>.md` for that step and confirm the page names the exit, its trigger and the user's next action. For each status or state value that the page explains (for example `paused`), grep SKILL.md and the Go code for every route that writes the value, and confirm the page names every cause. A page that omits the exit, or that gives one cause where several routes set the value, is a finding. This extends the closed-set bullet above: a page that has no list of exits still has to name each exit, and a value with one named cause is checked for the causes it lacks.
- A claim on a `docs/skills/<skill>.md` page that carries a scope word or a count word (always, never, again, only, every, once, twice, at most once, no longer, a later call) holds on every branch of the matching SKILL.md step: the first call, a repeat or resume call, a retry after an error, each stop or cancel exit, and a run that continues a file written by an earlier version (an old form that the step reads or converts). Walk each branch against the claim. For a claim about how many times a question or call runs, list every step and pass that can ask or make it (a repeat pass, a final pass, a hand-off) and count them. A claim that is true on the main path only is a finding, for example 'a later check does not ask again' when one branch of a later check asks again, 'the empty-section question is asked once' when a later pass asks it again, or 'the section exists only when a question is open' when an old-form file holds the section with no bullet until the step converts it. The page states the condition under which the claim holds. `documentation-review` checks the same kind of claim against the Go handler; this bullet checks it against SKILL.md.

## What NOT to flag

- `docs/skills/*.md` content that is intentionally more general or example-driven than SKILL.md's terse procedural prose.
- Internal SKILL.md implementation detail (state file paths, internal helper names) with no user-facing doc obligation.

## Cross-references

- `skill-prose-accuracy.md` covers SKILL.md-vs-source-code drift (implementation, not docs/).
- `skill-wiring-consistency.md` covers SKILL.md dispatch args vs MCP tool struct tags.
- This dimension is the missing third leg: `docs/skills/*.md` vs SKILL.md itself.
