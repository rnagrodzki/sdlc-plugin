package paths

import (
	"path/filepath"
	"testing"
)

// TestHistoryDir pins the durable history directory to <root>/.sdlc-v2/history.
// Several packages (internal/hooks, internal/tools) join this path to build a
// history.FileWriter; they must agree on it or a deferred item written by one
// is invisible to the other.
func TestHistoryDir(t *testing.T) {
	root := filepath.Join("/tmp", "repo")
	want := filepath.Join(root, DataDir, HistorySubdir)

	if got := HistoryDir(root); got != want {
		t.Errorf("HistoryDir(%q) = %q, want %q", root, got, want)
	}
	if HistorySubdir != "history" {
		t.Errorf("HistorySubdir = %q, want %q", HistorySubdir, "history")
	}
	if got, want := HistoryDir(root), filepath.Join(ProjectDir(root), HistorySubdir); got != want {
		t.Errorf("HistoryDir must sit under ProjectDir: got %q, want %q", got, want)
	}
}

// TestCacheDir pins the shared user cache root: $SDLC_CACHE_DIR when set,
// else <home>/.sdlc-cache, else ./.sdlc-cache. internal/tools, internal/links,
// and internal/hooks each join "jira" onto this root and must agree with
// sdlc-launcher.sh's own CACHE_DIR resolution.
func TestCacheDir(t *testing.T) {
	t.Run("SDLC_CACHE_DIR set wins", func(t *testing.T) {
		t.Setenv("SDLC_CACHE_DIR", "/x")
		if got, want := CacheDir(), "/x"; got != want {
			t.Errorf("CacheDir() = %q, want %q", got, want)
		}
	})

	t.Run("no env, falls back to home", func(t *testing.T) {
		t.Setenv("SDLC_CACHE_DIR", "")
		home := filepath.Join(t.TempDir(), "home")
		t.Setenv("HOME", home)
		want := filepath.Join(home, ".sdlc-cache")
		if got := CacheDir(); got != want {
			t.Errorf("CacheDir() = %q, want %q", got, want)
		}
	})

	t.Run("no env, no home, falls back to cwd", func(t *testing.T) {
		t.Setenv("SDLC_CACHE_DIR", "")
		t.Setenv("HOME", "")
		want := filepath.Join(".", ".sdlc-cache")
		if got := CacheDir(); got != want {
			t.Errorf("CacheDir() = %q, want %q", got, want)
		}
	})
}

// stateEntrySpecNames is the full 21-name spec set: every DataDir entry the
// MCP server or hooks write to, from the worktree-state-links spec's
// Link / Never-link table. BakSuffix is deliberately excluded: it is a
// suffix pattern ("*.bak"), not a named entry.
func stateEntrySpecNames() []string {
	return []string{
		RunsSubdir, ReportsSubdir, HistorySubdir, EvidenceSubdir,
		LearningsSubdir, ReviewsSubdir, StateArtifactsSubdir, TimingsFile,
		ConfigFile, GitignoreFile, ReviewDimensionsSubdir, LocalConfigFile,
		LegacyLocalJSONFile, LegacyConfigJSONFile, JiraTemplatesSubdir, LegacyExecutionSubdir,
		OpenspecStagingSubdir, PlanTemplateFile, PRTemplateFile, ScratchSubdir, BackupsSubdir,
	}
}

// TestStateEntryListsCoverEveryEntry pins LinkedStateEntries and
// UnlinkedStateEntries to the worktree-state-links spec's 21-name set: every
// entry must appear in exactly one of the two lists (never both, never
// neither), so a future DataDir addition that forgets to classify itself as
// linked or unlinked fails this test instead of silently falling through.
func TestStateEntryListsCoverEveryEntry(t *testing.T) {
	want := stateEntrySpecNames()
	if len(want) != 21 {
		t.Fatalf("stateEntrySpecNames() has %d names, want 21", len(want))
	}

	seen := make(map[string]int, len(want))
	for _, name := range LinkedStateEntries {
		seen[name]++
	}
	for _, name := range UnlinkedStateEntries {
		seen[name]++
	}

	for _, name := range want {
		switch seen[name] {
		case 0:
			t.Errorf("entry %q is in neither LinkedStateEntries nor UnlinkedStateEntries", name)
		case 1:
			// exactly one list — correct.
		default:
			t.Errorf("entry %q is in both LinkedStateEntries and UnlinkedStateEntries", name)
		}
	}

	if got := len(LinkedStateEntries) + len(UnlinkedStateEntries); got != len(want) {
		t.Errorf("LinkedStateEntries + UnlinkedStateEntries has %d entries, want %d (disjoint union of the spec set)", got, len(want))
	}

	for _, name := range LinkedStateEntries {
		if name == BakSuffix {
			t.Errorf("BakSuffix must not appear in LinkedStateEntries (it is a suffix pattern, not a named entry)")
		}
	}
	for _, name := range UnlinkedStateEntries {
		if name == BakSuffix {
			t.Errorf("BakSuffix must not appear in UnlinkedStateEntries (it is a suffix pattern, not a named entry)")
		}
	}
}
