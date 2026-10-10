# /dashboard

Open, check, or stop the local sdlc dashboard: one web page that shows the
pipelines of every registered repo, their progress, session activity,
issues, and preplan topic files (Preplans tab). The page can also delete one
preplan topic file, deferred item, or learning for good. The full command
name is `/sdlc:dashboard`.

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
