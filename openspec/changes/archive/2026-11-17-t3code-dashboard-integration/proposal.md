# Proposal

## Why

- Problem: in T3 Code, the sdlc dashboard opens in a system browser, away from the thread. A run has no link to the T3 thread that started it.
- Evidence: `dashboard` opens the default browser (`internal/tools/dashboard.go:169-175`). Run state has no thread field. At 600 px the header is cut and History overflows (live test 2026-11-16).
- Why now: T3 Code gives each thread a right panel and MCP App cards in the chat. The user asked for the run of each thread in that panel, and for a narrow layout.

## What Changes

| Area | Before | After |
|---|---|---|
| Run state | no thread field | new runs record `t3ThreadId` when the MCP server runs in T3 Code |
| `dashboard` tool, `ensure` | opens the system browser | in T3 Code: returns `previewUrl` and opens no browser. New input `runId` |
| `/sdlc:dashboard`, `/sdlc:setup` | print the address | in T3 Code: open the address in the T3 preview tab. A failed tab prints the address and one warning |
| Dashboard page | all pipelines | `?thread=<id>` shows only the runs of that thread. A thread chip on each block. A run hash waits for its run |
| Dashboard page width | breaks below about 700 px | fits 600 px. Below 640 px the rows stack vertically |
| New tool `dashboard_card` | none | read-only MCP App card of one ship or execute run in the T3 chat |
| `/sdlc:ship`, `/sdlc:execute` | no T3 output | at run start: open the run panel and show the start card. At run end: show the end card |
| `ship_prepare`, `execute_state` | no thread facts | return `t3ThreadId`. `resolve-config` returns `shipDispatched`. `read` returns `runId` |

Outside T3 Code, no behavior changes.

Flow of a ship run in a T3 thread after the change:
```mermaid
flowchart LR
  A["/sdlc:ship"] --> B["ship_prepare: t3ThreadId"]:::changed
  B --> C["dashboard ensure runId"]:::new
  C --> D["preview_open previewUrl"]:::new
  D --> E["dashboard_card runId"]:::new
  E --> F[pipeline steps]
  F --> G["dashboard_card runId final"]:::new
  classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px
  classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px
```

## Capabilities

### New Capabilities
- `tool-dashboard-card`: read-only MCP tool that shows one ship or execute run as an MCP App card.

### Modified Capabilities
- `tool-dashboard`: `ensure` returns a T3 preview address and takes `runId`.
- `skill-dashboard`: opens the T3 preview tab.
- `skill-setup`: opens the Setup tab in the T3 preview tab.
- `pipeline-dashboard`: thread filter, thread chip, pending run hash, narrow and vertical layout.
- `skill-ship`: run panel, start card and end card.
- `skill-execute`: run panel, start card and end card for a standalone run.
- `tool-ship-prepare`: returns `t3ThreadId`.
- `tool-ship-state`: ship state records `t3ThreadId`, `read` returns `runId`.
- `tool-execute-state`: execute state records `t3ThreadId`, `resolve-config` returns `t3ThreadId` and `shipDispatched`, `read` returns `runId`.
- `tool-plan-prepare`: plan state records `t3ThreadId`.

## Impact

| Path | Kind | Change |
|---|---|---|
| `internal/t3/` | internal package | new: reads `T3CODE_THREAD_ID` |
| `internal/state/state.go` | internal package | `Init` stamps `t3ThreadId` |
| `internal/tools/dashboard.go` | MCP tool | `previewUrl`, `runId` |
| `internal/tools/dashboard_card.go` | MCP tool | new tool and card view |
| `internal/mcpserver/` | internal package | MCP App registration path |
| `internal/tools/ship.go`, `execute_state.go`, `ship_state.go`, `execute_config.go` | MCP tool | thread facts, `runId` on `read` |
| `internal/dashboard/web/static/` | page | thread filter, chip, pending hash, 600 px and 640 px rules |
| `plugins/sdlc/schemas/ship-state.schema.json`, `execute-state.schema.json` | schema | optional `t3ThreadId` |
| `plugins/sdlc/skills/dashboard/`, `setup/`, `ship/`, `execute/` | skill | T3 preview tab, run panel, cards |
| `docs/dashboard.md`, `docs/skills/*.md`, `site/src/data/features.mjs` | doc | T3 Code section |
