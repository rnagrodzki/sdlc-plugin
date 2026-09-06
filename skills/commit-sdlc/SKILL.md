---
name: commit-sdlc
description: "Use this skill when committing staged changes, creating a git commit, or generating a commit message. Analyzes staged diff and recent commit history to generate a message matching the project's style. Stashes unstaged changes to isolate the commit, commits after user confirmation, and auto-restores the stash. Arguments: [--no-stash] [--scope <scope>] [--type <type>] [--amend] [--auto] [--force-default-branch]. Use --auto to skip interactive approval. Triggers on: commit changes, create commit, write commit message, git commit, smart commit, commit staged, stage and commit."
user-invocable: true
argument-hint: "[--no-stash] [--scope <scope>] [--type <type>] [--amend] [--auto] [--force-default-branch]"
model: haiku
---

# Smart Commit Skill

Call the `commit_prepare` MCP tool for commit context, generate a commit message matching
the project's style, and commit after user confirmation via the `commit_apply` MCP tool.

**Announce at start:** "I'm using commit-sdlc (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

## Port Notes (read before using this skill)

This is a Go/MCP port of the original script-driven skill. Its tool surface
(`commit_prepare`, `commit_apply`) is narrower than the frontmatter's argument-hint
suggests. Concretely:

- `--no-stash` is a no-op. This port never stashes anything — see Step 5.
- `--scope <scope>` / `--type <type>` are forwarded to the orchestrator only as drafting
  hints in its prompt text; no tool validates or enforces them.
- `--amend` is **not supported**. This port always creates a new commit.
- `--force-default-branch` is a no-op. There is no automatic block on committing to the
  default branch in this port to override — see Step 0's default-branch note.
- `--auto` still works, but purely as your own interpretation of the skill's invocation
  arguments — `commit_prepare`'s output carries no `flags.auto` field in this port.

## When to Use This Skill

- Committing staged changes with an auto-generated message
- Generating a commit message that matches the project's existing style
- Detecting (not auto-squashing — see Step 1c) WIP commits made by execute-plan-sdlc

## Workflow

### Step 0 — Plan Mode Check

If the system context contains "Plan mode is active":

1. Announce: "This skill requires write operations (git commit). Exit plan mode first, then re-invoke `/commit-sdlc`."
2. Stop. Do not proceed to subsequent steps.

---

### Step 0 (CONSUME): Call `commit_prepare`

```
commit_prepare({ skipConfigCheck: false, sessionID: "" }) → data
```

Leave `sessionID` empty unless you already know the session identifier from context — it is
only an override, not a required field.

**On tool error:** show the error to the user and stop.

Treat the returned `data` object as `COMMIT_CONTEXT`.

**If `COMMIT_CONTEXT.errors` is non-empty**, show each error message and stop. (This
includes "no files staged for commit" and any config-check failure — this port has no
config-migration surface to fold into a separate `migration` notice; `commit_prepare`'s
`migration` field is always `null`.)

**If `COMMIT_CONTEXT.warnings` is non-empty**, show the warnings to the user before
continuing.

**Default-branch note:** If `COMMIT_CONTEXT.onDefaultBranch` is `true`, warn the user before
Step 5 that they are about to commit directly to the default branch. There is no automatic
block in this port (unlike the original script's `--auto` + missing `--force-default-branch`
rejection) — the Step 5 approval prompt is the only gate.

### Step 0.5 (BRANCH-GUARD): not functional in this port

`COMMIT_CONTEXT.branchGuard` is always `{ ok: true }` in this port — there is no
`active`/`expectedBranch` concept, and `commit_prepare` accepts no branch-guard input. Skip
this check entirely and proceed to Step 1.

---

### Step 1 (CONSUME): Quick Context Read

Read from `COMMIT_CONTEXT`: `currentBranch`, `staged.files`, `staged.fileCount`,
`staged.diffStat`, `staged.diff` (or `staged.diffStat` plus `staged.truncatedFiles` when
`staged.diffTruncated` is true), `unstaged.hasChanges`, `recentCommits`,
`commitConfig.subjectPattern`, `commitConfig.subjectPatternError` (`commitConfig` is `null`
when the project has no `commit` config section — treat every `commitConfig.*` gate below as
skipped in that case).

