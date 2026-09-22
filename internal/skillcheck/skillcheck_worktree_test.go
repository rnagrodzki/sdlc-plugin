// This file guards against skill markdown handing the LLM a bare,
// cwd-relative ".sdlc-v2/..." path in a filesystem-verb instruction. A bare
// relative path resolves against the session's current working directory,
// which is the *linked* worktree during a worktree-based run, not the main
// worktree where ".sdlc-v2/" actually lives — see the "Worktree Anchoring"
// plan this test enforces (a bare path here would silently write/read the
// wrong worktree's state).
//
// This file intentionally contains no production logic. Every unexported
// identifier below is prefixed worktreeSkills, and every Test function is
// prefixed TestSkills/TestWorktreeSkills, to avoid name collisions with the
// sibling skillcheck_*_test.go files that share this package.
package skillcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// worktreeSkillsRepoRoot resolves plugins/sdlc from this test file's own
// location (internal/skillcheck/skillcheck_worktree_test.go), independent of
// the directory `go test` happens to be invoked from.
func worktreeSkillsRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("worktreeSkillsRepoRoot: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(file))), "plugins", "sdlc")
}

// worktreeSkillsFiles walks every *.md file under skills/, relative to
// repoRoot, rather than hardcoding a per-family list, so a new skill or
// support doc added anywhere under skills/ is picked up automatically.
// Returned paths are relative to repoRoot with forward slashes (e.g.
// "skills/setup/SKILL.md"), sorted for deterministic test output.
func worktreeSkillsFiles(t *testing.T, repoRoot string) []string {
	t.Helper()
	root := filepath.Join(repoRoot, "skills")
	var rels []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(rels) == 0 {
		t.Fatal("worktreeSkillsFiles: walked zero skill markdown files -- the walk root is likely wrong")
	}
	sort.Strings(rels)
	return rels
}

// worktreeSkillsVerbRe matches a filesystem-operation verb: a tool or
// command that can perform a raw filesystem operation. Case-insensitive so
// it also catches lowercase prose uses ("read", "write", ...).
var worktreeSkillsVerbRe = regexp.MustCompile(`(?i)\b(Write|Read|Glob|Bash|cp|cat|mkdir)\b`)

// worktreeSkillsProhibitionRe recognizes the "do not <verb> a bare path"
// prohibition idiom, which legitimately pairs a filesystem verb with a
// ".sdlc-v2/" path on the same line without instructing the LLM to perform
// that operation (e.g. plan/SKILL.md:91, execute/SKILL.md:25). It is
// deliberately narrow -- same line, verb within 40 chars after the negation
// -- because broadening it to also swallow every phrasing that
// worktreeSkillsProhibitionMisses documents individually below would risk
// silently hiding a real, differently-phrased instruction elsewhere.
var worktreeSkillsProhibitionRe = regexp.MustCompile(`(?i)\b(do not|don't|never|no)\b.{0,40}\b(read|write|glob)\b`)

// worktreeSkillsSdlcTokenRe finds every ".sdlc-v2/" token in a line.
var worktreeSkillsSdlcTokenRe = regexp.MustCompile(`\.sdlc-v2/`)

// worktreeSkillsAnchors are the prefixes that make a ".sdlc-v2/" token a
// properly anchored path rather than a bare, cwd-relative one: an absolute
// path (the token is immediately preceded by "/"), or one of this repo's
// three documented placeholder roots (each always written immediately
// followed by "/.sdlc-v2/", e.g. "<MAIN_ROOT>/.sdlc-v2/").
var worktreeSkillsAnchors = []string{"<MAIN_ROOT>", "<ACTIVE_ROOT>", "<main-worktree>"}

// worktreeSkillsIsBareOccurrence reports whether the ".sdlc-v2/" match
// starting at idx in line is bare (unanchored): not immediately preceded by
// "/", and not immediately preceded by one of worktreeSkillsAnchors.
func worktreeSkillsIsBareOccurrence(line string, idx int) bool {
	before := line[:idx]
	if strings.HasSuffix(before, "/") {
		return false
	}
	for _, a := range worktreeSkillsAnchors {
		if strings.HasSuffix(before, a) {
			return false
		}
	}
	return true
}

