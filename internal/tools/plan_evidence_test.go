package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// evidenceTestFixture creates a git repo with one plan run and returns the
// root and the run ID.
func evidenceTestFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	initGitFixture(t, root)
	gitCommit(t, root, "initial")
	out, err := planPrepareCore(root, root, PlanPrepareIn{SkipConfigCheck: true, UserPrompt: "add evidence"})
	if err != nil {
		t.Fatalf("planPrepareCore (seed): %v", err)
	}
	if out.RunID == "" {
		t.Fatal("planPrepareCore returned an empty runId")
	}
	return root, out.RunID
}

func evidenceSetClock(t *testing.T, now time.Time) {
	t.Helper()
	orig := evidenceNow
	evidenceNow = func() time.Time { return now }
	t.Cleanup(func() { evidenceNow = orig })
}

func evidenceMustCall(t *testing.T, root string, in PlanSupportIn) PlanSupportOut {
	t.Helper()
	out, err := planSupportCore(root, root, in)
	if err != nil {
		t.Fatalf("planSupportCore(%s): %v", in.Action, err)
	}
	return out
}

func evidenceWriterPath(root, runID, writerID string) string {
	return filepath.Join(state.EvidenceDir(root, runID), writerID+".json")
}

func evidenceWriteRaw(t *testing.T, root, runID, writerID string, wf evidenceWriterFile) {
	t.Helper()
	dir := state.EvidenceDir(root, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(wf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidenceWriterPath(root, runID, writerID), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func evidenceReadFile(t *testing.T, root, runID, writerID string) evidenceWriterFile {
	t.Helper()
	raw, err := os.ReadFile(evidenceWriterPath(root, runID, writerID))
	if err != nil {
		t.Fatalf("read writer file: %v", err)
	}
	var wf evidenceWriterFile
	if err := json.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("decode writer file: %v", err)
	}
	return wf
}

// evidenceSnapshotRuns returns every file under .sdlc-v2/runs with its bytes.
func evidenceSnapshotRuns(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	base := filepath.Join(root, paths.DataDir, "runs")
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatalf("snapshot read %s: %v", p, rerr)
		}
		snap[p] = string(b)
		return nil
	})
	return snap
}

// evidenceRender calls plan_support over an in-memory MCP session from
// inside root and returns the rendered markdown text.
func evidenceRender(t *testing.T, root string, args map[string]any) string {
	t.Helper()
	t.Chdir(root)
	s := mcpserver.New("test", "0.0.0-test")
	RegisterPlanSupportTools(s)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "plan_support", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("CallTool: no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	if res.IsError {
		t.Fatalf("CallTool returned an error result:\n%s", text.Text)
	}
	return text.Text
}

// ---------------------------------------------------------------------------
// Round trip, upsert, status
// ---------------------------------------------------------------------------

