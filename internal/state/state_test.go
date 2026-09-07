package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// SlugifyBranch
// ---------------------------------------------------------------------------

func TestSlugifyBranch(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"feat/my-feature", "feat-my-feature"},
		{"main", "main"},
		{"fix/220-foo", "fix-220-foo"},
		{"release/v1.2.3", "release-v1-2-3"},
		{"a/b/c", "a-b-c"},
		{"already-clean", "already-clean"},
		{"dots.and_underscores", "dots-and-underscores"},
		{"UPPER/Case", "UPPER-Case"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SlugifyBranch(tt.input)
			if got != tt.want {
				t.Fatalf("SlugifyBranch(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseStateFilename
// ---------------------------------------------------------------------------

func TestParseStateFilename(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		prefix  string
		slug    string
		ts      string
	}{
		{"ship simple", "ship-main-20260101T120000Z.json", false, "ship", "main", "20260101T120000Z"},
		{"execute with dashes", "execute-fix-220-foo-20260101T120000Z.json", false, "execute", "fix-220-foo", "20260101T120000Z"},
		{"plan", "plan-release-v1-2-3-20260905T214500Z.json", false, "plan", "release-v1-2-3", "20260905T214500Z"},
		{"commit", "commit-main-20260101T000000Z.json", false, "commit", "main", "20260101T000000Z"},
		{"unknown prefix", "deploy-main-20260101T120000Z.json", true, "", "", ""},
		{"no json ext", "ship-main-20260101T120000Z.txt", true, "", "", ""},
		{"bad timestamp", "ship-main-202601.json", true, "", "", ""},
		{"empty", "", true, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseStateFilename(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected non-nil result")
			}
			if got.Prefix != tt.prefix || got.Slug != tt.slug || got.Timestamp != tt.ts {
				t.Fatalf("got {%s, %s, %s}, want {%s, %s, %s}",
					got.Prefix, got.Slug, got.Timestamp,
					tt.prefix, tt.slug, tt.ts)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Init + Find round-trip
// ---------------------------------------------------------------------------

func TestInit_CreatesFileAndFindsIt(t *testing.T) {
	root := t.TempDir()
	st, err := Init(root, "ship", "feat/my-feature", "session-abc")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	if st.Prefix != "ship" {
		t.Fatalf("Prefix = %q, want %q", st.Prefix, "ship")
	}
	if st.BranchSlug != "feat-my-feature" {
		t.Fatalf("BranchSlug = %q, want %q", st.BranchSlug, "feat-my-feature")
	}
	if st.Data["sessionId"] != "session-abc" {
		t.Fatalf("sessionId = %v, want %q", st.Data["sessionId"], "session-abc")
	}

	// The file should exist on disk.
	if _, err := os.Stat(st.Path); err != nil {
		t.Fatalf("state file does not exist: %v", err)
	}

	// Find should locate it.
	found, err := Find(root, "ship", "feat/my-feature")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found == nil {
		t.Fatalf("Find returned nil")
	}
	if found.Path != st.Path {
		t.Fatalf("Find path = %q, want %q", found.Path, st.Path)
	}
	if found.Data["sessionId"] != "session-abc" {
		t.Fatalf("Find sessionId = %v, want %q", found.Data["sessionId"], "session-abc")
	}
}

func TestInit_EmptySessionIDStoresNil(t *testing.T) {
	root := t.TempDir()
	st, err := Init(root, "plan", "main", "")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if st.Data["sessionId"] != nil {
		t.Fatalf("sessionId = %v, want nil", st.Data["sessionId"])
	}

	// Verify on-disk representation has null.
	raw, err := os.ReadFile(st.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed["sessionId"] != nil {
		t.Fatalf("on-disk sessionId = %v, want null", parsed["sessionId"])
	}
}

// ---------------------------------------------------------------------------
// Find — delimiter-aware matching + mtime-newest
// ---------------------------------------------------------------------------

func TestFind_DelimiterAwareAndMtimeNewest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Create fixture files with controlled mtimes.
	// ship-main-* files (two, older and newer).
	oldMain := "ship-main-20260101T100000Z.json"
	newMain := "ship-main-20260102T100000Z.json"
	// ship-maintain-* file (should NOT match slug "main" in Find — but
	// actually it DOES in JS because of prefix matching; however the
	// timestamp portion is embedded in the prefix match. Let's verify the
	// delimiter-aware matching).
	maintain := "ship-maintain-feature-20260103T100000Z.json"
	// execute-main-* file (different prefix, should not match).
	execMain := "execute-main-20260104T100000Z.json"

	files := map[string]time.Time{
		oldMain:  time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		newMain:  time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC),
		maintain: time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC),
		execMain: time.Date(2026, 1, 4, 10, 0, 0, 0, time.UTC),
	}

	for name, mtime := range files {
		fp := filepath.Join(dir, name)
		data := map[string]any{"file": name}
		raw, _ := json.Marshal(data)
		if err := os.WriteFile(fp, raw, 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
		if err := os.Chtimes(fp, mtime, mtime); err != nil {
			t.Fatalf("Chtimes %s: %v", name, err)
		}
	}

	// Find ship + main should pick the newer main file, not maintain or execute.
	found, err := Find(root, "ship", "main")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found == nil {
		t.Fatalf("Find returned nil, expected a match")
	}
	if filepath.Base(found.Path) != newMain {
		t.Fatalf("Find picked %q, want %q", filepath.Base(found.Path), newMain)
	}

	// Find execute + main should pick execMain.
	found2, err := Find(root, "execute", "main")
	if err != nil {
		t.Fatalf("Find execute: %v", err)
	}
	if found2 == nil {
		t.Fatalf("Find execute returned nil")
	}
	if filepath.Base(found2.Path) != execMain {
		t.Fatalf("Find execute picked %q, want %q", filepath.Base(found2.Path), execMain)
	}
}

func TestFind_NoMatch_ReturnsNilNil(t *testing.T) {
	root := t.TempDir()
	// No state dir exists at all.
	found, err := Find(root, "ship", "main")
	if err != nil {
		t.Fatalf("Find: unexpected error %v", err)
	}
	if found != nil {
		t.Fatalf("Find returned non-nil for missing state dir")
	}
}

// ---------------------------------------------------------------------------
// Write — prune-on-write
// ---------------------------------------------------------------------------

func TestWrite_PrunesOldFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Create two old ship-main files that should be pruned.
	old1 := "ship-main-20260101T100000Z.json"
	old2 := "ship-main-20260102T100000Z.json"
	// A file for a different slug (main-extra) that should NOT be pruned.
	other := "ship-main-extra-20260101T100000Z.json"
	// A file for a different prefix that should NOT be pruned.
	diffPrefix := "execute-main-20260101T100000Z.json"

	for _, name := range []string{old1, old2, other, diffPrefix} {
		fp := filepath.Join(dir, name)
		if err := os.WriteFile(fp, []byte(`{}`), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	// The state we're writing — ship-main, a new file.
	current := "ship-main-20260103T100000Z.json"
	currentPath := filepath.Join(dir, current)
	st := &State{
		Path:       currentPath,
		Root:       root,
		Prefix:     "ship",
		BranchSlug: "main",
		Data:       map[string]any{"written": true},
	}

	if err := Write(st); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Verify current file exists and has our data.
	raw, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("ReadFile current: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed["written"] != true {
		t.Fatalf("written field = %v, want true", parsed["written"])
	}

	// old1 and old2 should be pruned.
	for _, name := range []string{old1, old2} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be pruned, but it still exists", name)
		}
	}

	// other and diffPrefix should still exist.
	for _, name := range []string{other, diffPrefix} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to survive prune, got error: %v", name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Filename round-trip: Init → Find → parse yields same file
// ---------------------------------------------------------------------------

func TestFilenameRoundTrip(t *testing.T) {
	root := t.TempDir()

	st, err := Init(root, "execute", "fix/220-dashboard", "sess-42")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	found, err := Find(root, "execute", "fix/220-dashboard")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found == nil {
		t.Fatalf("Find returned nil after Init")
	}
	if found.Path != st.Path {
		t.Fatalf("round-trip path mismatch: init=%q find=%q", st.Path, found.Path)
	}

	// Parse the filename.
	parsed := parseStateFilename(filepath.Base(found.Path))
	if parsed == nil {
		t.Fatalf("parseStateFilename returned nil for %q", filepath.Base(found.Path))
	}
	if parsed.Prefix != "execute" {
		t.Fatalf("parsed prefix = %q, want %q", parsed.Prefix, "execute")
	}
	if parsed.Slug != "fix-220-dashboard" {
		t.Fatalf("parsed slug = %q, want %q", parsed.Slug, "fix-220-dashboard")
	}
}

// ---------------------------------------------------------------------------
// PipelineAdvancing — truth table (8 combinations)
// ---------------------------------------------------------------------------

func TestPipelineAdvancing(t *testing.T) {
	step := func(name, status string) map[string]any {
		return map[string]any{"name": name, "status": status}
	}

	tests := []struct {
		name      string
		data      map[string]any
		advancing bool
		stepName  string
		index     int
	}{
		{
			name:      "nil data",
			data:      nil,
			advancing: false,
			stepName:  "",
			index:     -1,
		},
		{
			name:      "no steps",
			data:      map[string]any{},
			advancing: false,
			stepName:  "",
			index:     -1,
		},
		{
			name: "all completed/skipped",
			data: map[string]any{
				"steps": []any{
					step("build", "completed"),
					step("test", "skipped"),
				},
			},
			advancing: false,
			stepName:  "",
			index:     -1,
		},
		{
			name: "failed only (no pending)",
			data: map[string]any{
				"steps": []any{
					step("build", "failed"),
				},
			},
			advancing: false,
			stepName:  "",
			index:     -1,
		},
		{
			name: "in_progress alone",
			data: map[string]any{
				"steps": []any{
					step("build", "in_progress"),
				},
			},
			advancing: true,
			stepName:  "build",
			index:     0,
		},
		{
			name: "in_progress + failed",
			data: map[string]any{
				"steps": []any{
					step("build", "failed"),
					step("test", "in_progress"),
				},
			},
			advancing: true,
			stepName:  "test",
			index:     1,
		},
		{
			name: "pending no failed",
			data: map[string]any{
				"steps": []any{
					step("build", "completed"),
					step("test", "pending"),
				},
			},
			advancing: true,
			stepName:  "test",
			index:     1,
		},
		{
			name: "pending + failed, pending != cleanup",
			data: map[string]any{
				"steps": []any{
					step("build", "failed"),
					step("test", "pending"),
				},
			},
			advancing: false,
			stepName:  "",
			index:     -1,
		},
		{
			name: "pending + failed, pending == cleanup (R38 path)",
			data: map[string]any{
				"steps": []any{
					step("build", "failed"),
					step("cleanup", "pending"),
				},
			},
			advancing: true,
			stepName:  "cleanup",
			index:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := PipelineAdvancing(tt.data)
			if res.Advancing != tt.advancing {
				t.Fatalf("advancing = %v, want %v", res.Advancing, tt.advancing)
			}
			if !tt.advancing {
				if res.Step != nil {
					t.Fatalf("step should be nil when not advancing, got %v", res.Step)
				}
				if res.Index != -1 {
					t.Fatalf("index should be -1 when not advancing, got %d", res.Index)
				}
				return
			}
			gotName, _ := res.Step["name"].(string)
			if gotName != tt.stepName {
				t.Fatalf("step name = %q, want %q", gotName, tt.stepName)
			}
			if res.Index != tt.index {
				t.Fatalf("index = %d, want %d", res.Index, tt.index)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// HookEnforcementAllowed
// ---------------------------------------------------------------------------

func TestHookEnforcementAllowed(t *testing.T) {
	tests := []struct {
		name      string
		data      map[string]any
		sessionID string
		allowed   bool
		reason    string
	}{
		{
			name:      "both absent",
			data:      nil,
			sessionID: "",
			allowed:   false,
			reason:    "state sessionId absent",
		},
		{
			name:      "state sessionId absent",
			data:      map[string]any{},
			sessionID: "sess-1",
			allowed:   false,
			reason:    "state sessionId absent",
		},
		{
			name:      "payload session_id absent",
			data:      map[string]any{"sessionId": "sess-1"},
			sessionID: "",
			allowed:   false,
			reason:    "payload session_id absent",
		},
		{
			name:      "session id mismatch",
			data:      map[string]any{"sessionId": "sess-1"},
			sessionID: "sess-2",
			allowed:   false,
			reason:    "session id mismatch",
		},
		{
			name:      "session id match",
			data:      map[string]any{"sessionId": "sess-1"},
			sessionID: "sess-1",
			allowed:   true,
			reason:    "session id match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := HookEnforcementAllowed(tt.data, tt.sessionID)
			if res.Allowed != tt.allowed {
				t.Fatalf("allowed = %v, want %v", res.Allowed, tt.allowed)
			}
			if res.Reason != tt.reason {
				t.Fatalf("reason = %q, want %q", res.Reason, tt.reason)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Find — prefix match includes slug superstrings (Node parity)
// ---------------------------------------------------------------------------

// TestFind_PrefixMatchIncludesSlugSuperstrings verifies that Find's prefix
// match (HasPrefix(name, "ship-main-")) also picks up files whose slug is a
// superstring of the query slug (e.g. "main-extra" when searching for "main"),
// provided that superstring file has the newest mtime. This mirrors the JS
// findStateFile behaviour exactly — confirmed by running the same fixture
// through SDLC_STATE_DIR_OVERRIDE + node findStateFile.
func TestFind_PrefixMatchIncludesSlugSuperstrings(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	files := map[string]time.Time{
		"ship-main-20260101T100000Z.json":       time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		"ship-main-20260102T100000Z.json":       time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC),
		"ship-main-extra-20260103T100000Z.json": time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC),
		"execute-main-20260104T100000Z.json":    time.Date(2026, 1, 4, 10, 0, 0, 0, time.UTC),
	}
	for name, mtime := range files {
		fp := filepath.Join(dir, name)
		if err := os.WriteFile(fp, []byte(`{"fixture":true}`), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
		if err := os.Chtimes(fp, mtime, mtime); err != nil {
			t.Fatalf("Chtimes %s: %v", name, err)
		}
	}

	// Find ship+main should pick ship-main-extra because it has the newest
	// mtime and its filename starts with "ship-main-" (the prefix match
	// pattern). This is the JS parity behaviour.
	found, err := Find(root, "ship", "main")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found == nil {
		t.Fatalf("Find returned nil")
	}
	want := "ship-main-extra-20260103T100000Z.json"
	if filepath.Base(found.Path) != want {
		t.Fatalf("Find picked %q, want %q (Node parity)", filepath.Base(found.Path), want)
	}
}

// ---------------------------------------------------------------------------
// Find — mixed fixture directory (parity with Node implementation)
// ---------------------------------------------------------------------------

func TestFind_MixedFixtureDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Populate with a realistic mix of files across prefixes and branches.
	fixtures := []struct {
		name  string
		mtime time.Time
	}{
		{"ship-feat-login-20260101T100000Z.json", time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
		{"ship-feat-login-20260102T100000Z.json", time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)},
		{"execute-feat-login-20260103T100000Z.json", time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC)},
		{"plan-main-20260101T100000Z.json", time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
		{"commit-main-20260102T100000Z.json", time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)},
		// Noise: not a json file.
		{"ship-feat-login-20260104T100000Z.txt", time.Date(2026, 1, 4, 10, 0, 0, 0, time.UTC)},
	}

	for _, f := range fixtures {
		fp := filepath.Join(dir, f.name)
		if err := os.WriteFile(fp, []byte(`{"fixture":true}`), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", f.name, err)
		}
		if err := os.Chtimes(fp, f.mtime, f.mtime); err != nil {
			t.Fatalf("Chtimes %s: %v", f.name, err)
		}
	}

	// ship + feat/login → newest ship-feat-login file.
	found, err := Find(root, "ship", "feat/login")
	if err != nil {
		t.Fatalf("Find ship: %v", err)
	}
	if found == nil {
		t.Fatalf("Find ship returned nil")
	}
	wantBase := "ship-feat-login-20260102T100000Z.json"
	if filepath.Base(found.Path) != wantBase {
		t.Fatalf("Find ship picked %q, want %q", filepath.Base(found.Path), wantBase)
	}

	// execute + feat/login → the single execute file.
	found2, err := Find(root, "execute", "feat/login")
	if err != nil {
		t.Fatalf("Find execute: %v", err)
	}
	if found2 == nil {
		t.Fatalf("Find execute returned nil")
	}
	wantBase2 := "execute-feat-login-20260103T100000Z.json"
	if filepath.Base(found2.Path) != wantBase2 {
		t.Fatalf("Find execute picked %q, want %q", filepath.Base(found2.Path), wantBase2)
	}

	// ship + nonexistent branch → nil.
	found3, err := Find(root, "ship", "nonexistent")
	if err != nil {
		t.Fatalf("Find nonexistent: %v", err)
	}
	if found3 != nil {
		t.Fatalf("Find nonexistent should return nil, got %v", found3)
	}
}

// ---------------------------------------------------------------------------
// Init does NOT prune (unlike Write)
// ---------------------------------------------------------------------------

func TestInit_DoesNotPrune(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Pre-create an old state file.
	old := "ship-main-20260101T100000Z.json"
	if err := os.WriteFile(filepath.Join(dir, old), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Init should NOT remove the old file.
	_, err := Init(root, "ship", "main", "sess-1")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Old file should still exist.
	if _, err := os.Stat(filepath.Join(dir, old)); err != nil {
		t.Fatalf("old file was pruned by Init — it should not be: %v", err)
	}
}

// ---------------------------------------------------------------------------
// FindAny — branch-agnostic prefix lookup
// ---------------------------------------------------------------------------

func TestFindAny_NoMatch_ReturnsNilNil(t *testing.T) {
	root := t.TempDir()
	// No state dir exists at all.
	found, err := FindAny(root, "ship")
	if err != nil {
		t.Fatalf("FindAny: unexpected error %v", err)
	}
	if found != nil {
		t.Fatalf("FindAny returned non-nil for missing state dir")
	}

	// State dir exists but has no matching files.
	dir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	found, err = FindAny(root, "ship")
	if err != nil {
		t.Fatalf("FindAny: unexpected error %v", err)
	}
	if found != nil {
		t.Fatalf("FindAny returned non-nil for empty state dir")
	}
}

func TestFindAny_SingleMatch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	name := "ship-feat-login-20260101T100000Z.json"
	fp := filepath.Join(dir, name)
	if err := os.WriteFile(fp, []byte(`{"paused":true}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	found, err := FindAny(root, "ship")
	if err != nil {
		t.Fatalf("FindAny: %v", err)
	}
	if found == nil {
		t.Fatalf("FindAny returned nil, want a match")
	}
	if filepath.Base(found.Path) != name {
		t.Fatalf("FindAny picked %q, want %q", filepath.Base(found.Path), name)
	}
	if found.Prefix != "ship" {
		t.Fatalf("FindAny Prefix = %q, want %q", found.Prefix, "ship")
	}
	if found.BranchSlug != "feat-login" {
		t.Fatalf("FindAny BranchSlug = %q, want %q", found.BranchSlug, "feat-login")
	}
	if paused, _ := found.Data["paused"].(bool); !paused {
		t.Fatalf("FindAny Data[paused] = %v, want true", found.Data["paused"])
	}
}

func TestFindAny_MultipleMatches_PicksMostRecentByMtime(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Three ship state files across three different branches — FindAny must
	// ignore branch identity entirely and pick the newest by mtime.
	files := map[string]time.Time{
		"ship-main-20260101T100000Z.json":       time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		"ship-feat-login-20260103T100000Z.json": time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC),
		"ship-feat-other-20260102T100000Z.json": time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC),
	}
	for name, mtime := range files {
		fp := filepath.Join(dir, name)
		if err := os.WriteFile(fp, []byte(`{"fixture":true}`), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
		if err := os.Chtimes(fp, mtime, mtime); err != nil {
			t.Fatalf("Chtimes %s: %v", name, err)
		}
	}

	found, err := FindAny(root, "ship")
	if err != nil {
		t.Fatalf("FindAny: %v", err)
	}
	if found == nil {
		t.Fatalf("FindAny returned nil")
	}
	want := "ship-feat-login-20260103T100000Z.json"
	if filepath.Base(found.Path) != want {
		t.Fatalf("FindAny picked %q, want %q (most recent by mtime, any branch)", filepath.Base(found.Path), want)
	}
}

func TestFindAny_MixedPrefixesDoesNotCrossMatch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	fixtures := []struct {
		name  string
		mtime time.Time
	}{
		{"ship-main-20260101T100000Z.json", time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
		// Newer execute file — must NOT be picked when querying prefix "ship".
		{"execute-feat-login-20260105T100000Z.json", time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)},
		{"plan-main-20260104T100000Z.json", time.Date(2026, 1, 4, 10, 0, 0, 0, time.UTC)},
		{"commit-main-20260103T100000Z.json", time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC)},
		// Not JSON — must be ignored even though it starts with "ship-" and
		// is the newest by mtime.
		{"ship-main-20260106T100000Z.txt", time.Date(2026, 1, 6, 10, 0, 0, 0, time.UTC)},
	}
	for _, f := range fixtures {
		fp := filepath.Join(dir, f.name)
		if err := os.WriteFile(fp, []byte(`{"fixture":true}`), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", f.name, err)
		}
		if err := os.Chtimes(fp, f.mtime, f.mtime); err != nil {
			t.Fatalf("Chtimes %s: %v", f.name, err)
		}
	}

	found, err := FindAny(root, "ship")
	if err != nil {
		t.Fatalf("FindAny: %v", err)
	}
	if found == nil {
		t.Fatalf("FindAny returned nil")
	}
	want := "ship-main-20260101T100000Z.json"
	if filepath.Base(found.Path) != want {
		t.Fatalf("FindAny picked %q, want %q (must not cross-match other prefixes or non-json)", filepath.Base(found.Path), want)
	}

	// Querying prefix "execute" should pick the execute file, unaffected by
	// the presence of ship/plan/commit files.
	found, err = FindAny(root, "execute")
	if err != nil {
		t.Fatalf("FindAny execute: %v", err)
	}
	if found == nil {
		t.Fatalf("FindAny execute returned nil")
	}
	wantExec := "execute-feat-login-20260105T100000Z.json"
	if filepath.Base(found.Path) != wantExec {
		t.Fatalf("FindAny execute picked %q, want %q", filepath.Base(found.Path), wantExec)
	}
}