// worktreeSkillsLineHasBareSdlcToken reports whether line contains at least
// one bare (unanchored) ".sdlc-v2/" token.
func worktreeSkillsLineHasBareSdlcToken(line string) bool {
	for _, loc := range worktreeSkillsSdlcTokenRe.FindAllStringIndex(line, -1) {
		if worktreeSkillsIsBareOccurrence(line, loc[0]) {
			return true
		}
	}
	return false
}

// worktreeSkillsException documents one line this test would otherwise
// flag, and why it isn't a defect. contains is a short, distinctive
// substring that must still be present in the line at this key's
// "file:line" position -- if the line at that position ever changes (a
// later edit shifts what's there without shifting line numbers, or a
// genuine new defect lands at the exact spot an old exception vacated),
// contains stops matching and the line falls through to be reported as an
// unexplained hit, instead of silently being waved through by a stale key.
type worktreeSkillsException struct {
	contains string
	reason   string
}

// worktreeSkillsDescriptiveNoAction is category (a): lines that
// structurally match "filesystem verb ... bare .sdlc-v2/ path" but do not
// instruct the LLM to perform a filesystem operation at all -- they narrate
// an already-tool-routed operation, an automatic PostToolUse hook, past
// history, an AskUserQuestion menu label shown to the human, or a schema
// doc describing what a *tool* reads internally. Each entry was verified
// individually against this repo's current file content (not assumed from
// a prior report) before being added here. Keyed by "relative/path:line".
var worktreeSkillsDescriptiveNoAction = map[string]worktreeSkillsException{
	"skills/execute/SKILL.md:174": {
		contains: "pipeline-continue",
		reason:   `narrates the automatic "pipeline-continue" PostToolUse hook writing CLI evidence -- no LLM Read/Write/Glob call on this line`,
	},
	"skills/setup/SKILL.md:158": {
		contains: "all exist before any read below",
		reason:   `describes what step 2's setup_init({}) call already ensured -- the actual (disclosed-gap) Reads are on the next lines, not this one`,
	},
	"skills/setup/setup-pr-template.md:14": {
		contains: "stale leftover",
		reason:   `Port Notes blockquote narrating a past fix ("Task 6 — moved off a bare Write to a .sdlc-v2/ path"), not a current instruction`,
	},
	"skills/setup/setup-pr-template.md:124": {
		contains: "write this template as-is",
		reason:   `AskUserQuestion menu option label shown to the human; the actual write happens via setup_init two lines below (Step 6), not here`,
	},
	"skills/ship/SKILL.md:26": {
		contains: "hook records",
		reason:   `narrates the automatic "pipeline-continue" PostToolUse hook recording CLI evidence -- no LLM Read/Write/Glob call on this line`,
	},
	"skills/ship/config-format.md:123": {
		contains: "a per-step automation policy read independently",
		reason:   `schema doc describing what ship_state{action:"next"} reads internally -- not an LLM instruction`,
	},
}

