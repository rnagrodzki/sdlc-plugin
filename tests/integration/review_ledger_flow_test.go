//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// ghNoPRStubScript is a stand-in for the gh CLI. review_prepare's open-PR
// lookup runs `gh pr view`; this stub answers like gh does for a branch
// without a PR, so the lookup stays hermetic (no network, no auth).
const ghNoPRStubScript = `#!/bin/sh
echo "no pull requests found for branch" >&2
exit 1
`

// setupReviewLedgerClient registers review_prepare and execute_state on a
// fresh in-process MCP server/client pair, using the same recipe as
// setupShipClient.
func setupReviewLedgerClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	srv := mcpserver.New("review-ledger-flow-test", "0.0.0-test")
	tools.RegisterReviewTools(srv)
	tools.RegisterExecuteStateTools(srv)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := srv.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "review-ledger-flow-test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// reviewDimensionFile returns a review dimension file that matches every Go
// file.
func reviewDimensionFile(name string) string {
	return "---\nname: " + name + "\ndescription: " + name + " review\ntriggers:\n  - \"**/*.go\"\nseverity: medium\n---\nReview the code for " + name + " issues.\n"
}

// runMetaStopReasons reads run.meta of runID from disk and returns the
// stopReason of each planned dimension by workerId.
func runMetaStopReasons(t *testing.T, repo, runID string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, ".sdlc-v2", "runs", "ledger", runID, "run.meta"))
	if err != nil {
		t.Fatalf("read run.meta: %v", err)
	}
	var meta struct {
		Dimensions []struct {
			WorkerID   string `json:"workerId"`
			StopReason string `json:"stopReason"`
		} `json:"dimensions"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("parse run.meta: %v\n%s", err, raw)
	}
	got := map[string]string{}
	for _, d := range meta.Dimensions {
		got[d.WorkerID] = d.StopReason
	}
	return got
}

// TestReviewLedgerFlow_SkipRecordsStopReason drives ledger_skip through the
// real MCP tool surface against a scratch git repo: review_prepare writes
// run.meta for a review run with two dimensions, ledger_skip records stop
// reasons in it, and ledger_status output stays the same. It checks the
// ledger_skip result fields (workerId, stopReason, priorStopReason, the
// checked-out warning) and the run.meta bytes on disk.
func TestReviewLedgerFlow_SkipRecordsStopReason(t *testing.T) {
	const branch = "feat/review-ledger-flow"

	// --- gh stub on PATH (prepended, so git stays visible) ---
	binDir := t.TempDir()
	mustWriteFile(t, filepath.Join(binDir, "gh"), ghNoPRStubScript)
	if err := os.Chmod(filepath.Join(binDir, "gh"), 0o755); err != nil {
		t.Fatalf("chmod gh stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// --- repo: main with one commit, then a feature branch that adds Go files ---
	repo := realPath(t, t.TempDir())
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "checkout", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "integration-test@example.com")
	runGit(t, repo, "config", "user.name", "integration-test")
	mustWriteFile(t, filepath.Join(repo, ".gitignore"), ".sdlc-v2/\n")
	runGit(t, repo, "add", ".gitignore")
	runGit(t, repo, "commit", "-q", "-m", "init")
	runGit(t, repo, "checkout", "-q", "-b", branch)
	mustWriteFile(t, filepath.Join(repo, "src", "app.go"), "package main\n\nfunc main() {}\n")
	runGit(t, repo, "add", "src/app.go")
	runGit(t, repo, "commit", "-q", "-m", "add app")

	mustWriteFile(t, filepath.Join(repo, ".sdlc-v2", "config.toml"), "")
	for _, name := range []string{"code-quality", "security"} {
		mustWriteFile(t, filepath.Join(repo, ".sdlc-v2", "review-dimensions", name+".md"), reviewDimensionFile(name))
	}

	chdir(t, repo)
	c := setupReviewLedgerClient(t)

	// --- review_prepare: writes the manifest and run.meta ---
	prep := callTool(t, c, "review_prepare", map[string]any{
		"skipConfigCheck": true,
		"target":          "main",
	})
	if !prep.OK {
		t.Fatalf("review_prepare: not ok: code=%s body=%s", prep.Code, prep.Body)
	}
	manifestPath := renderedSection(prep.Body, "Fields")["manifestPath"]
	if manifestPath == "" {
		t.Fatalf("review_prepare: no manifestPath in body:\n%s", prep.Body)
	}
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if manifest.RunID == "" {
		t.Fatalf("manifest run_id is empty; review_prepare planned no waves\nbody:\n%s", prep.Body)
	}
	runID := manifest.RunID
	if got := runMetaStopReasons(t, repo, runID); len(got) != 2 || got["code-quality"] != "" || got["security"] != "" {
		t.Fatalf("run.meta stop reasons after review_prepare = %v, want code-quality and security with none set", got)
	}

	statusArgs := map[string]any{
		"action":          "ledger_status",
		"runId":           runID,
		"expectedWorkers": []string{"code-quality", "security"},
	}
	statusBefore := callTool(t, c, "execute_state", statusArgs)
	if !statusBefore.OK {
		t.Fatalf("ledger_status before: not ok: code=%s body=%s", statusBefore.Code, statusBefore.Body)
	}

	skip := func(workerID, reason string) map[string]string {
		t.Helper()
		res := callTool(t, c, "execute_state", map[string]any{
			"action": "ledger_skip", "runId": runID, "workerId": workerID, "reason": reason,
		})
		if !res.OK {
			t.Fatalf("ledger_skip %s %s: not ok: code=%s body=%s", workerID, reason, res.Code, res.Body)
		}
		if !strings.Contains(res.Body, "**Next:** Continue Step 3 of review.") {
			t.Errorf("ledger_skip %s %s: body has no Next line:\n%s", workerID, reason, res.Body)
		}
		return renderedSection(res.Body, "Fields")
	}

	// --- first skip: no prior reason, no warning ---
	first := skip("code-quality", "stalled")
	if first["workerId"] != "code-quality" || first["stopReason"] != "stalled" {
		t.Errorf("first ledger_skip fields = %v, want workerId=code-quality stopReason=stalled", first)
	}
	if _, ok := first["priorStopReason"]; ok {
		t.Errorf("first ledger_skip has priorStopReason %q, want none", first["priorStopReason"])
	}
	if _, ok := first["warnings"]; ok {
		t.Errorf("first ledger_skip has warnings, want none: %v", first)
	}

	// ledger_skip writes no worker file, so ledger_status does not change.
	statusAfter := callTool(t, c, "execute_state", statusArgs)
	if !statusAfter.OK || statusAfter.Body != statusBefore.Body {
		t.Errorf("ledger_status changed after ledger_skip:\nbefore:\n%s\nafter:\n%s", statusBefore.Body, statusAfter.Body)
	}

	// --- second skip of the same worker: replaces the reason, echoes the old one ---
	second := skip("code-quality", "missing")
	if second["priorStopReason"] != "stalled" {
		t.Errorf("second ledger_skip priorStopReason = %q, want stalled", second["priorStopReason"])
	}

	// --- skip of a worker that already checked out: warning ---
	for _, action := range []string{"ledger_checkin", "ledger_checkout"} {
		res := callTool(t, c, "execute_state", map[string]any{"action": action, "runId": runID, "workerId": "security"})
		if !res.OK {
			t.Fatalf("%s: not ok: code=%s body=%s", action, res.Code, res.Body)
		}
	}
	res := callTool(t, c, "execute_state", map[string]any{
		"action": "ledger_skip", "runId": runID, "workerId": "security", "reason": "unstopped",
	})
	if !res.OK {
		t.Fatalf("ledger_skip security: not ok: code=%s body=%s", res.Code, res.Body)
	}
	if !strings.Contains(res.Body, "worker already checked out; the dashboard shows it as done and ignores the stop reason") {
		t.Errorf("ledger_skip of a checked-out worker has no warning:\n%s", res.Body)
	}

	if got := runMetaStopReasons(t, repo, runID); got["code-quality"] != "missing" || got["security"] != "unstopped" {
		t.Errorf("run.meta stop reasons = %v, want code-quality=missing security=unstopped", got)
	}

	// --- a runId with no run.meta: domain error that points at the manifest run_id ---
	bad := callTool(t, c, "execute_state", map[string]any{
		"action": "ledger_skip", "runId": "review-unknown", "workerId": "security", "reason": "stalled",
	})
	if bad.OK || bad.Code != "domain" {
		t.Fatalf("ledger_skip unknown runId: ok=%v code=%s, want a domain error\n%s", bad.OK, bad.Code, bad.Body)
	}
	if !strings.Contains(bad.Body, "Pass the run_id from the review_prepare manifest of this review run.") {
		t.Errorf("ledger_skip unknown runId: Do this section does not name the manifest run_id:\n%s", bad.Body)
	}
}

// TestShipCommitCheckFlow drives ship_state commit-check through the real
// MCP tool surface against a scratch git repo. With the commit step
// in_progress, a dirty tree is staged and left for the commit agent; after
// the commit lands, a second call completes the commit step with a
// "committed <sha>" result. It checks the rendered output contract and the
// ship state on disk.
func TestShipCommitCheckFlow(t *testing.T) {
	dir := gitFixture(t, "feat/commit-check-flow")
	runGit(t, dir, "config", "user.email", "integration-test@example.com")
	runGit(t, dir, "config", "user.name", "integration-test")
	baseHead := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
	c := setupShipClient(t)

	prep := callTool(t, c, "ship_prepare", map[string]any{"sessionId": "commit-check-session"})
	if !prep.OK {
		t.Fatalf("ship_prepare: not ok: code=%s body=%s", prep.Code, prep.Body)
	}
	stateFile := renderedSection(prep.Body, "Fields")["stateFile"]
	if stateFile == "" {
		t.Fatalf("ship_prepare: no stateFile in body:\n%s", prep.Body)
	}

	// Complete every unconditional step before commit, then begin commit.
	foundCommit := false
	for _, s := range readShipSteps(t, c) {
		if s.Name == "commit" {
			foundCommit = true
			break
		}
		if s.HasCond {
			continue
		}
		for _, action := range []string{"begin-step", "complete-step"} {
			env := callTool(t, c, "ship_state", map[string]any{"action": action, "step": s.Name})
			if !env.OK {
				t.Fatalf("ship_state %s %q: not ok: code=%s body=%s", action, s.Name, env.Code, env.Body)
			}
		}
	}
	if !foundCommit {
		t.Fatal("ship state has no commit step")
	}
	if env := callTool(t, c, "ship_state", map[string]any{"action": "begin-step", "step": "commit"}); !env.OK {
		t.Fatalf("ship_state begin-step commit: not ok: code=%s body=%s", env.Code, env.Body)
	}

	// --- dirty tree: commit-check stages it and leaves the step open ---
	mustWriteFile(t, filepath.Join(dir, "feature.txt"), "feature\n")
	dirty := callTool(t, c, "ship_state", map[string]any{"action": "commit-check"})
	if !dirty.OK {
		t.Fatalf("commit-check dirty: not ok: code=%s body=%s", dirty.Code, dirty.Body)
	}
	if f := renderedSection(dirty.Body, "Fields"); f["clean"] != "false" || f["stagedCount"] != "1" || f["stepCompleted"] != "false" {
		t.Errorf("commit-check dirty fields = %v, want clean=false stagedCount=1 stepCompleted=false\n%s", f, dirty.Body)
	}
	if staged := strings.TrimSpace(runGit(t, dir, "diff", "--cached", "--name-only")); staged != "feature.txt" {
		t.Errorf("staged paths = %q, want feature.txt (and nothing under .sdlc-v2/)", staged)
	}

	// --- the commit lands: commit-check completes the step ---
	runGit(t, dir, "commit", "-q", "-m", "add feature")
	newHead := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
	done := callTool(t, c, "ship_state", map[string]any{"action": "commit-check"})
	if !done.OK {
		t.Fatalf("commit-check clean: not ok: code=%s body=%s", done.Code, done.Body)
	}
	f := renderedSection(done.Body, "Fields")
	if f["clean"] != "true" || f["stepCompleted"] != "true" {
		t.Errorf("commit-check clean fields = %v, want clean=true stepCompleted=true\n%s", f, done.Body)
	}
	if !strings.HasPrefix(f["result"], "committed ") || !strings.HasPrefix(newHead, strings.TrimPrefix(f["result"], "committed ")) {
		t.Errorf("commit-check result = %q, want \"committed <short sha of %s>\"", f["result"], newHead)
	}

	// --- ship state on disk: commitBaseHead is the HEAD of the first call,
	// and the commit step is completed ---
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read ship state: %v", err)
	}
	var st struct {
		CommitBaseHead string `json:"commitBaseHead"`
		Steps          []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("parse ship state: %v", err)
	}
	if st.CommitBaseHead != baseHead {
		t.Errorf("commitBaseHead = %q, want %q", st.CommitBaseHead, baseHead)
	}
	for _, s := range st.Steps {
		if s.Name == "commit" && s.Status != "completed" {
			t.Errorf("commit step status = %q, want completed", s.Status)
		}
	}
}
