package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// execActionResolveConfig direct-handler tests
//
// These tests exercise the resolution matrix: CLI flags, pipeline state,
// config.toml, and defaults. Each test seeds config as raw TOML text (not
// via config.WriteSection) so the schema-valid [execute] block is exercised
// end to end through config.ReadSection.
// ---------------------------------------------------------------------------

// TestExecActionResolveConfig_Defaults tests resolution with no config and no
// flags: auto=false, quality="", highRiskAutoApprove=false, all sources default/unset.
func TestExecActionResolveConfig_Defaults(t *testing.T) {
	root := t.TempDir()
	// Empty config files, no ship state
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")

	out, err := execActionResolveConfig(root, ExecuteStateIn{})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if result.Auto {
		t.Errorf("auto = %v, want false", result.Auto)
	}
	if result.Quality != "" {
		t.Errorf("quality = %q, want empty string", result.Quality)
	}
	if result.HighRiskAutoApprove {
		t.Errorf("highRiskAutoApprove = %v, want false", result.HighRiskAutoApprove)
	}
	if result.Sources["auto"] != "default" {
		t.Errorf("sources[auto] = %q, want default", result.Sources["auto"])
	}
	if result.Sources["quality"] != "unset" {
		t.Errorf("sources[quality] = %q, want unset", result.Sources["quality"])
	}
	if result.Sources["highRiskAutoApprove"] != "default" {
		t.Errorf("sources[highRiskAutoApprove] = %q, want default", result.Sources["highRiskAutoApprove"])
	}
	if len(result.Warnings) > 0 {
		t.Errorf("unexpected warnings: %v", result.Warnings)
	}
}

// TestExecActionResolveConfig_FromConfig tests reading all three keys from
// local [executePrefs].
func TestExecActionResolveConfig_FromConfig(t *testing.T) {
	root := t.TempDir()
	localTOML := `[executePrefs]
auto = true
quality = "minimal"
highRiskAutoApprove = true
`
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), localTOML)

	out, err := execActionResolveConfig(root, ExecuteStateIn{})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if !result.Auto {
		t.Errorf("auto = %v, want true", result.Auto)
	}
	if result.Quality != "minimal" {
		t.Errorf("quality = %q, want minimal", result.Quality)
	}
	if !result.HighRiskAutoApprove {
		t.Errorf("highRiskAutoApprove = %v, want true", result.HighRiskAutoApprove)
	}
	if result.Sources["auto"] != "config" {
		t.Errorf("sources[auto] = %q, want config", result.Sources["auto"])
	}
	if result.Sources["quality"] != "config" {
		t.Errorf("sources[quality] = %q, want config", result.Sources["quality"])
	}
	if result.Sources["highRiskAutoApprove"] != "config" {
		t.Errorf("sources[highRiskAutoApprove] = %q, want config", result.Sources["highRiskAutoApprove"])
	}
}

// TestExecActionResolveConfig_FlagWinsOverConfig tests that CLI flags override config.
func TestExecActionResolveConfig_FlagWinsOverConfig(t *testing.T) {
	root := t.TempDir()
	localTOML := `[executePrefs]
auto = false
quality = "minimal"
`
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), localTOML)

	out, err := execActionResolveConfig(root, ExecuteStateIn{
		Auto:    true,
		Quality: "full",
	})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if !result.Auto {
		t.Errorf("auto = %v, want true", result.Auto)
	}
	if result.Quality != "full" {
		t.Errorf("quality = %q, want full", result.Quality)
	}
	if result.Sources["auto"] != "cli" {
		t.Errorf("sources[auto] = %q, want cli", result.Sources["auto"])
	}
	if result.Sources["quality"] != "cli" {
		t.Errorf("sources[quality] = %q, want cli", result.Sources["quality"])
	}
}

// TestExecActionResolveConfig_PipelineAutoFromShipState tests that pipeline auto
// is read from ship state when no CLI flag and no config.auto.
func TestExecActionResolveConfig_PipelineAutoFromShipState(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")

	// Create a ship state with flags.auto=true
	createShipState(t, root, "feature/test", map[string]any{
		"flags": map[string]any{
			"auto": true,
		},
	})

	out, err := execActionResolveConfig(root, ExecuteStateIn{
		Branch: "feature/test",
	})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if !result.Auto {
		t.Errorf("auto = %v, want true", result.Auto)
	}
	if result.Sources["auto"] != "pipeline" {
		t.Errorf("sources[auto] = %q, want pipeline", result.Sources["auto"])
	}
}