// worktreeSkillsProhibitionMisses is category (b): lines that ARE
// legitimate "do not <verb> a bare .sdlc-v2/ path" prohibition prose, but
// that worktreeSkillsProhibitionRe structurally misses -- an off-list
// prohibited verb, a negation word outside the regex's (do not|don't|
// never|no) alternation, word order that puts the verb before the
// negation, or a sentence wrapped across lines. This is a per-line
// allowlist rather than a broadened worktreeSkillsProhibitionRe precisely
// so a broadening never silently swallows a real, differently-phrased
// instruction elsewhere. Each entry was verified individually against this
// repo's current file content. Keyed by "relative/path:line".
var worktreeSkillsProhibitionMisses = map[string]worktreeSkillsException{
	"skills/execute/SKILL.md:285": {
		contains: "never append to",
		reason:   `"never append to .sdlc-v2/learnings/log.md directly" -- prohibited verb is "append" (not in the read|write|glob alternation), and "write" appears earlier in the same line (in "a later write would land outside it"), before "never"`,
	},
	"skills/setup/setup-dimensions.md:192": {
		contains: "Write` to a `.sdlc-v2/` path",
		reason:   `sentence wraps across 2 lines -- the qualifying "no bare" is on the line above this one`,
	},
	"skills/setup/setup-execution-guardrails.md:146": {
		contains: "Read `.sdlc-v2/config.toml` (bare Read, Glob, or Bash)",
		reason:   `bullet under a "## Do Not" heading -- the negation is the heading two lines above, not on this bullet's own line`,
	},
	"skills/setup/setup-guardrails.md:205": {
		contains: "Read `.sdlc-v2/config.toml` (bare Read, Glob, or Bash)",
		reason:   `bullet under a "## Do Not" heading -- the negation is the heading above, not on this bullet's own line`,
	},
	"skills/setup/setup-guardrails.md:215": {
		contains: "a bare Read of `.sdlc-v2/config.toml` is not",
		reason:   `negation word is "not" ("... is not available to this sub-flow" on the next line), which is not in the (do not|don't|never|no) alternation`,
	},
	"skills/ship/SKILL.md:94": {
		contains: "Never construct a",
		reason:   `"(read-only) -> ... Never construct a .sdlc-v2/ path" -- "read" appears earlier in the same line, before "Never", and the prohibited verb is "construct" (not in the read|write|glob alternation)`,
	},
}

// worktreeSkillsDisclosedGapExceptions is category (c): sites where the
// skill genuinely instructs a bare Read of ".sdlc-v2/config.toml" or
// "local.toml", because no MCP tool exposes a generic full-content read of
// those files. Each site sits next to (or, for setup-pr-labels.md:70, is
// covered by the file's own "## Gotchas" item 1, which names this exact
// step) an explicit "disclosed gap" comment in the skill markdown itself --
// this map does not create the exception, it only records that the skill
// already discloses and justifies it. Kept separate from
// worktreeSkillsProhibitionMisses because it is a different kind of
// exception (a real, intentional bare Read, not a missed prohibition).
// Keyed by "relative/path:line".
var worktreeSkillsDisclosedGapExceptions = map[string]worktreeSkillsException{
	"skills/setup/SKILL.md:162": {
		contains: "projectConfig",
		reason:   `Read of config.toml; lines 165-171 immediately below carry an explicit "Disclosed gap: ... no other MCP tool returns full .sdlc-v2/config.toml / local.toml contents ... has no tool-backed alternative" comment`,
	},
	"skills/setup/SKILL.md:163": {
		contains: "localConfig",
		reason:   `Read of local.toml; same disclosed-gap comment (lines 165-171) as line 162`,
	},
	"skills/setup/SKILL.md:413": {
		contains: "re-call `setup_prepare` and re-Read",
		reason:   `re-Read of config.toml/local.toml; the continuation line immediately below reads "same disclosed gap as Step 0, no tool-backed alternative"`,
	},
	"skills/setup/SKILL.md:753": {
		contains: "Read the current",
		reason:   `Read of config.toml; the line immediately below reads "No MCP tool returns this value, so this Read is a deliberate, disclosed exception"`,
	},
	"skills/setup/SKILL.md:800": {
		contains: "Re-run Step 0's snapshot",
		reason:   `re-Read of config.toml/local.toml; the continuation line immediately below reads "same disclosed gap as Step 0, no tool-backed alternative"`,
	},
	"skills/setup/setup-pr-labels.md:22": {
		contains: "disclosed gap",
		reason:   `Port Notes: "use a direct Read of .sdlc-v2/config.toml as a disclosed gap (see Gotchas)" -- disclosed inline, and detailed in Gotchas item 1`,
	},
	"skills/setup/setup-pr-labels.md:70": {
		contains: "Read `.sdlc-v2/config.toml` (Read tool)",
		reason:   `Step 2's idempotency-check Read; the file's own "## Gotchas" item 1 names this exact step: "Step 2's idempotency check and Step 4's --append seed still read the raw file ... This is a disclosed gap, not a write path"`,
	},
}

