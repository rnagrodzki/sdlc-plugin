---
name: jira-sdlc
description: "Use this skill when creating, editing, reading, viewing, searching, transitioning, commenting on, or linking Jira issues using Atlassian MCP tools. Caches project metadata (custom fields, workflows, transitions, user mappings) to eliminate redundant discovery calls. Supports multi-project repos via jira.projects, and skipping workflow discovery for CI. Arguments: [--project <KEY>] [--force-refresh] [--init-templates] [--site <host>] [--skip-workflow-discovery]. Triggers on: create jira issue, edit jira ticket, search jira, transition jira, jira comment, link jira, assign jira, log work jira, bulk jira operations, manage jira, jira template, read jira, view jira, show jira, get jira, fetch jira, jira details, add comment, comment on jira, reply to jira, jira ticket, jira issue."
user-invocable: true
argument-hint: "[--project <KEY>] [--force-refresh] [--init-templates] [--site <host>] [--skip-workflow-discovery]"
model: sonnet
---

# Managing Jira Issues

Cache Jira project metadata on first use, then execute any Jira operation — create,
edit, search, transition, comment, link, assign, worklog — using only cached values.
Eliminate all redundant discovery calls after initialization.

**Announce at start:** "I'm using jira-sdlc (sdlc v{sdlc_version})." — extract the version from the `sdlc:` line in the session-start system-reminder. If no version is in context, omit the parenthetical.

## When to Use This Skill (implements R16)

- Creating, editing, or viewing Jira issues
- Transitioning issues through workflow statuses
- Adding comments to Jira issues
- Linking two issues (blocks, relates, duplicate)
- Assigning issues to team members
- Logging work on a Jira issue
- Searching for issues via JQL
- Initializing or refreshing the project cache
- When the user asks anything Jira-related

## How This Skill Works

On first use, this skill initializes a cache at `~/.sdlc-cache/jira/<sanitizedSiteHost>/<PROJECT_KEY>.json`
containing the site's `cloudId`, issue type definitions, field schemas, workflow graphs,
link types, and user mappings. `sanitizedSiteHost` is the site URL host lowercased with
`.` replaced by `_` (e.g., `acme.atlassian.net` → `acme_atlassian_net`). The cache lives
outside the working tree and is keyed by site to support repos that map to multiple Jira
tenants. The cache is permanent by default — it does not expire on a timer. After
initialization, every subsequent operation reads exclusively from the cache. The cache is
rebuilt only when `--force-refresh` is passed or when operations fail due to stale data
(invalid transition IDs, changed field schemas). **This port does not auto-migrate
pre-v5 caches** — if a legacy `.sdlc/jira-cache/<KEY>.json` or `.claude/jira-cache/<KEY>.json`
file exists from an earlier install, it is not read or moved; either copy it to the home
layout above manually or run cache initialization fresh.

Each issue type has a description template (shipped in the skill's `templates/` directory
and customizable per project at `.sdlc/jira-templates/<Type>.md`). Templates are filled
from user context before the MCP call, producing well-structured descriptions on the first
attempt. All `{placeholder}` markers must be replaced with real content or the section
removed entirely — the API call is never made with raw placeholder text.

---

## Step 0 — Parse Arguments and Check Cache

### Arguments

| Argument | Description | Default |
|----------|-------------|---------|
| `--project <KEY>` | Jira project key (e.g., PROJ). When `jira.projects` is set, values outside the list are rejected. | Auto-detected |
| `--force-refresh` | Rebuild cache even if fresh | false |
| `--init-templates` | Copy default templates to `.sdlc/jira-templates/` | false |
| `--site <host>` | Sanitized site host (e.g., `acme_atlassian_net`). Disambiguates `check`/`load` when the same project key is cached under multiple sites. | Unset |
| `--skip-workflow-discovery` | Bypass Phase 5; cache `workflows[type] = { unsampled: true }` per non-subtask type. Transitions fall back to live `getTransitionsForJiraIssue` per issue. Use in CI. | false |

**Project key resolution (ordered fallback):** (implements R13)

