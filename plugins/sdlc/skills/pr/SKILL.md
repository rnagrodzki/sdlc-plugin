---
name: pr
description: "Use this skill when creating or updating a pull request, updating a PR description, or generating PR content from commits and diffs. Handles the full PR workflow: consumes pre-computed context from the `pr_prepare` MCP tool, generates description with plan-critique-improve-do-critique-improve, user review, and gh CLI execution. Auto-labels PRs based on context signals (branch, commits, diff, Jira) with mandatory approval. Arguments: [--draft] [--update] [--base <branch>] [--auto] [--label <name>]. Use --auto to skip interactive approval. Triggers on: create PR, open pull request, update PR, write PR description, PR summary, describe changes for a pull request."
user-invocable: true
argument-hint: "[--draft] [--update] [--base <branch>] [--auto] [--label <name>]"
model: sonnet
---

# Creating Pull Requests

Call the `pr_prepare` MCP tool for preflight context, generate an 8-section (or
project-custom) PR description via plan-critique-improve-do-critique-improve, get user
approval, then call `pr_apply` to create or update the PR.

**Announce at start:** "I'm using pr (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

## Port Notes (read before using this skill)

This is a Go/MCP port. The frontmatter's argument-hint describes a richer feature set than
this port's tool surface (`pr_prepare`, `pr_apply`) actually supports:

- **This port does not support draft PRs, arbitrary label management, explicit base-branch
  override, or automatic account-switch retry on gh failure; if gh reports an error, stop and
  report it.** `pr_apply` accepts `title` and `body`, plus optional `releaseLevel` /
  `releasePreRelease` / `releaseNotes` (forwarded from the version step — see "Release intent"
  below): when `releaseLevel` is set, `pr_apply` injects release markers into the body and
  auto-applies a `release:<level>[-rc]` label via `gh pr edit --add-label`. There is still no
  field for `--draft`, an arbitrary `--label`, or a target base branch, and no recovery helper
  runs after a failure.
- `pr_prepare` does not supply a commit list, diff stat/content, remote state, changed files,
  repository labels, or a pre-detected create-vs-update mode. Draft the title and body using
  only context already available in this conversation (recent commits you made or observed,
  files you edited, what the user has told you) — do not run commands to gather this data.
- Branch-guard is real and enforced **inside** `pr_prepare` itself: if it fails, `pr_prepare`
  returns it as an error and the Step 0 check below already stops you. There is no separate
  branch-guard step to run.
- `pr_apply` decides create-vs-update internally (by checking whether a PR already exists for
  the current branch) and reports which one happened via its `created` field. Nothing in this
  port tells you in advance which mode will run — present the plan generically.
- There is no config-backed PR title pattern to validate against in this port (`pr_prepare`
  has no `prConfig`/title-pattern fields) — this port skips the title-pattern gate entirely.
  There is also no config-backed label-inference mode.
- Link verification (Step 6) routes through the `links_validate` tool, **not**
  `validate`'s `pr_body` action — that action only checks whether a template's section
  headings are present in the body, it does not check links at all.

## When to Use This Skill

- Creating a new pull request on any branch
- Updating an existing PR title or description
- Writing or rewriting a PR description
- Summarizing branch changes for review

## PR Template

> **Project template**: If `PR_CONTEXT.template` is not null, use its `headings` as the
> template's section names, in order. `PR_CONTEXT.template.content` shows the fill instruction
> text under each heading. Apply the same fill rules: real content, "N/A", or "Not detected" —
> never fabricate. All sections named in `headings` must appear in the output. If
> `PR_CONTEXT.template.legacy` is true, mention once that the template was found at the
> deprecated `.claude/pr-template.md` location and suggest moving it to `.sdlc-v2/pr-template.md`.

When `PR_CONTEXT.template` is null, every PR uses this 8-section flat structure. **All sections in the active template are always present.**

