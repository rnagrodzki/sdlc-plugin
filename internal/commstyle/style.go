// Package commstyle owns the chat and plan communication style settings
// shared by every sdlc skill: the allowed values, defaults, precedence
// between the [style] and [planStyle] config sections, and the numeric
// limits derived from a resolved Style. It is a pure package (no
// internal/config import) so plan_prepare, validate, setupmeta, and the
// session-start hook can all depend on it without an import cycle.
package commstyle

import (
	"fmt"
	"slices"
	"strings"
)

// Allowed values for the enum-shaped style keys.
var (
	Audiences        = []string{"technical", "functional", "executive", "general", "beginner"}
	WritingStandards = []string{"ste", "plain-language", "developer-docs", "smart-brevity"}
	Tones            = []string{"direct", "neutral"}
	VisualDensities  = []string{"high", "balanced", "low"}
)

// Defaults applied when a key is absent from both config sections, or when
// a configured value fails enum validation.
const (
	DefaultAudience        = "functional"
	DefaultWritingStandard = "plain-language"
	DefaultTone            = "direct"
	DefaultVisualDensity   = "balanced"
	DefaultLanguage        = "English"
)

// Style keys come in two groups, resolved one by one in FromSections:
//
//   - Shared keys (audience, writingStandard, tone, language,
//     technicalTerms) are read by every sdlc skill. They live in [style],
//     with a legacy fallback to [planStyle] (warning, value still works).
//   - Plan-only keys (visualDensity, narrativeRules, instructions) are read
//     only by the plan skill. They live in [planStyle]; a value found under
//     [style] instead is ignored with a warning.

// directBannedPhrases are the hedging/flattery phrases flagged when
// tone is "direct". tone "neutral" carries no banned phrases.
var directBannedPhrases = []string{
	"great question",
	"great idea",
	"excellent question",
	"excellent point",
	"you might want to consider",
	"you may want to consider",
	"it might be worth",
	"it is worth noting",
	"it's worth noting",
	"i hope this helps",
	"happy to help",
	"feel free to",
	"needless to say",
	"as you can see",
	"to be honest",
}

// densityLimit holds the prose/paragraph/list ceilings for one
// visualDensity value.
type densityLimit struct {
	maxProseShare         float64
	maxParagraphSentences int
	maxListItems          int
}

var densityLimits = map[string]densityLimit{
	"high":     {maxProseShare: 0.30, maxParagraphSentences: 3, maxListItems: 7},
	"balanced": {maxProseShare: 0.50, maxParagraphSentences: 5, maxListItems: 9},
	"low":      {maxProseShare: 0.75, maxParagraphSentences: 6, maxListItems: 12},
}

// standardLimit holds the sentence/instruction word ceilings and the STE
// flag for one writingStandard value.
type standardLimit struct {
	maxSentenceWords    int
	maxInstructionWords int
	ste                 bool
}

var standardLimits = map[string]standardLimit{
	"ste":            {maxSentenceWords: 25, maxInstructionWords: 20, ste: true},
	"plain-language": {maxSentenceWords: 25, maxInstructionWords: 0, ste: false},
	"developer-docs": {maxSentenceWords: 30, maxInstructionWords: 0, ste: false},
	"smart-brevity":  {maxSentenceWords: 20, maxInstructionWords: 0, ste: false},
}

// audienceJargon is the max share of jargon prose sentences tolerated, by
// audience. 1.00 means the check is off.
var audienceJargon = map[string]float64{
	"technical":  1.00,
	"functional": 0.15,
	"executive":  0.05,
	"general":    0.00,
	"beginner":   0.00,
}

// Limits is the numeric form of a Style: the thresholds the readability
// and STE checks enforce.
type Limits struct {
	MaxProseShare         float64  `json:"maxProseShare"`
	MaxParagraphSentences int      `json:"maxParagraphSentences"`
	MaxListItems          int      `json:"maxListItems"`
	MaxSentenceWords      int      `json:"maxSentenceWords"` // 0 = off
	MaxJargonShare        float64  `json:"maxJargonShare"`   // 1.0 = off
	BannedPhrases         []string `json:"bannedPhrases"`
	STE                   bool     `json:"ste"`                 // strict STE checks on
	MaxInstructionWords   int      `json:"maxInstructionWords"` // ste: 20; else 0 = off
	TechnicalTerms        []string `json:"technicalTerms"`      // words exempt from STE word checks
}

