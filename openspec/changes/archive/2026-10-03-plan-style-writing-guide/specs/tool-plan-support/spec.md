# Spec Delta

## MODIFIED Requirements

### Requirement: openspec_stage
The `openspec_stage` action SHALL replace the staging dir for `changeName` with the given `files`, write `stage.json`, and validate a temp copy, as defined by the `openspec-staging` capability. It SHALL also check every staged `.md` file for hard-to-read Mermaid colors, as defined by "Diagram contrast" in the `plan-writing-style` capability.

| Input | Encoding | Example |
|---|---|---|
| `changeName` | plain text, bare kebab-case | `add-widget` |
| `files` | JSON array of `{path, content}`; `path` relative to the change dir | `[{"path":"proposal.md","content":"# Proposal\n..."}]` |
| `planPath` | plain text, absolute path | `/Users/me/.claude/plans/add-widget.md` |

| Output | Meaning |
|---|---|
| `stagingDir` | Repo-relative staging dir, e.g. `.sdlc-v2/openspec-staging/add-widget/` |
| `files` | `[{path, sha256}]` as written to `stage.json` |
| `valid` | `true` when `openspec validate <changeName> --strict` exits 0 in the temp copy and no staged file has a diagram contrast hit |
| `validateOutput` | CLI output of that validation, then one line `diagram contrast: <path>:<line>: <reason>: <text>` per hit |

- Each call replaces the whole staging dir, so a file dropped from `files` is removed.
- `next` is `Staged and valid. Add the **OpenSpec-Staging:** header to the plan.` when `valid`, else `Fix the artifacts using validateOutput and call openspec_stage again.`
- The tool keeps `ReadOnly: true`: it writes only gitignored paths and the OS temp dir, and no path comes from a caller field without the name and path checks.

| Condition | Class | Message (short) |
|---|---|---|
| `changeName` empty or not bare kebab-case | `DomainError` | `openspec_stage: invalid changeName "<name>"` |
| A `files[].path` fails the path check | `DomainError` | `openspec_stage: path "<path>" not allowed` |
| `openspec` CLI not on PATH | `InfraError` | `openspec CLI not found on PATH` |
| Active worktree cannot be resolved | `DomainError` | `openspec_stage: active worktree not resolved` |

#### Scenario: Stage and validate
- **WHEN** `openspec_stage` is called with `changeName:"add-widget"` and valid proposal, spec, design, and tasks files
- **THEN** `stagingDir` is `.sdlc-v2/openspec-staging/add-widget/`
- **AND** `valid` is `true`
- **AND** `git status --porcelain` prints nothing

#### Scenario: Restage drops a file
- **WHEN** a second call for `add-widget` omits `design.md`
- **THEN** `.sdlc-v2/openspec-staging/add-widget/design.md` no longer exists

#### Scenario: Pastel diagram in a staged artifact
- **WHEN** line 30 of the staged `proposal.md` is `classDef new fill:#d4f7d4,stroke:#2a7a2a` inside a mermaid block and the CLI validation passes
- **THEN** `valid` is `false`
- **AND** `validateOutput` contains `diagram contrast: proposal.md:30: no text color: classDef new fill:#d4f7d4,stroke:#2a7a2a`