```markdown
## Summary
[1-3 sentence plain-language overview accessible to anyone — no jargon]

<!-- Only include this section when PR_CONTEXT.jiraTicket is non-empty -->
## JIRA Ticket
[Auto-detected from branch name, e.g. PROJ-123]

## Business Context
[Why this change is needed from a business/product perspective.
What problem or opportunity prompted it.
"N/A" only for pure internal tooling/infra with no business dimension.]

## Business Benefits
[What value this delivers — user impact, revenue, efficiency,
risk reduction, compliance, etc.
"N/A" only for pure internal tooling/infra with no business dimension.]

## Technical Design
[Architectural approach, key decisions, patterns used.
Non-obvious trade-offs or alternatives considered.]

## Technical Impact
[What systems, services, APIs, or areas are affected.
Breaking changes, migration needs, performance implications.
"N/A" if the change is fully isolated with no external impact.]

## Changes Overview
[Bullet-point list grouped by logical concern (not by file).
Each bullet describes a concept or behavior change — e.g.:
- Webhook handler validates event ID before processing and records it after success
- New migration adds processed_events table with TTL index
- Retry deduplication test coverage added
No file paths in this section.]

## Testing
[How this was verified: manual steps, automated tests, edge cases.
If no tests added, explain why.]
```

**Section fill rules:**