### Step 1c (WIP-commit squash detection — reporting only)

Read `wipSquash` from `COMMIT_CONTEXT`:

```json
{ "wipSquash": { "commits": ["<sha>\t<subject>", ...], "stagedClean": true } }
```

`commits[]` lists commits between the branch's fork-point and `HEAD` whose subject starts
with `wip(` or `wip:` (case-insensitive).

**This port does not auto-squash WIP commits.** If `wipSquash.commits` is non-empty, tell the
user: "Detected N WIP commit(s) from execute-plan-sdlc. This port does not squash them
automatically — they will remain as separate commits in history; the new commit you are
about to make is additional, not a replacement. Squash them yourself beforehand if you want
a single commit for this change." Then proceed to Step 2 regardless.

**Final-message invariant (no `wip:` prefix) — LLM-side only, no deterministic script check
in this port:** When drafting the commit subject (Step 2), never generate one starting with
`wip:` or `wip(execute):` — those are internal markers. Before showing the message in Step 5,
check it yourself against that rule; if it matches, revise it before presenting.

### Step 2 (PLAN): Dispatch the commit-orchestrator Agent

`sdlc:commit-orchestrator` only has Read tool access — it cannot call MCP tools itself. Write
`COMMIT_CONTEXT` to a scratch JSON file first (via the Write tool), then point the agent at
that file:

- `subagent_type`: `sdlc:commit-orchestrator`
- `model`: `haiku`
- `prompt` (exactly two lines, no other content):

  ```text
  MANIFEST_FILE: <path to the scratch file you just wrote>
  PROJECT_ROOT: <cwd>
  ```

The orchestrator reads the manifest, applies every `commitConfig` constraint
(`subjectPattern`, `allowedTypes`, `allowedScopes`, `requireBodyFor`, `requiredTrailers`),
detects style from `recentCommits`, runs its own self-critique loop, and returns ONLY the
final commit message string. It does not call `git`, does not write files, does not invoke
`gh`, and cannot call MCP tools.

Capture the orchestrator's return value as `MESSAGE`. If `MESSAGE` is empty, the orchestrator
detected an `errors[]` array in the manifest — surface those errors and stop.

**OpenSpec scope hint (main context, optional):** If no `--scope` was passed, Glob for
`openspec/config.yaml`. If found, Glob `openspec/changes/*/proposal.md` (exclude
`archive/`). If exactly one active change exists, or one matches the current branch name,
append an `OpenSpec-Change: <change-directory-name>` trailer to `MESSAGE` (after a blank
line; only if `MESSAGE` already has a body). The hook context fast-path applies: if the
session-start system-reminder has an `OpenSpec active:` line, use it instead of Glob.

### Step 3 (CRITIQUE) and Step 4 (IMPROVE)

The orchestrator agent owns Steps 3 and 4 internally. The main context does not re-run them;
the orchestrator's returned `MESSAGE` is already self-critiqued against the gate table below.

### Step 5 (DO): Present and Execute

**This port stages and commits everything currently in the working tree; there is no
stash-isolation of unstaged changes and no amend support.**

Show the full commit plan to the user with `MESSAGE` and the staged-file summary from Step 1.
**Do not call `commit_apply` before receiving explicit user approval via AskUserQuestion.**

**Auto mode:** If `--auto` was passed to this skill invocation, skip the AskUserQuestion
prompt entirely. Still display the full commit plan for visibility, then proceed directly to
execution. Treat the response as an implicit `yes`. (This is your own reading of the
invocation arguments, not a tool-reported flag — see Port Notes.)

```
Commit
────────────────────────────────────────────
Message:    feat(auth): add OAuth2 PKCE flow

            Replaces the implicit flow with PKCE to comply with
            the new OAuth 2.1 requirements.

Staged:     3 files changed, +142, -12
  src/auth/pkce.ts
  src/auth/index.ts
  tests/auth/pkce.test.ts

Trailer:    OpenSpec-Change: add-oauth2-pkce  (if applicable)

Note:       everything in the working tree is staged and committed together
            (no stash-isolation in this port)
────────────────────────────────────────────

```

