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
| `--auto` | Skip all interactive prompts. Picks conservative defaults. | off |
| `--spec [<change-name>]` | Opts into OpenSpec and skips the gate-check question. With a name: plan from that existing change (`openspec/changes/<change-name>/`). Without one: use the branch-matched change, else ask which active change to use, or offer to create a new one. | off |
| `--from-openspec <name>` | Deprecated alias of `--spec <name>`, kept for one release. Prints a deprecation notice. | none |

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

## Tips and gotchas

- **Plan files are saved to disk.** The plan is written to a file. You pass
  that file path to `/execute` or `/ship` later — they do not pick it up
  automatically.
- **`plan_prepare` always runs first, but it only resumes state when asked.**
  The skill's first action is always a `plan_prepare(...)` call. By default it
  starts a fresh run. Pass `resume: true` to reuse the active run instead —
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
- **Plan mode vs. normal mode.** In plan mode, the skill writes to the
  designated plan file path. In normal mode, it creates a file and tells you
  where it is.

## Recovery after compaction

- What you see: `Active plan (post-compact): step <n>, branch <b>; plan file: <path>` in the session context, followed by a line that tells Claude to invoke the sdlc:plan skill first when the skill instructions are not in context, and a `Resume with:` line.
- What the skill does: reloads the run with `plan_prepare` (resume mode), reads the evidence digest, then resumes at the saved step; it does not clear the plan file.
- Where evidence lives: <main-worktree>/.sdlc-v2/runs/<runId>.evidence/ (deleted after the plan is done).

## Custom plan instructions

- Key: `[planStyle] instructions` in `.sdlc-v2/local.toml` (one instruction per array entry).
- Where they apply: printed at plan start, passed to every subagent, repeated after compaction, checked in Step 7.
