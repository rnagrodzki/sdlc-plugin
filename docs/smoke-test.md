# Manual smoke test

Run this after installing the plugin (or after any launcher/hook/`.mcp.json`
change) to confirm the plugin actually works end to end. Every step is
manual — there is no automated harness for this flow. Do the steps in order;
step 3 is what completes the cold-cache binary download, and steps 4-5
depend on it having finished.

Prerequisite: a real GitHub Release tagged `v<version>` (matching
`plugins/sdlc/.claude-plugin/plugin.json`) must exist at
`https://github.com/rnagrodzki/sdlc-plugin/releases`, with assets
`sdlc-<version>-<os>-<arch>` and `checksums.txt` — the launcher cannot
download a binary that was never released. Check this first if any step
below fails with "binary not ready".

## 1. Install

```
/plugin marketplace add rnagrodzki/sdlc-plugin
/plugin install sdlc@sdlc-plugin
```

Expect: no error from either command.

## 2. Reload plugins

```
/reload-plugins
```

Expect: no error. This is what makes Claude Code read `.mcp.json` and
`hooks/hooks.json`.

## 3. Verify tools are registered

Ask Claude something that requires listing available tools (e.g. "what MCP
tools do you have from the sdlc plugin?"), or check your client's MCP/tool
inspector if it has one.

Expect: tools prefixed `mcp__plugin_sdlc_sdlc__`, e.g.
`mcp__plugin_sdlc_sdlc__commit_prepare`, `mcp__plugin_sdlc_sdlc__version_prepare`.
32 tools total (see README "Tool surface" table for the full list).

If no tools appear: this is the step that triggers the cold-cache binary
download (the MCP connect path has a 60s budget, unlike hooks). Wait a few
seconds and retry. If it still fails, check stderr for a
`sdlc-launcher: ...` line and see `README.md` → Troubleshooting.

## 4. One full commit flow

In a repo with an uncommitted change, ask Claude to commit it using the sdlc
commit flow (e.g. "commit this using sdlc" or invoke the `commit`
skill if installed).

Expect:
- `commit_prepare` runs and returns a manifest / diff summary.
- A commit message is produced and a real `git commit` happens.
- `git log -1` shows the new commit with the expected message.

## 5. One hook firing (SessionStart banner)

Hooks fire in the background of a normal session; the easiest one to
observe directly is SessionStart.

- If step 3 already completed the binary download, start a new session (or
  run `/clear`, matcher `startup|clear|compact` in `hooks/hooks.json`).
- Expect additional context injected at session start starting with
  `sdlc: v<version> (<N> skills loaded)`.

If nothing appears on the very first session after install: expected on a
cold cache (see README → "First-run binary fetch"). Do step 3 first, then
retry this step with `/clear`.

## 6. Resume mid-run deliver

Seed a fixture deliver state file for the current branch, then resume:

```bash
cat > ".sdlc/execution/deliver-$(git branch --show-current | tr -c 'a-zA-Z0-9-' '-')-20260101T000000Z.json" <<'EOF'
{
  "branch": "<current-branch>",
  "planPath": "plans/example.md",
  "phase": "review",
  "phaseHistory": ["plan", "execute"],
  "fixLoop": {"iteration": 0, "maxIterations": 3, "severityThreshold": "high", "history": []},
  "terminal": null,
  "createdAt": "2026-01-01T00:00:00Z",
  "updatedAt": "2026-01-01T00:00:00Z"
}
EOF
```

Then ask Claude to run `/deliver --resume`.

Expect: deliver reads the fixture file, reports resuming at the `review`
phase (the fixture's recorded `phase`), and dispatches review next — it
does not restart from the `plan` phase or re-dispatch execute-plan.

## Pass criteria

All six steps produce their expected result with no unhandled error. A
step failing with a `sdlc-launcher: ...` fail-open diagnostic on stderr is a
launcher/release problem, not a hook or tool-registration bug — resolve it
per `README.md` → Troubleshooting before treating any later step as a real
failure.