Use AskUserQuestion to ask:
> Commit as shown?

Options:
- **yes** — commit as shown
- **edit** — tell me what to change
- **cancel** — abort

**On `yes`:**

0. **Subject pattern gate (hard gate, LLM reasoning — no tool for this):** If
   `commitConfig.subjectPattern` is set, check yourself whether the subject line matches that
   pattern as a regular expression. Do not run any command to do this.

   - If it matches: continue to step 1.
   - If it does not match: show the message from `commitConfig.subjectPatternError` if set,
     otherwise the pattern itself as a fallback. Do **not** proceed with the commit. Use
     AskUserQuestion to offer:
     - **edit subject** — let the user revise the subject line to match the pattern; re-check
       it yourself before proceeding
     - **harden** — run `/harden-sdlc` to analyze why this failed and propose stronger
       guardrails. Opt-in — no surface is edited without your approval. Suppressed when
       `--auto` is set. When selected, dispatch `Skill(harden-sdlc)` with
       `--failure-text "Subject pattern reject: subject does not match commitConfig.subjectPattern"`,
       `--skill commit-sdlc`, `--step "Step 5 — subject pattern gate"`,
       `--operation "subject pattern validation"`.
     - **cancel** — abort the commit
     Do not allow overriding this gate via a non-edit choice.

1. **Link verification — HARD GATE.** Write the commit message body to a scratch file (via
   the Write tool), then:

   ```
   links_validate({ file: "<scratch file path>", offline: false }) → { results: [...] }
   ```

   Any `results[]` entry whose `status` is `"violation"` is a hard-gate failure (`"ok"` and
   `"skipped"` are both fine — `"skipped"` means the URL matched an intentional skip-list, not
   a problem). On any `"violation"` entry, do NOT proceed. Surface the full violation list
   (`url`, `line`, `reason`, `detail`) to the user. Stop. Do not retry. Do not edit URLs
   without user input. Pass `offline: true` in sandboxed/offline environments to skip network
   reachability checks while keeping context-aware ones.

2. Call the commit tool:

   ```
   commit_apply({ message: MESSAGE, skipConfigCheck: false, sessionID: "" }) → { sha }
   ```

   **On tool error:** show the error to the user and stop.

**On `edit`:** Ask what to change, revise `MESSAGE`, and present again. Loop until explicit
`yes` or `cancel`. Re-dispatching the orchestrator is not required for small wording tweaks —
apply user-supplied edits to `MESSAGE` directly and re-check the subject-pattern gate before
re-presenting.

**On `cancel`:** Abort without changes.

**Hook failure handling:** If `commit_apply` fails because a pre-commit hook rejected the
commit, tell the user: "The commit hook rejected this commit. Fix the hook issue and re-run
`/commit-sdlc`." There is no stash to restore in this port, so nothing else is needed.

### Step 6 (CRITIQUE): Verify

`commit_apply`'s returned `sha` is the confirmation — a non-empty `sha` means the commit
succeeded. No further command is needed to verify it.

Show the result:

```
✓ Committed: a1b2c3d feat(auth): add OAuth2 PKCE flow
  Files:   3 files changed, +142, -12
```

(Use the first 7 characters of `sha` in place of `a1b2c3d`.)

---

## Quality Gates

