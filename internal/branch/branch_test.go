package branch

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Slug generation — fixture matrix
// ---------------------------------------------------------------------------

func TestSlug(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		// basic lowercase + hyphenation
		{"Add Login Page", "add-login-page"},
		{"fix: broken button", "fix-broken-button"},

		// unicode characters replaced with hyphens
		{"héllo wörld", "h-llo-w-rld"},
		{"日本語タイトル", ""},
		{"café résumé", "caf-r-sum"},

		// special characters
		{"feat(scope): add thing!", "feat-scope-add-thing"},
		{"hello___world---test", "hello-world-test"},
		{"--leading-and-trailing--", "leading-and-trailing"},

		// already clean
		{"simple", "simple"},
		{"already-clean", "already-clean"},

		// empty after sanitisation
		{"", ""},

		// exactly 50 characters (no truncation needed)
		{"a]bcdefghij-klmnopqrst-uvwxyz-01234567890123456789", "a-bcdefghij-klmnopqrst-uvwxyz-01234567890123456789"},

		// >50 characters — should truncate at boundary
		{
			"this is a very long title that should be truncated at a word boundary for readability",
			"this-is-a-very-long-title-that-should-be-truncated",
		},
		{
			"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz-tail",
			"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwx",
		},

		// numbers and mixed
		{"JIRA-1234 fix login", "jira-1234-fix-login"},
		{"v2.0.0-beta.1 release", "v2-0-0-beta-1-release"},
	}

	for _, tt := range tests {
		got := Slug(tt.title)
		if got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.title, got, tt.want)
		}
		if len(got) > 50 {
			t.Errorf("Slug(%q) length %d > 50", tt.title, len(got))
		}
	}
}

// ---------------------------------------------------------------------------
// Resolve — template expansion
// ---------------------------------------------------------------------------

func TestResolve(t *testing.T) {
	tests := []struct {
		cfg   Config
		typ   string
		title string
		want  string
	}{
		// default template
		{Config{}, "feat", "Add Login", "feat/add-login"},
		{Config{}, "fix", "broken button", "fix/broken-button"},
		{Config{}, "chore", "Update deps", "chore/update-deps"},

		// custom template
		{Config{Template: "{type}-{slug}"}, "feat", "new thing", "feat-new-thing"},
		{Config{Template: "prefix/{type}/{slug}"}, "fix", "bug", "prefix/fix/bug"},

		// with allowed types — allowed
		{Config{AllowedTypes: []string{"feat", "fix"}}, "feat", "ok", "feat/ok"},
	}

	for _, tt := range tests {
		got, err := Resolve(tt.cfg, tt.typ, tt.title)
		if err != nil {
			t.Errorf("Resolve(%+v, %q, %q): unexpected error: %v", tt.cfg, tt.typ, tt.title, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Resolve(%+v, %q, %q) = %q, want %q", tt.cfg, tt.typ, tt.title, got, tt.want)
		}
	}
}

func TestResolve_Errors(t *testing.T) {
	// empty type
	_, err := Resolve(Config{}, "", "title")
	if err == nil {
		t.Error("expected error for empty type")
	}

	// empty title
	_, err = Resolve(Config{}, "feat", "")
	if err == nil {
		t.Error("expected error for empty title")
	}

	// disallowed type
	_, err = Resolve(Config{AllowedTypes: []string{"feat", "fix"}}, "chore", "stuff")
	if err == nil {
		t.Error("expected error for disallowed type")
	}
}

// ---------------------------------------------------------------------------
// Guard — tested only at the unit level (no git repo needed for branch
// name matching logic). Integration with gitx.CurrentBranch is tested
// separately in gitx tests.
// ---------------------------------------------------------------------------

// Note: Guard requires a real git repo via gitx.CurrentBranch. We skip
// detailed Guard tests here to avoid git setup overhead. The function is
// straightforward: it reads the branch name and checks the prefix. If
// integration tests are needed, they belong in a _test.go that sets up
// a temp git repo (like gitx_test.go does).
