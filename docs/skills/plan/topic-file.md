# Preplan topic file

Read this page when you plan from a topic file of [`/sdlc:preplan`](../preplan.md) and want to know how the plan reads each section of the file.

```
/sdlc:plan .sdlc-v2/preplan/2026-10-12-auth-flow.md
```

A topic file is a requirements file. The plan reads its sections by the
rules of this page.

## How the plan reads each section

| Topic section | Plan section |
|---|---|
| Goal, Users and effect, Flows | requirements context |
| Problem and benefit, Risk if wrong | Context |
| Non-goals, `Smallest cut:` line | Key Decisions |
| Decisions rows | requirements |

- If the plan template has no `Context` or `Key Decisions` section, the plan
  records the same content as requirement items.
- A `Decisions` row whose decision starts with `Dropped:` or `Moved:` is not a
  requirement. The `Smallest cut:` line is not a requirement either.
- A scope option under `Non-goals` that starts with `Offered, not picked:` is
  a non-goal, not a requirement. The line `None stated.` gives the Key
  Decisions bullet "Non-goals: none stated" and no discovery question.

The plan writes the smallest cut with the decision text of each picked row,
not the row number. With no `Smallest cut:` line, the smallest cut is all
proposals that are not dropped or moved.

## Empty or absent sections

If `Problem and benefit`, `Risk if wrong` or `Non-goals` is empty or absent
(an older topic file has none of them), the plan asks one discovery question
for each such section, except with `--auto`.

With `--auto`, the plan asks no question. It takes the narrowest reading,
records it in Context or Key Decisions, and adds a `Deviations & assumptions`
row with `asked=no`.

A section that the topic file already fills answers its question, so the plan
does not ask it again. An empty `Goal`, `Users and effect` or `Flows` section
makes the requirements vague, so the plan asks its usual discovery questions.

## Several topic files

Give two or more topic files to plan them in one run:

```
/sdlc:plan .sdlc-v2/preplan/2026-10-12-auth-flow.md .sdlc-v2/preplan/2026-10-12-session-store.md
```

- The plan reads each file with the rules above, in argument order, and
  numbers the requirements across all files.
- For each topic, it writes one non-goal entry and one smallest-cut entry in
  Key Decisions, and one root problem in Context. Each entry starts with the
  topic name. If the plan template has no such section, the plan records the
  entry as a requirement item.
- When two files contradict each other, the plan asks one question for each
  conflict. With `--auto`, the earlier file wins, and the plan records the
  choice in `Deviations & assumptions`.
- The `**Source:**` header of the plan lists the paths, separated by `, `,
  unless an OpenSpec change sets the source. A resumed run reads the paths
  back from that header.

The plan checks the paths before it starts a run. If a path is not a topic
file, the plan stops and prints the path. `--spec`, with or without a change
name, also stops the plan when it comes with two or more paths. Put a path
with spaces in double quotes. The [flags](./flags-and-gotchas.md#flags) table
has the full rule.