- ALL sections in the active template MUST always be present — never omit one
- **JIRA Ticket is an exception:** When `PR_CONTEXT.jiraTicket` is empty, omit this section entirely (don't include "Not detected")
- Fill with real content when derivable from conversation context
- Use **"N/A"** when a section genuinely doesn't apply (state why briefly)
- Use **"Not detected"** when detection was attempted but yielded nothing (except for JIRA Ticket — see above)
- **Never fabricate** — if unsure, ask a clarifying question before filling
- Ask clarifying questions (especially for Business Context and Business Benefits)
  when available context isn't sufficient to fill the section confidently

---

## Workflow

### Step 0 — Plan Mode Check

If the system context contains "Plan mode is active":

1. Announce: "This skill requires write operations (creating or updating a pull request). Exit plan mode first, then re-invoke `/pr`."
2. Stop. Do not proceed to subsequent steps.

---

### Step 0 (CONSUME): Call `pr_prepare`

```
pr_prepare({ skipConfigCheck: false, expectedBranch: "<optional>" }) → data
```

Pass `expectedBranch` only if this skill was invoked with an explicit expected-branch
requirement (for example, by an orchestrating pipeline); otherwise omit it.

**On tool error:** show the error to the user and stop.

Treat the returned `data` as `PR_CONTEXT`.

**If `PR_CONTEXT.ok` is `false`**, `PR_CONTEXT.errors` already explains why — this single
check covers every hard-gate failure in this port: config-migration failure, gh not
authenticated, gh account mismatch, branch-guard failure, and being on the default branch
(`main`/`master`). Show each error message and stop.

**If `PR_CONTEXT.warnings` is non-empty**, show the warnings prominently before continuing
(this includes an uncommitted-changes warning when applicable — those files will not be part
of the PR). Do not ask for confirmation — the Step 5 approval gate is the consent point.

### Step 1: Consume the Context

| Field | Description |
| ----- | ----------- |
| `ghAuthenticated` / `activeAccount` / `expectedAccount` | GitHub CLI auth state (a mismatch already caused a Step 0 stop) |
| `currentBranch` | The branch being PR'd |
| `uncommittedChanges` / `dirtyFiles` | Uncommitted files that will NOT be part of the PR |
| `jiraTicket` | Detected ticket reference from the branch name, or empty |
| `template` | `{ path, legacy, headings, content }` or `null` — see PR Template above |

**Release intent (invocation input, not part of `PR_CONTEXT`):** when this skill is dispatched
with `releaseLevel` / `releaseNotes` / `releasePreRelease` / `releaseSource` (e.g. by `/ship`
forwarding the version step's resolved plan, with `releaseSource` set to `"config"` or
`"pipeline"` per `/ship`'s own upfront gate), hold all of them for Step 6's `pr_apply` call and
for the Step 5 announcement below. `pr_apply` is a hard gate on this: it rejects any
`releaseLevel` whose `releaseSource` is missing or invalid, and (in `autoMode`) rejects
`releaseSource: "user"` outright — never call it with a `releaseLevel` and no matching
`releaseSource`, and never invent either value yourself.

**Without a `releaseLevel` at invocation** (this skill invoked standalone, not via `/ship`), do
not silently proceed with no release intent — run the Release Intent Gate below before Step 2.

### Step 1b (GATE): Release Intent

Skip this gate entirely if `releaseLevel` was already supplied at invocation (the `/ship` case
above) — nothing to ask, the source has already been decided upstream. This gate only fires on a
standalone `/pr` invocation with no release intent given.

**Auto mode, no `releaseLevel` supplied:** do not ask, and do not silently skip. Stop and report
a clear error: standalone `/pr --auto` cannot decide release intent — there is no human to confirm
it and `pr_apply` rejects `releaseSource: "user"` under `autoMode` unconditionally. Run `/ship`
instead, which resolves release intent (source `"config"` or `"pipeline"`) before it ever reaches
this skill. If standalone auto-mode PR creation with a release label is truly needed, the caller
must pass both `releaseLevel` and a `releaseSource` of `"config"` or `"pipeline"` as explicit
dispatch args to this skill — there is no `/pr` CLI flag for this, so a human cannot trigger it
directly. Do not fabricate a level or relabel it `"user"` to get past this.

**Interactive mode, no `releaseLevel` supplied:** use AskUserQuestion:

> No release intent specified. A merged PR without a release label skips the release pipeline.
>
> Options:
> 1. **Set release level** — specify patch/minor/major (optionally with an RC pre-release)
> 2. **Skip release (acknowledged)** — create this PR without release intent

On option 1: ask which level (and whether it's an RC), then hold `releaseLevel` and
`releaseSource: "user"` for Step 6. On option 2: proceed with no release intent — this was an
explicit, acknowledged choice, so do not ask again at Step 5 or Step 6.

### Step 2 (PLAN): Draft PR Description

> **If `PR_CONTEXT.template` is not null**: use its `headings` as the section list and its
> `content` as fill guidance. Skip the default per-section instructions below and draft all
> custom sections instead.

Draft all sections of the active template using only context already available in this
conversation — commits or diffs you have already seen, files you have already edited, or
what the user has told you. Do not run commands to gather commit history or diffs for this
purpose; if you don't have enough context to fill a section confidently, ask the user.

**OpenSpec enrichment (automatic when detected):**

1. Glob for `openspec/config.yaml`. If absent, skip this block entirely.
2. Identify the active change: Glob `openspec/changes/*/proposal.md` (exclude `archive/`). If one matches, use it. If multiple, match against `PR_CONTEXT.currentBranch`. If ambiguous, skip — do not ask during PR creation.
3. If an active change is found, Read in parallel:
   - `proposal.md` — use intent and scope to pre-fill **Business Context** and **Business Benefits** (reduces need for a clarifying question)
   - `design.md` (if it exists) — use architectural approach for **Technical Design**
4. Add to the PR description, below the title: `**OpenSpec:** openspec/changes/<name>/`

When OpenSpec context provides business rationale, use it directly instead of asking the user. Still ask if the proposal is too vague to fill Business Context/Benefits confidently.

For each section, apply the fill rules:

- **Summary**: Plain-language, no jargon, 1-3 sentences
- **JIRA Ticket**: Include only when `PR_CONTEXT.jiraTicket` is non-empty; omit the section entirely if no ticket found
- **Business Context / Benefits**: Infer from conversation context. If insufficient evidence, **use AskUserQuestion** to ask the user before writing. Don't guess. Acceptable question: *"What business problem does this PR solve? Who benefits and how?"*
- **Technical Design**: Infer from the changes you already know about — architecture, patterns, key decisions
- **Technical Impact**: Identify affected systems/APIs/services from what you already know
- **Changes Overview**: Group by logical concern — each bullet describes a concept or behavior change. Never list file paths.
- **Testing**: Summarize test coverage you already know about; if none, say so explicitly

Also draft the PR title: under 72 characters, using conventional commit style (`feat:`,
`fix:`, `refactor:`, etc.) unless the user directs otherwise. This port has no
config-backed title pattern to validate against — there is nothing further to enforce here.

### Step 3 (CRITIQUE): Self-review the Draft

Before presenting to the user, review the draft against every quality gate:

| Gate | Check | Pass Criteria |
| ---- | ----- | ------------- |
| All sections present | Every section named by the active template has content | Real content, "N/A", or "Not detected" — never empty |
| Specificity | Summary names a concrete change | No vague summaries like "various improvements" |
| Business honesty | Business Context/Benefits are concrete or "N/A" | No "because it was needed" or invented reasons |
| No file paths | Changes Overview uses concepts only | Zero file paths in this section |
| Title length | Title under 72 characters | `len(title) < 72` |
| No fabrication | All claims traceable to conversation context | Nothing invented |
| JIRA accuracy | JIRA section omitted when no ticket; when present, value matches `jiraTicket` | No guessed ticket numbers, section absent if not detected |
| Audience check | Readable by non-technical stakeholders | No unexplained jargon in Summary/Business sections |
| Documentation sync | If the change adds new commands, changes structure, renames concepts, or adds new directories/scripts: ask the user to confirm docs are updated — this port has no commit-list data to check for a `docs:` commit automatically | PR does not silently ship structural changes without addressing docs |
| Link verification | Every URL in the body will be validated by `links_validate` before publishing (see Step 6) | Deferred to Step 6's hard gate |

> **Note**: When a project template is active, the "No file paths in Changes Overview" gate
> applies only if the template includes a section named "Changes Overview". All other
> universal gates apply regardless of template.

Note every failing gate.

### Step 4 (IMPROVE): Revise Based on Critique

Fix each issue found in Step 3:

- Rewrite vague sections with specifics
- Replace invented content with "N/A" or "Not detected" plus a note
- If a business section still can't be filled confidently after revision,
  **use AskUserQuestion** to ask a targeted clarifying question and incorporate the answer
- Re-check all quality gates after revisions

Continue until all gates pass (max 2 iterations per gate).

### Step 5 (DO): Present for Review

Show the complete title and description. **Do not call `pr_apply` before receiving explicit
user approval via AskUserQuestion.**

**Announce release intent first, when set.** If a `releaseLevel` was passed to this skill (see
"Release intent" in Step 1), state it before the description, e.g.:

```text
Release: patch (pre-release: rc) — publishing will apply label "release:patch-rc"
```

Or, with no pre-release: `Release: minor — publishing will apply label "release:minor"`. Omit
this line entirely when no `releaseLevel` was passed — do not imply a release will happen when
one won't.

**Auto mode:** if `--auto` was passed to this skill invocation, skip the AskUserQuestion
prompt entirely. Still display the full title and description for visibility, then proceed
directly to Step 6. Treat the response as an implicit `yes`. All critique gates (Steps 3–4)
still run — only the interactive approval prompt is skipped. (This is your own reading of the
invocation arguments — `pr_prepare`'s output carries no `isAuto` field in this port.) Pass
`autoMode: true` to `pr_apply` in Step 6 whenever `--auto` was passed here — this is what
makes `pr_apply` enforce that `releaseLevel` (if any) came from config/pipeline, not the LLM.

```text
PR Title: <title>

PR Description:
─────────────────────────────────────────────
<full description>
─────────────────────────────────────────────
```

Use AskUserQuestion to ask:
> Publish this PR as shown? (This port cannot tell in advance whether it will create a new PR or update an existing one for this branch — that is decided automatically.)

Options: **yes** — publish | **edit** — tell me what to change | **cancel** — abort

If the user chooses `edit`, ask what to change, revise, and present again. Loop until
explicit `yes` or `cancel`.

### Step 6: Create or Update PR

**Only execute after explicit `yes` from Step 5.**

**Link verification — HARD GATE.** Before calling `pr_apply`, write the final PR body to a
scratch file (via the Write tool), then:

```
links_validate({ file: "<scratch file path>", offline: false }) → { results: [...] }
```

Any `results[]` entry whose `status` is `"violation"` is a hard-gate failure (`"ok"` and
`"skipped"` are both fine). On any `"violation"` entry, do NOT proceed. Surface the full
violation list (`url`, `line`, `reason`, `detail`) to the user and stop. Do not retry. Do not
edit URLs without user input. Do not bypass.

On zero violations, publish. Include `releaseLevel`/`releaseNotes`/`releasePreRelease`/
`releaseSource` only when a `releaseLevel` was resolved (Step 1 / Step 1b); omit them entirely
for a no-release PR. `autoMode` mirrors whether `--auto` was passed to this skill invocation:

```
pr_apply({
  title: <title>,
  body: <body>,
  releaseLevel: <if set — "major" | "minor" | "patch">,
  releaseNotes: <if set>,
  releasePreRelease: <if set>,
  releaseSource: <if releaseLevel set — "user" | "config" | "pipeline">,
  autoMode: <true | false — whether --auto was passed to this skill invocation>
}) → { url, created }
```

**On tool error:** show the error to the user and stop — this port does not retry or attempt
an account switch. If the failure looks like a GitHub CLI authentication or installation
problem, tell the user to install and authenticate the GitHub CLI themselves, then offer to
paste the drafted title and description so they can create or update the PR by hand.

On success, display the result using `created` to pick the wording:

```text
# created === true:
Pull request created: <url>

# created === false:
Pull request updated: <url>
```

---

## Best Practices

1. **Use all context you have, not just the latest message** — the PR is the sum of all branch work you already know about
2. **Ask rather than guess** — a clarifying question is better than fabricated content
3. **No file paths in Changes Overview** — reviewers think in concepts, not paths
4. **Flag risks** — call out migrations, permission changes, or config changes
5. **Preserve author intent** — if you already know the design rationale from commit messages or the conversation, carry it into the description

## DO NOT

- Omit any section from the active template — always include all defined sections
- Write generic descriptions ("various improvements", "code cleanup")
- Fabricate a JIRA ticket, business reason, or technical claim
- Include file paths in the Changes Overview section (only applies if that section exists)
- Call `pr_apply` without explicit user approval (unless `--auto` was passed)
- Skip the plan-critique-improve-do-critique-improve cycle before presenting to the user
- Run git or gh commands to gather data — all context comes from `PR_CONTEXT` plus what you already know from the conversation

## Error Recovery

| Error | Recovery | Invoke error-report? |
|-------|----------|---------------------------|
| `pr_prepare` tool error | Show the error, stop | Yes, if it looks like a defect |
| `PR_CONTEXT.ok` is `false` | Show each error, stop | No — user/auth/setup issue |
| `links_validate` reports violations | Show the violation list, stop, do not retry automatically | No — user needs to fix the links |
| `pr_apply` tool error | Show error; offer manual fallback (copy title + description) | Yes, if it looks like a defect |

When invoking `error-report`, provide:
- **Skill**: pr
- **Step**: the step where the tool call failed
- **Operation**: the MCP tool name and input
- **Error**: the tool's returned error message
- **Suggested investigation**: check installed plugin version; verify git remote is configured and branch is pushed; verify GitHub CLI authentication

---

## Gotchas

- **No draft PRs, labels, or base-branch override**: this port's `pr_apply` only accepts
  `title` and `body` — none of `--draft`, `--label`, or `--base` can be honored.
- **Create-vs-update is decided at publish time**: `pr_prepare` does not report whether a PR
  already exists for the branch, so the Step 5 plan must be presented generically; only
  `pr_apply`'s `created` field (Step 6) tells you which one happened.
- **No auto-recovery on gh failure**: a `pr_apply` failure (permission mismatch, missing
  auth, network error) surfaces directly — there is no automatic account-switch retry.
- **OpenSpec change detection during PR creation should not block.** Unlike plan, which
  can ask the user to disambiguate multiple active changes, pr should silently skip
  OpenSpec enrichment if the change cannot be uniquely identified from the branch name.

## Learning Capture

When creating pull requests, capture discoveries by calling `learnings_log({action: "append",
entry: "## YYYY-MM-DD — pr: <brief summary>\n<details>"})`. Record entries for: repository PR
conventions not covered by this skill, branch naming patterns, CI requirements that affect PR
descriptions, team-specific template preferences, JIRA project key patterns, or review
process quirks encountered while generating PR content.

## What's Next

After creating or updating the PR, common follow-ups include:
- `/review` — review the branch
- `/version` — tag a release after merge

If OpenSpec enrichment was applied in Step 2 (an active change was detected), also suggest:
- `openspec validate --strict <change>` — validate change spec files structurally (after merge)
- `openspec archive <change> --yes` — archive the OpenSpec change and merge delta specs (after validation passes)

## See Also

- [`/commit`](../commit/SKILL.md) — commit changes before creating a PR
- [`/review`](../review/SKILL.md) — review the branch
- [`/version`](../version/SKILL.md) — tag a release after merge
