---
name: review
description: "Use this skill when reviewing code changes across project-defined dimensions (security, performance, docs, concurrency, etc.). Runs the review_prepare tool to pre-compute all git data, then dispatches one background agent per dimension directly from the main session, coordinated via a file-based ledger (no orchestrator agent). Arguments: [--base <branch>] [--dry-run]. Triggers on: review changes, code review, review PR, multi-dimension review, run review."
user-invocable: true
argument-hint: "[--base <branch>] [--dry-run]"
model: sonnet
---

# Reviewing Changes

Runs `review_prepare`, then dispatches one background agent per review dimension directly
from this session — no orchestrator agent is spawned. Dimensions coordinate through a
file-based ledger (`execute_state`'s `ledger_checkin`/`ledger_checkout`/`ledger_status`
actions); this session polls the ledger, then runs the same dedupe/critique pass an
orchestrator agent used to run, now inline.

**Announce at start:** "I'm using review (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

---

## Step 0 — Run `review_prepare`

Parse `$ARGUMENTS`:

- `--base <branch>` → forwarded as `target`.
- `--dry-run` → handled entirely by Step 1 below; not forwarded to the tool.

**Scope note:** `review_prepare`'s only inputs are `target` and `skipConfigCheck` — scope
(`all` / `committed` / `staged` / `working` / `worktree`) is read by the tool from
`.sdlc-v2/config.json`'s `review.scope` (default `all`), not from a CLI flag. This port does
not expose `--committed` / `--staged` / `--working` / `--worktree` / `--set-default` /
`--dimensions` flags; change scope by editing project config (`/setup`) instead.

```
review_prepare({ target: "<branch from --base, or empty>", skipConfigCheck: false })
→ { manifestPath, summary: {
      total_dimensions, active_dimensions, skipped_dimensions, queued_dimensions,
      total_changed_files, uncovered_file_count, suggested_dimensions
    } }
```

**On tool error:** show the error message to the user and stop. Delete `manifestPath` first
if the tool reported one before failing.

