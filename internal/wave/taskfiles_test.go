package wave

import "testing"

// TestResolveTaskForFile is a pure, table-driven test — no filesystem
// access, matching internal/wave/progress_test.go's existing pattern for
// non-I/O cases.
func TestResolveTaskForFile(t *testing.T) {
	cases := []struct {
		name        string
		filesByTask map[string][]string
		rel         string
		want        string
	}{
		{
			name:        "nil map",
			filesByTask: nil,
			rel:         "internal/x.go",
			want:        "",
		},
		{
			name:        "empty map",
			filesByTask: map[string][]string{},
			rel:         "internal/x.go",
			want:        "",
		},
		{
			name: "single match",
			filesByTask: map[string][]string{
				"T1": {"internal/x.go"},
				"T2": {"internal/y.go"},
			},
			rel:  "internal/x.go",
			want: "T1",
		},
		{
			name: "zero matches",
			filesByTask: map[string][]string{
				"T1": {"internal/x.go"},
			},
			rel:  "internal/z.go",
			want: "",
		},
		{
			name: "two tasks list the same file",
			filesByTask: map[string][]string{
				"T1": {"internal/x.go"},
				"T2": {"internal/x.go"},
			},
			rel:  "internal/x.go",
			want: "",
		},
		{
			name: "normalizes dot-slash prefix",
			filesByTask: map[string][]string{
				"T1": {"./internal/x.go"},
			},
			rel:  "internal/x.go",
			want: "T1",
		},
		{
			name: "normalizes backslashes",
			filesByTask: map[string][]string{
				"T1": {"internal\\x.go"},
			},
			rel:  "internal/x.go",
			want: "T1",
		},
		{
			name: "normalizes doubled slashes",
			filesByTask: map[string][]string{
				"T1": {"internal//x.go"},
			},
			rel:  "internal/x.go",
			want: "T1",
		},
		{
			name: "normalized entries still miss an unrelated file",
			filesByTask: map[string][]string{
				"T1": {"./internal/x.go", "internal\\x.go", "internal//x.go"},
			},
			rel:  "internal/y.go",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveTaskForFile(tc.filesByTask, tc.rel)
			if got != tc.want {
				t.Fatalf("ResolveTaskForFile(%v, %q) = %q, want %q", tc.filesByTask, tc.rel, got, tc.want)
			}
		})
	}
}
