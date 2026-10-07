# Design

## Context

- Motivation: see proposal.md - Why.
- All readers of personal settings go through one package (`internal/config`). One merged loader covers every reader.
- The comment-safe write (`internal/config/splice.go`) exists in the code, but release 0.3.2 does not have it. This change closes its gaps. It does not add a second writer.
- The prompt hook gets every injected turn through the same event as typed text. The report reads at most 100 entries, so notices push out real prompts.

## Goals / Non-Goals

**Goals:**
- Keep mechanical work (classify, merge, check, write) in Go. Skills keep the judgment: guardrail repair and the save-target question.
- No new MCP tool. Extend `validate`, `setup_write_sections`, and `setup_prepare`.

**Non-Goals:**
- No release or version bump.
- No migration or version stamp for the user file. Moved-key migrations act only on the project file.
- No update of an old tip above a key that already has a comment.
- No keep of comment lines inside a multi-line value when the value changes.
- No change to the harden step inside ship.

## Architecture

Touched parts, grouped by layer:

```mermaid
flowchart TB
  subgraph Skills
    SH["harden SKILL.md"]
    SS["setup SKILL.md"]
    HO["harden-orchestrator agent"]
  end
  subgraph MCPTools["MCP tools"]
    V["validate guardrails"]
    W["setup_write_sections"]
    SP["setup_prepare"]
    SR["ship report"]
  end
  subgraph Hooks
    HP["record-user-input"]
    HA["record-user-answer"]
  end
  subgraph Internal["internal packages"]
    C["config: loader and merge"]
    SPL["config: splice and tips"]
    UI["tools: user input evidence"]
  end
  subgraph External["filesystem"]
    UF["~/.sdlc/local.toml"]
    PF[".sdlc-v2/local.toml"]
    CF[".sdlc-v2/config.toml"]
  end
  HO --> SH
  SH --> V
  SH --> W
  SS --> SP
  SS --> W
  W --> SPL
  SPL --> UF
  SPL --> PF
  SPL --> CF
  SP --> C
  C --> UF
  C --> PF
  HP --> UI
  HA --> UI
  UI --> SR
  classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px
  classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px
  class HA,UF new
  class SH,SS,HO,V,W,SP,SR,HP,C,SPL,UI changed
```

## Runtime flow

Harden applies one guardrail proposal:

```mermaid
sequenceDiagram
  participant U as user
  participant S as harden skill
  participant V as validate
  participant W as setup_write_sections
  participant F as filesystem
  S->>V: guardrails + candidatesJson
  V-->>S: findings with fix
  S->>S: repair: shorten or split (max 2 rounds)
  S->>V: guardrails + candidatesJson
  V-->>S: no findings
  S->>W: sectionsJson plan.guardrails.<id> (full leaf)
  W->>F: in-place edit of .sdlc-v2/config.toml, comments kept
  S->>V: guardrails (disk)
  V-->>S: no findings
  S-->>U: 5d summary with Repaired
```

## Persisted state

Where one local key gets its value:

```mermaid
stateDiagram-v2
  [*] --> BuiltIn: no file sets the key
  BuiltIn --> User: key set in ~/.sdlc/local.toml
  User --> Project: key set in .sdlc-v2/local.toml
  BuiltIn --> Project: key set in .sdlc-v2/local.toml
  Project --> User: key removed from the project file
  User --> BuiltIn: key removed from the user file
```

Template change (`plugins/sdlc/templates/local.toml`):

```diff
 [ship]
 # Auto-run full pipeline without confirmation prompts.
-auto = true
+# auto = false
-steps = ["execute", "commit", "review", "harden", "pr", "verify-pipeline"]
+# steps = ["execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"]
-quick = ["execute", "commit", "review"]
+# No built-in default: when unset, --quick has no steps to run.
+# quick = ["execute", "commit", "review"]
```

Every other `[ship]` key keeps its value and becomes a commented example.

## Data contracts

| Struct | Field | Type | Encoding | Example |
|---|---|---|---|---|
| `ValidateIn` | `candidatesJson` | string | JSON array | `[{"id":"dry","description":"…","severity":"error"}]` |
| `SetupWriteSectionsIn` | `target` | string (enum) | plain text | `user` |
| `SetupPrepareOut` | `userConfigPath` | string | plain text | `/Users/me/.sdlc/local.toml` |
| `SetupPrepareOut` | `localValues` | map | JSON object | `{"communication-style":{"values":{"audience":"technical"},"sources":{"audience":"user"}}}` |
| `sectionRow` | `defaultTarget` | string | plain text | `user` |
| `UserInputEntry` | `kind` | string | plain text | `answer` |

## Decisions

| Decision | Chosen | Rejected alternatives | Reason |
|---|---|---|---|
| Where to filter notices | One classifier used by the hook and the report | Filter only in the report | The 100-entry window fills with notices |
| Harden apply order | Check in memory, repair, write once | Write, check, revert | Revert deletes the learned rule |
| Harden write path | `setup_write_sections` | Plain file edit | Keeps comments. No new tool |
| TOML writer | Keep the in-place splice | New TOML library | No Go TOML library keeps comments on a full rewrite |
| List merge | Project list replaces the user list | Append lists | With append, a project cannot remove a user entry |
| User file path | `~/.sdlc/local.toml`, `SDLC_USER_CONFIG` overrides | XDG path | User choice. The env var also isolates tests |
| Template `[ship]` keys | Commented examples with built-in defaults | Keep live keys | Live keys hide the user file |

## Risks / Trade-offs

| Risk | Impact | Mitigation |
|---|---|---|
| A bad merge gives wrong values to every skill | High | Table tests: user only, project only, both, nested, list replace |
| Tests read the real user file of the developer | Wrong test results | `TestMain` sets `SDLC_USER_CONFIG` to an empty temp path in each package that reads settings |
| The splice uncomments the wrong line | Config damage | Never match inside a multi-line value. Decode check after each write; on mismatch the write fails |
| New projects lose `harden` and `verify-pipeline` from default steps | Different ship pipeline | Documented in getting-started. Set once in the user file |
| Answer payload location is not pinned by a fixture | No answers recorded | Hook reads `tool_input.answers`, then `tool_response.answers`. Manual check after deploy |

## Migration Plan

- No data migration. A project with no user file behaves as before.
- Rollback: revert the commit. The user file is then ignored, and the project file still works.