func TestEvidence_RoundTrip_DigestHasNoBody(t *testing.T) {
	root, runID := evidenceTestFixture(t)

	rec := evidenceMustCall(t, root, PlanSupportIn{
		Action: "evidence_record", RunID: runID, WriterID: "explore-auth-flow", Status: "done",
		Items: []EvidenceItem{
			{ID: "F-auth-1", Summary: "token check skips expiry", Ref: "internal/auth.go:42", Body: "BODY-SECRET-ONE"},
			{ID: "F-auth-2", Summary: "refresh path unguarded", Body: "BODY-SECRET-TWO"},
		},
	})
	if rec.Record == nil || rec.Record.ItemCount != 2 || rec.Record.Status != "done" || rec.Record.WriterID != "explore-auth-flow" {
		t.Fatalf("record = %+v", rec.Record)
	}
	if rec.Record.FileBytes <= 0 {
		t.Errorf("record.fileBytes = %d, want > 0", rec.Record.FileBytes)
	}
	if info, err := os.Stat(evidenceWriterPath(root, runID, "explore-auth-flow")); err != nil || int(info.Size()) != rec.Record.FileBytes {
		t.Errorf("writer file size mismatch: stat=%v err=%v fileBytes=%d", info, err, rec.Record.FileBytes)
	}
	if want := "Stored 2 items for explore-auth-flow (status done). Continue your step."; rec.Next != want {
		t.Errorf("record next = %q, want %q", rec.Next, want)
	}

	dig := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID})
	if dig.Digest == nil || !strings.Contains(dig.Digest.Index, "| F-auth-1 | explore-auth-flow | internal/auth.go:42 | token check skips expiry |") {
		t.Fatalf("digest index missing F-auth-1 row: %+v", dig.Digest)
	}
	if !strings.Contains(dig.Digest.Index, "| F-auth-2 | explore-auth-flow | — | refresh path unguarded |") {
		t.Errorf("digest index missing F-auth-2 row:\n%s", dig.Digest.Index)
	}
	if dig.Digest.UserPrompt != "add evidence" {
		t.Errorf("digest.userPrompt = %q", dig.Digest.UserPrompt)
	}
	if want := filepath.Join(state.EvidenceDir(root, runID), "guardrails.md"); dig.Digest.GuardrailsFile != want {
		t.Errorf("digest.guardrailsFile = %q, want %q", dig.Digest.GuardrailsFile, want)
	}

	rendered := evidenceRender(t, root, map[string]any{"action": "evidence_digest", "runId": runID})
	if strings.Contains(rendered, "BODY-SECRET") {
		t.Errorf("rendered digest contains an item body:\n%s", rendered)
	}

	get := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_get", RunID: runID, IDs: []string{"F-auth-1", "F-auth-2"}})
	if get.Get == nil || !strings.Contains(get.Get.Evidence, "BODY-SECRET-ONE") || !strings.Contains(get.Get.Evidence, "BODY-SECRET-TWO") {
		t.Fatalf("get.evidence missing bodies: %+v", get.Get)
	}
	wantBlock := "### F-auth-1 — explore-auth-flow\n- ref: internal/auth.go:42\n- summary: token check skips expiry\n\nBODY-SECRET-ONE\n"
	if !strings.Contains(get.Get.Evidence, wantBlock) {
		t.Errorf("get.evidence block shape wrong:\n%s", get.Get.Evidence)
	}
	if len(get.Get.NotFound) != 0 {
		t.Errorf("get.notFound = %v, want empty", get.Get.NotFound)
	}
	if want := "Bodies returned for 2 items."; get.Next != want {
		t.Errorf("get next = %q, want %q", get.Next, want)
	}
}

