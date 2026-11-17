package t3

import (
	"fmt"
	"strings"
	"testing"
)

func TestThreadID(t *testing.T) {
	tests := []struct {
		name string
		set  bool
		val  string
		want string
	}{
		{"valid uuid", true, "7da2a17d-7244-4ecf-a30c-7e5e50d609a1", "7da2a17d-7244-4ecf-a30c-7e5e50d609a1"},
		{"one character", true, "a", "a"},
		{"trimmed", true, "  abc-123\n", "abc-123"},
		{"unset", false, "", ""},
		{"empty", true, "", ""},
		{"only spaces", true, "   ", ""},
		{"slash", true, "abc/def", ""},
		{"question mark", true, "abc?x=1", ""},
		{"hash", true, "abc#frag", ""},
		{"inner space", true, "abc def", ""},
		{"ampersand", true, "abc&def", ""},
		{"max length", true, strings.Repeat("a", MaxThreadIDLen), strings.Repeat("a", MaxThreadIDLen)},
		{"over max length", true, strings.Repeat("a", MaxThreadIDLen+1), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(EnvThreadID, tt.val)
			} else {
				t.Setenv(EnvThreadID, "")
			}
			if got := ThreadID(); got != tt.want {
				t.Errorf("ThreadID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPatternsHoldLengthConstants(t *testing.T) {
	if want := fmt.Sprintf("{1,%d}$", MaxThreadIDLen); !strings.HasSuffix(ThreadIDPattern, want) {
		t.Errorf("ThreadIDPattern %q does not end with %q", ThreadIDPattern, want)
	}
	if want := fmt.Sprintf("{1,%d}$", MaxRunSlugLen); !strings.HasSuffix(RunIDPattern, want) {
		t.Errorf("RunIDPattern %q does not end with %q", RunIDPattern, want)
	}
}

func TestThreadIDWarning(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want string
	}{
		{"unset or empty", "", ""},
		{"only spaces", "   ", ""},
		{"valid", "thread-a", ""},
		{"slash", "abc/def", "T3CODE_THREAD_ID is set but is not 1 to 128 characters of A-Z a-z 0-9 . _ - (got 7 characters): it is ignored, and the run records no T3 Code thread"},
		{"over max length", strings.Repeat("a", MaxThreadIDLen+1), fmt.Sprintf("T3CODE_THREAD_ID is set but is not 1 to %d characters of A-Z a-z 0-9 . _ - (got %d characters): it is ignored, and the run records no T3 Code thread", MaxThreadIDLen, MaxThreadIDLen+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvThreadID, tt.val)
			if got := ThreadIDWarning(); got != tt.want {
				t.Errorf("ThreadIDWarning() = %q, want %q", got, tt.want)
			}
		})
	}
}
