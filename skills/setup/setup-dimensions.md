# Dimensions Sub-Flow

Project-aware dimension creator: consumes tech stack scan results from the
parent, proposes tailored dimensions with evidence, lets the user select,
writes files, and validates with the `validate` MCP tool.

> **Port Notes** (Task 44 KD9 rewrite):
> - `validate-dimensions.js` → `validate({ action: "dimensions" })` (Step 2, Step 7).
> - `skill/review.js --json` (uncovered-file evidence in `--add` mode) has no Go
>   equivalent and is dropped — no substitute exists in the ported tool surface.
>   `--add` mode proceeds using only the installed-dimension scan.
> - The source's `review/REFERENCE.md` (dimension format spec) and
>   `EXAMPLES.md` (5 sample dimensions) were never ported as standalone files —
>   `skills/review/SKILL.md` covers the review pipeline only, not dimension
>   authoring. This file inlines a compact frontmatter spec (below, sourced from
>   `internal/dimensions.Validate`'s D1-D13 rules) in their place. There are no
>   copy-paste example bodies to start from; author each dimension directly from
>   scan evidence using the spec table.
> - `dimension-to-instructions.js` → `dimensions_render_instructions` MCP tool
>   (Step 8).
> - `lib/dimensions.js::readCommonPrompt` (the inline `node -e` reading
>   `_common.md`) is now handled inside `dimensions_render_instructions` itself
>   — pass `commonFile` and the tool reads it. No separate read step needed.

> **CRITICAL — Inline output only.** Always produce dimension proposals, evidence
> citations, and trigger patterns directly in your current response. Never write
> "the simulated output is complete above" or defer to a prior response.

> **Permission context:** This sub-flow inherits the parent skill's permission mode.
> Do NOT call ExitPlanMode, change permission settings, or exit any mode during this
> sub-flow. Do NOT ask the user to approve file writes individually — the parent
> (setup) manages mode transitions. Write all dimension files in rapid
> succession to minimize permission prompts.

---

## Dimension Frontmatter Spec

Every dimension file is Markdown with YAML frontmatter followed by a body
(checklist etc., minimum 10 characters). Fields (source of truth:
`internal/dimensions.Validate`):

| Field | Required | Type | Constraint |
|---|---|---|---|
| `name` | yes | string | lowercase letters, digits, hyphens only; max 64 chars |
| `description` | yes | string | non-empty; max 256 chars |
| `triggers` | yes | array of strings | non-empty; each entry a valid glob |
| `severity` | no | string | one of `critical`, `high`, `medium`, `low`, `info` |
| `skip-when` | no | array of strings | valid globs |
| `max-files` | no | integer | positive |
| `requires-full-diff` | no | boolean | — |
| `model` | no | string | non-empty |

Unknown frontmatter fields are flagged (warning, not error). The body must be
at least 10 characters after the frontmatter block.

---

## Arguments

- `--add` — expansion mode: propose only dimensions not already installed
- `--no-copilot` — skip GitHub Copilot instructions entirely (bypasses the GitHub hosting prompt)

---

## Scan Input

This sub-flow expects the parent skill (setup) to have provided the
following scan results in its Step 3.S "Scan phase":

- **Dependency manifests** — contents of package.json, requirements.txt, pyproject.toml, setup.py, go.mod, Cargo.toml, pom.xml, build.gradle, Gemfile
- **Framework/config signals** — .github/workflows/*.yml, .eslintrc*, tsconfig.json, lerna.json, pnpm-workspace.yaml, nx.json, *.graphql, openapi.yaml, openapi.json, openspec/config.yaml
- **Directory structure** — glob results for all patterns in `@scan-patterns.md`
- **CI/CD config** — .github/workflows, .circleci, Jenkinsfile presence
- **Database signals** — migrations/ dir, *.sql files, Prisma/Alembic/Flyway presence
- **Test structure** — *.test.*, *.spec.* files, test runner configs
- **Existing review dimensions** — what is already installed in .sdlc/review-dimensions/
- **GitHub hosting detection** — output of the multi-signal cascade (git remote, gh CLI, .github/ dir)

---

## Workflow

### Step 2 — Discover Existing Dimensions

Check `.sdlc/review-dimensions/` (Glob `.sdlc/review-dimensions/*.md`) for
already-installed dimension files.

In `--add` (expansion) mode:

- Call `validate({ action: "dimensions" })`. Findings reference existing
  installed files by path; read each installed file directly (Read tool) to
  extract its `triggers` list — new proposals must avoid identical globs.
- The source's `skill/review.js --json` uncovered-file evidence step has no
  Go equivalent and is skipped (see Port Notes). Proceed with the installed-
  dimension scan only.

If NOT in expansion mode: skip this step.

---

### Step 3 (PLAN) — Propose Dimension Catalog

Read `./dimension-catalog.md` for dimension definitions. Propose dimensions
matching detected evidence from Core and Extended sections.

**Rules:**

- Only propose a dimension if there is concrete evidence
- Always include `code-quality-review` as the baseline
- In `--add` mode: exclude installed dimensions
- Distinguish `api-review` (route/controller code quality) from `api-contract-review` (schema file changes); flag if both are proposed
- Distinguish `documentation-review` (docs presence/structure) from `documentation-quality-review` (docs content accuracy); flag if both are proposed
- When `openspec/config.yaml` is detected, propose `spec-compliance-review` (high severity) — this dimension verifies that code changes satisfy the delta spec requirements from the active OpenSpec change. The dimension body should reference `openspec/changes/*/specs/` as the authoritative requirements source and include checklist items for: every ADDED requirement has corresponding implementation, every MODIFIED requirement's changes are reflected in code, no REMOVED requirements still have active code paths.

For each proposed dimension, prepare: name (lowercase-hyphenated), description
(one sentence, max 256 chars), why relevant (cite specific evidence), trigger
patterns (match actual directory names), skip-when patterns, and a tailored
body checklist — following the Dimension Frontmatter Spec above.

**Customization is mandatory** — reference the project's actual stack in the
body (e.g., "Check SQLAlchemy ORM usage — avoid raw `session.execute()` with
string concatenation", not just "avoid raw SQL").

---

### Step 4 (CRITIQUE) — Evaluate Proposals

| Gate | Check |
|---|---|
| Trigger specificity | Any trigger matching `**/*` or broader? Tighten to actual directories found. |
| Overlap | Two proposed dimensions share identical trigger file sets? Flag for merging. |
| Evidence quality | Each proposal backed by concrete scan findings? Remove any without evidence. |
| Instructions tailored | Each body references project's specific framework by name? Add context if not. |
| Expansion gaps (`--add`) | Project patterns not covered by existing OR proposed dimensions? |
| Trigger validity | Patterns conform to glob syntax (no `***`, balanced brackets)? |
| Dimension distinction | If both `api-review`+`api-contract-review` or both doc variants proposed, confirm both are justified. |
| Spec conformance | Every proposal satisfies the Dimension Frontmatter Spec's required fields and constraints. |

---

### Step 5 (IMPROVE) — Refine Proposals

Based on the critique: tighten broad triggers, resolve overlaps (merge or
split for exclusive coverage), add project-specific framework names to each
body, remove dimensions without concrete evidence.

---

### Step 6 (DO) — Present and Create

**Present proposals** as a numbered list with evidence summaries:

```text
Proposed review dimensions for this project:

1. code-quality-review (medium severity) — always included
   Coverage: **/*.ts, **/*.tsx
   Why: TypeScript project with 47 source files

2. security-review (high severity)
   Coverage: **/middleware/**, **/auth/**, **/*auth*
   Why: Found `jsonwebtoken` and `passport` in package.json; src/auth/ with 8 files
```

Use AskUserQuestion to ask: "Install which dimensions?" Options: **all** /
**select** (comma-separated numbers) / **cancel**.

For each selected dimension:

1. Ensure `.sdlc/review-dimensions/` exists (create if needed).
2. Write the full dimension file (frontmatter + tailored body) per the
   Dimension Frontmatter Spec, customized with project-specific evidence.
3. Confirm each file written with its path.

---

### Step 7 — Validate Installation

Call `validate({ action: "dimensions" })`.

- **Findings present:** show them (id, severity, message, path). Use
  AskUserQuestion: "Fix these validation errors automatically? (yes / no)".
  On `yes`, correct the offending file(s) per the Dimension Frontmatter Spec
  and re-validate.
- **Tool call itself errors (infra failure):** this is a prepare-tool-class
  failure — offer to invoke error-report, provide: Skill=setup,
  Step=Step 7 — Validate Installation, Operation=validate dimensions,
  Error=`<tool error message>`.

Present a summary table of files checked and their status. If any file has
findings, show the detail and offer to fix automatically.

---

### Step 8 (COPILOT) — Propose GitHub Copilot Review Instructions

**Skip this step if:** Step 7 reported any unresolved findings, or
`copilotEnabled` is false (determined during the parent's Step 1 GitHub
hosting detection).

**Check existing state:** Glob `.github/instructions/*.instructions.md`. If
files exist with the same names as selected dimensions, confirm overwrite. In
`--add` mode: when `.sdlc/review-dimensions/_common.md` exists and contains
new or changed content, regenerate ALL existing mirrors (not just newly added
ones) to incorporate the common instructions; otherwise, only generate for
newly added dimensions.

**PLAN — map dimensions to files:** Show the proposed
`.github/instructions/<name>.instructions.md` list with `applyTo` and
estimated char count. Use AskUserQuestion: "Generate these Copilot instruction
files?" Options: **yes** / **no** / **select** (numbers).

**CRITIQUE — before writing:**

| Check | Rule |
|---|---|
| Broad `applyTo` | Flag any pattern that is `**/*` or `**` alone for tightening. |
| 4,000-char overflow | Estimate char count; flag any that exceed the limit. |
| Duplicate `applyTo` | Note overlapping patterns across files (acceptable but worth flagging). |

**IMPROVE — before writing:** Condense instructions exceeding 4,000 chars
(remove verbose prose, keep checklist and severity table; if still over,
truncate and add `<!-- truncated to fit 4,000-char Copilot limit -->`).
Tighten broad `applyTo` patterns.

**DO — write files:** for each selected dimension:

```
dimensions_render_instructions({
  file: ".sdlc/review-dimensions/<name>.md",
  commonFile: ".sdlc/review-dimensions/_common.md",
}) → { ok, path }
```

Omit `commonFile` if `_common.md` does not exist — the tool tolerates a
missing common file and omits the Common Review Instructions section. If the
call errors (unparseable dimension), display the error and skip this
dimension.

Confirm each file with its returned `path`. Print a final summary listing all
generated files.

**Field mapping reference (for understanding the transformation):**

| Dimension field | Copilot field | Transformation |
|---|---|---|
| `triggers` (array) | `applyTo` (string) | Join with `,` |
| `description` | Opening paragraph | Used as-is |
| `severity` | Header note | "Default severity: {value}" |
| `_common.md` | Common Review Instructions section | Injected after severity, before Checklist (if present) |
| Body checklist | Checklist section | Strip `- [ ]` → `- ` |
| `skip-when` | Note section | Advisory text |
| `max-files`, `requires-full-diff`, `model` | — | Omit |

---

## Quality Gates

- Every proposed dimension cites specific project evidence (file paths, dependency names, counts)
- Every created dimension passes `validate({ action: "dimensions" })` with no findings
- No duplicate dimension names (including against existing dimensions in `--add` mode)
- All trigger patterns reference the project's actual directory structure, not generic defaults

---

## Error Recovery

| Error Type | Example | Invoke error-report? | Recovery Action |
|---|---|---|---|
| User error — missing tools | `gh` not authenticated | No | Tell user clearly. Provide the command to fix it. |
| User error — wrong flags | `--add` with no dimension name | No | Show correct usage. Ask user to re-invoke. |
| Stale cache | Dimension metadata out of sync | No | Re-run the scan from scratch (parent Step 3.S). |
| `validate` tool call errors (infra failure) | MCP tool crash | Yes | Invoke error-report with full context. |
| `validate` findings present | Invalid dimension file format | No | Show findings; let user correct manually or auto-fix. |

---

## Gotchas

- **Dimension file naming collision.** In `--add` mode, the skill derives a filename from the dimension name. If a file with that name already exists, it will be silently overwritten. Always check before writing.
- **Glob pattern count explosion.** The parent's scan phase runs many glob patterns. On large monorepos this can produce thousands of matches — if Glob returns >500 paths, sample the first 20.
- **Copilot step gated on gh auth.** Check `gh auth status` at the start of Step 8; skip gracefully if unauthenticated rather than failing mid-workflow.
- **GitHub hosting detection is multi-signal.** The primary `git remote -v` check may miss custom SSH aliases (e.g., `github-rn:org/repo`), but `gh repo view` (Signal 2) resolves the actual remote URL regardless of local SSH config. The `.github/` directory fallback (Signal 3) is a weak heuristic — it can false-positive for repos that have GitHub Actions config but are hosted elsewhere. If all three signals fail, use `--no-copilot` to bypass.

---

## DO NOT

- Do NOT create dimension files without first running the tech stack scan (parent Step 3.S)
- Do NOT skip the `validate({ action: "dimensions" })` step — invalid dimensions cause review to fail silently
- Do NOT overwrite existing dimension files without explicit user consent
- Do NOT propose more than 10 dimensions at once — offer expansion in follow-up runs with `--add`
- Do NOT invoke `error-report` for user errors or validation findings — only for `validate` tool infra failures

---

## What's Next

After setting up review dimensions, common follow-ups include:
- `/review` — run a code review with the new dimensions
- `/commit` — commit the dimension files

## See Also

- `./dimension-catalog.md` — evidence-to-dimension mapping used in Step 3
- [`/review`](../review/SKILL.md) — uses the dimensions created by this skill
- `/setup --pr-template` — custom PR template configuration
- `/setup --guardrails` — plan mode guardrails configuration
