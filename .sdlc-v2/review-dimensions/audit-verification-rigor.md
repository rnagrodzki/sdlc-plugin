---
name: audit-verification-rigor
description: Audit and negative-assertion tasks must verify absence claims with explicit source-reading (grep, code inspection).
triggers:
  - ".sdlc-v2/**/*.md"
  - "plugins/sdlc/skills/**/*.md"
severity: high
---

## Checklist
- Claims of absence verified with grep/source inspection
- Verification method documented in task/review notes
- Multi-source claims enumerate specific files with grep results
- "New findings" distinguished from previously-documented ones
- Enumerated rule/check IDs in lane prompts or guidance documents verified
  complete against the external standard they cite (e.g. STE rules in a
  judgment list cross-checked against the STE spec); any missing id is
  named and justified, not silently dropped
- A rule, check or doc sentence that states an absence or an exclusivity
  (only, never, no X shows Y, sole owner) names the search that proves it: the
  command or pattern, the scope, the exclusions and the result to record. The
  claim was tested against the source before it was written; one counter-example
  breaks it, and the claim is then narrowed or the exception is named. A claim
  with no stated search, or one that the source contradicts, is a finding.
- A sweep for literal values (a URL, a base path, a name) names the search
  pattern for each value, the folders searched, the folders excluded and the
  files that the search matched. A sweep with no search method is a finding.
