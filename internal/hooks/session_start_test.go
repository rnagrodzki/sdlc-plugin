package hooks

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// Test helpers
//
// worktree.MainRoot/ActiveRoot (called directly by several session-start
// phases, with no directory-override parameter available outside their own
// package — see internal/worktree/worktree.go) always resolve against the
// process's current working directory. Exercising those phases therefore
// requires actually chdir-ing the test process into a fixture git repo, the
// same way internal/worktree/worktree_test.go builds its own git fixtures
// (runGit/realPath helpers duplicated here since they're unexported in that
// package).
// ---------------------------------------------------------------------------

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("could not resolve symlinks for %s: %v", path, err)
	}
	return resolved
}

// chdir switches the process's cwd to dir for the test's duration, restoring
// the original directory on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir(%s): %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
}

// gitFixture creates a fresh single-worktree git repo with one commit on
// branch, chdirs the test process into it, and returns its (symlink-
// resolved) real path.
func gitFixture(t *testing.T, branch string) string {
	t.Helper()
	dir := realPath(t, t.TempDir())
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "checkout", "-q", "-b", branch)
	runGit(t, dir, "-c", "user.email=hooks-test@example.com", "-c", "user.name=hooks-test", "commit", "--allow-empty", "-q", "-m", "init")
	chdir(t, dir)
	return dir
}

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d\ngot:  %q\nwant: %q", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Run dispatch (hooks.go)
// ---------------------------------------------------------------------------

func TestRun_UnknownHook(t *testing.T) {
	var out bytes.Buffer
	code := Run("does-not-exist", strings.NewReader(""), &out)
	if code != 0 {
		t.Errorf("Run(unknown) exit code = %d, want 0", code)
	}
	if out.Len() != 0 {
		t.Errorf("Run(unknown) wrote stdout output: %q, want none", out.String())
	}
}

// TestRun_RegistryContainsExactlyKnownHooks was
// TestRun_RegistryContainsExactlySessionStart under Task 37, when
// "session-start" was the registry's only entry. Task 38 authorized adding
// block-askuserquestion-auto, pipeline-continue, and post-tool-validate
// (see hooks.go); Task 39 added the Stop/PreCompact quartet
// (pre-compact-save, stop-state-save, stop-plan-integrity,
// stop-pipeline-continue), bringing the registry to 8 total entries — still
// a closed-set check, just over the current known set rather than a single
// entry.
func TestRun_RegistryContainsExactlyKnownHooks(t *testing.T) {
	want := []string{
		"session-start", "block-askuserquestion-auto", "pipeline-continue", "post-tool-validate",
		"pre-compact-save", "stop-state-save", "stop-plan-integrity", "stop-pipeline-continue",
	}
	if len(registry) != len(want) {
		t.Fatalf("registry has %d entries, want exactly %d: %v", len(registry), len(want), registryKeys())
	}
	for _, name := range want {
		if _, ok := registry[name]; !ok {
			t.Errorf("registry missing %q key: %v", name, registryKeys())
		}
	}
}

func registryKeys() []string {
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}
	return keys
}

func TestRun_SessionStart_NoStdin(t *testing.T) {
	// Isolate cwd and HOME from any real repo/plugin/config so the run is
	// deterministic and doesn't walk the real tester's ~/.claude/plugins.
	chdir(t, realPath(t, t.TempDir()))
	t.Setenv("HOME", realPath(t, t.TempDir()))

	var out bytes.Buffer
	code := Run("session-start", strings.NewReader(""), &out)
	if code != 0 {
		t.Errorf("Run(session-start) exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Plan mode routing:") {
		t.Errorf("Run(session-start) output missing plan-mode-routing line:\n%s", out.String())
	}
}

// TestReadEvent exercises hooks.go's readEvent directly: the JSON-unmarshal
// success and failure paths, source defaulting, and session_id extraction
// into HookCtx. Run() and sessionStart() both build on this function but
// never expose its intermediate result, so its own success/failure/edge
// cases are only directly observable here.
func TestReadEvent(t *testing.T) {
	cases := []struct {
		name       string
		stdin      io.Reader
		wantSource string
		wantRaw    bool
		wantSID    string
	}{
		{"nil stdin", nil, "startup", false, ""},
		{"empty stdin", strings.NewReader(""), "startup", false, ""},
		{"unparseable json", strings.NewReader("{not valid json"), "startup", false, ""},
		{"top-level json array, not an object", strings.NewReader(`["a","b"]`), "startup", false, ""},
		{"valid json, no source field", strings.NewReader(`{"session_id":"sess-1"}`), "startup", true, "sess-1"},
		{"valid json, explicit source", strings.NewReader(`{"source":"resume","session_id":"sess-2"}`), "resume", true, "sess-2"},
		{"valid json, empty source string defaults", strings.NewReader(`{"source":""}`), "startup", true, ""},
		{"valid json, non-string session_id ignored", strings.NewReader(`{"source":"compact","session_id":42}`), "compact", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, event := readEvent(c.stdin)
			if event.Source != c.wantSource {
				t.Errorf("Source = %q, want %q", event.Source, c.wantSource)
			}
			if (event.Raw != nil) != c.wantRaw {
				t.Errorf("Raw != nil = %v, want %v", event.Raw != nil, c.wantRaw)
			}
			if ctx.SessionID != c.wantSID {
				t.Errorf("SessionID = %q, want %q", ctx.SessionID, c.wantSID)
			}
		})
	}
}

