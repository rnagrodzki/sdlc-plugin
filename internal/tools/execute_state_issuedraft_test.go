package tools

import (
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// issue-draft action
// ---------------------------------------------------------------------------

func TestExecState_IssueDraft_EmptyTitle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
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
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
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
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
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
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
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

func TestExecState_IssueDraft_UnknownBranch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
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
