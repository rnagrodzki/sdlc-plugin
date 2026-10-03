package tools

import (
	"github.com/rnagrodzki/sdlc-plugin/internal/commstyle"
)

// ChatStyle is the plugin-wide communication style a skill follows in chat
// and questions. It is attached to execute_state's and ship_state's "read"
// output (the mandatory first call of the execute and ship skills) so a
// resumed run carries the same audience, tone, writing standard, and
// language as the session-start block (internal/hooks/session_start.go),
// without a second config read. It is the chat-relevant subset of
// commstyle.Style: no plan-only visualDensity/narrativeRules/instructions/
// limits, and Guide holds the chat guide (commstyle.ChatGuide), not the
// plan writing guide.
type ChatStyle struct {
	Audience        string   `json:"audience"`
	WritingStandard string   `json:"writingStandard"`
	Tone            string   `json:"tone"`
	Language        string   `json:"language"`
	Guide           string   `json:"guide"`    // commstyle.ChatGuide(style); same text as the session-start block
	Warnings        []string `json:"warnings"` // [] when none
}

// chatStyleFor resolves the communication style execute_state's and
// ship_state's "read" action attach to their output: the "style" and
// "planStyle" config sections (readStyleSection, shared with plan_prepare's
// loadPlanStyle), merged by commstyle.FromSections. Never returns an
// error: a read failure (malformed local.toml) falls back to commstyle's
// defaults and is folded into Warnings instead, using readStyleSection's
// own "Failed to read <section> config: <cause>" message — mirroring
// FromSections' benign-absence handling for a missing section.
//
// Both sections live in the same local.toml, so a malformed file fails
// both readStyleSection calls with the same underlying cause; only one
// warning is kept (preferring the "style" section's message, the exact
// text the spec names) rather than two redundant entries about the same
// failure.
func chatStyleFor(mainRoot string) ChatStyle {
	shared, sErr := readStyleSection(mainRoot, "style")
	plan, pErr := readStyleSection(mainRoot, "planStyle")
	s := commstyle.FromSections(shared, plan)

	warnings := append([]string{}, s.Warnings...)
	switch {
	case sErr != "":
		warnings = append(warnings, sErr)
	case pErr != "":
		warnings = append(warnings, pErr)
	}

	return ChatStyle{
		Audience:        s.Audience,
		WritingStandard: s.WritingStandard,
		Tone:            s.Tone,
		Language:        s.Language,
		Guide:           commstyle.ChatGuide(s),
		Warnings:        warnings,
	}
}