func TestEvidence_Upsert_KeepsFirstPositionWithNewerContent(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	evidenceMustCall(t, root, PlanSupportIn{
		Action: "evidence_record", RunID: runID, WriterID: "w1",
		Items: []EvidenceItem{{ID: "F-x-1", Summary: "old", Body: "old"}, {ID: "F-x-0", Summary: "kept"}},
	})
	evidenceMustCall(t, root, PlanSupportIn{
		Action: "evidence_record", RunID: runID, WriterID: "w1",
		Items: []EvidenceItem{{ID: "F-x-1", Summary: "new"}, {ID: "F-x-2", Summary: "added"}},
	})
	got := evidenceReadFile(t, root, runID, "w1").Items
	want := []EvidenceItem{{ID: "F-x-1", Summary: "new"}, {ID: "F-x-0", Summary: "kept"}, {ID: "F-x-2", Summary: "added"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("items = %+v, want %+v", got, want)
	}
}

func TestEvidence_StatusTransitions(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	cases := []struct {
		name   string
		stored string // "", running, done, unreadable
		input  string
		want   string
	}{
		{"no file, omitted", "", "", "running"},
		{"no file, running", "", "running", "running"},
		{"no file, done", "", "done", "done"},
		{"running, omitted", "running", "", "running"},
		{"running, done", "running", "done", "done"},
		{"done, omitted", "done", "", "done"},
		{"done, running", "done", "running", "running"},
		{"unreadable, omitted", "unreadable", "", "running"},
		{"unreadable, running", "unreadable", "running", "running"},
		{"unreadable, done", "unreadable", "done", "done"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writer := fmt.Sprintf("w-%d", i)
			switch tc.stored {
			case "running", "done":
				evidenceWriteRaw(t, root, runID, writer, evidenceWriterFile{WriterID: writer, Status: tc.stored, UpdatedAt: "2026-01-01T00:00:00Z", Items: []EvidenceItem{}})
			case "unreadable":
				if err := os.WriteFile(evidenceWriterPath(root, runID, writer), []byte("{not json"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: writer, Status: tc.input})
			if out.Record.Status != tc.want {
				t.Errorf("record.status = %q, want %q", out.Record.Status, tc.want)
			}
			if got := evidenceReadFile(t, root, runID, writer).Status; got != tc.want {
				t.Errorf("stored status = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Brief
// ---------------------------------------------------------------------------

func TestEvidence_Brief_MainOnly_AndDigestBriefPath(t *testing.T) {
	root, runID := evidenceTestFixture(t)

	before := evidenceRender(t, root, map[string]any{"action": "evidence_digest", "runId": runID})
	if !strings.Contains(before, "- briefPath: (none)") {
		t.Errorf("digest before brief: want '- briefPath: (none)':\n%s", before)
	}

	out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "main", Brief: "# Discovery Brief\n"})
	wantPath := filepath.Join(state.EvidenceDir(root, runID), "brief.md")
	if out.Record.BriefPath != wantPath {
		t.Errorf("record.briefPath = %q, want %q", out.Record.BriefPath, wantPath)
	}
	if b, err := os.ReadFile(wantPath); err != nil || string(b) != "# Discovery Brief\n" {
		t.Errorf("brief.md = %q, err %v", b, err)
	}

	after := evidenceRender(t, root, map[string]any{"action": "evidence_digest", "runId": runID})
	// The MCP call resolves the repo root from the working directory, so the
	// path may carry resolved symlinks (for example /private/var on macOS).
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	wantRendered := filepath.Join(state.EvidenceDir(resolvedRoot, runID), "brief.md")
	if !strings.Contains(after, "- briefPath: "+wantRendered) {
		t.Errorf("digest after brief: want briefPath %s:\n%s", wantRendered, after)
	}
	direct := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID})
	if direct.Digest.BriefPath != wantPath {
		t.Errorf("digest.briefPath = %q, want %q", direct.Digest.BriefPath, wantPath)
	}

	_, err = planSupportCore(root, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "explore-x", Brief: "# b"})
	var de *mcpserver.DomainError
	if !errors.As(err, &de) || de.Msg != "brief is accepted only for writerId main" {
		t.Errorf("brief with non-main writer: err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Missing and stalled writers
// ---------------------------------------------------------------------------

func TestEvidence_Digest_MissingAndStalledTable(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	evidenceSetClock(t, now)
	ago := func(s int) string { return now.Add(-time.Duration(s) * time.Second).Format(time.RFC3339) }

	evidenceWriteRaw(t, root, runID, "lane-content-coverage-r1", evidenceWriterFile{WriterID: "lane-content-coverage-r1", Status: "running", UpdatedAt: ago(2000), Items: []EvidenceItem{}})
	evidenceWriteRaw(t, root, runID, "lane-guardrail-compliance-r1", evidenceWriterFile{WriterID: "lane-guardrail-compliance-r1", Status: "done", UpdatedAt: ago(5000), Items: []EvidenceItem{}})
	evidenceWriteRaw(t, root, runID, "main", evidenceWriterFile{WriterID: "main", Status: "running", UpdatedAt: ago(5000), Items: []EvidenceItem{}})
	evidenceWriteRaw(t, root, runID, "explore-auth-flow", evidenceWriterFile{WriterID: "explore-auth-flow", Status: "running", UpdatedAt: ago(9000), Items: []EvidenceItem{}})

	expected := []string{"lane-static-structural-r1", "lane-content-coverage-r1", "lane-guardrail-compliance-r1"}
	out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID, ExpectedWriters: expected})
	w := out.Writers
	if fmt.Sprint(w.MissingWriters) != "[lane-static-structural-r1]" {
		t.Errorf("missingWriters = %v", w.MissingWriters)
	}
	if fmt.Sprint(w.StalledWriters) != "[lane-content-coverage-r1]" {
		t.Errorf("stalledWriters = %v", w.StalledWriters)
	}
	for _, row := range []string{
		"| lane-content-coverage-r1 | running | 0 | " + ago(2000) + " | yes |",
		"| lane-guardrail-compliance-r1 | done | 0 | " + ago(5000) + " | no |",
		"| main | running | 0 | " + ago(5000) + " | no |",
		"| explore-auth-flow | running | 0 | " + ago(9000) + " | no |",
	} {
		if !strings.Contains(w.Table, row) {
			t.Errorf("table missing row %q:\n%s", row, w.Table)
		}
	}
	if strings.Contains(w.Table, "lane-static-structural-r1") {
		t.Errorf("missing writer must have no table row:\n%s", w.Table)
	}
	if !strings.HasPrefix(out.Next, "If the sdlc:plan skill instructions are not in context, ") {
		t.Errorf("full digest next must start with the skill re-invoke sentence: %q", out.Next)
	}
	if !strings.HasSuffix(out.Next, "Missing: lane-static-structural-r1. Stalled: lane-content-coverage-r1. Wait one poll cycle (evidence_digest statusOnly); if a writer is still listed, re-dispatch it or force-progress past it.") {
		t.Errorf("full digest next lagging suffix wrong: %q", out.Next)
	}

	// main running for 5000 s is stalled only when main is expected.
	out = evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID, ExpectedWriters: []string{"main"}})
	if fmt.Sprint(out.Writers.StalledWriters) != "[main]" {
		t.Errorf("stalled with main expected = %v, want [main]", out.Writers.StalledWriters)
	}

	// Neither input nor checkpoint: both lists empty.
	out = evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID})
	if len(out.Writers.MissingWriters) != 0 || len(out.Writers.StalledWriters) != 0 {
		t.Errorf("no expected set: missing=%v stalled=%v, want both empty", out.Writers.MissingWriters, out.Writers.StalledWriters)
	}

	// timeoutSeconds widens the stall window.
	out = evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID, ExpectedWriters: expected, TimeoutSeconds: 3000})
	if len(out.Writers.StalledWriters) != 0 {
		t.Errorf("timeoutSeconds 3000: stalled = %v, want empty", out.Writers.StalledWriters)
	}
}

