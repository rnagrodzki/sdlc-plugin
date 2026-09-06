package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/frontmatter"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// sessionStart is the "session-start" hook handler, ported from
// hooks/session-start.js. It outputs the plugin version, skill count, and
// project context (pipeline resume, OpenSpec, git status, Jira cache, ship
// config) as plain text for Claude Code's SessionStart system-reminder.
//
// Every phase below degrades independently and silently on error — a
// problem in one phase (missing repo, unreadable cache, corrupt state file)
// never suppresses another phase's output, mirroring the JS source's
// per-phase try/catch structure. Phases that can panic on unexpected input
// are wrapped with recover() for the same reason; safePhase's fail-open
// convention is documented on its own doc comment below.
func sessionStart(_ HookCtx, event Event) (Output, error) {
	var header []string

	pluginRoot, rootOK := safeResolvePluginRoot()
	if rootOK {
		count := safeCountSkills(pluginRoot)
		header = append(header, fmt.Sprintf("sdlc: v%s (%d skills loaded)", PluginVersion, count))
	}

	header = append(header, "Plan mode routing: always invoke plan via the Skill tool when plan mode is active.")

	// Judgment call (Task 37, Ruling B leaves this open): the ruling only
	// says to skip "the skill-count line" when resolvePluginRoot fails.
	// Printing this line with an empty root string would be equally
	// dishonest, so it is omitted together with the skill-count line rather
	// than shown with a blank value.
	if rootOK {
		header = append(header, fmt.Sprintf("sdlc plugin root: %s", pluginRoot))
	}

	var resume []string
	resume = append(resume, safeStringsPhase("pipeline-resume", func() []string { return pipelineResumePhase(event.Source) })...)
	resume = append(resume, safeStringsPhase("compact-recovery", compactRecoveryPhase)...)
	resume = append(resume, safeStringsPhase("openspec", openSpecPhase)...)
	resume = append(resume, safeStringsPhase("git", gitContextPhase)...)
	resume = append(resume, safeStringsPhase("jira-cache", jiraCachePhase)...)
	resume = append(resume, safeStringsPhase("ship-config", shipConfigPhase)...)

	lines := make([]string, 0, len(header)+len(resume))
	lines = append(lines, header...)
	lines = append(lines, resume...)

	return Output{PlainText: strings.Join(lines, "\n") + "\n", ExitCode: 0}, nil
}

// safeStringsPhase runs fn and recovers any panic, logging it to stderr and
// returning nil (i.e. the phase contributes no lines) instead of letting the
// panic escape. This is the per-phase equivalent of the JS source's
// try { ... } catch { /* degrade */ } blocks: a defect in one phase must
// never blank out the header or any other phase's output.
func safeStringsPhase(name string, fn func() []string) (lines []string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[sdlc/session-start] %s: panic: %v\n", name, r)
		}
	}()
	return fn()
}

func safeResolvePluginRoot() (root string, ok bool) {
	defer func() { recover() }()
	return resolvePluginRoot()
}

func safeCountSkills(pluginRoot string) (count int) {
	defer func() { recover() }()
	return countUserInvocableSkills(pluginRoot)
}

// resolveActiveWorktreeSafe returns a best-effort "current worktree"
// directory for git operations, never failing. worktree.ActiveRoot returns
// (\"\", err) by design (see internal/worktree's doc comment) so that most
// callers can decide how to react; the JS source's own "Safe"-suffixed
// helpers never throw, always falling back to the raw cwd. This is that
// fallback for the hook package, where every phase must degrade rather than
// abort.
func resolveActiveWorktreeSafe() string {
	if root, err := worktree.ActiveRoot(); err == nil && root != "" {
		return root
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return ""
}

// ---------------------------------------------------------------------------
// Phase: plugin root + skill count (Ruling B)
// ---------------------------------------------------------------------------

// pluginWalkSkipDirs prunes directories that can never contain a plugin
// manifest but can be very large (vendored checkouts, node_modules), mirroring
// plan.go's skillWalkSkipDirs convention for the same ~/.claude/plugins walk.
var pluginWalkSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".cache":       true,
	"dist":         true,
	"build":        true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
}

