# Review results

Read this page when you have a review comment in front of you. It explains the comment, the verdict, the posting options and what a run leaves behind. For the value of the skill and its steps, see the [overview](../review.md).

## The review comment

The skill builds one comment for the full review. It shows all of the comment in the terminal before it asks a question: no summary, no cut, no placeholder for a finding. The comment has this shape:

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

How to read it:

- `{N}` in the title counts the dimensions that gave a result. A skipped dimension is not in the count.
- `{M}` is the total count of findings.
- `{date}` is the date of the review, as `YYYY-MM-DD`.
- The `### {dimension.name}` block repeats for each dimension.

Before the skill builds the comment, it checks the findings of all reviewers together:

| Check | Result in the comment |
|---|---|
| Two dimensions flag the same file and line | The comment keeps the finding of the dimension with the highest severity and adds `Also flagged by: {other-dimension}` |
| Two findings at the same file and line give conflicting advice | The comment keeps both and adds `Note: conflicting recommendations — manual review required.` |
| A finding has a wrong severity, for example `info` for a credential exposure | The skill corrects the severity |
| A dimension has zero findings | The skill asks if the diff of that dimension holds possible problems for its concern |

The skill saves the comment as `review-comment.md` in the temp directory of the run. The comment never holds an AI-tool attribution line.

## Verdict

The verdict comes from the counts of findings at each severity.

| Verdict | When |
|---|---|
| `CHANGES REQUESTED` | One or more `critical` findings, or 3 or more `high` findings |
| `APPROVED WITH NOTES` | One or more `high` findings, or 5 or more `medium` findings |
| `APPROVED` | All other cases |

The verdict heading holds only the verdict word, also when the review ran in more than one wave.

## Skipped dimensions

The skill checks the reviewers of a wave about one time each minute. It stops a reviewer in two cases:

- The reviewer is stalled on two checks in a row.
- The reviewer never checked in, on two checks in a row.

The comment then has a block for that dimension with one of these notes in place of the findings:

- `Skipped — worker stalled twice; no findings collected`
- `Skipped — worker never checked in; no findings collected`

The comment also states that the findings of that dimension are absent from the review. The skill ignores the findings of a stopped reviewer, also when that reviewer finishes later. The other reviewers of the wave continue.

**A reviewer that cannot be stopped.** When `/sdlc:ship` starts the review, the stop can fail with an ownership or authorization error. The skill does not try the stop again. The note of that dimension then adds `possibly still running; a later ledger entry for it was not consolidated`. More than `maxParallelDimensions` agents can then run at the same time, and the final output says so.

**On the dashboard.** The skill records the reason of each stop: `stalled`, `missing`, or `unstopped` when the stop failed or the reviewer has no task ID. The [dashboard](../dashboard.md) shows that dimension as skipped with the reason, until the reviewer checks out.

## Where the review goes

After the full comment, the skill asks where the review goes. The options depend on the PR and on the [scope](./settings.md#scope).

| Situation | Options |
|---|---|
| The branch has an open PR, and the scope is `all` or `committed` | `yes`: post the comment to the PR. `save`: save the review to a file. `cancel`: keep the review in the terminal only |
| No PR, and the scope is `all` or `committed` | 1: create a draft PR and attach the review as a comment. 2: save the review to a file. 3: keep the review in the terminal only |
| The scope is `staged`, `working` or `worktree` | 1: save the review to a file. 2: keep the review in the terminal only |

The saved file is `.sdlc-v2/reviews/<branch>-<YYYY-MM-DD>.md`.

**Link check.** Before each post, the skill checks every URL in the comment. When a link fails the check, the skill does not post. It shows the list of bad links and stops. It does not try again, and it does not change a URL without your input. For the draft PR option, the check runs before the PR is created, so a bad link leaves no draft PR without its review.

**PR lookup warning.** When `gh` cannot check for a PR, the skill shows a warning at the start of the run. It then shows the options for no PR, also when a PR can exist.

## Fix the findings

When the verdict is `CHANGES REQUESTED` or `APPROVED WITH NOTES`, the skill asks if you want to address the findings now.

| Answer | Result |
|---|---|
| `fix` | Starts [/sdlc:received-review](../received-review.md) with the findings of this review |
| `harden` | Starts [/sdlc:harden](../harden.md). It analyzes why the review failed and proposes stronger guardrails, dimensions or instructions. It edits nothing without your approval. The skill offers this answer only when the verdict is `CHANGES REQUESTED` with one or more dimension blockers |
| `no` | Ends the run |

When the verdict is `APPROVED`, the skill does not ask.

**Severity levels drive pipelines.** In [/sdlc:ship](../ship.md), findings at or above the configured threshold start an automatic fix loop. Findings with a lower severity do not block. They are saved for [/sdlc:deferred](../deferred.md), not dropped.

## What stays after a run

**Temp files.** On each end of a run, the skill stops the reviewers that did not finish. Then it deletes its manifest file and its temp diff directory. This applies to a dry run, an error stop and a normal end. The skill keeps the temp directory only when a reviewer could not be stopped, because that reviewer can still read its files. The final output then names the kept path. The directory is under the system temp directory, so the operating system removes it later.

**The run ledger.** Each run records its reviewers in a ledger under `.sdlc-v2/runs/ledger/`. A dry run writes no ledger. The skill does not delete the ledger. The [dashboard](../dashboard.md) reads the ledger to show the review dimensions of a finished run. The first `execute_state` `gc` sweep or `/sdlc:ship` `cleanup-pipeline` sweep after 7 days (the default `state.gc.ttlDays`) removes the ledger.

**An interrupted review.** A review that stops does not resume. Run it again. The new run starts at Step 0 with a new manifest and a new `run_id`, and does not read the ledger of the old run.

- The old ledger folder shows as `stalled` on the dashboard after 30 minutes with no change. It stays until the sweep removes it.
- Reviewers of the old run can still be active when the session lost their task IDs, for example in a new session. The skill tells you that up to `maxParallelDimensions` old reviewers can still run. Each one ends after its own review and writes only to the ledger of the old run.
