# Changelog-Update Workflow (not available in this port)

This file documents Branch C ("Changelog-Update") of the original `version-sdlc` skill. It is
kept for reference only.

## Why it is unavailable

The Go/MCP port's `version_prepare` tool always reports `flow: "release"` — there is no
`"changelog-update"` flow value it can return. The original Changelog-Update flow (editing the
`CHANGELOG.md` entry for a version that is already tagged, without bumping the version again)
has no backing implementation in this port.

`version-sdlc`'s main `SKILL.md` never dispatches here, and a bare `--changelog` (without a
bump argument) is a no-op in this port.

## What to do instead

Edit `CHANGELOG.md` directly for the already-released version, then use `/commit-sdlc` to
commit the change. There is no dedicated tool-backed workflow for this in the current port.
