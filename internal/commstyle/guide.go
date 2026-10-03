package commstyle

import (
	"fmt"
	"strings"
)

// chatScope and chatQuestions are the fixed <scope> and <questions> tag
// bodies in ChatGuide. Neither varies with Style.
const (
	chatScope = `Apply to every explanation, status line, summary, and AskUserQuestion text in every sdlc skill.
Do not apply to commit messages, PR bodies, review comments, or Jira text. They follow their own templates and config.`
	chatQuestions = `- Before you ask, state what is decided, why it matters, and the facts the reader needs. Do not assume the reader remembers earlier context.
- Give each option a concrete consequence: what it does, what it costs, what it risks.
- Put the recommended option first and mark it "(Recommended)".`
)

// Guide builds the plan writing guide: 9 XML-tagged Markdown sections in
// this exact order — reader, tone, writing_standard, layout, visual_rules,
// limits, extra_rules, custom_instructions, example — wrapped in
// <plan_writing_guide>.
func Guide(s Style) string {
	sections := []string{
		wrapTag("reader", fmt.Sprintf(`audience="%s"`, s.Audience), packFor(audiencePacks, s.Audience, DefaultAudience)),
		wrapTag("tone", fmt.Sprintf(`value="%s"`, s.Tone), packFor(tonePacks, s.Tone, DefaultTone)),
		wrapTag("writing_standard", fmt.Sprintf(`value="%s"`, s.WritingStandard), packFor(standardPacks, s.WritingStandard, DefaultWritingStandard)),
		wrapTag("layout", "", corePack),
		wrapTag("visual_rules", fmt.Sprintf(`density="%s"`, s.VisualDensity), packFor(densityPacks, s.VisualDensity, DefaultVisualDensity)),
		wrapTag("limits", "", limitsLine(s.Limits)),
		wrapTag("extra_rules", "", extraRulesBody(s.NarrativeRules)),
		wrapTag("custom_instructions", "", InstructionsText(s.Instructions)),
		wrapTag("example", "", exampleBody(s.WritingStandard)),
	}
	return "<plan_writing_guide>\n" + strings.Join(sections, "\n") + "\n</plan_writing_guide>"
}

// ChatGuide builds the chat guide every sdlc skill other than plan
// follows for chat and questions: 5 XML-tagged Markdown sections in this
// exact order — scope, reader, tone, writing_standard, questions —
// wrapped in <sdlc_communication_style>. reader, tone, and
// writing_standard reuse the same pack text as Guide (one source);
// ChatGuide holds no plan limits, visual rules, example, or custom
// instructions.
func ChatGuide(s Style) string {
	sections := []string{
		wrapTag("scope", "", chatScope),
		wrapTag("reader", fmt.Sprintf(`audience="%s"`, s.Audience), packFor(audiencePacks, s.Audience, DefaultAudience)),
		wrapTag("tone", fmt.Sprintf(`value="%s"`, s.Tone), packFor(tonePacks, s.Tone, DefaultTone)),
		wrapTag("writing_standard", fmt.Sprintf(`value="%s"`, s.WritingStandard), packFor(standardPacks, s.WritingStandard, DefaultWritingStandard)),
		wrapTag("questions", "", chatQuestions),
	}
	return "<sdlc_communication_style>\n" + strings.Join(sections, "\n") + "\n</sdlc_communication_style>"
}

// wrapTag renders one guide section as "<name attr>\nbody\n</name>", or
// "<name>\nbody\n</name>" when attr is empty.
func wrapTag(name, attr, body string) string {
	if attr == "" {
		return fmt.Sprintf("<%s>\n%s\n</%s>", name, body, name)
	}
	return fmt.Sprintf("<%s %s>\n%s\n</%s>", name, attr, body, name)
}

// packFor looks up key in m, falling back to m[def] when key is absent
// (a Style built without FromSections, holding an unrecognized value).
func packFor(m map[string]string, key, def string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return m[def]
}

// limitsLine renders the <limits> tag body: every Limits value on one
// line, separated by " · ". MaxSentenceWords, MaxInstructionWords, and
// MaxJargonShare print "off" at their off sentinel (0, 0, and 1.0
// respectively) instead of a number.
func limitsLine(l Limits) string {
	parts := []string{
		fmt.Sprintf("prose share ≤ %.2f", l.MaxProseShare),
		fmt.Sprintf("paragraph ≤ %d sentences", l.MaxParagraphSentences),
		fmt.Sprintf("list ≤ %d items", l.MaxListItems),
	}
	if l.MaxSentenceWords > 0 {
		parts = append(parts, fmt.Sprintf("description sentence ≤ %d words", l.MaxSentenceWords))
	} else {
		parts = append(parts, "description sentence off")
	}
	if l.MaxInstructionWords > 0 {
		parts = append(parts, fmt.Sprintf("instruction sentence ≤ %d words", l.MaxInstructionWords))
	} else {
		parts = append(parts, "instruction sentence off")
	}
	if l.MaxJargonShare < 1.0 {
		parts = append(parts, fmt.Sprintf("jargon share ≤ %.2f", l.MaxJargonShare))
	} else {
		parts = append(parts, "jargon share off")
	}
	if l.STE {
		parts = append(parts, "STE checks on")
	} else {
		parts = append(parts, "STE checks off")
	}
	if len(l.BannedPhrases) == 0 {
		parts = append(parts, "banned phrases: none")
	} else {
		parts = append(parts, "banned phrases: "+strings.Join(l.BannedPhrases, ", "))
	}
	return strings.Join(parts, " · ")
}

// extraRulesBody renders the <extra_rules> tag body: each narrativeRules
// entry verbatim as a bullet, or "none" when the list is empty.
func extraRulesBody(rules []string) string {
	if len(rules) == 0 {
		return "none"
	}
	lines := make([]string, len(rules))
	for i, r := range rules {
		lines[i] = "- " + r
	}
	return strings.Join(lines, "\n")
}

// exampleBody renders the <example> tag body: the before/after pair for
// writingStandard, falling back to the default standard's pair.
func exampleBody(writingStandard string) string {
	pair, ok := standardExamples[writingStandard]
	if !ok {
		pair = standardExamples[DefaultWritingStandard]
	}
	return "Before: " + pair[0] + "\nAfter: " + pair[1]
}