// TestRun_SessionStart_MalformedStdin is the AC's explicit "(b) unparseable
// stdin" case, exercised through the real Run() entry point rather than
// readEvent directly: malformed JSON must degrade to the same default
// ("startup"-sourced) output as no stdin at all, with exit 0 and no crash —
// readEvent's fail-open contract never returns an error, so no stderr
// diagnostic is expected here (unlike an unknown hook name or a handler
// error, which do log one).
func TestRun_SessionStart_MalformedStdin(t *testing.T) {
	chdir(t, realPath(t, t.TempDir()))
	t.Setenv("HOME", realPath(t, t.TempDir()))

	var out bytes.Buffer
	code := Run("session-start", strings.NewReader("{not valid json"), &out)
	if code != 0 {
		t.Errorf("Run(session-start) with malformed stdin exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Plan mode routing:") {
		t.Errorf("Run(session-start) with malformed stdin missing default header output:\n%s", out.String())
	}
}

// TestRun_SessionStart_GoldenSources is the AC's literal "golden test:
// fixture stdin -> stdout" methodology, driven through the real Run() entry
// point (not sessionStart or pipelineResumePhase directly) for each of the
// four documented source values. event.Source only affects
// pipelineResumePhase's output (compact gets a "(post-compact)" banner
// variant; startup/clear/resume all share the plain banner), so an
// in-progress execute fixture is used to make that difference
// observable in real stdout bytes produced from real JSON stdin.
func TestRun_SessionStart_GoldenSources(t *testing.T) {
	branch := "feat/golden-sources"
	root := gitFixture(t, branch)
	t.Setenv("HOME", realPath(t, t.TempDir()))

	st, err := state.Init(root, "execute", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	st.Data["waves"] = []any{
		map[string]any{"number": float64(1), "status": "completed"},
		map[string]any{"number": float64(2), "status": "in_progress"},
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	runWithSource := func(source string) string {
		t.Helper()
		stdin := `{"source":"` + source + `","session_id":"sess-golden"}`
		var out bytes.Buffer
		code := Run("session-start", strings.NewReader(stdin), &out)
		if code != 0 {
			t.Fatalf("Run(session-start, source=%s) exit code = %d, want 0", source, code)
		}
		return out.String()
	}

	wantPlain := "Active execution: execute on " + branch + " (wave 1 of 2 complete)"
	wantPostCompact := "Active execution (post-compact): execute on " + branch + " (wave 1 of 2 complete)"

	for _, source := range []string{"startup", "clear", "resume"} {
		out := runWithSource(source)
		if !strings.Contains(out, wantPlain) {
			t.Errorf("Run(session-start, source=%s) missing %q:\ngot:\n%s", source, wantPlain, out)
		}
		if strings.Contains(out, wantPostCompact) {
			t.Errorf("Run(session-start, source=%s) unexpectedly shows post-compact banner:\ngot:\n%s", source, out)
		}
	}

	compactOut := runWithSource("compact")
	if !strings.Contains(compactOut, wantPostCompact) {
		t.Errorf("Run(session-start, source=compact) missing %q:\ngot:\n%s", wantPostCompact, compactOut)
	}
}

// ---------------------------------------------------------------------------
// safeStringsPhase / safeResolvePluginRoot / safeCountSkills (panic recovery)
// ---------------------------------------------------------------------------

func TestSafeStringsPhase_RecoversPanic(t *testing.T) {
	got := safeStringsPhase("boom", func() []string {
		panic("kaboom")
	})
	if got != nil {
		t.Errorf("safeStringsPhase after panic = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// Small pure helpers
// ---------------------------------------------------------------------------

func TestPluralS(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{{1, ""}, {0, "s"}, {2, "s"}, {-1, "s"}}
	for _, c := range cases {
		if got := pluralS(c.n); got != c.want {
			t.Errorf("pluralS(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestCountNonEmptyLines(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a\nb\nc", 3},
		{"a\n\nb\n", 2},
		{"\n\n", 0},
	}
	for _, c := range cases {
		if got := countNonEmptyLines(c.in); got != c.want {
			t.Errorf("countNonEmptyLines(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestJiraAgeDisplay(t *testing.T) {
	cases := []struct {
		hours float64
		want  string
	}{
		{0.2, "less than 1h ago"},
		{0.99, "less than 1h ago"},
		{1, "1h ago"},
		{5.6, "6h ago"},
		{24, "1 day ago"},
		{49, "2 days ago"},
	}
	for _, c := range cases {
		if got := jiraAgeDisplay(c.hours); got != c.want {
			t.Errorf("jiraAgeDisplay(%v) = %q, want %q", c.hours, got, c.want)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{{4, "4"}, {4.5, "4.5"}, {0, "0"}}
	for _, c := range cases {
		if got := formatNumber(c.in); got != c.want {
			t.Errorf("formatNumber(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizePresetValue(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"A", "full"},
		{"b", "balanced"},
		{"C", "minimal"},
		{"custom", "custom"},
		{float64(3), "3"},
	}
	for _, c := range cases {
		if got := normalizePresetValue(c.in); got != c.want {
			t.Errorf("normalizePresetValue(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestJsStringify(t *testing.T) {
	if got := jsStringify("minor"); got != "minor" {
		t.Errorf("jsStringify(minor) = %q, want minor", got)
	}
	if got := jsStringify(float64(80)); got != "80" {
		t.Errorf("jsStringify(80) = %q, want 80", got)
	}
	if got := jsStringify(true); got != "true" {
		t.Errorf("jsStringify(true) = %q, want true", got)
	}
}

func TestStageLabel(t *testing.T) {
	mk := func(stage string, done, total int) openspec.OpenspecChangeInfo {
		s := stage
		return openspec.OpenspecChangeInfo{Stage: &s, TasksDone: done, TasksTotal: total}
	}
	cases := []struct {
		name string
		in   openspec.OpenspecChangeInfo
		want string
	}{
		{"spec-in-progress", mk("spec-in-progress", 0, 0), "spec in progress"},
		{"ready-for-plan", mk("ready-for-plan", 0, 5), "ready for implementation (5 tasks)"},
		{"implementation-in-progress", mk("implementation-in-progress", 2, 5), "implementing (2/5 tasks done)"},
		{"tasks-complete", mk("tasks-complete", 5, 5), "tasks complete"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stageLabel(c.in); got != c.want {
				t.Errorf("stageLabel(%+v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRecoveryPipelineLines(t *testing.T) {
	t.Run("ship full", func(t *testing.T) {
		rm := map[string]any{
			"pipeline": "ship", "branch": "feat/x",
			"currentStep": "review", "reviewVerdict": "changes-requested",
			"deferredFindings": float64(2),
		}
		assertLines(t, recoveryPipelineLines(rm), []string{
			"  Pipeline: ship on feat/x",
			"  Current step: review",
			"  Review verdict: changes-requested (2 deferred)",
		})
	})

	t.Run("ship minimal", func(t *testing.T) {
		rm := map[string]any{"pipeline": "ship", "branch": "feat/x"}
		assertLines(t, recoveryPipelineLines(rm), []string{"  Pipeline: ship on feat/x"})
	})

	t.Run("execute", func(t *testing.T) {
		rm := map[string]any{
			"pipeline": "execute", "branch": "feat/x",
			"completedWaves": float64(3), "totalWaves": float64(5),
		}
		assertLines(t, recoveryPipelineLines(rm), []string{
			"  Pipeline: execute on feat/x",
			"  Progress: wave 3 of 5 complete",
		})
	})

	t.Run("unknown pipeline", func(t *testing.T) {
		if got := recoveryPipelineLines(map[string]any{"pipeline": "mystery"}); got != nil {
			t.Errorf("recoveryPipelineLines(unknown) = %v, want nil", got)
		}
	})
}

func TestSameWorktree(t *testing.T) {
	// No git repo here: resolveActiveWorktreeSafe falls back to os.Getwd().
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	t.Run("absent field fails open to true", func(t *testing.T) {
		if !sameWorktree(map[string]any{}) {
			t.Error("sameWorktree with no worktree field = false, want true (fail open)")
		}
	})

	t.Run("matching worktree", func(t *testing.T) {
		cwd, _ := os.Getwd()
		if !sameWorktree(map[string]any{"worktree": cwd}) {
			t.Error("sameWorktree with matching worktree = false, want true")
		}
	})

	t.Run("mismatched worktree", func(t *testing.T) {
		other := realPath(t, t.TempDir())
		if sameWorktree(map[string]any{"worktree": other}) {
			t.Error("sameWorktree with mismatched worktree = true, want false")
		}
	})

	t.Run("nonexistent worktree path fails open to true", func(t *testing.T) {
		if !sameWorktree(map[string]any{"worktree": "/does/not/exist/anywhere"}) {
			t.Error("sameWorktree with unresolvable worktree path = false, want true (fail open)")
		}
	})
}

// ---------------------------------------------------------------------------
// Plugin root resolution
// ---------------------------------------------------------------------------

func TestWalkUpForPluginManifest(t *testing.T) {
	root := realPath(t, t.TempDir())
	manifestDir := filepath.Join(root, ".claude-plugin")
	mustMkdirAll(t, manifestDir)
	mustWriteFile(t, filepath.Join(manifestDir, "plugin.json"), `{"name":"sdlc"}`)

	nested := filepath.Join(root, "a", "b", "c")
	mustMkdirAll(t, nested)

	got, ok := walkUpForPluginManifest(nested)
	if !ok {
		t.Fatal("walkUpForPluginManifest: not found, want found")
	}
	if got != root {
		t.Errorf("walkUpForPluginManifest = %q, want %q", got, root)
	}
}

func TestWalkUpForPluginManifest_NotFound(t *testing.T) {
	dir := realPath(t, t.TempDir())
	if _, ok := walkUpForPluginManifest(dir); ok {
		t.Error("walkUpForPluginManifest found a manifest in an isolated tmp tree; want not found")
	}
}

func TestWalkUpForMarketplacePluginRoot(t *testing.T) {
	root := realPath(t, t.TempDir())
	manifestDir := filepath.Join(root, ".claude-plugin")
	mustMkdirAll(t, manifestDir)
	mustWriteFile(t, filepath.Join(manifestDir, "marketplace.json"), `{"plugins":[{"name":"sdlc","source":"./plugins/sdlc"}]}`)

	pluginRoot := filepath.Join(root, "plugins", "sdlc")
	pluginManifestDir := filepath.Join(pluginRoot, ".claude-plugin")
	mustMkdirAll(t, pluginManifestDir)
	mustWriteFile(t, filepath.Join(pluginManifestDir, "plugin.json"), `{"name":"sdlc"}`)

	nested := filepath.Join(root, "internal", "hooks")
	mustMkdirAll(t, nested)

	got, ok := walkUpForMarketplacePluginRoot(nested, "sdlc")
	if !ok {
		t.Fatal("walkUpForMarketplacePluginRoot: not found, want found")
	}
	if got != pluginRoot {
		t.Errorf("walkUpForMarketplacePluginRoot = %q, want %q", got, pluginRoot)
	}
}

func TestWalkUpForMarketplacePluginRoot_NameMismatch(t *testing.T) {
	root := realPath(t, t.TempDir())
	manifestDir := filepath.Join(root, ".claude-plugin")
	mustMkdirAll(t, manifestDir)
	mustWriteFile(t, filepath.Join(manifestDir, "marketplace.json"), `{"plugins":[{"name":"other-plugin","source":"./plugins/other"}]}`)

	if _, ok := walkUpForMarketplacePluginRoot(root, "sdlc"); ok {
		t.Error("walkUpForMarketplacePluginRoot matched a differently-named plugin; want not found")
	}
}

func TestResolvePluginRoot_MarketplaceSourceLayout(t *testing.T) {
	// Reproduces this repo's own layout: the repo root's .claude-plugin/
	// holds only marketplace.json (source: "./plugins/sdlc"); the real
	// plugin.json lives one level down, so strategy 1 (walkUpForPluginManifest
	// from cwd) never finds it and strategy 2 must.
	root := realPath(t, t.TempDir())
	manifestDir := filepath.Join(root, ".claude-plugin")
	mustMkdirAll(t, manifestDir)
	mustWriteFile(t, filepath.Join(manifestDir, "marketplace.json"), `{"plugins":[{"name":"sdlc","source":"./plugins/sdlc"}]}`)

	pluginRoot := filepath.Join(root, "plugins", "sdlc")
	pluginManifestDir := filepath.Join(pluginRoot, ".claude-plugin")
	mustMkdirAll(t, pluginManifestDir)
	mustWriteFile(t, filepath.Join(pluginManifestDir, "plugin.json"), `{"name":"sdlc"}`)

	chdir(t, root)
	t.Setenv("HOME", realPath(t, t.TempDir()))

	got, ok := resolvePluginRoot()
	if !ok {
		t.Fatal("resolvePluginRoot: not found, want found via marketplace.json source resolution")
	}
	if got != pluginRoot {
		t.Errorf("resolvePluginRoot = %q, want %q", got, pluginRoot)
	}
}

func TestResolvePluginRoot_FallbackUnderHome(t *testing.T) {
	// Isolate cwd so strategies 1 (executable dir) and 2 (cwd) both miss,
	// forcing the ~/.claude/plugins fallback (strategy 3).
	chdir(t, realPath(t, t.TempDir()))

	fakeHome := realPath(t, t.TempDir())
	t.Setenv("HOME", fakeHome)

	pluginDir := filepath.Join(fakeHome, ".claude", "plugins", "some-marketplace", "sdlc-utilities")
	manifestDir := filepath.Join(pluginDir, ".claude-plugin")
	mustMkdirAll(t, manifestDir)
	mustWriteFile(t, filepath.Join(manifestDir, "plugin.json"), `{"name":"sdlc"}`)

	got, ok := resolvePluginRoot()
	if !ok {
		t.Fatal("resolvePluginRoot: not found, want found via ~/.claude/plugins fallback")
	}
	if got != pluginDir {
		t.Errorf("resolvePluginRoot = %q, want %q", got, pluginDir)
	}
}

func TestResolvePluginRoot_Unresolvable(t *testing.T) {
	chdir(t, realPath(t, t.TempDir()))
	t.Setenv("HOME", realPath(t, t.TempDir())) // empty fake home: no plugins dir at all

	if _, ok := resolvePluginRoot(); ok {
		t.Error("resolvePluginRoot resolved with no manifest anywhere reachable; want not found")
	}
}

func TestCountUserInvocableSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill := func(name, frontmatter string) {
		dir := filepath.Join(root, "skills", name)
		mustMkdirAll(t, dir)
		mustWriteFile(t, filepath.Join(dir, "SKILL.md"), frontmatter)
	}
	writeSkill("plan", "---\nname: plan\nuser-invocable: true\n---\nbody\n")
	writeSkill("internal-only", "---\nname: internal-only\nuser-invocable: false\n---\nbody\n")
	writeSkill("no-flag", "---\nname: no-flag\n---\nbody\n")

	if got := countUserInvocableSkills(root); got != 1 {
		t.Errorf("countUserInvocableSkills = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// Header assembly (Ruling B: plugin-root-line/skill-count-line joint gating)
// ---------------------------------------------------------------------------

func TestSessionStart_HeaderLines_PluginRootUnresolved(t *testing.T) {
	chdir(t, realPath(t, t.TempDir()))
	t.Setenv("HOME", realPath(t, t.TempDir()))

	out, err := sessionStart(HookCtx{}, Event{Source: "startup"})
	if err != nil {
		t.Fatalf("sessionStart returned error: %v", err)
	}
	if strings.Contains(out.PlainText, "sdlc: v") {
		t.Errorf("version/skills line present despite unresolved plugin root:\n%s", out.PlainText)
	}
	if strings.Contains(out.PlainText, "sdlc plugin root:") {
		t.Errorf("plugin-root line present despite unresolved plugin root:\n%s", out.PlainText)
	}
	if !strings.Contains(out.PlainText, "Plan mode routing:") {
		t.Errorf("plan-mode-routing line missing (must always be present):\n%s", out.PlainText)
	}
}

func TestSessionStart_HeaderLines_PluginRootResolved(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)
	t.Setenv("HOME", realPath(t, t.TempDir()))

	mustMkdirAll(t, filepath.Join(dir, ".claude-plugin"))
	mustWriteFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"sdlc"}`)
	mustMkdirAll(t, filepath.Join(dir, "skills", "plan"))
	mustWriteFile(t, filepath.Join(dir, "skills", "plan", "SKILL.md"), "---\nuser-invocable: true\n---\nbody\n")

	orig := PluginVersion
	PluginVersion = "9.9.9"
	t.Cleanup(func() { PluginVersion = orig })

	out, err := sessionStart(HookCtx{}, Event{Source: "startup"})
	if err != nil {
		t.Fatalf("sessionStart returned error: %v", err)
	}
	if !strings.Contains(out.PlainText, "sdlc: v9.9.9 (1 skills loaded)") {
		t.Errorf("version/skills line missing or wrong:\n%s", out.PlainText)
	}
	if !strings.Contains(out.PlainText, "sdlc plugin root: "+dir) {
		t.Errorf("plugin-root line missing or wrong:\n%s", out.PlainText)
	}
}

// ---------------------------------------------------------------------------
// Pipeline resume phase
// ---------------------------------------------------------------------------

func TestPipelineResumePhase_ShipInProgress(t *testing.T) {
	branch := "feat/ship-in-progress"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "ship", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	st.Data["steps"] = []any{
		map[string]any{"name": "commit", "status": "completed"},
		map[string]any{"name": "review", "status": "in_progress"},
		map[string]any{"name": "pr", "status": "pending"},
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	assertLines(t, pipelineResumePhase("startup"), []string{
		"Active pipeline: ship on " + branch + " (paused at step 2: review)",
		"  Resume with: /ship --resume",
	})
}

func TestPipelineResumePhase_ShipLastCompleted(t *testing.T) {
	branch := "feat/ship-last-completed"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "ship", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	st.Data["steps"] = []any{
		map[string]any{"name": "commit", "status": "completed"},
		map[string]any{"name": "review", "status": "completed"},
		map[string]any{"name": "pr", "status": "pending"},
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	assertLines(t, pipelineResumePhase("startup"), []string{
		"Active pipeline: ship on " + branch + " (last completed step 2: review)",
		"  Resume with: /ship --resume",
	})
}

func TestPipelineResumePhase_ExecuteSourceVariants(t *testing.T) {
	branch := "feat/exec-source-variants"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "execute", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	st.Data["waves"] = []any{
		map[string]any{"number": float64(1), "status": "completed"},
		map[string]any{"number": float64(2), "status": "completed"},
		map[string]any{"number": float64(3), "status": "in_progress"},
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	t.Run("startup", func(t *testing.T) {
		assertLines(t, pipelineResumePhase("startup"), []string{
			"Active execution: execute on " + branch + " (wave 2 of 3 complete)",
			"  Resume with: /execute --resume",
		})
	})

	t.Run("compact", func(t *testing.T) {
		assertLines(t, pipelineResumePhase("compact"), []string{
			"Active execution (post-compact): execute on " + branch + " (wave 2 of 3 complete)",
			"  Resume with: /execute --resume",
		})
	})
}

func TestPipelineResumePhase_WorktreeMismatchSuppressesBanner(t *testing.T) {
	branch := "feat/worktree-mismatch"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "ship", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	st.Data["steps"] = []any{
		map[string]any{"name": "commit", "status": "in_progress"},
	}
	// A real, but different, existing directory: EvalSymlinks succeeds on
	// both sides so this exercises a genuine mismatch, not the "unresolvable
	// path fails open" branch.
	st.Data["worktree"] = realPath(t, t.TempDir())
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	if got := pipelineResumePhase("startup"); got != nil {
		t.Errorf("pipelineResumePhase with mismatched worktree = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// Resume banner suppression for completed runs (Task 23)
// ---------------------------------------------------------------------------

func TestPipelineResumePhase_ExecuteRunStatusCompleted_SuppressesBanner(t *testing.T) {
	branch := "feat/exec-run-status-completed"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "execute", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	st.Data["waves"] = []any{
		map[string]any{"number": float64(1), "status": "completed"},
		map[string]any{"number": float64(2), "status": "completed"},
	}
	st.Data["runStatus"] = "completed"
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	if got := pipelineResumePhase("startup"); got != nil {
		t.Errorf("pipelineResumePhase with runStatus=completed = %v, want nil", got)
	}
}

func TestPipelineResumePhase_ShipPipelineStatusCompleted_SuppressesBanner(t *testing.T) {
	branch := "feat/ship-pipeline-status-completed"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "ship", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	st.Data["steps"] = []any{
		map[string]any{"name": "commit", "status": "completed"},
		map[string]any{"name": "review", "status": "completed"},
		map[string]any{"name": "pr", "status": "completed"},
	}
	st.Data["pipelineStatus"] = "completed"
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	if got := pipelineResumePhase("startup"); got != nil {
		t.Errorf("pipelineResumePhase with pipelineStatus=completed = %v, want nil", got)
	}
}

func TestPipelineResumePhase_ExecuteBackwardCompat_AllWavesCompleted_SuppressesBanner(t *testing.T) {
	branch := "feat/exec-backcompat-all-completed"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "execute", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	// No runStatus key at all -- mirrors a state file written before Task 22.
	st.Data["waves"] = []any{
		map[string]any{"number": float64(1), "status": "completed"},
		map[string]any{"number": float64(2), "status": "completed"},
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	if got := pipelineResumePhase("startup"); got != nil {
		t.Errorf("pipelineResumePhase with no runStatus but all waves completed = %v, want nil", got)
	}
}

func TestPipelineResumePhase_ShipBackwardCompat_AllStepsTerminal_SuppressesBanner(t *testing.T) {
	branch := "feat/ship-backcompat-all-terminal"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "ship", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	// No pipelineStatus key at all -- mirrors a state file written before
	// Task 22.
	st.Data["steps"] = []any{
		map[string]any{"name": "commit", "status": "completed"},
		map[string]any{"name": "review", "status": "completed"},
		map[string]any{"name": "pr", "status": "skipped"},
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	if got := pipelineResumePhase("startup"); got != nil {
		t.Errorf("pipelineResumePhase with no pipelineStatus but all steps terminal = %v, want nil", got)
	}
}

func TestPipelineResumePhase_ShipBackwardCompat_ConditionalPendingStepTreatedAsTerminal(t *testing.T) {
	branch := "feat/ship-backcompat-conditional-pending"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "ship", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	// received-review/commit-fixes rest at pending forever when their
	// trigger condition never fires -- that carries a condition key and
	// must not block the all-terminal check, mirroring
	// shipValidatePipelineContract's own treatment of the same steps.
	st.Data["steps"] = []any{
		map[string]any{"name": "commit", "status": "completed"},
		map[string]any{"name": "review", "status": "completed"},
		map[string]any{"name": "received-review", "status": "pending", "condition": "review.verdict == changes-requested"},
		map[string]any{"name": "pr", "status": "completed"},
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	if got := pipelineResumePhase("startup"); got != nil {
		t.Errorf("pipelineResumePhase with conditional pending step = %v, want nil", got)
	}
}

func TestPipelineResumePhase_ExecuteBackwardCompat_PartialWaves_BannerStillShown(t *testing.T) {
	branch := "feat/exec-backcompat-partial"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "execute", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	// No runStatus key, and not every wave is completed -- must not be
	// suppressed by the backward-compat fallback.
	st.Data["waves"] = []any{
		map[string]any{"number": float64(1), "status": "completed"},
		map[string]any{"number": float64(2), "status": "failed"},
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	assertLines(t, pipelineResumePhase("startup"), []string{
		"Active execution: execute on " + branch + " (wave 1 of 2 complete)",
		"  Resume with: /execute --resume",
	})
}

func TestPipelineResumePhase_ShipBackwardCompat_PendingWithoutConditionBlocksSuppression(t *testing.T) {
	branch := "feat/ship-backcompat-pending-no-condition"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "ship", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	// No pipelineStatus key, and "pr" is pending with no condition key -- a
	// genuine stalled step, not a conditional resting state, so the banner
	// must still show.
	st.Data["steps"] = []any{
		map[string]any{"name": "commit", "status": "completed"},
		map[string]any{"name": "review", "status": "completed"},
		map[string]any{"name": "pr", "status": "pending"},
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	assertLines(t, pipelineResumePhase("startup"), []string{
		"Active pipeline: ship on " + branch + " (last completed step 2: review)",
		"  Resume with: /ship --resume",
	})
}

func TestPipelineResumePhase_ShipBackwardCompat_FailedStepTreatedAsTerminal(t *testing.T) {
	branch := "feat/ship-backcompat-failed"
	root := gitFixture(t, branch)

	st, err := state.Init(root, "ship", branch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	// No pipelineStatus key. "review" is failed -- shipValidatePipelineContract
	// never flags failed steps as violations (cleanup only blocks on steps that
	// never reached ANY terminal state), so failed counts as terminal here too
	// and the banner must be suppressed, matching the mirrored contract.
	st.Data["steps"] = []any{
		map[string]any{"name": "commit", "status": "completed"},
		map[string]any{"name": "review", "status": "failed"},
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	if got := pipelineResumePhase("startup"); got != nil {
		t.Fatalf("pipelineResumePhase() = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// Compact recovery phase
// ---------------------------------------------------------------------------

func TestCompactRecoveryPhase_ConsumeSidecar(t *testing.T) {
	branch := "feat/recovery-consume"
	root := gitFixture(t, branch)
	slug := state.SlugifyBranch(branch)

	data := map[string]any{
		"pipeline":    "ship",
		"branch":      branch,
		"currentStep": "review",
	}
	if err := state.WriteRecoverySidecar(root, slug, data); err != nil {
		t.Fatal(err)
	}

	assertLines(t, compactRecoveryPhase(), []string{
		"Pipeline state recovered after compaction:",
		"  Pipeline: ship on " + branch,
		"  Current step: review",
	})

	sidecarPath := filepath.Join(root, paths.DataDir, "execution", ".compact-recovery-"+slug+".json")
	if _, err := os.Stat(sidecarPath); !os.IsNotExist(err) {
		t.Errorf("sidecar still exists after single-use consume: stat err=%v", err)
	}
}

func TestCompactRecoveryPhase_StaleSweep(t *testing.T) {
	branch := "feat/recovery-sweep"
	root := gitFixture(t, branch)
	execDir := filepath.Join(root, paths.DataDir, "execution")
	mustMkdirAll(t, execDir)

	staleFiles := []string{".compact-recovery-otherslug.json", ".stop-block-count-otherslug.json"}
	freshFile := ".compact-recovery-freshslug.json"

	for _, name := range append(append([]string{}, staleFiles...), freshFile) {
		mustWriteFile(t, filepath.Join(execDir, name), "{}")
	}

	old := time.Now().Add(-25 * time.Hour)
	for _, name := range staleFiles {
		if err := os.Chtimes(filepath.Join(execDir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}

	compactRecoveryPhase()

	for _, name := range staleFiles {
		if _, err := os.Stat(filepath.Join(execDir, name)); !os.IsNotExist(err) {
			t.Errorf("stale sidecar %s still exists after 24h sweep", name)
		}
	}
	if _, err := os.Stat(filepath.Join(execDir, freshFile)); err != nil {
		t.Errorf("fresh sidecar %s was removed by 24h sweep: %v", freshFile, err)
	}
}

func TestCompactRecoveryPhase_LegacyCleanup(t *testing.T) {
	branch := "feat/recovery-legacy"
	root := gitFixture(t, branch)
	execDir := filepath.Join(root, paths.DataDir, "execution")
	mustMkdirAll(t, execDir)
	legacyPath := filepath.Join(execDir, ".compact-recovery.json")
	mustWriteFile(t, legacyPath, "{}")

	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(legacyPath, old, old); err != nil {
		t.Fatal(err)
	}

	compactRecoveryPhase()

	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Error("legacy sidecar older than 1h TTL still exists after cleanup")
	}
}

func TestCompactRecoveryPhase_LegacyCleanup_KeepsFresh(t *testing.T) {
	branch := "feat/recovery-legacy-fresh"
	root := gitFixture(t, branch)
	execDir := filepath.Join(root, paths.DataDir, "execution")
	mustMkdirAll(t, execDir)
	legacyPath := filepath.Join(execDir, ".compact-recovery.json")
	mustWriteFile(t, legacyPath, "{}") // fresh mtime, well within TTL

	compactRecoveryPhase()

	if _, err := os.Stat(legacyPath); err != nil {
		t.Errorf("fresh legacy sidecar was removed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OpenSpec phase
// ---------------------------------------------------------------------------

func TestOpenSpecPhase_ZeroChanges(t *testing.T) {
	branch := "feat/openspec-zero"
	root := gitFixture(t, branch)
	mustMkdirAll(t, filepath.Join(root, "openspec"))
	mustWriteFile(t, filepath.Join(root, "openspec", "config.yaml"), "{}")

	assertLines(t, openSpecPhase(), []string{
		"OpenSpec: INITIALIZED — verified via openspec/config.yaml (0 specs, 0 active changes)",
	})
}

func TestOpenSpecPhase_OneChange_ReadyForPlan(t *testing.T) {
	branch := "feat/openspec-one"
	root := gitFixture(t, branch)
	changeDir := filepath.Join(root, "openspec", "changes", "add-widget")
	mustMkdirAll(t, changeDir)
	mustWriteFile(t, filepath.Join(root, "openspec", "config.yaml"), "{}")
	mustWriteFile(t, filepath.Join(changeDir, "proposal.md"), "# proposal\n")
	mustWriteFile(t, filepath.Join(changeDir, "tasks.md"), "- [ ] task one\n- [ ] task two\n")

	assertLines(t, openSpecPhase(), []string{
		`OpenSpec: INITIALIZED (openspec/config.yaml, 0 specs) · active: change "add-widget" (ready for implementation (2 tasks), 0 delta specs)`,
		"  Plan with: /plan --from-openspec add-widget",
		"  Or full pipeline: /ship (after planning)",
	})
}

// ---------------------------------------------------------------------------
// Git context phase
// ---------------------------------------------------------------------------

func TestGitContextPhase_CleanAndDirty(t *testing.T) {
	branch := "feat/git-context"
	root := gitFixture(t, branch)

	t.Run("clean", func(t *testing.T) {
		assertLines(t, gitContextPhase(), []string{"Git: branch " + branch + " (clean) [snapshot]"})
	})

	t.Run("dirty", func(t *testing.T) {
		mustWriteFile(t, filepath.Join(root, "new-file.txt"), "x")
		assertLines(t, gitContextPhase(), []string{"Git: branch " + branch + " (1 file modified) [snapshot]"})
	})
}

// ---------------------------------------------------------------------------
// Jira cache phase
// ---------------------------------------------------------------------------

func TestJiraCachePhase(t *testing.T) {
	fakeHome := realPath(t, t.TempDir())
	t.Setenv("HOME", fakeHome)

	siteDir := filepath.Join(fakeHome, ".sdlc-cache", "jira", "cleeng_atlassian_net")
	mustMkdirAll(t, siteDir)

	writeCacheFile := func(name string, ageHours float64, maxAgeHours float64) {
		cache := map[string]any{
			"lastUpdated": time.Now().Add(-time.Duration(ageHours * float64(time.Hour))).UTC().Format(time.RFC3339Nano),
			"maxAgeHours": maxAgeHours,
		}
		raw, err := json.Marshal(cache)
		if err != nil {
			t.Fatal(err)
		}
		mustWriteFile(t, filepath.Join(siteDir, name), string(raw))
	}

	writeCacheFile("INT.json", 2, 24)   // fresh
	writeCacheFile("OPS.json", 48, 24)  // stale
	writeCacheFile("PERM.json", 240, 0) // permanent

	joined := strings.Join(jiraCachePhase(), "\n")
	for _, want := range []string{
		"Jira cache: INT@cleeng_atlassian_net (last updated 2h ago, TTL 24h)",
		"Jira cache: OPS@cleeng_atlassian_net (stale — 2 days ago, TTL 24h) — refresh with /jira --force-refresh",
		"Jira cache: PERM@cleeng_atlassian_net (last updated 10 days ago, permanent)",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("jiraCachePhase output missing %q\ngot:\n%s", want, joined)
		}
	}
}

// ---------------------------------------------------------------------------
// Ship config phase
// ---------------------------------------------------------------------------

func TestShipConfigPhase(t *testing.T) {
	branch := "feat/ship-config"
	root := gitFixture(t, branch)

	localPath := filepath.Join(root, paths.DataDir, "local.json")
	mustMkdirAll(t, filepath.Dir(localPath))
	raw := `{"ship": {"steps": ["review", "commit"], "preset": "A", "skip": ["docs"], "bump": "minor", "reviewThreshold": 80}}`
	mustWriteFile(t, localPath, raw)

	assertLines(t, shipConfigPhase(), []string{
		`Ship config: steps ["review","commit"], preset full, skip ["docs"], bump minor, threshold 80`,
	})
}

// ---------------------------------------------------------------------------
// Per-phase fail-open isolation (a defect in one phase must not blank the
// header or any other phase's output)
// ---------------------------------------------------------------------------

func TestSessionStart_PhaseIsolation(t *testing.T) {
	branch := "feat/phase-isolation"
	root := gitFixture(t, branch)
	t.Setenv("HOME", realPath(t, t.TempDir()))
	slug := state.SlugifyBranch(branch)

	// Break the pipeline-resume phase's input: a ship-state file matching
	// the state filename grammar, but containing invalid JSON, so
	// state.Find returns a parse error and pipelineResumePhase must
	// degrade to no lines rather than propagate the failure.
	execDir := filepath.Join(root, paths.DataDir, "execution")
	mustMkdirAll(t, execDir)
	mustWriteFile(t, filepath.Join(execDir, "ship-"+slug+"-20260101T000000Z.json"), "{not valid json")

	// A healthy OpenSpec fixture, so a working phase's output is verifiable
	// alongside the broken one.
	mustMkdirAll(t, filepath.Join(root, "openspec"))
	mustWriteFile(t, filepath.Join(root, "openspec", "config.yaml"), "{}")

	out, err := sessionStart(HookCtx{}, Event{Source: "startup"})
	if err != nil {
		t.Fatalf("sessionStart returned error: %v", err)
	}
	if strings.Contains(out.PlainText, "Active pipeline:") {
		t.Errorf("corrupt ship-state should not surface a pipeline-resume line:\n%s", out.PlainText)
	}
	if !strings.Contains(out.PlainText, "OpenSpec: INITIALIZED") {
		t.Errorf("healthy openspec phase output missing despite unrelated phase failure:\n%s", out.PlainText)
	}
	if !strings.Contains(out.PlainText, "Plan mode routing:") {
		t.Errorf("header line missing despite unrelated phase failure:\n%s", out.PlainText)
	}
}

// ---------------------------------------------------------------------------
// small fixture-writing helpers
// ---------------------------------------------------------------------------

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
