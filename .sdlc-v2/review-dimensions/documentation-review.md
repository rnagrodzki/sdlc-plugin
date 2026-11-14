---
name: documentation-review
description: Docs under docs/, the root README and the site under site/ stay accurate as the setup/ship/review pipeline evolves.
triggers:
  - "docs/**"
  - "site/**"
  - "README.md"
  - "plugins/sdlc/skills/**"
  - "plugins/sdlc/schemas/**"
severity: medium
---

This project keeps user documentation in `docs/` (`getting-started.md`,
`local-install.md`, `dashboard.md`, `versioning.md`, `docs/skills/*.md`) and
contributor documentation in `docs/internal/` (`plan-architecture.md`,
`smoke-test.md`), separate from the runtime skill content under
`plugins/sdlc/skills/`. The site under `site/` builds its skill and docs pages
from the Markdown in `README.md`, `CHANGELOG.md` and `docs/**` (not
`docs/internal/**`). The only text that the site owns is in
`site/src/data/features.mjs` (title, summary, description and claim text of
each product page) and `site/src/data/assets.json` (alt and caption text of
each image). Check:

- Behavioral changes to a skill (`plugins/sdlc/skills/**`) or an MCP tool's
  contract are reflected in `docs/` where relevant, rather than only in the
  skill's own inline prose.
- Code examples and CLI invocations in `docs/` still match current tool
  names and flags (e.g. `setup_prepare`, `--dimensions`) — a renamed tool or
  flag that isn't updated here will mislead the next contributor.
- New root-level scripts, directories, or config files (`.sdlc-v2/`,
  `lefthook.yml`) that a new contributor would need to know about are
  mentioned in `README.md` or `docs/getting-started.md`.
- When a task adds a config-controlled limit, threshold, or cap (e.g.
  `maxParallelDimensions`), the corresponding docs section must state the
  configuration key, the default value, the valid range or constraint, and
  the procedure to change it. A schema description alone does not satisfy
  this — users need user-facing guidance, not only structured metadata.
- Per this session's own memory note, architecture/contributor docs belong
  under `docs/internal/`, not under `skills/` (which is runtime-only) — flag any doc
  content added to `plugins/sdlc/skills/` that reads as contributor-facing
  background rather than skill instructions.
- A changed or new page under `docs/skills/` follows `docs/internal/skill-page-style.md`:
  value and an example come first, the overview page stays at 150 lines or
  fewer, and technical detail (flags, state files, error tables, traces) sits
  in a reference page `docs/skills/<skill>/<topic>.md`. Flag an overview page
  that opens with mechanics, that has no example in its first screen, or that
  states a capability the skill's `SKILL.md` does not have.
- On any configuration path or directory rename (e.g. `.sdlc/` ->
  `.sdlc-v2/`), perform a full-repository grep sweep across `docs/**`,
  `README.md`, `plugins/sdlc/skills/**`, and `plugins/sdlc/schemas/**` to
  catch all references and update them in the same task. Incomplete sweeps
  leave contributors following stale instructions.
- On any SKILL.md step reorder or phase relocation, enumerate every table,
  walkthrough, and lifecycle section in `docs/internal/*-architecture.md` that lists
  that skill's steps by name or order; grep the skill name and check each
  table for the moved phase name and its tool calls at the relocated
  position. Incomplete updates leave contributors following stale order.
- Every limit, threshold, or cap that a doc states, including a hardcoded one
  with no config key, names the set it counts (which items, in which folder,
  per call or in total), the unit, and what happens at the limit (rejected,
  truncated, or ignored). A bare number such as 'at most 100 files' with no
  counted set is a finding. Check the counted set against the code that
  enforces the limit, and check that every other page that states the same
  limit gives the same scope. This extends the config-controlled limit bullet
  above.
- A name or label that a doc gives to a value the code derives (a duration, a
  count, an age, a total) states what the code computes. A span names its start
  point, its end point and its unit, and says what it leaves out. A count names
  what it counts. Read the code that computes the value and compare it with the
  label. A label that names a nearby quantity is a finding. Example: a
  'Duration' column that holds the span from a start stamp to the last file
  change shows elapsed time between two stamps, not time spent.
