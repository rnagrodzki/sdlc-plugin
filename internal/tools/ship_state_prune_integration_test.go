//go:build integration

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// TestIntegration_ShipStateInitKeepsCompletedRun calls the registered
// ship_state tool for init on a branch that has a completed sibling run and a
// live sibling run in runs/. The first state write must delete the live
// sibling and keep the completed one. The returned text must list only the
// deleted file under prunedOrphans.
func TestIntegration_ShipStateInitKeepsCompletedRun(t *testing.T) {
	const branch = "feat/keep-completed-int"
	dir := shipStateIntegrationRepo(t, branch)

	runs := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
	slug := state.SlugifyBranch(branch)
	completed := filepath.Join(runs, "ship-"+slug+"-20200101T000000Z.json")
	live := filepath.Join(runs, "ship-"+slug+"-20200102T000000Z.json")
	writeFile(t, completed, `{"pipelineStatus": "completed", "sessionId": null}`)
	writeFile(t, live, `{"sessionId": null}`)

	res, text := callRegisteredShipStateText(t, map[string]any{"action": "init"})
	if res.IsError {
		t.Fatalf("init returned a tool error:\n%s", text)
	}

	if _, err := os.Stat(completed); err != nil {
		t.Errorf("completed sibling %s should still exist, got stat error: %v", completed, err)
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Errorf("live sibling %s still exists, want removed (stat err = %v)", live, err)
	}

	idx := strings.Index(text, "prunedOrphans")
	if idx < 0 {
		t.Fatalf("init output has no prunedOrphans:\n%s", text)
	}
	orphans := text[idx:]
	if !strings.Contains(orphans, filepath.Base(live)) {
		t.Errorf("prunedOrphans does not list the deleted file %s:\n%s", live, text)
	}
	if strings.Contains(text, filepath.Base(completed)) {
		t.Errorf("init output names the kept file %s, want only the deleted file under prunedOrphans:\n%s", completed, text)
	}
}
