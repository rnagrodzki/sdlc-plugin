# Init Workflow (not available in this port)

This file documents Branch A ("Init") of the original `version` skill. It is kept for
reference only.

## Why it is unavailable

The Go/MCP port's `version_prepare` tool always reports `flow: "release"` — there is no
`"init"` flow value it can return, and no separate init tool exists among the registered MCP
tools. The original Init flow (first-time version-file creation, initial `CHANGELOG.md`
scaffold, and first tag) has no backing implementation in this port.

`version`'s main `SKILL.md` never dispatches here. If a project has no supported version
file (`package.json`, `plugin.json`, `Cargo.toml`, `pyproject.toml`, `pubspec.yaml`, or
`VERSION`), `version_prepare` fails outright with an error instead of offering to create one.

## What to do instead

Create the version file yourself (for example, add a `VERSION` file containing `0.1.0`, or add
a `version` field to an existing manifest such as `package.json`), commit it through
`/commit`, and then re-run `/version` for subsequent releases.
