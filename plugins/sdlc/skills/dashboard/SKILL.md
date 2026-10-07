---
name: dashboard
description: "Use this skill to open the local sdlc dashboard: one web page that shows the pipelines of every registered repo, their progress, session activity, and issues. --status reports the server. --stop ends it. --no-open starts it without a browser. Arguments: [--status | --stop | --no-open]. Triggers on: dashboard, open dashboard, pipeline dashboard, show pipelines, stop dashboard."
user-invocable: true
argument-hint: "[--status | --stop | --no-open]"
model: sonnet
---

# Dashboard (SDLC)

The sdlc dashboard is one local web page that shows every registered repo's
pipeline state, session activity, and issues, without switching sessions or
terminals. This skill starts it, reports on it, or stops it through the
`dashboard` MCP tool, in one step.

**Announce at start:** "I'm using dashboard (sdlc v{sdlc_version})." - extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

**Communication style:** Follow the `sdlc communication style` block in session context for every explanation, status line, summary, and AskUserQuestion text in this skill.

---

## Step 1: Parse arguments

| Arguments | Mode |
|---|---|
| none | Ensure and open |
| `--no-open` | Ensure without opening |
| `--status` | Status |
| `--stop` | Stop |

Stop with a one-line usage message, `/sdlc:dashboard [--status | --stop | --no-open]`, when more than one of these flags is given, or when any other argument appears. Do not call any tool first.

## Step 2: Call the tool and print the message

Make exactly one `dashboard` tool call, from the mode found in Step 1:

| Arguments | Tool call | Message |
|---|---|---|
| none | `dashboard({action:"ensure", open:true})` | `Dashboard: <url>` |
| `--no-open` | `dashboard({action:"ensure", open:false})` | `Dashboard: <url>` |
| `--status` | `dashboard({action:"status"})` | the tool's `summary` |
| `--stop` | `dashboard({action:"stop"})` | the tool's `summary` |
| tool error | none | print the `## Do this` text as it is, then stop |

`<url>` is the tool's `url` field.

### Every exit state

- **Started** — the first `ensure` call on this machine (or after a version or port change): the tool returns `Started:true`. Print `Dashboard: <url>`.
- **Already active** — `ensure` finds a server of the current version already listening: the tool returns `Started:false`, `Running:true`. No second server starts. Print the same `Dashboard: <url>` — the message does not change between this case and "started", only the tool's `started` field does.
- **Stopped** — `--stop` succeeds and the server is down. Print the tool's `summary` (it reports the server as not running, with its last known address). If the tool's `running` field is still `true` (the server did not stop within its wait window), also print the tool's `next` line, which says to check the server's log and try again.
- **Status** — `--status` reports the server's current state. Print the tool's `summary` (running, with its URL, pid and version, or not running).
- **Error** — any tool error, from any mode. Print the error's `## Do this` text exactly as it is, with no paraphrase, and stop. Make no other call and do not retry.

---

## Rules

- This skill is one step. It starts no sub-agents.
- It is safe to run twice: `ensure` is idempotent, so running it again with a server already active returns the same URL instead of starting a second server.
- Never paraphrase a tool error's `## Do this` text. Print it exactly as it is.

Related: [`docs/dashboard.md`](../../../../docs/dashboard.md) — how the dashboard server, registry, and web page fit together.
