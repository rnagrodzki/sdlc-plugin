---
name: error-report
description: "Internal skill invoked by other SDLC skills when they encounter an actionable error (script crash, CLI failure, persistent API error, build failure after retries). Proposes creating a GitHub issue in rnagrodzki/sdlc-plugin to track the error with full context capture, two-gate user consent, and pre-flight verification. NOT user-invocable — only dispatched from within another skill's error handling path."
user-invocable: false
disable-model-invocation: true
---

# Error-to-GitHub Issue Proposal

<!-- disable-model-invocation: true prevents the harness from auto-triggering this skill
     when conversation content matches the description. It does NOT prevent explicit
     dispatch from another skill's error-handling path — that is the only intended
     activation route. user-invocable: false hides the skill from the / menu.

     Do NOT pin `model:` in this frontmatter — doing so would route this skill's own
     invocation into a subagent that inherits the full conversation transcript (issue
     #202), which is exactly the problem the dedicated error-report-orchestrator agent
     (Step 4) exists to avoid. Pin `model: haiku` on that Agent-tool dispatch instead. -->

Internal procedure invoked by SDLC skills when an actionable error occurs.
Captures error context, verifies gh CLI availability, gets user consent, and
creates a tracking issue in `rnagrodzki/sdlc-plugin` using the gh CLI.

The skill body runs in the main parent-model context. The heavy work — assembling
the issue title and body from the error context and the `templates/ToolingError.md`
template — is dispatched to the dedicated `error-report-orchestrator` agent so the
main conversation transcript is never inherited. Both consent gates
and the `gh issue create` call stay in the main context.

## When This Skill Is Invoked

Another skill explicitly directs Claude here after encountering an issue-worthy
error. The calling skill provides:

- **Skill**: which skill encountered the error
- **Step**: which step/operation failed
- **Operation**: what was being attempted
- **Error**: full error details (exit code, message, HTTP status)
- **Suggested investigation**: skill-specific diagnostic hints

## Step 1 — Classify (main context)

Only proceed with a GitHub issue proposal for **issue-worthy** errors. Skip silently
for all others — return to the calling skill's normal error handling immediately.

**Issue-worthy** (proceed with proposal):

| Error | Examples |
|---|---|
| Prepare-tool failure | An MCP `*_prepare` tool call returns a domain/infra error |
| CLI tool failure | `gh pr create` / `gh pr edit` fails with non-auth error; `git tag` or `git push` fails |
| Persistent API error | HTTP 400/5xx on the same external API operation 2+ times in a row |
| Persistent conflict | HTTP 409 that persists after one retry |
| Escalated task failure | Task in `execute` fails after 2 retries |
| Build failure blocking execution | Build fails and blocks wave progression |

**NOT issue-worthy** (skip proposal, continue normal error handling):

| Error | Reason |
|---|---|
| Domain-validation error from a prepare tool (missing/invalid input) | User input error — missing config, wrong args |
| HTTP 401 | Auth token expired — user action needed |
| HTTP 403 | Insufficient permission — user action needed |
| HTTP 404 on issue key | User typo — not a bug |
| User cancellation | Intentional — not an error |
| Lint-only failure | Low severity, auto-fixable |
| Missing project key / config | User setup — not a bug |
| `gh auth` not logged in | User setup — not a bug |

## Step 2 — Pre-flight Verification (main context)

Run these checks before offering the proposal. If either fails, **skip the proposal
silently** and return to the calling skill's normal error handling.

**Check 1 — gh CLI available and authenticated:**

```bash
gh auth status
```

If the command fails (exit non-zero) → skip proposal.

**Check 2 — GitHub remote resolvable:**

```bash
REPO_URL=$(git remote get-url origin 2>/dev/null)
```

If `REPO_URL` is empty or the command fails → skip proposal.

The target repository for every tooling error report is the fixed value
`rnagrodzki/sdlc-plugin` — not `REPO_URL` above (that check only confirms a
remote exists; `prepare_orchestrator` (mode `"error_report"`) reports the actual target repo separately).

## Step 3 — Consent Gate 1: Offer (main context)

Use `AskUserQuestion`. This prompt MUST run in the main context (not inside the
orchestrator agent) — the user's consent is required before any further work,
including calling the prepare tool.

```
This error may be worth tracking as a GitHub issue. Create one? (yes / no)
  yes — I'll draft the issue with the full error context for your review
  no  — skip, continue with normal error handling
```

**On `no`:** Return to the calling skill's normal error handling. Do not proceed.

**On `yes`:** Continue to Step 4.

## Step 4 — Call `prepare_orchestrator` (mode: `"error_report"`) (main context)

```
prepare_orchestrator({
  mode: "error_report",
  skill: "<calling skill name>",
  step: "<step/operation that failed>",
  operation: "<what was being attempted>",
  errorText: "<full error message/output>",
  exitOrHttpCode: "<exit code or HTTP status, if any>",
  errorType: "<script crash | CLI failure | persistent API error | ...>",
  userIntent: "<what the user was doing, if known>",
  argsString: "<arguments the calling skill was invoked with, if any>",
  suggestedInvestigation: "<skill-specific diagnostic hints, if any>",
}) → { manifestPath }
```

`skill`, `step`, `operation`, and `errorText` are required; the rest may be empty
strings — the tool tolerates empty optional fields and the orchestrator omits
dependent template sections accordingly.

**On tool error:** show the error message to the user and stop. Do **not**
recursively dispatch this skill on its own prepare-tool failure.

There is no bash trap spanning this run — `manifestPath` is a plain return value
from the tool, not a shell variable. Clean it up explicitly with `rm -f
"<manifestPath>"` at every stop point below (Step 5's `no`, Step 6's `cancel`, and
Step 7's end).

## Step 5 — Dispatch the error-report-orchestrator Agent

Issue #202: pinning `model:` in skill frontmatter routes the skill into a subagent
that inherits the entire conversation transcript and overflows smaller-window
models on long sessions. To keep the main context clean and bound the
orchestrator's input to the prepared payload only, dispatch the dedicated
`error-report-orchestrator` agent.

Use the `Agent` tool with:

- `subagent_type`: `sdlc:error-report-orchestrator`
- `model`: `haiku` (the Agent tool `model:` parameter takes precedence over agent
  frontmatter; passing `haiku` here keeps this bounded task on a lightweight model
  regardless of the parent context's model)
- `prompt` (exactly two lines, no other content):

  ```text
  MANIFEST_FILE: <manifestPath>
  PROJECT_ROOT: <cwd>
  ```

  Substitute `<manifestPath>` with the path returned in Step 4. Substitute `<cwd>`
  with the current working directory.

The orchestrator reads the manifest, reads
`skills/error-report/templates/ToolingError.md`, fills every `{placeholder}`
strictly from manifest fields, removes sections whose manifest fields are empty,
determines priority (**High**: prepare-tool crash or infra error, build failure
blocking waves; **Medium**: CLI failure, persistent API error, escalated task
failure), builds the title as `[{skill-name}] {one-line error summary}` (max 72
chars), and returns ONLY a JSON object:

```json
{
  "title": "<assembled title>",
  "body": "<filled markdown body>"
}
```

The orchestrator does not call `gh`, does not call `git`, does not write any file.

Capture the returned object as `PROPOSAL = { title, body }`. If the parse fails,
`rm -f "<manifestPath>"` and stop.

## Step 6 — Consent Gate 2: Review (main context)

Display `PROPOSAL.title` and `PROPOSAL.body` to the user along with the labels
(`tooling-error` plus the calling skill's name) and the priority. Use
`AskUserQuestion` for the `yes / edit / cancel` choice.

```
Proposed GitHub Issue:
───────────────────────────────────────────
Title:    {PROPOSAL.title}
Priority: {High | Medium}
Labels:   tooling-error, {skill-name}

Description:
{PROPOSAL.body}
───────────────────────────────────────────
Create this issue? (yes / edit / cancel)
  yes    — create the issue as shown
  edit   — tell me what to change
  cancel — skip issue creation
```

**On `edit`:** Apply the requested changes to `PROPOSAL.title` and/or
`PROPOSAL.body` in the main context (do not re-dispatch the orchestrator for small
edits) and re-present. Loop until `yes` or `cancel`.

**On `cancel`:** `rm -f "<manifestPath>"`. Return to the calling skill's normal
error handling. Do not create anything.

**On `yes`:** Continue to Step 6b.

## Step 6b — Duplicate Issue Search (main context)

Search for existing open issues that may cover the same error:

```bash
CANDIDATES=$(gh issue list \
  --repo "rnagrodzki/sdlc-plugin" \
  --label "tooling-error" \
  --label "$SKILL_NAME" \
  --state open \
  --limit 10 \
  --json number,title,url)
```

Parse `CANDIDATES` JSON. Filter to issues whose title starts with `[$SKILL_NAME]`
(same prefix as `PROPOSAL.title` per Step 5 format).

**If no matches:** proceed to Step 7 silently (no prompt).

**If matches found:** present via `AskUserQuestion`:

```
Similar open issue(s) found:
  #42 — [plan] Task decomposition fails on empty specs
  #38 — [plan] Gate A dispatch timeout

Options:
  comment  — add this error as a comment to an existing issue
  new      — create a new issue, ignore matches
  cancel   — skip issue creation
```

**On `comment`:** Display a list of matched issues and ask the user to select one:

```
Which issue should we comment on?
  #42 — [plan] Task decomposition fails on empty specs
  #38 — [plan] Gate A dispatch timeout
  (cancel)
```

Once the user selects an issue number, construct the comment body:

```
Additional occurrence reported by error-report:

{PROPOSAL.body}
```

Display the comment body for user approval via `AskUserQuestion`:

```
Comment to post on #{issue-number}:
───────────────────────────────────────────
{comment-body}
───────────────────────────────────────────
Post this comment? (yes / cancel)
  yes    — post the comment as shown
  cancel — skip
```

**On `yes` (comment approval):** Post the comment and return to the calling skill:

```bash
gh issue comment <number> \
  --repo "rnagrodzki/sdlc-plugin" \
  --body "$COMMENT_BODY"
```

Report success:

```
Comment added to #<number> — <url>
```

Then `rm -f "<manifestPath>"` and return control to the calling skill's normal
error handling.

**On `cancel` (comment approval):** `rm -f "<manifestPath>"`. Return to the
calling skill's normal error handling. Do not post anything.

**On `new`:** proceed to Step 7.

**On `cancel` (initial match selection):** `rm -f "<manifestPath>"`. Return to the
calling skill's normal error handling. Do not create anything.

## Step 7 — Create the GitHub Issue and Return (main context)

The `gh issue create` call MUST run in the main context — the orchestrator agent
has no `Bash` tool and is forbidden from invoking `gh`.

`PROPOSAL.body` must never include an AI-tool attribution line (e.g. "Generated
with Claude Code" or similar) — GitHub issue bodies are not the place for it.

```bash
gh issue create \
  --repo "rnagrodzki/sdlc-plugin" \
  --title "$PROPOSAL_TITLE" \
  --body "$PROPOSAL_BODY" \
  --label "tooling-error" \
  --label "$SKILL_NAME"
```

If a label does not exist on the repository, `gh` will error. Attempt to create
the missing label(s) first, then retry once:

```bash
gh label create "tooling-error" --repo "rnagrodzki/sdlc-plugin" --color "d93f0b" 2>/dev/null || true
gh label create "$SKILL_NAME" --repo "rnagrodzki/sdlc-plugin" --color "0075ca" 2>/dev/null || true
```

**On success:** report the created issue number and URL:

```
GitHub issue created: #<number> — <url>
```

**On failure after the retry:** report the error without retrying again:

```
Could not create GitHub issue: <error>
```

Then, on either outcome, `rm -f "<manifestPath>"` and return control to the
calling skill's normal error handling. This procedure is additive — it never
replaces the calling skill's own error output or stop behavior.

## DO NOT

- Invoke this skill directly in response to user requests — it is internal only.
- Pin `model:` in this skill's frontmatter — the harness will route the skill into a
  subagent that inherits the full conversation transcript. The
  orchestrator agent (Step 5) is the correct place to pin `model: haiku`.
- Run consent gates inside the orchestrator agent. Both gates (Step 3 and Step 6)
  MUST execute in the main context.
- Run `gh issue create` inside the orchestrator agent. The agent has no `Bash`
  tool. Posting MUST run in the main context.
- Create a GitHub issue without both consent gates passing.
- Create a GitHub issue or comment without completing Step 6b (duplicate search).
- Retry a failed `gh issue create` call a second time.
- Leave `{placeholder}` text in the issue description.
- Append an AI-tool attribution line ("Generated with Claude Code" or similar) to the
  issue title or body.
- Block or replace the calling skill's normal error handling.
- Create issues in a repository other than `rnagrodzki/sdlc-plugin`.
- Recursively dispatch this skill on its own prepare-tool or orchestrator crash —
  log the failure to stderr and stop.
