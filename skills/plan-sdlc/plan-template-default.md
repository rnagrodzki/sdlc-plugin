# Plan Template

The default plan structure shipped with `plan-sdlc`. A project can replace this file entirely via
`.sdlc/plan-template.md`; when that file is absent, this shipped default is the active template (see
plan-format-reference.md's `## Plan Template` section). The active template is the single source PF10
reads for section presence, and the source the Step 2 planner follows when writing plan sections.

## Required Sections
- Context <!-- narrative: true -->
- Research Findings <!-- narrative: true -->
- Deviations & assumptions
- Key Decisions <!-- narrative: true -->
- Final Shape <!-- narrative: true -->
- Tasks
- Verification Scorecard
- OpenSpec Appendix <!-- conditional: source matches openspec/changes/ or openspecInlineGenerate -->
- Contract Examples

## Discovery Questions

Questions the Step 1 exploration phase answers before decomposition begins; the answers become the
`## Context` section.
- What problem does this change solve?
- What prompted this change now?
- What does success look like?

## Verification Patterns

Verification approaches a plan's tasks should draw from when filling in each task's `**Verify:**`
field and the `## Verification Scorecard`.
- Run existing test suite for regression
- Verify new checks against fixtures
