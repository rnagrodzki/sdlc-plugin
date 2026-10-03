package commstyle

import (
	"fmt"
	"strings"
	"testing"
)

// steLimits turns on only the strict STE checks, with the STE instruction
// limit and the given technical terms.
func steLimits(terms ...string) Limits {
	l := offLimits()
	l.STE = true
	l.MaxInstructionWords = 20
	l.TechnicalTerms = append([]string{}, terms...)
	return l
}

func steHitsFor(content string, l Limits) []SteHit {
	return Measure(content, nil, l).SteHits
}

func hitsOfRule(hits []SteHit, rule string) []SteHit {
	var out []SteHit
	for _, h := range hits {
		if h.Rule == rule {
			out = append(out, h)
		}
	}
	return out
}

// words builds a sentence of n words that starts with first and then
// repeats the neutral word "word".
func words(first string, n int) string {
	return first + " " + nWords(n-1) + "."
}

func TestSteRules(t *testing.T) {
	cases := []struct {
		name    string
		rule    string
		content string
		want    string // matched text of the expected hit; "" = the rule must not hit
		anyText bool   // true: a hit is expected, its text is not checked
	}{
		{"contraction n't fails", "contraction", "The tool doesn't stop.", "doesn't", false},
		{"contraction it's fails", "contraction", "It's a slow tool.", "It's", false},
		{"contraction curly apostrophe fails", "contraction", "We don’t stop.", "don't", false},
		{"contraction pass", "contraction", "The tool does not stop.", "", false},
		{"possessive pass", "contraction", "The task's file is here.", "", false},

		{"semicolon fails", "semicolon", "X fails; Y stops.", "fails; Y", false},
		{"semicolon pass", "semicolon", "X fails. Y stops.", "", false},

		{"ing after preposition fails", "ing-form", "Read the guide before starting the tool.", "starting", false},
		{"ing after be fails", "ing-form", "The tool is running now.", "running", false},
		{"ing after preposition before noun fails", "ing-form", "The step runs when using caches.", "using", false},
		{"ing pass", "ing-form", "Read the guide before you start the tool.", "", false},
		{"ing noun after article pass", "ing-form", "The warning names each setting.", "", false},
		{"ing adjective before noun pass", "ing-form", "The list of existing files is short.", "", false},
		{"ing non-verb pass", "ing-form", "The value is nothing.", "", false},

		{"perfect has been fails", "perfect-tense", "The tool has been slow.", "has been", false},
		{"perfect has changed fails", "perfect-tense", "The team has changed the file.", "has changed", false},
		{"perfect has not run fails", "perfect-tense", "The job has not run.", "has not run", false},
		{"perfect pass", "perfect-tense", "The tool is slow.", "", false},
		{"perfect -en non-participle pass", "perfect-tense", "The pull request has open issues.", "", false},
		{"perfect adjective pass", "perfect-tense", "The tool has limited support.", "", false},

		{"phrasal base fails", "phrasal-verb", "Set up the tool.", "Set up", false},
		{"phrasal past fails", "phrasal-verb", "We carried out the test.", "carried out", false},
		{"phrasal three words fails", "phrasal-verb", "They came up with a name.", "came up with", false},
		{"phrasal pass", "phrasal-verb", "Install the tool.", "", false},

		{"avoid word fails", "avoid-word", "Utilize the cache.", "Utilize", false},
		{"avoid inflected fails", "avoid-word", "The step modified the file.", "modified", false},
		{"avoid phrase fails", "avoid-word", "Read it in order to save time.", "in order to", false},
		{"avoid pass", "avoid-word", "Use the cache.", "", false},

		{"instruction 21 words fails", "instruction-length", words("Delete", 21), "", true},
		{"instruction 20 words pass", "instruction-length", words("Delete", 20), "", false},
		{"condition-first instruction 21 words fails", "instruction-length", "If the run fails, delete " + nWords(16) + ".", "", true},
		// "use" and "run" are also common nouns: they start an instruction
		// only when an object starter or CODE follows them.
		{"noun-like verb before article 21 words fails", "instruction-length", "Use the " + nWords(19) + ".", "", true},
		{"noun-like verb before CODE 21 words fails", "instruction-length", "Run `go test` " + nWords(19) + ".", "", true},
		{"noun-like verb before plain word 21 words pass", "instruction-length", words("Use", 21), "", false},
		{"verb followed by is 21 words pass", "instruction-length", "Delete is " + nWords(19) + ".", "", false},

		{"description 26 words fails", "description-length", words("The", 26), "", true},
		{"description 25 words pass", "description-length", words("The", 25), "", false},

		{"paragraph 7 sentences fails", "paragraph-length", strings.TrimSpace(strings.Repeat("The tool works. ", 7)), "7 sentences", false},
		{"paragraph 6 sentences pass", "paragraph-length", strings.TrimSpace(strings.Repeat("The tool works. ", 6)), "", false},

		{"passive modal fails", "passive-instruction", "The file must be deleted.", "must be deleted", false},
		{"passive modal not fails", "passive-instruction", "The file should not be changed.", "should not be changed", false},
		{"passive imperative clause fails", "passive-instruction", "Make sure the file is saved.", "is saved", false},
		{"passive pass", "passive-instruction", "Delete the file.", "", false},
		{"passive description pass", "passive-instruction", "The file is deleted by the tool.", "", false},
		{"passive in subordinate clause pass", "passive-instruction", "Delete the cache when a restart is required.", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hitsOfRule(steHitsFor(tc.content, steLimits()), tc.rule)
			switch {
			case tc.anyText:
				if len(got) != 1 {
					t.Fatalf("%s hits = %+v, want 1", tc.rule, got)
				}
			case tc.want == "":
				if len(got) != 0 {
					t.Fatalf("%s hits = %+v, want none", tc.rule, got)
				}
			default:
				if len(got) != 1 || got[0].Text != tc.want || got[0].Line != 1 {
					t.Fatalf("%s hits = %+v, want one hit %q on line 1", tc.rule, got, tc.want)
				}
			}
		})
	}
}

