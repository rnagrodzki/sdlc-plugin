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
