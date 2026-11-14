# Custom instructions

Read this page when you want each proposal to follow a rule of your project,
for example a fixed severity for CI rules. You write the guidance one time, and
each run reads it.

```toml
[harden.instructions]
plan-guardrails = ["Prefer error severity for CI rules."]
```

`/sdlc:harden` can follow project guidance that you write in the
`[harden.instructions]` table of `.sdlc-v2/config.toml`. Each list holds
plain-text instructions for one proposal surface. The orchestrator reads them
from the manifest when it drafts proposals. Edit this table by hand.
[`/sdlc:setup`](../setup.md#config-templates) does not ask about it.

| Key | Proposals it shapes | Default | Limit |
|-----|---------------------|---------|-------|
| `plan-guardrails` | Plan guardrails | `[]` | 10 items, 1024 characters each |
| `execute-guardrails` | Execute guardrails | `[]` | 10 items, 1024 characters each |
| `review-dimensions` | Review dimensions | `[]` | 10 items, 1024 characters each |
| `copilot-instructions` | Copilot instructions | `[]` | 10 items, 1024 characters each |

The four lists are independent. A missing table, a missing key and an empty
list all mean "no instructions". The limits apply to each list. `/sdlc:harden`
counts the characters of an item before it trims the item, and it ignores an
item that is blank after the trim. The config schema requires at least 1
character in each item.

**To change the instructions:** edit the lists in `.sdlc-v2/config.toml`, save
the file, then run `/sdlc:harden` again. `/sdlc:harden` reads the table at the start of
each run. The config template at `plugins/sdlc/templates/config.toml` shows
the same four keys.

**What `/sdlc:harden` prints:** after it loads the failure, `/sdlc:harden` prints each
list, one line for each surface, or `Custom harden instructions: none
configured.` when all four lists are empty. In `--from-learnings` mode it
prints the block once, after the first entry that loads.

**Rules that custom instructions never change:**

- No relaxing. An instruction cannot make `/sdlc:harden` relax a rule, or remove a
  rule outside the three removal cases (see
  [What harden can remove](./auto-mode-and-edits.md#what-harden-can-remove)).
- Approval prompts. An instruction cannot add or skip an approval prompt.
- `--auto`. An instruction cannot turn `--auto` on or off. In an `--auto` run
  the instructions still apply, and the summary shows
  `Custom instructions: <N> configured (config.toml [harden.instructions])`,
  where `<N>` is the total number of items in the four lists.
- Target files. Each proposal is still limited to the files of its surface.

**An invalid table stops the run.** An unknown key, a value that is not a list,
more than 10 items, an item that is not a string and an item over 1024
characters each give an error that names the key and a suggested fix. `/sdlc:harden`
does not offer `error-report` for this error, because the cause is the config
and not the plugin. Fix the file and run `/sdlc:harden` again. A `config.toml` that
`/sdlc:harden` cannot read or parse also stops the run. In `--from-learnings` mode
the first such error stops the whole triage run, and `/sdlc:harden` removes no
learnings entry.
