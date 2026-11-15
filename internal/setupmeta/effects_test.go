package setupmeta

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/commstyle"
)

// isChoiceField reports whether EffectsFor describes the field.
func isChoiceField(f Field) bool {
	switch f.Type {
	case "enum", "multi-enum", "flag-set", "boolean":
		return true
	}
	return false
}

// findField returns the field of a section, or fails the test.
func findField(t *testing.T, sectionID, name string) Field {
	t.Helper()
	for _, s := range Sections() {
		if s.ID != sectionID {
			continue
		}
		for _, f := range s.Fields {
			if f.Name == name {
				return f
			}
		}
	}
	t.Fatalf("field %s/%s not found", sectionID, name)
	return Field{}
}

// TestEffects_CoverEveryOption fails when a choice field misses an effect
// (or has a blank one), when a non-choice field gets effects, or when
// fieldEffects holds a key for a field or an option that does not exist.
func TestEffects_CoverEveryOption(t *testing.T) {
	known := map[string]Field{}
	choiceCount := 0
	for _, s := range Sections() {
		for _, f := range s.Fields {
			key := s.ID + "/" + f.Name
			known[key] = f
			got := EffectsFor(s.ID, f)
			if !isChoiceField(f) {
				if got != nil {
					t.Errorf("EffectsFor(%s) = %v, want nil for type %q", key, got, f.Type)
				}
				continue
			}
			choiceCount++
			if len(got) != len(f.Options) {
				t.Errorf("EffectsFor(%s) has %d effects, want %d (one for each option)", key, len(got), len(f.Options))
				continue
			}
			for i, e := range got {
				if e.Option != f.Options[i] {
					t.Errorf("EffectsFor(%s)[%d].Option = %q, want %q (Options order)", key, i, e.Option, f.Options[i])
				}
				if strings.TrimSpace(e.Text) == "" {
					t.Errorf("EffectsFor(%s) option %q has no text", key, e.Option)
				}
			}
		}
	}
	if choiceCount == 0 {
		t.Error("found no choice fields in Sections()")
	}

	for key, options := range fieldEffects {
		f, ok := known[key]
		if !ok {
			t.Errorf("fieldEffects key %q is no field of the field list", key)
			continue
		}
		if !isChoiceField(f) {
			t.Errorf("fieldEffects key %q is a field of type %q, want a choice field", key, f.Type)
		}
		for opt := range options {
			if !slices.Contains(f.Options, opt) {
				t.Errorf("fieldEffects[%q] has option %q that is not in Options %v", key, opt, f.Options)
			}
		}
	}
}

// TestEffects_WritingStandardSamples checks each option of
// communication-style/writingStandard holds the sample of
// commstyle.StandardExample, and no other field holds a sample.
func TestEffects_WritingStandardSamples(t *testing.T) {
	f := findField(t, "communication-style", "writingStandard")
	for _, e := range EffectsFor("communication-style", f) {
		pair, ok := commstyle.StandardExample(e.Option)
		before, after := pair.Before, pair.After
		if !ok {
			t.Fatalf("StandardExample(%q) ok = false", e.Option)
		}
		if e.Before != before || e.After != after {
			t.Errorf("writingStandard %q sample = (%q, %q), want (%q, %q)", e.Option, e.Before, e.After, before, after)
		}
	}

	for _, s := range Sections() {
		for _, fld := range s.Fields {
			if s.ID == "communication-style" && fld.Name == "writingStandard" {
				continue
			}
			for _, e := range EffectsFor(s.ID, fld) {
				if e.Before != "" || e.After != "" {
					t.Errorf("%s/%s option %q has a sample, want none", s.ID, fld.Name, e.Option)
				}
			}
		}
	}
}

// effectText returns the text of one option of a choice field.
func effectText(t *testing.T, sectionID, name, option string) string {
	t.Helper()
	f := findField(t, sectionID, name)
	for _, e := range EffectsFor(sectionID, f) {
		if e.Option == option {
			return e.Text
		}
	}
	t.Fatalf("option %q not found in %s/%s", option, sectionID, name)
	return ""
}

