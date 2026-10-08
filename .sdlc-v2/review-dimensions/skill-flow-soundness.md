---
name: skill-flow-soundness
description: Walks a SKILL.md's narrated step flow end-to-end to catch missing steps, unreachable branches, contradictory gates, orphaned flags, and unreachable exit states.
triggers:
  - "plugins/sdlc/skills/**/SKILL.md"
severity: high
---

## What to check

- **Step reachability.** Every step must be reachable under some valid
  input. A branch gated on a condition that can never hold — or that
  contradicts an earlier gate in the same flow — is dead.
- **Exit-state completeness.** Every terminal state the skill describes
  (success, cancel, error, stall) must have an explicit next instruction.
  A state the flow can enter but never says how to leave is incomplete.
- **Gate consistency.** A condition gating one step must not be
  contradicted by another step in the same file assuming the opposite
  (e.g. one step skips work "if `--force` was NOT passed" while a later
  step assumes `--force` is always set by that point).
- **Arguments-table / Workflow parity.** Every flag documented in the
  Arguments table must be referenced by name somewhere in Workflow, and
  every flag referenced in Workflow must appear in the Arguments table.
  An orphan in either direction is a flow defect — a flag nobody reads, or
  a behavior nobody can invoke.
- **Numbered-step drift.** After steps are renumbered or reordered, every
  cross-reference to a step number — in the same file, or in a companion
  sub-flow it dispatches — must be updated to match.
- **Step insertion impact on branch routing.** When a new step (e.g. Step
  2b) is inserted into an existing flow, verify that every conditional
  branch and routing that previously connected step N to step N+1 is still
  accurate — the inserted step may now lie in the middle. Also verify that
  bypass notes and direct-entry options that list skippable steps remain
  accurate: is the newly inserted step bypassable, or has it changed the
  mandatory-vs-optional structure of any path?
- **Approval-gate reachability.** An `AskUserQuestion` step must be
  reachable on every path that needs it. Verify no earlier branch can
  silently skip past a mandatory approval gate.
- **Per-step rule wiring.** When a skill's prose defines a general rule
  that applies to multiple steps (e.g. "each checkpoint step calls
  `plan_mark`", "every lane/lens/reviewer that records evidence has a
  fallback when `evidence_record` fails"), grep each named step body to
  verify the inline call or fallback is actually present. A blanket
  paragraph stating the rule does not satisfy a contract that names
  per-step behavior; every named step must carry its own inline
  instruction.
- **AskUserQuestion option quality.** Options offered to the user must be
  mutually exclusive, phrased without a leading/loaded framing, and (per
  this project's own tool convention) carry a real, statable default —
  not a set where only one option is actually workable.
- **Resource cleanup.** When a skill's flow dispatches background workers,
  sub-agents, or async tasks (e.g. Agent tool calls), verify that every
  possible exit path (success, stall, error, timeout) includes an explicit
  cleanup step: a TaskStop call, an agent termination, or a documented
  reason why cleanup is not needed (e.g. workers terminate themselves).
  No worker should be left running after the skill completes or is
  interrupted. Specifically, for stall handlers (Step 3) and cleanup
  phases (Step 9), grep for TaskStop calls or equivalent termination logic
  on each dispatched Agent task. Abandoned workers are a resource leak and
  token waste.
- **Routing-bullet exclusivity.** When one step lists several routing
  bullets keyed on the same input (a round number, a verdict, a gate
  result), exactly one bullet must match any given input state. Walk the
  boundary states, such as the last round, a CRITICAL gate result, or a
  single-item batch, against every bullet. A state that matches two
  bullets with different outcomes is a finding, even when each bullet
  reads correctly on its own.
- **Shared-record field parity.** When two or more routes write the same
  persisted record (a round record, a history row, a state snapshot),
  every route must derive each field from the same named source, or the
  prose must state which route wins on conflict. A route that hard-codes
  a field (for example zero counts) while another route can still add
  data to that field makes the record disagree with itself, and is a
  finding. The same applies to one call described in two places with
  different arguments.

## What NOT to flag

- An optional step intentionally skipped by a documented flag — that is a
  designed shortcut, not a dead branch.
- Prose style or wording choices that don't change the actual step
  sequence — that is `skill-prose-accuracy`'s job, not this dimension's.

## Cross-references

- `skill-prose-accuracy.md` — verifies individual factual claims against
  source; this dimension verifies the shape of the flow itself
  (reachability, completeness, gate consistency), a different failure
  mode from "is this sentence true."
- `skill-cross-reference-consistency.md` — verifies claims across
  multiple skill files; this dimension is single-file, flow-shape-only.
- Guardrail: `skill-flow-soundness` (plan and execute) enforces this same
  check at plan time and again independently before a task is marked
  done.