// TestSkillsNoBareSdlcPaths fails when any skill markdown hands the LLM a
// bare, cwd-relative ".sdlc-v2/..." path in a filesystem-verb instruction.
//
// A line is a "candidate" when all of the following hold:
//  1. it contains a bare (unanchored) ".sdlc-v2/" token
//     (worktreeSkillsLineHasBareSdlcToken);
//  2. it also contains a filesystem verb (worktreeSkillsVerbRe);
//  3. it does not match the same-line "do not <verb>" prohibition idiom
//     (worktreeSkillsProhibitionRe).
//
// Every candidate must then be explained by exactly one of the three
// per-line allowlists above (descriptive/no-action, a prohibition-regex
// miss, or a disclosed-gap exception), each checked against both its key
// ("file:line") and a recorded content substring so a line that changed at
// an old exception's position cannot silently ride through on a stale key.
// Any candidate left unexplained is a genuine defect and fails the test.
func TestSkillsNoBareSdlcPaths(t *testing.T) {
	repoRoot := worktreeSkillsRepoRoot(t)
	files := worktreeSkillsFiles(t, repoRoot)

	candidates := 0
	seen := map[string]bool{}

	for _, rel := range files {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if !worktreeSkillsLineHasBareSdlcToken(line) {
				continue
			}
			if !worktreeSkillsVerbRe.MatchString(line) {
				continue
			}
			if worktreeSkillsProhibitionRe.MatchString(line) {
				continue
			}

			candidates++
			key := fmt.Sprintf("%s:%d", rel, i+1)
			trimmed := strings.TrimSpace(line)

			if explained := worktreeSkillsCheckException(t, worktreeSkillsDescriptiveNoAction, key, line, seen); explained {
				continue
			}
			if explained := worktreeSkillsCheckException(t, worktreeSkillsProhibitionMisses, key, line, seen); explained {
				continue
			}
			if explained := worktreeSkillsCheckException(t, worktreeSkillsDisclosedGapExceptions, key, line, seen); explained {
				continue
			}

			t.Errorf("%s: bare .sdlc-v2 path in a filesystem operation: %s", key, trimmed)
		}
	}

	if candidates == 0 {
		t.Fatal("TestSkillsNoBareSdlcPaths: extracted zero candidate lines -- the verb/path/prohibition detection logic likely no longer matches this file's style")
	}

	// Every allowlist entry must have matched a real candidate line this
	// run. An entry that matched nothing is dead weight -- either the line
	// it was written for was since fixed/removed, or it never matched to
	// begin with -- and must be pruned rather than left widening the
	// allowlist for no reason.
	for key := range worktreeSkillsDescriptiveNoAction {
		if !seen[key] {
			t.Errorf("worktreeSkillsDescriptiveNoAction[%q] did not match any candidate line this run -- remove the stale entry", key)
		}
	}
	for key := range worktreeSkillsProhibitionMisses {
		if !seen[key] {
			t.Errorf("worktreeSkillsProhibitionMisses[%q] did not match any candidate line this run -- remove the stale entry", key)
		}
	}
	for key := range worktreeSkillsDisclosedGapExceptions {
		if !seen[key] {
			t.Errorf("worktreeSkillsDisclosedGapExceptions[%q] did not match any candidate line this run -- remove the stale entry", key)
		}
	}
}

// worktreeSkillsCheckException looks up key in m. If absent, it returns
// false (not explained by this map). If present but the line no longer
// contains the recorded exception's substring, it reports an error (the
// line at this position changed since the exception was audited, so the
// exception no longer vouches for it) and returns true (handled -- the
// caller must not also report the generic "bare path" error for the same
// line). If present and the substring still matches, it marks the key seen
// and returns true.
func worktreeSkillsCheckException(t *testing.T, m map[string]worktreeSkillsException, key, line string, seen map[string]bool) bool {
	t.Helper()
	exc, ok := m[key]
	if !ok {
		return false
	}
	if !strings.Contains(line, exc.contains) {
		t.Errorf("%s: line at a previously-allowlisted position no longer contains %q -- it may have changed into a genuine, unaudited defect; re-audit this line: %s", key, exc.contains, strings.TrimSpace(line))
		return true
	}
	seen[key] = true
	return true
}

