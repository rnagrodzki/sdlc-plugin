package tools

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// issue-draft action
// ---------------------------------------------------------------------------

func TestExecState_IssueDraft_EmptyTitle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	createExecState(t, root, "feat/drafts", map[string]any{
		"branch": "feat/drafts",
	})
	clock := fixedClock(testNow)

	_, err := executeState(root, root, ExecuteStateIn{
		Action:          "issue-draft",
		Branch:          "feat/drafts",
		IssueDraftTitle: "   ",
		IssueDraftBody:  "some body",
	}, clock)
	if err == nil {
		t.Fatal("expected error for empty title")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
}

func TestExecState_IssueDraft_EmptyBody(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	createExecState(t, root, "feat/drafts", map[string]any{
		"branch": "feat/drafts",
	})
	clock := fixedClock(testNow)

	_, err := executeState(root, root, ExecuteStateIn{
		Action:          "issue-draft",
		Branch:          "feat/drafts",
		IssueDraftTitle: "a title",
		IssueDraftBody:  "",
	}, clock)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
}

func TestExecState_IssueDraft_SingleCall(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	createExecState(t, root, "feat/drafts", map[string]any{
		"branch": "feat/drafts",
	})
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action:           "issue-draft",
		Branch:           "feat/drafts",
		IssueDraftTitle:  "Fix flaky test",
		IssueDraftBody:   "The test fails intermittently.",
		IssueDraftLabels: []string{"bug", "flaky"},
		TaskID:           "7",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(IssueDraftOut)
	if !ok {
		t.Fatalf("expected IssueDraftOut, got %T", result)
	}
	if !out.Added {
		t.Error("expected Added=true")
	}
	if out.TotalDrafts != 1 {
		t.Errorf("expected TotalDrafts=1, got %d", out.TotalDrafts)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none on a clean persist", out.Warnings)
	}

	data := readExecState(t, root, "feat/drafts")
	drafts, _ := data["pendingIssueDrafts"].([]any)
	if len(drafts) != 1 {
		t.Fatalf("expected 1 pending draft in state, got %d", len(drafts))
	}
	draft, ok := drafts[0].(map[string]any)
	if !ok {
		t.Fatalf("expected draft entry to be a map, got %T", drafts[0])
	}
	if draft["title"] != "Fix flaky test" {
		t.Errorf("expected title %q, got %v", "Fix flaky test", draft["title"])
	}
	if draft["body"] != "The test fails intermittently." {
		t.Errorf("expected body %q, got %v", "The test fails intermittently.", draft["body"])
	}
	if draft["taskId"] != "7" {
		t.Errorf("expected taskId %q, got %v", "7", draft["taskId"])
	}
	labels, _ := draft["labels"].([]any)
	if len(labels) != 2 || labels[0] != "bug" || labels[1] != "flaky" {
		t.Errorf("expected labels [bug flaky], got %v", draft["labels"])
	}
}

func TestExecState_IssueDraft_AccumulatesAcrossCalls(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	createExecState(t, root, "feat/drafts", map[string]any{
		"branch": "feat/drafts",
	})
	clock := fixedClock(testNow)

	for i, title := range []string{"first issue", "second issue", "third issue"} {
		result, err := executeState(root, root, ExecuteStateIn{
			Action:          "issue-draft",
			Branch:          "feat/drafts",
			IssueDraftTitle: title,
			IssueDraftBody:  "body for " + title,
		}, clock)
		if err != nil {
			t.Fatalf("unexpected error on call %d: %v", i, err)
		}
		out := result.(IssueDraftOut)
		if out.TotalDrafts != i+1 {
			t.Errorf("call %d: expected TotalDrafts=%d, got %d", i, i+1, out.TotalDrafts)
		}
	}

	data := readExecState(t, root, "feat/drafts")
	drafts, _ := data["pendingIssueDrafts"].([]any)
	if len(drafts) != 3 {
		t.Fatalf("expected 3 accumulated drafts, got %d", len(drafts))
	}
	// Never overwritten: each entry keeps its own distinct title.
	first, _ := drafts[0].(map[string]any)
	third, _ := drafts[2].(map[string]any)
	if first["title"] != "first issue" {
		t.Errorf("expected first draft title %q, got %v", "first issue", first["title"])
	}
	if third["title"] != "third issue" {
		t.Errorf("expected third draft title %q, got %v", "third issue", third["title"])
	}
}

// ---------------------------------------------------------------------------
// issue-draft: durable persistence to .sdlc-v2/history/deferred.json (KD-1)
// ---------------------------------------------------------------------------