// Style is the resolved communication style: the [style] and [planStyle]
// config sections merged, validated, and defaulted by FromSections.
type Style struct {
	Audience        string   `json:"audience"`
	WritingStandard string   `json:"writingStandard"`
	Tone            string   `json:"tone"`
	VisualDensity   string   `json:"visualDensity"`
	Language        string   `json:"language"`
	TechnicalTerms  []string `json:"technicalTerms"` // code and product names exempt from STE word checks
	NarrativeRules  []string `json:"narrativeRules"`
	Instructions    []string `json:"instructions"`
	Warnings        []string `json:"warnings"`
	Limits          Limits   `json:"limits"`
	WritingGuide    string   `json:"writingGuide"` // filled by the guide builder
}

// InstructionsText renders instructions as "1. <text>" lines, one per
// line, or "none configured" when the list is empty.
func InstructionsText(instructions []string) string {
	if len(instructions) == 0 {
		return "none configured"
	}
	lines := make([]string, len(instructions))
	for i, instr := range instructions {
		lines[i] = fmt.Sprintf("%d. %s", i+1, instr)
	}
	return strings.Join(lines, "\n")
}

// FromSections normalizes the raw [style] and [planStyle] config maps
// (nil means the section is absent) into a Style. A shared key is read
// from [style]; when [style] lacks it and [planStyle] has it, the
// [planStyle] value is used and one legacy warning is added. A plan-only
// key found under [style] is ignored, with a warning. Every invalid enum
// value falls back to its default, with a warning.
func FromSections(style, plan map[string]any) Style {
	s := Style{
		TechnicalTerms: []string{},
		NarrativeRules: []string{},
		Instructions:   []string{},
		Warnings:       []string{},
	}

	s.Audience = resolveEnumShared(style, plan, "audience", Audiences, DefaultAudience, &s.Warnings)
	s.WritingStandard = resolveEnumShared(style, plan, "writingStandard", WritingStandards, DefaultWritingStandard, &s.Warnings)
	s.Tone = resolveEnumShared(style, plan, "tone", Tones, DefaultTone, &s.Warnings)
	s.Language = resolveTextShared(style, plan, "language", DefaultLanguage, &s.Warnings)
	s.TechnicalTerms = resolveTermsShared(style, plan, &s.Warnings)

	s.VisualDensity = resolvePlanOnlyEnum(style, plan, "visualDensity", VisualDensities, DefaultVisualDensity, &s.Warnings)
	s.NarrativeRules = resolvePlanOnlyList(style, plan, "narrativeRules", false, &s.Warnings)
	s.Instructions = resolvePlanOnlyList(style, plan, "instructions", true, &s.Warnings)

	if _, ok := plan["verbosity"]; ok {
		s.Warnings = append(s.Warnings, "planStyle.verbosity is no longer used; set visualDensity (high | balanced | low) instead")
	}

	s.Limits = LimitsFor(s)
	return s
}

// LimitsFor derives the numeric Limits for a resolved Style: the
// visualDensity table, the writingStandard table (STE paragraph-sentence
// cap included), the audience jargon ladder, and the tone banned-phrase
// list. An unrecognized value (e.g. a Style built without FromSections)
// falls back to the matching default rather than panicking.
func LimitsFor(s Style) Limits {
	density, ok := densityLimits[s.VisualDensity]
	if !ok {
		density = densityLimits[DefaultVisualDensity]
	}
	standard, ok := standardLimits[s.WritingStandard]
	if !ok {
		standard = standardLimits[DefaultWritingStandard]
	}
	jargon, ok := audienceJargon[s.Audience]
	if !ok {
		jargon = audienceJargon[DefaultAudience]
	}

	maxParagraphSentences := density.maxParagraphSentences
	if standard.ste && maxParagraphSentences > 6 {
		maxParagraphSentences = 6
	}

	maxSentenceWords := standard.maxSentenceWords
	lang := strings.ToLower(strings.TrimSpace(s.Language))
	if lang != "english" && lang != "en" {
		maxSentenceWords = 0
	}

	bannedPhrases := []string{}
	if s.Tone != "neutral" {
		bannedPhrases = append(bannedPhrases, directBannedPhrases...)
	}

	technicalTerms := s.TechnicalTerms
	if technicalTerms == nil {
		technicalTerms = []string{}
	}

	return Limits{
		MaxProseShare:         density.maxProseShare,
		MaxParagraphSentences: maxParagraphSentences,
		MaxListItems:          density.maxListItems,
		MaxSentenceWords:      maxSentenceWords,
		MaxJargonShare:        jargon,
		BannedPhrases:         bannedPhrases,
		STE:                   standard.ste,
		MaxInstructionWords:   standard.maxInstructionWords,
		TechnicalTerms:        technicalTerms,
	}
}

