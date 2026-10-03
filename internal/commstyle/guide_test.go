package commstyle

import (
	"fmt"
	"strings"
	"testing"
)

// planTags are the 9 Guide tag names, in the required order.
var planTags = []string{"reader", "tone", "writing_standard", "layout", "visual_rules", "limits", "extra_rules", "custom_instructions", "example"}

// chatTags are the 5 ChatGuide tag names, in the required order.
var chatTags = []string{"scope", "reader", "tone", "writing_standard", "questions"}

// tagIndexes asserts each tag in order opens exactly once and closes
// exactly once in s, and returns each tag's opening index (for order
// checks).
func tagIndexes(t *testing.T, s string, tags []string) []int {
	t.Helper()
	indexes := make([]int, len(tags))
	for i, tag := range tags {
		openCount := strings.Count(s, "<"+tag+" ") + strings.Count(s, "<"+tag+">")
		if openCount != 1 {
			t.Errorf("tag %q opens %d times, want 1", tag, openCount)
		}
		closeCount := strings.Count(s, "</"+tag+">")
		if closeCount != 1 {
			t.Errorf("tag %q closes %d times, want 1", tag, closeCount)
		}
		idx := strings.Index(s, "<"+tag)
		if idx < 0 {
			t.Fatalf("tag %q not found", tag)
		}
		indexes[i] = idx
	}
	return indexes
}

func assertAscending(t *testing.T, tags []string, indexes []int) {
	t.Helper()
	for i := 1; i < len(indexes); i++ {
		if indexes[i] <= indexes[i-1] {
			t.Errorf("tag %q (index %d) does not come after %q (index %d)", tags[i], indexes[i], tags[i-1], indexes[i-1])
		}
	}
}

