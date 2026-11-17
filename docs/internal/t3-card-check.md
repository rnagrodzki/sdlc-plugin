# T3 Code run card check

T3 Code 0.1.9 (Beta, read from the app bundle), sdlc 61ef441, 2026-11-17.
Setup: task deploy, then /reload-plugins in the T3 thread.

The operator ran the checks by hand in a live T3 thread. Rows 3 and 5 are not tested.

Setup check: `dashboard_card` is listed in `tools/list` in the T3 thread. Result: pass.

| # | Check | How | Result | Evidence |
|---|---|---|---|---|
| 1 | The card renders in the chat | call dashboard_card with the runId of a real ship run in a T3 thread | pass | The operator saw the card render for run `ship-feat-t3-support-20261117T075435Z` |
| 2 | A refresh needs no approval | click Refresh in the card | pass | The operator clicked Refresh and the card showed the new state (execute and commit completed, review in progress). The operator reported no approval problem |
| 3 | The card between two turns | wait 60 s with no agent turn | not tested | |
| 4 | The **Next:** text reaches the model | read the tool result the model saw | pass | The tool result carried `next: "Card shows run ship-feat-t3-support-20261117T075435Z (running). Continue the current step."` |
| 5 | Remote T3 web client | open the thread from the web client | not tested | |
| 6 | The run panel opens in the right panel of this thread only | `dashboard` ensure with `open:true` and `runId`, then `preview_open` with `previewUrl`, in thread A. Switch to thread B, then back | partial | `dashboard` ensure returned `previewUrl`. `preview_open` showed the dashboard with the "Thread eeb2e7ba" filter. The run showed "No run of this thread yet" because the run file has no `t3ThreadId`: the run started before the stamping existed. The switch to thread B is not tested |
| 7 | The page width follows the panel | in the panel, read `window.innerWidth`, then make the panel narrower | pass | `innerWidth` was 1280, then 600 after the tab resize to 600 |

## Effect on the plan

No row failed. No result blocks Tasks 13, 14 and 15.

Open risks. Each risk stays open until a person tests it:

- Row 3 (card between two turns) is not tested. Task 15 must not state that the card refreshes without an agent turn.
- Row 5 (remote T3 web client) is not tested. Task 15 must not state that the card renders in the remote web client.
- Row 6, thread switch (thread A, then thread B, then thread A) is not tested. Task 15 must not state that each thread has its own right panel. The user stated this on 2026-11-16 and the check did not confirm it.
- Row 6, thread filter: the check did not show a run that matches the filter. The run had no `t3ThreadId`. Repeat the check with a run that starts after the stamping of the thread id exists.
