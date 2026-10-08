# Spec Delta

## MODIFIED Requirements

### Requirement: Step status by run kind
The snapshot SHALL take the status of one step from the run kind by this table.

| Kind | One step is | Status rule |
|---|---|---|
| ship | a ship step | the stored value |
| execute | a wave | the stored value, with `partial` shown as `failed` |
| plan | a station that groups checkpoint steps | before, at, after the station of the current step: `completed`, `in_progress`, `pending`. With the `done` marker: all `completed` |
| review | a dimension | `completed` when `checkoutAt` is set, else `in_progress` |

#### Scenario: Partial wave
- **WHEN** an execute wave has status `partial`
- **THEN** the step status is `failed`

#### Scenario: Plan checkpoint
- **WHEN** a plan run is at checkpoint step `5`
- **THEN** stations `setup`, `explore`, and `draft` are `completed`
- **AND** station `review` is `in_progress` and station `finalize` is `pending`

### Requirement: Pipeline issues
The snapshot SHALL list one issue for each failed ship step, each failed or partial execute wave, each finding of a review dimension file, each failed task row with an error, each state issue not matched by these, and each stalled run.

| Match | `source` | `severity` | `text` | `ref` |
|---|---|---|---|---|
| ship step `failed` | `step` | `high` | the step `error` or `reason` | step name |
| execute wave `failed` or `partial` | `wave` | `high` | wave status | `wave <n>` |
| review finding | `review` | the finding `severity` | the rationale | dimension name |
| failed task row with `error` | `task` | `high` | the error | task id |
| state `issues[]` entry with no matching issue above | `state` | the mapped severity | summary and detail | task id, else step, else `wave <n>` |
| stalled run | `pipeline` | `medium` | `no update for 30+ min` | empty |

Each issue also has `file` and `line`. They are empty except for a review finding. `line` is text, for example `42` or `12-14`.

#### Scenario: Failed ship step
- **WHEN** the ship step `pr` is `failed` with error `gh auth failed`
- **THEN** the pipeline has the issue `{source:"step", severity:"high", text:"gh auth failed", file:"", line:"", ref:"pr"}`

#### Scenario: Review finding
- **WHEN** a review dimension file `code` holds a finding with severity `medium`, file `a.go`, line `12`, rationale `nil map`
- **THEN** the pipeline has the issue `{source:"review", severity:"medium", text:"nil map", file:"a.go", line:"12", ref:"code"}`

#### Scenario: State issue of a failed step
- **WHEN** a ship state has a `ship-fail` state issue for step `pr`
- **AND** the pipeline has a `step` issue with `ref:"pr"`
- **THEN** the state issue is not listed

#### Scenario: Other state issue
- **WHEN** an execute state has a state issue with category `drift` for wave `2`
- **THEN** the pipeline has one issue with `source:"state"` and `ref:"wave 2"`

#### Scenario: Stalled run issue
- **WHEN** a run has the status `stalled`
- **THEN** the pipeline has one issue with `source:"pipeline"`, `severity:"medium"`, and `text:"no update for 30+ min"`

### Requirement: Page behavior
The page SHALL show the pipelines of all repos as one feed of blocks, SHALL remember the chosen tab and the repo filter across reloads, SHALL show each block in its default state after a reload (unfinished blocks open, finished blocks collapsed), and SHALL insert all snapshot text as plain text, never as HTML.

| State | Text |
|---|---|
| no pipeline in any repo | `Nothing is running. Start /sdlc:ship in any repo and it shows here.` |
| no pipeline in the repos of the filter | `No pipelines for the selected repos.` |
| event stream lost | `Reconnecting…` |

#### Scenario: Filter after reload
- **WHEN** the user turns on the repo chip `sdlc-plugin` and reloads the page
- **THEN** the chip `sdlc-plugin` is still on

#### Scenario: Tab after reload
- **WHEN** the user opens the History tab and reloads the page
- **THEN** the History tab is open

#### Scenario: Group state after reload
- **WHEN** the user collapses an unfinished block and opens a finished block
- **AND** the user reloads the page
- **THEN** the unfinished block is open and the finished block is collapsed

