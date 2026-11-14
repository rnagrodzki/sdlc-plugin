# Proposal

## Why

- Problem: people who find the repository see only the README and raw Markdown. They do not see what the plugin changes in their work, and few install it.
- Evidence: the preplan topic `.sdlc-v2/preplan/2026-11-13-github-pages.md` (23 decisions, approved design reference). No public site exists today.
- Why now: the docs, the dashboard and the ship pipeline are stable enough to show. The design reference is approved.

## What Changes

| Area | Before | After |
|---|---|---|
| Public site | none | Astro static site on GitHub Pages at `https://rnagrodzki.github.io/sdlc-plugin/` |
| Docs reading | raw Markdown on github.com | site pages built from `docs/`, `README.md` and `CHANGELOG.md`, with no copies |
| Internal docs | 5 contributor pages at the top of `docs/` | the 5 pages move to `docs/internal/` and are not published |
| Skill docs pages | prose only | each page adds a `## Steps` and a `### Run facts` table that the site reads |
| CI | no site check | one `Site build` check on each pull request, deploy on each push to `main` |
| Plan rules | no rule for site pages | a plan guardrail and a review check for product pages and image versions |
| README and marketplace | no site link | a site link in the README and a `homepage` field in the marketplace entry |

The evaluator flow after the change (new steps in green, changed steps in amber):

```mermaid
flowchart LR
  E1["Search result, share card, README or marketplace link"]:::new --> E2["Developer opens the site"]:::new
  E2 --> E3["Developer sees the flow, screenshots and recordings"]:::new
  E3 --> E4["Developer copies the install command or stars the repository"]:::changed
  classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px
  classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px
```

## Capabilities

### New Capabilities
- `product-site`: the public pages (Home, product pages, skill pages, all-skills page, docs, release notes), their content sources, their build checks and their accessibility rules.
- `site-deploy`: the pull request check, the change gate, the Lighthouse bar and the deploy to GitHub Pages.

### Modified Capabilities
- none. No skill or MCP tool behavior changes. The skill docs pages only gain tables.

## Impact

| Path | Kind | Change |
|---|---|---|
| `site/**` | doc | new Astro project, tested modules in `site/src/lib/*.mjs` |
| `.github/workflows/site.yml` | CI | new workflow: check, build, Lighthouse, deploy |
| `docs/internal/**` | doc | 5 moved pages and the copied design reference |
| `docs/skills/*.md` | doc | `## Steps` and `### Run facts` tables, new `error-report.md` |
| `docs/skills/README.md`, `docs/getting-started.md` | doc | skill map, handovers, ship step fix |
| `plugins/sdlc/skills/**`, `plugins/sdlc/agents/**` | skill | moved-doc path text only |
| `internal/**`, `tests/**` | doc | moved-doc path text in comments only |
| `.sdlc-v2/config.toml` | template | new guardrail `feature-change-updates-site-pages` |
| `.sdlc-v2/review-dimensions/documentation-review.md` | doc | `site/**` trigger and site checks |
| `README.md`, `.claude-plugin/marketplace.json` | doc | site link, `homepage` field |
| `Taskfile.yml`, `.gitignore` | CI | `site:*` tasks, ignored build folders |

New npm dependencies in `site/package.json` only (approved): `astro`, `@astrojs/sitemap`, `mermaid`, dev `@lhci/cli`. The Go module does not change.
