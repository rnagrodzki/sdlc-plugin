package commstyle

import (
	"reflect"
	"strings"
	"testing"
)

// nWords returns n distinct-looking plain words joined by spaces.
func nWords(n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = "word"
	}
	return strings.Join(w, " ")
}

// offLimits turns every check off; tests switch on only the check they need.
func offLimits() Limits {
	return Limits{MaxJargonShare: 1, BannedPhrases: []string{}}
}

func onlySection(t *testing.T, rep Report) SectionMetrics {
	t.Helper()
	if len(rep.Sections) != 1 {
		t.Fatalf("Sections = %d entries, want 1: %+v", len(rep.Sections), rep.Sections)
	}
	return rep.Sections[0]
}

func TestMeasureWorkedExample(t *testing.T) {
	content := strings.Join([]string{
		"## Context",
		"The loader in `internal/tools/plan.go` accepts any audience value and the setup wizard offers a different list, so plans ignore the reader setting.",
		"This change adds one value list.",
		"| Setting | Today | After |",
		"|---|---|---|",
		"| `audience` | 3 lists | 1 list |",
		"- Reader: executive",
		"- Owner: plan skill team",
	}, "\n")
	l := LimitsFor(Style{
		VisualDensity:   "high",
		Audience:        "executive",
		WritingStandard: DefaultWritingStandard,
		Tone:            DefaultTone,
		Language:        DefaultLanguage,
	})

	sm := onlySection(t, Measure(content, []string{"Context"}, l))

	if sm.Words != 42 {
		t.Errorf("Words = %d, want 42", sm.Words)
	}
	if sm.ProseShare != 0.67 {
		t.Errorf("ProseShare = %v, want 0.67", sm.ProseShare)
	}
	if sm.JargonShare != 0.50 {
		t.Errorf("JargonShare = %v, want 0.50", sm.JargonShare)
	}
	if sm.LongestParagraph != 2 {
		t.Errorf("LongestParagraph = %d, want 2", sm.LongestParagraph)
	}
	if sm.LongestList != 2 {
		t.Errorf("LongestList = %d, want 2", sm.LongestList)
	}
	if sm.LongSentenceShare != 0 {
		t.Errorf("LongSentenceShare = %v, want 0", sm.LongSentenceShare)
	}
	if sm.Status != "fail" {
		t.Errorf("Status = %q, want fail", sm.Status)
	}
	want := []string{"prose share 0.67 > 0.30", "jargon share 0.50 > 0.05"}
	if !reflect.DeepEqual(sm.Failures, want) {
		t.Errorf("Failures = %q, want %q", sm.Failures, want)
	}
}

func TestMeasureLineWordCounts(t *testing.T) {
	cases := []struct {
		line  string
		class lineClass
		words int
	}{
		{"| Setting | Today | After |", classVisual, 3},
		{"|---|---|---|", classVisual, 0},
		{"| `audience` | 3 lists | 1 list |", classVisual, 5},
		{"- Reader: executive", classVisual, 2},
		{"1. Owner: plan skill team", classVisual, 4},
		{"- [ ] Run the tests", classVisual, 3},
		{"**Label:** some text here", classProse, 4},
		{"### Sub heading", classIgnored, 0},
		{"---", classIgnored, 0},
		{"", classIgnored, 0},
	}
	for _, c := range cases {
		got := classifyLines(c.line)[0]
		if got.class != c.class || got.words != c.words {
			t.Errorf("%q: class=%d words=%d, want class=%d words=%d", c.line, got.class, got.words, c.class, c.words)
		}
	}
}

func TestMeasureSkipsShortSection(t *testing.T) {
	content := "## Context\n" + nWords(39) + "."
	l := offLimits()
	l.MaxProseShare = 0.30
	sm := onlySection(t, Measure(content, []string{"Context"}, l))
	if sm.Status != "skipped" {
		t.Errorf("Status = %q, want skipped", sm.Status)
	}
	if sm.Failures == nil || len(sm.Failures) != 0 {
		t.Errorf("Failures = %#v, want empty non-nil", sm.Failures)
	}
	if sm.Words != 39 || sm.ProseShare != 1 {
		t.Errorf("Words=%d ProseShare=%v, want 39 and 1 (numbers still filled)", sm.Words, sm.ProseShare)
	}

	content = "## Context\n" + nWords(40) + "."
	sm = onlySection(t, Measure(content, []string{"Context"}, l))
	if sm.Status != "fail" {
		t.Errorf("40 words: Status = %q, want fail", sm.Status)
	}
}

