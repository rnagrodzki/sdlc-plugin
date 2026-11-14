# Tips and gotchas

Read this page before your first `/sdlc:ship` run, or when a run pauses, skips a prompt or picks a value that you did not expect.

- **Configure before first use.** Run [/sdlc:setup](../setup.md) to set the
  step table, review threshold, release bump, and more.
- **`--quick` needs configuration.** It uses the `[ship.quick]` table from your
  project config. Nothing happens if no quick table is set up.
- **Review threshold.** Default: `info` — every finding, including Info,
  triggers the fix loop. Change it via `/sdlc:setup`. Findings below a higher
  threshold are saved for [/sdlc:deferred](../deferred.md), not dropped.
- **Saved progress survives sessions.** Run `/sdlc:ship --resume` (or a plain
  `/sdlc:ship`) in a new session to continue an interrupted pipeline. See
  [State and Resolution Trace](./state-and-trace.md#state-and-resolution-trace).
- **Two independent automation controls.** `--auto` (and its config
  equivalent) suppresses confirmation prompts for the whole run. A separate
  per-step automation setting in your project config controls whether
  individual steps pause even when `--auto` is off — the two do not imply
  each other, so check both if a step pauses (or doesn't) unexpectedly.
- **Promoting an RC to final doesn't need a new PR.** Use the "Promote Release" GitHub
  Actions workflow (Actions → Promote Release → Run workflow) to turn the latest RC tag
  into a final release at the same tested commit. See [versioning docs](../../versioning.md#promoting-rc-to-final-release).