func TestSteLengthHitText(t *testing.T) {
	hits := hitsOfRule(steHitsFor(words("Delete", 21), steLimits()), "instruction-length")
	want := "Delete word word word word word ... (21 words)"
	if len(hits) != 1 || hits[0].Text != want {
		t.Fatalf("hits = %+v, want text %q", hits, want)
	}
}

func TestSteNumberedItemIsInstruction(t *testing.T) {
	// A list item is prose only above 30 words, so the item holds a
	// second sentence. The first sentence of a numbered or "- [ ]" item
	// is an instruction even when it does not start with a verb.
	second := " " + words("The", 11)
	for _, marker := range []string{"1. ", "- [ ] "} {
		fail := marker + words("The", 21) + second
		if got := hitsOfRule(steHitsFor(fail, steLimits()), "instruction-length"); len(got) != 1 {
			t.Errorf("%q: instruction-length hits = %+v, want 1", marker, got)
		}
		pass := marker + words("The", 20) + second
		if got := hitsOfRule(steHitsFor(pass, steLimits()), "instruction-length"); len(got) != 0 {
			t.Errorf("%q: instruction-length hits = %+v, want none", marker, got)
		}
	}
	// The same 21-word sentence in a plain paragraph is a description.
	if got := steHitsFor(words("The", 21)+second, steLimits()); len(got) != 0 {
		t.Errorf("plain paragraph hits = %+v, want none", got)
	}
}

// TestSteShortListItemIsScanned verifies that a short "- [ ] " or "N. "
// item (an acceptance criterion, a numbered step) is scanned for STE rule
// breaks even though it is far under the 30-word prose floor that the
// readability density check uses to tell a visual list item from prose.
func TestSteShortListItemIsScanned(t *testing.T) {
	for _, marker := range []string{"- [ ] ", "1. "} {
		content := marker + "Before starting the tool, read the guide."
		if got := hitsOfRule(steHitsFor(content, steLimits()), "ing-form"); len(got) != 1 || got[0].Text != "starting" {
			t.Errorf("%q: ing-form hits = %+v, want one hit on %q", marker, got, "starting")
		}
	}
	// A short item with no rule break still contributes no hits.
	if got := steHitsFor("- [ ] Install the tool.", steLimits()); len(got) != 0 {
		t.Errorf("clean short item: hits = %+v, want none", got)
	}
}

func TestSteFalsePositiveGuards(t *testing.T) {
	for _, s := range []string{
		"the warning says",
		"each setting has a default",
		"the finding is valid",
		"the task's file",
		"use the mapping table",
	} {
		if got := steHitsFor(s, steLimits()); len(got) != 0 {
			t.Errorf("%q: hits = %+v, want none", s, got)
		}
	}
}

