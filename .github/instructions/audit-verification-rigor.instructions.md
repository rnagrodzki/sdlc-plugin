---
applyTo: ".sdlc-v2/**/*.md,plugins/sdlc/skills/**/*.md"
---
# audit-verification-rigor — Review Instructions

Audit and negative-assertion tasks must verify absence claims with explicit source-reading (grep, code inspection).

Default severity: high

## Checklist

- Claims of absence verified with grep/source inspection
- Verification method documented in task/review notes
- Multi-source claims enumerate specific files with grep results
- "New findings" distinguished from previously-documented ones
- A rule, check or doc sentence that states an absence or an exclusivity (only, never, no X shows Y, sole owner) names the search that proves it: the command or pattern, the scope, the exclusions and the result to record. A claim with no stated search, or one that the source contradicts, is a finding.
- A sweep for literal values (a URL, a base path, a name) names the search pattern for each value, the folders searched and excluded, and the files matched. A sweep with no search method is a finding.