// resolvePluginRoot locates the installed sdlc plugin's root directory (the
// one containing .claude-plugin/plugin.json and, typically, a skills/
// subdirectory). Unlike the JS hook, which derives this trivially from
// __dirname, a compiled Go binary has no notion of "the directory containing
// this script" that also doubles as the plugin root — so this is a genuine
// port gap (Task 37, Ruling B), resolved via two strategies:
//
//  1. Walk up from the running binary's own directory, and separately from
//     the current working directory, looking for .claude-plugin/plugin.json.
//     This covers the common case where the compiled binary lives inside (or
//     is invoked from within) the plugin's own repo, which doubles as the
//     installed plugin root when marketplace.json declares source: ".".
//  2. Fall back to walking ~/.claude/plugins for a directory whose own
//     .claude-plugin/plugin.json declares name "sdlc" (this binary's own
//     plugin name), mirroring plan.go's resolveSkillTemplate convention for
//     locating installed plugin content.
//
// Returns ("", false) if neither resolves — callers must fail open (skip the
// output that depends on it) rather than print a fake or empty root.
func resolvePluginRoot() (string, bool) {
	if exe, err := os.Executable(); err == nil {
		if root, ok := walkUpForPluginManifest(filepath.Dir(exe)); ok {
			return root, true
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if root, ok := walkUpForPluginManifest(cwd); ok {
			return root, true
		}
	}
	return findPluginByNameUnderHome("sdlc")
}

// walkUpForPluginManifest walks upward from start looking for a directory
// containing .claude-plugin/plugin.json, returning that directory.
func walkUpForPluginManifest(start string) (string, bool) {
	dir := start
	for {
		manifest := filepath.Join(dir, ".claude-plugin", "plugin.json")
		if fi, err := os.Stat(manifest); err == nil && !fi.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// findPluginByNameUnderHome walks ~/.claude/plugins looking for a directory
// whose .claude-plugin/plugin.json declares the given plugin name.
func findPluginByNameUnderHome(name string) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	pluginsRoot := filepath.Join(home, ".claude", "plugins")

	var found string
	_ = filepath.WalkDir(pluginsRoot, func(p string, d os.DirEntry, err error) error {
		if found != "" {
			return filepath.SkipAll
		}
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if pluginWalkSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "plugin.json" || filepath.Base(filepath.Dir(p)) != ".claude-plugin" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		var manifest struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return nil
		}
		if manifest.Name == name {
			found = filepath.Dir(filepath.Dir(p)) // parent of .claude-plugin
		}
		return nil
	})

	if found == "" {
		return "", false
	}
	return found, true
}

// countUserInvocableSkills counts skills/*/SKILL.md files under pluginRoot
// whose YAML frontmatter declares "user-invocable: true".
func countUserInvocableSkills(pluginRoot string) int {
	skillsDir := filepath.Join(pluginRoot, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return 0
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(skillsDir, e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		meta, _, err := frontmatter.Parse(raw)
		if err != nil {
			continue
		}
		if v, ok := meta["user-invocable"].(bool); ok && v {
			count++
		}
	}
	return count
}

// ---------------------------------------------------------------------------
// Phase: pipeline resume detection (ship/execute state)
// ---------------------------------------------------------------------------

func pipelineResumePhase(source string) []string {
	root, err := worktree.MainRoot()
	if err != nil {
		return nil
	}
	branch, err := gitx.CurrentBranch(resolveActiveWorktreeSafe())
	if err != nil || branch == "" || branch == "HEAD" {
		return nil
	}

	var lines []string
	lines = append(lines, shipResumeLines(root, branch)...)
	lines = append(lines, executeResumeLines(root, branch, source)...)
	return lines
}

// sameWorktree reports whether stateData's recorded worktree (if any)
// resolves to the same real path as the active worktree. It fails open
// (returns true — keep showing the banner) when the field is absent (state
// files written before Task 9 added it) or on any resolution error, mirroring
// session-start.js's own sameWorktree helper exactly.
func sameWorktree(data map[string]any) bool {
	wt, _ := data["worktree"].(string)
	if wt == "" {
		return true
	}
	a, err := filepath.EvalSymlinks(wt)
	if err != nil {
		return true
	}
	b, err := filepath.EvalSymlinks(resolveActiveWorktreeSafe())
	if err != nil {
		return true
	}
	return a == b
}

func shipResumeLines(root, branch string) []string {
	st, err := state.Find(root, "ship", branch)
	if err != nil || st == nil {
		return nil
	}
	steps, ok := st.Data["steps"].([]any)
	if !ok {
		return nil
	}

	inProgressIdx, lastCompletedIdx := -1, -1
	for i, s := range steps {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		status, _ := sm["status"].(string)
		if status == "in_progress" && inProgressIdx == -1 {
			inProgressIdx = i
		}
		if status == "completed" {
			lastCompletedIdx = i
		}
	}

	idx := lastCompletedIdx
	inProgress := false
	if inProgressIdx != -1 {
		idx = inProgressIdx
		inProgress = true
	}
	if idx == -1 {
		return nil
	}

	if !sameWorktree(st.Data) {
		return nil
	}

	sm, _ := steps[idx].(map[string]any)
	name, _ := sm["name"].(string)
	if name == "" {
		if id, ok := sm["id"].(string); ok && id != "" {
			name = id
		} else {
			name = "unknown"
		}
	}

	stepIndex := idx + 1
	var label string
	if inProgress {
		label = fmt.Sprintf("paused at step %d: %s", stepIndex, name)
	} else {
		label = fmt.Sprintf("last completed step %d: %s", stepIndex, name)
	}

	return []string{
		fmt.Sprintf("Active pipeline: ship on %s (%s)", branch, label),
		"  Resume with: /ship --resume",
	}
}

func executeResumeLines(root, branch, source string) []string {
	st, err := state.Find(root, "execute", branch)
	if err != nil || st == nil {
		return nil
	}
	waves, ok := st.Data["waves"].([]any)
	if !ok {
		return nil
	}
	if !sameWorktree(st.Data) {
		return nil
	}

	completed := 0
	for _, w := range waves {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		if status, _ := wm["status"].(string); status == "completed" {
			completed++
		}
	}
	total := len(waves)

	var line string
	if source == "compact" {
		line = fmt.Sprintf("Active execution (post-compact): execute-plan on %s (wave %d of %d complete)", branch, completed, total)
	} else {
		line = fmt.Sprintf("Active execution: execute-plan on %s (wave %d of %d complete)", branch, completed, total)
	}
	return []string{line, "  Resume with: /execute-plan --resume"}
}

// ---------------------------------------------------------------------------
// Phase: compact-recovery sidecar consume + stale sweeps
// ---------------------------------------------------------------------------

// compactRecoveryTTL mirrors state's own unexported recoverySidecarTTL
// (internal/state/sidecar.go). Duplicated here because that constant isn't
// exported; used only for the legacy no-suffix sidecar's age gate below —
// the per-branch sidecar's own freshness gate is enforced inside
// state.ConsumeRecoverySidecar.
const compactRecoveryTTL = time.Hour

// staleSidecarThreshold is the age past which an orphaned per-branch sidecar
// (never consumed — JSON parse error, permission failure, killed session) is
// swept regardless of its own TTL semantics.
const staleSidecarThreshold = 24 * time.Hour

var staleSidecarNameRe = regexp.MustCompile(`^\.(?:compact-recovery|stop-block-count)-.+\.json$`)

func compactRecoveryPhase() []string {
	root, err := worktree.MainRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[sdlc/session-start] compact-recovery consumption failed: %v\n", err)
		return nil
	}
	dir := filepath.Join(root, ".sdlc", "execution")

	var lines []string
	if branch, err := gitx.CurrentBranch(resolveActiveWorktreeSafe()); err == nil && branch != "" && branch != "HEAD" {
		slug := state.SlugifyBranch(branch)
		// ConsumeRecoverySidecar deletes the sidecar (single-use) whether it
		// was fresh or expired, or is a no-op if none exists — matching the
		// JS source's always-unlink-after-read behavior. Both an expired
		// sidecar and a corrupt/unreadable one surface as a non-nil error
		// here; state doesn't export a way to distinguish the two, so
		// (unlike the JS source's console.error for the corrupt case only)
		// this deviation stays silent for both — a disclosed simplification,
		// not a behavior gap in what gets shown or cleaned up.
		if data, err := state.ConsumeRecoverySidecar(root, slug); err == nil && data != nil {
			if rm, ok := data.(map[string]any); ok {
				lines = append(lines, "Pipeline state recovered after compaction:")
				lines = append(lines, recoveryPipelineLines(rm)...)
			}
		}
	}

	sweepStaleSidecars(dir)
	cleanupLegacySidecar(dir)

	return lines
}

func recoveryPipelineLines(rm map[string]any) []string {
	pipeline, _ := rm["pipeline"].(string)
	branch, _ := rm["branch"].(string)

	switch pipeline {
	case "ship":
		var lines []string
		lines = append(lines, fmt.Sprintf("  Pipeline: ship on %s", branch))
		if step, ok := rm["currentStep"].(string); ok && step != "" {
			lines = append(lines, fmt.Sprintf("  Current step: %s", step))
		}
		if verdict, ok := rm["reviewVerdict"].(string); ok && verdict != "" {
			findings := ""
			if n, ok := rm["deferredFindings"].(float64); ok && n != 0 {
				findings = fmt.Sprintf(" (%s deferred)", formatNumber(n))
			}
			lines = append(lines, fmt.Sprintf("  Review verdict: %s%s", verdict, findings))
		}
		return lines
	case "execute-plan":
		completed, _ := rm["completedWaves"].(float64)
		total, _ := rm["totalWaves"].(float64)
		return []string{
			fmt.Sprintf("  Pipeline: execute-plan on %s", branch),
			fmt.Sprintf("  Progress: wave %s of %s complete", formatNumber(completed), formatNumber(total)),
		}
	default:
		return nil
	}
}

// sweepStaleSidecars removes per-branch compact-recovery/stop-block-count
// sidecars older than 24h — files orphaned by a session that died before its
// own single-use unlink fired. Each entry's stat/remove failure is logged
// and skipped independently (matching the JS source's per-entry try/catch);
// a directory-read failure is logged once and the sweep is skipped entirely.
func sweepStaleSidecars(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[sdlc/session-start] stale compact-recovery sweep error: %v\n", err)
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !staleSidecarNameRe.MatchString(name) {
			continue
		}
		full := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[sdlc/session-start] stale compact-recovery sweep error: %v\n", err)
			continue
		}
		if time.Since(info.ModTime()) > staleSidecarThreshold {
			if err := os.Remove(full); err != nil {
				fmt.Fprintf(os.Stderr, "[sdlc/session-start] stale compact-recovery sweep error: %v\n", err)
			}
		}
	}
}

