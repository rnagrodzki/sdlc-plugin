# /sdlc:dashboard

See the pipelines of every registered repo on one local web page, with one command and no switch between sessions.

## Why use it

You run pipelines in more than one repo and in more than one session.
To see their progress, you switch between sessions and terminals.
This skill opens one local web page that shows every registered repo: pipeline state, session activity, issues and preplan topic files.
The same command, with one flag, reports on the server or stops it.

## Example

Open the dashboard:

```text
/sdlc:dashboard
```

The skill starts the server when it is not active, opens the page in your default browser, and prints one line:

- `Dashboard: <url>`

`<url>` is the address of the server. The default address is `http://127.0.0.1:7385`.
[Dashboard](../dashboard.md) describes each tab and each control of the page.

## What you get

- See every registered repo on one page: pipeline state with the status of each follow-up instruction, session activity, issues and preplan topic files, without a switch of session or terminal.
- Run the command again at no risk: when a server of the current version is active, you get the same address and no second server.
- Check or stop the server with one flag: `--status` reports the address, the pid and the version, and `--stop` ends the server.
- Work over SSH: `--no-open` starts the server and prints the address, and opens no browser.
- Act from the page: [archive a run](../dashboard.md#archive-a-run), [clear the cache](../dashboard.md#clear-cache-files), [delete an item](../dashboard.md#delete-an-item), or [change a preplan status, delete History rows and dismiss a wait](../dashboard.md#change-a-preplan-status-delete-history-rows-dismiss-a-wait). The page has no undo.

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 1: Parse arguments | skill | Reads the flag and picks the mode: ensure and open, ensure without opening, status, or stop. It stops with a usage line when two flags or an unknown argument are given. | The chosen mode | no | always |
| Step 2: Call the tool and print the message | tool | Makes one `dashboard` tool call for the mode. It prints `Dashboard: <url>` for ensure, or the tool `summary` for status and stop. | The dashboard address, or the status text | no | always |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/sdlc:dashboard`, with no flag or with exactly one of `--status`, `--stop` or `--no-open`. |
| Ends | After Step 2 prints one message. A tool error stops the skill after it prints the error `## Do this` text as it is. |
| Asks the user | Never. The skill has no question and no gate. |
| Resume | none |

## More examples

**Check that the server runs:**

```text
/sdlc:dashboard --status
```

The skill reports that the server runs, with its address, pid and version, or that the server does not run. It starts nothing.

**Stop the server:**

```text
/sdlc:dashboard --stop
```

The skill ends the server and reports it as not running, with its last known address. Use it before your laptop sleeps or restarts.
When the server does not stop in time, the skill also tells you to check the server log and to try again.

**Start the server with no browser:**

```text
/sdlc:dashboard --no-open
```

The skill starts the server when it is not active and prints `Dashboard: <url>`. It opens no browser. Use it over SSH, or when you open the address yourself.

## Related skills

- [/sdlc:ship](./ship.md): runs the pipeline that the dashboard shows.
- [/sdlc:preplan](./preplan.md): writes the preplan topic files that the Preplans tab shows.
- [/sdlc:deferred](./deferred.md): triages the deferred items that you can also delete on the Activity tab.