// TestExecActionResolveConfig_AutoDefaultsQualityToBalanced tests that quality
// defaults to "balanced" when auto=true and no quality is supplied.
func TestExecActionResolveConfig_AutoDefaultsQualityToBalanced(t *testing.T) {
	root := t.TempDir()
	localTOML := `[executePrefs]
auto = true
`
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), localTOML)

	out, err := execActionResolveConfig(root, ExecuteStateIn{})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if result.Quality != "balanced" {
		t.Errorf("quality = %q, want balanced", result.Quality)
	}
	if result.Sources["quality"] != "default" {
		t.Errorf("sources[quality] = %q, want default", result.Sources["quality"])
	}
}

// TestExecActionResolveConfig_NoAutoLeavesQualityUnset tests that quality
// stays empty when auto=false and no quality is supplied.
func TestExecActionResolveConfig_NoAutoLeavesQualityUnset(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")

	out, err := execActionResolveConfig(root, ExecuteStateIn{
		Auto: false,
	})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if result.Quality != "" {
		t.Errorf("quality = %q, want empty string", result.Quality)
	}
	if result.Sources["quality"] != "unset" {
		t.Errorf("sources[quality] = %q, want unset", result.Sources["quality"])
	}
}

// TestExecActionResolveConfig_InvalidQualityWarns tests that an invalid quality
// value in config triggers a warning and quality ends up unset.
func TestExecActionResolveConfig_InvalidQualityWarns(t *testing.T) {
	root := t.TempDir()
	localTOML := `[executePrefs]
quality = "turbo"
`
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), localTOML)

	out, err := execActionResolveConfig(root, ExecuteStateIn{})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if result.Quality != "" {
		t.Errorf("quality = %q, want empty string", result.Quality)
	}
	// When quality is invalid and auto is false, sources[quality] ends up "unset"
	if result.Sources["quality"] != "unset" {
		t.Errorf("sources[quality] = %q, want unset", result.Sources["quality"])
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(result.Warnings), result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "executePrefs.quality") || !strings.Contains(result.Warnings[0], "turbo") {
		t.Errorf("warning does not mention executePrefs.quality or turbo: %s", result.Warnings[0])
	}
}

// TestExecActionResolveConfig_WrongTypeWarns tests that a wrong-type value
// for highRiskAutoApprove in config triggers a warning and falls back to default.
func TestExecActionResolveConfig_WrongTypeWarns(t *testing.T) {
	root := t.TempDir()
	localTOML := `[executePrefs]
highRiskAutoApprove = "yes"
`
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), localTOML)

	out, err := execActionResolveConfig(root, ExecuteStateIn{})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if result.HighRiskAutoApprove {
		t.Errorf("highRiskAutoApprove = %v, want false (fallback to default)", result.HighRiskAutoApprove)
	}
	if result.Sources["highRiskAutoApprove"] != "default" {
		t.Errorf("sources[highRiskAutoApprove] = %q, want default", result.Sources["highRiskAutoApprove"])
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(result.Warnings), result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "wrong type") {
		t.Errorf("warning does not mention wrong type: %s", result.Warnings[0])
	}
}

// TestExecActionResolveConfig_UnreadableShipStateWarns pins the warning path at
// resolve-config's own call site: a ship state file that exists but cannot be
// parsed must surface execPipelineAuto's warning in this action's Warnings, not
// only in execActionInit's. Uses the same corrupt-JSON technique as
// TestExecState_Init_PipelineAuto's "corrupt ship state" subtest.
func TestExecActionResolveConfig_UnreadableShipStateWarns(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")

	// Create a valid ship state, then overwrite its file with corrupt JSON.
	createShipState(t, root, "feat/unreadable", map[string]any{})
	shipSt, err := state.Find(root, "ship", "feat/unreadable")
	if err != nil || shipSt == nil {
		t.Fatalf("find ship state for corruption: err=%v, st=%v", err, shipSt)
	}
	if err := os.WriteFile(shipSt.Path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("corrupt ship state: %v", err)
	}

	out, err := execActionResolveConfig(root, ExecuteStateIn{Branch: "feat/unreadable"})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if result.Auto {
		t.Errorf("auto = %v, want false (ship state unreadable)", result.Auto)
	}
	if result.Sources["auto"] != "default" {
		t.Errorf("sources[auto] = %q, want default", result.Sources["auto"])
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected a warning for unreadable ship state, got none")
	}
	if !strings.Contains(result.Warnings[0], "ship state unreadable") {
		t.Errorf("warning does not mention ship state unreadable: %s", result.Warnings[0])
	}
}

// TestExecActionResolveConfig_UnreadableConfigWarns pins the error-vs-absent
// distinction on config.ReadSection: a malformed config.toml must warn rather
// than collapse silently into "section not configured" and resolve to the
// built-in defaults with no signal.
func TestExecActionResolveConfig_UnreadableConfigWarns(t *testing.T) {
	root := t.TempDir()
	// Unclosed table header — a TOML parse error, not a missing section.
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "[execute\nauto = true\n")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")

	out, err := execActionResolveConfig(root, ExecuteStateIn{})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if result.Auto {
		t.Errorf("auto = %v, want false (config unreadable, must not be trusted)", result.Auto)
	}
	if result.Sources["auto"] != "default" {
		t.Errorf("sources[auto] = %q, want default", result.Sources["auto"])
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected a warning for unreadable config, got none")
	}
	if !strings.Contains(result.Warnings[0], "unreadable") {
		t.Errorf("warning does not mention unreadable: %s", result.Warnings[0])
	}
}