func TestEvidence_Digest_ExpectedWritersFromCheckpoint(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	if _, err := planMark(root, root, PlanMarkIn{
		Marker: "checkpoint",
		Data:   map[string]any{"step": "6.5", "iteration": float64(2), "expectedWriters": []any{"lane-static-structural-r1"}},
	}); err != nil {
		t.Fatalf("planMark(checkpoint): %v", err)
	}
	out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID})
	if fmt.Sprint(out.Writers.MissingWriters) != "[lane-static-structural-r1]" {
		t.Errorf("missingWriters = %v, want checkpoint's expected writer", out.Writers.MissingWriters)
	}
	if out.Digest.Checkpoint == nil || out.Digest.Checkpoint.Step != "6.5" || out.Digest.Checkpoint.Iteration != 2 {
		t.Errorf("digest.checkpoint = %+v", out.Digest.Checkpoint)
	}
	if !strings.Contains(out.Next, "continue at step 6.5 (iteration 2)") {
		t.Errorf("next = %q, want step 6.5 iteration 2", out.Next)
	}
	if !strings.HasPrefix(out.Summary, runID+" — step 6.5, iteration 2; writers: 0 done, 0 running, 1 missing, 0 stalled; 0 items; 0 custom instructions.") {
		t.Errorf("summary = %q", out.Summary)
	}
}

// ---------------------------------------------------------------------------
// Rendering: empty raw markers, statusOnly, pipes, cap
// ---------------------------------------------------------------------------

func TestEvidence_EmptyRawFieldMarkers(t *testing.T) {
	root, runID := evidenceTestFixture(t)

	r := evidenceRender(t, root, map[string]any{"action": "evidence_digest", "runId": runID})
	if !strings.Contains(r, "- table: (no writers recorded)") {
		t.Errorf("want '- table: (no writers recorded)':\n%s", r)
	}

	evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "w1"})
	r = evidenceRender(t, root, map[string]any{"action": "evidence_digest", "runId": runID})
	if !strings.Contains(r, "- index: (no items recorded)") {
		t.Errorf("want '- index: (no items recorded)':\n%s", r)
	}

	r = evidenceRender(t, root, map[string]any{"action": "evidence_get", "runId": runID, "ids": []string{"F-none"}})
	if !strings.Contains(r, "- evidence: (no items found)") {
		t.Errorf("want '- evidence: (no items found)':\n%s", r)
	}
}

func TestEvidence_StatusOnly_WritersSectionOnly(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	evidenceSetClock(t, now)
	evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "lane-a", Status: "done"})
	evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "lane-b"})

	r := evidenceRender(t, root, map[string]any{"action": "evidence_digest", "runId": runID, "statusOnly": true, "expectedWriters": []string{"lane-a", "lane-b"}})
	if !strings.Contains(r, "## Summary") || !strings.Contains(r, "**Next:**") || !strings.Contains(r, "## writers") {
		t.Errorf("statusOnly render missing summary/next/writers:\n%s", r)
	}
	if strings.Contains(r, "## digest") {
		t.Errorf("statusOnly render must not contain the digest section:\n%s", r)
	}

	cases := []struct {
		name     string
		expected []string
		want     string
	}{
		{"all done", []string{"lane-a"}, "All expected writers are done. Fetch their results with evidence_get writerIds."},
		{"others running", []string{"lane-a", "lane-b"}, "Poll again in about 60 seconds."},
		{"missing", []string{"lane-a", "lane-c"}, "Missing: lane-c. Stalled: (none). Wait one more poll cycle; if a writer is still listed, force-progress past it (SKILL.md POLL step)."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID, StatusOnly: true, ExpectedWriters: tc.expected})
			if out.Digest != nil {
				t.Errorf("statusOnly digest = %+v, want nil", out.Digest)
			}
			if out.Next != tc.want {
				t.Errorf("next = %q, want %q", out.Next, tc.want)
			}
		})
	}

	// Stalled writer in statusOnly: wait-one-cycle text, never re-dispatch.
	evidenceSetClock(t, now.Add(2000*time.Second))
	out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID, StatusOnly: true, ExpectedWriters: []string{"lane-b"}})
	if want := "Missing: (none). Stalled: lane-b. Wait one more poll cycle; if a writer is still listed, force-progress past it (SKILL.md POLL step)."; out.Next != want {
		t.Errorf("stalled statusOnly next = %q, want %q", out.Next, want)
	}
	if strings.Contains(out.Next, "re-dispatch") {
		t.Errorf("statusOnly next must not name re-dispatch: %q", out.Next)
	}
}

