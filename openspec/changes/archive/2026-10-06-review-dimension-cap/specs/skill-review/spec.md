# Spec Delta

## MODIFIED Requirements

### Requirement: Dry run
When `--dry-run` is passed the skill SHALL print the review plan from the manifest, delete `manifestPath` and `manifest.diff_dir`, and stop without dispatching any agent.

The printed plan has this shape:

```text
Review Plan (dry run — no agents dispatched)

  Base branch:    {manifest.base_branch}
  Changed files:  {manifest.git.changed_files_count}
  Dimensions:     {manifest.summary.active_dimensions} active, {manifest.summary.skipped_dimensions} skipped, {manifest.summary.queued_dimensions} queued (cap {plan_critique.dimension_cap})

| Dimension | Files | Severity | Status |
|-----------|-------|----------|--------|
{one row per entry in manifest.dimensions}

Plan critique:
  - Uncovered files:       {plan_critique.uncovered_files or "none"}
  - Over-broad:            {plan_critique.over_broad_dimensions or "none"}
  - Suggested dimensions:  {plan_critique.uncovered_suggestions[].dimension or "none"}
  - Queued (not reviewed): {plan_critique.queued_dimensions or "none"}

To execute the full review, run /review (without --dry-run).
```

#### Scenario: Dry run stops early
- **WHEN** the user runs `/review --dry-run`
- **THEN** the skill prints `Review Plan (dry run — no agents dispatched)` and the dimension table
- **AND** runs `rm -f "<manifestPath>"` and `rm -rf "{manifest.diff_dir}"`, and stops
- **AND** does not call `ledger_cleanup`, because no `runId` exists yet

#### Scenario: Dry run with queued dimensions
- **WHEN** the user runs `/review --dry-run` and `plan_critique.queued_dimensions` is `["info-a", "info-b"]` with `plan_critique.dimension_cap` `8`
- **THEN** the `Dimensions:` line ends with `2 queued (cap 8)`
- **AND** the plan critique shows `Queued (not reviewed): info-a, info-b`

#### Scenario: Dry run with no queued dimensions
- **WHEN** the user runs `/review --dry-run` and `plan_critique.queued_dimensions` is empty
- **THEN** the plan critique shows `Queued (not reviewed): none`

### Requirement: Consolidated comment file
The skill SHALL write the consolidated comment with the Write tool to `{manifest.diff_dir}/review-comment.md`.

- First line: `## Code Review — {N} dimension(s), {M} finding(s)`; `{N}` excludes dimensions skipped as stalled.
- Second block: `> Automated review by \`review\` v{plugin_version} · {date}`, with `{plugin_version}` from `manifest.plugin_version` and `{date}` as `YYYY-MM-DD`.
- When `plan_critique.queued_dimensions` is not empty, the next line is `> Queued (not reviewed, dimension cap {plan_critique.dimension_cap}): {names joined by ", "}. Raise \`maxDimensions\` in the \`[review]\` section of \`.sdlc-v2/local.toml\` to review them.` When it is empty, this line is absent.
- A `### Summary` table with columns `Dimension | Findings | Critical | High | Medium | Low | Info` and a **Total** row.
- A `### Verdict: {verdict}` heading and a one-sentence assessment. When `plan_critique.queued_dimensions` is not empty, the heading ends with ` — partial coverage: {K} dimension(s) queued, not reviewed`, where `{K}` is the number of queued dimensions. The verdict word itself does not change.
- One `### {dimension.name} — {N} finding(s)` block per dimension, with findings in a `<details>` element.
- Each finding: `#### [{SEVERITY}] {title}`, `**File:** \`{file}:{line}\``, description, `**Suggestion:**`.
- The body never includes an AI-tool attribution line.

#### Scenario: Comment persisted
- **WHEN** consolidation finishes
- **THEN** `{manifest.diff_dir}/review-comment.md` exists and starts with `## Code Review —`

#### Scenario: Queued note present
- **WHEN** `plan_critique.queued_dimensions` is `["info-a", "info-b"]` and `plan_critique.dimension_cap` is `8`
- **THEN** the comment contains `> Queued (not reviewed, dimension cap 8): info-a, info-b.`
- **AND** the first line is still `## Code Review — {N} dimension(s), {M} finding(s)`

#### Scenario: Queued note absent
- **WHEN** `plan_critique.queued_dimensions` is empty
- **THEN** the comment contains no `Queued (not reviewed` line

#### Scenario: Verdict carries a coverage caveat
- **WHEN** `plan_critique.queued_dimensions` is `["info-a", "info-b"]` and the findings give the verdict `APPROVED`
- **THEN** the verdict heading is `### Verdict: APPROVED — partial coverage: 2 dimension(s) queued, not reviewed`
- **AND** Step 8 treats the verdict as `APPROVED`

#### Scenario: Verdict without queued dimensions
- **WHEN** `plan_critique.queued_dimensions` is empty
- **THEN** the verdict heading has no `partial coverage` caveat
