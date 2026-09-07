# sdlc-plugin

A Claude Code plugin that automates software development lifecycle workflows
(commit, version, PR, plan, review, ship, Jira sync) through an MCP server and
a set of session/tool-use hooks. The plugin itself is a small shell launcher;
the actual logic is a single compiled Go binary (`sdlc`) fetched on first use
and cached locally.

This repository is both the plugin and its own marketplace: plugin name
`sdlc`, marketplace name `sdlc-plugin` (`.claude-plugin/plugin.json`,
`.claude-plugin/marketplace.json`).

New to this plugin? See [`docs/getting-started.md`](docs/getting-started.md)
for install, project setup, and the day-to-day skill workflow. This README
covers how the plugin works internally.

## Install

```
/plugin marketplace add rnagrodzki/sdlc-plugin
/plugin install sdlc@sdlc-plugin
```

Then reload plugins so Claude Code picks up `.mcp.json` and `hooks/hooks.json`:

```
/reload-plugins
```

Reload plugins (or restart the session) again any time `.mcp.json` or
`hooks/hooks.json` change, including a plugin update — neither file is
picked up mid-session without one.

### First-run binary fetch

`.mcp.json` and every entry in `hooks/hooks.json` invoke the same script,
`bin/sdlc-launcher.sh` (the only file under `bin/` checked into git — see
`.gitignore`). On each invocation the launcher:

1. Reads the plugin version from `.claude-plugin/plugin.json`.
2. Looks for a cached binary at
   `${SDLC_CACHE_DIR:-$HOME/.sdlc-cache}/bin/sdlc-<version>-<os>-<arch>`.
3. If missing, takes an `mkdir`-based lock, downloads that asset plus
   `checksums.txt` from the `v<version>` GitHub Release of this repo,
   verifies the SHA-256 checksum, `chmod +x`, and atomically renames it into
   the cache before exec'ing it.

The launcher never blocks or hangs a Claude Code session: any transient
failure (offline, download error, checksum mismatch, lock timeout) prints a
one-line diagnostic to stderr and exits `0` ("fail open"), so a hook simply
degrades silently and the MCP connection retries on the next attempt. The one
exception is an unsupported OS/arch, which exits non-zero since retrying
can't help.

Timeout budgets differ by mode: hook invocations (`hook <name>`) get ~1s
connect/max-time budgets, since a hook runs inside Claude Code's own hook
timeout (3-10s, see `hooks/hooks.json`) and must fail open fast rather than
attempt a real download. The MCP connect path (`mcp`) gets a 60s max-time /
65s lock timeout and is the path that actually completes a cold download.
**Practical effect:** on a cold cache, hooks (including the SessionStart
banner) may show nothing on the very first session — that's expected. Opening
the MCP connection (which `/reload-plugins` and any tool call trigger)
finishes the download; hooks work normally from the next session on.

All of the above is overridable via environment variables:
`SDLC_CACHE_DIR`, `SDLC_LAUNCHER_LOCK_TIMEOUT_S`,
`SDLC_LAUNCHER_CURL_CONNECT_TIMEOUT_S`, `SDLC_LAUNCHER_CURL_MAX_TIME_S`.

### Forcing a re-fetch

There is no re-fetch flag — the launcher only downloads when the cached
binary is absent. To force a fresh download (e.g. after a checksum failure,
or to pick up a new release under the same version), delete the cached
binary and let the next invocation re-fetch it:

```
rm ~/.sdlc-cache/bin/sdlc-<version>-<os>-<arch>
# or, to reset the whole cache (including any stale lock directories):
rm -rf ~/.sdlc-cache
```

`<version>` is the `version` field in `.claude-plugin/plugin.json`; `<os>` is
`darwin`/`linux`; `<arch>` is `amd64`/`arm64`.

## Usage

### Tool surface

The MCP server (`sdlc mcp`) registers 32 tools across 15 groups (grepped from
`internal/tools/*.go`, wired in `cmd/sdlc/main.go`):

| Group | Tools |
|---|---|
| Commit | `commit_prepare`, `commit_apply` |
| Version | `version_prepare`, `version_apply` |
| Pull request | `pr_prepare`, `pr_validate_body`, `pr_apply` |
| Plan | `plan_prepare`, `plan_mark`, `plan_explore_prepare` |
| Review | `review_prepare`, `received_review_prepare` |
| Ship | `ship_prepare`, `ship_verify_side_effect`, `ship_state` |
| Execute state | `execute_state` |
| Jira | `jira` |
| Setup & migrate | `setup_prepare`, `setup_init`, `migrate` |
| Remote/CI polling | `await_remote_review`, `verify_pipeline_await`, `verify_pipeline_classify` |
| Validation & links | `validate`, `links_validate`, `mcp_failure_record` |
| CI scaffolding | `scaffold_ci`, `verify_tag_ancestry` |
| Hardening & error reports | `harden_prepare`, `error_report_prepare` |
| OpenSpec | `openspec_enrich` |
| Dimensions rendering | `dimensions_render_instructions` |