// TestExecActionResolveConfig_InvalidCLIQualityWarnsAndFallsThrough pins the
// contract for an out-of-enum --quality value: non-fatal, never echoed back as
// sources.quality="cli", and resolution continues down the same
// config > auto-default > unset path an absent flag takes. Erroring instead
// would reject the legacy A/B/C tiers the execute skill still accepts.
func TestExecActionResolveConfig_InvalidCLIQualityWarnsAndFallsThrough(t *testing.T) {
	assertWarned := func(t *testing.T, result ExecuteResolveConfigOut) {
		t.Helper()
		if result.Sources["quality"] == "cli" {
			t.Errorf("sources[quality] = cli, want the invalid CLI value to be rejected")
		}
		if len(result.Warnings) == 0 {
			t.Fatalf("expected a warning for the invalid --quality value, got none")
		}
		if !strings.Contains(result.Warnings[0], "turbo-bogus") {
			t.Errorf("warning does not mention the offending value: %s", result.Warnings[0])
		}
	}

	t.Run("falls through to config", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
		writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[executePrefs]\nquality = \"minimal\"\n")

		out, err := execActionResolveConfig(root, ExecuteStateIn{Quality: "turbo-bogus"})
		if err != nil {
			t.Fatalf("execActionResolveConfig: %v", err)
		}
		result := out.(ExecuteResolveConfigOut)
		assertWarned(t, result)
		if result.Quality != "minimal" {
			t.Errorf("quality = %q, want minimal (from config)", result.Quality)
		}
		if result.Sources["quality"] != "config" {
			t.Errorf("sources[quality] = %q, want config", result.Sources["quality"])
		}
	})

	t.Run("falls through to the auto default", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
		writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")

		out, err := execActionResolveConfig(root, ExecuteStateIn{Quality: "turbo-bogus", Auto: true})
		if err != nil {
			t.Fatalf("execActionResolveConfig: %v", err)
		}
		result := out.(ExecuteResolveConfigOut)
		assertWarned(t, result)
		if result.Quality != "balanced" {
			t.Errorf("quality = %q, want balanced (auto default)", result.Quality)
		}
		if result.Sources["quality"] != "default" {
			t.Errorf("sources[quality] = %q, want default", result.Sources["quality"])
		}
	})

	t.Run("falls through to unset so the skill prompts", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
		writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")

		out, err := execActionResolveConfig(root, ExecuteStateIn{Quality: "turbo-bogus"})
		if err != nil {
			t.Fatalf("execActionResolveConfig: %v", err)
		}
		result := out.(ExecuteResolveConfigOut)
		assertWarned(t, result)
		if result.Quality != "" {
			t.Errorf("quality = %q, want empty (prompt)", result.Quality)
		}
		if result.Sources["quality"] != "unset" {
			t.Errorf("sources[quality] = %q, want unset", result.Sources["quality"])
		}
	})
}

// TestExecActionResolveConfig_NoBranchSkipsShipCrossRead tests that when
// Branch is empty, ship state is not cross-read, even if it exists.
func TestExecActionResolveConfig_NoBranchSkipsShipCrossRead(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")

	// Create a ship state with flags.auto=true
	createShipState(t, root, "feature/test", map[string]any{
		"flags": map[string]any{
			"auto": true,
		},
	})

	// Call without Branch — ship state should not be consulted
	out, err := execActionResolveConfig(root, ExecuteStateIn{
		Branch: "", // Empty branch
	})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if result.Auto {
		t.Errorf("auto = %v, want false (ship state not consulted)", result.Auto)
	}
	if result.Sources["auto"] != "default" {
		t.Errorf("sources[auto] = %q, want default", result.Sources["auto"])
	}
}

// ---------------------------------------------------------------------------
// executePrefs migration tests
// ---------------------------------------------------------------------------

