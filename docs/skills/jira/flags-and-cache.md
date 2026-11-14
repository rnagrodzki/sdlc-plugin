# Flags, project key and cache

Read this page when you want to know each flag of `/sdlc:jira`, how the skill
finds the project key, when it rebuilds the cache, or what it does when an
operation fails. The short overview is [/sdlc:jira](../jira.md).

## Example

```text
/sdlc:jira --project PROJ --force-refresh
```

The skill uses the project `PROJ` and builds a new cache for it. It reports
the number of issue types and workflow states in the new cache.

## Flags

| Flag | What it does | Default |
|---|---|---|
| `--project <KEY>` | Sets the Jira project key, for example `PROJ`. | Found automatically. See [How the skill finds the project key](#how-the-skill-finds-the-project-key). |
| `--force-refresh` | Rebuilds the cache, also when the cache is complete. | off |
| `--init-templates` | Copies the default templates to `.sdlc-v2/jira-templates/`, then stops. See [Copy the default templates](./templates.md#copy-the-default-templates). | off |
| `--site <host>` | Selects the Jira site when the same project key has a cache under more than one site. The value is the site host in lower case with each `.` replaced by `_`, for example `acme_atlassian_net`. | unset |
| `--skip-workflow-discovery` | Skips the read of the workflows when the skill builds the cache. A transition then reads the live transitions of the issue. Use it in CI. | off |

## What the skill needs

- The Atlassian MCP server, connected and authorized. The skill sends each
  Jira operation through it.
- A project key. To set a default key, run `/sdlc:setup --only jira`. See
  [/sdlc:setup](../setup.md).

## How the skill finds the project key

The skill uses the first source that gives a key:

1. The `--project <KEY>` flag.
2. The current git branch. A branch such as `feat/PROJ-123-fix` gives `PROJ`.
3. `jira.defaultProject` in `.sdlc-v2/config.toml`.
4. A question to you: "Which Jira project key should I use?"

## More than one project in a repository

Add a `projects` list with two or more keys to the `jira` section of
`.sdlc-v2/config.toml`:

```toml
[jira]
defaultProject = "PROJ"
projects = ["PROJ", "TEAM"]
```

- The skill rejects a `--project` key that is not in the list.
- The skill ignores a key from the branch name that is not in the list.
- When the skill must ask you for the key, it offers the keys of the list.

A repository with no `projects` list accepts each project key.

## The cache

The cache is one file for each project:
`~/.sdlc-cache/jira/<site>/<PROJECT_KEY>.json`. `<site>` is the site host in
lower case with each `.` replaced by `_`. The file is outside your working
tree.

The cache holds the cloud ID and the URL of the site, the current user, the
project, the issue types, the field schemas, the workflows, the link types
and the user mappings.

| Event | What the skill does |
|---|---|
| The cache is missing or incomplete | Builds the cache before the operation. |
| You pass `--force-refresh` | Builds the cache again. |
| An operation fails because of stale data, such as an invalid transition ID or a changed field | Builds the cache again, then tries the operation one more time. |
| An assign operation finds a new user | Saves the user mapping in the cache. |
| A transition starts from a status that the cache does not know | Saves the new transitions in the cache. |
| The same project key has a cache under two or more sites | Asks you which site to use. `--site <host>` answers the question in advance. |

- The cache does not expire on a timer by default.
- The skill does not read or move a cache of an earlier version at
  `.sdlc-v2/jira-cache/<KEY>.json` or `.claude/jira-cache/<KEY>.json`. Copy
  the file to the new location, or let the skill build a new cache.
- With `--skip-workflow-discovery`, the cache marks the workflow of each
  issue type as `unsampled`. The same is true for an issue type that has no
  issue in the project. A transition of such a type reads the live
  transitions of the issue.

## When an operation fails

| Error | Cause | What occurs |
|---|---|---|
| 400 on create or edit | A required field is missing, or a field has the wrong shape | The skill checks the field against the cache. If the field does not match, it rebuilds the cache and tries one more time. |
| 400 on transition | A required field of the transition is missing, such as the resolution | The skill adds the required fields from the cached workflow. |
| Stale transition | The transition ID is not valid now | The skill rebuilds the cache and tries again with the new IDs. |
| 401 | The auth token expired | Connect the Atlassian MCP server again. The skill cannot recover. |
| 403 | You do not have the permission | The skill reports the error. It cannot correct it. |
| 404 on an issue | The issue key is wrong, or the issue is deleted | The skill asks you to check the issue key. |
| 404 on a project | The project key is wrong, or you have no access | The skill checks the cache again. Check that the site is correct. |
| 409 | Two edits at the same time | The skill tries the operation one more time. |
| Failed link check | A URL in the description or the comment failed the check | The skill sends nothing to Jira and shows each failed URL. It does not try again and does not change a URL without your input. |

The skill does not try a failed operation more than one time before it finds
the cause. When an operation still fails after a cache rebuild, the skill
reports the failure to you.
