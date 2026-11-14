# Skill page style

Rules for every page under `docs/skills/`. The site at `site/` renders these pages, and GitHub shows the same files. The pages explain what a skill is worth to a user. They do not explain how the skill works inside.

## Who reads a skill page

A person who has not used the skill yet. The person wants to know three things, in this order:

1. What problem does this skill remove?
2. What do I type, and what do I get?
3. Is it worth my time?

The page answers these three questions before it shows any flag, state file or error table.

## Principles

| Principle | What it means for the text |
|---|---|
| Sell the value first | The first screen names the user's problem and the result. It does not describe the mechanism. |
| Lead with examples | A real command and its result come before any explanation. Text around an example is short. |
| Short beats complete | A reader who stops after 30 seconds still knows what the skill does. Detail has its own page. |
| Benefit before feature | A capability starts with what the user gains: "Resume a stopped run", not "State file with step records". |
| True, not hype | Every claim matches the skill's `SKILL.md`. Never state a capability the skill does not have. |

## Page shape

An overview page is `docs/skills/<skill>.md`. It has these sections, in this order. A section marked optional can be left out when it has nothing to say.

| # | Heading | Content | Limit |
|---|---|---|---|
| 1 | `# /sdlc:<skill>` | The command name only | one line |
| 2 | Lead (no heading) | One sentence: who it helps and what they get | 1 sentence, 25 words or fewer |
| 3 | `## Why use it` | The problem, then the result. A user story is fine | 2 to 4 lines |
| 4 | `## Example` | One real command and what the user sees or gets | 15 lines or fewer, one code block |
| 5 | `## What you get` | The capabilities, benefit first | 3 to 5 bullets, 1 line each |
| 6 | `## Steps` | The Steps table. Its columns are fixed by the site scan | the table only |
| 7 | `## More examples` (optional) | 1 to 3 more cases, each with its command and result | 10 lines each |
| 8 | `## Reference` (optional) | Links to reference pages, one line each | one line per page |
| 9 | `## Related skills` | The skills that run before or after this one | one line per skill |

An overview page has 150 lines or fewer. A skill with no reference pages usually fits in 60.

## Reference pages

Technical detail has its own page: `docs/skills/<skill>/<topic>.md`. Use a reference page for flags, state files, error tables, traces and writing rules.

- One topic for each page. The H1 names the topic.
- The first paragraph says who needs this page and when.
- A reference page may be long. It still starts with an example when the topic has one.
- The overview page links to each reference page in its `## Reference` section.
- Do not move a heading that other files link to without changing those links in the same commit. Run `git grep -n "<skill>.md#"` first.

## Writing rules

- Write to the reader: "you", present tense, active voice.
- Start a bullet with a verb or with the benefit. Do not start with the skill name.
- One idea in each sentence. 25 words or fewer.
- A code block holds a command a user can copy. Show the result below it, not the internals.
- Name the result in the user's words: "a pull request with a release label", not "a `pr_apply` call".
- No marketing words: "powerful", "seamless", "robust", "effortless". A concrete number or a concrete result replaces them.
- Do not repeat the content of another page. Link to it.
- Code names, paths and commands stay exact. Put them in code formatting.

## Accuracy

The page sells, and it is also a contract. Before a claim goes in, find its source in `plugins/sdlc/skills/<skill>/SKILL.md` or in the tool code. A claim that has no source does not go in. When a skill changes, change its page in the same pull request.

## Review checklist

A page is ready when all answers are yes.

- [ ] A new reader knows the problem and the result after the first screen.
- [ ] An example comes before any explanation of how the skill works.
- [ ] The page has no section that explains internals. Internals are in a reference page.
- [ ] The `## Steps` table is present and valid for the site scan.
- [ ] Each claim matches `SKILL.md`.
- [ ] Each link and heading anchor resolves. `bun run verify` in `site/` prints no line.
- [ ] The page has 150 lines or fewer.

## How the rules are enforced

- The `documentation-review` dimension checks changed pages in `docs/skills/` against this file.
- `site/src/lib/skill-page-shape.test.mjs` checks the heading order, the section limits and the `## Steps` table of each overview page.
- `bun run verify` in `site/` checks that each page has a route and each link resolves.