func TestEvidence_Index_EscapesPipes(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	evidenceMustCall(t, root, PlanSupportIn{
		Action: "evidence_record", RunID: runID, WriterID: "w1",
		Items: []EvidenceItem{{ID: "F-1", Summary: "a | b", Ref: "x|y"}},
	})
	out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID})
	if want := `| F-1 | w1 | x\|y | a \| b |`; !strings.Contains(out.Digest.Index, want) {
		t.Errorf("index missing escaped row %q:\n%s", want, out.Digest.Index)
	}
}

func TestEvidence_Index_CapsAt200Rows(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	mk := func(prefix string, n int) []EvidenceItem {
		items := make([]EvidenceItem, n)
		for i := range items {
			items[i] = EvidenceItem{ID: fmt.Sprintf("%s-%d", prefix, i), Summary: "s"}
		}
		return items
	}
	evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "wa", Items: mk("A", 150)})
	evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "wb", Items: mk("B", 51)})
	out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID})
	lines := strings.Split(out.Digest.Index, "\n")
	// header + separator + 200 rows + more-row
	if len(lines) != 203 {
		t.Fatalf("index has %d lines, want 203", len(lines))
	}
	if want := "| … | — | — | 1 more; use evidence_get writerIds |"; lines[len(lines)-1] != want {
		t.Errorf("last index row = %q, want %q", lines[len(lines)-1], want)
	}
}

func TestEvidence_Digest_InstructionsReadFresh(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID})
	if len(out.Digest.Instructions) != 0 {
		t.Fatalf("instructions before local.toml = %v", out.Digest.Instructions)
	}
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[planStyle]\ninstructions = [\"Cite file:line.\", \"Ask before adding a dependency.\"]\n")
	out = evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID})
	if fmt.Sprint(out.Digest.Instructions) != "[Cite file:line. Ask before adding a dependency.]" {
		t.Errorf("instructions after local.toml = %v", out.Digest.Instructions)
	}
	if !strings.Contains(out.Summary, "; 2 custom instructions.") {
		t.Errorf("summary = %q, want 2 custom instructions", out.Summary)
	}
}

// ---------------------------------------------------------------------------
// evidence_get
// ---------------------------------------------------------------------------

func TestEvidence_Get_NotFoundAndKnown(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "w1", Items: []EvidenceItem{{ID: "F-1", Summary: "s", Body: "body one"}}})
	out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_get", RunID: runID, IDs: []string{"F-1", "F-missing"}, WriterIDs: []string{"w-missing"}})
	if !strings.Contains(out.Get.Evidence, "body one") {
		t.Errorf("known id not returned:\n%s", out.Get.Evidence)
	}
	if fmt.Sprint(out.Get.NotFound) != "[F-missing w-missing]" {
		t.Errorf("notFound = %v", out.Get.NotFound)
	}
	if want := "Not found: F-missing, w-missing. Check the digest index for valid ids."; out.Next != want {
		t.Errorf("next = %q, want %q", out.Next, want)
	}
}

func TestEvidence_Get_SameIDInTwoWriters(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "zeta", Items: []EvidenceItem{{ID: "R1", Summary: "z", Body: "from zeta"}}})
	evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "alpha", Items: []EvidenceItem{{ID: "R1", Summary: "a", Body: "from alpha"}}})
	out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_get", RunID: runID, IDs: []string{"R1"}})
	a := strings.Index(out.Get.Evidence, "### R1 — alpha")
	z := strings.Index(out.Get.Evidence, "### R1 — zeta")
	if a < 0 || z < 0 || a > z {
		t.Errorf("want both headings in writer-name order (alpha, zeta):\n%s", out.Get.Evidence)
	}
}