#### Scenario: Markup in a prompt
- **WHEN** a prompt preview contains `<b>x</b>`
- **THEN** the page shows the text `<b>x</b>` and no bold text

#### Scenario: Reduced motion
- **WHEN** the browser has `prefers-reduced-motion: reduce`
- **THEN** the lamp of the active step does not pulse

## ADDED Requirements

### Requirement: Issue severity values
The snapshot SHALL give each issue one severity of `critical`, `high`, `medium`, `low`, or `info`. It SHALL map `error` to `high`, `warning` to `medium`, and any other value to `info`.

#### Scenario: Error severity
- **WHEN** a state issue has severity `error`
- **THEN** the issue has `severity:"high"`

#### Scenario: Unknown severity
- **WHEN** a state issue has severity `notice`
- **THEN** the issue has `severity:"info"`

### Requirement: Step detail kind
Each step `detail` SHALL carry `kind`: `waves`, `dimensions`, `explorers`, `rounds`, or `findings`. A step with no detail SHALL have no `detail` key. An empty list SHALL be absent, and the `kind` SHALL stay.

#### Scenario: Plan station with no explorers
- **WHEN** a ship state has `planExploreSummary: []`
- **THEN** the `plan` step has `detail` `{kind:"explorers"}` and no `explorers` key

#### Scenario: Harden step
- **WHEN** a ship run has the step `harden`
- **THEN** the step has no `detail` key

### Requirement: Review findings detail
Each `completed` dimension step of a standalone review run SHALL have `detail.kind` `findings` and its findings with text, severity, file, and line. An `in_progress` dimension SHALL have no detail.

#### Scenario: Dimension with a finding
- **WHEN** the dimension file `correctness` holds one finding with rationale `off-by-one in batch cursor pagination`, severity `medium`, file `internal/payout/batch.go`, line `88`
- **THEN** the step `correctness` has `detail` `{kind:"findings", findings:[{text:"off-by-one in batch cursor pagination", severity:"medium", file:"internal/payout/batch.go", line:"88"}]}`

#### Scenario: Dimension with no finding
- **WHEN** the dimension `security` is checked out with no finding
- **THEN** the step `security` has `detail` `{kind:"findings"}`

#### Scenario: Dimension still running
- **WHEN** the dimension `performance` has no `checkoutAt`
- **THEN** the step `performance` has no `detail` key

### Requirement: Execute step detail
The snapshot SHALL give each execute wave step a `detail.waves` entry with the wave number, status, commit sha (`""` when not committed), and its tasks with id, name, and status. Planned tasks in no wave SHALL go to a last step `queued` with status `pending`.

#### Scenario: Running task
- **WHEN** a started wave has a task with no task row
- **AND** the progress file of that task exists
- **THEN** the task status is `in_progress`

#### Scenario: Planned task not started
- **WHEN** the execute state has `plannedTasks` with task `7`
- **AND** no wave holds task `7`
- **THEN** the last step is `queued` with task `7` in `detail.queued`

#### Scenario: Old run without names
- **WHEN** an execute state has no `plannedTasks` key
- **THEN** each task without a name shows its id and `name:""`

#### Scenario: Commits off
- **WHEN** the execute state has `commitWaves:"false"`
- **THEN** the pipeline has `commitWaves:false`

### Requirement: Plan stations
The snapshot SHALL show a plan run as 5 stations: `setup` (step 0), `explore` (1), `draft` (2), `review` (3 to 6), `finalize` (6.5 to 7). Progress SHALL count stations, with total 5.

#### Scenario: Explore detail
- **WHEN** the plan evidence holds writer `explore-auth-flow` with 12 items
- **THEN** station `explore` lists explorer `auth-flow` with `total:12` and its first 5 findings

#### Scenario: Review rounds
- **WHEN** the plan state has 2 `reviewRounds` rows and the run is at step `5`
- **THEN** station `review` lists 2 rounds with `maxRounds:5`
- **AND** the progress label is `round 3 of 5`

#### Scenario: Unreadable evidence
- **WHEN** the plan evidence path cannot be read
- **THEN** station `explore` has no detail
- **AND** the snapshot does not fail

