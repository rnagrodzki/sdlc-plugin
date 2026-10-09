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
  status`). Required by `pr`, `ship`, `received-review`, `verify-pipeline`,
  and link validation — these shell out to `gh` directly. `openspec-save`
  also needs it, because it runs the `pr` skill.
- The [OpenSpec CLI](https://github.com/Fission-AI/OpenSpec)
  (`npm i -g @fission-ai/openspec`), only if you use OpenSpec: `plan` with
  **Create OpenSpec change**, `openspec-save`, and the OpenSpec steps of `ship`.
- A Jira/Atlassian MCP connection, only if you plan to use `jira`.
  Everything else works without it.

## 1. Install the plugin

```
/plugin marketplace add rnagrodzki/sdlc-plugin
/plugin install sdlc@sdlc-plugin
/reload-plugins
```

Installing from a local clone instead (dev/offline)? See
[`local-install.md`](local-install.md).

`/reload-plugins` is what makes Claude Code read `.mcp.json` and
`hooks/hooks.json`. Run it again after any future plugin update.

### What to expect on the first session

The plugin's logic lives in a compiled Go binary that gets downloaded and
cached on first use — nothing is bundled in the plugin install itself. On a
cold cache:

- Hooks (including the `sdlc: v<version> (commit <hash>, built <time>) (<N>
  skills loaded)` SessionStart banner) may print nothing on this very first
  session. Hooks run under a short (~1s) timeout and fail open rather than
  wait on a download. `<hash>`/`<time>` come from the binary's build info
  (embedded at release build time, or read from Go's VCS stamping on a plain
  `go build`) and default to `dev`/`unknown` when neither is available.
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

This scaffolds `.sdlc-v2/config.toml` (project config, committed) and
`.sdlc-v2/local.toml` (user-local, gitignored) and walks you through a
selective-section menu — review dimensions, PR template, guardrails, and
more. Every section explains what it changes and which skills consume it
before it prompts you for anything.

Both files are written verbatim from browsable, fully-commented template
files shipped with the plugin at `plugins/sdlc/templates/config.toml` and
`plugins/sdlc/templates/local.toml` — read them directly to preview the
whole config shape before running `/setup`.

### User-level personal settings
Put values that you want in every project in `~/.sdlc/local.toml` (or the path in `SDLC_USER_CONFIG`).
The file uses the same sections as `.sdlc-v2/local.toml`.
Order: built-in default < `~/.sdlc/local.toml` < `.sdlc-v2/local.toml` < command flags.
Tables merge key by key. A list in the project file replaces the user list.
The template writes every `[ship]` key as a commented example, so a new project uses the built-in defaults or your user file.

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
`ship` chain several of them for you.

| Stage | Skill | When to use it |
|---|---|---|
| Preplan | [`/preplan`](skills/preplan.md) | Shape an idea into a topic file, with each proposal checked against the plan guardrails. Pass the file to `/plan`. |
| Plan | `/plan` | Turn a requirement, spec, or description into a task-decomposed implementation plan. |
| Save the spec | `/openspec-save <plan-file>` | After `/plan` staged an OpenSpec change, save it as its own branch, commit, and PR. Merge that PR first, then run `/ship` on a new branch from the default branch. |
| Execute | `/execute <plan-file>` | Implement a plan file wave by wave, with per-wave verification. |
| Commit | `/commit` | Generate a commit message from the staged diff and commit history, then commit tracked changes plus staged files (untracked files stay out). |
| Review | `/review` | Multi-dimension code review (security, performance, docs, etc.) of the current diff. |
| Respond to review | `/received-review` | Work through reviewer or CI feedback on an open PR. |
| Open a PR | `/pr` | Generate a PR description from commits/diff and open it via `gh`; also diagnoses version state (source, divergence, recommended next bump) when a version config is set. |
| Verify CI | `/verify-pipeline --pr <N>` | Diagnose and optionally fix a failing CI run on a PR. |
| Jira | `/jira` | Create, read, or update Jira issues linked to the work. |
| After a failure | `/harden` | Propose guardrail changes so the same pipeline failure can't recur. |
| After a run | `/deferred` | Triage findings that were saved but not fixed — review findings and issue drafts earlier runs recorded and left open. |

For detailed documentation on each skill (flags, examples, tips), see the
[Skill Reference](skills/README.md).

For the full end-to-end flow instead of running each stage by hand:

```
/ship
```

runs execute → commit → review → PR → CI verification as one
pipeline, stopping for confirmation between steps by default.

It accepts `--resume` to pick back up from a saved state file if
interrupted.

### Integration branch

Set `[git] baseBranch = "develop"` in `.sdlc-v2/config.toml` when you
integrate on a branch other than the repository default. Execute, ship, pr,
review and commit use it. An explicit `--base` flag on `pr` or `review`
still wins; `execute`, `ship`, and `commit` have no `--base` flag and always
use the resolved value.

Leave it unset (the default, `""`) and each of those skills falls back to the
repository's own default branch instead.

### Linked worktrees

When a skill runs from a git worktree other than the main one, a
session-start hook symlinks the run-generated parts of `.sdlc-v2/` into the
linked worktree, so they stay visible and live without a new session:

- `runs`
- `reports`
- `history`
- `evidence`
- `learnings`
- `reviews`
- `state`
- `timings.json`
- `preplan`
- `run-archive`

`.sdlc-v2/config.toml` is never linked or symlinked — each worktree keeps its
own real config file.

At each session start the hook also checks the state links. A state link is
`.sdlc-v2/<name>`, where `<name>` is one of the names in the list above.
`<main worktree>` is the path of the main worktree. A broken link is a link
whose target does not exist.

In a linked worktree, the hook does these steps:

- It creates a missing state link as a link to `<main worktree>/.sdlc-v2/<name>`.
- It replaces a broken state link that points to another path with a link to `<main worktree>/.sdlc-v2/<name>`. For example, a `.sdlc-v2/run-archive` link to a deleted worktree becomes a link to `<main worktree>/.sdlc-v2/run-archive`.
- It keeps a state link to another path that exists, and prints a note.
- It creates the folder `<main worktree>/.sdlc-v2/<name>` when it is missing. It does this for each name in the list except `timings.json`. `timings.json` is a file, and a run writes it later.
- When `<main worktree>/.sdlc-v2/<name>` is itself a broken link, it prints a note that tells you to start a session in the main worktree.

In the main worktree, the hook never removes a real file or a real folder. It
does these steps for each state link:

- It removes a broken state link when the target of the state link ends in `.sdlc-v2/<name>`. The next write creates a real folder.
- It keeps each other state link, and prints a note. This includes a link to a path that exists, a broken link to another path, and a link loop.

Two operations stop at a broken link:

- The dashboard Archive action stops with `ARCHIVE_FAILED` when `.sdlc-v2/run-archive`, or a folder above it, is a broken link. See [Archive a run](dashboard.md#archive-a-run).
- `/sdlc:preplan` stops in the `plan_support` action `preplan_context` when `.sdlc-v2/preplan`, or a folder above it, is a broken link. It also stops when the topic file `<slug>.md` is a broken link.

For a broken folder link, the error says:
`<link> is a link to <target>, which does not exist. Start a new session so the session-start hook repairs the link, or run: mkdir -p <target>`

For a broken topic file link, the error says:
`<link> is a link to <target>, which does not exist. Remove the link, then try again: rm <link>`

When the link is the first link of a chain, the first sentence is
`<link> resolves through links to <target>, which does not exist.`

The dashboard shows the two sentences in one message. `preplan_context`
puts the first sentence in the error message and the recovery sentence in the
suggestion.

To recover from a broken folder link:

- If the link is a state link in a linked worktree, start a new session in that worktree. If the session prints a note about the main worktree, start a session in the main worktree too.
- If the link is a state link in the main worktree and `<target>` ends in `.sdlc-v2/<name>`, start a new session in the main worktree.
- In all other cases, the hook keeps the link, so a new session does not repair it. Run `mkdir -p <target>`, or remove the link by hand: `rm <link>`.

## 4. Supervised vs. unattended

By default (`automation.mode: supervised` in `.sdlc-v2/local.toml`), every
pipeline step stops for your confirmation. Set `mode: unattended`, optionally
with per-step overrides in `automation.steps`, to let `ship`
run end-to-end without stopping. Field reference: `README.md`
→ "Automation config".

## Next steps

- Run through [`smoke-test.md`](smoke-test.md) to confirm the install works
  end to end.
- [Skill Reference](skills/README.md) — detailed docs for every slash command.
- Hit an error? `README.md` → "Troubleshooting" covers launcher and
  MCP-registration failures.