func TestEvidence_Get_WriterIDs(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "w1", Items: []EvidenceItem{{ID: "A", Summary: "a"}, {ID: "B", Summary: "b"}}})
	out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_get", RunID: runID, WriterIDs: []string{"w1"}})
	if !strings.Contains(out.Get.Evidence, "### A — w1") || !strings.Contains(out.Get.Evidence, "### B — w1") {
		t.Errorf("writerIds did not return every item:\n%s", out.Get.Evidence)
	}
	if out.Next != "Bodies returned for 2 items." {
		t.Errorf("next = %q", out.Next)
	}
}

// ---------------------------------------------------------------------------
// Corrupt writer file
// ---------------------------------------------------------------------------

func TestEvidence_CorruptWriterFile_UnreadableThenReplaced(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	path := evidenceWriterPath(root, runID, "w1")
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_digest", RunID: runID})
	if !strings.Contains(out.Writers.Table, "| w1 | unreadable |") {
		t.Errorf("table missing unreadable row:\n%s", out.Writers.Table)
	}
	// w1 is not expected, so it is neither missing nor stalled — the
	// structured unreadableWriters field is the only signal for it.
	if got := out.Writers.UnreadableWriters; len(got) != 1 || got[0] != "w1" {
		t.Errorf("UnreadableWriters = %v, want [w1]", got)
	}
	if len(out.Writers.StalledWriters) != 0 || len(out.Writers.MissingWriters) != 0 {
		t.Errorf("stalled = %v, missing = %v, want both empty", out.Writers.StalledWriters, out.Writers.MissingWriters)
	}
	rec := evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "w1", Items: []EvidenceItem{{ID: "F-1", Summary: "s"}}})
	if !strings.Contains(rec.Summary, "Replaced the unreadable writer file "+path) {
		t.Errorf("summary does not report the replacement: %q", rec.Summary)
	}
	if got := evidenceReadFile(t, root, runID, "w1"); len(got.Items) != 1 || got.Status != "running" {
		t.Errorf("replaced file = %+v", got)
	}
}

// TestEvidence_Brief_WriteFailsAfterWriterFile isolates the second write of
// evidence_record: the writer file is written, then brief.md fails (it is a
// directory). The call returns an InfraError, the writer file already holds
// the new items, and a retry after the fault is cleared stores the brief
// without duplicating items (upsert by id).
func TestEvidence_Brief_WriteFailsAfterWriterFile(t *testing.T) {
	root, runID := evidenceTestFixture(t)
	briefPath := filepath.Join(state.EvidenceDir(root, runID), "brief.md")
	if err := os.MkdirAll(briefPath, 0o755); err != nil {
		t.Fatal(err)
	}
	in := PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "main", Brief: "# brief",
		Items: []EvidenceItem{{ID: "F-main-1", Summary: "s", Ref: "a.go:1"}}}

	_, err := planSupportCore(root, root, in)
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %T %v, want *mcpserver.InfraError", err, err)
	}
	if want := "evidence write failed: " + briefPath; ie.Msg != want {
		t.Errorf("msg = %q, want %q", ie.Msg, want)
	}
	if got := evidenceReadFile(t, root, runID, "main"); len(got.Items) != 1 || got.Items[0].ID != "F-main-1" {
		t.Errorf("writer file after failed brief write = %+v, want the new item", got)
	}

	if err := os.Remove(briefPath); err != nil {
		t.Fatal(err)
	}
	rec := evidenceMustCall(t, root, in)
	if rec.Record.BriefPath != briefPath || rec.Record.ItemCount != 1 {
		t.Errorf("retry record = %+v, want briefPath %s and 1 item", rec.Record, briefPath)
	}
}

// ---------------------------------------------------------------------------
// Error test matrix
// ---------------------------------------------------------------------------

