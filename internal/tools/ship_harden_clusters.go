package tools

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// ---------------------------------------------------------------------------
// Action: harden_clusters
// ---------------------------------------------------------------------------

// HardenClusterFinding is one review finding fed into harden_clusters via
// detail.findings. Verdict is the reviewing agent's own judgment on the
// finding (received-review's vocabulary); Reason, when present, is one of
// history.DeferredReasons() — the same field shipStateDefer records.
type HardenClusterFinding struct {
	File     string `json:"file"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Verdict  string `json:"verdict"`          // agree-will-fix | agree-won't-fix | disagree | needs-direction
	Reason   string `json:"reason,omitempty"` // history.DeferredReasons()
}

// HardenCluster groups every surviving finding for one file. FailureText is
// the rendered prompt handed to harden's fix step; AlreadyHardened is true
// when a prior harden run already recorded this exact failure as its
// trigger.
type HardenCluster struct {
	Key             string                 `json:"key"`
	Findings        []HardenClusterFinding `json:"findings"`
	FailureText     string                 `json:"failureText"`
	AlreadyHardened bool                   `json:"alreadyHardened"`
}

// HardenClustersOut is the output of ship_state's harden_clusters action.
type HardenClustersOut struct {
	Clusters      []HardenCluster `json:"clusters"`      // never null
	Suppressed    []string        `json:"suppressed"`    // file paths, never null
	LoneDisagree  []string        `json:"loneDisagree"`  // file paths, never null
	DirtySurfaces []string        `json:"dirtySurfaces"` // never null
	Narration     string          `json:"narration"`
	Next          string          `json:"next"` // e.g. "dispatch 2 cluster(s) with alreadyHardened:false, one at a time" | "no clusters — skip"
}

// hardenVerdicts are the accepted values for a finding's verdict field.
var hardenVerdicts = []string{"agree-will-fix", "agree-won't-fix", "disagree", "needs-direction"}

// hardenMaxClusters caps how many clusters one harden_clusters call reports
// — the harden pipeline dispatches clusters one at a time, and a run with
// dozens of findings needs a bound on how many it will chase in one pass.
const hardenMaxClusters = 5

// hardenFailureTextCap is the character cap on a cluster's rendered
// FailureText — the budget harden's own fix-step prompt allows this text.
const hardenFailureTextCap = 4096

// hardenTriggerPrefixLen is how much of FailureText is compared against a
// recorded healing.hardened[].trigger to decide AlreadyHardened. Harden
// itself only ever wrote a trigger from a FailureText this function
// produced, so comparing a stable-length prefix is enough to recognize a
// repeat without storing (and comparing) the full text.
const hardenTriggerPrefixLen = 200

// shipStateHardenClusters groups review findings into harden clusters (see
// ship_state's harden_clusters tool-description line for the full
// contract). It is read-only: no ship state field is written. With no
// live ship run for the branch, AlreadyHardened is false for every
// cluster — harden_clusters works standalone, outside /ship, exactly like
// healing_record's read side.
func shipStateHardenClusters(root, workDir string, in ShipStateIn) (any, error) {
	findings, err := hardenParseFindings(in.Detail)
	if err != nil {
		return nil, err
	}

	clusters, suppressed, loneDisagree := hardenBuildClusters(findings)

	triggers, err := hardenLiveTriggers(root, workDir, detailStr(in.Detail, "branch"))
	if err != nil {
		return nil, err
	}
	for i := range clusters {
		clusters[i].AlreadyHardened = triggers[hardenCapRunes(clusters[i].FailureText, hardenTriggerPrefixLen)]
	}

	dirty, err := hardenDirtySurfaces(activeWorktreeRootSafe())
	if err != nil {
		return nil, err
	}

	return HardenClustersOut{
		Clusters:      clusters,
		Suppressed:    suppressed,
		LoneDisagree:  loneDisagree,
		DirtySurfaces: dirty,
		Narration: fmt.Sprintf(
			"harden_clusters: %d cluster(s), %d suppressed, %d lone-disagree file(s), %d dirty surface(s).",
			len(clusters), len(suppressed), len(loneDisagree), len(dirty)),
		Next: hardenClustersNext(clusters),
	}, nil
}

// hardenParseFindings validates and extracts detail.findings. Every
// finding needs a non-empty file, a severity from dimensions.ValidSeverities
// (case-insensitive, stored lowercase — mirrors shipStateDefer), and a
// verdict from hardenVerdicts; an optional reason must be one of
// history.DeferredReasons() when present.
func hardenParseFindings(d map[string]any) ([]HardenClusterFinding, error) {
	const usage = "Pass detail.findings as a JSON array of {file, severity, title, body, verdict, reason?} objects, then retry ship_state harden_clusters."

	raw, ok := d["findings"]
	if !ok || raw == nil {
		return nil, &mcpserver.DomainError{
			Msg:        "harden_clusters: detail.findings is required",
			Suggestion: usage,
		}
	}
	items, isList := raw.([]any)
	if !isList {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("harden_clusters: detail.findings must be an array, got %T", raw),
			Suggestion: usage,
		}
	}

	severities := strings.Join(dimensions.ValidSeverities, " | ")
	verdicts := strings.Join(hardenVerdicts, " | ")
	reasons := strings.Join(history.DeferredReasons(), " | ")

	findings := make([]HardenClusterFinding, 0, len(items))
	for i, item := range items {
		m, isObj := item.(map[string]any)
		if !isObj {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("harden_clusters: detail.findings[%d] must be an object, got %T", i, item),
				Suggestion: fmt.Sprintf("Pass detail.findings[%d] as a {file, severity, title, body, verdict, reason?} object, then retry ship_state harden_clusters.", i),
			}
		}

		file, _ := m["file"].(string)
		if strings.TrimSpace(file) == "" {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("harden_clusters: detail.findings[%d].file is required and must be a non-empty string", i),
				Suggestion: fmt.Sprintf("Set detail.findings[%d].file to the finding's file path, then retry ship_state harden_clusters.", i),
			}
		}

		rawSeverity, _ := m["severity"].(string)
		severity := strings.ToLower(strings.TrimSpace(rawSeverity))
		if !slices.Contains(dimensions.ValidSeverities, severity) {
			return nil, &mcpserver.DomainError{
				Msg: fmt.Sprintf("harden_clusters: detail.findings[%d].severity %q is not a recognised review severity — accepted values are %s",
					i, rawSeverity, severities),
				Suggestion: fmt.Sprintf("Set detail.findings[%d].severity to one of %s (case-insensitive), then retry ship_state harden_clusters.", i, severities),
			}
		}

		verdict, _ := m["verdict"].(string)
		if !slices.Contains(hardenVerdicts, verdict) {
			return nil, &mcpserver.DomainError{
				Msg: fmt.Sprintf("harden_clusters: detail.findings[%d].verdict %q is not a recognised verdict — accepted values are %s",
					i, verdict, verdicts),
				Suggestion: fmt.Sprintf("Set detail.findings[%d].verdict to one of %s, then retry ship_state harden_clusters.", i, verdicts),
			}
		}

		var reason string
		if v, ok := m["reason"]; ok && v != nil {
			s, isStr := v.(string)
			if !isStr {
				return nil, &mcpserver.DomainError{
					Msg:        fmt.Sprintf("harden_clusters: detail.findings[%d].reason must be a string, got %T", i, v),
					Suggestion: fmt.Sprintf("Set detail.findings[%d].reason to one of %s, or omit it entirely, then retry ship_state harden_clusters.", i, reasons),
				}
			}
			reason = s
		}
		if reason != "" && !history.ValidDeferredReason(reason) {
			return nil, &mcpserver.DomainError{
				Msg: fmt.Sprintf("harden_clusters: detail.findings[%d].reason %q is not a recognised deferral reason — accepted values are %s",
					i, reason, reasons),
				Suggestion: fmt.Sprintf("Set detail.findings[%d].reason to one of %s, or omit it entirely, then retry ship_state harden_clusters.", i, reasons),
			}
		}

		title, _ := m["title"].(string)
		body, _ := m["body"].(string)
		findings = append(findings, HardenClusterFinding{
			File: file, Severity: severity, Title: title, Body: body, Verdict: verdict, Reason: reason,
		})
	}
	return findings, nil
}

// hardenIsDisagree reports whether a finding counts as "disagree" for the
// lone-disagree rule — by its own verdict, or by a reason the reviewer
// recorded as disagree instead (received-review lets a human record either).
func hardenIsDisagree(f HardenClusterFinding) bool {
	return f.Verdict == "disagree" || f.Reason == history.ReasonDisagree
}

// hardenBuildClusters groups findings by file (below-threshold findings are
// dropped outright — they were already routed out of the fix loop by
// severity, so harden should never see them). A file whose lone finding is
// a disagree is set aside in loneDisagree instead of becoming a
// single-finding cluster nobody asked to fix; a file with two or more
// disagree findings still clusters normally. When more than
// hardenMaxClusters files remain, the ones with the fewest findings (ties
// broken alphabetically) are moved to suppressed instead of dropped
// silently.
func hardenBuildClusters(findings []HardenClusterFinding) (clusters []HardenCluster, suppressed, loneDisagree []string) {
	suppressed = []string{}
	loneDisagree = []string{}

	order := make([]string, 0, len(findings))
	byFile := map[string][]HardenClusterFinding{}
	for _, f := range findings {
		if f.Reason == history.ReasonBelowThreshold {
			continue
		}
		if _, seen := byFile[f.File]; !seen {
			order = append(order, f.File)
		}
		byFile[f.File] = append(byFile[f.File], f)
	}

	type candidate struct {
		file     string
		findings []HardenClusterFinding
	}
	candidates := make([]candidate, 0, len(order))
	for _, file := range order {
		fs := byFile[file]
		if len(fs) == 1 && hardenIsDisagree(fs[0]) {
			loneDisagree = append(loneDisagree, file)
			continue
		}
		candidates = append(candidates, candidate{file: file, findings: fs})
	}

	// Most findings first; ties alphabetical by file path — the order the
	// cap below keeps, and the order clusters are returned in.
	sort.SliceStable(candidates, func(i, j int) bool {
		if len(candidates[i].findings) != len(candidates[j].findings) {
			return len(candidates[i].findings) > len(candidates[j].findings)
		}
		return candidates[i].file < candidates[j].file
	})
	if len(candidates) > hardenMaxClusters {
		for _, c := range candidates[hardenMaxClusters:] {
			suppressed = append(suppressed, c.file)
		}
		sort.Strings(suppressed)
		candidates = candidates[:hardenMaxClusters]
	}

	clusters = make([]HardenCluster, 0, len(candidates))
	for _, c := range candidates {
		clusters = append(clusters, HardenCluster{
			Key:         c.file,
			Findings:    c.findings,
			FailureText: hardenFailureText(c.findings),
		})
	}
	return clusters, suppressed, loneDisagree
}

// hardenFailureText renders a cluster's findings into the text handed to
// harden's fix step: "[<severity>] <title> — <verdict>\n<body>" per
// finding, findings separated by a blank line, the whole text capped at
// hardenFailureTextCap characters.
//
// Titles and bodies can come from untrusted PR review comments, and callers
// pass this text inside a double-quoted --failure-text "..." argument. A
// double quote in it could close that argument early and smuggle in a flag
// such as --auto, which would switch off harden's approval gate. So every
// double quote becomes a single quote, and every backslash (which could
// escape the closing quote) becomes a slash — the text can then never leave
// the quoted argument.
func hardenFailureText(findings []HardenClusterFinding) string {
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		parts = append(parts, fmt.Sprintf("[%s] %s — %s\n%s", f.Severity, f.Title, f.Verdict, f.Body))
	}
	return hardenCapRunes(hardenQuoteSafe.Replace(strings.Join(parts, "\n\n")), hardenFailureTextCap)
}

// hardenQuoteSafe neutralizes the two characters that can end a
// double-quoted argument: " and \.
var hardenQuoteSafe = strings.NewReplacer(`"`, `'`, `\`, `/`)

// hardenCapRunes returns the first n runes of s unchanged when s already
// has n or fewer — a plain byte slice could cut a multi-byte rune in half.
func hardenCapRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// hardenLiveTriggers returns the set of healing.hardened[].trigger values
// recorded in the live ship run for branchIn, across both phases (started
// and done) — either one means harden already targeted that exact failure.
// No ship state for the branch is not an error: it returns an empty set, so
// AlreadyHardened comes back false for every cluster.
func hardenLiveTriggers(root, workDir, branchIn string) (map[string]bool, error) {
	st, err := shipResolveAndFind(branchIn, workDir, root)
	if errors.Is(err, errNoShipState) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	healing, _ := st.Data["healing"].(map[string]any)
	hardened, _ := healing["hardened"].([]any)
	triggers := make(map[string]bool, len(hardened))
	for _, h := range hardened {
		m, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["trigger"].(string); t != "" {
			triggers[t] = true
		}
	}
	return triggers, nil
}

// hardenClustersNext renders the dispatch instruction for HardenClustersOut.
func hardenClustersNext(clusters []HardenCluster) string {
	if len(clusters) == 0 {
		return "no clusters — skip"
	}
	pending := 0
	for _, c := range clusters {
		if !c.AlreadyHardened {
			pending++
		}
	}
	if pending == 0 {
		return "all clusters already hardened — commit leftover edits"
	}
	return fmt.Sprintf("dispatch %d cluster(s) with alreadyHardened:false, one at a time", pending)
}

// ---------------------------------------------------------------------------
// dirtySurfaces: harden-surface paths with uncommitted edits
// ---------------------------------------------------------------------------

// hardenSurfaceRoots are the harden-surface paths dirtySurfaces checks for
// uncommitted edits, relative to the active worktree root: guardrails (and
// the reviewThreshold default) live in config.toml, review dimensions and
// Copilot instructions are each a directory of their own files.
var hardenSurfaceRoots = []string{
	".sdlc-v2/config.toml",
	".sdlc-v2/review-dimensions",
	".github/instructions",
}

// hardenSurfaceStatus is a seam over `git status --porcelain -- <surfaces>`
// — the same execx.Run call gitx.Status makes (internal/gitx/gitx.go:64),
// plus a pathspec limiting it to hardenSurfaceRoots so unrelated dirty
// files elsewhere in the worktree never make a surface look dirty. Tests
// swap this var out instead of exercising real git (no-real-fs-git-in-tests
// guardrail).
var hardenSurfaceStatus = func(dir string) (string, error) {
	args := append([]string{"status", "--porcelain", "--"}, hardenSurfaceRoots...)
	out, err := execx.Run("git", args, execx.Options{Dir: dir})
	if err != nil {
		return "", fmt.Errorf("gitx: status: %w", err)
	}
	return out, nil
}

// hardenSurfacePath extracts the path from one `git status --porcelain`
// line: two status letters, a space, then the path — or "old -> new" for a
// rename, whose destination is what matters here.
func hardenSurfacePath(line string) string {
	line = strings.TrimRight(line, "\r")
	if len(line) < 4 {
		return strings.TrimSpace(line)
	}
	path := strings.TrimSpace(line[3:])
	if idx := strings.Index(path, " -> "); idx >= 0 {
		path = path[idx+4:]
	}
	return strings.Trim(path, `"`)
}

// hardenDirtySurfaces reports which of hardenSurfaceRoots has a pending
// edit in dir's working tree, in hardenSurfaceRoots order. A git status
// failure is a DomainError, never silently an empty list — harden must
// never skip committing a surface it failed to check.
func hardenDirtySurfaces(dir string) ([]string, error) {
	out, err := hardenSurfaceStatus(dir)
	if err != nil {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("harden_clusters: git status failed: %s", err.Error()),
			Suggestion: "Check that " + dir + " is a readable git working tree, then retry ship_state harden_clusters.",
			Cause:      err,
		}
	}
	lines := strings.Split(out, "\n")
	dirty := []string{}
	for _, root := range hardenSurfaceRoots {
		for _, line := range lines {
			if line == "" {
				continue
			}
			path := hardenSurfacePath(line)
			if path == root || strings.HasPrefix(path, root+"/") {
				dirty = append(dirty, root)
				break
			}
		}
	}
	return dirty, nil
}