// stringValue reads key from m as a string. A nil map, an absent key, or
// a present key whose value is not a string all report ok=false.
func stringValue(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// validateOrWarn returns raw when it is one of allowed; otherwise it
// appends the "not valid" warning and returns def.
func validateOrWarn(section, key, raw string, allowed []string, def string, warnings *[]string) string {
	if slices.Contains(allowed, raw) {
		return raw
	}
	*warnings = append(*warnings, fmt.Sprintf(`%s.%s "%s" is not valid (%s); using "%s"`, section, key, raw, strings.Join(allowed, " | "), def))
	return def
}

// resolveEnumShared resolves one shared-key enum value: [style] first,
// then a legacy [planStyle] fallback (with a "moved" warning), then def.
func resolveEnumShared(style, plan map[string]any, key string, allowed []string, def string, warnings *[]string) string {
	if raw, ok := stringValue(style, key); ok {
		return validateOrWarn("style", key, raw, allowed, def, warnings)
	}
	if raw, ok := stringValue(plan, key); ok {
		*warnings = append(*warnings, fmt.Sprintf("planStyle.%s moved to [style]; move it there (the value still works)", key))
		return validateOrWarn("planStyle", key, raw, allowed, def, warnings)
	}
	return def
}

// resolveTextShared resolves one shared-key free-text value (no enum
// check): [style] first, then a legacy [planStyle] fallback (with a
// "moved" warning), then def.
func resolveTextShared(style, plan map[string]any, key string, def string, warnings *[]string) string {
	if raw, ok := stringValue(style, key); ok && raw != "" {
		return raw
	}
	if raw, ok := stringValue(plan, key); ok && raw != "" {
		*warnings = append(*warnings, fmt.Sprintf("planStyle.%s moved to [style]; move it there (the value still works)", key))
		return raw
	}
	return def
}

// resolveTermsShared resolves the shared technicalTerms list: [style]
// first, then a legacy [planStyle] fallback (with a "moved" warning).
// Values are trimmed, lower-cased, and non-strings/blanks are dropped.
func resolveTermsShared(style, plan map[string]any, warnings *[]string) []string {
	if raw, ok := style["technicalTerms"].([]any); ok {
		return cleanTerms(raw)
	}
	if raw, ok := plan["technicalTerms"].([]any); ok {
		*warnings = append(*warnings, "planStyle.technicalTerms moved to [style]; move it there (the value still works)")
		return cleanTerms(raw)
	}
	return []string{}
}

// cleanTerms trims, lower-cases, and drops non-strings and blanks.
func cleanTerms(raw []any) []string {
	out := make([]string, 0, len(raw))
	for _, el := range raw {
		s, ok := el.(string)
		if !ok {
			continue
		}
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// resolvePlanOnlyEnum resolves one plan-only enum value from
// [planStyle] only. A same-named key under [style] is ignored, with a
// "belongs in [planStyle]" warning.
func resolvePlanOnlyEnum(style, plan map[string]any, key string, allowed []string, def string, warnings *[]string) string {
	if _, ok := style[key]; ok {
		*warnings = append(*warnings, fmt.Sprintf("style.%s belongs in [planStyle]", key))
	}
	if raw, ok := stringValue(plan, key); ok {
		return validateOrWarn("planStyle", key, raw, allowed, def, warnings)
	}
	return def
}

// resolvePlanOnlyList resolves one plan-only list value from
// [planStyle] only. A same-named key under [style] is
// ignored, with a "belongs in [planStyle]" warning. Non-strings are
// always dropped; when trimAndDropBlank is true, values are also
// trimmed and blanks are dropped (the instructions behavior).
func resolvePlanOnlyList(style, plan map[string]any, key string, trimAndDropBlank bool, warnings *[]string) []string {
	if _, ok := style[key]; ok {
		*warnings = append(*warnings, fmt.Sprintf("style.%s belongs in [planStyle]", key))
	}
	raw, ok := plan[key].([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(raw))
	for _, el := range raw {
		s, ok := el.(string)
		if !ok {
			continue
		}
		if trimAndDropBlank {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}
