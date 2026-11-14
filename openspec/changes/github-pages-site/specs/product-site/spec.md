# Spec Delta

## Purpose

The public product and docs site of the plugin. It shows evaluators what the plugin does and gives users the docs as site pages built from the repository Markdown.

## ADDED Requirements

### Requirement: Pages come from the repository Markdown
The site SHALL build one page for each published Markdown file and SHALL NOT keep a copy of the text. Published files are `README.md`, `CHANGELOG.md` and each `docs/**/*.md` except `docs/internal/**`.

| Source | Route under `/sdlc-plugin` |
|---|---|
| `README.md` | `/docs/` |
| `docs/skills/README.md` | `/skills/` |
| `docs/skills/<name>.md` | `/skills/<name>/` |
| `docs/<path>.md` | `/docs/<path>/` |
| `CHANGELOG.md` | `/releases/` |

#### Scenario: Docs page published
- **WHEN** `docs/getting-started.md` exists
- **THEN** the site has the page `/sdlc-plugin/docs/getting-started/`

#### Scenario: Internal page not published
- **WHEN** a file is under `docs/internal/`
- **THEN** the site has no page for it

#### Scenario: Two files give one route
- **WHEN** two published files map to one route
- **THEN** the build stops with the error code `route-clash`

### Requirement: A broken docs link stops the build
The site build SHALL stop when a relative link in a published page names a file that does not exist. A link to an unpublished repository file SHALL become a GitHub URL.

#### Scenario: Missing link target
- **WHEN** a published page links `missing.md` and no such file exists
- **THEN** the build stops with the error code `missing-target` and names the file and the link

#### Scenario: Link to an internal page
- **WHEN** `docs/skills/README.md` links `../internal/smoke-test.md`
- **THEN** the site link is `https://github.com/rnagrodzki/sdlc-plugin/blob/main/docs/internal/smoke-test.md`

### Requirement: The output scan finds broken content
The site check SHALL scan the built pages and SHALL fail when it finds a broken link, a broken anchor, a broken media path, a leftover `.md` link, a missing route, a missing step name or a bad Steps table.

#### Scenario: Clean output
- **WHEN** the scan finds no finding
- **THEN** `bun run verify` exits 0 and prints no line

#### Scenario: Finding present
- **WHEN** a page links an anchor that does not exist
- **THEN** `bun run verify` prints `<route> broken-anchor <detail>` and exits 1

#### Scenario: Read error
- **WHEN** the scan cannot read a built file
- **THEN** `bun run verify` prints `read error <file>: <message>` and exits 2

### Requirement: Skill pages come from Steps tables
Each skill docs page SHALL hold a `## Steps` table and a `### Run facts` table in the pinned format. Its site page SHALL show each step, and a bad table SHALL fail the check.

#### Scenario: Valid Steps table
- **WHEN** `docs/skills/ship.md` has a valid Steps table
- **THEN** `/skills/ship/` shows each step name of the table

#### Scenario: Missing Steps table
- **WHEN** a skill docs page has no `## Steps` heading
- **THEN** the scan prints `/skills/<name>/ bad-steps-table no-section`

#### Scenario: Duplicate step name
- **WHEN** two Steps rows have one step name
- **THEN** the build stops with the error code `duplicate-key`

### Requirement: Home and product pages
The Home page SHALL start with the pipeline flow and the install command. The site SHALL have 4 product pages, and each claim on a product page SHALL link to a docs heading.

#### Scenario: Product page claim
- **WHEN** the `dashboard` product page shows a claim
- **THEN** the claim links to a docs heading, and the scan fails if the anchor does not exist

#### Scenario: Feature with no claims
- **WHEN** a feature entry has no claims
- **THEN** the site builds no page for it and Home shows it with no link

### Requirement: All-skills page
The `/skills/` page SHALL show the ship steps, preplan, plan and the harden lane as one closed loop, plus a tile for each other skill and a handover table.

#### Scenario: No script
- **WHEN** the browser runs no script
- **THEN** the map and the handover table show all steps and handovers

### Requirement: Install command and star on every page
Each page SHALL show the install commands and a link that stars the repository.

#### Scenario: Copy the install command
- **WHEN** the user activates the copy button with the keyboard
- **THEN** the install command is copied, or selected for a manual copy when no clipboard API exists

### Requirement: Media show their plugin version
Each screenshot and recording SHALL show its caption and the plugin version of its capture. A video SHALL NOT play by itself.

#### Scenario: Unknown asset id
- **WHEN** a page names an asset id that is not in `site/src/data/assets.json`
- **THEN** the build stops with `unknown asset id <id>`

### Requirement: Accessible pages
Each page SHALL meet WCAG 2.2 AA text contrast of 4.5:1, work with the keyboard only, and stop all motion under `prefers-reduced-motion: reduce`.

#### Scenario: Reduced motion
- **WHEN** the user sets reduced motion
- **THEN** no animation, typing effect or signal moves on any page

#### Scenario: Color pair below the minimum
- **WHEN** a declared text color pair has a contrast below 4.5:1
- **THEN** the site unit tests fail

### Requirement: Search and share metadata
Each page SHALL have a title, a description, Open Graph tags with an absolute image URL, and an entry in the sitemap.

#### Scenario: Share card
- **WHEN** a page link is shared
- **THEN** the card shows the page title, description and `https://rnagrodzki.github.io/sdlc-plugin/og/default.png`

### Requirement: Diagrams render in the browser
A page with a Mermaid block SHALL render it as a diagram. A page with no block SHALL load no Mermaid code. A diagram that fails to render SHALL stay as its code block.

#### Scenario: Page with no diagram
- **WHEN** Home loads
- **THEN** the browser loads no Mermaid chunk

#### Scenario: Render error
- **WHEN** one diagram fails to render
- **THEN** that block stays as code and the other diagrams render
