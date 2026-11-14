---
name: ci-cd-pipeline-review
description: GitHub Actions workflows and the lefthook pre-push hook must stay in sync — test.yml explicitly documents this coupling.
triggers:
  - ".github/workflows/**"
  - ".github/scripts/**"
  - "lefthook.yml"
severity: medium
---

`.github/workflows/test.yml` runs `go vet ./...` and
`go test -tags integration ./...` on every `pull_request`, and its own
comment states these two commands "are mirrored in lefthook.yml's pre-push
hook — keep them in sync." Check:

- A change to the commands, flags, or build tags in `test.yml` is mirrored
  in `lefthook.yml`'s pre-push hook, and vice versa. A one-sided edit is a
  bug even if CI still passes, because local pre-push checks would silently
  diverge from what CI enforces.
- New or modified workflow steps use pinned action versions (`uses:
  actions/checkout@v4`, not a floating tag) and least-privilege
  `permissions:` blocks.
- `check-version-bump.yml` and `release.yml` are not weakened (e.g.
  removing a required check, widening a trigger to `pull_request_target`
  without justification, or dropping a permissions restriction).
- Secrets are referenced via `${{ secrets.* }}` only, never echoed into
  logs or passed as a plain command-line argument that would appear in the
  Actions log.
- New workflow jobs that shell out reuse the project's existing tooling
  conventions rather than introducing a parallel, unreviewed script.
- `.cjs` entrypoints exporting helper functions must guard their `main()`
  invocation with `if (require.main === module)` so the module is importable
  by tests.
- New test suites (e.g. `__tests__/**`) introduced alongside a script must be
  wired into the corresponding CI workflow (`test.yml`) in the same task.
- Version-specific tool behavior: a workflow command must work across the
  tool versions a routine bump would reach, not only the pinned one. For
  example, `node --test <dir>/` works on Node 20 but fails on Node >=21 with
  MODULE_NOT_FOUND; pass an explicit glob (`<dir>/*.test.cjs`) instead. When
  a command depends on one version's behavior, say so in a comment next to
  the pinned version.
- CI/local-hook parity gaps: every check that any workflow runs (not only
  `test.yml`: also the site workflow, lint, build, Lighthouse, and Node or
  bun tests) is either mirrored in `lefthook.yml` or has a comment next to
  the step that says the gap is intentional and why. A check with neither is
  a finding. An unexplained gap reads as an accidental omission.
- Floating tool and runtime versions: a step that fetches a tool at run time
  (`npx`, `bunx`, `pnpm dlx`, `go install` with `@latest`, `pip install` with
  no `==`) names an exact package version (for example
  `@lhci/cli@<exact version>`), never a bare package name or `@latest`. Each
  step that runs `node`, `bun` or a script they run uses a runtime from a
  setup step with an explicit version (`node-version`, `bun-version` or a
  version file), never the runner default image. This covers every job,
  including a change-gate or path-filter step. A step that runs before its
  setup step is a finding.
- Sibling release workflows (`promote-release.yml`, `release.yml`,
  `release-on-main.yml`) that push or tag the same ref (e.g., release branch)
  must each have an explicit `concurrency:` block with a comment stating
  whether they share one group (to serialize pushes) or use separate groups.
  A concurrency change in one sibling must be mirrored in the other or the
  task must document why they differ. Ensure `cancel-in-progress` is `false`
  on any job that pushes or tags, and verify `permissions:` and trigger
  filters are synchronized across siblings pushing the same target.
- A new or changed workflow that builds, tests or deploys a directory (for example a site workflow for `site/`) is described in `README.md` or a page under `docs/internal/`: its trigger, its checks, its deploy target and its pinned tool versions. Check: grep `README.md` and `docs/internal/` (exclude `node_modules`) for the workflow file name. A workflow with no match is a finding. A change to its trigger, its checks or its deploy target that leaves that text stale is a finding.