// cleanupLegacySidecar removes the pre-#256, no-branch-suffix
// .compact-recovery.json file once it is older than the normal TTL. It is
// never re-injected — only ever cleaned up.
func cleanupLegacySidecar(dir string) {
	legacyPath := filepath.Join(dir, ".compact-recovery.json")
	info, err := os.Stat(legacyPath)
	if err != nil {
		return
	}
	if time.Since(info.ModTime()) > compactRecoveryTTL {
		if err := os.Remove(legacyPath); err != nil {
			fmt.Fprintf(os.Stderr, "[sdlc/session-start] legacy compact-recovery cleanup error: %v\n", err)
		}
	}
}

// formatNumber renders a float64 the way a JS template literal would render
// a JSON number: no trailing ".0" for whole numbers, no exponent notation.
func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ---------------------------------------------------------------------------
// Phase: OpenSpec context
// ---------------------------------------------------------------------------

func openSpecPhase() []string {
	contentRoot, err := worktree.ActiveRoot()
	if err != nil {
		return nil
	}
	info := openspec.DetectActiveChanges(contentRoot)
	if !info.Present {
		return nil
	}

	specPlural := pluralS(info.SpecsCount)

	switch len(info.ActiveChanges) {
	case 0:
		return []string{fmt.Sprintf("OpenSpec: INITIALIZED — verified via openspec/config.yaml (%d spec%s, 0 active changes)", info.SpecsCount, specPlural)}

	case 1:
		change := info.ActiveChanges[0]
		lines := []string{fmt.Sprintf(
			"OpenSpec: INITIALIZED (openspec/config.yaml, %d spec%s) · active: change \"%s\" (%s, %d delta spec%s)",
			info.SpecsCount, specPlural, change.Name, stageLabel(change), change.DeltaSpecCount, pluralS(change.DeltaSpecCount),
		)}

		if info.BranchMatch != nil && *info.BranchMatch == change.Name {
			if branch, err := gitx.CurrentBranch(resolveActiveWorktreeSafe()); err == nil && branch != "" && branch != "HEAD" {
				lines = append(lines, fmt.Sprintf("  Branch match: %s -> auto-linked", branch))
			}
		}

		stage := ""
		if change.Stage != nil {
			stage = *change.Stage
		}
		switch stage {
		case "spec-in-progress":
			// No suggestion — matches the JS source's empty case.
		case "ready-for-plan":
			lines = append(lines, fmt.Sprintf("  Plan with: /plan --from-openspec %s", change.Name))
			lines = append(lines, "  Or full pipeline: /ship (after planning)")
		case "implementation-in-progress":
			lines = append(lines, "  Or commit progress: /commit")
		case "tasks-complete":
			lines = append(lines, "  Ship: /ship (commit -> review -> PR)")
			lines = append(lines, fmt.Sprintf("  Verify first: openspec validate --strict %s", change.Name))
		}
		return lines

	default:
		names := make([]string, len(info.ActiveChanges))
		for i, c := range info.ActiveChanges {
			names[i] = c.Name
		}
		return []string{
			fmt.Sprintf("OpenSpec: INITIALIZED (openspec/config.yaml, %d spec%s) · active: %d changes (%s)", info.SpecsCount, specPlural, len(info.ActiveChanges), strings.Join(names, ", ")),
			"  Pass --spec or --from-openspec <name> to select",
		}
	}
}

