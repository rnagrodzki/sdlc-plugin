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
| `[spec-file-path]` | Path to a requirements or spec file to plan from. | none |
| `--auto` | Skip all interactive prompts. Picks conservative defaults. | off |
| `--spec` | Enable OpenSpec integration. Loads spec context from the project's OpenSpec directory. | off |
| `--from-openspec <name>` | Load a specific OpenSpec change by name (directory name under `openspec/changes/`). | none |

## Examples

**Plan from a description you type interactively:**

    /plan

The skill asks you to describe what you want to implement. It explores the
codebase, decomposes the work into tasks, and writes a plan file.

**Plan from a spec file:**

    /plan docs/requirements/auth-redesign.md

Reads the spec file and uses it as input for planning.

**Plan from an OpenSpec change:**

    /plan --from-openspec user-onboarding

Loads the proposal, delta specs, and task list from
`openspec/changes/user-onboarding/` and builds a plan around them.

## Related skills

- [/execute](execute.md) — Takes the plan file this skill produces and
  implements it wave by wave.
- [/ship](ship.md) — Runs `/execute` as its first step, so it consumes plan
  files too.

## Tips and gotchas

- **Plan files are saved to disk.** The plan is written to a file. You pass
  that file path to `/execute` or `/ship` later — they do not pick it up
  automatically.
- **Complexity routing matters.** If your change touches only 1 file, the skill
  tells you no plan is needed (or writes a lightweight plan in plan mode). Full
  planning with multi-agent exploration kicks in for 4+ files or unclear scope.
- **OpenSpec is opt-in.** Even if your project has OpenSpec configured, the
  skill only loads spec context when you pass `--spec` or `--from-openspec`.
- **Plan mode vs. normal mode.** In plan mode, the skill writes to the
  designated plan file path. In normal mode, it creates a file and tells you
  where it is.
