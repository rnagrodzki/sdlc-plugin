package tools

import "testing"

func TestInlineSeverityPrefix(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		want    string
	}{
		{"capitalised high", "[High] fix this", "high"},
		{"lower medium", "[medium] fix this", "medium"},
		{"critical", "[critical] fix this", "critical"},
		{"upper critical", "[CRITICAL] fix this", "critical"},
		{"low", "[low] nit", "low"},
		{"minor maps to low", "[minor] nit", "low"},
		{"leading whitespace", "  \n[High] fix this", "high"},
		{"no prefix", "fix this", ""},
		{"unknown tag", "[urgent] fix this", ""},
		{"info is not a prefix", "[info] fyi", ""},
		{"tag not at start", "see [high] later", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inlineSeverityPrefix(tt.comment); got != tt.want {
				t.Errorf("inlineSeverityPrefix(%q) = %q, want %q", tt.comment, got, tt.want)
			}
		})
	}
}

func TestHigherSeverity(t *testing.T) {
	order := []string{"", "low", "medium", "high", "critical"}
	for i, a := range order {
		for j, b := range order {
			want := a
			if j > i {
				want = b
			}
			if got := higherSeverity(a, b); got != want {
				t.Errorf("higherSeverity(%q, %q) = %q, want %q", a, b, got, want)
			}
		}
	}
}