func wantContains(t *testing.T, label, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Errorf("%s: text %q does not hold %q", label, text, want)
	}
}

// unownedEffectNumbers lists, by "<section>/<field>/<option>", each number of
// a limit text that no Go constant owns. TestEffects_LimitNumbers accepts
// these numbers as written. Add an entry only when no code holds the number.
var unownedEffectNumbers = map[string][]int{
	// "20 rules": ASD-STE100 has 20 writing rules, but no Go constant counts
	// them; commstyle checks only a subset (ste.go).
	"communication-style/writingStandard/ste": {20},
}

// textNumberRe finds each whole number of a text. A number that is part of a
// word, such as 100 in ASD-STE100, does not match.
var textNumberRe = regexp.MustCompile(`\b\d+\b`)

// textNumbers returns the whole numbers of text, sorted.
func textNumbers(t *testing.T, text string) []int {
	t.Helper()
	var out []int
	for _, m := range textNumberRe.FindAllString(text, -1) {
		n, err := strconv.Atoi(m)
		if err != nil {
			t.Fatalf("number %q of %q: %v", m, text, err)
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// percent returns share as a whole percent, rounded.
func percent(share float64) int {
	return int(share*100 + 0.5)
}

// TestEffects_LimitNumbers builds each limit number from
// commstyle.LimitsFor and fails when the effect text does not hold it. It
// also extracts every number of each limit text and fails when the text holds
// a number that LimitsFor does not give and unownedEffectNumbers does not
// list, so a text cannot state a limit that no code owns.
func TestEffects_LimitNumbers(t *testing.T) {
	// want maps "<section>/<field>/<option>" to the numbers its text must
	// hold, from LimitsFor.
	want := map[string][]int{}

	for _, density := range commstyle.VisualDensities {
		l := commstyle.LimitsFor(commstyle.Style{VisualDensity: density})
		text := effectText(t, "plan-style", "visualDensity", density)
		label := "visualDensity " + density
		wantContains(t, label, text, fmt.Sprintf("%d%%", percent(l.MaxProseShare)))
		wantContains(t, label, text, fmt.Sprintf("%d sentences", l.MaxParagraphSentences))
		wantContains(t, label, text, fmt.Sprintf("%d items", l.MaxListItems))
		want["plan-style/visualDensity/"+density] = []int{percent(l.MaxProseShare), l.MaxParagraphSentences, l.MaxListItems}
	}

	for _, audience := range commstyle.Audiences {
		l := commstyle.LimitsFor(commstyle.Style{Audience: audience})
		text := effectText(t, "communication-style", "audience", audience)
		label := "audience " + audience
		key := "communication-style/audience/" + audience
		switch {
		case l.MaxJargonShare >= 1:
			wantContains(t, label, text, "No jargon limit")
			want[key] = nil
		case l.MaxJargonShare <= 0:
			wantContains(t, label, text, "No sentence can hold jargon")
			want[key] = nil
		default:
			wantContains(t, label, text, fmt.Sprintf("%d%% of the sentences", percent(l.MaxJargonShare)))
			want[key] = []int{percent(l.MaxJargonShare)}
		}
	}

	for _, standard := range commstyle.WritingStandards {
		l := commstyle.LimitsFor(commstyle.Style{WritingStandard: standard, Language: "English"})
		text := effectText(t, "communication-style", "writingStandard", standard)
		label := "writingStandard " + standard
		key := "communication-style/writingStandard/" + standard
		wantContains(t, label, text, fmt.Sprintf("%d words at most", l.MaxSentenceWords))
		want[key] = []int{l.MaxSentenceWords}
		if l.MaxInstructionWords > 0 {
			wantContains(t, label, text, fmt.Sprintf("an instruction %d", l.MaxInstructionWords))
			want[key] = append(want[key], l.MaxInstructionWords)
		}
	}

	for _, tone := range commstyle.Tones {
		l := commstyle.LimitsFor(commstyle.Style{Tone: tone})
		key := "communication-style/tone/" + tone
		want[key] = nil
		if len(l.BannedPhrases) > 0 {
			wantContains(t, "tone "+tone, effectText(t, "communication-style", "tone", tone), fmt.Sprintf("%d filler phrases", len(l.BannedPhrases)))
			want[key] = []int{len(l.BannedPhrases)}
		}
	}

	for key, numbers := range want {
		parts := strings.SplitN(key, "/", 3)
		got := textNumbers(t, effectText(t, parts[0], parts[1], parts[2]))
		expected := append(slices.Clone(numbers), unownedEffectNumbers[key]...)
		slices.Sort(expected)
		if !slices.Equal(got, expected) {
			t.Errorf("%s: text numbers = %v, want %v (from commstyle.LimitsFor plus unownedEffectNumbers)", key, got, expected)
		}
	}
	for key := range unownedEffectNumbers {
		if _, ok := want[key]; !ok {
			t.Errorf("unownedEffectNumbers key %q is no limit text of this test", key)
		}
	}
}

// TestEffects_ContractShapeKeys checks the contractShape texts name the
// five keys of the Contract block, and the review-dimensions section
// names .md files.
func TestEffects_ContractShapeKeys(t *testing.T) {
	keys := []string{"shape", "names", "mirror", "decisions", "sync"}
	f := findField(t, "plan-tasks", "contractShape")

	full := effectText(t, "plan-tasks", "contractShape", "full")
	for _, k := range keys {
		wantContains(t, "contractShape full effect", full, k)
		wantContains(t, "contractShape description", f.Description, k)
		wantContains(t, "contractShape details", f.Details, k)
		wantContains(t, "contractShape examples", strings.Join(f.Examples, " "), k)
	}
	for _, old := range []string{"Complexity", "Risk", "Depends on"} {
		for label, text := range map[string]string{"description": f.Description, "details": f.Details, "examples": strings.Join(f.Examples, " ")} {
			if strings.Contains(text, old) {
				t.Errorf("contractShape %s still names %q: %s", label, old, text)
			}
		}
	}

	exact := map[string]string{
		"minimal": "Each task that changes a file must have a Contract block. /plan does not check its keys.",
		"none":    "/plan does not check the keys of the Contract block. A task that changes a file still needs the block.",
	}
	for option, want := range exact {
		if got := effectText(t, "plan-tasks", "contractShape", option); got != want {
			t.Errorf("contractShape %s effect = %q, want %q", option, got, want)
		}
	}
}

// TestEffects_PushAutoApproveSharedText pins the one text that both options
// of automation/push.featureBranchAutoApprove show: no code reads the value.
func TestEffects_PushAutoApproveSharedText(t *testing.T) {
	const want = "No step reads this value now. With the mode unattended, the read config holds yes, also when the file holds no. The commit step of /ship pushes the feature branch with no question. The push skips the default branch."
	for _, option := range []string{"yes", "no"} {
		if got := effectText(t, "automation", "push.featureBranchAutoApprove", option); got != want {
			t.Errorf("push.featureBranchAutoApprove %s effect = %q, want %q", option, got, want)
		}
	}
}

func TestSections_ReviewDimensionsNamesMd(t *testing.T) {
	for _, s := range Sections() {
		if s.ID != "review-dimensions" {
			continue
		}
		wantContains(t, "Purpose", s.Purpose, "review-dimensions/*.md")
		if !slices.Equal(s.FilesModified, []string{".sdlc-v2/review-dimensions/*.md"}) {
			t.Errorf("FilesModified = %v, want [.sdlc-v2/review-dimensions/*.md]", s.FilesModified)
		}
		if strings.Contains(s.Purpose, ".yaml") {
			t.Errorf("Purpose still names .yaml: %s", s.Purpose)
		}
		return
	}
	t.Fatal("review-dimensions section not found")
}