// stageLabel mirrors lib/openspec.js's STAGE_LABELS map.
func stageLabel(change openspec.OpenspecChangeInfo) string {
	stage := ""
	if change.Stage != nil {
		stage = *change.Stage
	}
	switch stage {
	case "spec-in-progress":
		return "spec in progress"
	case "ready-for-plan":
		return fmt.Sprintf("ready for implementation (%d tasks)", change.TasksTotal)
	case "implementation-in-progress":
		return fmt.Sprintf("implementing (%d/%d tasks done)", change.TasksDone, change.TasksTotal)
	case "tasks-complete":
		return "tasks complete"
	default:
		return stage
	}
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---------------------------------------------------------------------------
// Phase: git context
// ---------------------------------------------------------------------------

func gitContextPhase() []string {
	dir := resolveActiveWorktreeSafe()

	branch, err := gitx.CurrentBranch(dir)
	if err != nil {
		return nil
	}
	status, err := gitx.Status(dir)
	if err != nil {
		return nil
	}

	dirtyCount := countNonEmptyLines(status)
	var dirtyLabel string
	if dirtyCount > 0 {
		dirtyLabel = fmt.Sprintf("%d file%s modified", dirtyCount, pluralS(dirtyCount))
	} else {
		dirtyLabel = "clean"
	}

	aheadLabel := ""
	if base, err := gitx.DefaultBranch(dir); err == nil {
		if out, err := execx.Run("git", []string{"rev-list", "--count", "origin/" + base + "..HEAD"}, execx.Options{Dir: dir}); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil && n > 0 {
				aheadLabel = fmt.Sprintf("ahead of %s by %d", base, n)
			}
		}
	}

	parts := []string{dirtyLabel}
	if aheadLabel != "" {
		parts = append(parts, aheadLabel)
	}
	return []string{fmt.Sprintf("Git: branch %s (%s) [snapshot]", branch, strings.Join(parts, ", "))}
}

