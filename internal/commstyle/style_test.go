package commstyle

import (
	"reflect"
	"testing"
)

func TestFromSections(t *testing.T) {
	t.Run("defaults with no warnings", func(t *testing.T) {
		s := FromSections(nil, nil)
		if s.Audience != DefaultAudience {
			t.Errorf("Audience = %q, want %q", s.Audience, DefaultAudience)
		}
		if s.WritingStandard != DefaultWritingStandard {
			t.Errorf("WritingStandard = %q, want %q", s.WritingStandard, DefaultWritingStandard)
		}
		if s.Tone != DefaultTone {
			t.Errorf("Tone = %q, want %q", s.Tone, DefaultTone)
		}
		if s.VisualDensity != DefaultVisualDensity {
			t.Errorf("VisualDensity = %q, want %q", s.VisualDensity, DefaultVisualDensity)
		}
		if s.Language != DefaultLanguage {
			t.Errorf("Language = %q, want %q", s.Language, DefaultLanguage)
		}
		if len(s.Warnings) != 0 {
			t.Errorf("Warnings = %v, want none", s.Warnings)
		}
		if s.TechnicalTerms == nil || len(s.TechnicalTerms) != 0 {
			t.Errorf("TechnicalTerms = %v, want empty non-nil", s.TechnicalTerms)
		}
		if s.NarrativeRules == nil || len(s.NarrativeRules) != 0 {
			t.Errorf("NarrativeRules = %v, want empty non-nil", s.NarrativeRules)
		}
		if s.Instructions == nil || len(s.Instructions) != 0 {
			t.Errorf("Instructions = %v, want empty non-nil", s.Instructions)
		}
	})

	t.Run("invalid enum values fall back with one warning each", func(t *testing.T) {
		style := map[string]any{
			"audience":        "mixed",
			"writingStandard": "bogus",
			"tone":            "sarcastic",
		}
		s := FromSections(style, map[string]any{"visualDensity": "medium"})
		if s.Audience != DefaultAudience {
			t.Errorf("Audience = %q, want default %q", s.Audience, DefaultAudience)
		}
		if s.WritingStandard != DefaultWritingStandard {
			t.Errorf("WritingStandard = %q, want default %q", s.WritingStandard, DefaultWritingStandard)
		}
		if s.Tone != DefaultTone {
			t.Errorf("Tone = %q, want default %q", s.Tone, DefaultTone)
		}
		if s.VisualDensity != DefaultVisualDensity {
			t.Errorf("VisualDensity = %q, want default %q", s.VisualDensity, DefaultVisualDensity)
		}
		want := []string{
			`style.audience "mixed" is not valid (technical | functional | executive | general | beginner); using "functional"`,
			`style.writingStandard "bogus" is not valid (ste | plain-language | developer-docs | smart-brevity); using "plain-language"`,
			`style.tone "sarcastic" is not valid (direct | neutral); using "direct"`,
			`planStyle.visualDensity "medium" is not valid (high | balanced | low); using "balanced"`,
		}
		if !reflect.DeepEqual(s.Warnings, want) {
			t.Errorf("Warnings = %v, want %v", s.Warnings, want)
		}
	})

	t.Run("style takes precedence over legacy planStyle, no legacy warning", func(t *testing.T) {
		style := map[string]any{"audience": "executive"}
		plan := map[string]any{"audience": "general"}
		s := FromSections(style, plan)
		if s.Audience != "executive" {
			t.Errorf("Audience = %q, want executive", s.Audience)
		}
		if len(s.Warnings) != 0 {
			t.Errorf("Warnings = %v, want none", s.Warnings)
		}
	})

	t.Run("legacy planStyle value used with moved warning", func(t *testing.T) {
		plan := map[string]any{"audience": "general"}
		s := FromSections(nil, plan)
		if s.Audience != "general" {
			t.Errorf("Audience = %q, want general", s.Audience)
		}
		want := []string{"planStyle.audience moved to [style]; move it there (the value still works)"}
		if !reflect.DeepEqual(s.Warnings, want) {
			t.Errorf("Warnings = %v, want %v", s.Warnings, want)
		}
	})

	t.Run("legacy planStyle language used with moved warning", func(t *testing.T) {
		plan := map[string]any{"language": "Polish"}
		s := FromSections(nil, plan)
		if s.Language != "Polish" {
			t.Errorf("Language = %q, want Polish", s.Language)
		}
		want := []string{"planStyle.language moved to [style]; move it there (the value still works)"}
		if !reflect.DeepEqual(s.Warnings, want) {
			t.Errorf("Warnings = %v, want %v", s.Warnings, want)
		}
	})

	t.Run("plan-only key under style is ignored with warning", func(t *testing.T) {
		style := map[string]any{"visualDensity": "high"}
		plan := map[string]any{"visualDensity": "low"}
		s := FromSections(style, plan)
		if s.VisualDensity != "low" {
			t.Errorf("VisualDensity = %q, want low (style value ignored)", s.VisualDensity)
		}
		want := []string{"style.visualDensity belongs in [planStyle]"}
		if !reflect.DeepEqual(s.Warnings, want) {
			t.Errorf("Warnings = %v, want %v", s.Warnings, want)
		}
	})

	t.Run("verbosity key adds legacy warning", func(t *testing.T) {
		plan := map[string]any{"verbosity": "terse"}
		s := FromSections(nil, plan)
		want := []string{"planStyle.verbosity is no longer used; set visualDensity (high | balanced | low) instead"}
		if !reflect.DeepEqual(s.Warnings, want) {
			t.Errorf("Warnings = %v, want %v", s.Warnings, want)
		}
	})

	t.Run("narrativeRules drops non-strings only", func(t *testing.T) {
		plan := map[string]any{"narrativeRules": []any{"Use short sentences.", 7, "", "  keep spacing  "}}
		s := FromSections(nil, plan)
		want := []string{"Use short sentences.", "", "  keep spacing  "}
		if !reflect.DeepEqual(s.NarrativeRules, want) {
			t.Errorf("NarrativeRules = %#v, want %#v", s.NarrativeRules, want)
		}
	})

	t.Run("instructions drops non-strings and blanks, trims", func(t *testing.T) {
		plan := map[string]any{"instructions": []any{"  always add before -> after  ", 7, "", "   "}}
		s := FromSections(nil, plan)
		want := []string{"always add before -> after"}
		if !reflect.DeepEqual(s.Instructions, want) {
			t.Errorf("Instructions = %#v, want %#v", s.Instructions, want)
		}
	})

	t.Run("technicalTerms cleaning trims lower-cases and drops non-strings and blanks", func(t *testing.T) {
		style := map[string]any{"technicalTerms": []any{" Logging ", 3, ""}}
		s := FromSections(style, nil)
		want := []string{"logging"}
		if !reflect.DeepEqual(s.TechnicalTerms, want) {
			t.Errorf("TechnicalTerms = %#v, want %#v", s.TechnicalTerms, want)
		}
	})

	t.Run("audience technical still loads with max jargon share off", func(t *testing.T) {
		style := map[string]any{"audience": "technical"}
		s := FromSections(style, nil)
		if s.Audience != "technical" {
			t.Errorf("Audience = %q, want technical", s.Audience)
		}
		if s.Limits.MaxJargonShare != 1.00 {
			t.Errorf("Limits.MaxJargonShare = %v, want 1.00", s.Limits.MaxJargonShare)
		}
	})

	t.Run("writingStandard ste sets STE, instruction words, and caps paragraph sentences", func(t *testing.T) {
		style := map[string]any{"writingStandard": "ste"}
		plan := map[string]any{"visualDensity": "low"}
		s := FromSections(style, plan)
		if !s.Limits.STE {
			t.Error("Limits.STE = false, want true")
		}
		if s.Limits.MaxInstructionWords != 20 {
			t.Errorf("Limits.MaxInstructionWords = %d, want 20", s.Limits.MaxInstructionWords)
		}
		if s.Limits.MaxParagraphSentences != 6 {
			t.Errorf("Limits.MaxParagraphSentences = %d, want 6 (low density capped at 6, not 6+)", s.Limits.MaxParagraphSentences)
		}
	})

	t.Run("InstructionsText renders none configured and numbered lines", func(t *testing.T) {
		if got := InstructionsText(nil); got != "none configured" {
			t.Errorf("InstructionsText(nil) = %q, want %q", got, "none configured")
		}
		if got := InstructionsText([]string{"A", "B"}); got != "1. A\n2. B" {
			t.Errorf("InstructionsText([A B]) = %q, want %q", got, "1. A\n2. B")
		}
	})
}