func TestEvidence_ErrorMatrix(t *testing.T) {
	const validMissingRun = "plan-main-20000101T000000Z"
	many := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s-%d", prefix, i)
		}
		return out
	}
	manyItems := func(n int) []EvidenceItem {
		out := make([]EvidenceItem, n)
		for i := range out {
			out[i] = EvidenceItem{ID: fmt.Sprintf("F-%d", i), Summary: "s"}
		}
		return out
	}
	// enotdir replaces the run's evidence directory with a regular file.
	enotdir := func(t *testing.T, root, runID string) {
		dir := state.EvidenceDir(root, runID)
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir, []byte("not a dir"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runStatePath := func(root, runID string) string {
		return filepath.Join(planTestRunsDir(root), runID+".json")
	}
	// corruptRun overwrites the run's own state file with invalid JSON.
	corruptRun := func(t *testing.T, root, runID string) {
		if err := os.WriteFile(runStatePath(root, runID), []byte("{broken"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// badCheckpoint stores a checkpoint value that is not an object.
	badCheckpoint := func(t *testing.T, root, runID string) {
		p := runStatePath(root, runID)
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		data["checkpoint"] = "not-an-object"
		raw, err = json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	corruptMsg := func(_, runID string) string { return "plan run " + runID + " state read failed" }
	seedMain := func(t *testing.T, root, runID string) {
		evidenceMustCall(t, root, PlanSupportIn{Action: "evidence_record", RunID: runID, WriterID: "main", Items: []EvidenceItem{{ID: "F-0", Summary: "seed"}}})
	}

	type tc struct {
		name      string
		setup     func(t *testing.T, root, runID string)
		in        func(runID string) PlanSupportIn
		infra     bool
		msg       func(root, runID string) string // exact message; nil when msgPrefix is used
		msgPrefix func(root, runID string) string
	}
	exact := func(s string) func(string, string) string { return func(string, string) string { return s } }

	cases := []tc{
		// evidence_record
		{name: "record/runId empty", in: func(string) PlanSupportIn { return PlanSupportIn{Action: "evidence_record", WriterID: "w1"} },
			msg: exact("runId is required for evidence_record")},
		{name: "record/runId not found", in: func(string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: validMissingRun, WriterID: "w1"}
		}, msg: exact("plan run " + validMissingRun + " not found")},
		{name: "record/runId rejected", in: func(string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: "../x", WriterID: "w1"}
		},
			msg: exact("plan run ../x not found")},
		{name: "record/status paused", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "w1", Status: "paused"}
		}, msg: exact(`status "paused" is not valid`)},
		{name: "record/writerId invalid", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "a/b"}
		},
			msg: exact(`writerId "a/b" is invalid`)},
		{name: "record/items 201", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "w1", Items: manyItems(201)}
		}, msg: exact("items has 201 entries, max 200")},
		{name: "record/items[0].id invalid", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "w1", Items: []EvidenceItem{{ID: "-x", Summary: "s"}}}
		}, msg: exact(`items[0].id "-x" is invalid`)},
		{name: "record/summary too long", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "w1", Items: []EvidenceItem{{ID: "F-1", Summary: strings.Repeat("s", 201)}}}
		}, msg: exact("items[0].summary must be one line of at most 200 characters (got 201)")},
		{name: "record/summary multi-line", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "w1", Items: []EvidenceItem{{ID: "F-1", Summary: "a\nb"}}}
		}, msg: exact("items[0].summary must be one line of at most 200 characters (got 3)")},
		{name: "record/ref too long", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "w1", Items: []EvidenceItem{{ID: "F-1", Summary: "s", Ref: strings.Repeat("r", 501)}}}
		}, msg: exact("items[0].ref must be one line of at most 500 characters")},
		{name: "record/ref multi-line", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "w1", Items: []EvidenceItem{{ID: "F-1", Summary: "s", Ref: "a\nb"}}}
		}, msg: exact("items[0].ref must be one line of at most 500 characters")},
		{name: "record/brief non-main", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "w1", Brief: "# b"}
		}, msg: exact("brief is accepted only for writerId main")},
		{name: "record/brief too big", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "main", Brief: strings.Repeat("b", 65537)}
		}, msg: exact("brief is 65537 bytes, limit 65536")},
		{name: "record/writer file too big", setup: seedMain, in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "main", Items: []EvidenceItem{{ID: "F-1", Summary: "s", Body: strings.Repeat("x", 66000)}}}
		}, msgPrefix: exact("writer main evidence would be ")},
		{name: "record/OS error", setup: enotdir, infra: true, in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "w1"}
		}, msgPrefix: func(root, runID string) string { return "evidence read failed: " + state.EvidenceDir(root, runID) }},
		{name: "record/run state corrupt", setup: corruptRun, infra: true, in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_record", RunID: r, WriterID: "w1"}
		}, msg: corruptMsg},

		// evidence_digest
		{name: "digest/runId empty", in: func(string) PlanSupportIn { return PlanSupportIn{Action: "evidence_digest"} },
			msg: exact("runId is required for evidence_digest")},
		{name: "digest/runId not found", in: func(string) PlanSupportIn { return PlanSupportIn{Action: "evidence_digest", RunID: validMissingRun} },
			msg: exact("plan run " + validMissingRun + " not found")},
		{name: "digest/runId rejected", in: func(string) PlanSupportIn { return PlanSupportIn{Action: "evidence_digest", RunID: "../x"} },
			msg: exact("plan run ../x not found")},
		{name: "digest/expectedWriters[0] invalid", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_digest", RunID: r, ExpectedWriters: []string{"a/b"}}
		}, msg: exact(`expectedWriters[0] "a/b" is invalid`)},
		{name: "digest/expectedWriters 33", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_digest", RunID: r, ExpectedWriters: many("w", 33)}
		}, msg: exact("expectedWriters has 33 entries, max 32")},
		{name: "digest/timeoutSeconds 5", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_digest", RunID: r, TimeoutSeconds: 5}
		}, msg: exact("timeoutSeconds 5 is out of range 60-86400")},
		{name: "digest/OS error", setup: enotdir, infra: true, in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_digest", RunID: r}
		}, msg: func(root, runID string) string { return "evidence read failed: " + state.EvidenceDir(root, runID) }},
		{name: "digest/run state corrupt", setup: corruptRun, infra: true, in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_digest", RunID: r}
		}, msg: corruptMsg},
		{name: "digest/checkpoint malformed", setup: badCheckpoint, infra: true, in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_digest", RunID: r}
		}, msgPrefix: func(_, runID string) string { return "plan run " + runID + " has a malformed checkpoint in " }},

		// evidence_get
		{name: "get/runId empty", in: func(string) PlanSupportIn { return PlanSupportIn{Action: "evidence_get", IDs: []string{"F-1"}} },
			msg: exact("runId is required for evidence_get")},
		{name: "get/runId not found", in: func(string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_get", RunID: validMissingRun, IDs: []string{"F-1"}}
		}, msg: exact("plan run " + validMissingRun + " not found")},
		{name: "get/runId rejected", in: func(string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_get", RunID: "../x", IDs: []string{"F-1"}}
		}, msg: exact("plan run ../x not found")},
		{name: "get/writerIds[0] invalid", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_get", RunID: r, WriterIDs: []string{"a/b"}}
		}, msg: exact(`writerIds[0] "a/b" is invalid`)},
		{name: "get/writerIds 33", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_get", RunID: r, WriterIDs: many("w", 33)}
		}, msg: exact("writerIds has 33 entries, max 32")},
		{name: "get/ids 201", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_get", RunID: r, IDs: many("F", 201)}
		}, msg: exact("ids has 201 entries, max 200")},
		{name: "get/ids[0] invalid", in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_get", RunID: r, IDs: []string{"-x"}}
		}, msg: exact(`ids[0] "-x" is invalid`)},
		{name: "get/no ids and no writerIds", in: func(r string) PlanSupportIn { return PlanSupportIn{Action: "evidence_get", RunID: r} },
			msg: exact("evidence_get needs ids or writerIds")},
		{name: "get/OS error", setup: enotdir, infra: true, in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_get", RunID: r, IDs: []string{"F-1"}}
		}, msg: func(root, runID string) string { return "evidence read failed: " + state.EvidenceDir(root, runID) }},
		{name: "get/run state corrupt", setup: corruptRun, infra: true, in: func(r string) PlanSupportIn {
			return PlanSupportIn{Action: "evidence_get", RunID: r, IDs: []string{"F-1"}}
		}, msg: corruptMsg},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, runID := evidenceTestFixture(t)
			if c.setup != nil {
				c.setup(t, root, runID)
			}
			before := evidenceSnapshotRuns(t, root)

			_, err := planSupportCore(root, root, c.in(runID))
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			var msg, suggestion string
			if c.infra {
				var ie *mcpserver.InfraError
				if !errors.As(err, &ie) {
					t.Fatalf("err = %T %v, want *mcpserver.InfraError", err, err)
				}
				msg, suggestion = ie.Msg, ie.Suggestion
				if ie.Cause == nil {
					t.Error("InfraError.Cause is nil, want the OS error")
				}
			} else {
				var de *mcpserver.DomainError
				if !errors.As(err, &de) {
					t.Fatalf("err = %T %v, want *mcpserver.DomainError", err, err)
				}
				msg, suggestion = de.Msg, de.Suggestion
			}
			if c.msg != nil {
				if want := c.msg(root, runID); msg != want {
					t.Errorf("msg = %q, want %q", msg, want)
				}
			} else if want := c.msgPrefix(root, runID); !strings.HasPrefix(msg, want) {
				t.Errorf("msg = %q, want prefix %q", msg, want)
			}
			if strings.TrimSpace(suggestion) == "" {
				t.Error("suggestion is empty")
			}

			after := evidenceSnapshotRuns(t, root)
			if fmt.Sprint(before) != fmt.Sprint(after) {
				t.Errorf("files under .sdlc-v2/runs changed on error:\nbefore keys=%d after keys=%d", len(before), len(after))
			}
		})
	}
}
