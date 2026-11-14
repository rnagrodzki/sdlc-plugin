# /dashboard

Open, check, or stop the local sdlc dashboard: one web page that shows the
pipelines of every registered repo, their progress, session activity,
issues, and preplan topic files (Preplans tab, and the Preplans box on the
Activity tab). The page can also change the
status of a preplan topic file, delete History rows, dismiss a wait that no
pipeline shows, and delete one preplan topic file, deferred item, or learning
for good. The full command name is `/sdlc:dashboard`.

## When to use

- You want to see every repo's pipeline progress at a glance, without
  switching sessions or terminals.
- You want to check whether the dashboard server is already running.
- You want to shut the server down before your laptop sleeps or reboots.
- You want to start the server without a browser popping up, for example
  when working over SSH.
- You want to see your preplan topic files and their status on the Preplans
  tab.
- You want to delete a preplan topic file (Preplans tab), or a deferred item
  or a learning (Activity tab). The delete is permanent: the page has no
  undo. See [Delete an item](../dashboard.md#delete-an-item).
- You want to change the status of a preplan topic file, delete History rows,
  or dismiss a wait that no pipeline shows. See
  [Change a preplan status, delete History rows, dismiss a wait](../dashboard.md#change-a-preplan-status-delete-history-rows-dismiss-a-wait).

## Syntax

    /dashboard [--status | --stop | --no-open]

With no flag, `/dashboard` starts the server if it is not already running,
then opens it in your default browser.

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--status` | Report whether the server is running, and its address, pid, and version. Starts nothing. Cannot be combined with `--stop` or `--no-open`. | off |
| `--stop` | End the running server. Cannot be combined with `--status` or `--no-open`. | off |
| `--no-open` | Start the server if it is not already running, but do not open a browser. Cannot be combined with `--status` or `--stop`. | off |

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 1: Parse arguments | skill | Reads the flag and picks the mode: ensure and open, ensure without opening, status, or stop. It stops with a usage line when two flags or an unknown argument are given. | The chosen mode | no | always |
| Step 2: Call the tool and print the message | tool | Makes one `dashboard` tool call for the mode. It prints `Dashboard: <url>` for ensure, or the tool `summary` for status and stop. | The dashboard address, or the status text | no | always |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/dashboard`, with or without one flag: `--status`, `--stop` or `--no-open`. |
| Ends | After Step 2 prints one message. A tool error stops the skill after it prints the error `## Do this` text as it is. |
| Asks the user | Never. The skill has no question and no gate. |
| Resume | none |

## Examples

**Open the dashboard:**

    /dashboard

Starts the server if it is not already running, then opens it in your
default browser. If a server of the current version is already running,
no second server starts and the same address opens.

**Check if it is running:**

    /dashboard --status

Reports the server's address, process id, and version if it is running, or
reports that it is not running. Starts nothing.

**Stop the server:**

    /dashboard --stop

Ends the running server. Use this before your laptop sleeps or reboots, or
when you are done with the dashboard for the session.

**Start it without opening a browser:**

    /dashboard --no-open

Starts the server if it is not already running, and prints its address, but
does not open a browser. Useful over SSH, or when you will open the address
yourself.
