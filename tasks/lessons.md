# Lessons

## 2026-10-01 — OpenSpec change from ideas file
**Trigger:** User asked to "create the openspec change" (singular) from tmp/ideas.md, then "make sure all ideas are incorporated". I offered a split into one change per idea, marked it "Recommended", and immediately dispatched background agents that started creating separate changes. User wanted one combined change.
**Rule:** If the user's own words name a single deliverable ("the change", "one plan"), keep it single. Do not offer or recommend a split that contradicts that wording. If a split seems better, say why in one line and wait for an explicit "split it" before creating anything.
**Example:** "create the openspec change from those ideas" → one `openspec new change`, all ideas as capabilities/task groups inside it. No parallel agents creating extra change dirs.