| Gate | Check | Pass Criteria |
| ---- | ----- | ------------- |
| Style match | Message follows project's commit style | Consistent with `recentCommits` patterns |
| Subject length | Subject ≤ 72 characters | `len(subject) <= 72` |
| Accuracy | Message describes the actual staged diff | Every claim traceable to `staged.diff` or `staged.diffStat` (when `staged.diffTruncated` is true) |
| Type correctness | Commit type matches the change | `feat`=new feature, `fix`=bug fix, `refactor`=restructure, `chore`=maintenance |
| Imperative mood | Subject uses imperative form | "add" not "adds" or "added" |
| No fabrication | Nothing invented beyond the diff | Every claim backed by staged changes |
| Body relevance | Body adds value or is absent | Does not restate the subject; no filler |
| No WIP prefix | Subject does not start with `wip:`/`wip(` | See Step 1c invariant |
| Pattern match | Subject matches `commitConfig.subjectPattern` regex | Regex passes; skip when `commitConfig` is null or `subjectPattern` is absent |
| Required body | Body present when type in `commitConfig.requireBodyFor` | Body non-empty for the selected type; skip when `commitConfig` is null or `requireBodyFor` is absent |
| Required trailers | All `commitConfig.requiredTrailers` keys present in body | Every listed trailer key appears; skip when `commitConfig` is null or `requiredTrailers` is absent |

## Best Practices

1. Read the full staged diff when available; when `staged.diffTruncated` is true, combine included diffs with `staged.diffStat` for truncated files
2. Match the project's commit style from `recentCommits`
3. Prefer conventional commits when the project uses them
4. Keep the subject concise — details go in the body
5. Body explains "why"; subject explains "what"
6. Present the full diff stat so the user can verify scope before confirming

## DO NOT

- Call `commit_apply` without explicit user approval (`yes`) (unless `--auto` was passed)
- Fabricate changes not present in `staged.diff`
- Skip the critique step (Step 3, owned by the orchestrator)
- Include file paths in the subject line
- Claim this port stashes or amends anything — it does neither (see Port Notes)
- Silently drop detected WIP commits without telling the user (Step 1c)

## Error Recovery

| Error | Recovery | Invoke error-report-sdlc? |
| ----- | -------- | ------------------------- |
| `commit_prepare` tool error | Show the error, stop | No — usually a user/config issue |
| No staged changes (`errors` includes it) | Inform user, suggest staging files | No — user action needed |
| `commit_apply` tool error (pre-commit hook) | Show the error; inform user there is no stash to restore; suggest fixing the hook and retrying | No — hook failure is expected |
| `commit_apply` tool error (other) | Show the error | Yes, if it looks like a defect rather than a user/config issue |
| `links_validate` reports violations | Show the violation list, stop, do not retry automatically | No — user needs to fix the links |

When invoking `error-report-sdlc`, provide:
- **Skill**: commit-sdlc
- **Step**: the step where the tool call failed
- **Operation**: the MCP tool name and input
- **Error**: the tool's returned error message
- **Suggested investigation**: check git identity; verify no branch protection rules

---

## Gotchas

- **No stash isolation**: unlike the original script-driven skill, this port always commits
  the full working tree via `commit_apply`. If you need to commit only part of your changes,
  stage exactly what you want before invoking this skill, then use another means to preserve
  the rest — this skill does not do it for you.
- **No amend support**: to change the last commit, use another tool outside this skill.
- **WIP commits are not squashed**: see Step 1c — they remain as separate commits.
- **Commit on default branch**: a warning is shown (Step 0), but nothing blocks it — the
  Step 5 approval prompt is the only gate.
- **Empty body**: a commit body is optional. Only include one when the staged diff is
  non-trivial and the "why" adds real value.
- **Single commit in repo**: `recentCommits` may have fewer than 15 entries on a new repo.
  This is fine — fall back to conventional commits as the default style.

## Learning Capture

After completing a commit, if the project's detected commit style was non-conventional or
unusual, append to `.sdlc/learnings/log.md`:

```
## YYYY-MM-DD — commit-sdlc: <brief summary>
<what was learned about this project's commit style or any edge case encountered>
```

## What's Next

After completing the commit, common follow-ups include:
- `/review-sdlc` — review the changes
- `/version-sdlc` — tag a release
- `/pr-sdlc` — create a pull request

## See Also

- [`/review-sdlc`](../review-sdlc/SKILL.md) — review changes after committing
- [`/pr-sdlc`](../pr-sdlc/SKILL.md) — create a PR after committing
- [`/version-sdlc`](../version-sdlc/SKILL.md) — tag a release after committing
