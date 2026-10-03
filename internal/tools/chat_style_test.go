package tools

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// TestChatStyle_Defaults verifies chatStyleFor's zero-config defaults: the
// commstyle defaults (functional audience, plain-language writing standard,
// direct tone, English), no warnings, and a non-empty chat guide wrapped in
// <sdlc_communication_style> (not the plan writing guide).
func TestChatStyle_Defaults(t *testing.T) {
	dir := t.TempDir()

	s := chatStyleFor(dir)

	if s.Audience != "functional" {
		t.Errorf("Audience = %q, want functional", s.Audience)
	}
	if s.WritingStandard != "plain-language" {
		t.Errorf("WritingStandard = %q, want plain-language", s.WritingStandard)
	}
	if s.Tone != "direct" {
		t.Errorf("Tone = %q, want direct", s.Tone)
	}
	if s.Language != "English" {
		t.Errorf("Language = %q, want English", s.Language)
	}
	if len(s.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty", s.Warnings)
	}
	if !strings.Contains(s.Guide, "<sdlc_communication_style>") {
		t.Errorf("Guide = %q, want it wrapped in <sdlc_communication_style>", s.Guide)
	}
	if strings.Contains(s.Guide, "<plan_writing_guide>") {
		t.Errorf("Guide = %q, want the chat guide, not the plan writing guide", s.Guide)
	}
}

// TestChatStyle_LegacyPlanStyleKey verifies a shared key (audience) set
// under the legacy [planStyle] section still resolves, with a "moved to
// [style]" warning — mirroring commstyle.FromSections' legacy fallback for
// plan_prepare's loadPlanStyle.
func TestChatStyle_LegacyPlanStyleKey(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), ""+
		"[planStyle]\n"+
		"audience = \"technical\"\n")

	s := chatStyleFor(dir)

	if s.Audience != "technical" {
		t.Errorf("Audience = %q, want technical", s.Audience)
	}
	found := false
	for _, w := range s.Warnings {
		if strings.Contains(w, "planStyle.audience moved to [style]") {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want a \"planStyle.audience moved to [style]\" entry", s.Warnings)
	}
}

// TestChatStyle_ReadErrorWarns verifies a local.toml that fails to parse is
// not silently treated as "no style config": chatStyleFor still returns the
// commstyle defaults (never an error), with a "Failed to read style config:
// " warning surfacing the read failure instead of discarding it.
func TestChatStyle_ReadErrorWarns(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), "[style\naudience = [\n")

	s := chatStyleFor(dir)

	if s.Audience != "functional" {
		t.Errorf("Audience = %q, want functional (default) despite the read error", s.Audience)
	}
	found := false
	for _, w := range s.Warnings {
		if strings.Contains(w, "Failed to read style config: ") {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want a \"Failed to read style config: \" entry", s.Warnings)
	}
	// A malformed local.toml fails the "style" and "planStyle" reads with
	// the same underlying cause: exactly one warning, not two redundant
	// entries about the same failure.
	if len(s.Warnings) != 1 {
		t.Errorf("Warnings = %v, want exactly 1 entry", s.Warnings)
	}
}