- A page under `docs/` that states what a tool or skill does, never does, keeps, removes or exposes (for example 'the tool never removes an entry', 'no tool returns the stored table') is checked against the Go handler, not only against SKILL.md. Walk each mode and input form that the handler accepts, and each tool that is registered. A case that breaks the claim is a finding. This applies the negative-claim check of `skill-prose-accuracy` to docs pages. The `skill-doc-drift` check compares docs with SKILL.md only, so a claim that both texts repeat and the code breaks passes it.
- A claim on a page under `docs/` that gives a count of calls, errors or exits, a sequence or outcome ('the dashboard then shows X'), or the values a field can hold (can be empty, may be omitted, one of a set of members) is checked against the Go handler and the code that builds the shown value, not only against SKILL.md. Walk each mode, input form, error exit and stored-file shape, and list what the writer can produce for the field. A claim that holds on the main path only, a documented value that no code path produces, or a producible value that the page leaves out is a finding. This extends the negative-claim bullet above. `skill-doc-drift` checks the same claims against SKILL.md.
- A docs page for a tool-backed skill covers what the Go handler enforces and produces, not only what the page chooses to state. Read the handler and list (1) each cap that a read enforces (a file size, an entry count, a shared path budget) with the set it counts and the effect at the cap, (2) each error that the handler returns for each kind of input the skill accepts (a folder, a file, a link, an issue) with its cause, and (3) the text that the handler writes for each kind of input, such as a row of a structure table. Compare the list with the page. A cap or an error cause that the page omits, a cause that the page gives for the wrong kind of input, or row text that differs from what the handler writes is a finding. This extends the limit bullet above, which checks only a limit that the page states.
- A change to a user-facing feature of the plugin updates the matching entry in
  `site/src/data/features.mjs` and the `docs/` page that the claims of that entry
  link to. A claim on a product page that the plugin no longer supports, or an
  entry that names a removed feature, is a finding.
- Each claim on a product page links to a heading that exists in a published
  page. When a change renames or removes a heading under `docs/`, grep
  `site/src/data/` for the anchor of the old heading. A claim link that points
  to the old anchor is a finding.
- A change to a `docs/skills/<name>.md` page keeps its `## Steps` table and its
  `### Run facts` table in the pinned format. A step that a skill adds, renames
  or removes changes the Steps table of the page in the same change. The site
  builds the skill page from that table, so a stale step name is a finding.
- A file moved into or out of `docs/internal/` changes what the site publishes.
  Grep `docs/`, `README.md` and `site/` for each relative link to the moved
  file. A link from a published page to a file that now sits in
  `docs/internal/` goes to GitHub, not to a site page, and the page text must
  not say 'see the page'.
- The site URL, the base path and the repository blob URL come from
  `site/site.config.mjs`. A file under `site/` that repeats one of these values
  as a literal is a finding. Tests (`*.test.mjs`) may hold the values as
  fixtures. Check: grep `site/` for each value of `SITE`, `BASE` and
  `REPO_BLOB_URL` in `site/site.config.mjs`, with `node_modules`, `dist`,
  `.astro` and `.lighthouseci` excluded. Record in the finding or in the
  verified note the files that the grep matched.
- Each entry of `site/src/data/assets.json` and `site/src/data/step-times.json`
  has a `version`. Flag it when its minor version is older than the newest
  `## [X.Y.0]` heading in `CHANGELOG.md`.
- No image, recording or sample data under `site/` shows a private repository
  name, branch name or ticket key. Check: grep `site/src/data/*.json` and the
  alt and caption text in `site/src/data/assets.json`, then open each file under
  `site/src/assets` and `site/public` and read every repository name, branch
  name and ticket key it shows (the repo filter row of a dashboard capture
  too). Name each file that you opened. A file that you did not open is not
  verified.
- A new build surface (a directory with its own toolchain and lockfile, such as `site/`, or a workflow under `.github/workflows/` that builds, tests or deploys it) has contributor documentation in `README.md` or a page under `docs/internal/`. The text states: the commands to build, test and preview it locally, the checks that CI runs for it and where they run, how and where it deploys, and the tool versions it pins. Check: grep `README.md` and `docs/internal/` (exclude `node_modules`) for the directory name and for the workflow file name. No match for either is a finding. A match that lacks one of the four items is a finding that names the missing item. A change to the commands, the trigger or the deploy target of such a workflow updates that text in the same change.
- A committed file (a docs page, the README, a site data file, a test fixture, a spec) that cites, links to, includes, reads or copies from a path that git ignores or does not track breaks on a clean checkout, in CI and on the published site. Check: list each path that the change names as a source, link target, include or input. For each path, run `git ls-files --error-unmatch <path>` and `git check-ignore -v <path>`. A path that git ignores or that is untracked is a finding, unless the needed content is copied into a tracked file or the text says that the path is local only. Name each path that you checked. This extends the moved-file bullet above, which checks only links to files that moved.
