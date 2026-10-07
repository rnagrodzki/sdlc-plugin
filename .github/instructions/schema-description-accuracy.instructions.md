---
applyTo: "plugins/sdlc/schemas/**,plugins/sdlc/templates/**,internal/config/**"
---
# schema-description-accuracy — Review Instructions

Prose in JSON schema property descriptions, template comments and Go doc comments must match what the code actually needs, and every prose default must also be a structured schema "default" keyword.

Default severity: high

## Checklist

- When prose says "every X", "all X", "any X" or "each X", grep the code's filtering or iteration logic and confirm the covered set is exactly X. Flag prose broader than the code (for example "every repo" when only registered repos are covered) and prose narrower than the code.
- Sibling prose sites: the schema description, the template comment and the Go doc comment often repeat one claim. When any of them is wrong or is corrected, grep the same phrase in the other two and in `docs/` in the same pass, and report every divergence together.
- Permission-scope claims: trace the code that uses the configured value and confirm the stated scope covers every call.
- Default claims: a default stated in prose must also be a `"default"` keyword in the schema.
- Examples: an example value in prose must pass the property's enum, pattern and type constraints.
