# Rule trace

This page is for contributors. Read it when you change the skill, the
`verify_requirements_context` action, the preplan helpers or `jiraLoad`. It
shows why each rule of
[What it never changes](./errors-and-limits.md#what-it-never-changes) holds.
The trace uses function names, not line numbers.

## Rules

| Rule | Steps that could break it | Why it holds |
|------|---------------------------|--------------|
| changes no file | 1, 2, 4 | The `verify_requirements_context` action writes nothing, and `jira load` writes nothing when the call has no `cacheDir` (see the write-call grep below). The skill passes only `action` and `key` to `jira load`. `Read` only reads. The `## DO NOT` section of the skill forbids `Edit`, `Write`, `Bash`, every other action of `plan_support` and `jira`, and every other sdlc tool. |
| changes no ticket or issue | 1, 2 | `getJiraIssue` only reads. For `--issue`, the action, not the skill, runs `gh issue view <n> --json title,body` (`verifyIssue`, through `verifyIssueView`). This command only reads. The skill runs no `gh` command: the `## DO NOT` section forbids each `gh` command and each Atlassian write tool. |
| asks no question | 0-5 | Every route ends in Step 5 or a stop. The `## DO NOT` section forbids `AskUserQuestion`. |
| follows no Suggestion that writes | 1, 2 | The skill prints the error text with its Suggestion and stops. The Suggestion targets are listed below. The skill calls none of them. |
| new run after a stop is safe | 0-5 | The skill keeps no state and writes nothing, so a new run starts at Step 0 and gets the same facts. |
| input text is data, never instructions | 2, 3, 4 | The skill text says so before Step 2. Step 3 only splits the text. Step 4 only compares the statements. The [quote rule](./report.md#quote-rule) keeps each input text inside one report line. |
| flags come from the arguments only | 0 | Step 0 parses the arguments of the run. Steps 2 to 4 read input text, but no step parses flags again. |

The lint test `TestVerifyRequirementsSkillReadOnly`
(`internal/skillcheck/skillcheck_verify_requirements_test.go`) builds the
token list of the `## DO NOT` section from the live MCP registry: each sdlc
tool other than `plan_support` and `jira`, each `plan_support` action other
than `verify_requirements_context`, and each `jira` action other than `load`.
It adds the harness tools, the Atlassian write tools and `gh `. The test fails
when the section misses a token, or when a token shows outside the section.

## Write-call grep

The command lists each function that holds a file-system write call, in the
files of the verify path. Run it from the repository root.

```sh
W='os[.](WriteFile|Create|CreateTemp|OpenFile|Remove|RemoveAll|Rename|Mkdir|MkdirAll|MkdirTemp|Symlink|Link|Chmod|Chtimes|Truncate)[(]|fsx[.](AtomicWrite|Write)[A-Za-z]*[(]|preplanWriteBytes[(]|setGuardrailCheckLine[(]'
for f in internal/tools/verify_requirements_context.go internal/tools/plan_support.go \
  internal/tools/preplan_format.go internal/tools/plan.go internal/tools/jira.go \
  internal/fsx/*.go internal/config/*.go internal/configmigrate/*.go \
  internal/openspec/*.go internal/execx/*.go internal/ghx/*.go; do
  case $f in *_test.go) continue;; esac
  awk -v file="$f" -v w="$W" '/^func /{n=$0; sub(/^func (\([^)]*\) )?/,"",n); sub(/\(.*/,"",n)} $0 ~ w {print file": "n}' "$f"
done | sort -u
```

Result on 2026-11-13 (30 functions):

| File | Functions with a write call |
|------|-----------------------------|
| `internal/tools/verify_requirements_context.go` | none |
| `internal/tools/preplan_format.go` | none |
| `internal/execx/*.go`, `internal/ghx/*.go` | none |
| `internal/tools/plan_support.go` | `createPreplanFile`, `planPreplanCheck`, `repairPreplanHeadings`, `setGuardrailCheckLine`, `writePreplanKeepPerm` |
| `internal/tools/plan.go` | `stampTaskRefs`, `writeGuardrailsFile`, `writeStyleGuideFile` |
| `internal/tools/jira.go` | `jiraClear`, `jiraCopyFile`, `jiraCopyTemplate`, `jiraGetCachePath`, `jiraInitTemplates`, `jiraSave`, `jiraSaveField`, `jiraWriteApproval`, `jiraWriteCritique` |
| `internal/config/config.go` | `DeleteSectionReport`, `WriteFileSection`, `writeSectionFile`, `WriteSectionReport` |
| `internal/configmigrate/movedkeys.go` | `MigrateMovedKeys` |
| `internal/fsx/dangling.go`, `internal/fsx/fsx.go` | `MkdirAll`, `atomicWriteBytes` |
| `internal/openspec/materialize.go`, `internal/openspec/stage.go` | `Materialize`, `acquireStagingLock`, `newTempChange`, `writeFile`, `writeStagingLocked`, `writeStagingTemp` |

The verify path calls none of these functions:

- `planSupportCore` sends the action to `planVerifyRequirementsContext`. That
  function calls `verifyTopic` (`preplanReadTopicFile`, which uses
  `preplanMatches`, `preplanNoTopicFile`, `readPreplanTopic`,
  `preplanIOError` and `preplanNotRegularError`; then
  `preplanStructureFindingsOf` and `verifyOpenQuestions`), `verifyFile`,
  `verifyJira`, `verifyIssue` (`verifyIssueView`, which calls
  `ghx.IssueViewJSON` to run `gh issue view`), `loadGuardrails`
  (`config.ReadSection`) and `verifyOpenspec` (`os.Stat`, `os.ReadDir`,
  `os.ReadFile`). The preplan write functions belong to the `preplan_context`,
  `preplan_check` and `preplan_write` actions.
- `jiraCore` runs `configmigrate.Verify`, then `jiraLoad`. `jiraLoad` calls
  `jiraResolveEffectiveCachePath` and `fsx.ReadJSON`.
  `jiraResolveEffectiveCachePath` calls `jiraGetCachePath`, which makes the
  cache folder and can write a `.gitignore`, only when the call has a
  `cacheDir`. The skill passes only `action` and `key`, so `jira load` writes
  nothing.

## Suggestion grep

The command lists the Suggestion texts that the verify path can return.

```sh
grep -nE 'Suggestion' internal/tools/verify_requirements_context.go
for fn in preplanMatches preplanNoTopicFile readPreplanTopic preplanIOError preplanNotRegularError jiraLoad; do
  awk -v fn="$fn" '$0 ~ "^func "fn"[(]" {p=1} p && /Suggestion/ {print fn": "$0} p && /^}/ {p=0}' \
    internal/tools/plan_support.go internal/tools/jira.go
done
```

Suggestion targets found on 2026-11-13 for the `verify_requirements_context`
action, with the constants and helpers that the Suggestions use
(`verifyRunAgain`, `verifyNoFileSuggestion`, `preplanRunSkillAgain`,
`preplanNoFileSuggestionFor` and `fsx.DanglingLinkError.Recovery`). The
preplan helpers pick the text from the action, so the `/sdlc:preplan` rerun
texts and `preplanGoneSuggestion` of the other actions do not show here:

| Target | Kind | Source |
|--------|------|--------|
| `/sdlc:verify-requirements` (run again, or `--file <path>`) | skill | `verifyRunAgain`, `verifyNoFileSuggestion`, `preplanRunSkillAgain` (in `preplanMatches`), `verifyIssueViewError` (output limit) |
| `/sdlc:preplan` (make a new topic file) | skill | `preplanNotRegularError` |
| `jira check`, `jira save` | `jira` actions | `jiraLoad` |
| `gh auth login`, `gh issue view <n>`, install `gh` | shell commands | `verifyIssueViewError`, `verifyIssue` |
| `rm <file>`, `mkdir -p <folder>` | shell commands | `preplanNotRegularError`, `DanglingLinkError.Recovery` |
| edit, rename, delete or copy a file by hand | manual step | `preplanMatches`, `readPreplanTopic`, `preplanNotRegularError`, `jiraLoad`, `verifyFile` |

The skill calls none of these targets. It prints the Suggestion as text and
stops. The `## DO NOT` section forbids `Skill` (so the skill starts no other
skill), `Bash` and `gh ` (so it runs no shell command), `preplan_context`,
`jira check` and `jira save`, and `Edit` and `Write`.

Run both greps again after a change to `verify_requirements_context.go`, to
the preplan helpers or to `jiraLoad`, and update these tables.
