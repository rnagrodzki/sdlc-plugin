# Review-finding clusters for harden

Callers: `received-review` Step 11.6, `ship` step `harden`.
The cluster rules are computed by `ship_state({action:"harden_clusters"})`. Do not re-implement them.

## Input

Call `ship_state({action:"harden_clusters", detail:{findings:[...]}})`. `detail.findings` is one
record per finding: `file`, `severity`, `title`, `body`, `verdict`, optional `reason`.

`verdict` is received-review's own: `agree-will-fix | agree-won't-fix | disagree | needs-direction`.
`reason` is a deferral reason from `ship_state defer`: `wont-fix | disagree | needs-direction | below-threshold`.
Do not pass `cannot-verify` findings or findings that were never evaluated.

## Ship mapping

| Ship data | verdict | reason |
|---|---|---|
| finding recorded in `data.healing.fixed` | `agree-will-fix` | — |
| `deferredFindings[]` with reason `wont-fix` | `agree-won't-fix` | `wont-fix` |
| `deferredFindings[]` with reason `disagree` | `disagree` | `disagree` |
| `deferredFindings[]` with reason `needs-direction` | `needs-direction` | `needs-direction` |
| `deferredFindings[]` with reason `below-threshold` | not passed | — |
| finding with neither record (unaccounted) | not passed | — |

## Output

Follow `next`. It names the action for this result.

- `clusters[]`: `{key, findings, failureText, alreadyHardened}`. `key` is the file the cluster
  groups on. Dispatch each cluster where `alreadyHardened` is false.
- `suppressed[]`, `loneDisagree[]`: file paths. List them in the caller's summary.
- `dirtySurfaces[]`: harden surfaces with uncommitted edits in the active worktree.

## Dispatch

```
Skill("harden",
  "--failure-text \"<cluster.failureText>\"
   --skill <caller>
   --step \"<caller step>\"
   --operation \"review-feedback-driven hardening\"
   [--auto when the caller's own --auto was passed]"
)
```

Clusters run one at a time — they edit the same config file.
