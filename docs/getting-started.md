# Getting started

This is the onboarding path: install the plugin, set up a project, and learn
which command to run at each stage of a change. For how the plugin works
internally (launcher, binary cache, tool list, config schema), see
[`README.md`](../README.md). For a step-by-step verification checklist after
install, see [`smoke-test.md`](smoke-test.md).

## Prerequisites

- Claude Code with plugin marketplace support.
- `git`.
- [GitHub CLI](https://cli.github.com/) (`gh`), authenticated (`gh auth
  status`). Required by `pr`, `ship`, `verify-pipeline`, and
  link validation — these shell out to `gh` directly.
- A Jira/Atlassian MCP connection, only if you plan to use `jira`.
  Everything else works without it.

## 1. Install the plugin

```
/plugin marketplace add rnagrodzki/sdlc-plugin
/plugin install sdlc@sdlc-plugin
/reload-plugins
```

`/reload-plugins` is what makes Claude Code read `.mcp.json` and
`hooks/hooks.json`. Run it again after any future plugin update.

### What to expect on the first session

The plugin's logic lives in a compiled Go binary that gets downloaded and
cached on first use — nothing is bundled in the plugin install itself. On a
cold cache:

- Hooks (including the `sdlc: v<version> (<N> skills loaded)` SessionStart
  banner) may print nothing on this very first session. Hooks run under a
  short (~1s) timeout and fail open rather than wait on a download.
- The first tool call, or `/reload-plugins` itself, opens the MCP connection,
  which has a longer budget (60s) and actually completes the download.

If the banner doesn't show up, don't treat it as broken — run `/clear` (or
start a new session) after the first tool call or reload, and it will appear.
Full detail: `README.md` → "First-run binary fetch".

## 2. Set up a project

Run once per repository, before using any other `sdlc` skill in it:

```
/setup
```

This scaffolds `.sdlc/config.json` (project config, committed) and
`.sdlc/local.json` (user-local, gitignored) and walks you through a
selective-section menu — review dimensions, PR template, guardrails, and
more. Every section explains what it changes and which skills consume it
before it prompts you for anything.

If the repo already has SDLC config from the old Node-based plugin
(markers like `.claude/sdlc.json`, `.sdlc/jira-config.json`,
`.sdlc/review.json`), any skill that needs config will tell you to migrate
instead. Run:

```
/setup --migrate
```

Don't run plain `/setup` on a project you're not sure about — if it
turns out to have legacy config, a skill will tell you so and name
`--migrate` explicitly; you don't need to guess in advance.

## 3. The day-to-day workflow

Each stage of a change maps to one skill. Run them in order, or let
`ship` / `deliver` chain several of them for you.

| Stage | Skill | When to use it |
|---|---|---|
| Plan | `/plan` | Turn a requirement, spec, or description into a task-decomposed implementation plan. |
| Execute | `/execute-plan <plan-file>` | Implement a plan file wave by wave, with per-wave verification. |
| Commit | `/commit` | Generate a commit message from the staged diff and commit history, then commit. |
| Review | `/review` | Multi-dimension code review (security, performance, docs, etc.) of the current diff. |
| Respond to review | `/received-review` | Work through reviewer or CI feedback on an open PR. |
| Version | `/version <major\|minor\|patch>` | Bump the version, update the changelog, tag a release. |
| Open a PR | `/pr` | Generate a PR description from commits/diff and open it via `gh`. |
| Verify CI | `/verify-pipeline --pr <N>` | Diagnose and optionally fix a failing CI run on a PR. |
| Jira | `/jira` | Create, read, or update Jira issues linked to the work. |
| After a failure | `/harden` | Propose guardrail changes so the same pipeline failure can't recur. |

For the full end-to-end flow instead of running each stage by hand:

```
/ship
```

runs execute → commit → review → version → PR → CI verification as one
pipeline, stopping for confirmation between steps by default. For an
unattended multi-hour run (execute → review-fix loop → ship, no stops):

```
/deliver <plan-file>
```

Both accept `--resume` to pick back up from a saved state file if
interrupted.

## 4. Supervised vs. unattended

By default (`automation.mode: supervised` in `.sdlc/local.json`), every
pipeline step stops for your confirmation. Set `mode: unattended`, optionally
with per-step overrides in `automation.steps`, to let `ship` /
`deliver` run end-to-end without stopping. Field reference: `README.md`
→ "Automation config".

## Next steps

- Run through [`smoke-test.md`](smoke-test.md) to confirm the install works
  end to end.
- Hit an error? `README.md` → "Troubleshooting" covers launcher and
  MCP-registration failures.