### Requirement: Ship run joins
The snapshot SHALL nest an execute run and a review run into the ship run of the same branch. A nested run SHALL not show as its own pipeline. A run that matches no ship run SHALL stay its own pipeline.

#### Scenario: Execute inside the ship window
- **WHEN** an execute run on branch `feat/x` starts after the ship run on `feat/x` starts
- **THEN** the ship step `execute` holds the waves of that execute run
- **AND** the execute run is not its own pipeline

#### Scenario: Review with a ship run id
- **WHEN** the `run.meta` file of a review ledger has the ship run id of a ship run
- **THEN** the ship step `review` lists the review dimensions

#### Scenario: Review without run meta
- **WHEN** a review ledger folder has no `run.meta`
- **THEN** the review run stays its own pipeline

#### Scenario: Two matching execute runs
- **WHEN** two execute runs match one ship run
- **THEN** the newest execute run nests and the other stays its own pipeline

### Requirement: Review station totals
The ship step `review` SHALL list each dimension with its name, status, finding count, and worst severity, and SHALL give the totals found, fixed, deferred, and unaccounted from the ship healing data when that data exists.

#### Scenario: Dimension count
- **WHEN** a nested review dimension `security` has 3 findings, one of them `high`
- **THEN** the dimension has `findings:3` and `worst:"high"`

#### Scenario: Totals
- **WHEN** the ship healing data has 9 found, 6 fixed, 3 deferred, and 0 unaccounted
- **THEN** the review step has `reviewTotals` `{found:9, fixed:6, deferred:3, unaccounted:0}`

### Requirement: Plan station on a ship run
A ship run whose state has `planExploreSummary` SHALL get a first step `plan` with status `completed` that lists the stored explorers. A ship run without the key SHALL get no `plan` step.

#### Scenario: Stored summary
- **WHEN** a ship state has `planExploreSummary` with explorer `auth-flow`
- **THEN** the first ship step is `plan` and lists explorer `auth-flow`
- **AND** the progress total goes up by 1

### Requirement: Pipeline session
Each pipeline SHALL carry `sessionId` from its state, or `""`. A nested execute run without an id SHALL use the ship id. The page SHALL show the session with that id, else the newest session on the same branch.

#### Scenario: No session id
- **WHEN** a pipeline has `sessionId:""`
- **AND** two sessions exist on its branch
- **THEN** the page shows the session with the newest last activity

### Requirement: Run history
Each repo SHALL carry `history`: the 50 newest rows of `runs.jsonl`, newest first, each with kind, branch, outcome, start time, end time, and duration. A missing file SHALL give `history:[]`.

#### Scenario: Failure row
- **WHEN** `runs.jsonl` holds a ship row with `outcome:"failure"`
- **THEN** `history` lists it with `outcome:"failure"`

#### Scenario: Corrupt line
- **WHEN** one line of `runs.jsonl` does not parse
- **THEN** the other rows still show

### Requirement: Page layout
The page SHALL have the title `sdlc signal room` and a header with the tabs Pipelines, Activity, and History, each with a count. Under the header a sticky repo filter SHALL serve all three tabs. The page SHALL have no left rail.

#### Scenario: Tabs by keyboard
- **WHEN** the Pipelines tab has focus and the user presses the right arrow key
- **THEN** the Activity tab opens

#### Scenario: End key
- **WHEN** a tab has focus and the user presses `End`
- **THEN** the History tab opens

#### Scenario: Narrow page
- **WHEN** the page is narrower than 1000 px
- **THEN** the header counts are hidden

### Requirement: Repo filter
The filter SHALL show `All` and one chip for each repo with its pipeline count. Many chips SHALL be able to be on. No chip on SHALL mean all repos. The filter SHALL hide the blocks, activity rows, and history rows of other repos. Tab counts SHALL use the filter. Header counts SHALL not.

#### Scenario: Two chips on
- **WHEN** the chips `sdlc-plugin` and `payments-service` are on
- **THEN** the Pipelines tab shows only the blocks of those two repos

#### Scenario: Failed run count
- **WHEN** the repo `identity-service` has a failed run
- **THEN** its chip shows the pipeline count in red

