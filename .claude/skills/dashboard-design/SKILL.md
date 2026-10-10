---
name: dashboard-design
description: "Design the sdlc dashboard page in a live browser preview - start the preview program, edit the draft from chat feedback, log each request of the user, mark the data the draft needs, and approve the design into a requirements file that goes to /sdlc:preplan and then /sdlc:plan. Use when the user wants to change, redesign, or try a layout of the sdlc dashboard. Triggers on: dashboard design, design the dashboard, dashboard preview, redesign the dashboard, design preview."
compatibility: Requires task, go, and curl.
---

Design the sdlc dashboard page with a live preview.

The preview program serves the draft in `design/dashboard/static/` next to the real dashboard API. You edit the draft from chat feedback. You write each request of the user into a log. The user reloads the browser tab to see each change. When the user approves, you write `design/dashboard/requirements.md` from the log and offer `/sdlc:preplan`.

The log exists because the draft shows only the result. The log keeps what the user asked for and why. `/sdlc:preplan` and `/sdlc:plan` need that to plan the real implementation.

**This skill changes only files in `design/dashboard/`.** Never edit `internal/dashboard/web/static/`. That folder holds the shipped page.

## Facts

| Item | Value |
|---|---|
| Preview address | `http://127.0.0.1:55570/` |
| Draft folder | `design/dashboard/` |
| Draft page files | `design/dashboard/static/*` |
| Dependency records | `design/dashboard/dependencies.json` |
| Request log | `design/dashboard/requests.md` |
| Base commit record | `design/dashboard/base.json` (fields `baseCommit`, `shippedPath`) |
| Status route | `GET http://127.0.0.1:55570/__design/status` |
| Stop route | `POST http://127.0.0.1:55570/__design/stop` |
| Start tasks | `task design`, `task design:fresh`, `task design:continue` |

- No step writes run state. There is no worker and no run ledger. The request log is a record of the design, not run state.
- The only background process is the preview program.
- Re-entry is safe. `task design` runs the start rule again each time. The start rule keeps a draft that has own work. It asks only in case d.
- The request log stays with the draft. The start rule removes the log when it copies the shipped page (case a, case c, `fresh`). A log with one or more request rows counts as own work, like a changed draft file or a dependency record.
- The preview program serves `GET /api/*` from the real dashboard. Any other method on `/api/*` answers 405.
- The preview program adds the marks script to the draft page. Do not add it yourself.

## Step 1: Start

1. Run `task design` with Bash and `run_in_background: true`. Note the background task id.
2. Read the output of the background task again and again. Ignore lines that do not start with `design preview:`. Step 2 is the one exception: it also reads the commit lines of case d.
3. Follow the marker map for the first line that matches.

### Marker map

The first row that matches a line decides the route.

| Line or event | Next |
|---|---|
| `design preview: case d` | Step 2. The process ends with exit 3. This is expected. |
| `design preview: error — 127.0.0.1:55570 is in use` | Step 1b |
| `design preview: error` (any other) | Exit E. The error line names the fix. |
| `design preview: serving` | Step 3 |
| `design preview: case a`, `case b`, `case c`, `fresh`, `continue` | Keep reading |
| The task ends with no row above matched, or 120 s pass with no `serving` line | Exit E (stall) |

## Step 1b: Old preview program

The in-use line comes before the start rule. The draft is unchanged.

1. Run `curl -s http://127.0.0.1:55570/__design/status`.
2. The answer is JSON with a `dashboard` key. An old preview program runs. Run the stop procedure. Its report line for the old program uses `pid unknown`.
3. If the stop procedure prints `preview still running`, go to exit E without a second stop procedure. Do not retry.
4. Run the same task that printed the in-use line once more (`task design`, `task design:fresh` or `task design:continue`) with `run_in_background: true`.
5. Follow the marker map. A second in-use line goes to exit E.
6. Any other answer from the status route, or no answer, is not a preview program. Go to exit E without the stop procedure. Do not stop that process.

## Step 2: Case d

Case d means the shipped page changed since the base commit, and the draft has own work. The program changed nothing and ended.

1. Show the user the printed commits. They are the lines between the `design preview: case d` line and the `design preview: next` line.
2. Ask with AskUserQuestion. Offer these options:
   - Override: discard the draft, its dependency records, and its request log. Copy the shipped page again.
   - Keep: keep the draft and move its base commit to HEAD.
   - Cancel.
3. Override: run `task design:fresh` with `run_in_background: true`. Then follow the marker map.
4. Keep: run `task design:continue` with `run_in_background: true`. Then follow the marker map.
5. Cancel: go to exit S. The process already ended, so there is nothing to stop.

## Step 3: URL