**Read `manifestPath` into the main context.** Unlike the retired orchestrator-based model,
there is no separate agent to shield this session from the manifest — the manifest is a
**thin index** (R-manifest-index-slices, #447): each `dimensions[]` entry carries only
`name, description, severity, model, status, requires_full_diff, truncated, matched_count,
diff_file, slice_file` (exactly `reviewDimIndexEntry`'s JSON tags), plus root-level
`subagent_model`. **Do NOT read the contents of any `slice_file` or `diff_file` referenced
inside it** — those paths are forwarded to each dispatched worker in Step 2, and the worker
reads them itself. This session's context must scale with dimension count, not diff content.

**No bash trap spans this skill run.** `manifestPath` is a plain return value from an MCP
tool call, not a subshell result — there is nothing to attach a `trap` to. Delete it
explicitly with `rm -f "<manifestPath>"` at every stop point: the dry-run stop below (Step
1), any error stop, and Step 9 (Cleanup) on normal completion.

---

## Step 1 — Dry Run Check

Only if `--dry-run` was passed in `$ARGUMENTS`:

Using the manifest already read in Step 0, output **exactly** this format:

```
Review Plan (dry run — no agents dispatched)

  Base branch:    {manifest.base_branch}
  Changed files:  {manifest.git.changed_files_count}
  Dimensions:     {manifest.summary.active_dimensions} active, {manifest.summary.skipped_dimensions} skipped

| Dimension | Files | Severity | Status |
|-----------|-------|----------|--------|
{one row per entry in manifest.dimensions}

Plan critique:
  - Uncovered files:       {manifest.plan_critique.uncovered_files.join(', ') or "none"}
  - Over-broad:            {manifest.plan_critique.over_broad_dimensions.join(', ') or "none"}
  - Suggested dimensions:  {manifest.plan_critique.uncovered_suggestions.map(s => s.dimension).join(', ') or "none"}

To execute the full review, run /review (without --dry-run).
```

`rm -f "<manifestPath>"`. Stop here.

---

## Step 2 — Flat Dispatch

For each dimension entry with `status: "ACTIVE"` or `status: "TRUNCATED"`:

1. **Mint a `runId` for this review run** — this run's ledger namespace:

   ```
   runId := "review-" + sanitize(manifest.timestamp)
   ```

   `sanitize` replaces every character outside `[A-Za-z0-9_-]` with `-` (the manifest
   timestamp is RFC3339 and contains `:` characters, which are unsafe for a ledger path
   segment).

2. **Derive this dimension's `workerId`:**

   ```
   workerId := slugify(dimension.name)
   ```

   `slugify` lowercases the name, then collapses every run of one-or-more characters
   outside `[A-Za-z0-9_-]` to a single `-`.

3. **Do NOT read `dimension.slice_file` or `dimension.diff_file`.** The dispatched agent
   reads them itself (R-manifest-index-slices, #447) — reading them here would relocate the
   context overflow into this session, exactly what the thin-index design avoids.

4. Build the agent prompt by filling the template below with the thin-index fields
   (`{dimension.name}`, `{dimension.description}`, `{dimension.severity}`) and the
   `slice_file` / `diff_file` **paths** (not contents):

   ```
   # Code Review: {dimension.name}

   ## Your Role
   You are a code reviewer focused exclusively on: {dimension.description}

   ## Your Input Files
   You MUST read both of these files before reviewing:

   - **Slice file** (JSON): {dimension.slice_file}
     Read it and parse the JSON. It contains:
     - `body` — your full review instructions (Markdown)
     - `matched_files` — the list of changed files in your scope (review ONLY these)
     - `file_context` — array of {file, commits: [{hash, subject}]} for author-intent context
     - `warnings` — any prepare-time warnings (e.g., matched files with no diff content)
   - **Diff file**: {dimension.diff_file}
     Read it for the pre-filtered diff hunks covering your matched files.

   ## How to Use Them
   1. Read({dimension.slice_file}) and parse the JSON.
   2. Treat the parsed `body` as your Review Instructions — follow it verbatim.
   3. Review ONLY the files in `matched_files`.
   4. For each `file_context` entry whose `commits` array is non-empty, use the commit
      hashes/subjects to understand the author's intent.
   5. Read({dimension.diff_file}) for the diff to review.
   6. Surface any `warnings` entries that affect your review.

   ## Default Severity
   Unless the review instructions specify otherwise, classify findings as: {dimension.severity}

   ## Coordination (do this, in this order)
   1. Call execute_state({ action: "ledger_checkin", runId: "{runId}", workerId: "{workerId}" })
      BEFORE reading your slice/diff files.
   2. Review per the instructions above. Cap at 20 findings (prioritize by severity).
   3. Write your findings to the file
      ".sdlc-v2/execution/ledger/{runId}/{workerId}.findings.json" as a raw JSON array of
      objects shaped {severity, file, line, rationale} — write `[]` if you have zero
      findings. Do this BEFORE the next step.
   4. Call execute_state({ action: "ledger_checkout", runId: "{runId}", workerId: "{workerId}" })
      LAST, even when your findings file is an empty array.

   ## Constraints
   - Review ONLY the files listed above — do not read other files
   - Review ONLY for the concerns described in the review instructions — stay in scope
   - Do NOT fabricate issues
   - Each finding must reference a specific file and line number
   ```

   `requires_full_diff` scoping: when `dimension.requires_full_diff` is `false`, the
   `diff_file` already contains only the matched files' hunks; when `true`, it contains the
   complete diff for those files. No extra prompt wording is needed either way — the
   prepared file already reflects the right scope.

5. Dispatch one Agent per ACTIVE/TRUNCATED dimension, **all in a single message**, with
   **`run_in_background: true`** — the inversion of the previous mandatory `false`. Use
   `model: dimension.model || manifest.subagent_model` per dimension (per-dimension override
   wins; forward the string verbatim, no whitelist).

**Workflow variant:** Prefer the Workflow tool's native fan-out when available; otherwise
use the flat background-dispatch + ledger path described above.

---

## Step 3 — Poll Ledger Status

Loop calling:

```
execute_state({ action: "ledger_status", runId, timeoutSeconds: 1800 })
→ { runId, workers: [{ workerId, status, checkinAt, checkoutAt, stalled, stepId? }], stalledWorkers: [...] }
```

roughly every 60 seconds (a pacing suggestion for this session's own polling cadence, not a
tool parameter) until every `workerId` dispatched in Step 2 shows `status: "done"` in
`workers[]`.

**Stall handling (fail-partial-open, disclosed):**

- If a `workerId` appears in `stalledWorkers`, do not treat it as failed yet — wait one more
  poll cycle.
- If it is **still** present in `stalledWorkers` on the next poll, stop waiting on that
  worker: proceed to Step 4 with the results collected so far, and explicitly note the
  skipped dimension(s) by name in the final `review-comment.md` (Step 5) — this is a
  disclosed degraded mode, not a silent drop.

---

## Step 4 — Consolidate Findings (relocated critique/dedupe pass)

Once every dispatched worker is `done` (or was force-progressed past a stall per Step 3),
read each worker's findings file at
`.sdlc-v2/execution/ledger/<runId>/<workerId>.findings.json`. This is the same dedupe/
contradiction/severity-recalibration pass previously run by a separate orchestration step,
now inline in this session:

**Critique:**

- **Duplicates**: same `file:line` flagged by multiple dimensions?
- **Contradictions**: conflicting recommendations at the same `file:line`?
- **Zero findings credibility**: a dimension returned no findings — does its diff actually
  contain potential issues for that dimension's concern?
- **Severity calibration**: any finding with a wrong severity (e.g., `info` for credential
  exposure, or `critical` for a minor style note)?

**Improve:**

- **Deduplicate**: when the same `file:line` appears in multiple dimensions' findings, keep
  the entry from the highest-severity dimension and add `Also flagged by: {other-dimension}`.
- **Contradictions**: keep both findings; add `Note: conflicting recommendations — manual
  review required.`
- **Re-calibrate** any miscalibrated severities found above.

---

## Step 5 — Build and Persist Consolidated Comment

Format the comment using this template:

```markdown
## Code Review — {N} dimension(s), {M} finding(s)

> Automated review by `review` v{plugin_version} · {date}

### Summary

| Dimension | Findings | Critical | High | Medium | Low | Info |
|-----------|----------|----------|------|--------|-----|------|
| {name} | {total} | {critical} | {high} | {medium} | {low} | {info} |
| **Total** | **{total}** | **{critical}** | **{high}** | **{medium}** | **{low}** | **{info}** |

### Verdict: {CHANGES REQUESTED | APPROVED WITH NOTES | APPROVED}

{One-sentence overall assessment}

---

### {dimension.name} — {N} finding(s)

<details>
<summary>{critical} critical · {high} high · {medium} medium · {low} low · {info} info</summary>

#### [{SEVERITY}] {title}
**File:** `{file}:{line}`{if OWASP set: ` · OWASP {OWASP}`}
{description}
**Suggestion:** {suggestion}

</details>

---
```

Repeat the `### {dimension.name}` block once per consolidated dimension. If a dimension was
skipped as stalled (Step 3), add one more such block naming it with the note "Skipped —
worker stalled twice; no findings collected" in place of its finding list.

**Template variable substitution:**

- `{date}` ← today's date in `YYYY-MM-DD` format
- `{plugin_version}` ← `manifest.plugin_version` (verbatim, no extra `v` prefix)
- `{N}` ← number of active dimensions actually consolidated (excluding any skipped-as-stalled
  per Step 3)
- `{M}` ← total finding count
- All other `{...}` placeholders ← computed from the findings gathered in Step 4
- If any dimension was skipped as stalled (Step 3), add an explicit note naming it and
  stating its findings are absent from this review

**Compute verdict:**

- `CHANGES REQUESTED` — any `critical` finding, OR ≥ 3 `high` findings
- `APPROVED WITH NOTES` — any `high` finding, OR ≥ 5 `medium` findings
- `APPROVED` — all other cases

**Persist** the comment body to `{manifest.diff_dir}/review-comment.md` using the `Write`
tool (content verbatim, no surrounding fences, no shell escaping).

---

## Step 6 — Display Full Comment Body

This step implements R13 and quality gate G5: the user MUST see the full consolidated
review (every per-dimension finding, every severity) in the terminal before any posting
prompt.

Use the Read tool to load `{manifest.diff_dir}/review-comment.md`, then emit its full
contents verbatim to the user inside a fenced markdown block. No summarization, no
truncation, no per-severity collapsed table, no "Additional finding (see PR comment for
details)" placeholders.

### DO NOT (Step 6 display)

- Do NOT summarize or paraphrase findings — emit the file contents byte-for-byte
- Do NOT collapse any finding to a placeholder like
  "Additional finding (see PR comment for details)"
- Do NOT synthesize a severity/count table in place of the persisted body
- Do NOT skip the Read step

Do NOT delete `manifestPath` or the ledger directory here — cleanup happens in Step 9 on
every terminal branch.

---

## Step 7 — Handle Posting

This step runs entirely in the main context. The comment body at
`{manifest.diff_dir}/review-comment.md` is authoritative.

### PR exists (`manifest.pr.exists == true`)

Prompt in the main context:

```text
Post this review comment to PR #{manifest.pr.number}? (yes / save / cancel)
  yes    — post the comment to the PR
  save   — save review to .sdlc-v2/reviews/<branch>-<YYYY-MM-DD>.md instead
  cancel — keep in terminal only (already shown above)
```

Wait for the user's reply.

- `yes` → **link verification (R14, issue #198) — HARD GATE.** Before posting, validate
  every URL embedded in the consolidated review comment body:

  ```
  links_validate({ file: "{manifest.diff_dir}/review-comment.md", offline: false })
  → { results: [{ url, line, status, reason, detail }] }
  ```

  If any `results[]` entry has a non-`ok` `status`, do NOT post. Surface the violation list
  verbatim to the user. Stop. Do not retry. Do not edit URLs without user input. Do not
  bypass.

  On all-clear, post the comment via `gh api` using the file body form (safe for large
  markdown, backticks, quotes):

  ```bash
  gh api repos/{manifest.pr.owner}/{manifest.pr.repo}/issues/{manifest.pr.number}/comments -F body=@{manifest.diff_dir}/review-comment.md
  ```

  Pass `offline: true` to `links_validate` (replacing the old `SDLC_LINKS_OFFLINE=1` env
  var) to skip network reachability while keeping context-aware checks — use in sandboxed CI.

- `save` → (implements `R-reviews-path` — canonical save target is `.sdlc-v2/reviews/`)

  ```bash
  BRANCH_SAFE="${branch//[^a-zA-Z0-9_-]/-}"
  mkdir -p .sdlc-v2/reviews
  cp "{manifest.diff_dir}/review-comment.md" ".sdlc-v2/reviews/${BRANCH_SAFE}-$(date +%Y-%m-%d).md"
  ```

- `cancel` → no action. The comment is already visible in the terminal from Step 6.

### No PR, branch scope (`manifest.scope` is `all`, `committed`, or `worktree`)

Prompt:

```text
No PR found. Options:
  1. Create a draft PR and attach this review as a comment
  2. Save review to .sdlc-v2/reviews/<branch>-<YYYY-MM-DD>.md
  3. Keep in terminal only
```

- Option 1 → invoke `pr` from the main context in draft mode, wait for PR creation,
  then post via the `gh api … -F body=@...` command above using the newly created PR's
  owner/repo/number.
- Option 2 → same `save` command as above.
- Option 3 → no action.

### No PR, local scope (`manifest.scope` is `staged` or `working`)

Prompt:

```text
Reviewing local changes — no PR to post to. Options:
  1. Save review to .sdlc-v2/reviews/<branch>-<YYYY-MM-DD>.md
  2. Keep in terminal only
```

- Option 1 → `save` command above.
- Option 2 → no action.

---

## Step 8 — Offer Self-Fix

If the verdict is **CHANGES REQUESTED** or **APPROVED WITH NOTES**, offer to fix:

> The review found actionable items. Address them now?

- **fix** — invoke `received-review` (findings are in conversation context)
- **harden** — run `/harden` to analyze why this failed and propose stronger guardrails
  / dimensions / instructions that would catch it earlier next time. Opt-in — no surface is
  edited without your approval. (Offered only when verdict is **CHANGES REQUESTED** with at
  least one dimension blocker; suppressed when `--auto` is set.)
- **no** — done

When the user selects **harden**, dispatch `Skill(harden)` with
`--failure-text "Review verdict CHANGES REQUESTED — dimension blocker(s): <dimension-list>"`,
`--skill review`, `--step "Step 8 — actionable findings"`, `--operation "self-fix
offer"`. Implements R16.

If verdict is **APPROVED**: skip — nothing to fix.

---

## Step 9 — Cleanup

Remove everything this run created, on every terminal path (dry-run stop, error stop, and
normal completion):

```bash
rm -f "<manifestPath>"
rm -rf "{manifest.diff_dir}"
rm -rf ".sdlc-v2/execution/ledger/<runId>"
```

---

## DO NOT

- Do NOT read a dimension's `slice_file` or `diff_file` contents into this session — the
  dispatched agent reads them (Step 2)
- Do NOT invent a `findings` field on any `execute_state` ledger action — none of
  `ledger_checkin` / `ledger_checkout` / `ledger_status` carries a findings/result payload;
  findings travel only through the sibling `<workerId>.findings.json` file (Step 2)
- Do NOT invoke error-report for user errors — only for tool-call crashes
- Do NOT skip the Step 3 poll and consolidate on partial or zero results without a worker
  actually confirmed stalled twice in a row

## See Also

- [`/setup --dimensions`](../setup/SKILL.md) — creates review dimensions
- [`/received-review`](../received-review/SKILL.md) — responds to findings
- [`/commit`](../commit/SKILL.md) — commit after review approval
