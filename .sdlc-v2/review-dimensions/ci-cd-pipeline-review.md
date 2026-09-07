---
name: ci-cd-pipeline-review
description: GitHub Actions workflows and the lefthook pre-push hook must stay in sync — test.yml explicitly documents this coupling.
triggers:
  - ".github/workflows/**"
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