func TestLimitsFor(t *testing.T) {
	t.Run("density table", func(t *testing.T) {
		cases := []struct {
			density               string
			maxProseShare         float64
			maxParagraphSentences int
			maxListItems          int
		}{
			{"high", 0.30, 3, 7},
			{"balanced", 0.50, 5, 9},
			{"low", 0.75, 6, 12},
		}
		for _, c := range cases {
			l := LimitsFor(Style{WritingStandard: "plain-language", VisualDensity: c.density, Language: "English"})
			if l.MaxProseShare != c.maxProseShare || l.MaxParagraphSentences != c.maxParagraphSentences || l.MaxListItems != c.maxListItems {
				t.Errorf("density %q: got {%v %v %v}, want {%v %v %v}", c.density, l.MaxProseShare, l.MaxParagraphSentences, l.MaxListItems, c.maxProseShare, c.maxParagraphSentences, c.maxListItems)
			}
		}
	})

	t.Run("writingStandard table", func(t *testing.T) {
		cases := []struct {
			standard            string
			maxSentenceWords    int
			maxInstructionWords int
			ste                 bool
		}{
			{"ste", 25, 20, true},
			{"plain-language", 25, 0, false},
			{"developer-docs", 30, 0, false},
			{"smart-brevity", 20, 0, false},
		}
		for _, c := range cases {
			l := LimitsFor(Style{WritingStandard: c.standard, VisualDensity: "balanced", Language: "English"})
			if l.MaxSentenceWords != c.maxSentenceWords || l.MaxInstructionWords != c.maxInstructionWords || l.STE != c.ste {
				t.Errorf("standard %q: got {%v %v %v}, want {%v %v %v}", c.standard, l.MaxSentenceWords, l.MaxInstructionWords, l.STE, c.maxSentenceWords, c.maxInstructionWords, c.ste)
			}
		}
	})

	t.Run("audience jargon ladder", func(t *testing.T) {
		cases := map[string]float64{
			"technical":  1.00,
			"functional": 0.15,
			"executive":  0.05,
			"general":    0.00,
			"beginner":   0.00,
		}
		for audience, want := range cases {
			l := LimitsFor(Style{Audience: audience, WritingStandard: "plain-language", VisualDensity: "balanced", Language: "English"})
			if l.MaxJargonShare != want {
				t.Errorf("audience %q: MaxJargonShare = %v, want %v", audience, l.MaxJargonShare, want)
			}
		}
	})

	t.Run("tone controls banned phrases, never nil", func(t *testing.T) {
		direct := LimitsFor(Style{Tone: "direct", WritingStandard: "plain-language", VisualDensity: "balanced", Language: "English"})
		if len(direct.BannedPhrases) == 0 {
			t.Error("tone direct: BannedPhrases is empty, want the hedging phrase list")
		}
		neutral := LimitsFor(Style{Tone: "neutral", WritingStandard: "plain-language", VisualDensity: "balanced", Language: "English"})
		if neutral.BannedPhrases == nil {
			t.Error("tone neutral: BannedPhrases is nil, want empty non-nil slice")
		}
		if len(neutral.BannedPhrases) != 0 {
			t.Errorf("tone neutral: BannedPhrases = %v, want empty", neutral.BannedPhrases)
		}
	})

	t.Run("language gates MaxSentenceWords, any letter case", func(t *testing.T) {
		cases := []struct {
			language string
			want     int
		}{
			{"English", 25},
			{"ENGLISH", 25},
			{"en", 25},
			{"EN", 25},
			{"French", 0},
			{"", 0},
		}
		for _, c := range cases {
			l := LimitsFor(Style{WritingStandard: "ste", VisualDensity: "balanced", Language: c.language})
			if l.MaxSentenceWords != c.want {
				t.Errorf("language %q: MaxSentenceWords = %d, want %d", c.language, l.MaxSentenceWords, c.want)
			}
		}
	})

	t.Run("nil TechnicalTerms normalized to empty non-nil", func(t *testing.T) {
		l := LimitsFor(Style{WritingStandard: "plain-language", VisualDensity: "balanced", Language: "English"})
		if l.TechnicalTerms == nil {
			t.Error("TechnicalTerms is nil, want empty non-nil slice")
		}
	})
}