func TestGuide(t *testing.T) {
	t.Run("holds the 9 tags in order, each exactly once", func(t *testing.T) {
		s := FromSections(nil, nil)
		guide := Guide(s)
		if !strings.HasPrefix(guide, "<plan_writing_guide>\n") || !strings.HasSuffix(guide, "\n</plan_writing_guide>") {
			t.Errorf("Guide output is not wrapped in <plan_writing_guide>: %q", guide)
		}
		indexes := tagIndexes(t, guide, planTags)
		assertAscending(t, planTags, indexes)
	})

	t.Run("default settings keep the R62 rules", func(t *testing.T) {
		s := FromSections(nil, nil)
		guide := Guide(s)
		if !strings.Contains(guide, "One idea in each sentence.") {
			t.Error(`Guide output does not contain "One idea in each sentence."`)
		}
		if !strings.Contains(guide, "Explain each uncommon technical term the first time you use it.") {
			t.Error(`Guide output does not contain "Explain each uncommon technical term the first time you use it."`)
		}
	})

	t.Run("ste writing_standard pack is the golden 20-rule text", func(t *testing.T) {
		want := `ASD-STE100 Simplified Technical English. Follow every rule.
Words
1. Use simple, common words. Use one word for one meaning. Use the same word for the same thing every time. Do not use synonyms.
2. Use technical names (code, commands, paths, products) exactly. Use them only as nouns or adjectives.
3. Use noun clusters of 3 words or fewer. Split a longer cluster with "of", "for", or a relative clause.
4. Do not use the words in the avoid list. Use the replacement: utilize → use, leverage → use, ensure → make sure, facilitate → help, perform → do, commence → start, terminate → stop, obtain → get, sufficient → enough, numerous → many, prior to → before, subsequent → next, in order to → to, demonstrate → show, modify → change, eliminate → remove, initiate → start, regarding → about, approximately → about, additional → more.
Verbs
5. Use only these verb forms: the infinitive, the imperative, the simple present, the simple past, and the simple future.
6. Do not use perfect tenses ("has done", "have been", "had found"). Write "did" or "is".
7. Do not use "-ing" verb forms. Write "Before you start the tool", not "Before starting the tool".
8. Use the past participle only as an adjective ("the changed file") or in a description with "is" or "are".
9. Use the active voice. Use the passive voice only in a description, and only when the doer is not known.
10. Do not use phrasal verbs ("set up", "carry out", "find out", "look into", "point out"). Use one verb: "install", "do", "find", "examine", "show".
Sentences
11. Write one instruction in each sentence, unless two actions occur at the same time.
12. Write instructions in the imperative. Put a condition first: "If X, do Y."
13. Use 20 words or fewer in an instruction sentence. Use 25 words or fewer in a description sentence.
14. Keep the articles "the", "a", "an". Do not remove words to make a sentence shorter.
15. Use a pronoun only when it has one clear referent. Otherwise, repeat the noun.
16. Write a cause and its effect as two sentences: "X fails. The cause is Y."
Paragraphs and layout
17. Write one topic in each paragraph. Use 6 sentences or fewer in each paragraph. Start with the most important fact.
18. Use a vertical list for 3 or more items, steps, or conditions.
19. Start a warning with the command, then give the reason: "Do not run X. X deletes the table."
Punctuation and style
20. Do not use contractions, semicolons, idioms, slang, or figurative words. Explain each uncommon technical term the first time you use it.`
		if standardPacks["ste"] != want {
			t.Errorf("standardPacks[ste] does not match the golden text:\ngot:\n%s\nwant:\n%s", standardPacks["ste"], want)
		}

		guide := Guide(Style{WritingStandard: "ste", Audience: DefaultAudience, Tone: DefaultTone, VisualDensity: DefaultVisualDensity, Language: DefaultLanguage})
		for _, want := range []string{"20 words", "25 words", `Do not use "-ing" verb forms`} {
			if !strings.Contains(guide, want) {
				t.Errorf("ste guide missing %q", want)
			}
		}
		if n := strings.Count(guide, "\n1. "); n == 0 {
			t.Error("ste guide does not start a numbered rule list")
		}
		for i := 1; i <= 20; i++ {
			marker := fmt.Sprintf("%d. ", i)
			if !strings.Contains(guide, marker) {
				t.Errorf("ste guide missing rule marker %q", marker)
			}
		}
	})

	t.Run("steAvoidWords pairs are all present in rule 4", func(t *testing.T) {
		rule4 := standardPacks["ste"]
		for word, replacement := range steAvoidWords {
			pair := word + " → " + replacement
			if !strings.Contains(rule4, pair) {
				t.Errorf("rule 4 missing avoid-list pair %q", pair)
			}
		}
	})

	t.Run("custom_instructions holds InstructionsText", func(t *testing.T) {
		instructions := []string{"always add before -> after for the changed flows"}
		s := Style{Instructions: instructions, Audience: DefaultAudience, Tone: DefaultTone, WritingStandard: DefaultWritingStandard, VisualDensity: DefaultVisualDensity, Language: DefaultLanguage}
		guide := Guide(s)
		want := "<custom_instructions>\n" + InstructionsText(instructions) + "\n</custom_instructions>"
		if !strings.Contains(guide, want) {
			t.Errorf("Guide output missing %q", want)
		}

		none := Guide(Style{Audience: DefaultAudience, Tone: DefaultTone, WritingStandard: DefaultWritingStandard, VisualDensity: DefaultVisualDensity, Language: DefaultLanguage})
		if !strings.Contains(none, "<custom_instructions>\nnone configured\n</custom_instructions>") {
			t.Error("Guide output with no instructions does not contain the none-configured custom_instructions block")
		}
	})

	t.Run("every visual_rules pack holds the Mermaid contrast rule", func(t *testing.T) {
		for density, pack := range densityPacks {
			if !strings.Contains(pack, "`"+ClassDefNew+"`") {
				t.Errorf("densityPacks[%q] missing ClassDefNew", density)
			}
			if !strings.Contains(pack, "`"+ClassDefChanged+"`") {
				t.Errorf("densityPacks[%q] missing ClassDefChanged", density)
			}
			if !strings.Contains(pack, "Do not use pastel fills.") {
				t.Errorf("densityPacks[%q] missing the pastel-fill rule", density)
			}
		}
	})

	t.Run("extra_rules holds each narrativeRules entry verbatim, or none", func(t *testing.T) {
		base := Style{Audience: DefaultAudience, Tone: DefaultTone, WritingStandard: DefaultWritingStandard, VisualDensity: DefaultVisualDensity, Language: DefaultLanguage}

		withNone := Guide(base)
		if !strings.Contains(withNone, "<extra_rules>\nnone\n</extra_rules>") {
			t.Error("Guide output with no narrativeRules does not contain the none extra_rules block")
		}

		withRules := base
		withRules.NarrativeRules = []string{"Name the owner of each risk."}
		guide := Guide(withRules)
		if !strings.Contains(guide, "Name the owner of each risk.") {
			t.Error("Guide output does not contain the verbatim narrativeRules entry")
		}
	})

	t.Run("limits prints every Limits value; off limits print off", func(t *testing.T) {
		on := Style{Audience: DefaultAudience, Tone: "direct", WritingStandard: "ste", VisualDensity: "high", Language: "English"}
		on.Limits = LimitsFor(on)
		guide := Guide(on)
		for _, want := range []string{
			"prose share ≤ 0.30",
			"paragraph ≤ 3 sentences",
			"list ≤ 7 items",
			"description sentence ≤ 25 words",
			"instruction sentence ≤ 20 words",
			"jargon share ≤ 0.15",
			"STE checks on",
			"banned phrases: great question",
		} {
			if !strings.Contains(guide, want) {
				t.Errorf("limits (all on) missing %q in:\n%s", want, guide)
			}
		}

		off := Style{Audience: "technical", Tone: "neutral", WritingStandard: "plain-language", VisualDensity: "balanced", Language: "French"}
		off.Limits = LimitsFor(off)
		guideOff := Guide(off)
		for _, want := range []string{
			"description sentence off",
			"instruction sentence off",
			"jargon share off",
			"STE checks off",
			"banned phrases: none",
		} {
			if !strings.Contains(guideOff, want) {
				t.Errorf("limits (all off) missing %q in:\n%s", want, guideOff)
			}
		}
	})

	t.Run("every value in the 4 Task-1 lists has a pack entry", func(t *testing.T) {
		for _, a := range Audiences {
			if _, ok := audiencePacks[a]; !ok {
				t.Errorf("audiencePacks missing entry for audience %q", a)
			}
		}
		for _, w := range WritingStandards {
			if _, ok := standardPacks[w]; !ok {
				t.Errorf("standardPacks missing entry for writingStandard %q", w)
			}
			if _, ok := standardExamples[w]; !ok {
				t.Errorf("standardExamples missing entry for writingStandard %q", w)
			}
		}
		for _, tn := range Tones {
			if _, ok := tonePacks[tn]; !ok {
				t.Errorf("tonePacks missing entry for tone %q", tn)
			}
		}
		for _, d := range VisualDensities {
			if _, ok := densityPacks[d]; !ok {
				t.Errorf("densityPacks missing entry for visualDensity %q", d)
			}
		}
	})

	t.Run("no pack text contains a banned phrase", func(t *testing.T) {
		all := map[string]string{}
		for k, v := range standardPacks {
			all["standardPacks["+k+"]"] = v
		}
		for k, v := range audiencePacks {
			all["audiencePacks["+k+"]"] = v
		}
		for k, v := range tonePacks {
			all["tonePacks["+k+"]"] = v
		}
		for k, v := range densityPacks {
			all["densityPacks["+k+"]"] = v
		}
		all["corePack"] = corePack
		all["chatScope"] = chatScope
		all["chatQuestions"] = chatQuestions

		for name, text := range all {
			lower := strings.ToLower(text)
			for _, phrase := range directBannedPhrases {
				if strings.Contains(lower, phrase) {
					t.Errorf("%s contains banned phrase %q", name, phrase)
				}
			}
		}
	})
}

