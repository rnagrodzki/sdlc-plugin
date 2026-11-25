---
applyTo: "plugins/sdlc/skills/**/SKILL.md"
---
# skill-prose-accuracy — Review Instructions

Validates that SKILL.md prose descriptions accurately reflect actual implementation via direct grep/read, not inferred from build/test success alone.

Default severity: high

## Verification Checklist
- A verb such as evaluates, validates or enforces matches what the code does, and a prune or garbage-collection claim names each case the code keeps.
- A quoted or paraphrased error gives the cause and the recovery that the code text gives.
- A positional reference such as 'the fourth value' matches the canonical list.
- When a change replaces a check or a call with another, each step that relied on the old one names the tool and action that now does the job. A step that says 'check again' with no named call is a finding.
- A quoted or paraphrased error, `next` text, question or option text gives the cause, the recovery and the result that the source text gives. A quote is copied from the source, and a quote of each branch of one choice is checked on its own.
- A row or rule that states what a state does to calls, or how a value is set, cleared or pruned, is compared with each write path and event in the code. A row that refuses two write paths when the code refuses one, or a lifecycle rule that lists three of four events, is a finding, unless the text says it names only some.
- A list of the actions, kinds or values that a flag or mode applies (for example the edit actions of --auto), or a step that describes a dispatch, is compared with the enum or the agent file that defines it. A member, input, step or output that the source has and the text omits is a finding, unless the text says that it names only some.
- A pointer to a field of a tool result, or a claim that an action returns it (for example warnings), names a field that the `*Out` struct and the handler of that action return.
- Prose that says a field or key is empty, blank or none is compared with what the writer produces. A key that the writer leaves out is absent, not empty, and prose that names the other state is a finding.
- A sentinel such as `(none)` is compared with each state that the writer or renderer maps to it (empty, absent, not checked, unavailable). A sentinel that hides two states that the skill routes differently, with no stated way to tell them apart, is a finding.
- A change that adds a question, a step, a section, a mode or a write is swept against the whole SKILL.md. A negative claim that the new part breaks (for example 'does not ask about X' when a step now asks about X), and a count word that the new part makes wrong (for example 'five sections'), are findings, also when the changed lines read correctly alone.
- A change that adds a member to a set that the skill lists (a new kind of section, bullet, line or prefix) is swept against every list of that set in the SKILL.md: a prefix list, a list of bullet forms that a step recognizes or strips, the overview and each table. A list of the old kinds that omits the new kind is a finding, also when the changed lines are complete.
- A claim that cites a source (a file and line, a function, a test, a docs page) is checked by opening the source. A citation that is silent on the claim, says something else or covers only one mode is a finding, also when the claim is true.