These are consumed by the plugin's skills (under `skills/`), not typically
called by name directly.

### Migrating a Node-era (pre-v5) project

Config is read from `.sdlc/config.json` (project) and `.sdlc/local.json`
(user-local, gitignored). If any legacy pre-v5 marker file is found —
`.claude/sdlc.json`, `.claude/version.json`, `.sdlc/jira-config.json`,
`.sdlc/ship-config.json`, `.sdlc/review.json`, `.claude/review.json` — any
tool that needs config fails with an error naming the `migrate` tool
(`internal/config/config.go`, `detectLegacy`).

Ask Claude to run the `migrate` tool to move a project from the legacy
(Node-plugin era) layout to the current one. It takes one of two actions,
each independently, with an optional `dryRun`:

- `config` — schema-migrates the legacy config file(s) into `.sdlc/config.json`.
- `import` — non-destructively copies config, templates, jira-templates,
  learnings, and review-dimensions from the old plugin's data directory into
  the current one, skipping anything that already exists.

For a project that has never had any sdlc config (not a migration, a fresh
install), use `setup_init` instead — it scaffolds `.sdlc/config.json` and
`.sdlc/local.json` from scratch.

### Automation config

`.sdlc/local.json`'s `automation` section controls how much confirmation
pipeline steps require (`internal/config/config.go`):

- `mode` — `supervised` (default) or `unattended`.
- `reviewFixIterations` — max automatic review-fix loops (default 3).
- `reviewFixSeverityThreshold` — minimum severity that blocks ship without a
  fix (default `high`).
- `steps` — a per-step map overriding `mode` with `auto` or `confirm` for an
  individual pipeline step; a per-step override always wins over `mode`.

`unattended` mode plus per-step `auto` overrides is what lets a pipeline run
end-to-end (commit → version → PR → ship) without stopping for confirmation
at each step; `supervised` is the safer default for interactive use.

## Development

- Go module at the repository root; entry point `cmd/sdlc/main.go` with
  subcommands `mcp`, `hook <name>`, `version`.
- `go build ./...` and `go vet ./...` must pass before committing.
- `go test -tags integration ./...` runs the integration-tagged suite (see
  `.github/workflows/test.yml`, which runs on every PR).
- The launcher itself has no Go test — it's exercised by
  `internal/launcher/launcher_test.sh`, a bash harness that stubs `curl` on
  `PATH` to deterministically cover the happy path, checksum mismatch,
  network failure, warm-cache, and concurrent-invocation cases without any
  real network access.
- Releases are cut by `.goreleaser.yaml` (darwin/linux × amd64/arm64,
  `sdlc-<version>-<os>-<arch>` binaries, no archive format, plus a
  sha256sum-compatible `checksums.txt`), triggered by pushing a `v<version>`
  tag (`.github/workflows/release.yml`). `lefthook.yml` runs the same
  `go vet` + test steps as CI on `pre-push`.

## Troubleshooting

**"sdlc binary not ready — run any sdlc tool or re-open session" keeps
appearing.** The launcher failed open. Check the preceding
`sdlc-launcher: ...` diagnostic line on stderr:

- `download failed: <url>` — offline, or no GitHub Release exists yet for
  the version in `.claude-plugin/plugin.json`. Confirm a release tagged
  `v<version>` exists at `https://github.com/rnagrodzki/sdlc-plugin/releases`
  and that its assets include `sdlc-<version>-<os>-<arch>` and
  `checksums.txt`.
- `checksum mismatch for ... — download aborted, nothing cached` — the
  downloaded binary didn't match `checksums.txt`. Nothing is cached in this
  case; just retry (transient network corruption) or re-check the release
  assets if it persists.
- `timed out waiting for fetch lock ...` — a previous launcher invocation
  was killed mid-download and left its lock directory behind (the
  `mkdir`-based lock has no liveness check, by design — see the comment in
  `bin/sdlc-launcher.sh`). Remove it manually:
  `rmdir ~/.sdlc-cache/bin/.fetch-<version>.lock`.
- `cached binary missing or not executable: <path>` or `unsupported OS/arch`
  — see "Forcing a re-fetch" above, or file an issue if your platform isn't
  `darwin`/`linux` × `amd64`/`arm64`.

**Tools don't show up / MCP server doesn't respond.** Run `/reload-plugins`.
`.mcp.json` and `hooks/hooks.json` are only read on plugin load or explicit
reload, not on every message.

**No SessionStart banner on a brand-new install.** Expected on a cold cache
— see "First-run binary fetch" above. Trigger a fresh SessionStart with
`/clear` (matcher is `startup|clear|compact`, `hooks/hooks.json`) once the
first MCP connection has completed the download.

**Legacy config error naming `migrate`.** See "Migrating a Node-era
project" above.
