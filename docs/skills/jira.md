# /sdlc:jira

Create, find and update Jira issues from Claude Code, and approve every write before it reaches Jira.

## Why use it

You track your work in Jira, and each change of an issue takes you out of your work and into a browser form.
`/sdlc:jira` takes the request in plain words: create, search, move, comment, link, assign or log work.
It reads the fields and the workflows of your project one time, saves them in a cache, and uses the cache for each later operation.
Before each write, it shows you the full payload and waits for your answer.

## Example

Run the command, then describe the operation in plain words:

```text
/sdlc:jira
Create a task in PROJ: "Add rate limiting to auth API" with priority High
```

On the first run for `PROJ`, the skill builds the cache and reports the number of issue types and workflow states. Then you see, in this order:

- An `Initial:` / `Critique:` / `Final:` block: the draft, the findings of the check, and the corrected payload.
- The full payload. The description has the sections of your template for the issue type, or `## Summary`, `## Context` and `## Acceptance Criteria`.
- A question with three answers: approve, change or cancel.

After approve, the skill checks every link in the description. A failed check stops the operation. A passed check creates the issue in Jira.

## What you get

- **The daily Jira work in one command.** Create, edit, view and search issues, move them through the workflow, comment, link, assign, log work, or ask for many operations in one request.
- **Fast after the first run.** The skill caches the fields, workflows and link types of your project, and makes no more discovery calls until the next refresh.
- **No guessed values.** Field values come from the cache or from you, and the skill asks for each required field that you did not give.
- **Your approval before every write.** You see a critique of the draft and the full payload, then you answer approve, change or cancel.
- **Descriptions with a fixed structure.** The skill fills the template of the issue type, or a base structure, and sends no text with a raw placeholder.

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
| Starts | With `/sdlc:jira`, with or without a flag. Then the user describes the operation in natural language. |
| Ends | After Step 4. It ends earlier with `--init-templates`, with a cancelled approval, or with a failed link check. |
| Asks the user | When the project key is unknown, when the cache exists under more than one site, when the request is ambiguous, for each required field the user did not give, and at the approval of every write operation. |
| Resume | none |

## More examples

**Find issues:**

```text
/sdlc:jira
Find all open bugs assigned to me in PROJ
```

The skill builds the JQL query for the project and shows the result as a table. A search or a view changes nothing in Jira, so it asks for no approval.

**Move an issue to a new status:**

```text
/sdlc:jira
Move PROJ-789 to "In Review"
```

The skill finds the transition in the cached workflow of the issue type. When no transition goes to that status, it lists the available transitions and asks which one to use. You approve the payload before the move.

**Rebuild the cache after a change of the Jira configuration:**

```text
/sdlc:jira --project PROJ --force-refresh
```

The cache does not expire on a timer. This command reads the fields and the workflows of `PROJ` again and saves a new cache.

## Reference

- [Flags, project key and cache](./jira/flags-and-cache.md): the 5 flags, what the skill needs, how it finds the project key, the cache, and what occurs when an operation fails.
- [Description templates](./jira/templates.md): where a template comes from, the base structure, `--init-templates`, placeholders, and the rules for descriptions and comments.

## Related skills

- [/sdlc:setup](./setup.md): sets the default Jira project key that this skill reads.
- [/sdlc:verify-requirements](./verify-requirements.md): checks the requirements of a Jira ticket, and needs the cache that this skill builds.
- [/sdlc:plan](./plan.md): writes an implementation plan for a ticket.
- [/sdlc:execute](./execute.md): executes the plan.
