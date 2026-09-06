---
name: received-review
description: "Use this skill when responding to code review feedback on a pull request or inline reviewer comments. Covers reading, verifying, evaluating, and responding to reviewer comments with a dual self-critique gate — prevents performative agreement and ensures technical rigor. Can be launched manually or automatically after /review. Triggers on: process review feedback, respond to review, handle review comments, address PR feedback, fix review findings, received-review."
user-invocable: true
argument-hint: "[--pr <number>] [--auto]"
model: opus
---

# Responding to Code Review Feedback

Process reviewer comments with technical rigor. Each item is verified against the full
codebase context — not just the change diff — before any response is drafted. Internal
self-critique gates ensure quality. No changes are made until the user explicitly approves
the proposed action plan.

**Announce at start:** "I'm using received-review (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

---

## Scope of This Port

This port does not classify review threads (outstanding/resolved/self-replied/stale); read
the raw PR view/checks output and use judgment to identify unresolved feedback. Automatic
per-thread reply/resolve is not supported.

Concretely: the `received_review_prepare` tool returns only
`{version, timestamp, pr:{number,owner,repo}, view, checks, plugin_version}` — a PR overview
(`gh pr view`) and CI status (`gh pr checks`), nothing more. There is no per-thread
`status` field, no `flags` object, no `reply_footer`, and no GraphQL review-thread ID. Every
decision this skill made in a previous version by reading a pre-computed manifest field is
now made by the model reading raw output and applying judgment instead — Steps 2–9 below
already work this way and are unaffected. Two capabilities from a previous version are not
available here and are not simulated:

- **Auto-classification of thread state** (which comments are outstanding vs. already
  resolved vs. already self-replied vs. stale). Step 1, below, always re-fetches the current
  comment list live and expects the model to judge which are already addressed.
- **Automatic thread resolution.** This skill can post replies (Step 12), but it does not
  call the GitHub GraphQL `resolveReviewThread` mutation — resolve threads manually in the
  GitHub UI after confirming the reply addresses the feedback, or ask the user to.

There is likewise no per-user configuration surface for auto-fix-by-severity or
auto-harden-by-default in this port (no tool resolves `.sdlc/local.json` for this skill).
`--auto`, described in Steps 10–12 below, is parsed directly from this invocation's own
`$ARGUMENTS` and has one meaning throughout: skip the confirmation prompt for "agree, will
fix" items. There is no severity-based partial-auto mode.

---

## Step 0 — Plan Mode Check

If the system context contains "Plan mode is active":

1. Announce: "This skill requires write operations (file edits, gh api calls). Exit plan mode first, then re-invoke `/received-review`."
2. Stop. Do not proceed to subsequent steps.

---

## Step 1 — READ: Gather Review Feedback

Parse `--auto` from this invocation's own `$ARGUMENTS` now; store it as a boolean for Steps 10–12. Parse `--pr <number>` if present.

### Step 1a — Fetch PR overview (when a PR number is available)

```
received_review_prepare({ pr: <PR_NUMBER> })
→ { version, timestamp, pr: { number, owner, repo }, view, checks, plugin_version }
```

**On success:** `view` is the plain-text output of `gh pr view <PR_NUMBER>` (title, branch,
labels, additions/deletions — no comments); `checks` is the plain-text output of
`gh pr checks <PR_NUMBER>`. Use both for overview context and to confirm the PR is the one
the user means. Report any failing checks alongside the analysis in Step 10.

**On tool error** (bad PR number, no `gh` auth, no git remote): show the error to the user.
If no PR number was supplied at all, this step is simply skipped — proceed to Step 1b for a
non-PR source of feedback.

### Step 1b — Fetch actual comments (always required — the tool above does not return them)

Fetch the reviewer feedback itself directly:

```bash
gh pr view <PR_NUMBER> --comments
gh api repos/{owner}/{repo}/pulls/{PR_NUMBER}/comments
gh api repos/{owner}/{repo}/pulls/{PR_NUMBER}/reviews
```

