---
name: dashboard-design
description: "Design the sdlc dashboard page in a live browser preview - start the preview program, edit the draft from chat feedback, mark the data the draft needs, and approve the design into a requirements file for /sdlc:plan. Use when the user wants to change, redesign, or try a layout of the sdlc dashboard. Triggers on: dashboard design, design the dashboard, dashboard preview, redesign the dashboard, design preview."
compatibility: Requires task, go, and curl.
---

Design the sdlc dashboard page with a live preview.

The preview program serves the draft in `design/dashboard/static/` next to the real dashboard API. You edit the draft from chat feedback. The user reloads the browser tab to see each change. When the user approves, you write `design/dashboard/requirements.md`.

**This skill changes only the draft and `requirements.md`.** Never edit `internal/dashboard/web/static/`. That folder holds the shipped page.

## Facts

| Item | Value |
|---|---|
| Preview address | `http://127.0.0.1:55570/` |
| Draft folder | `design/dashboard/` |
| Draft page files | `design/dashboard/static/*` |
| Dependency records | `design/dashboard/dependencies.json` |
| Base commit record | `design/dashboard/base.json` (field `baseCommit`) |
| Status route | `GET http://127.0.0.1:55570/__design/status` |
| Stop route | `POST http://127.0.0.1:55570/__design/stop` |
| Start tasks | `task design`, `task design:fresh`, `task design:continue` |

- No step writes state. There is no worker and no run ledger.
- The only background process is the preview program.
- Re-entry is safe. `task design` runs the start rule again each time. The start rule keeps a draft that has own work. It asks only in case d.
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
   - Override: discard the draft and copy the shipped page again.
   - Keep: keep the draft and move its base commit to HEAD.
   - Cancel.
3. Override: run `task design:fresh` with `run_in_background: true`. Then follow the marker map.
4. Keep: run `task design:continue` with `run_in_background: true`. Then follow the marker map.
5. Cancel: go to exit S. The process already ended, so there is nothing to stop.

## Step 3: URL

1. The serving line has this form: `design preview: serving http://127.0.0.1:<port>/ (pid <pid>)`.
2. Print the URL and the pid.
3. Keep the URL, the pid, and the background task id for the stop procedure.
4. Go to step 4.

## Step 4: Loop

Repeat this loop until the user approves, stops, or an error occurs.

1. Take the chat feedback from the user.
2. Edit `design/dashboard/static/*` with Edit or Write.
3. For each element that shows data the dashboard does not give yet, apply the marking rule.
4. Tell the user to reload the browser tab.
5. Go back to the loop start. Go to exit A when the user approves. Go to exit S when the user stops. Go to exit E when the background task ends or the page shows a file error.

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

## Exits

No exit writes state. Every exit runs the stop procedure when a preview program runs. A preview program runs when the background task still runs or the port answers.

| Exit | When | Action | State write |
|---|---|---|---|
| A (approve) | The user approves the design | Run the stop procedure. Then write `requirements.md`. | none |
| S (stop or cancel) | The user stops, or cancels in step 2 | Run the stop procedure if a preview program runs. Say the draft stays in the repo. | none |
| E (error, stall, or the process ends) | An error line, a stall, or the process ended | Print the last output line. Run the stop procedure if a preview program runs. | none |

### Exit A: approve

1. Run the stop procedure.
2. Read `base.json`. Take the `baseCommit` value.
3. If `design/dashboard/requirements.md` exists, Read it first. Write can replace a file only after a Read.
4. Write `design/dashboard/requirements.md` in the shape below. Write replaces the whole file in one call.
5. If the write succeeds, offer `/sdlc:plan design/dashboard/requirements.md`. Do not run it.
6. If the write fails, print the error. Then follow exit E.

Shape of `requirements.md`:

```markdown
# Dashboard design: <goal in one line>

**Draft:** `design/dashboard/static/`
**Base commit:** <sha from base.json>

## Goal
<goal of the design change>

## Dependencies
| ID | Kind | Need | Elements |
|---|---|---|---|
| D1 | data | Snapshot gives the queue wait time for each run. | `#run-row .wait` |
```

- Write one row for each record in `dependencies.json`.
- Write each selector of `elements` as a code span. Separate selectors with a comma.
- If the user did not state the goal, ask one question before the write.

### Exit S: stop or cancel

1. Run the stop procedure if a preview program runs.
2. Tell the user that the draft stays in `design/dashboard/`. Nothing is committed.

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
