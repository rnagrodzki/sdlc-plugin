# Design reference: sdlc site (GitHub Pages)

This folder is the design attachment of the topic file
`.sdlc-v2/preplan/2026-11-13-github-pages.md`. Decision #23 of that file makes
it the source of truth for the look of the site. The user approved it in
preplan on 2026-11-13.

## What is locked and what is not

| Part | Locked | Source of the final value |
|---|---|---|
| Palette, signal green, glass, color fields | Yes | `site-preview.src.html`, the `.site` token block |
| Type: families, sizes, weights, letter spacing | Yes | `site-preview.src.html` |
| Shapes: panels, pills, chips, tiles, the flow shape | Yes | `site-preview.src.html` |
| Layout of each page and its behavior at phone width | Yes | `site-preview.src.html`, `ref-*.jpeg` |
| Motion and interactive elements | Yes | `site-preview.src.html`, decisions #20 to #22 |
| Copy: headings, descriptions, labels, table text | No | The docs under `docs/` and the plan |
| Sample data: step times, counts, the run panel values | No | One real run, with its plugin version (#14) |
| The dashboard image | No | A new capture with no private data (#10, #14) |
| The preview chooser (the tabs Home, Skill page, All skills) and the spec strip at the page end | Not part of the site | Preview tooling only |

The copy in the reference is draft text. Do not treat it as final. Do not
change the theme or the look to fit new copy; change the copy.

## Files

| File | Content |
|---|---|
| `site-preview.src.html` | Source of the 3 reference pages: Home, Skill page (`/sdlc:ship`), All skills. Holds the token `DASHBOARD_DATA_URI` one time. |
| `dashboard.png` | Test capture of the dashboard, 1440x900, sdlc v0.4.12, filtered to one public repository. |
| `build.py` | Builds `sdlc-site-looks.html` from the two files above. |
| `ref-home.jpeg` | Full-page capture of Home at 1440px. |
| `ref-skill-page.jpeg` | Full-page capture of the Skill page at 1440px, step Execute selected. |
| `ref-all-skills.jpeg` | Full-page capture of All skills at 1440px, skill review selected. |

## How to view it

```bash
python3 build.py
open sdlc-site-looks.html        # add #skill or #skills to open another page
```

The page needs network access for the fonts Geist and Geist Mono (Google
Fonts). With no network, the fallback font shows, and the type is not the
reference.

## Checksums (SHA-256)

```
2986f77c7779c269b458e5c7d55d7298fb7a5a05f0c90d468b78e40ca7ea053b  site-preview.src.html
e93645394bd480038a42f1e1f7a3e0b895d9881f9c0f5c1efe70a06530c7f919  dashboard.png
```

A file with another checksum is not the approved reference.

## Contrast fixes

The plan checked each color pair against WCAG 2.2 AA (#17). Five pairs were
below the target. The user approved these fixes. Each fix keeps the hue and
changes only the lightness or the alpha. The checksum above includes them.
`site/src/lib/tokens.test.mjs` checks each pair that the site uses.

| Pair (reference rule) | Target | Before | After | Fix |
|---|---|---|---|---|
| Accent text on the amber field `#E5CDAE` (`.change-title`) | 4.5:1 | 4.34:1 | 4.67:1 | New token `--accent-on-field: #884608` |
| `--go` text on `#DDF1E8` (`.h-t .mono`) | 4.5:1 | 4.20:1 | 4.67:1 | New token `--go-text: #1C7754` |
| Inactive steps with `opacity:.5` (`.seq-step:not(.is-done):not(.is-now)`) | 4.5:1 | 2.16-3.12:1 | 6.88:1 | `color: var(--muted)` and no opacity |
| Switch off track on the panel `#F4F2EF` (`.switch-ui`) | 3:1 | 1.58:1 | 3.24:1 | Track alpha `.22` to `.5` |
| Time-bar part edge on the panel (`.seg`) | 3:1 | 1.43:1 | 3.24:1 | 1px inset edge `rgba(143,74,8,.7)`; the fill stays |

## Rules for the plan and for each task

1. The first task copies this folder into the repository as
   `docs/internal/site-design/` and commits it. Verify the checksums before
   the copy. Git ignores `.sdlc-v2/`, so this folder is not in the repository
   until that task runs.
2. The design system (#16) takes each token from the `.site` block of
   `site-preview.src.html`. It adds no value that the reference does not hold.
3. Each task that builds a page or a component names the reference page and
   its capture as an input.
4. Each such task ends with a check: open the built page and the reference
   side by side at 1440px and at phone width, and list each visible
   difference in theme or look. A difference in copy or in sample data is
   expected. No pixel-diff tool is necessary.
5. A difference in theme or look is a defect. Fix the built page. Only the
   user can approve a change of the reference; such a change updates
   `docs/internal/site-design/` in the same commit.

## Not checked in preplan

- Width below 500px.
- Keyboard use of the new controls.
- Reduced motion.
- Text contrast against WCAG 2.2 AA for each color pair (#17).

The plan must check these. A fix for one of them can change a value of the
reference; rule 5 applies.