func TestChatGuide(t *testing.T) {
	t.Run("holds exactly the 5 tags in order, no plan-only tags", func(t *testing.T) {
		s := FromSections(nil, nil)
		guide := ChatGuide(s)
		if !strings.HasPrefix(guide, "<sdlc_communication_style>\n") || !strings.HasSuffix(guide, "\n</sdlc_communication_style>") {
			t.Errorf("ChatGuide output is not wrapped in <sdlc_communication_style>: %q", guide)
		}
		indexes := tagIndexes(t, guide, chatTags)
		assertAscending(t, chatTags, indexes)

		for _, forbidden := range []string{"<visual_rules", "<limits>", "<example>", "<custom_instructions>"} {
			if strings.Contains(guide, forbidden) {
				t.Errorf("ChatGuide output contains forbidden tag %q", forbidden)
			}
		}
	})

	t.Run("scope text is the verbatim D12 boundary", func(t *testing.T) {
		want := `Apply to every explanation, status line, summary, and AskUserQuestion text in every sdlc skill.
Do not apply to commit messages, PR bodies, review comments, or Jira text. They follow their own templates and config.`
		if chatScope != want {
			t.Errorf("chatScope = %q, want %q", chatScope, want)
		}
	})

	t.Run("reader, tone, and writing_standard reuse Guide's pack text", func(t *testing.T) {
		s := Style{Audience: "executive", Tone: "neutral", WritingStandard: "developer-docs", VisualDensity: DefaultVisualDensity, Language: DefaultLanguage}
		planGuide := Guide(s)
		chat := ChatGuide(s)

		for _, pack := range []string{
			audiencePacks["executive"],
			tonePacks["neutral"],
			standardPacks["developer-docs"],
		} {
			if !strings.Contains(planGuide, pack) {
				t.Errorf("Guide output missing expected pack text %q", pack)
			}
			if !strings.Contains(chat, pack) {
				t.Errorf("ChatGuide output missing expected pack text %q", pack)
			}
		}
	})

	t.Run("fits in 4096 bytes for the largest settings", func(t *testing.T) {
		s := Style{Audience: "beginner", Tone: "direct", WritingStandard: "ste", VisualDensity: DefaultVisualDensity, Language: DefaultLanguage}
		guide := ChatGuide(s)
		if n := len([]byte(guide)); n > 4096 {
			t.Errorf("ChatGuide(beginner, ste) is %d bytes, want <= 4096", n)
		}
	})
}
