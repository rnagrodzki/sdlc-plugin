# /jira

Create, read, search, update, and manage Jira issues directly from Claude
Code. Caches project metadata to keep operations fast.

## When to use

- You want to create a Jira issue for work you are doing.
- You want to read or search existing Jira issues.
- You want to transition an issue, add a comment, or log work.
- You want to link Jira issues to each other.

## Syntax

    /jira [options]

After invoking `/jira`, describe what you want in natural language.

## Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--project <KEY>` | Jira project key (e.g., `PROJ`). | auto-detected from branch, else configured default |
| `--force-refresh` | Rebuild the cached project metadata. | off |
| `--init-templates` | Copy default issue templates to `.sdlc-v2/jira-templates/` for per-project customization. The plugin ships no default templates today, so this copies nothing and says so. | off |
| `--site <host>` | Jira site hostname (e.g., `mycompany.atlassian.net`). Disambiguates cached projects that exist under more than one site. | unset |
| `--skip-workflow-discovery` | Skip loading workflows and transitions (faster startup; useful in CI). | off |

## Steps

| Step | Actor | What it does | Hands over | Optional | When |
|---|---|---|---|---|---|
| Step 0: Parse arguments and check cache | skill | Reads the flags, finds the project key and checks the cache with `jira check`. With `--init-templates` it copies the templates and stops. | The project key and the cache state | no | always |
| Step 1: Deterministic cache initialization | skill | Announces the cache build and runs the six phases below. After it, the skill makes no more discovery calls until the next refresh. | The cache of the project | no | the cache is missing or incomplete, `--force-refresh` is set, a TTL expired, or an operation error forces a refresh |
| Step 1, Phase 1: Identity | tool | Reads the cloud ID, site URL and current user from Atlassian. | The cloud ID and the user | no | the cache is rebuilt |
| Step 1, Phase 2: Project metadata | tool | Reads the project and the issue link types. | The project and the link types | no | the cache is rebuilt |
| Step 1, Phase 3: Issue types | tool | Reads the issue types of the project. | The issue type list | no | the cache is rebuilt |
| Step 1, Phase 4: Field schemas | tool | Reads the standard and custom fields of each issue type. | The field schemas | no | the cache is rebuilt |
| Step 1, Phase 5: Workflow discovery | tool | Reads the statuses and the transitions of each issue type that is not a subtask. | The workflow graph, or an `unsampled` marker | yes | the cache is rebuilt. `--skip-workflow-discovery` skips it |
| Step 1, Phase 6: Assemble and save cache | tool | Builds the cache object and saves it with `jira save`. | The saved cache file | no | the cache is rebuilt |
| Step 2: Classify operation | skill | Sorts the request into one of the operations: create, edit, search, transition, comment, link, assign, worklog, view or bulk. | The operation type | no | always |
| Step 2.5: Critique | skill | Builds the payload, checks it against the template and the cache, and shows an Initial, Critique and Final block. | The checked payload and the critique file | no | write operations only. Search and view skip it |
| Step 2.6: Approval | user | Shows the full payload. The user chooses approve, change or cancel. | The approval token | no | write operations only |
| Step 2.7: Link verification | tool | Checks every URL in the payload with `jira validate-body`. A failed check stops the operation. This is a hard gate. | The link check result and the ADF body | no | write operations only |
| Step 3: Execute operation | tool | Sends the call to Jira for the operation, from the cached values. | The Jira result | no | always |
| Step 4: Post-operation cache updates | tool | Saves new user mappings and new transitions to the cache. A stale cache triggers a refresh and one retry. | The updated cache | no | the operation showed new information or a stale cache |

### Run facts

| Fact | Value |
|---|---|
| Starts | With `/jira`, with or without a flag. Then the user describes the operation in natural language. |
| Ends | After Step 4. It ends earlier with `--init-templates`, with a cancelled approval, or with a failed link check. |
| Asks the user | When the project key is unknown, when the cache exists under more than one site, when the request is ambiguous, for each required field the user did not give, and at the approval of every write operation. |
| Resume | none |

## Examples

**Create an issue:**

    /jira
    > Create a task in PROJ: "Add rate limiting to auth API" with priority High

**Read an issue:**

    /jira
    > Show me PROJ-456

**Search issues:**

    /jira
    > Find all open bugs assigned to me in PROJ

**Transition an issue:**

    /jira
    > Move PROJ-789 to "In Review"

**Initialize templates:**

    /jira --init-templates --project NEWPROJ

## Related skills

- [/plan](plan.md) — Plans can reference Jira issues as requirements.
- [/pr](pr.md) — PR descriptions can include Jira issue links.
- [/setup](setup.md) — Configure the default Jira project key.

## Tips and gotchas

- **Configure first.** Run `/setup` and configure the `jira` section to set a
  default project key before using `/jira`.
- **Requires Atlassian MCP connection.** The skill dispatches Jira operations
  through the Atlassian MCP connector — make sure it is authorized.
- **Metadata is cached.** The first invocation loads and caches fields,
  workflows, and transitions; the cache does not expire on its own. Use
  `--force-refresh` if the project's Jira configuration changed.
- **Multi-project repos.** Add a `projects` array under the `jira` section of
  `.sdlc-v2/config.toml` to restrict which project keys are accepted, then
  use `--project <KEY>` to pick the active one per invocation.
- **Templates.** A description template per issue type is optional. With a
  template at `.sdlc-v2/jira-templates/<Type>.md`, `/jira` fills it. Without
  one, `/jira` tells you no template was found and drafts the description
  from a base structure: `## Summary`, `## Context`, `## Acceptance Criteria`.
  You still review the critique and approve the payload before anything is
  sent to Jira. The plugin ships no default templates today, so
  `--init-templates` copies nothing unless it finds a templates directory
  (see the skill for the lookup order).