Use `{owner}/{repo}` from `pr.owner`/`pr.repo` in Step 1a's output, or from
`git remote get-url origin` when no PR number/tool result is available. The
`/pulls/{n}/comments` REST call returns each inline comment's `id` (needed later to reply in
Step 12) — record it alongside the parsed comment.

When no PR number is available at all, locate feedback from one of:
- Findings already in conversation context (e.g. passed in from `/review`)
- User paste

Parse each comment into a structured list:

```
| # | File | Line | Reviewer | Comment | Type | Comment ID |
```

Type classification: `bug`, `style`, `architecture`, `feature-request`, `question`, `unclear`.

Use judgment to skip comments that are clearly already addressed — a later comment on the
same thread from the PR author explaining or confirming a fix, or a comment whose referenced
line no longer differs from what it originally flagged. When in doubt, include the comment
rather than silently drop it — the cost of asking about an already-resolved comment is far
lower than the cost of silently skipping live feedback (see Scope of This Port, above).

**On persistent `gh` failure** (auth error, repo not found): show the error, ask the user to
verify `gh auth status` and PR number/access, then stop. This is a permissions/auth issue —
do not invoke `error-report` for it.

---

## Step 2 — UNDERSTAND: Categorize and Flag

For each item:
- Assign a type from the classification above
- Flag items that are **unclear** (ambiguous intent, missing context, could be interpreted multiple ways)

**CRITICAL:** If ANY item is unclear:
```
STOP — do not implement anything yet.
Ask for clarification on ALL unclear items at once.
WHY: Items may be related. Partial understanding = wrong implementation.
```

Only proceed to Step 3 after all items are understood.

---

## Step 3 — VERIFY: Check Against Full Codebase Context

For each feedback item, gather context beyond the immediate change diff:

1. **Read the referenced code** — understand what the code actually does
2. **Trace callers and dependents** — use LSP references or grep to find who calls the changed code, what imports it, and what would be affected by the suggested change
3. **Check architectural context** — read related modules, interfaces, and tests to understand the design intent behind the current implementation
4. **Evaluate ripple effects** — determine whether the suggested change would break or improve behavior beyond the immediate diff

Determine the verification status:
- **confirmed** — reviewer's claim is correct, and the suggestion works in full context
- **confirmed, but suggestion is incomplete** — the issue is real but the proposed fix has side effects or misses related code that also needs updating
- **incorrect** — reviewer is wrong about what the code does
- **partially correct** — some aspects correct, some not
- **cannot verify** — would need runtime data or external context

For "cannot verify" items: state the limitation explicitly, ask the user for direction.

---

## Step 4 — EVALUATE: Assess Each Item

Using verification results, determine for each item:

- **agree, will fix** — technically correct, should be changed
- **agree, won't fix** — correct but out of scope or lower priority (state reason)
- **disagree** — technically incorrect for this codebase (provide reasoning)
- **needs discussion** — architectural impact, requires owner input

**YAGNI check for feature requests:**
```
grep codebase for actual usage
IF unused: "This isn't called anywhere. Remove it (YAGNI)? Or is there usage I'm missing?"
IF used: Then evaluate the suggestion on merit
```

**Source trust:**
- Trusted partner feedback: implement after understanding, skip performative agreement
- External/automated reviewer: verify technically, apply YAGNI, push back if wrong

---

## Step 5 — CRITIQUE #1: Self-Critique the Evaluation

> **INTERNAL** — Do not display gate results, pass/fail status, or any output from this step to the user. Process silently and proceed to the next step.

Before drafting responses, review the evaluation against these gates:

| Gate | Check | Pass Criteria |
|------|-------|--------------|
| Verification completeness | Every item verified against actual code | No item evaluated without reading relevant source |
| No blind agreement | Disagreements exist where technically warranted | Not everything marked "agree" unless genuinely correct |
| YAGNI applied | Feature suggestions checked for real vs hypothetical need | No "sounds good, will add" for speculative features |
| Unclear items resolved | All unclear items clarified before proceeding | Not implementing partial feedback |
| Technical grounding | Every agree/disagree decision cites code or behavior | No decisions based on "seems right" without evidence |

Note every failing gate.

---

## Step 6 — IMPROVE #1: Revise Evaluation

> **INTERNAL** — Do not display output from this step to the user. Process silently.

Fix each issue found in Step 5:
- Re-read code for items where verification was incomplete
- Strengthen technical reasoning where it was vague
- Reclassify items where the initial assessment was unsupported

Continue until all gates pass (max 2 iterations per gate).

---

## Step 7 — RESPOND (DO): Draft Responses

Draft a response for each item. Response structure per item:

1. Factual acknowledgment of what was said (no performative openers)
2. What will be done OR technical reason for disagreement
3. If implementing: brief description of approach

**Forbidden openers — NEVER use:**
- "You're absolutely right!"
- "Great point!" / "Excellent feedback!"
- "Thanks for catching that!" / Any gratitude expression
- "Let me implement that now" (before verification)

