// Package skillcheck cross-checks the received-review skill
// (plugins/sdlc/skills/received-review/SKILL.md) against the live MCP registry
// and against the harden skill it dispatches in Step 11.6. Issue #51 was this
// dispatch: received-review told harden to run with --auto, and harden did not
// document that flag.
//
// The shared matching and registry helpers live in skillcheck_flags_test.go.
// Every unexported identifier below is prefixed receivedReviewSkills, and the
// one declared Test function is TestReceivedReviewSkillParity.
package skillcheck

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// receivedReviewSkillsSkillValueRe reads the value of --skill in a dispatch.
var receivedReviewSkillsSkillValueRe = regexp.MustCompile(`--skill\s+([a-z][a-z0-9-]*)`)

func TestReceivedReviewSkillParity(t *testing.T) {
	t.Run("tools_and_actions_resolve", func(t *testing.T) {
		flagParityAssertToolsResolve(t, "received-review",
			"received_review_prepare",
			"received_review_verify",
			"links_validate",
			"learnings_log:append",
			// Step 4 records every unfixed finding through this action.
			"ship_state:defer",
		)
	})

	// Under --auto only agree-will-fix ends a finding; every other outcome
	// becomes needs-direction and is recorded. The skill text is the whole
	// implementation of that rule, so these are the assertions that keep it
	// from being edited back out.
	t.Run("auto_mode_has_one_terminal_disposition", func(t *testing.T) {
		body := flagParityBody(executeSkillsReadFile(t,
			filepath.Join(executeSkillsRepoRoot(t), "skills", "received-review", "SKILL.md")))

		for _, want := range []string{
			"needs-direction", // the renamed verdict
			`action:"defer"`,  // the durable record
			"below-threshold", // the ledger groups deferred records by reason
			"UNACCOUNTED",     // a mismatch is named, never swallowed
			"/sdlc:deferred",  // the backlog has a stated next step
			"two or more",     // the >=2-approaches rule
		} {
			if !strings.Contains(body, want) {
				t.Errorf("received-review no longer states %q — the no-silent-skip rule is incomplete", want)
			}
		}

		// "needs discussion" was renamed. One mention survives, in the Step 4
		// bullet that records the rename; anything beyond that is a site the
		// rename missed.
		if n := strings.Count(strings.ToLower(body), "needs discussion"); n > 1 {
			t.Errorf(`"needs discussion" appears %d times, want at most 1 (the rename note in Step 4)`, n)
		}
	})

	t.Run("argument_hint_flags_are_described", func(t *testing.T) {
		content := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "skills", "received-review", "SKILL.md"))
		hint := flagParityHintFlags(content)
		if len(hint) == 0 {
			t.Fatal("received-review has no flags in its argument-hint frontmatter")
		}
		body := flagParityBody(content)
		for f := range hint {
			// The body must explain the flag: name it as the start of an
			// inline code span, as in "Parse `--pr <number>` if present".
			if !regexp.MustCompile("`" + regexp.QuoteMeta(f) + "[ `]").MatchString(body) {
				t.Errorf("argument-hint lists %s, but the body never describes it in a code span", f)
			}
		}
	})

	t.Run("harden_dispatch_is_documented_by_harden", func(t *testing.T) {
		skills := flagParityLoadSkills(t)
		content, ok := skills["received-review"]
		if !ok {
			t.Fatal("skills/received-review/SKILL.md not found")
		}
		if _, ok := skills["harden"]; !ok {
			t.Fatal("skills/harden/SKILL.md not found")
		}
		known := make(map[string]bool, len(skills))
		for name := range skills {
			known[name] = true
		}

		dispatched := map[string]bool{}
		for _, d := range flagParityExtract("received-review", content, known) {
			if d.to == "harden" {
				dispatched[d.flag] = true
			}
		}

		// Step 11.6 dispatches these flags. If one disappears from the
		// dispatch, or the matcher stops seeing it, this fails loudly
		// instead of the check below passing on an empty set.
		for _, f := range []string{"--failure-text", "--skill", "--step", "--operation", "--auto"} {
			if !dispatched[f] {
				t.Errorf("received-review no longer dispatches %s to harden in Step 11.6", f)
			}
		}

		documented := flagParityDocumented(skills["harden"])
		for f := range dispatched {
			if !documented[f] {
				t.Errorf("received-review dispatches %s to harden, but harden does not document that flag", f)
			}
		}

		// harden files its learnings under the caller named by --skill.
		calls := 0
		for _, c := range flagParitySkillCalls(content) {
			if c.to != "harden" {
				continue
			}
			calls++
			got := ""
			if m := receivedReviewSkillsSkillValueRe.FindStringSubmatch(flagParityBlankQuoted(content[c.start:c.end])); m != nil {
				got = m[1]
			}
			if got != "received-review" {
				t.Errorf("the harden dispatch must pass --skill received-review, got --skill %q", got)
			}
		}
		if calls == 0 {
			t.Error(`found no Skill("harden", ...) call in received-review/SKILL.md`)
		}
	})
}