func countNonEmptyLines(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Phase: Jira cache freshness
// ---------------------------------------------------------------------------

// jiraCachePhase walks ~/.sdlc-cache/jira/<site>/<PROJECT_KEY>.json directly.
// This is a third independent copy of that walk (internal/tools/jira.go and
// internal/links/links.go each already have their own) — consistent with an
// existing repo convention rather than a new smell (no shared helper exists
// yet to extract it into). Unlike links.go's discoverJiraSiteFromCache, the
// site label here is the RAW sanitized directory name, not de-sanitized back
// to dots — matching session-start.js's own `site: siteEntry.name` exactly.
func jiraCachePhase() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	root := filepath.Join(home, ".sdlc-cache", "jira")
	siteDirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var lines []string
	for _, siteEntry := range siteDirs {
		if !siteEntry.IsDir() {
			continue
		}
		site := siteEntry.Name()
		siteDir := filepath.Join(root, site)

		files, err := os.ReadDir(siteDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			if line, ok := jiraCacheLine(site, siteDir, f.Name()); ok {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func jiraCacheLine(site, siteDir, fileName string) (string, bool) {
	projectKey := strings.TrimSuffix(fileName, ".json")

	raw, err := os.ReadFile(filepath.Join(siteDir, fileName))
	if err != nil {
		return "", false
	}
	var cache map[string]any
	if err := json.Unmarshal(raw, &cache); err != nil {
		return "", false
	}

	lastUpdated, _ := cache["lastUpdated"].(string)
	if lastUpdated == "" {
		return "", false
	}
	t, err := time.Parse(time.RFC3339Nano, lastUpdated)
	if err != nil {
		return "", false
	}

	ageHours := time.Since(t).Hours()
	maxAgeHours := 0.0
	if v, ok := cache["maxAgeHours"].(float64); ok {
		maxAgeHours = v
	}

	label := fmt.Sprintf("%s@%s", projectKey, site)
	ageDisplay := jiraAgeDisplay(ageHours)

	switch {
	case maxAgeHours == 0:
		return fmt.Sprintf("Jira cache: %s (last updated %s, permanent)", label, ageDisplay), true
	case ageHours > maxAgeHours:
		return fmt.Sprintf("Jira cache: %s (stale — %s, TTL %sh) — refresh with /jira --force-refresh", label, ageDisplay, formatNumber(maxAgeHours)), true
	default:
		return fmt.Sprintf("Jira cache: %s (last updated %s, TTL %sh)", label, ageDisplay, formatNumber(maxAgeHours)), true
	}
}

func jiraAgeDisplay(ageHours float64) string {
	switch {
	case ageHours < 1:
		return "less than 1h ago"
	case ageHours < 24:
		return fmt.Sprintf("%dh ago", int(round(ageHours)))
	default:
		d := int(round(ageHours / 24))
		return fmt.Sprintf("%d day%s ago", d, pluralS(d))
	}
}

func round(f float64) float64 {
	if f < 0 {
		return -round(-f)
	}
	return float64(int64(f + 0.5))
}

// ---------------------------------------------------------------------------
// Phase: ship config
// ---------------------------------------------------------------------------

// legacyPresetMap mirrors lib/config.js's normalizePreset: legacy single-
// letter presets (from before named presets existed) map to their current
// name; anything else passes through unchanged.
var legacyPresetMap = map[string]string{
	"A": "full",
	"B": "balanced",
	"C": "minimal",
}

func normalizePresetValue(v any) string {
	s, ok := v.(string)
	if !ok {
		return jsStringify(v)
	}
	if mapped, ok := legacyPresetMap[strings.ToUpper(s)]; ok {
		return mapped
	}
	return s
}

func jsStringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if f, ok := v.(float64); ok {
		return formatNumber(f)
	}
	return fmt.Sprintf("%v", v)
}

func shipConfigPhase() []string {
	root, err := worktree.MainRoot()
	if err != nil {
		return nil
	}
	section, err := config.ReadSection(root, "ship")
	if err != nil || section == nil {
		return nil
	}

	var parts []string

	if raw, ok := section["steps"]; ok {
		if arr, ok2 := raw.([]any); ok2 {
			if b, err := json.Marshal(arr); err == nil {
				parts = append(parts, "steps "+string(b))
			}
		}
	}
	if raw, ok := section["preset"]; ok {
		parts = append(parts, "preset "+normalizePresetValue(raw))
	}
	if raw, ok := section["skip"]; ok {
		if b, err := json.Marshal(raw); err == nil {
			parts = append(parts, "skip "+string(b))
		}
	}
	if raw, ok := section["bump"]; ok {
		parts = append(parts, "bump "+jsStringify(raw))
	}
	if raw, ok := section["reviewThreshold"]; ok {
		parts = append(parts, "threshold "+jsStringify(raw))
	}

	if len(parts) == 0 {
		return nil
	}
	return []string{"Ship config: " + strings.Join(parts, ", ")}
}