func TestMeasureMissingSectionAndEmptyContent(t *testing.T) {
	rep := Measure("", nil, offLimits())
	if rep.Sections == nil || rep.BannedHits == nil || rep.SteHits == nil {
		t.Fatalf("nil slice in Report: %#v", rep)
	}
	if len(rep.Sections)+len(rep.BannedHits)+len(rep.SteHits) != 0 {
		t.Errorf("Report = %#v, want all empty", rep)
	}

	sm := onlySection(t, Measure("## Other\ntext", []string{"Context"}, offLimits()))
	if sm.Name != "Context" || sm.Status != "skipped" || sm.Words != 0 || sm.Failures == nil {
		t.Errorf("missing section = %#v, want skipped with 0 words and empty failures", sm)
	}

	sm = onlySection(t, Measure("text\n## Context", []string{"Context"}, offLimits()))
	if sm.Status != "skipped" || sm.Words != 0 {
		t.Errorf("empty trailing section = %#v, want skipped with 0 words", sm)
	}
}

func TestMeasureSectionBoundaries(t *testing.T) {
	content := strings.Join([]string{
		"# Plan",
		"intro words here",
		"## Context",
		"one two three.",
		"### Sub",
		"four five.",
		"```",
		"## not a heading inside a fence",
		"```",
		"## Next",
		"six seven eight nine.",
	}, "\n")
	rep := Measure(content, []string{"context", "Next"}, offLimits())
	if got := rep.Sections[0].Words; got != 11 {
		t.Errorf("Context words = %d, want 11 (### and fenced ## do not end it)", got)
	}
	if got := rep.Sections[1].Words; got != 4 {
		t.Errorf("Next words = %d, want 4", got)
	}
}

func TestMeasureFencesAreVisual(t *testing.T) {
	content := strings.Join([]string{
		"## Context",
		"```mermaid",
		"flowchart LR",
		"  A[Start here now] --> B[End there later]",
		"```",
		"  ```go",
		"  func Measure(content string) Report { return Report{} }",
		"  ```",
		"Short prose.",
	}, "\n")
	lines := classifyLines(content)
	for _, i := range []int{2, 3, 6} {
		if lines[i].class != classVisual || !lines[i].inFence {
			t.Errorf("line %d: class=%d inFence=%v, want visual fence body", i+1, lines[i].class, lines[i].inFence)
		}
	}
	for _, i := range []int{1, 4, 5, 7} {
		if lines[i].class != classIgnored {
			t.Errorf("line %d (fence delimiter): class=%d, want ignored", i+1, lines[i].class)
		}
	}
	sm := onlySection(t, Measure(content, []string{"Context"}, offLimits()))
	if sm.ProseShare != share(2, sm.Words) {
		t.Errorf("ProseShare = %v, want only the 2 prose words counted as prose (words=%d)", sm.ProseShare, sm.Words)
	}
}

func TestMeasureUnclosedFenceIsOrdinaryText(t *testing.T) {
	lines := classifyLines("```go\nplain prose line")
	if lines[0].inFence || lines[1].inFence || lines[1].class != classProse {
		t.Errorf("unclosed fence: %+v, want ordinary lines", lines)
	}
}

func TestMeasureInlineCodeNotSplit(t *testing.T) {
	got := splitSentences(replaceCodeSpans("Call `foo. bar! baz?` now. Then stop."))
	want := []string{"Call CODE now.", "Then stop."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sentences = %q, want %q", got, want)
	}
	got = splitSentences(replaceCodeSpans("Use ``a ` b`` here, e.g. in plan.go files. Done"))
	want = []string{"Use CODE here, e.g. in plan.go files.", "Done"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sentences = %q, want %q", got, want)
	}
	if got := replaceCodeSpans("an `unclosed span"); got != "an `unclosed span" {
		t.Errorf("unclosed span = %q, want unchanged", got)
	}
}

func TestMeasureListItemProseThreshold(t *testing.T) {
	lines := classifyLines("- " + nWords(30) + "\n- " + nWords(31))
	if lines[0].class != classVisual {
		t.Errorf("30-word item: class=%d, want visual", lines[0].class)
	}
	if lines[1].class != classProse {
		t.Errorf("31-word item: class=%d, want prose", lines[1].class)
	}
}

func TestMeasureProseLines(t *testing.T) {
	content := strings.Join([]string{
		"First line with `code`.",
		"Second line same paragraph.",
		"",
		"1. " + nWords(31) + ".",
		"- [ ] " + nWords(31) + ".",
		"- " + nWords(31) + ".",
		"Lazy continuation line.",
		"| table | row |",
		"After table.",
	}, "\n")
	got := collectProseLines(classifyLines(content))
	type row struct {
		line, para         int
		listItem, numbered bool
	}
	want := []row{
		{1, 0, false, false},
		{2, 0, false, false},
		{4, 1, true, true},
		{5, 2, true, true},
		{6, 3, true, false},
		{7, 3, false, false},
		{9, 4, false, false},
	}
	if len(got) != len(want) {
		t.Fatalf("prose lines = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Line != w.line || g.Paragraph != w.para || g.ListItem != w.listItem || g.Numbered != w.numbered {
			t.Errorf("prose line %d = %+v, want %+v", i, g, w)
		}
	}
	if got[0].Text != "First line with CODE." {
		t.Errorf("Text = %q, want inline code replaced", got[0].Text)
	}
	if strings.HasPrefix(got[2].Text, "1.") || strings.HasPrefix(got[3].Text, "[ ]") {
		t.Errorf("list marker kept in Text: %q / %q", got[2].Text, got[3].Text)
	}
}

