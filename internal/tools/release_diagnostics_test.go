package tools

import (
	"testing"
)

// TestVersionSuggestedPreRelease covers all three PreReleasePolicy values
// against both existing-RCs states. Pure unit test, no filesystem or git
// fixtures — versionSuggestedPreRelease is a pure function.
func TestVersionSuggestedPreRelease(t *testing.T) {
	cases := []struct {
		name           string
		policy         string
		hasExistingRCs bool
		want           string
	}{
		{"always-rc suggests RC with no existing RCs", "always-rc", false, "rc"},
		{"always-rc suggests RC with existing RCs", "always-rc", true, "rc"},
		{"continue-rc suggests RC only when existing RCs found", "continue-rc", true, "rc"},
		{"continue-rc suggests nothing with no existing RCs", "continue-rc", false, ""},
		{"never suggests nothing with existing RCs", "never", true, ""},
		{"never suggests nothing with no existing RCs", "never", false, ""},
		{"unrecognized policy falls through like never", "bogus", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := versionSuggestedPreRelease(tc.policy, tc.hasExistingRCs)
			if got != tc.want {
				t.Errorf("versionSuggestedPreRelease(%q, %v) = %q, want %q", tc.policy, tc.hasExistingRCs, got, tc.want)
			}
		})
	}
}