1. `--project <KEY>` argument. When `jira.projects` is set (≥2 entries), a project key outside the list is rejected (the `jira` tool's `check` action returns an error).
2. Parse current git branch for `[A-Z]{2,10}-\d+` pattern (e.g., `feat/PROJ-123-fix` → `PROJ`). When `jira.projects` is set, accept only keys in the list; otherwise fall through.
3. Read `.sdlc/config.json` → `jira.defaultProject`.
4. When `jira.projects` has ≥2 entries, use AskUserQuestion with a closed list matching `jira.projects` ("Which Jira project key should I use?").
5. Use AskUserQuestion to ask: "Which Jira project key should I use? (e.g., PROJ, TEAM)".

Backward compatible: repos without `jira.projects` retain the previous 4-step behavior (1/2/3/5).

**Multi-candidate cache disambiguation:** (implements R15)

When `check` is called without `site` and the home-cache contains entries for the project key under two or more site subdirectories, the tool returns `exists: false` and `candidateSites: [<host>, …]`. Present the `candidateSites` list to the user via AskUserQuestion and re-run with `site: "<host>"`, or use `--force-refresh` to rebuild against a specific site.

### Cache Check

```
jira({ action: "check", key: "<PROJECT_KEY>", cacheDir?, site? })
→ { exists, fresh, ageHours, maxAgeHours, projectKey, cachePath, candidateSites,
    sections, templates, missing, flags: { skipWorkflowDiscovery, site }, errors, warnings }
```

**On tool error:** show the error message to the user and stop.

### Cache Status Evaluation

**Hook context fast-path:** If the session-start system-reminder contains a `Jira cache:` line with `stale`, use it to skip the `check` call and immediately prompt for `--force-refresh`. If the line shows the cache as current, proceed with `check` as normal — the tool validates more deeply than the hook's age check. The hook context is a session-start snapshot.

Read the check output:

- If `exists: false` → cache not initialized. Proceed to **Step 1**.
- If `missing` array contains required sections (`cloudId`, `project`, `issueTypes`, `fieldSchemas`) → cache incomplete. Proceed to **Step 1**.
- If `--force-refresh` passed → rebuild regardless of age. Proceed to **Step 1**.
- If `fresh: false` AND `maxAgeHours > 0` → TTL-based expiry exceeded. Proceed to **Step 1**.
- Otherwise (cache exists, complete, and either permanent or within TTL) → load cache, skip to **Step 2**.

Load cache:

```
jira({ action: "load", key: "<PROJECT_KEY>" })
→ <full cache object: version, lastUpdated, maxAgeHours, cloudId, siteUrl, currentUser,
    project, issueTypes, fieldSchemas, workflows, linkTypes, userMappings>
```

### Handle `--init-templates`

If `--init-templates` flag is present:

1. Run init-templates:
   ```
   jira({ action: "init-templates", key: "<PROJECT_KEY>", templatesDir? })
   → { initialized: [...], skipped: [...], unavailable: [...] }
   ```

2. Report: "N templates initialized (exact match), N skipped (already exist)." — `N` from `initialized.length` / `skipped.length`.

3. If `unavailable` is non-empty AND the cache is loaded:
   - Announce: "Found N issue types with no matching default template. I'll suggest a template for each based on its Jira hierarchy level."
   - For each unavailable type, look up its metadata in `cache.issueTypes[typeName]`:
     - Determine suggestion based on `hierarchyLevel`:
       - `hierarchyLevel === 1` → suggest "Epic"
       - `hierarchyLevel === 0` and `subtask === false` → suggest "Task"
       - `subtask === true` → suggest "Skip (subtask)"
       - No `hierarchyLevel` available → no suggestion, present all options equally
     - Use AskUserQuestion:
       > Issue type "[typeName]" (hierarchy level: [N]) has no matching template.
       > Which default template should I use?

       Options: [Suggested template (Recommended)], [other available default templates], [Skip — no template for this type]
   - For each user selection (not "Skip"), copy the template:
     ```
     jira({ action: "copy-template", key: "<PROJECT_KEY>", templateType: "<typeName>", templateFrom: "<selectedTemplate>" })
     → { copied: true, type, from, destination } | { copied: false, reason: "exists", type, destination }
     ```
   - Report final results: "N additional templates created from user selections."

4. No cleanup needed — tool calls return structured data directly; there are no temp files to remove.

5. Stop. Do not proceed with any Jira operation.

---

## Step 1 — Deterministic Cache Initialization

> Run this phase only when the cache is missing, incomplete, `--force-refresh` is set,
> a TTL-based expiry was exceeded, or an operation error triggered an auto-refresh.
> After it completes, the skill never calls discovery endpoints again until the next refresh.

Announce: "Initializing Jira cache for project `[PROJECT_KEY]`…"

### Phase 1 — Identity (run BOTH in parallel)

```
mcp__atlassian__getAccessibleAtlassianResources()
→ Extract: sites[0].id → cloudId
           sites[0].url → siteUrl

mcp__atlassian__atlassianUserInfo()
→ Extract: accountId → currentUser.accountId
           displayName → currentUser.displayName
           emailAddress → currentUser.email
```

### Phase 2 — Project metadata (run BOTH in parallel, needs cloudId)

```
mcp__atlassian__getVisibleJiraProjects({ cloudId, searchString: PROJECT_KEY })
→ Extract: values[0].key, values[0].name, values[0].id → project object

mcp__atlassian__getIssueLinkTypes({ cloudId })
→ Extract: issueLinkTypes array → linkTypes (name, inward, outward per entry)
```

### Phase 3 — Issue types (needs project)

```
mcp__atlassian__getJiraProjectIssueTypesMetadata({ cloudId, projectKey: PROJECT_KEY })
→ Extract: for each issue type: name → key, id, subtask boolean, hierarchyLevel (integer)
→ Store as: issueTypes = { "Task": { "id": "10001", "subtask": false, "hierarchyLevel": 0 }, ... }
```

### Phase 4 — Field schemas (one call per issue type, run ALL in parallel)

For each issueType from Phase 3:

```
mcp__atlassian__getJiraIssueTypeMetaWithFields({ cloudId, projectKey, issueTypeId: issueType.id })
→ Extract: ALL fields — standard AND custom
→ For each field: name, key (fieldId), required (boolean), schema.type, allowedValues
→ Field type mapping:
    API type "string"            → cache type "string"
    API type "number"            → cache type "number"
    API type "priority"          → cache type "priority" (has allowedValues)
    API type "option"            → cache type "option" (custom single-select)
    API type "array" of "option" → cache type "multi-option" (custom multi-select)
    API type "user"              → cache type "user"
    API type "date"              → cache type "date" (format: YYYY-MM-DD)
    API type "datetime"          → cache type "datetime" (ISO-8601)
→ Store allowedValues as flat string arrays (extract the name or value property)
→ Store in: fieldSchemas[issueTypeName] = { [fieldKey]: { required, type, name?, allowedValues? } }
```

### Phase 5 — Workflow discovery (per non-subtask issue type) (implements R14)

**Determining the skip branch:** the `jira` tool's `check` action always reports
`flags.skipWorkflowDiscovery: false` — this field is a disclosed dead passthrough (it was
already write-only in the original CLI, never read back for behavior there either; the Go
port doesn't wire a tenth input field for it). Take the skip branch instead when
`--skip-workflow-discovery` is present in this invocation's own `$ARGUMENTS` — the flag
still works, it is just read directly by this skill rather than round-tripped through the
cache-check output.

**Skip branch (when `--skip-workflow-discovery` was passed):**

Do not issue any of the Phase 5a/5b/5c calls. Instead, for each non-subtask issue type in
`issueTypes`, write:

```json
"workflows": { "<issueTypeName>": { "unsampled": true } }
```

Subtask types are omitted (no workflow entry). Transitions at runtime fall back to a live
`getTransitionsForJiraIssue` call per issue — the existing stale-cache auto-refresh path
handles `unsampled` markers identically to a cache miss. Use this branch in CI and other
pre-seeded environments where Phase 5 is too expensive.

**Standard branch (default):**

For each non-subtask issue type in `issueTypes`:

**5a** — Find all statuses in use:

```
mcp__atlassian__searchJiraIssuesUsingJql({
  cloudId,
  jql: `project = "${PROJECT_KEY}" AND issuetype = "${issueTypeName}" ORDER BY status ASC`,
  fields: ["status"],
  maxResults: 100
})
→ Extract unique status names from results
```

**5b** — For each unique status, find one issue in that status:

```
mcp__atlassian__searchJiraIssuesUsingJql({
  cloudId,
  jql: `project = "${PROJECT_KEY}" AND issuetype = "${issueTypeName}" AND status = "${statusName}"`,
  fields: ["status"],
  maxResults: 1
})
→ Get issues[0].key
```

**5c** — Get transitions from that status:

```
mcp__atlassian__getTransitionsForJiraIssue({ cloudId, issueKey })
→ Extract: for each transition: id, name, to.name (target status)
→ Extract requiredFields: if transition has a screen, extract field schemas for required fields
→ Store in: workflows[issueTypeName].transitions[currentStatusName] = [
    { "id": "21", "name": "...", "to": "...", "requiredFields": { ... } }
  ]
```

If no issues exist in a given status (5b returns empty), skip that status — note it in
`workflows[type].statuses` as known but unsampled.

### Phase 6 — Assemble and save cache

Assemble the full cache object:

```json
{
  "version": 1,
  "lastUpdated": "<current ISO timestamp>",
  "maxAgeHours": 0,
  "cloudId": "...",
  "siteUrl": "...",
  "currentUser": { "accountId": "...", "displayName": "...", "email": "..." },
  "project": { "key": "...", "name": "...", "id": "..." },
  "issueTypes": { "Task": { "id": "10001", "subtask": false, "hierarchyLevel": 0 }, "Bug": { "id": "10002", "subtask": false, "hierarchyLevel": 0 }, "Epic": { "id": "10005", "subtask": false, "hierarchyLevel": 1 }, "Sub-task": { "id": "10004", "subtask": true, "hierarchyLevel": -1 } },
  "fieldSchemas": { "Task": { "summary": { "required": true, "type": "string" }, "...": {} } },
  "workflows": { "Task": { "transitions": { "To Do": [ { "id": "21", "name": "...", "to": "...", "requiredFields": {} } ] } } },
  "linkTypes": [ { "name": "Blocks", "inward": "is blocked by", "outward": "blocks" } ],
  "userMappings": {}
}
```

Save (required fields: `version`, `cloudId`, `project`, `siteUrl`):

```
jira({ action: "save", key: "<PROJECT_KEY>", data: <cache_json> })
→ { saved: true, cachePath }
```

Then load the cache:

```
jira({ action: "load", key: "<PROJECT_KEY>" })
→ <cache object>
```

Report: "Cache initialized for `[PROJECT_KEY]` — `[N]` issue types, `[N]` workflow states mapped."

---

## Step 2 — Classify Operation

Parse user intent into one of these operations:

| Operation | Trigger Phrases | Calls with cache |
|-----------|----------------|-----------------|
| `create` | create issue, new ticket, add bug/story/task | 1 |
| `edit` | update, change, set priority/label/assignee | 1 |
| `search` | find, list, show, search, which issues | 1 |
| `transition` | move to, start, close, complete, done, in progress | 1–2 |
| `comment` | comment on, add note, reply | 1 |
| `link` | link to, blocks, relates to, duplicate | 1 |
| `assign` | assign to, give to, ownership | 1–2 |
| `worklog` | log time, log work, spent time | 1 |
| `view` | show, get, display, details of | 1 |
| `bulk` | create N issues, multiple operations | N |

For ambiguous requests, use AskUserQuestion to ask one clarifying question before classifying.

---

## Step 2.5 — Critique (write-ops only, R20)

Skip this step for read operations (`search`, `view`). For every write operation (`create`, `edit`, `transition`, `comment`, `link`, `assign`, `worklog`, `bulk`), run a critique pass against the proposed payload **before** showing it to the user. Implements R20.

1. Build the initial payload exactly as you would dispatch it (template-resolved per R18, placeholders resolved per R19, fields validated against cache per G5/G6/G8).
   - **Template resolution and fallback notices (R18):** Read `resolved`, `fallbacks`, and `noneTypes` from:
     ```
     jira({ action: "templates", key: "<PROJECT_KEY>", templatesDir? })
     → { issueTypes, customTemplates, defaultTemplates, resolved, fallbacks: [{type, fallbackTo}], noneTypes }
     ```
     For each entry in `fallbacks`, print a one-line notice before building the payload:
     `Using <fallbackTo> template for <type> — override at .sdlc/jira-templates/<type>.md`
     For each entry in `noneTypes`, print a one-line warning and stop the operation:
     `No template for <type>. Run /jira-sdlc --init-templates or create .sdlc/jira-templates/<type>.md`
     Sub-bug, Sub-task, and Subtask types resolve via a fixed fallback map inside the `jira` tool (Sub-bug → Bug, Sub-task → Task, Subtask → Task) — the skill never re-derives this mapping.
2. Run the critique checklist:
   - **Template completeness** (create / description-touching edit) — every `## ` heading in the payload description belongs to the resolved template; no invented sections.
   - **Field correctness** — issue type / project key / parent / components / labels match cached `allowedValues`.
   - **Workflow validity** — for `transition`, the target status is reachable per the cached workflow graph (R6).
   - **Terminology consistency** — summary vocabulary matches description vocabulary (no contradictions).
   - **Terse content (R25)** — every `## ` section body in the description payload is a bullet list, numbered list, sub-heading set, or (Release Notes only) a single sentence. No paragraph longer than two consecutive non-list non-heading lines in any section. No filler transitional sentences between sections (`This ticket covers…`, `In summary…`, `The goal of…`). The `## Acceptance Criteria` section body is exclusively `- [ ] …` checklist items — no prose introduction, no prose summary, no sentence-form criteria. Summary is an imperative phrase ≤ 100 characters with no filler tokens (`This task covers`, `The goal of`, `We need to make sure`). Surface any violations in the `Critique:` block.
3. Compute the canonical content hash and write the critique artifact:
   - Canonicalize the payload (stable key order, `trimEnd` on every string value — R21.1). Callers do not need to strip trailing whitespace from file-sourced payloads (e.g., markdown bodies that end in `\n`).
   - Compute the hash via Bash: `printf '%s' "$canonical_json" | sha256sum | cut -c1-12` (or `shasum -a 256` where `sha256sum` is unavailable) — the same shell-level, tool-neutral hash for every write operation.
   - Write the critique artifact with the `Write` tool to `.sdlc/state/artifacts/critique-<hash>.json`: `{ initial: '<one-line summary of initial draft>', findings: [...], final: '<one-line summary of final payload>' }`.
4. Surface the critique to the user as an `Initial:` / `Critique:` / `Final:` block — do not apply deltas silently.

## Step 2.6 — Approval (write-ops only, R17)

Skip for read operations. Implements R17.

1. Print the full final payload (not a summary — the bytes the MCP call will dispatch).
2. Call `AskUserQuestion` with three options:
   - **approve** — proceed to Step 3 dispatch
   - **change <what>** — describe the desired change; loop back to Step 2.5 with the revised draft (new hash, fresh artifacts; the previous artifacts are stale and can be deleted)
   - **cancel** — abort the operation, do not dispatch
3. On `approve` only, write the approval token with the `Write` tool to `.sdlc/state/artifacts/approval-<hash>.token` (its content is not read back — its existence, next to the critique artifact from Step 2.5, is the record that this exact payload was approved this turn).
4. Proceed to Step 3.

## Step 2.7 — Link verification (write-ops only, R22, issue #198) — HARD GATE

Skip for read operations. After approval (Step 2.6) and before MCP dispatch, validate every URL embedded in the description payload (for `createJiraIssue`/`editJiraIssue`) and the comment body (for `addCommentToJiraIssue`):

```
jira({ action: "validate-body", key: "<PROJECT_KEY>", markdownBody: "<body_or_description>" })
→ { ok, violations: [{ url, line, reason, detail? }], skipped: [{ url, line, reason }],
    message?, adf? }
```

Pass the **raw markdown** body — do not pre-convert to ADF before this call. When `markdownBody` is non-empty, the same call also returns the markdown-to-ADF conversion in `adf` (KD16): this is the ADF payload to use for `addCommentToJiraIssue`'s `contentFormat: "adf"` dispatch (see Comment Operation, below) — there is no separate ADF-conversion call or ADF-text-node-extraction step in this port.

On `ok: false`:
- Do NOT dispatch the MCP write tool — the payload is never sent to Jira
- Surface `message` (or the formatted `violations` list: URL, line, reason, detail) verbatim to the user
- Stop. Do not retry. Do not edit URLs without user input. Do not bypass.
- Record the failure: `mcp_failure_record({ tool: "jira validate-body", rPath: "R22", site: JIRA_SITE, project: PROJECT_KEY, errorMessage: "link verification failed", recovered: "no" })` — see **MCP Failure Telemetry**, below, for what this does and does not do in this port.

On `ok: true`, proceed to Step 3.

Offline mode (skip network reachability checks, keep structural context-aware checks such as GitHub identity match and Atlassian host match) is controlled by the `SDLC_LINKS_OFFLINE=1` environment variable on the MCP server process itself, not a per-call parameter on this tool — set it before starting the session in sandboxed CI runs.

### MCP Failure Telemetry (partial port, R27)

This port does not classify or file issues automatically. **Occurrence counting and
automatic remediation-proposal generation are not available in this port; record failures
via `mcp_failure_record` and handle remediation manually or via harden-sdlc.**

```
mcp_failure_record({ tool, httpStatus?, errorMessage?, hookDenyReason?, rPath?, site?, project?, recovered?, sessionId? })
→ { class, sessionId, recorded }
```

The tool classifies the failure automatically from the signal you pass (`httpStatus`,
`errorMessage`, `hookDenyReason`, `rPath`) into one of `transport`, `auth`, `schema`,
`workflow`, `hook-block`, `link-verification`, `unknown` — there is no explicit `class`
input field, unlike the original CLI's `--class` flag. `recovered` is a free-form status
string, e.g. `"no"` or `"yes:R9"`. Every other telemetry call site in this skill (Step 3's
auth ladder, the Error Recovery R9 path, the Gotchas unsampled-transition path) follows
this same shape and this same disclosed limitation — call `mcp_failure_record` and stop
there; do not look for an `--analyze` equivalent or an automatic dispatch gate.

---

## Step 3 — Execute Operation

For write operations: precondition — Step 2.6 returned `approve` and Step 2.7 link
verification passed. **This port has no PreToolUse enforcement hook** — the Step 2.6
`AskUserQuestion` approval is the sole enforcement boundary for write dispatch (see DO NOT,
below). Never skip it, even when invoked from another skill or pipeline context.

**On cloudId authorization error** (response text matches `isn't explicitly granted` or auth/403 with cloudId substring) — implements spec R23:

1. Call `getAccessibleAtlassianResources` exactly once.
2. Compare the returned cloudId(s) against the cached value at `~/.sdlc-cache/jira/<site>/<KEY>.json`.
3. If different, run `/jira-sdlc --force-refresh` and reload the cache.
4. Retry the original MCP call exactly once under the primary namespace. If it still fails with the same error — try the sibling namespace (`mcp__claude_ai_Atlassian__`) once if registered.
5. If the primary namespace retry failed, record it before trying the sibling namespace: `mcp_failure_record({ tool: MCP_TOOL_NAME, errorMessage: AUTH_ERROR, site: JIRA_SITE, project: PROJECT_KEY, recovered: "no" })`.
6. If the sibling namespace also fails (dual-namespace exhausted), record it again: `mcp_failure_record({ tool: "mcp__claude_ai_Atlassian__" + MCP_TOOL_SUFFIX, errorMessage: AUTH_ERROR_SIBLING, site: JIRA_SITE, project: PROJECT_KEY, recovered: "no" })`. Report the failure to the user — see **MCP Failure Telemetry**, above; remediation from here is manual.

After Step 2 classifies the operation type, follow the matching procedure below.

### 3.1 — Create Operation

1. Determine issue type from user request — map user language ("bug", "feature", "task") to the exact type name from `cache.issueTypes`. If ambiguous, ask. Read `cache.fieldSchemas[issueTypeName]`; ask before proceeding for every required field the user didn't provide.
2. Resolve the description template (R18) — see Step 2.5. Free-form descriptions are prohibited.
3. Detect placeholders via the C13 regex (R19) — `\{[a-zA-Z_][a-zA-Z0-9_-]*\}|\[[^\]\n]{3,}\]`. Classify each marker `high` (explicit user input or definitive cache value) or `low`; escalate every `low` marker via AskUserQuestion. Never leave a raw placeholder in the final description.
4. Build the payload: `issueTypeName` exact string from cache (e.g., `"Task"` not `"task"`); `priority: { name: "..." }`; `labels` flat string array; `components` array of `{ name: "..." }`; custom fields by `fieldId` key (e.g. `customfield_10016`) with the correct shape (see Field Format Quick Reference, below); for Sub-task, include `parent: "PROJ-123"` as a top-level parameter.
5. Run Steps 2.5–2.7 (critique, approval, link verification).
6. Dispatch `mcp__atlassian__createJiraIssue` with `contentFormat: "markdown"`. On 400: check `fieldSchemas` for the issue type; verify field shapes against Field Format Quick Reference.
7. Post-op: see Step 4.

### 3.2 — Edit Operation

1. Parse which issue key, which field(s), what new value(s).
2. Resolve the description template (R18) ONLY when `description` is being touched — look up the issue's `issueTypeName` via cache or `getJiraIssue`.
3. Detect placeholders (R19) across every string-valued field, not only description; traverse ADF text nodes recursively when editing an ADF field.
4. Build `fields` — **flat object, not nested under `fields.fields`**: `priority: { name: "..." }`; `labels` flat string array (**REPLACES** existing labels entirely, not a merge); `components` array of `{ name: "..." }`; custom select `{ value: "..." }`; `assignee: { accountId: "..." }` from `cache.userMappings`.
5. Run Steps 2.5–2.7.
6. Dispatch `mcp__atlassian__editJiraIssue` with `responseContentFormat: "markdown"`. On 400: check field key spelling (`customfield_XXXXX`), field type, and value shape.
7. Post-op: see Step 4.

### 3.3 — Search Operation

1. Build JQL from user intent using the JQL Quick Reference, below — always scope with `project = <KEY>` unless the user explicitly wants cross-project.
2. Choose fields: summary view `["summary", "status", "assignee", "priority", "issuetype"]`; detailed view adds `"created", "updated", "description"`.
3. Call `mcp__atlassian__searchJiraIssuesUsingJql` — `maxResults: 25` for summary, `10` for detailed; `responseContentFormat: "markdown"`.
4. Format results as a readable table. If `total > maxResults`, inform the user and offer to paginate with `startAt`.

Read operation — Steps 2.5–2.7 do not apply.

### 3.4 — Transition Operation

1. Determine target status from user intent ("move to Done", "start", "mark in review"); get current status (context or `getJiraIssue`). Template resolution and placeholder detection do not apply — transitions have no description field or free-text fields.
2. Build the payload: look up `transitions = cache.workflows[issueTypeName].transitions[currentStatus]`; find the transition matching the target status (if none, inform the user of available transitions and ask which to use); include `requiredFields` when non-empty (e.g., `{ resolution: { name: "Done" } }`). Final shape: `{ cloudId, issueKey, transition: { id }, fields? }`.
3. Run Steps 2.5–2.7 (critique verifies the target is reachable per the cached workflow graph).
4. Dispatch `mcp__atlassian__transitionJiraIssue`. On 400 with `requiredFields`: verify all required fields were included with correct shapes. On "transition not found": call `getTransitionsForJiraIssue` for a fresh list (auto-refresh path).
5. Post-op: record fresh transitions if the cache was stale (Step 4).

### 3.5 — Comment Operation

1. Compose the comment in markdown (Markdown Content Rules, below). Template resolution does not apply — comments have no template.
2. Detect placeholders (R19) in the markdown source before conversion.
3. Call `jira({ action: "validate-body", key, markdownBody: "<comment markdown>" })` (Step 2.7) and take its `adf` output as the comment body — this is the only ADF-conversion step; there is no separate conversion call. Final shape: `{ cloudId, issueIdOrKey, commentBody: <adf>, contentFormat: "adf", responseContentFormat: "markdown" }`. Never use HTML tags, task lists (`- [ ]`), or footnotes in the source markdown.
4. Run Steps 2.5–2.6 (critique, approval); Step 2.7 is the validate-body call in 3 above.
5. Dispatch `mcp__atlassian__addCommentToJiraIssue`.
6. Post-op: none typically required.

### 3.6 — Link Operation

1. Determine link direction from user intent: "PROJ-A blocks PROJ-B" → `outwardIssue = PROJ-A`, `inwardIssue = PROJ-B`; "PROJ-A is blocked by PROJ-B" → reversed; "relates to" → either direction (symmetric). Cross-reference `cache.linkTypes` for exact inward/outward label semantics. Template resolution and placeholder detection do not apply — link payloads carry no free text.
2. Build the payload: find the link type in `cache.linkTypes` by name; assemble `{ cloudId, linkType: { name: "Blocks" }, inwardIssue: { key: "PROJ-123" }, outwardIssue: { key: "PROJ-456" } }`.
3. Run Steps 2.5–2.7.
4. Dispatch `mcp__atlassian__createIssueLink`.
5. Post-op: none typically required.

### 3.7 — Assign Operation

1. If the user mentions a name/email already in `cache.userMappings`, use the cached `accountId` directly.
2. Otherwise, call `mcp__atlassian__lookupJiraAccountId({ cloudId, query: "<name or email>" })`. If multiple results, show all and ask the user to confirm which one. Once confirmed, save it to the cache:
   ```
   jira({ action: "save-field", key: "<PROJECT_KEY>", fieldName: "userMappings", data: { "<displayName>": "<accountId>" } })
   → { saved: true, field: "userMappings", cachePath }
   ```
3. Dispatch `mcp__atlassian__editJiraIssue({ cloudId, issueKey, fields: { assignee: { accountId: "<confirmed accountId>" } }, responseContentFormat: "markdown" })` (Steps 2.5–2.7 apply — this is an edit under the hood).

### 3.8 — Worklog Operation

1. Parse time from user input ("2 hours 30 minutes" → `"2h 30m"`, "half a day" → `"4h"`); collect an optional work description. Template resolution and placeholder detection do not apply beyond the plain-text comment field.
2. Build the payload: `{ cloudId, issueKey, timeSpent: "<Jira duration string>", comment: "<optional description>", adjustEstimate: "auto" }`.
3. Run Steps 2.5–2.7.
4. Dispatch `mcp__atlassian__addWorklogToJiraIssue`.
5. Post-op: none typically required.

### 3.9 — View Operation

1. Determine detail level: quick summary `fields: ["summary", "status", "assignee", "priority", "issuetype", "labels"]`; full details — all fields including description and custom fields.
2. Call `mcp__atlassian__getJiraIssue({ cloudId, issueKey, fields: [...], responseContentFormat: "markdown" })`.
3. Render the response clearly; show the description as formatted markdown.

Read operation — Steps 2.5–2.7 do not apply.

### 3.10 — Bulk Operation

1. Parse all items from the user request into discrete operation specs.
2. Identify dependencies (e.g., an Epic must exist before Stories that link to it).
3. Execute independent operations in parallel; dependent operations sequentially. Each operation still runs its own Steps 2.5–2.7.
4. Report progress after each batch: "Created 3 of 5 issues…"
5. On partial failure: complete remaining independent operations, then report all failures together.

---

## Step 4 — Post-Operation Cache Updates

After operations that reveal new information, update the cache incrementally:

| Trigger | Cache update |
|---------|---------------------|
| New user resolved via `lookupJiraAccountId` | `jira({ action: "save-field", key, fieldName: "userMappings", data: { "<name>": "<id>" } })` |
| Transition from a status not in workflow cache | `jira({ action: "save-field", key, fieldName: "workflows", data: <workflows_json> })` |
| Cache returned stale transition ID (404/400) | **Auto-refresh**: run `--force-refresh`, reload cache, retry operation once |
| Operation fails with field key or value not in cache | **Auto-refresh**: run `--force-refresh`, reload cache, retry operation once |

---

## Error Recovery

| Error | Diagnosis | Recovery |
|-------|-----------|----------|
| 400 on create | Missing required field or wrong field shape | Verify field key/shape against the cached `fieldSchemas` object and the Field Format Quick Reference below. If the field doesn't match, run `--force-refresh`, reload cache, retry once. If still failing after refresh, use the **gated dispatch** below (R9 exhausted path) |
| 400 on transition | Missing required transition field (e.g., resolution) | Check `workflows[type].transitions[status][n].requiredFields`; include required fields. If transition ID is not recognized, **auto-refresh**: run `--force-refresh`, reload cache, retry once |
| 400 on edit | Wrong field shape or incorrect custom field key | Verify field key/shape against the cached `fieldSchemas` object and the Field Format Quick Reference below. If the field doesn't match, run `--force-refresh`, reload cache, retry once |
| 401 | Auth token expired | Reconnect Atlassian MCP; cannot recover programmatically |
| 403 | Insufficient permission | Report to user — cannot fix |
| 404 issue | Issue key wrong or issue deleted | Ask user to verify the issue key |
| 404 project | Wrong project key or no access | Re-run `check`; verify cloudId matches the correct site |
| 409 | Concurrent edit conflict | Retry the operation once |
| Stale transition | Transition ID no longer valid | **Auto-refresh**: run `--force-refresh`, reload cache, retry with new IDs |
| Repeated 400 (2+ attempts) | Cache may have incorrect schema data | **Auto-refresh**: run `--force-refresh`, reload cache, retry once. If still failing after refresh, use the **gated dispatch** below (R9 exhausted path) |

**R9 exhausted path:** when a 400 on create or repeated 400 still fails after cache auto-refresh, record it — `mcp_failure_record({ tool: MCP_TOOL_NAME, errorMessage: ERROR_MSG, site: JIRA_SITE, project: PROJECT_KEY, recovered: "no" })` — then report to the user; see **MCP Failure Telemetry**, above. Also call `mcp_failure_record` on every retry, even successful ones (`recovered: "yes:R9"`), to maintain a per-session failure log.

---

## Field Format Quick Reference

Use this table when constructing `createJiraIssue.additional_fields` or
`editJiraIssue.fields`. Deviations from these shapes are the most common cause of 400
errors.

| Field / Type | JSON Shape | Example | Notes |
|---|---|---|---|
| `summary` (string) | `"value"` | `"Fix login redirect bug"` | Top-level param on create, not in `additional_fields` |
| `description` (markdown) | `"markdown text"` | `"## Bug\n\nSteps to reproduce:\n1. ..."` | Always pair with `contentFormat: "markdown"` |
| `priority` | `{ "name": "..." }` | `{ "name": "High" }` | NOT `{ "id": "2" }` or bare `"High"` |
| `assignee` | `{ "accountId": "..." }` | `{ "accountId": "abc123" }` | Get from `cache.userMappings` or `lookupJiraAccountId` |
| `labels` | `["...", "..."]` | `["backend", "urgent"]` | Flat string array — NOT array of objects |
| `components` | `[{ "name": "..." }]` | `[{ "name": "API" }]` | Array of name-keyed objects |
| `fixVersions` | `[{ "name": "..." }]` | `[{ "name": "2.0" }]` | Array of name-keyed objects |
| `resolution` | `{ "name": "..." }` | `{ "name": "Done" }` | Required on transitions to Done in many workflows |
| `parent` (subtask) | `"PROJ-123"` | `"PROJ-100"` | String key, top-level param on create only |
| `duedate` | `"YYYY-MM-DD"` | `"2026-03-31"` | ISO date string, no time component |
| `datetime` fields | ISO-8601 string | `"2026-03-12T10:00:00.000+0000"` | Full ISO-8601 with timezone offset |
| `number` (story points, custom) | `N` | `5` | Raw number — no object wrapper |
| `sprint` (`customfield_10020`) | `N` | `42` | Sprint **ID** as number — not the sprint name or an object |
| `custom select` (single) | `{ "value": "..." }` | `{ "value": "Option A" }` | NOT `{ "name": "..." }` |
| `custom multi-select` | `[{ "value": "..." }]` | `[{ "value": "A" }, { "value": "B" }]` | Array of value-keyed objects |
| `custom cascading select` | `{ "value": "...", "child": { "value": "..." } }` | `{ "value": "Level1", "child": { "value": "Level2" } }` | Nested value objects |
| `custom user picker` | `{ "accountId": "..." }` | `{ "accountId": "abc123" }` | Same shape as `assignee` |
| `custom text field` | `"value"` | `"any string"` | Plain string, same as `summary` |

**Important notes on `editJiraIssue`:**

- `fields` is a flat object — do NOT nest fields under `fields.fields`.
- Pass custom fields directly by their key: `{ "customfield_10016": 5, "customfield_10020": 42 }`.
- Omitted fields are left unchanged — you do not need to include all fields on every edit.
- To clear a field, pass `null` for fields that accept null (not all do — check `fieldSchemas`).

If an operation fails with 400 and the field format matches this table, check whether the
field is required for the specific issue type using `cache.fieldSchemas[issueType][field].required`.
Required fields on create produce 400 if omitted.

## JQL Quick Reference

| Intent | JQL |
|--------|-----|
| My open issues | `assignee = currentUser() AND status != Done ORDER BY updated DESC` |
| Sprint backlog | `project = PROJ AND sprint in openSprints() ORDER BY rank ASC` |
| Recent bugs (7 days) | `project = PROJ AND issuetype = Bug AND created >= -7d ORDER BY created DESC` |
| By label | `project = PROJ AND labels = "backend"` |
| Unassigned open issues | `project = PROJ AND assignee is EMPTY AND status != Done` |
| All subtasks of parent | `parent = PROJ-123` |
| Text search | `project = PROJ AND text ~ "search term" ORDER BY updated DESC` |
| By status category | `project = PROJ AND statusCategory = "In Progress"` |
| Updated in last 24h | `project = PROJ AND updated >= -1d ORDER BY updated DESC` |
| Linked to an issue | `issue in linkedIssues("PROJ-123")` |
| High/Highest priority open | `project = PROJ AND priority in (Highest, High) AND status != Done` |
| Created by me | `project = PROJ AND reporter = currentUser() ORDER BY created DESC` |
| Resolved last week | `project = PROJ AND resolved >= -1w AND resolved <= now()` |
| Epic children | `"Epic Link" = PROJ-50 ORDER BY rank ASC` |
| By component | `project = PROJ AND component = "API"` |
| Open blockers of an issue | `project = PROJ AND issue in linkedIssues("PROJ-123", "is blocked by") AND status != Done` |
| Multiple statuses | `project = PROJ AND status in ("To Do", "In Progress")` |
| No fix version (non-Epic) | `project = PROJ AND fixVersion is EMPTY AND issuetype != Epic` |
| Overdue (past due date) | `project = PROJ AND duedate < now() AND status != Done` |
| Issues I'm watching | `issue in watchedIssues()` |

**Escaping rules:**

- Wrap values containing spaces in double quotes: `project = "My Project"`
- Escape single quotes inside a value: `summary ~ "can\\'t login"`
- Reserved words (`AND`, `OR`, `NOT`, `ORDER`, `BY`, `ASC`, `DESC`, `IS`, `EMPTY`, `NULL`, `TRUE`, `FALSE`) must be quoted if used as literal values, but are used bare as keywords in the query structure
- Issue keys do not need quotes: `parent = PROJ-123`
- `currentUser()` is a function — no quotes around it: `assignee = currentUser()`
- Relative date offsets use a number followed by a unit suffix: `-7d` (days), `-1w` (weeks), `-1h` (hours). No spaces between number and suffix.
- `in` clauses use parentheses, not square brackets: `status in ("To Do", "In Progress")`
- `is EMPTY` and `is not EMPTY` test for null/unset fields — do not use `= null`

## Markdown Content Rules

Compose all content (descriptions and comments) in markdown following the rules below.
For **comments**, the markdown is then converted to ADF via `jira`'s `validate-body`
action (its `adf` output field — Step 2.7) before posting; the conversion happens as a
side effect of the link-verification call. For **descriptions**, markdown is submitted
directly with `contentFormat: "markdown"`.

The Jira markdown renderer is a subset of CommonMark. The following tables define what is
safe to use and what to avoid.

**Supported:**

| Element | Syntax | Example |
|---------|--------|---------|
| Heading 1 | `# text` | `# Summary` |
| Heading 2 | `## text` | `## Steps to Reproduce` |
| Heading 3 | `### text` | `### Expected Behavior` |
| Bold | `**text**` | `**Critical**` |
| Italic | `*text*` | `*optional*` |
| Unordered list | `- item` or `* item` | `- Step one` |
| Ordered list | `1. item` | `1. Open the app` |
| Inline code | `` `code` `` | `` `null pointer` `` |
| Fenced code block | ` ```lang\ncode\n``` ` | ` ```js\nconsole.log()\n``` ` |
| Link | `[text](url)` | `[Ticket](https://...)` |
| Table | `\| col \| col \|` with `\|---\|---\|` separator row | Standard markdown table |
| Horizontal rule | `---` | Separates sections |
| Blockquote (single level) | `> text` | `> Original requirement` |

**Broken or unsupported — avoid:**

| Element | Syntax | Problem |
|---------|--------|---------|
| HTML tags | `<b>`, `<br>`, `<details>` | Rendered as literal text, not interpreted |
| Task lists | `- [ ] item` | Variable support; renders as literal `[ ]` in many Jira versions |
| Nested blockquotes | `>> text` | Renders as literal `>` |
| Footnotes | `[^1]: text` | Not supported |
| Definition lists | `term\n: definition` | Not supported |
| Strikethrough | `~~text~~` | Not supported in all Jira versions |
| Custom emoji | `:smile:` | Not rendered |
| Raw HTML entities | `&nbsp;`, `&mdash;` | May render as literals |

**Template placeholder rule:** never submit a description or comment that contains
unfilled placeholder text such as `{placeholder}`, `{{variable}}`, `<INSERT HERE>`, or
`TODO: fill in`. Either replace it with real content from user context, or remove the
entire line or section. A description with raw placeholders visible to other Jira users
is always incorrect.

## Error Code Reference

| HTTP Status | Meaning | Likely Cause | Recovery |
|-------------|---------|--------------|----------|
| 400 Bad Request | Invalid field value or format | Wrong field shape (e.g., `"High"` instead of `{ "name": "High" }`), missing required field, invalid enum value | Check `cache.fieldSchemas` for allowed values; verify field shape against Field Format Quick Reference |
| 400 on transition | Transition validation failed | Missing required field for the transition (e.g., `resolution` when closing) | Check `cache.workflows[issueType].transitions[currentStatus][n].requiredFields`; include all required fields in `transitionJiraIssue.fields` |
| 401 Unauthorized | Authentication failure | MCP token expired or not connected | Reconnect the Atlassian MCP integration; cannot recover programmatically |
| 403 Forbidden | Insufficient permissions | Authenticated user lacks the required project or issue permission | Report to user — this cannot be fixed programmatically |
| 404 Issue not found | Issue key does not exist | Typo in issue key, issue was deleted, or wrong project prefix | Ask user to verify the issue key; check project key in `cache.project.key` |
| 404 Project not found | Project key does not exist | Typo, no access, or wrong Atlassian cloud | Re-run `check` to validate; verify `cloudId` matches the intended site |
| 409 Conflict | Concurrent edit detected | Another user or process modified the issue between your read and write | Retry once after a brief pause; if it persists, fetch fresh state and reapply the change |
| 422 Unprocessable Entity | Schema validation failed | Field value type mismatch (e.g., passing a string where a number is expected) | Re-read field schema from `cache.fieldSchemas`; cross-check the type column in Field Format Quick Reference |
| Stale transition ID | Transition ID no longer valid | Jira workflow was reconfigured by an admin after the cache was written | Pass `--force-refresh` to rebuild the cache, or call `getTransitionsForJiraIssue` fresh before retrying |

**Diagnosing 400 errors systematically:**

1. Confirm the field key is correct (check `cache.fieldSchemas` — custom field keys are `customfield_XXXXX`, not human names).
2. Confirm the value shape matches Field Format Quick Reference for the declared field type.
3. Check `cache.fieldSchemas[issueType][field].required` — if `true` and the field was omitted on create, the API returns 400.
4. Check `cache.fieldSchemas[issueType][field].allowedValues` — if the field has constrained values and the submitted value is not in the list, the API returns 400.
5. If all of the above check out and the error persists, the cache may be stale; run `--force-refresh` and retry.

---

## Quality Gates

| Gate | Check |
|------|-------|
| Cache loaded | `cloudId`, `project`, `issueTypes`, `fieldSchemas` all present before any operation |
| Content format | Comment calls use `contentFormat: "adf"` with the ADF body from `jira validate-body`'s `adf` output; description/create calls use `contentFormat: "markdown"` |
| Response format | Every content-returning call uses `responseContentFormat: "markdown"` |
| No raw placeholders | All `{placeholder}` markers in templates filled or section removed |
| Required fields | All required fields per `fieldSchemas` have values before create |
| Transition safety | Transition `id` from cache or fresh `getTransitionsForJiraIssue`, never guessed |
| User disambiguation | `lookupJiraAccountId` results always disambiguated if multiple matches |
| No fabricated values | All field values derived from cache `allowedValues` or user input |
| Approval gate (G9) | No write MCP call dispatched without an `approve` from the R17 prompt in this turn |
| Template enforced (G10) | No `description` field built without a resolved template — `.sdlc/jira-templates/<Type>.md` (override) or shipped `templates/<Type>.md` (R18) |
| Placeholders resolved (G11) | No `low`-confidence `{name}` or `[prose]` marker dispatched without explicit user resolution (R19) |
| Critique surfaced (G12) | No proposal presented to the user without a preceding `Initial:` / `Critique:` / `Final:` block (R20) |
| Cooperative approval (G13) | Write dispatch relies on the Step 2.6 `AskUserQuestion` answer alone — this port has no automated hook that re-verifies the payload hash before dispatch. Treat the approval step as a hard behavioral rule, not a technically enforced one |
| Link verified (G14, R22, #198) | No write MCP call (`createJiraIssue`, `editJiraIssue`, `addCommentToJiraIssue`) dispatched without `jira`'s `validate-body` action returning `ok: true`. See Step 2.7 |
| Terse content (G15) | No `createJiraIssue` / `editJiraIssue` dispatch where the description's `## Acceptance Criteria` section contains non-checklist lines. Not deterministically blocked — enforced entirely via the Step 2.5 critique checklist and the Step 2.6 approval review, same as the rest of R25's bullet/no-prose enforcement (R25.1–R25.4) |

---

## DO

- Present the full final payload before any write MCP call (R17)
- Resolve a description template — override or shipped — before building `description` (R18)
- Escalate every low-confidence placeholder marker via `AskUserQuestion` (R19)
- Run a critique pass before the approval gate; surface findings to the user (R20)
- Write the critique and approval-token artifacts with the `Write` tool, and compute the canonical content hash via `sha256sum`/`shasum` (through Bash) over the canonicalized payload (R21)
- Compose description section bodies as bullet lists or numbered lists; emit `## Acceptance Criteria` content as `- [ ] …` checklist items only (R25)

## DO NOT

- Post comments with `contentFormat: "markdown"` — always take the `adf` field from the `jira validate-body` call (Step 2.7) and dispatch with `contentFormat: "adf"`
- Call `getAccessibleAtlassianResources` after cache init — use cached `cloudId`
- Call `getJiraIssueTypeMetaWithFields` after cache init — use cached `fieldSchemas`
- Call `getIssueLinkTypes` after cache init — use cached `linkTypes`
- Pass transition name to `transitionJiraIssue` — requires `{ id: "..." }` object
- Pass display name as assignee — requires `{ accountId: "..." }`
- Guess field IDs, custom field keys, or transition IDs
- Skip required fields for transitions (e.g., resolution when closing)
- Use values not in cache `allowedValues` — never fabricate enum values
- Retry a failed operation more than once without diagnosing the cause first
- Leave raw `{placeholder}` syntax in issue descriptions
- Ignore custom templates at `.sdlc/jira-templates/<Type>.md` when they exist
- Generate unstructured descriptions when a template is available
- Dispatch a write MCP without an `approve` answer to the R17 prompt in this turn (R17)
- Use a free-form description on `createJiraIssue` or `editJiraIssue` (R18)
- Fill `[bracketed prose]` or `{name}` placeholders from inference — every `low`-confidence marker requires explicit user resolution (R19)
- Apply critique deltas silently — always surface the `Initial:` / `Critique:` / `Final:` block (R20)
- Compute the canonical hash by any means other than `sha256sum`/`shasum` over the canonicalized payload, or skip writing the critique/approval-token artifacts — the hash is what the Step 2.6 approval binds to, even though nothing re-verifies it mechanically (R21)
- Dispatch any write MCP tool without an `AskUserQuestion` approval gate (Step 2.6) — this port has no PreToolUse hook that verifies write dispatch; the Step 2.6 `AskUserQuestion` prompt is the sole enforcement boundary. Never skip it, even when invoked from another skill or pipeline context
- Write prose paragraphs in description sections — bullet lists, numbered lists, or sub-headings only (R25)
- Write acceptance criteria as sentences — every item is `- [ ] <discrete criterion>` (R25)
- Add filler transitional sentences between description sections (`This ticket covers…`, `The goal of…`, `In summary…`) (R25)

---

## Gotchas

- `createJiraIssue` uses `issueTypeName` (string `"Task"`), NOT `issueTypeId` (`"10001"`)
- `editJiraIssue.fields` is a flat object — do NOT nest under `fields.fields`
- `additional_fields` on create is for everything beyond summary/description/assignee/parent
- Priority values are `{ name: "High" }` — NOT `{ id: "2" }` or bare `"High"`
- Labels are flat strings `["label1"]` — NOT `[{ name: "label1" }]`
- Components are objects `[{ name: "API" }]` — NOT flat strings `["API"]`
- Custom single-select uses `{ value: "Option" }` — NOT `{ name: "Option" }`
- Sprint field is a number (sprint ID integer) — NOT the sprint name or an object
- `lookupJiraAccountId` may return multiple results — always disambiguate with the user
- JQL values with special characters need escaping: `summary ~ "can\\'t"` not `"can't"`
- Sub-task creation requires `parent: "PROJ-123"` as a string parameter AND the exact subtask type name from `cache.issueTypes` (may be `"Sub-task"`, `"Subtask"`, or custom)
- When a transition is absent from `getTransitionsForJiraIssue` results, it means transition conditions aren't met (e.g., all subtasks must be closed) — missing transitions are intentional, not a bug
- Transition `requiredFields` may include screen-only fields not in `fieldSchemas` — if a required field is absent from the schema, try the transition without it first; screen fields sometimes only block the Jira UI, not the API
- `getVisibleJiraProjects` uses `searchString` (not `query`) for filtering — check parameter name before calling
- When Phase 5 workflow sampling finds no issues at all for a type, skip workflow discovery for that type entirely and note it in the cache as `"workflows": { "Story": { "unsampled": true } }`
- `unsampled: true` markers (from `--skip-workflow-discovery` in CI, or from no-sample results above) route transition operations through a live `getTransitionsForJiraIssue` per issue — the skill reuses the existing stale-cache auto-refresh path, so no separate branch is required in Step 3. Treat `unsampled` identically to "transition ID not cached". When the live `getTransitionsForJiraIssue` call itself fails on an unsampled path (R14 exhausted), record it — `mcp_failure_record({ tool: "getTransitionsForJiraIssue", errorMessage: TRANSITION_ERROR, site: JIRA_SITE, project: PROJECT_KEY, recovered: "no" })` — and report to the user; see **MCP Failure Telemetry**, above.
- The `mcp__atlassian__` prefix is the default; if the user's MCP is registered under a different prefix (e.g., `mcp__claude_ai_Atlassian__`), use the active prefix consistently across all calls in the session
- **Namespace fallback (spec R23):** When the primary namespace (`mcp__atlassian__`) returns a cloudId authorization error and `mcp__claude_ai_Atlassian__` is also registered (visible in the deferred-tools list), retry the operation under the sibling namespace once. Persist the working namespace for the rest of the session — do not re-probe per-call. Combine with the Step 3 cloudId-error ladder: namespace-fallback is the second leg after the cache-refresh retry fails.
- **Release Notes is the one allowed single-sentence carve-out (R25.5).** The `## Release Notes` section in Bug and Story templates may contain a single sentence — it is changelog-bound and bullet form is contextually awkward. Two or more sentences in this section fail the R25 critique check. All other sections must use bullet lists, numbered lists, or sub-headings.

---

## Learning Capture

When executing Jira operations, capture discoveries by appending to `.sdlc/learnings/log.md`.
Record entries for: field formats that differ from the defaults documented here, workflow
quirks discovered in specific projects, issue type names that aren't standard (e.g., custom
subtask type names), user lookup disambiguation patterns, and transition required fields not
captured by the workflow sampling.

**MCP failures use the structured R27 form** (written by `mcp_failure_record`). It writes a
block under a `## YYYY-MM-DD — jira-sdlc mcp-failure[<class>]: <tool>` heading. Non-MCP
discoveries continue to use the free-form prose style above.

## What's Next

After completing a Jira operation, common follow-ups include:
- `/plan-sdlc` — write an implementation plan for a ticket
- `/execute-plan-sdlc` — execute an existing plan

## See Also

- [`/plan-sdlc`](../plan-sdlc/SKILL.md) — write an implementation plan from a Jira ticket
- [`/execute-plan-sdlc`](../execute-plan-sdlc/SKILL.md) — execute an existing plan