func TestExecState_IssueDraft_PersistsToDeferredHistory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	createExecState(t, root, "feat/drafts", map[string]any{"branch": "feat/drafts"})
	mem := useMemHistory(t)

	result, err := executeState(root, root, ExecuteStateIn{
		Action:          "issue-draft",
		Branch:          "feat/drafts",
		IssueDraftTitle: "Fix flaky test",
		IssueDraftBody:  "The test fails intermittently.",
		TaskID:          "7",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("issue-draft: %v", err)
	}

	if len(mem.Deferred) != 1 {
		t.Fatalf("deferred.json entries = %d, want 1", len(mem.Deferred))
	}
	ts := testNow.UTC().Format("2006-01-02T15:04:05Z07:00")
	want := history.DeferredIssue{
		ID:          "execute-drift-" + ts + "-1",
		Created:     ts,
		Source:      "execute-drift",
		Priority:    "medium",
		Description: "Fix flaky test", // title only — DeferredIssue has no body field
		Status:      "open",
	}
	if mem.Deferred[0] != want {
		t.Errorf("deferred entry =\n %+v\nwant\n %+v", mem.Deferred[0], want)
	}

	// The response echoes the id it just wrote, so the caller can name the
	// entry in a later deferred operation without reconstructing the format.
	out, ok := result.(IssueDraftOut)
	if !ok {
		t.Fatalf("expected IssueDraftOut, got %T", result)
	}
	if out.DeferredID != want.ID {
		t.Errorf("DeferredID = %q, want %q", out.DeferredID, want.ID)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", out.Warnings)
	}
	if !strings.Contains(out.Next, "durably") {
		t.Errorf("Next = %q, want it to say the draft is recorded durably", out.Next)
	}
	if strings.Contains(out.Next, "deferred_add") {
		t.Errorf("Next = %q, want no recovery call on the success path", out.Next)
	}
}

// Two drafts recorded at the same instant must land as two entries with
// distinct ids — the 1-based position disambiguates them.
func TestExecState_IssueDraft_SameTimestampDistinctIDs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	createExecState(t, root, "feat/drafts", map[string]any{"branch": "feat/drafts"})
	mem := useMemHistory(t)

	for _, title := range []string{"first issue", "second issue"} {
		if _, err := executeState(root, root, ExecuteStateIn{
			Action:          "issue-draft",
			Branch:          "feat/drafts",
			IssueDraftTitle: title,
			IssueDraftBody:  "body for " + title,
		}, fixedClock(testNow)); err != nil {
			t.Fatalf("issue-draft %q: %v", title, err)
		}
	}

	if len(mem.Deferred) != 2 {
		t.Fatalf("deferred.json entries = %d, want 2", len(mem.Deferred))
	}
	if mem.Deferred[0].ID == mem.Deferred[1].ID {
		t.Errorf("both entries share id %q, want distinct ids", mem.Deferred[0].ID)
	}
	if !strings.HasSuffix(mem.Deferred[0].ID, "-1") || !strings.HasSuffix(mem.Deferred[1].ID, "-2") {
		t.Errorf("ids = %q, %q; want 1-based position suffixes -1 and -2",
			mem.Deferred[0].ID, mem.Deferred[1].ID)
	}
}

// The same-id guard inside persistDeferred is pinned by
// TestPersistDeferred_SkipsDuplicateID in ship_state_test.go. It is not
// reachable from issue-draft: every call appends a draft and derives the id
// from the new 1-based position, so two calls always mean two distinct ids
// (TestExecState_IssueDraft_SameTimestampDistinctIDs pins that).

func TestExecState_IssueDraft_PersistFailureWarnsButSucceeds(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	createExecState(t, root, "feat/drafts", map[string]any{"branch": "feat/drafts"})
	useFailingHistory(t)

	result, err := executeState(root, root, ExecuteStateIn{
		Action:          "issue-draft",
		Branch:          "feat/drafts",
		IssueDraftTitle: "Fix flaky test",
		IssueDraftBody:  "The test fails intermittently.",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("issue-draft must not fail when deferred.json is unwritable: %v", err)
	}

	out, ok := result.(IssueDraftOut)
	if !ok {
		t.Fatalf("expected IssueDraftOut, got %T", result)
	}
	if !out.Added || out.TotalDrafts != 1 {
		t.Errorf("out = %+v, want Added=true TotalDrafts=1", out)
	}
	// Nothing durable was written, so no id may be echoed.
	if out.DeferredID != "" {
		t.Errorf("DeferredID = %q, want empty when the persist failed", out.DeferredID)
	}
	if len(out.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want exactly one", out.Warnings)
	}
	warning := out.Warnings[0]
	if !strings.Contains(warning, "deferred.json") || !strings.Contains(warning, "no space left on device") {
		t.Errorf("warning = %q, want it to name the deferred.json write failure", warning)
	}
	// The recovery must be actionable: the exact call, the exact id, and an
	// explicit "do not retry" (a retry appends a duplicate draft).
	wantID := "execute-drift-" + testNow.UTC().Format("2006-01-02T15:04:05Z07:00") + "-1"
	for _, want := range []string{"ship_state action=deferred_add", "detail.id=" + wantID, "detail.description=Fix flaky test", "Do NOT retry issue-draft"} {
		if !strings.Contains(out.Next, want) {
			t.Errorf("Next = %q, want it to contain %q", out.Next, want)
		}
	}

	// The run-scoped write still happened — only the durable one was lost.
	data := readExecState(t, root, "feat/drafts")
	if drafts, _ := data["pendingIssueDrafts"].([]any); len(drafts) != 1 {
		t.Errorf("pendingIssueDrafts = %v, want 1 entry", drafts)
	}
}

func TestExecState_IssueDraft_UnknownBranch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	clock := fixedClock(testNow)

	_, err := executeState(root, root, ExecuteStateIn{
		Action:          "issue-draft",
		Branch:          "feat/does-not-exist",
		IssueDraftTitle: "title",
		IssueDraftBody:  "body",
	}, clock)
	if err == nil {
		t.Fatal("expected error for missing state file")
	}
	if _, ok := err.(*mcpserver.DataError); !ok {
		t.Fatalf("expected DataError, got %T: %v", err, err)
	}
}