func TestMeasureParagraphAndList(t *testing.T) {
	content := strings.Join([]string{
		"## Context",
		"One. Two. Three. Four.",
		"Five.",
		"",
		"Six.",
		"- a",
		"- b",
		"  - c",
		"  - d",
		"  - e",
		"  - f",
		"- g",
		"",
		"- h",
		"Text ends the list.",
		"- i",
		"- " + nWords(31) + ". " + nWords(5) + ".",
		"- " + nWords(31) + ".",
	}, "\n")
	l := offLimits()
	l.MaxParagraphSentences = 4
	l.MaxListItems = 3
	sm := onlySection(t, Measure(content, []string{"Context"}, l))
	if sm.LongestParagraph != 5 {
		t.Errorf("LongestParagraph = %d, want 5 (long list items are separate paragraphs)", sm.LongestParagraph)
	}
	if sm.LongestList != 4 {
		t.Errorf("LongestList = %d, want 4 (a, b, g, h; nested c-f is 4 too)", sm.LongestList)
	}
	want := []string{"longest paragraph 5 sentences > 4", "longest list 4 items > 3"}
	if !reflect.DeepEqual(sm.Failures, want) {
		t.Errorf("Failures = %q, want %q", sm.Failures, want)
	}
}

func TestMeasureSentenceTolerance(t *testing.T) {
	build := func(long int) string {
		var b strings.Builder
		b.WriteString("## Context\n")
		for i := 0; i < 10; i++ {
			if i < long {
				b.WriteString(nWords(26) + ".\n\n")
			} else {
				b.WriteString(nWords(5) + ".\n\n")
			}
		}
		return b.String()
	}
	l := offLimits()
	l.MaxSentenceWords = 25

	sm := onlySection(t, Measure(build(1), []string{"Context"}, l))
	if sm.LongSentenceShare != 0.10 || sm.Status != "pass" {
		t.Errorf("1 of 10 long: share=%v status=%q, want 0.10 pass", sm.LongSentenceShare, sm.Status)
	}

	sm = onlySection(t, Measure(build(2), []string{"Context"}, l))
	want := []string{"long sentence share 0.20 > 0.10 (sentences over 25 words)"}
	if sm.LongSentenceShare != 0.20 || !reflect.DeepEqual(sm.Failures, want) {
		t.Errorf("2 of 10 long: share=%v failures=%q, want 0.20 %q", sm.LongSentenceShare, sm.Failures, want)
	}

	l.MaxSentenceWords = 0
	sm = onlySection(t, Measure(build(10), []string{"Context"}, l))
	if sm.Status != "pass" || sm.LongSentenceShare != 0 {
		t.Errorf("check off: share=%v status=%q, want 0 pass", sm.LongSentenceShare, sm.Status)
	}
}

func TestMeasureJargon(t *testing.T) {
	content := "## Context\n" + nWords(20) + " in `x`.\n" + nWords(20) + ".\n" + nWords(20) + " see docs/a/b.md now."
	l := offLimits()
	l.MaxJargonShare = 0.00
	sm := onlySection(t, Measure(content, []string{"Context"}, l))
	want := []string{"jargon share 0.67 > 0.00"}
	if sm.JargonShare != 0.67 || !reflect.DeepEqual(sm.Failures, want) {
		t.Errorf("JargonShare=%v Failures=%q, want 0.67 %q", sm.JargonShare, sm.Failures, want)
	}

	l.MaxJargonShare = 1.00
	sm = onlySection(t, Measure(content, []string{"Context"}, l))
	if sm.Status != "pass" {
		t.Errorf("jargon check off: Status=%q, want pass", sm.Status)
	}
}

func TestMeasureBannedPhrases(t *testing.T) {
	content := strings.Join([]string{
		"# Great Question about plans",
		"```",
		"great question inside a fence",
		"```",
		"Use `great question` in code only.",
		"| Feel   free to | ask |",
		"Greater question marks are fine.",
		"I hope this helps and great question.",
	}, "\n")
	l := offLimits()
	l.BannedPhrases = []string{"great question", "feel free to", "i hope this helps"}
	got := Measure(content, nil, l).BannedHits
	want := []BannedHit{
		{Phrase: "great question", Line: 1},
		{Phrase: "feel free to", Line: 6},
		{Phrase: "great question", Line: 8},
		{Phrase: "i hope this helps", Line: 8},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BannedHits = %+v, want %+v", got, want)
	}

	l.BannedPhrases = []string{}
	if got := Measure(content, nil, l).BannedHits; got == nil || len(got) != 0 {
		t.Errorf("no phrases: BannedHits = %#v, want empty non-nil", got)
	}
}