// worktreeSkillsProhibitionSurvivors pins the two prohibition-prose lines
// the task spec calls out by name as required to survive detection via
// worktreeSkillsProhibitionRe itself (not via a per-line allowlist): each
// must still contain a bare ".sdlc-v2/" token and a filesystem verb (so it
// would be a candidate if the prohibition check were removed), and must
// still be caught by worktreeSkillsProhibitionRe (so it is correctly
// excluded, not accidentally flagged).
var worktreeSkillsProhibitionSurvivors = []struct {
	rel  string
	line int
}{
	{"skills/plan/SKILL.md", 91},
	{"skills/execute/SKILL.md", 25},
}

// TestSkillsProhibitionProseSurvivesDetection guards the two prohibition
// sites the task spec names explicitly, so a future change to
// worktreeSkillsProhibitionRe or to either file that stops matching one of
// them is caught here directly, instead of only being noticed as a
// silently-widened or silently-narrowed detector in
// TestSkillsNoBareSdlcPaths.
func TestSkillsProhibitionProseSurvivesDetection(t *testing.T) {
	repoRoot := worktreeSkillsRepoRoot(t)

	for _, s := range worktreeSkillsProhibitionSurvivors {
		path := filepath.Join(repoRoot, filepath.FromSlash(s.rel))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		lines := strings.Split(string(data), "\n")
		if s.line < 1 || s.line > len(lines) {
			t.Fatalf("%s:%d: line number out of range (file has %d lines)", s.rel, s.line, len(lines))
		}
		line := lines[s.line-1]

		if !worktreeSkillsLineHasBareSdlcToken(line) {
			t.Errorf("%s:%d: expected a bare .sdlc-v2/ token on this line, found none -- the file changed and this pin is stale: %s", s.rel, s.line, strings.TrimSpace(line))
		}
		if !worktreeSkillsVerbRe.MatchString(line) {
			t.Errorf("%s:%d: expected a filesystem verb on this line, found none -- the file changed and this pin is stale: %s", s.rel, s.line, strings.TrimSpace(line))
		}
		if !worktreeSkillsProhibitionRe.MatchString(line) {
			t.Errorf("%s:%d: expected worktreeSkillsProhibitionRe to match this prohibition-prose line, it did not: %s", s.rel, s.line, strings.TrimSpace(line))
		}
	}
}

// worktreeSkillsExecuteStateReportWriteRe matches an execute_state call
// that reports and persists via the tool (action:"report", write:true),
// the fix this task requires ship/SKILL.md to use instead of a bare Write
// to a .sdlc-v2/reports/... path.
var worktreeSkillsExecuteStateReportWriteRe = regexp.MustCompile(`execute_state\(\{action:"report",\s*write:true`)

// TestSkillsShipUsesExecuteStateReportWrite is the spec's required positive
// assertion: ship/SKILL.md must persist its execution report through
// execute_state({action:"report", write:true, ...}) -- once per supported
// format (json, md) -- rather than a bare Write to a .sdlc-v2/reports/...
// path.
func TestSkillsShipUsesExecuteStateReportWrite(t *testing.T) {
	repoRoot := worktreeSkillsRepoRoot(t)
	path := filepath.Join(repoRoot, "skills", "ship", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	matches := worktreeSkillsExecuteStateReportWriteRe.FindAllString(string(data), -1)
	if len(matches) < 2 {
		t.Errorf(`ship/SKILL.md: expected at least 2 calls matching execute_state({action:"report", write:true, ...}) (one per report format: json, md), found %d`, len(matches))
	}
}