// TestExecActionResolveConfig_MigratesStaleHighRiskAutoApprove tests that a
// stale execute.highRiskAutoApprove left in config.toml is moved to local
// executePrefs before resolution reads it: the moved value is used, a
// "Moved personal settings" warning is reported, and config.toml no longer
// has the key afterward.
func TestExecActionResolveConfig_MigratesStaleHighRiskAutoApprove(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "[execute]\nhighRiskAutoApprove = true\n")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")

	out, err := execActionResolveConfig(root, ExecuteStateIn{})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if !result.HighRiskAutoApprove {
		t.Errorf("highRiskAutoApprove = %v, want true (migrated from config.toml)", result.HighRiskAutoApprove)
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "Moved personal settings") {
		t.Fatalf("expected a \"Moved personal settings\" warning, got: %v", result.Warnings)
	}

	execSect, rerr := config.ReadSection(root, "execute")
	if rerr != nil && !errors.Is(rerr, config.ErrNotFound) {
		t.Fatalf("re-read config execute section: %v", rerr)
	}
	if _, ok := execSect["highRiskAutoApprove"]; ok {
		t.Errorf("config.toml still has execute.highRiskAutoApprove after migration")
	}

	prefsSect, rerr := config.ReadSection(root, "executePrefs")
	if rerr != nil {
		t.Fatalf("re-read local executePrefs section: %v", rerr)
	}
	if hraa, ok := prefsSect["highRiskAutoApprove"].(bool); !ok || !hraa {
		t.Errorf("local.toml executePrefs.highRiskAutoApprove = %v, want true", prefsSect["highRiskAutoApprove"])
	}
}

// TestExecActionResolveConfig_ConflictingMoveReturnsDataError tests that a
// stale execute.auto in config.toml conflicting with a different
// executePrefs.auto already in local.toml fails the automatic move instead
// of picking a winner: a *mcpserver.DataError with a non-empty Suggestion,
// and neither file is touched.
func TestExecActionResolveConfig_ConflictingMoveReturnsDataError(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, paths.DataDir, "config.toml")
	localPath := filepath.Join(root, paths.DataDir, "local.toml")
	configBefore := "[execute]\nauto = true\n"
	localBefore := "[executePrefs]\nauto = false\n"
	writeFile(t, configPath, configBefore)
	writeFile(t, localPath, localBefore)

	out, err := execActionResolveConfig(root, ExecuteStateIn{})
	if out != nil {
		t.Errorf("out = %v, want nil on error", out)
	}
	var dataErr *mcpserver.DataError
	if !errors.As(err, &dataErr) {
		t.Fatalf("err = %v (%T), want *mcpserver.DataError", err, err)
	}
	if dataErr.Suggestion == "" {
		t.Errorf("dataErr.Suggestion is empty, want a non-empty fix-it suggestion")
	}

	configAfter, rerr := os.ReadFile(configPath)
	if rerr != nil {
		t.Fatalf("read config.toml: %v", rerr)
	}
	if string(configAfter) != configBefore {
		t.Errorf("config.toml changed:\n got: %q\nwant: %q", configAfter, configBefore)
	}
	localAfter, rerr := os.ReadFile(localPath)
	if rerr != nil {
		t.Fatalf("read local.toml: %v", rerr)
	}
	if string(localAfter) != localBefore {
		t.Errorf("local.toml changed:\n got: %q\nwant: %q", localAfter, localBefore)
	}
}

// TestExecActionResolveConfig_CommitWavesFromConfigWithExecutePrefs tests
// that commitWaves still resolves from config.toml [execute] even when
// local [executePrefs] also exists and supplies other keys.
func TestExecActionResolveConfig_CommitWavesFromConfigWithExecutePrefs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "[execute]\ncommitWaves = false\n")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[executePrefs]\nauto = true\n")

	out, err := execActionResolveConfig(root, ExecuteStateIn{})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if result.CommitWaves {
		t.Errorf("commitWaves = %v, want false (from config.toml)", result.CommitWaves)
	}
	if result.Sources["commitWaves"] != "config" {
		t.Errorf("sources[commitWaves] = %q, want config", result.Sources["commitWaves"])
	}
	if !result.Auto {
		t.Errorf("auto = %v, want true (from local executePrefs)", result.Auto)
	}
}

// TestExecActionResolveConfig_MalformedExecutePrefsWarns tests that a
// malformed executePrefs value (wrong Go type, not an invalid enum value)
// is a warning, not an error -- pinning the quality-specific wrong-type
// branch that TestExecActionResolveConfig_WrongTypeWarns does not cover
// (that one exercises highRiskAutoApprove).
func TestExecActionResolveConfig_MalformedExecutePrefsWarns(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[executePrefs]\nquality = 123\n")

	out, err := execActionResolveConfig(root, ExecuteStateIn{})
	if err != nil {
		t.Fatalf("execActionResolveConfig: %v", err)
	}

	result := out.(ExecuteResolveConfigOut)
	if result.Quality != "" {
		t.Errorf("quality = %q, want empty string (wrong type falls back to default/unset)", result.Quality)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(result.Warnings), result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "wrong type") || !strings.Contains(result.Warnings[0], "executePrefs.quality") {
		t.Errorf("warning does not mention wrong type or executePrefs.quality: %s", result.Warnings[0])
	}
}
