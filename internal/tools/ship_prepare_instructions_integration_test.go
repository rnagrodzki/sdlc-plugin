//go:build integration

package tools

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// TestShipPrepareInstructions runs ship_prepare on a temp repo with the team,
// project local and user-level config files, and reads the seeded state.
func TestShipPrepareInstructions(t *testing.T) {
	userPath := filepath.Join(t.TempDir(), "user-local.toml")
	writeFile(t, userPath, "[[instructions.pr]]\ntext = \"user pr\"\n\n[[instructions.ship]]\ntext = \"user ship\"\n")
	t.Setenv(config.UserConfigPathEnv, userPath)

	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/instructions-integration")
	writeFile(t, filepath.Join(dir, paths.DataDir, paths.ConfigFile),
		"[[instructions.commit]]\ntext = [\"team commit a\", \"team commit b\"]\n\n[[instructions.pr]]\ntext = \"team pr\"\n\n[[instructions.harden]]\ntext = \"harden is off\"\n")
	writeFile(t, filepath.Join(dir, paths.DataDir, paths.LocalConfigFile),
		"[[instructions.review]]\ntext = \"local review\"\nnormalQuestions = true\n")

	run := func(t *testing.T, in ShipPrepareIn) map[string]any {
		t.Helper()
		in.SkipConfigCheck = true
		out, err := shipPrepare(dir, dir, in)
		if err != nil {
			t.Fatalf("shipPrepare: %v", err)
		}
		if len(out.Errors) != 0 {
			t.Fatalf("Errors = %v, want empty", out.Errors)
		}
		return readStateData(t, out.StateFile)
	}
	records := func(t *testing.T, data map[string]any) []map[string]any {
		t.Helper()
		raw, ok := data["instructions"].([]any)
		if !ok {
			t.Fatalf("instructions = %v, want a list", data["instructions"])
		}
		recs := make([]map[string]any, 0, len(raw))
		for _, r := range raw {
			recs = append(recs, r.(map[string]any))
		}
		return recs
	}
	keys := func(recs []map[string]any) []string {
		out := []string{}
		for _, r := range recs {
			out = append(out, r["key"].(string))
		}
		return out
	}

	for _, tc := range []struct {
		name string
		in   ShipPrepareIn
	}{
		{"normal run", ShipPrepareIn{}},
		{"dry run", ShipPrepareIn{DryRun: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := run(t, tc.in)
			recs := records(t, data)
			// harden is not a default step. The user pr list is hidden by
			// no local pr key, so team pr and user pr both run.
			wantKeys := []string{"commit", "commit", "review", "pr", "pr", "ship"}
			if got := keys(recs); !slices.Equal(got, wantKeys) {
				t.Fatalf("keys = %v, want %v", got, wantKeys)
			}
			for _, r := range recs {
				if r["status"] != instrPending || r["outcome"] != "" || r["startedAt"] != "" || r["endedAt"] != "" {
					t.Errorf("record not pending and empty: %v", r)
				}
			}
			if recs[2]["source"] != config.InstructionSourceProjectLocal || recs[2]["normalQuestions"] != true {
				t.Errorf("review record = %v, want project local with normalQuestions", recs[2])
			}
			if recs[3]["source"] != config.InstructionSourceTeam || recs[4]["source"] != config.InstructionSourceUser {
				t.Errorf("pr sources = %v, %v, want team then user", recs[3]["source"], recs[4]["source"])
			}
			if err := shipStateSchemaValidator(t)(data); err != nil {
				t.Errorf("state does not validate against ship-state.schema.json: %v", err)
			}
		})
	}
}