#### Scenario: Header counts
- **WHEN** a chip is on
- **THEN** the header counts running, stalled, and failed still count all repos

### Requirement: Pipeline blocks
The Pipelines tab SHALL show one block for each pipeline: unfinished runs first in snapshot order, then finished runs. A block head SHALL show the status lamp, kind, branch, repo name, an issue chip when issues exist, the status word, and a `details N` toggle.

#### Scenario: Default state
- **WHEN** the page loads with one running and one completed pipeline
- **THEN** the running block is open and the completed block is collapsed

#### Scenario: Collapsed block
- **WHEN** the user collapses a block
- **THEN** the block shows its head and track only

#### Scenario: Collapse all
- **WHEN** a visible block is open and the user clicks `Collapse all details`
- **THEN** every visible block collapses and the button text is `Expand all details`

### Requirement: Step tiles
A block SHALL show one open tile for each step with detail, in track order, with the step name and a summary. A step with no detail SHALL have no tile. Issues SHALL be a tile when issues exist, open by default. Session SHALL be a tile when a session is found, closed by default.

#### Scenario: Review tile summary
- **WHEN** a review step has 5 dimensions, 3 done, and totals with 5 found
- **THEN** its tile summary is `3/5 dimensions done · 5 findings`

#### Scenario: Station click
- **WHEN** the user clicks the station `review` of a collapsed block
- **THEN** the block opens, the `review` tile opens, and the page scrolls to it

#### Scenario: Station with no tile
- **WHEN** the user clicks the station `commit` that has no detail
- **THEN** nothing changes

#### Scenario: Commits off
- **WHEN** a pipeline has `commitWaves:false`
- **THEN** each wave shows `commits off`

#### Scenario: Explorer with more findings
- **WHEN** an explorer has `total:12`
- **THEN** the tile shows 2 findings and `10 more`

#### Scenario: Empty findings tile
- **WHEN** a findings tile has no findings
- **THEN** the tile shows `No findings.`

### Requirement: Page state across snapshots
A new snapshot SHALL keep, for each pipeline, the collapsed state, the tiles the user closed, and the selected station. It SHALL keep the scroll position and the keyboard focus.

#### Scenario: Closed tile
- **WHEN** the user closes the `execute` tile and a new snapshot arrives
- **THEN** the `execute` tile stays closed

#### Scenario: Scroll position
- **WHEN** the user scrolls down the feed and a new snapshot arrives
- **THEN** the scroll position does not change

### Requirement: Page links
The page SHALL read the URL hash on load and on each hash change. `#<pipeline id>` SHALL scroll to that block. `#<pipeline id>/<n>` SHALL also select station n. `#activity` and `#history` SHALL open that tab.

#### Scenario: Link to a station
- **WHEN** the page opens with `#ship-feat-x-20261007T072607Z/2`
- **THEN** the page scrolls to that ship block and selects station 2

#### Scenario: Link to a tab
- **WHEN** the page opens with `#history`
- **THEN** the History tab opens

#### Scenario: Unknown id
- **WHEN** the hash names no pipeline and no tab
- **THEN** the page ignores the hash

### Requirement: Activity and history tabs
The Activity tab SHALL show `Open deferred (n)` and then `Learnings today (n)`, each row with its repo. The History tab SHALL show one table with outcome, kind, branch, repo, finished time, and duration. Each list SHALL have its own empty text.

#### Scenario: No history
- **WHEN** no repo in scope has history rows
- **THEN** the History tab shows `No finished runs for the selected repos.`

#### Scenario: No deferred items
- **WHEN** no repo in scope has open deferred items
- **THEN** the Activity tab shows `No open deferred items.`

### Requirement: Page text safety and contrast
The page SHALL insert every snapshot string as plain text, SHALL show a URL ref as text and not as a link, and SHALL give every text colour a contrast of 4.5:1 or more on the page background.

#### Scenario: Markup in a branch name
- **WHEN** a branch name is `<b>x</b>`
- **THEN** the page shows the text `<b>x</b>` and no bold text

#### Scenario: URL ref
- **WHEN** an explorer finding has ref `https://example.com`
- **THEN** the page shows the ref as text with no link
