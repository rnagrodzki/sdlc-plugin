# Spec Delta

## MODIFIED Requirements

### Requirement: Activity and history tabs
The Activity tab SHALL show `Open deferred (n)`, then `Learnings today (n)`, then a full-width `Preplans (n)` box, each row with its repo. The History tab SHALL show one table with outcome, kind, branch, repo, finished time, plan time, ship time, and total time. Each list SHALL have its own empty text.

#### Scenario: No history
- **WHEN** no repo in scope has history rows
- **THEN** the History tab shows `No finished runs for the selected repos.`

#### Scenario: No deferred items
- **WHEN** no repo in scope has open deferred items
- **THEN** the Activity tab shows `No open deferred items.`

#### Scenario: Ship row columns
- **WHEN** a ship row has `planDurationMs` 4800000, `durationMs` 3600000 and `totalMs` 9600000
- **THEN** the plan, ship and total cells show `1h 20m`, `1h 00m` and `2h 40m`

#### Scenario: No preplans
- **WHEN** no repo in scope has preplan topic files
- **THEN** the Activity tab shows the box `Preplans (0)` with the text `No preplans.`

## ADDED Requirements

### Requirement: Preplan topic summary
The snapshot SHALL give each `preplans` row `goal`, `live`, `kept`, `dropped`, `lastCheckAt`, `openQuestions`, `gaps`, `smallestCut` and `unread`, read from the whole file up to 256 KiB and checked with the topic-file format rules. A field that fails the check SHALL be named in `unread`.

#### Scenario: Valid topic file
- **WHEN** a topic file has 3 live rows (1 kept), 1 dropped row, `Smallest cut: #1.`, a check line at `2026-10-29T07:58:25Z`, 2 open questions and an empty `## Flows`
- **THEN** the row has `live` 3, `kept` 1, `dropped` 1, `smallestCut` `set`, `lastCheckAt` `2026-10-29T07:58:25Z`, `openQuestions` 2, `gaps` `["Flows"]` and `unread` `[]`

#### Scenario: File over the summary limit
- **WHEN** a topic file is 262145 bytes
- **THEN** `unread` holds `goal`, `proposals`, `lastCheck`, `openQuestions`, `gaps` and `smallestCut`
- **AND** `warnings` holds `Preplan <slug> summary not read: the file is 262145 bytes, the limit is 262144 bytes`

#### Scenario: Never checked
- **WHEN** a topic file has no `**Guardrail check:**` line in its first 4 KiB and no legacy `## Guardrail check` section
- **THEN** `lastCheckAt` is `""` and `unread` does not hold `lastCheck`

#### Scenario: Check line with a bad time
- **WHEN** the `**Guardrail check:**` line has no valid `Last check:` time
- **THEN** `unread` holds `lastCheck`

#### Scenario: File gone during the snapshot
- **WHEN** a topic file goes away after its head read
- **THEN** the snapshot has no row for it and no warning for it

#### Scenario: Broken Decisions table
- **WHEN** the `## Decisions` body breaks a table rule
- **THEN** `unread` holds `proposals` and `smallestCut`

#### Scenario: Code fence left open
- **WHEN** a code fence is open in `## Problem and benefit`, so `## Decisions` and `## Open questions` are hidden
- **THEN** `unread` holds `proposals`, `smallestCut`, `openQuestions` and `gaps`

#### Scenario: Empty lists
- **WHEN** a topic file has no gaps and no unread field
- **THEN** the row encodes `gaps` and `unread` as `[]`, never `null`

### Requirement: Activity Preplans box rows
The Preplans box SHALL show one read-only row for each `preplans` row of the repos in scope with topic, status, date, repo name, goal, proposal counts, check date, open question count, gaps and cut state. A field named in `unread` SHALL show its `<part> unreadable` label, never `0` or an empty cell. A row with an empty `lastCheckAt` and no `lastCheck` in `unread` SHALL show `not checked`.

#### Scenario: Never checked row
- **WHEN** a row has `lastCheckAt` `""` and `unread` `[]`
- **THEN** the row shows `not checked` in place of the check date

#### Scenario: Unread proposals
- **WHEN** a row has `unread` `["proposals"]`
- **THEN** the row shows `proposals unreadable` in place of the counts

#### Scenario: Full row
- **WHEN** a row has `live` 3, `kept` 1, `dropped` 1 and `unread` `[]`
- **THEN** the row shows `live 3 · kept 1 · dropped 1`
