# Installing from a local clone

Use this instead of [`getting-started.md`](getting-started.md) when you're
developing the plugin itself, testing an unreleased change, or working
offline — anything where you want Claude Code to load the plugin from a
directory on disk rather than from GitHub.

## Prerequisites

Same as [`getting-started.md`](getting-started.md#prerequisites): Claude Code
with plugin marketplace support, `git`, and `gh` if you'll use `pr` / `ship` /
`verify-pipeline`.

## 1. Clone the repo

```
git clone https://github.com/rnagrodzki/sdlc-plugin.git
cd sdlc-plugin
```

If you already have a local checkout (e.g. this working copy), skip this step
and use its path instead.

## 2. Add it as a local marketplace

The repo is its own marketplace — `.claude-plugin/marketplace.json` lists the
`sdlc` plugin with `"source": "./plugins/sdlc"`. Point Claude Code at the
repo directory instead of the GitHub shorthand:

```
/plugin marketplace add /absolute/path/to/sdlc-plugin
/plugin install sdlc@sdlc-plugin
/reload-plugins
```

Use an absolute path, not `~` or a relative path. If you're already inside
the repo, `/plugin marketplace add .` also works.

## 3. Picking up local changes

A local marketplace add does not re-copy files on every session — it points
Claude Code at the path you gave it. To pick up edits:

- Changes to `plugins/sdlc/hooks/hooks.json` or `plugins/sdlc/.mcp.json`: run
  `/reload-plugins`.
- Changes to skills, agents, or templates: run `/reload-plugins`, or start a
  new session.
- Changes to the Go binary source (`cmd/`, `internal/`):
  `plugins/sdlc/bin/sdlc-launcher.sh` execs a compiled binary from
  `~/.sdlc-cache/bin/`, not your source tree —
  `task build` alone only produces `./sdlc` locally and the launcher never
  sees it. Run `task deploy` (build + copy into the launcher's cache), then
  `/reload-plugins`. Full detail on how the launcher resolves and caches the
  binary: `README.md` → "First-run binary fetch".

## 4. Removing it

```
/plugin uninstall sdlc@sdlc-plugin
/plugin marketplace remove sdlc-plugin
```

Then, if you want the GitHub-hosted version instead, follow
[`getting-started.md`](getting-started.md#1-install-the-plugin).

## Next steps

Continue at [`getting-started.md`](getting-started.md#2-set-up-a-project)
("Set up a project" onward) — project setup and the day-to-day workflow are
the same regardless of where the plugin was installed from.