1. The serving line has this form: `design preview: serving http://127.0.0.1:<port>/ (pid <pid>)`.
2. Print the URL and the pid.
3. Keep the URL, the pid, and the background task id for the stop procedure.
4. If `design/dashboard/requests.md` does not exist, write the empty log (see the logging rule). The start rule removes the log with a new draft, so a missing log means a new draft or a draft from before the log existed.
5. If the log has no row, check the draft. Read `base.json` and take `shippedPath`. Run `diff -rq <shippedPath> design/dashboard/static` with Bash. Exit code 1 means the draft differs. This is not an error.
   - The draft differs: it holds work with no logged request. Run `diff -ru` on the changed files. Write one row for each visible change with the write steps of the logging rule. Use the status `applied`. Start each `Request` cell with `(from diff)`. Put the matching `D<n>` ids from `dependencies.json` in `Deps`. Tell the user to correct these rows in chat.
   - The draft does not differ: go on.
6. Go to step 4.

## Step 4: Loop

Repeat this loop until the user approves, stops, or an error occurs.

1. Take the chat feedback from the user.
2. Edit `design/dashboard/static/*` with Edit or Write.
3. For each element that shows data the dashboard does not give yet, apply the marking rule.
4. Apply the logging rule to the feedback.
5. Tell the user to reload the browser tab.
6. Go back to the loop start. Go to exit A when the user approves. Go to exit S when the user stops. Go to exit E when the background task ends or the page shows a file error.

### Marking rule

1. Take the next free id `D<n>` in `dependencies.json`. Use the highest number in the file plus 1. Start with `D1`.
2. Add one record to the array in `dependencies.json`. Replace the whole file with Edit or Write.
3. In the draft script, write the value as `designDep('D<n>', value, el)`.

A record has these fields:

| Field | Rule |
|---|---|
| `id` | `D<n>`. Unique in the file. |
| `kind` | `data`, `state-write`, or `flow` |
| `need` | One sentence on one line. Not empty. |
| `elements` | List of CSS selectors for the elements. May be empty. |
| `sample` | Optional JSON value that the page shows while the data is missing. |

```json
[
  {
    "id": "D1",
    "kind": "data",
    "need": "Snapshot gives the queue wait time for each run.",
    "elements": ["#run-row .wait"],
    "sample": "4 min"
  }
]
```

`designDep` returns the text to show. An empty value gets a "missing" mark. An id that is not in `dependencies.json` gets an "unknown" mark. The file root must stay a JSON array. A broken file shows an error in the dependency panel of the page.

### Logging rule

A request is a part of the feedback that asks for a change of what the page shows or does. A question, a reload check, and "looks good" are not requests. One message can hold more than one request. Write one row for each request.

1. Take the next free id `R<n>`: the highest number in the log plus 1. Start with `R1`.
2. Read `requests.md` if you did not read or write it yet in this session. Write needs a Read first.
3. Write the whole file with one new row at the end of the table.
4. Write the row after the draft edit, so the `Draft change` cell is true.

The empty log has this shape:

```markdown
# Design requests

| ID | Request | Why | Draft change | Deps | Status |
|---|---|---|---|---|---|
```

A row has these cells:

| Cell | Rule |
|---|---|
| `ID` | `R<n>`. Unique in the file. |
| `Request` | What the user asked for, in the words of the user and in one line. Say who sees what. No code name and no selector. |
| `Why` | The reason the user gave. If the user gave no reason, write `—`. Never invent a reason. Never ask for one in the loop. |
| `Draft change` | What the page shows or does now, in one line. Say it as a reader of the page sees it. |
| `Deps` | The ids `D<n>` that the marking rule added for this request, separated by commas. `—` if none. |
| `Status` | `applied`, `not done - <reason>`, `replaced by R<n>`, or `dropped` |

- A cell has one line. Replace each `|` in the text with `/`.
- Never delete a row and never reuse an id. The log keeps the history of the design.
- If the user withdraws a request, set its status to `dropped`. If a new request replaces an old one, set the old status to `replaced by R<n>` and write the new row. This edit is part of the same Write.
- If the page cannot show the request, write the row with `not done - <reason>`. The request still goes to the plan.

## Exits

No exit writes state. Every exit runs the stop procedure when a preview program runs. A preview program runs when the background task still runs or the port answers.

| Exit | When | Action | State write |
|---|---|---|---|
| A (approve) | The user approves the design | Run the stop procedure. Then write `requirements.md`. Then hand off to preplan. | none |
| S (stop or cancel) | The user stops, or cancels in step 2 | Run the stop procedure if a preview program runs. Say the draft stays in the repo. | none |
| E (error, stall, or the process ends) | An error line, a stall, or the process ended | Print the last output line. Run the stop procedure if a preview program runs. | none |

### Exit A: approve

1. Run the stop procedure.
2. Read `base.json`. Take the `baseCommit` and `shippedPath` values.
3. Read `requests.md`. If it does not exist, treat the log as empty.
4. List the draft files that differ from the shipped page. Run `diff -rq <shippedPath> design/dashboard/static` with Bash. Exit code 1 means the folders differ. This is not an error. Ignore `.DS_Store`.
5. If `design/dashboard/requirements.md` exists, Read it first. Write can replace a file only after a Read.
6. Write `design/dashboard/requirements.md` in the shape below. Write replaces the whole file in one call.
7. If the write fails, print the error. Then follow exit E.
8. If the write succeeds, follow **Hand off to preplan**.

Shape of `requirements.md`:

```markdown
# Dashboard design: <goal in one line>

**Draft:** `design/dashboard/static/`
**Base commit:** <sha from base.json>

## Goal
<goal of the design change>

## Requests
| ID | Request | Why | Draft change | Deps |
|---|---|---|---|---|
| R1 | Each step shows how long it took. | Operators cannot see which step is slow. | Each station shows a time. | D1 |

## Not in the draft
| ID | Request | Why | Reason |
|---|---|---|---|

## Draft changes
| File | Change |
|---|---|
| `render.js` | changed |

## Dependencies
| ID | Kind | Need | Elements |
|---|---|---|---|
| D1 | data | Snapshot gives the queue wait time for each run. | `#run-row .wait` |

## Port notes
- The draft is the reference. Run `diff -ru <shippedPath> design/dashboard/static` to see every change.
- The preview program adds `designDep`. The shipped page does not have it. Remove each `designDep` call and read the real value. Remove each `sample` value.
- The draft folder is local and may not be in git. Run plan and execute in this worktree.
- This file has no behavior rules and no acceptance checks. `/sdlc:preplan` collects them.
```

- `## Requests`: copy each log row with the status `applied`. Leave out the `Status` cell.
- `## Not in the draft`: copy each log row with a status that starts with `not done`. The `Reason` is the text after `not done - `. If there is no such row, write the line `None.` in place of the table.
- Leave out each row with the status `dropped` or `replaced by R<n>`. The log keeps them.
- `## Draft changes`: write one row for each line of the `diff -rq` output. `Files … differ` is `changed`. `Only in design/dashboard/static` is `added`. `Only in <shippedPath>` is `removed`. Write each file name as a code span.
- `## Dependencies`: write one row for each record in `dependencies.json`. Write each selector of `elements` as a code span. Separate selectors with a comma.
- If the user did not state the goal, ask one question before the write.

### Hand off to preplan

The requirements file holds what the user asked for. It does not hold the behavior rules or the acceptance checks. `/sdlc:preplan` asks for them. Plan alone must guess them.

1. Ask with AskUserQuestion. Restate these facts first: the design is approved, `requirements.md` holds N requests and M data needs, and it has no behavior rules and no acceptance checks. Offer these options:
   - **Preplan first (Recommended)**: runs `/sdlc:preplan` in this session. Preplan asks one question at a time about who is affected, what changes, and what fails. It checks each answer against the plan guardrails. It then offers `/sdlc:plan`.
   - **Plan now**: skips preplan. Plan reads `requirements.md` as it is and guesses the missing rules.
   - **Stop**: ends this skill. `requirements.md` stays in the repo.
2. Preplan first: run `Skill(sdlc:preplan, "<topic>")`. The topic is a short name of the goal, 5 words at most. Preplan makes the file name from it.
3. Plan now: print `/sdlc:plan design/dashboard/requirements.md`. Do not run it. This skill never starts plan.
4. Stop: print the path of `requirements.md`. Say that `/sdlc:preplan <topic>` can start later. Tell the user to name `design/dashboard/requirements.md` in the answer to the first preplan question.

Plan reads the preplan topic file, not `requirements.md`. Preplan runs plan with the topic file. So the topic file must point to `requirements.md` and carry the port approach.

While preplan runs, the preplan rules stay in force. Use `requirements.md` only as the source of answers:

| Preplan section | Source in `requirements.md` |
|---|---|
| `## Goal` | `## Goal`. Add this line: `Design source: design/dashboard/requirements.md. Draft: design/dashboard/static/.` |
| `## Users and effect` | The `Why` cells of `## Requests`. Many cells are `—`, because the loop does not ask for a reason. Preplan asks the user for the missing ones. |
| `## Decisions` | One proposal for each row of `## Requests`. One for each record of `## Dependencies`. One for the port: copy the draft to the shipped folder, remove each `designDep` call and each `sample` value. |
| `## Open questions` | Each row of `## Not in the draft`. Each missing behavior rule or acceptance check. |

- Offer the answer from the requirements file as the first option of each preplan question. The user confirms or changes it.
- Preplan checks each proposal against the guardrails. This skill does not.

### Exit S: stop or cancel

1. Run the stop procedure if a preview program runs.
2. Tell the user that the draft and the request log stay in `design/dashboard/`. Nothing is committed.

### Exit E: error, stall, or the process ends

1. Print the last output line of the background task.
2. Run the stop procedure if a preview program runs.
3. If the task ended with no `serving` line, print the third report line below.

## Stop procedure

1. Run `curl -s -X POST http://127.0.0.1:55570/__design/stop`.
2. Run TaskStop on the background task if it still runs.
3. Run `curl -s -o /dev/null http://127.0.0.1:55570/`. This probe must fail. A failed probe is a non-zero exit code of curl.
4. If the probe answers, wait 1 s and probe one more time.

Then print each report line that applies:

| Case | Report line |
|---|---|
| The port still answers | `preview still running: <URL> (pid <pid>)` |
| TaskStop fails | `could not stop background task <task id>: <error>` |
| The task ended with no `serving` line | `preview task <task id> ended with no serving line. Last line: <line>` |

If a case does not apply, print nothing for it.
