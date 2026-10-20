---
name: skill-prose-accuracy
severity: high
description: Validates that SKILL.md prose descriptions accurately reflect actual implementation via direct grep/read, not inferred from build/test success alone.
triggers:
  - plugins/sdlc/skills/**/SKILL.md
---

## Rules
- When a SKILL.md describes a mechanism, verify accuracy by direct grep/read against actual source code.
- Prose drift between documentation and implementation is not caught by build/test success; explicit verification required.
- When a SKILL.md section enumerates an MCP tool's manifest or output
  fields (e.g. listing the fields of a review manifest or report-data
  structure), compare the enumeration field-by-field against the
  corresponding tool's `*Out` struct in both directions: every field in the
  prose must exist in the struct, and every required or documented field in
  the struct must be represented in the prose. A mismatch means the section
  is out of sync and must be corrected before the task is marked done.
- Field names cited in skill prose (e.g. task object fields for wave-start)
  must match the tool's input schema exactly — grep the struct, do not
  assume from a similar-sounding prior name (e.g. "title" vs "name").
- Ordering/sequencing claims (e.g. "GATES runs before wave-done") must be
  verified against the actual dispatch code, not assumed from the skill's
  own narrative structure.
- Task-ID format claims in skill prose must match the format actually
  validated in code.
- Verification-procedure prose (e.g. spec-compliance steps) must match the
  actual review-dimension/guardrail text it references or summarizes.
- Preflight/verification claims of the form "if X passes, Y will succeed"
  must be checked against real permission/behavior differences in the
  underlying command — a passing read-only check does not prove a write
  operation will succeed.
- When SKILL.md contains hardcoded enumerations of Go-defined structural sets (section ids from internal/setupmeta.Sections(), local.toml section lists, state-computation rule sections, enum tag values, const blocks, config-source or precedence chains), verify the enumeration re-derives from that canonical source: name every element, confirm the stated count equals the enumeration length, and re-verify after any structural change that no element is silently omitted or misnamed.
- When an instruction or exception overrides generic guidance (e.g., 'use remoteOwner instead of field.default'), place it adjacent to the instruction it overrides, not distant in a section intro or unrelated paragraph.
- When a modification removes a paragraph that carried reader instructions (e.g., a step to collect a field), verify the instruction survives elsewhere in the modified text or an explicit ruling documents why it was dropped; silent deletion of instructions is a failure.
- Field paths and nesting: when prose reads a field from an MCP result (e.g. `writers.missingWriters`), verify the exact nesting matches the handler's `*Out` struct; do not assume a flat structure when the struct nests the field under a parent object.
- ID and naming convention consistency: when prose defines an ID template used in dispatch prompts (e.g. `F-{dimension.name}-n` vs `F-{DIM_SLUG}-n`), verify every occurrence uses the same format as the coordination block or template body; a mismatch between documented and actual IDs breaks prompt-driven processing.
- Item schema field completeness: when a SKILL.md describes an item schema (e.g. inline-exploration findings), verify every field required by a sibling schema (e.g. the `ref` field required for explorer findings) is also documented for it.
- State-format and persistence-claim consistency: when prose claims persistence, garbage-collection, or pruning behavior (e.g. "evidence dirs are reaped by TTL GC", "sibling state files are pruned on every write"), grep the companion state-format doc and the implementation source to verify the claim is accurate and not contradicted by either.
- When multiple sections of a SKILL.md describe the same tool, feature, or behavior (e.g., Gotchas vs Port Notes, Idempotency description vs narrative steps), verify they are consistent on accepted parameters, supported features, and behavior descriptions. Contradictions between sections must be flagged as findings.
- When SKILL.md contains references to specific tool attributes (field names, version schemes, configuration keys, feature properties), grep the relevant Go *In structs and implementation code to verify they still exist. Flag stale references to removed or renamed features, especially when documenting tool capabilities (e.g., "only accepts fields X and Y") where field additions or model changes are easy to miss.
- When a SKILL.md makes a claim about absence or exclusion (e.g., "Port Notes are not recorded here", "this field is not included", "X is not collected", "no second server starts"), verify the claim by grepping the implementation to confirm the named element is indeed absent from the documented scope. When the claim depends on non-obvious verification (conditional logic, a specific test assertion, a configuration pattern), flag it for documentation clarification: the SKILL.md or a companion reference doc should cite the evidence (code location with line range, or test name) so a later reader can confirm the claim without re-deriving it. An absence claim with no cited evidence is a documentation defect, because a reviewer cannot tell it was verified rather than assumed.
- When enumerating a struct's fields in prose — whether as a field list, return-shape specification, or output-manifest description — confirm the enumeration is complete: every exported field in the corresponding Go struct must appear in the prose. A partial list that claims to describe the structure but silently omits fields is a documentation defect.
- When prose makes an exclusivity claim ("only", "sole", "exclusively reads", "the only field X reads"), list every field or element the code actually reads, using grep on the handler, and confirm the claim holds. A claim that "X is the only Y" while the code reads Y and Z is a finding. Example: a sentence that says `fail` reads only `detail.error` is false when the handler also reads other detail fields.
- When prose describes a sequence as unconditional ("always", "every exit", "before every path", "no matter what"), trace each exit path in the skill and in the code. If any path skips a step, for example a resume path, a skip path, or a failure path, the prose must name the condition. An unconditional claim over a conditional sequence is a finding.
- When a SKILL.md lists the members of a set that the flow or the Go code defines (authoring paths, evidence kinds, route names, modes), confirm the list names every member. Walk each route in the flow and each kind the code writes. A member missing from the list is a finding, even when every listed member is accurate.
- When prose makes a negative claim about an effect or about what exists ('never removes', 'never overwrites', 'does not change', 'no tool exposes', 'no other path'), look for the case that breaks it. For an effect claim, walk each mode, input form and stored-file shape that the write path accepts (for example a one-id write and a whole-table write). State what each one does to the data that the claim calls safe. For a claim that nothing provides a capability, grep every `mcpserver.Register` call and each action enum, not only the tool that the prose names. One case that breaks the claim is a finding, even when the common case is true. This extends the absence, exclusivity and unconditional bullets above. They check the named element or the named path, not every mode, input form or tool. The same walk applies to a row or sentence that states when an effect happens or what a flag or mode does to existing data ('replaces', 'overwrites', 'keeps', 'skips', for example an Arguments-table row for an `--add` flag). Read the branch for each flag value and each state of the existing file, and compare the condition the prose states with the condition the code checks. A stated condition that is wider or narrower than the code's condition is a finding.
- When a SKILL.md step lists the results, errors or statuses that a call can return (a result table, a route row or an error list), read the handler and compare the list with the code in both directions. Each listed item must be something that this call returns for the input that the step sends. Each item that the handler returns for that input must be listed, or covered by a stated catch-all row. A row for a result that another action returns, or that this input cannot reach, is a finding. A missing row is a finding too. This extends the set-membership bullet above, which flags only a missing member.
