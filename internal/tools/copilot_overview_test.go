package tools

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readOverviewFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "copilot_overview", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

func TestParseCopilotOverview_Headers(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{"approval", "### 🟢 Approval recommended\n", overviewApprovalRecommended, true},
		{"changes", "### 🟡 Changes recommended\n", overviewChangesRecommended, true},
		{"closer look", "### 🔵 Needs a closer look\n", overviewNeedsCloserLook, true},
		{"no header", "Copilot reviewed 3 files and found nothing.\n", "", false},
		{"unknown header", "### 🔴 Something else\n", "", false},
		{"header on last line, no newline", "intro\n### 🟡 Changes recommended", overviewChangesRecommended, true},
		{"header without marker comment", "text\n### 🔵 Needs a closer look\n\nmore\n", overviewNeedsCloserLook, true},
		{"header with marker comment", "<!-- ccr-overview-v2 -->\n### 🟢 Approval recommended\n<!-- ccr-overview-v2 -->\n", overviewApprovalRecommended, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseCopilotOverview(tc.body)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if got.Header != tc.want {
				t.Errorf("Header = %q, want %q", got.Header, tc.want)
			}
		})
	}
}

func TestParseCopilotOverview_ApprovalFixture(t *testing.T) {
	got, ok := parseCopilotOverview(readOverviewFixture(t, "approval_no_issues.md"))
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got.Header != overviewApprovalRecommended {
		t.Errorf("Header = %q, want %q", got.Header, overviewApprovalRecommended)
	}
	if n := len(got.Open) + len(got.Missed) + len(got.Resolved); n != 0 {
		t.Errorf("items = %d, want 0", n)
	}
}

func TestParseCopilotOverview_ChangesFixture(t *testing.T) {
	body := readOverviewFixture(t, "changes_recommended.md")
	if !strings.Contains(body, "\u200b") {
		t.Fatal("fixture lost its U+200B bytes: re-create it with the printf and sed method")
	}
	got, ok := parseCopilotOverview(body)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := CopilotOverview{
		Header: overviewChangesRecommended,
		Open: []OverviewItem{
			{Title: "Evaluate every oneOf variant", Severity: "medium", CommentID: 4575025310},
		},
		Missed: []OverviewItem{
			{Title: "Stale metadata key", Severity: "low", Path: "src/metadata.ts", Line: 646, Body: "The key is read before it is set."},
		},
		Resolved: []OverviewItem{
			{Title: "Wrong ref path", Severity: "high", CommentID: 4570000001},
			{Title: "Typo in log", Severity: "low", CommentID: 4570000002},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("overview mismatch\n got: %+v\nwant: %+v", got, want)
	}
	if strings.Contains(got.Missed[0].Path, "\u200b") {
		t.Error("missed Path still holds U+200B")
	}
}

func TestParseCopilotOverview_SingularAndPluralTitles(t *testing.T) {
	item := func(id string) string {
		return `- <picture><img alt="High severity" src="x"></picture> [T` + id + `](https://github.com/o/r/pull/1#discussion_r` + id + `)` + "\n"
	}
	body := "### 🟡 Changes recommended\n\n" +
		"<details open><summary><strong>1 open issue</strong></summary>\n\n" + item("11") + "\n</details>\n\n" +
		"<details open><summary><strong>2 open issues</strong></summary>\n\n" + item("12") + item("13") + "\n</details>\n"
	got, ok := parseCopilotOverview(body)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if len(got.Open) != 3 {
		t.Fatalf("Open = %d items, want 3: %+v", len(got.Open), got.Open)
	}
	for i, id := range []int{11, 12, 13} {
		if got.Open[i].CommentID != id {
			t.Errorf("Open[%d].CommentID = %d, want %d", i, got.Open[i].CommentID, id)
		}
	}
}

func TestParseCopilotOverview_WhatChangedGivesNoItem(t *testing.T) {
	body := "### 🟢 Approval recommended\n\n" +
		"<details><summary><strong>What changed in this PR</strong></summary>\n\n" +
		"- <picture><img alt=\"High severity\" src=\"x\"></picture> [Not an item](https://github.com/o/r/pull/1#discussion_r5)\n\n</details>\n"
	got, ok := parseCopilotOverview(body)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if n := len(got.Open) + len(got.Missed) + len(got.Resolved); n != 0 {
		t.Errorf("items = %d, want 0: %+v", n, got)
	}
}

func TestParseCopilotOverview_ItemWithoutIcon(t *testing.T) {
	body := "### 🟡 Changes recommended\n\n" +
		"<details open><summary><strong>1 open issue</strong></summary>\n\n" +
		"- [No icon here](https://github.com/o/r/pull/1#discussion_r77)\n\n</details>\n"
	got, _ := parseCopilotOverview(body)
	want := []OverviewItem{{Title: "No icon here", Severity: "", CommentID: 77}}
	if !reflect.DeepEqual(got.Open, want) {
		t.Errorf("Open = %+v, want %+v", got.Open, want)
	}
}

func TestParseCopilotOverview_MissedItemWithoutPath(t *testing.T) {
	body := "### 🟡 Changes recommended\n\n" +
		"<details><summary><strong>1 previously missed issue</strong></summary>\n\n" +
		"<details><summary><picture><img alt=\"Medium severity\" src=\"x\"></picture> No path item</summary>\n\n" +
		"Only a paragraph.\n\n</details>\n\n</details>\n"
	got, _ := parseCopilotOverview(body)
	want := []OverviewItem{{Title: "No path item", Severity: "medium", Body: "Only a paragraph."}}
	if !reflect.DeepEqual(got.Missed, want) {
		t.Errorf("Missed = %+v, want %+v", got.Missed, want)
	}
}

func TestParseCopilotOverview_TwoMissedItems(t *testing.T) {
	body := "### 🟡 Changes recommended\n\n" +
		"<details><summary><strong>2 previously missed issues</strong></summary>\n\n" +
		"<details><summary><picture><img alt=\"High severity\" src=\"x\"></picture> First</summary>\n\n`a.go:1`\n\nOne.\n\n</details>\n\n" +
		"<details><summary><picture><img alt=\"Low severity\" src=\"x\"></picture> Second</summary>\n\n`b/c.go:22`\n\nTwo.\n\n</details>\n\n</details>\n"
	got, _ := parseCopilotOverview(body)
	want := []OverviewItem{
		{Title: "First", Severity: "high", Path: "a.go", Line: 1, Body: "One."},
		{Title: "Second", Severity: "low", Path: "b/c.go", Line: 22, Body: "Two."},
	}
	if !reflect.DeepEqual(got.Missed, want) {
		t.Errorf("Missed = %+v, want %+v", got.Missed, want)
	}
}
