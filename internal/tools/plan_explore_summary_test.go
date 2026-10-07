package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// The run ID below is a fixed name. planExploreSummary does not read the run
// state file, only the evidence folder, so no plan run is needed.
const exploreSummaryTestRun = "plan-main-20261007T205636Z"

func exploreSummaryItems(n int) []EvidenceItem {
	items := make([]EvidenceItem, n)
	for i := range items {
		items[i] = EvidenceItem{
			ID:      fmt.Sprintf("F-%d", i+1),
			Summary: fmt.Sprintf("finding %d", i+1),
			Ref:     fmt.Sprintf("pkg/file.go:%d", i+1),
			Body:    fmt.Sprintf("body of finding %d", i+1),
		}
	}
	return items
}

func TestPlanExploreSummary_MissingFolder_EmptyNonNilNoError(t *testing.T) {
	root := t.TempDir()
	got, err := planExploreSummary(root, exploreSummaryTestRun)
	if err != nil {
		t.Fatalf("planExploreSummary: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want an empty non-nil list")
	}
	if len(got) != 0 {
		t.Fatalf("got %d entries, want 0", len(got))
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Errorf("JSON = %s, want []", raw)
	}
}

func TestPlanExploreSummary_ListsOnlyExplorers_SortedByName(t *testing.T) {
	root := t.TempDir()
	write := func(id, status string, n int) {
		evidenceWriteRaw(t, root, exploreSummaryTestRun, id, evidenceWriterFile{
			WriterID: id, Status: status, Items: exploreSummaryItems(n),
		})
	}
	write("explore-zeta", "running", 1)
	write("explore-auth-flow", "done", 2)
	write("explore-middle", "done", 0)
	write("main", "done", 3)
	write("lane-static-structural-r1", "done", 3)
	write("lens-risk-r1", "done", 3)
	write("reviewer-r1", "done", 3)
	// Files that share the folder but are not writer files.
	dir := state.EvidenceDir(root, exploreSummaryTestRun)
	for _, name := range []string{"brief.md", "guardrails.md", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := planExploreSummary(root, exploreSummaryTestRun)
	if err != nil {
		t.Fatalf("planExploreSummary: %v", err)
	}
	type row struct {
		name, status string
		total, top   int
	}
	var rows []row
	for _, e := range got {
		rows = append(rows, row{e.Name, e.Status, e.Total, len(e.Top)})
	}
	want := []row{
		{"auth-flow", "done", 2, 2},
		{"middle", "done", 0, 0},
		{"zeta", "running", 1, 1},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("entries = %+v, want %+v", rows, want)
	}
}

func TestPlanExploreSummary_CapsTopKeepsTotal_NoBody(t *testing.T) {
	root := t.TempDir()
	evidenceWriteRaw(t, root, exploreSummaryTestRun, "explore-auth-flow", evidenceWriterFile{
		WriterID: "explore-auth-flow", Status: "done", Items: exploreSummaryItems(12),
	})

	got, err := planExploreSummary(root, exploreSummaryTestRun)
	if err != nil {
		t.Fatalf("planExploreSummary: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	e := got[0]
	if e.Total != 12 {
		t.Errorf("Total = %d, want 12", e.Total)
	}
	if len(e.Top) != exploreSummaryTop {
		t.Fatalf("len(Top) = %d, want %d", len(e.Top), exploreSummaryTop)
	}
	for i, it := range e.Top {
		want := ExploreSummaryItem{Summary: fmt.Sprintf("finding %d", i+1), Ref: fmt.Sprintf("pkg/file.go:%d", i+1)}
		if it != want {
			t.Errorf("Top[%d] = %+v, want %+v", i, it, want)
		}
	}

	// The JSON shape is the one the ship cleanup stores and the dashboard readers parse.
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "body") {
		t.Errorf("JSON carries a body: %s", raw)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	if len(generic) != 4 {
		t.Errorf("JSON keys = %v, want name, status, total, top", generic)
	}
	for _, k := range []string{"name", "status", "total", "top"} {
		if _, ok := generic[k]; !ok {
			t.Errorf("JSON misses key %q: %s", k, raw)
		}
	}
	if !strings.HasPrefix(string(raw), `{"name":"auth-flow","status":"done","total":12,"top":[{"summary":"finding 1","ref":"pkg/file.go:1"}`) {
		t.Errorf("JSON = %s", raw)
	}
}

func TestPlanExploreSummary_ExactlyCapItems_AllKept(t *testing.T) {
	root := t.TempDir()
	evidenceWriteRaw(t, root, exploreSummaryTestRun, "explore-a", evidenceWriterFile{
		WriterID: "explore-a", Status: "running", Items: exploreSummaryItems(exploreSummaryTop),
	})
	got, err := planExploreSummary(root, exploreSummaryTestRun)
	if err != nil {
		t.Fatalf("planExploreSummary: %v", err)
	}
	if len(got) != 1 || got[0].Total != exploreSummaryTop || len(got[0].Top) != exploreSummaryTop {
		t.Errorf("got %+v, want total and top both %d", got, exploreSummaryTop)
	}
}

func TestPlanExploreSummary_EmptyEntry_TopIsEmptyListNotNull(t *testing.T) {
	root := t.TempDir()
	// A nil Items slice is written as "items":null.
	evidenceWriteRaw(t, root, exploreSummaryTestRun, "explore-empty", evidenceWriterFile{
		WriterID: "explore-empty", Status: "running",
	})
	got, err := planExploreSummary(root, exploreSummaryTestRun)
	if err != nil {
		t.Fatalf("planExploreSummary: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	e := got[0]
	if e.Status != "running" || e.Total != 0 {
		t.Errorf("entry = %+v, want running with total 0", e)
	}
	if e.Top == nil {
		t.Fatal("Top is nil, want an empty list")
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"name":"empty","status":"running","total":0,"top":[]}`; string(raw) != want {
		t.Errorf("JSON = %s, want %s", raw, want)
	}
}

func TestPlanExploreSummary_UnreadableFile_UnreadableEntry(t *testing.T) {
	root := t.TempDir()
	evidenceWriteRaw(t, root, exploreSummaryTestRun, "explore-good", evidenceWriterFile{
		WriterID: "explore-good", Status: "done", Items: exploreSummaryItems(1),
	})
	bad := evidenceWriterPath(root, exploreSummaryTestRun, "explore-broken")
	if err := os.WriteFile(bad, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := planExploreSummary(root, exploreSummaryTestRun)
	if err != nil {
		t.Fatalf("planExploreSummary: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Name != "broken" || got[1].Name != "good" {
		t.Fatalf("names = %q, %q, want broken, good", got[0].Name, got[1].Name)
	}
	b := got[0]
	if b.Status != "unreadable" || b.Total != 0 {
		t.Errorf("broken entry = %+v, want unreadable with total 0", b)
	}
	if b.Top == nil || len(b.Top) != 0 {
		t.Errorf("broken Top = %#v, want an empty non-nil list", b.Top)
	}
	if got[1].Status != "done" || got[1].Total != 1 {
		t.Errorf("good entry = %+v, want done with total 1", got[1])
	}
}

func TestPlanExploreSummary_StatusOtherThanDone_IsRunning(t *testing.T) {
	root := t.TempDir()
	// Valid JSON with no status field: the writer never reported done.
	path := evidenceWriterPath(root, exploreSummaryTestRun, "explore-nostatus")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"writerId":"explore-nostatus"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := planExploreSummary(root, exploreSummaryTestRun)
	if err != nil {
		t.Fatalf("planExploreSummary: %v", err)
	}
	if len(got) != 1 || got[0].Status != "running" {
		t.Errorf("got %+v, want one running entry", got)
	}
}

func TestPlanExploreSummary_ReadError_ReturnsInfraError(t *testing.T) {
	root := t.TempDir()
	// The evidence path is a regular file, so os.ReadDir fails with an error
	// that is not "not exist".
	dir := state.EvidenceDir(root, exploreSummaryTestRun)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := planExploreSummary(root, exploreSummaryTestRun)
	if err == nil {
		t.Fatalf("err = nil, got %+v", got)
	}
	if got != nil {
		t.Errorf("got = %+v, want nil with an error", got)
	}
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %T %v, want *mcpserver.InfraError", err, err)
	}
	if !strings.Contains(ie.Msg, dir) {
		t.Errorf("InfraError.Msg = %q, want the evidence path %q", ie.Msg, dir)
	}
	if ie.Cause == nil {
		t.Error("InfraError.Cause is nil, want the OS error")
	}
}