func TestSteExemptions(t *testing.T) {
	bad := "We don't stop; utilize it before starting it."
	if got := steHitsFor(bad, steLimits()); len(got) == 0 {
		t.Fatalf("control line %q: no hits, want some", bad)
	}
	cases := map[string]string{
		"inline code":  "Read `" + bad + "` now.",
		"fenced block": "```\n" + bad + "\n```",
		"table row":    "| " + bad + " | x |",
	}
	for name, content := range cases {
		if got := steHitsFor(content, steLimits()); len(got) != 0 {
			t.Errorf("%s: hits = %+v, want none", name, got)
		}
	}
}

func TestSteTechnicalTerms(t *testing.T) {
	line := "Read the log before logging the data."
	if got := hitsOfRule(steHitsFor(line, steLimits()), "ing-form"); len(got) != 1 {
		t.Fatalf("without term: ing-form hits = %+v, want 1", got)
	}
	if got := steHitsFor(line, steLimits("logging")); len(got) != 0 {
		t.Errorf("term logging: hits = %+v, want none", got)
	}
	if got := steHitsFor("Read the log before Logging the data.", steLimits("logging")); len(got) != 0 {
		t.Errorf("term is case-insensitive: hits = %+v, want none", got)
	}
	// Whole word only: the term "log" does not exempt "logging".
	if got := hitsOfRule(steHitsFor(line, steLimits("log")), "ing-form"); len(got) != 1 {
		t.Errorf("term log: ing-form hits = %+v, want 1", got)
	}
	// A multi-word term.
	if got := steHitsFor("Use carry out to run the job.", steLimits("carry out")); len(got) != 0 {
		t.Errorf("term carry out: hits = %+v, want none", got)
	}
}

func TestSteOff(t *testing.T) {
	content := "We don't stop; utilize it before starting it."
	rep := Measure(content, nil, offLimits())
	if rep.SteHits == nil || len(rep.SteHits) != 0 {
		t.Errorf("STE off: SteHits = %#v, want empty non-nil", rep.SteHits)
	}

	base := Style{Audience: DefaultAudience, Tone: DefaultTone, VisualDensity: DefaultVisualDensity, Language: DefaultLanguage}
	plain := base
	plain.WritingStandard = "plain-language"
	if got := Measure(content, nil, LimitsFor(plain)).SteHits; len(got) != 0 {
		t.Errorf("plain-language: SteHits = %+v, want none", got)
	}
	ste := base
	ste.WritingStandard = "ste"
	if got := Measure(content, nil, LimitsFor(ste)).SteHits; len(got) == 0 {
		t.Errorf("ste: SteHits empty, want hits")
	}
}

func TestSteParagraphOneHit(t *testing.T) {
	content := strings.Join([]string{
		"The tool works. The tool works. The tool works. The tool works.",
		"The tool works. The tool works. The tool works.",
		"",
		"The tool works.",
	}, "\n")
	got := hitsOfRule(steHitsFor(content, steLimits()), "paragraph-length")
	if len(got) != 1 || got[0].Line != 1 || got[0].Text != "7 sentences" {
		t.Fatalf("paragraph-length hits = %+v, want one on line 1 with text %q", got, "7 sentences")
	}
}

func TestSteSortAndDedupe(t *testing.T) {
	content := strings.Join([]string{
		"We don't stop; utilize it. We don't stop.",
		"",
		"It's slow.",
	}, "\n")
	got := steHitsFor(content, steLimits())
	want := []SteHit{
		{Rule: "avoid-word", Text: "utilize", Line: 1},
		{Rule: "contraction", Text: "don't", Line: 1},
		{Rule: "semicolon", Text: "stop; utilize", Line: 1},
		{Rule: "contraction", Text: "It's", Line: 3},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("hits = %+v\nwant %+v", got, want)
	}
}

func TestSteTruncation(t *testing.T) {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = "We don't stop."
	}
	got := steHitsFor(strings.Join(lines, "\n\n"), steLimits())
	if len(got) != steMaxHits+1 {
		t.Fatalf("len(hits) = %d, want %d", len(got), steMaxHits+1)
	}
	for i := 1; i < steMaxHits; i++ {
		if got[i].Line < got[i-1].Line {
			t.Fatalf("hits not sorted by line at %d: %+v", i, got[i-1:i+1])
		}
	}
	last := got[steMaxHits]
	// Each "We don't stop." sits on an odd line (1, 3, 5, ...), so the
	// first left-out hit is two lines after the last kept hit.
	if last.Rule != "truncated" || last.Text != "10 more" || last.Line != got[steMaxHits-1].Line+2 {
		t.Errorf("last = %+v, want {truncated, \"10 more\", line of first left-out hit}", last)
	}
}