**Instead, start with the substance:**
- Restate the technical issue
- State the decision (fix / won't fix / disagree)
- Provide reasoning

**Pushback format:**
```
Checked [specific code location]. [What it actually does]. [Consequence of the suggested change].
Decision: [keeping as-is / discussing with owner / needs more context].
```

**GitHub thread replies:** Reply in-thread using the comment ID from Step 1b, not as a top-level PR comment:
```bash
gh api repos/{owner}/{repo}/pulls/{pr}/comments/{comment_id}/replies \
  -f body="<response text>"
```

---

## Step 8 — CRITIQUE #2: Self-Critique the Responses

> **INTERNAL** — Do not display gate results, pass/fail status, or any output from this step to the user. Process silently and proceed to the next step.

Review drafted responses against these gates:

| Gate | Check | Pass Criteria |
|------|-------|--------------|
| No performative language | Zero forbidden openers or gratuitous praise | Responses start with substance, not social filler |
| Technically grounded | Every response references specific code, behavior, or constraint | No hand-waving ("this should be fine") |
| Pushback is technical | Disagreements cite code, performance data, or design constraints | No "I prefer" or "I think" without backing evidence |
| Thread-level replies | Each response targets its specific comment thread | No top-level dump of all responses |
| Implementation plan clear | For accepted items, response states what will change | Reviewer knows what to expect in next push |
| No blind agreement | Factual errors corrected, not accommodated | Incorrect reviewer claims are challenged |
| Proportional effort | Simple fixes get short responses; complex items get detailed ones | No walls of text for typo fixes |

Note every failing gate.

---

## Step 9 — IMPROVE #2: Revise Responses

> **INTERNAL** — Do not display output from this step to the user. Process silently.

Fix each issue found in Step 8:
- Delete performative openers, replace with substance
- Add specific code references where missing
- Shorten over-explained simple fixes

Continue until all gates pass (max 2 iterations per gate).

---

## Step 10 — PRESENT: Show Findings and Proposed Plan

This is the first user-visible output after the analysis phase. Present the complete analysis
and proposed actions to the user. **No changes have been made yet.**

**1. Analysis summary table:**

```
| # | File | Line | Type | Verdict | Reasoning |
```

Show every item with its type (bug, style, architecture, etc.) and verdict (agree will fix /
agree won't fix / disagree / needs discussion) with a one-line reasoning summary.

**2. Proposed action plan:**

Group items by action:
- **Will fix:** list items with brief description of the change
- **Will push back:** list items with the core technical reason
- **Needs discussion:** list items with what's unresolved

**3. Drafted PR responses:**

Show the full text of each drafted response, labeled by item number.

**4. Consent gate:**

**Auto mode:** When `--auto` was parsed at Step 1, skip the `AskUserQuestion` prompt below.
Still display the full analysis table and action plan above for visibility, then proceed
directly to Step 11 as if the user selected `implement` for every "agree, will fix" item.
Items with "disagree", "needs discussion", or "won't fix" verdicts are displayed but NEVER
auto-actioned, in either mode.

**Manual mode (default):** When `--auto` was not passed, use AskUserQuestion to ask:
> No changes have been made yet. How to proceed?

Options:
- **implement** — post responses to PR and apply code changes
- **edit** — modify the plan before proceeding
- **skip** — discard, make no changes

If the user chooses **edit**, ask what to change, revise, and present again.
Loop until explicit **implement** or **skip**.

**Do NOT proceed to Step 11 without explicit `implement` from the user via AskUserQuestion**,
unless `--auto` was passed at Step 1. Without `--auto`, pipeline context does NOT override
this gate — even when invoked from `/ship`, if `--auto` was not explicitly passed as a
flag to this invocation, the consent gate is mandatory. Do not infer from surrounding context
that automatic execution is expected.

---

## Step 11 — IMPLEMENT: Execute Changes

**Only execute after explicit `implement` from Step 10, OR when `--auto` was passed at Step 1 (auto-proceed for "will fix" items only).**

Post responses to PR threads, then implement accepted code changes.

**Implementation order:**
1. Blocking issues (breaks functionality, security)
2. Simple fixes (typos, imports, naming)
3. Complex fixes (refactoring, logic changes)

For each change: make the edit, verify it compiles/passes tests, then move to the next.
Do NOT batch changes across items.

**Items marked "disagree" or "needs discussion":** Do NOT implement — await reviewer or
owner input.

**Gracefully correcting wrong pushback:**
If you pushed back and were wrong:
```
Correct: "You were right — I checked [X] and it does [Y]. Implementing now."
Wrong:   Long apology, defensive explanation, over-explaining
```
State the correction factually and move on.

---

## Step 11.6 — META-ANALYZE: Cluster Findings and Dispatch harden

**Best-effort step.** Failure here MUST NOT abort Step 11.7 or Step 12.

Only cluster findings whose verdict is one of the four Step 4 outcomes (`agree-will-fix |
agree-won't-fix | disagree | needs-discussion`). Findings marked `cannot-verify` in Step 3, or
never reached that far, MUST NOT enter a cluster.

**Cluster key:** the file each finding references (from Step 1b's parsed `File` column).
`disagree` findings require ≥2 findings against the same file before forming a cluster —
a singleton `disagree` is silently skipped. Cap at 5 clusters: when more than 5 files have
qualifying findings, keep the 5 with the most findings (ties broken alphabetically by file
path) and note the rest as suppressed in the summary below.

**Consent:**

- When `--auto` was **not** passed (default): for each cluster, present:
  > Cluster: file=`<file>`, findings=`<count>`. Dispatch `harden` for this cluster?

  `AskUserQuestion: dispatch | skip`.
- When `--auto` **was** passed: skip the consent prompt, dispatch every cluster (still capped
  at 5), propagating `--auto` to each dispatch.

**Dispatch per approved cluster:**

1. Synthesize `--failure-text`: concatenate the cluster's finding comments + verification
   status + verdict, trimmed to 4096 chars.
2. Dispatch:
   ```
   Skill("harden",
     "--failure-text \"<synthesized cluster text>\"
      --skill received-review
      --step \"Step 11.6 — meta-analysis\"
      --operation \"review-feedback-driven hardening\"
      [--auto when --auto was passed]"
   )
   ```
3. On dispatch failure: note it in the Step 12 summary (`harden dispatch failed — file=<file>`)
   and continue to the next cluster. Do NOT abort Step 11.7 or Step 12.

---

## Step 11.7 — LINK VERIFICATION (issue #198) — HARD GATE

Before any `gh api` reply is posted, validate every URL embedded in every drafted reply body.

1. Concatenate all reply bodies (one per line) and write them with the `Write` tool to
   `.sdlc/state/artifacts/received-review-reply-bodies.md` (overwrite each run — this is a
   scratch working file, not a durable record).
2. Validate:
   ```
   links_validate({ file: ".sdlc/state/artifacts/received-review-reply-bodies.md", offline: false })
   → { results: [{ url, line, status, reason, detail }] }
   ```
3. If any `results[]` entry has `status !== "ok"`:
   - Do NOT post any replies. Do NOT proceed to Step 12.
   - Surface the violation list (url, line, reason, detail) verbatim to the user.
   - Stop. Do not retry. Do not edit URLs without user input. Do not bypass.

On all-clear (every result `ok` or `skipped`), proceed to Step 12. Pass `offline: true`
instead of `false` to skip network reachability while keeping context-aware checks (GitHub
identity match, Atlassian host match) — use in sandboxed CI.

## Step 12 — REPLY: Post PR Thread Replies

**Mandatory step — always presented after Step 11 completes.**

1. **Summarize** what was done:

```
Review feedback processing complete:
- N comments addressed (code changes implemented)
- M comments pushed back (with technical reasoning)
- K comments intentionally skipped (agree, won't fix)
```

2. **Consent gate:**

**Auto mode:** When `--auto` was passed at Step 1, skip the `AskUserQuestion` consent gate
below. Still display the summary block above for visibility, then proceed directly to step 3
below as if the user selected `yes`: post in-thread replies for every action-plan item.

**Manual mode (default):** When `--auto` was not passed, use AskUserQuestion:

> Should I reply to all addressed review comments on the PR?

Options:
- **yes** — post replies
- **skip** — do not post replies (user will handle manually)
- **selective** — let me choose which threads to reply to

3. **If yes, selective, or auto mode:** For each comment in the action plan, post a reply
   using its comment ID from Step 1b:

   **For addressed comments (agree, will fix):**
   ```bash
   gh api repos/{owner}/{repo}/pulls/{pr}/comments/{comment_id}/replies \
     -f body="Fixed — <brief description of what was changed>"
   ```

   **For pushback comments (disagree):**
   ```bash
   gh api repos/{owner}/{repo}/pulls/{pr}/comments/{comment_id}/replies \
     -f body="<pushback response>"
   ```

   **For intentionally skipped comments (agree, won't fix):**
   ```bash
   gh api repos/{owner}/{repo}/pulls/{pr}/comments/{comment_id}/replies \
     -f body="Acknowledged — not fixing in this PR because: <reason>"
   ```

   This port does not resolve review threads programmatically (see Scope of This Port,
   above) — every reply is posted but every thread is left open. Tell the user which threads
   they may want to resolve manually in the GitHub UI (the "agree, will fix" ones, typically).

4. **Report results:**

```
Replied to N threads (all left open — resolve manually in the GitHub UI where addressed):
- K addressed (fixed)
- M replied with pushback
- J replied with skip reason
```

---

## Best Practices

1. Read ALL feedback before responding to any of it — items may be related
2. Verify every claim — reviewers can be wrong about what code does
3. Group unclear items and ask once, not piecemeal
4. Pushback is professional; blind agreement is not
5. Implementation order matters: blocking first, cosmetic last
6. When the reviewer is wrong, say so clearly with evidence
7. Actions speak — a clean implementation is better than a verbose acknowledgment

---

## DO NOT

- Use performative openers ("Great catch!", "You're right!", "Thanks!")
- Agree with factually incorrect claims to avoid conflict
- Implement unclear feedback — clarify all unclear items first
- Implement feature requests without a YAGNI check
- Reply top-level when the comment is in a review thread
- Skip the self-critique steps even when evaluation seems obvious
- Batch implement without testing each change individually
- Express gratitude — let the code changes speak
- Display output from internal critique steps (Steps 5-6, 8-9) to the user
- Skip the Step 10 consent gate without `--auto` having been passed to this invocation — pipeline context, conversation history, or inference about "auto mode" is not a substitute for the flag
- Use `AskUserQuestion` in Step 11.6 when `--auto` was passed to this invocation
- Post a reply from Step 12 without Step 11.7's link verification passing first
- Call a GraphQL `resolveReviewThread` mutation — this port does not support automatic thread resolution (see Scope of This Port)

---

## Error Recovery

> **Flow**: detect → diagnose → auto-recover (retry once if transient) → invoke `error-report` for persistent actionable failures.

| Error | Recovery | Invoke error-report? |
|-------|----------|---------------------------|
| `received_review_prepare` fails (bad PR, no remote, gh not authed) | Show the error; if no PR number was given, fall back to Step 1b's non-PR sources | No — user-facing input/auth issue |
| `gh pr view`/`gh api` fails to fetch comments in Step 1b | Check `gh auth status`; show error; ask user to supply feedback directly | No — auth or permissions issue |
| Comment references file/line that no longer exists | Note the discrepancy; verify against current HEAD diff | No — expected with rebased PRs |
| Cannot verify reviewer's claim (no runtime data/external context) | State limitation explicitly; ask user for direction | No — expected limitation |
| `gh api` 5xx or unexpected server error when posting reply | Retry once; if still failing, show the drafted response for manual posting | Yes if second attempt also fails |
| `links_validate` reports a violation | Surface the violation list; do not post; do not retry without user input | No — expected hard gate behavior |

When invoking `error-report`, provide:
- **Skill**: received-review
- **Step**: Step 11 — IMPLEMENT (posting GitHub thread replies, only after user consent in Step 10)
- **Operation**: `gh api` call to post comment reply
- **Error**: HTTP status + error message from above
- **Suggested investigation**: Check `gh auth status`; verify PR number is correct and accessible; confirm repo permissions

---

## Gotchas

- **Contradictory comments across threads:** When reviewer leaves contradictory feedback in
  different threads, flag the contradiction and ask for clarification rather than guessing
  which one they meant.
- **Comments on deleted lines:** May reference code that no longer exists in the current
  revision. Verify against current HEAD, not the diff context shown in the review.
- **Automated review tools:** Findings from `/review` or similar automated tools should
  be treated as external reviewer feedback — verify each finding against actual code before
  accepting it.
- **Re-running after a partial session:** This port does not track which comments already
  received a reply. Before drafting a response in Step 7, check whether a reply with
  materially the same content is already present on that comment's thread (from Step 1b's
  fetched data) — skip drafting a duplicate.
- **REST comment ID is the only ID this port uses.** Replies are posted via the REST
  `comments/{comment_id}/replies` endpoint using the `id` field from
  `gh api repos/{owner}/{repo}/pulls/{pr}/comments`. There is no GraphQL thread ID lookup in
  this port — thread resolution is manual (see Scope of This Port).
- **Auto mode scope:** `--auto` only auto-implements "will fix" items. "Disagree", "needs
  discussion", and "won't fix" items are always displayed and never auto-actioned. This
  prevents automated tools from silently suppressing pushback.

---

## Learning Capture

After processing review feedback, append discoveries to `.sdlc/learnings/log.md`. Record
entries for: reviewer patterns worth knowing (e.g., they always flag X style), pushback
outcomes (accepted or rejected — to calibrate future responses), unclear feedback patterns
that revealed communication gaps, YAGNI findings that removed unnecessary work, or codebase
facts uncovered during verification.

---

## What's Next

After replying to review threads, common follow-ups include:
- `/commit` — commit the fixes

## See Also

- [`/review`](../review/SKILL.md) — source of findings this skill responds to
- [`/commit`](../commit/SKILL.md) — commit the fixes after review
- [`/pr`](../pr/SKILL.md) — the PR being reviewed
