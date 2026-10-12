# Spec Delta

## MODIFIED Requirements

### Requirement: Preplan topic list
The snapshot SHALL give each repo `preplans`: the newest 100 `.md` files of `.sdlc-v2/preplan/`, newest first, ties by file name, each with `slug`, `topic`, `status`, `path`, `updatedAt`, and `durationMs`. A missing folder SHALL give `preplans:[]`. `durationMs` SHALL be the file change time minus the UTC RFC 3339 time on the `**Started:**` line in the first 4 KiB of the file, and SHALL be `0` when that value is unknown or not above 0.

#### Scenario: Topic and status
- **WHEN** `.sdlc-v2/preplan/auth-flow.md` starts with `# Preplan: auth flow` and holds `**Status:** in progress`
- **THEN** `preplans` has `{"slug":"auth-flow","topic":"auth flow","status":"in progress","path":".sdlc-v2/preplan/auth-flow.md"}`

#### Scenario: No heading
- **WHEN** a topic file has no `# Preplan:` line
- **THEN** its `topic` is the file name without `.md`

#### Scenario: Over the cap
- **WHEN** the folder holds 101 `.md` files
- **THEN** `preplans` holds the newest 100
- **AND** `warnings` holds `1 older preplan topic files not shown`

#### Scenario: Duration from the Started line
- **WHEN** a topic file holds `**Started:** 2026-10-10T07:40:00Z` and its change time is `2026-10-10T09:00:00Z`
- **THEN** its `durationMs` is `4800000`

#### Scenario: No Started line
- **WHEN** a topic file has no `**Started:**` line
- **THEN** its `durationMs` is `0`
- **AND** `warnings` gets no entry for it

#### Scenario: Bad Started line
- **WHEN** a topic file `auth-flow.md` holds `**Started:** yesterday`
- **THEN** its `durationMs` is `0`
- **AND** `warnings` holds `Preplan auth-flow start time not read: "yesterday"`

#### Scenario: Start at or after the change
- **WHEN** the Started time is equal to or later than the file change time
- **THEN** its `durationMs` is `0`
- **AND** `warnings` gets no entry for it

### Requirement: Preplans tab
The Preplans tab SHALL list `preplans` of the repos in scope, newest first, with status chips (all, in progress, ready for plan, paused, unknown) with counts. A status that is not in that list SHALL show as `unknown`. Each row SHALL have a status menu with `in progress`, `ready for plan` and `paused`. A change in the menu SHALL send `POST /api/preplan-status` with no confirm question. A failed write SHALL show the message and the suggestion in a dialog, and the menu SHALL show the status of the last snapshot. The columns SHALL be status, topic, repo, updated, duration and the bin column. The repo, updated and duration headers SHALL be sort buttons: repo starts A to Z, updated and duration start with the largest value, and a second click flips the order. A row with duration `0` SHALL show `—` and SHALL be last in both duration orders.

#### Scenario: Status chip
- **WHEN** the user clicks the `paused` chip
- **THEN** only topic files with status `paused` show

#### Scenario: Chip with no rows
- **WHEN** the chosen status has no rows after a refresh
- **THEN** the chip stays active with count `0` and the tab shows `No preplans match this filter`

#### Scenario: Status menu change
- **WHEN** the user picks `paused` in the menu of a topic file
- **THEN** the page sends `POST /api/preplan-status` with the slug, the repo and `paused`

#### Scenario: Failed status write
- **WHEN** the status write answers 500
- **THEN** a dialog shows the message and the suggestion
- **AND** the menu shows the old status

#### Scenario: Repo sort
- **WHEN** the user clicks the `repo` header one time
- **THEN** the rows sort by repo name A to Z
- **AND** the header has `aria-sort` `ascending`
- **AND** the focus stays on the `repo` header after the redraw

#### Scenario: Duration sort
- **WHEN** the user clicks the `duration` header one time
- **THEN** the row with the longest duration is first
- **AND** a row with no duration is last

#### Scenario: Duration cell
- **WHEN** a row has `durationMs` `4800000`
- **THEN** its duration cell shows `1h 20m`

#### Scenario: Unknown duration cell
- **WHEN** a row has `durationMs` `0`
- **THEN** its duration cell shows `—`

### Requirement: Lone-wait card
The Pipelines feed SHALL show one card at the top for each lone wait of a repo in scope, with the kind, header, text, branch, wait age and a Dismiss button. With lone waits and no runs, the feed SHALL show the cards and no empty-feed text. The Dismiss button SHALL be in the banner bar right after the wait age. The card head SHALL show the repo name right after the branch. The head of a pipeline with a wait SHALL keep the repo name in its right group.

#### Scenario: Cards first
- **WHEN** a repo in scope has one lone wait and two runs
- **THEN** the feed shows the card first, then the runs

#### Scenario: Dismiss
- **WHEN** the user clicks Dismiss on a card
- **THEN** the page sends `POST /api/wait-dismiss` with no confirm question

#### Scenario: Failed dismiss
- **WHEN** the dismiss answers 500
- **THEN** a dialog shows the message and the suggestion
- **AND** the card stays

#### Scenario: Dismiss in the banner bar
- **WHEN** the feed shows a lone-wait card
- **THEN** the Dismiss button is the last element of the banner bar, after the wait age
- **AND** the card head has no Dismiss button

#### Scenario: Repo name after the branch
- **WHEN** the feed shows a lone-wait card
- **THEN** the repo name starts less than 40 px after the branch in the card head

#### Scenario: Pipeline with a wait
- **WHEN** a pipeline has a wait banner
- **THEN** its head keeps the repo name in the right group
- **AND** its banner bar has no Dismiss button
