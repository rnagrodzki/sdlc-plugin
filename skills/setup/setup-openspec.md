# OpenSpec Enrichment Sub-Flow

Enriches `openspec/config.yaml` with a managed block pointing contributors to
`/plan`, `/execute-plan`, and `/ship`. Idempotent: re-running
at the current plugin version is a no-op.

> **Permission context:** This sub-flow inherits the parent skill's permission mode.
> Do NOT call ExitPlanMode, change permission settings, or exit any mode during this sub-flow.

---

## Arguments

- `--remove` — remove the managed block instead of adding/updating it

---

## Workflow

### Step 1 — Call openspec_enrich

Call the `openspec_enrich` MCP tool:

```
openspec_enrich({ remove: <true if the parent passed --remove-openspec, else false> })
```

### Step 2 — Parse and report

Read the tool's output. Report the result:

- `action: "append"` — "Managed block added to openspec/config.yaml."
- `action: "update"` — "Managed block updated to v{version} in openspec/config.yaml."
- `action: "unchanged"` — "openspec/config.yaml already at current version — no changes needed."
- `action: "removed"` — "Managed block removed from openspec/config.yaml."
- `action: "missing"` — "openspec/config.yaml not found. Initialize OpenSpec first (`openspec init`)."
- `action: "skipped-existing-context"` — "openspec/config.yaml already declares a top-level `context:` key. The managed block was not appended to avoid creating a duplicate YAML key. Manually fold sdlc-utilities guidance into your existing `context:` value, then re-run."

If a `warning` field is present, display it.

Return to the parent skill (Step 5 summary).
