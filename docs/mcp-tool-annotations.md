# MCP Tool Annotations

Every tool declares all five annotation fields: Title, ReadOnly, Destructive, Idempotent, and OpenWorld. mcpserver.Register requires an mcpserver.Annotations value (compile-enforced by Task 2). This document states when to set each field and how to classify new tools.

## Title

Title is a short display name (2-5 words), not a restatement of Description. Examples: "Create a git commit", "Validate SDLC artifacts", "Prepare plan state and template". It is distinct from the full description (which is passed separately to Register).

## ReadOnly Boundary Rule

A tool may declare ReadOnly:true only if **both** conditions hold:

1. **Every path it writes is gitignored or under os.TempDir()** — checked with `git check-ignore`, not by eyeballing the .sdlc-v2/ prefix. Some paths are force-tracked (e.g., .sdlc-v2/config.toml and .sdlc-v2/review-dimensions/*.md) and disqualify a tool even if they live under .sdlc-v2. Writes into .git count too: git fetch writes .git/refs/tags and disqualifies a tool even though git status stays clean.

2. **The caller cannot choose that path** — a path derived from an input field fails this condition even when it normally lands in tempdir. For example, poll_await's state_file parameter is caller-redirectable, so poll_await is not read-only despite taking paths that could land in gitignored locations.

Both conditions must hold. If either is uncertain, set ReadOnly:false (the safe default).

**Exception: plan tools.** plan_prepare, plan_mark, plan_support, and plan_explore_prepare keep ReadOnly:true, because the plan skill runs in Claude Code plan mode and needs them callable there (see docs/plan-architecture.md). Today only plan_support takes path-selecting inputs. Its runId picks .sdlc-v2/runs/<runId>.evidence/. That input only selects a run inside a fixed gitignored directory; it cannot change the directory. state.LoadRun rejects any runId that is not a bare filename matching the plan state-file grammar, before any path join. Its openspec_stage action takes changeName and files[].path, which pick <main-worktree>/.sdlc-v2/openspec-staging/<changeName>/ and files inside it. openspec.ValidChangeName rejects any changeName that is not bare kebab-case, and openspec.Stage rejects any files[].path that is not a clean relative path matching an artifact outputPath; both checks run before the first path join or write. Its planPath input is only stored as text in stage.json, never joined. Its preplan_context action takes topic, which picks <main-worktree>/.sdlc-v2/preplan/<slug>.md or YYYY-MM-DD-<slug>.md. It reads at most 10001 names of that folder (Readdirnames) before any create. branch.Slug keeps only [a-z0-9-], and an empty slug is rejected before the path join. The create uses O_EXCL. It never replaces a file that exists. When the file exists and lacks a section heading, it adds the absent headings, which is additive. When a code fence is still open at the end of the file, it also adds one closing fence line before the headings, which is additive too. Before the read it refuses a link, a folder or a pipe, and a file over 8 MiB. It also refuses a repair that would make the file larger than 8 MiB. It writes a temp file in the same folder, then renames it, and the file keeps its mode. It writes no file that has every heading, so a second call writes nothing. Its preplan_check action takes the same topic and finds the file the same way. It rewrites an existing regular file in that folder: it replaces the first **Guardrail check:** line in the first 4096 bytes, which is a destructive update, or, on the first check, adds the line after the **Started:** or **Status:** line, which is additive. It refuses a link, a folder or a pipe, and a file over 8 MiB, before the read, and it writes a temp file in the same folder, then renames it, and the file keeps its mode. Each call writes a new Last check time, so two equal calls give two different files. Its preplan_write action takes the same topic and finds the file the same way. It rewrites an existing regular file in that folder and never creates one. It replaces the body of one section or the first **Status:** line in the first 4096 bytes, which is a destructive update. It also deletes the Open questions or Guardrail check section when content is an empty string, which is destructive too. When the section is the last one and a code fence in its body never closes, the replace or the delete runs to the end of the file and also removes the text after the fence, which is destructive too. It adds an absent Open questions section after Decisions, or a **Status:** line when the first 4096 bytes hold none, which is additive. It refuses a link, a folder or a pipe, and a file over 8 MiB, before the read, and it refuses a write that would make the file larger than 8 MiB. It writes a temp file in the same folder, then renames it, and the file keeps its mode. Two equal calls give the same file. With topicStatus ready for plan it also reads the first 4096 bytes of the newest 100 topic files in that folder, this file included, ignores older files, and writes none of them. Its section, content and topicStatus inputs are checked against fixed lists and rules before any file is read, and none of them selects a path. The plan-tools exception keeps ReadOnly:true for preplan_check and preplan_write too; Idempotent has no meaning under ReadOnly:true. openspec_instructions and openspec_stage also write a temp copy under os.TempDir() for the openspec CLI. Both refuse with a DomainError when the active worktree cannot be resolved, because they read openspec/config.yaml and the specs of the active worktree. openspec_stage writes the staging folder under the main worktree. Condition 1 still applies in full. A plan tool keeps this exception only while every path input passes the same kind of check before any path join.

## Writer Annotation Fields

For tools with ReadOnly:false, spell out all four bools explicitly. Go's zero value is false for Destructive and OpenWorld; the MCP spec's default for both is true. **Omitting them under-claims danger.** Destructive and Idempotent are meaningless under ReadOnly:true and may be left at zero there. OpenWorld must never be omitted — set it deliberately on every row, read-only or writer.

### Destructive

Destructive:true indicates the tool may perform destructive (non-additive) updates — e.g., deleting a file, overwriting a section, moving or renaming something, or any operation that loses prior data. When in doubt, declare true; over-restricting is safe.

### Idempotent

Idempotent:true indicates repeating a call with the same arguments has no further effect. A tool that edits a file in place (e.g., rewriting a config block) is idempotent. A tool that appends or creates new state entries is idempotent only if re-running it with identical input leaves the state unchanged (e.g., an append-only log that checks for duplicate entries before appending). When in doubt, declare false.

### OpenWorld

OpenWorld:true indicates the tool reaches external entities: network (HTTP, git remote, GitHub API), cloud services, or any caller-controlled endpoint. Local-only tools (pure string processing, local git, tempdir writes) are false. **This field is required on every tool, read-only or writer.** Go's zero value is false; the MCP spec's default is true. Never omit it.

## Zero-Value Hazard

Go's zero value for bool is false. A tool whose author forgot to set Destructive:true will silently default to false, under-claiming danger. The golden map in internal/tools/annotations_test.go is the only guard; a missing Destructive entry or a mismatch will fail TestEveryToolMatchesItsAnnotationDecision.

When a value is not verified in code (or the code is too complex to verify), declare the more dangerous one and say so in the reason: Destructive:true, Idempotent:false, OpenWorld:true. Over-restricting is safe; under-restricting is not.

## Conservative Default

New tools generated by the scaffolder agent default to:
- ReadOnly: false
- Destructive: true
- Idempotent: false
- OpenWorld: true
- reason: "not audited — conservative default"

This ensures new tools are honest rather than optimistic. The developer audits the tool against the boundary rule and the five bools, then updates the scaffolder's output to the true values before landing the tool.

## Re-Audit Trigger

Re-audit the classification when any of these change:

- A new tool is registered (add it to toolAnnotations in internal/tools/annotations_test.go)
- internal/mcpserver/register.go is modified (e.g., new fields on Annotations)
- internal/state, internal/telemetry, internal/fsx, internal/gitx, or internal/ghx is refactored (these are the write/network surfaces)
- A tool's imports change (e.g., a read-only tool starts importing ghx)
- A refactor moves a write between tools (e.g., moving a state.Write from one tool to another)

Re-audit means re-deriving each read-only tool's write set from its call closure, not re-reading the table. If a tool now imports a module it previously did not, check whether the import adds a write or network call.

## Golden Data — internal/tools/annotations_test.go

The test file toolAnnotations map is the source of truth. It lists all 33 tools with all five annotation values and a reason for each. A tool missing from the map or a value mismatch fails TestEveryToolMatchesItsAnnotationDecision.

TestReadOnlyToolsWriteNothingTracked runs every read-only tool against a minimal fixture repo and verifies git status --porcelain is empty afterward. Tools requiring external dependencies (links_validate, received_review_prepare, received_review_verify) are skipped with the reason recorded in the map, so skipping is visible rather than silent.
