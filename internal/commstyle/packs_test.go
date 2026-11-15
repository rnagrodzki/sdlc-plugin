package commstyle

import "testing"

// TestStandardExample checks the exported reader returns the pair of each
// writing standard, and reports ok == false for any other value.
func TestStandardExample(t *testing.T) {
	for _, standard := range WritingStandards {
		pair, ok := StandardExample(standard)
		before, after := pair.Before, pair.After
		if !ok {
			t.Errorf("StandardExample(%q) ok = false, want true", standard)
			continue
		}
		if before == "" || after == "" || before == after {
			t.Errorf("StandardExample(%q) = (%q, %q), want two different non-empty texts", standard, before, after)
		}
		want := standardExamples[standard]
		if before != want.Before || after != want.After {
			t.Errorf("StandardExample(%q) = (%q, %q), want the pair of standardExamples", standard, before, after)
		}
	}

	for _, bad := range []string{"x", "", "STE"} {
		pair, ok := StandardExample(bad)
		before, after := pair.Before, pair.After
		if ok || before != "" || after != "" {
			t.Errorf("StandardExample(%q) = (%q, %q, %v), want (\"\", \"\", false)", bad, before, after, ok)
		}
	}
}
