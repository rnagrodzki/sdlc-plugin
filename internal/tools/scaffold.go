package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// --- scaffold_ci ---

// scaffoldManifestEntry maps an embedded payload to its project destination.
type scaffoldManifestEntry struct {
	// PayloadKey is the key in Payloads() (e.g. "retag-release.cjs").
	PayloadKey string
	// Dest is the destination relative to project root.
	Dest string
	// LegacyDest is the legacy destination (old .js extension) for migration.
	LegacyDest string
	// VersionRegex extracts the version constant from installed file content.
	VersionRegex *regexp.Regexp
	// Group categorizes the entry (retag, changelog, or release).
	Group string
}

// scaffoldManifest is the file manifest mirroring scaffold-ci.js's MANIFEST.
var scaffoldManifest = []scaffoldManifestEntry{
	{
		PayloadKey:   "retag-release.cjs",
		Dest:         filepath.Join(".github", "scripts", "retag-release.cjs"),
		LegacyDest:   filepath.Join(".github", "scripts", "retag-release.js"),
		VersionRegex: regexp.MustCompile(`const\s+RETAG_SCRIPT_VERSION\s*=\s*(\d+)`),
		Group:        "retag",
	},
	{
		PayloadKey:   "retag-release.yml",
		Dest:         filepath.Join(".github", "workflows", "retag-release.yml"),
		VersionRegex: regexp.MustCompile(`(?m)^#\s*retag-release-version:\s*(\d+)`),
		Group:        "retag",
	},
	{
		PayloadKey:   "check-changelog.cjs",
		Dest:         filepath.Join(".github", "scripts", "check-changelog.cjs"),
		LegacyDest:   filepath.Join(".github", "scripts", "check-changelog.js"),
		VersionRegex: regexp.MustCompile(`const\s+CHECK_CHANGELOG_SCRIPT_VERSION\s*=\s*(\d+)`),
		Group:        "changelog",
	},
	{
		PayloadKey:   "check-changelog.yml",
		Dest:         filepath.Join(".github", "workflows", "check-changelog.yml"),
		VersionRegex: regexp.MustCompile(`(?m)^#\s*check-changelog-version:\s*(\d+)`),
		Group:        "changelog",
	},
	{
		PayloadKey:   "release-on-main.cjs",
		Dest:         filepath.Join(".github", "scripts", "release-on-main.cjs"),
		VersionRegex: regexp.MustCompile(`const\s+RELEASE_ON_MAIN_SCRIPT_VERSION\s*=\s*(\d+)`),
		Group:        "release",
	},
	{
		PayloadKey:   "release-on-main.yml",
		Dest:         filepath.Join(".github", "workflows", "release-on-main.yml"),
		VersionRegex: regexp.MustCompile(`(?m)^#\s*release-on-main-version:\s*(\d+)`),
		Group:        "release",
	},
	{
		PayloadKey:   "verify-release-intent.cjs",
		Dest:         filepath.Join(".github", "scripts", "verify-release-intent.cjs"),
		VersionRegex: regexp.MustCompile(`const\s+VERIFY_RELEASE_INTENT_SCRIPT_VERSION\s*=\s*(\d+)`),
		Group:        "release",
	},
	{
		PayloadKey:   "verify-release-intent.yml",
		Dest:         filepath.Join(".github", "workflows", "verify-release-intent.yml"),
		VersionRegex: regexp.MustCompile(`(?m)^#\s*verify-release-intent-version:\s*(\d+)`),
		Group:        "release",
	},
	{
		PayloadKey:   "promote-release.cjs",
		Dest:         filepath.Join(".github", "scripts", "promote-release.cjs"),
		VersionRegex: regexp.MustCompile(`const\s+PROMOTE_RELEASE_SCRIPT_VERSION\s*=\s*(\d+)`),
		Group:        "release",
	},
	{
		PayloadKey:   "promote-release.yml",
		Dest:         filepath.Join(".github", "workflows", "promote-release.yml"),
		VersionRegex: regexp.MustCompile(`(?m)^#\s*promote-release-version:\s*(\d+)`),
		Group:        "release",
	},
}

// ScaffoldCIIn is the input for the scaffold_ci tool.
type ScaffoldCIIn struct {
	Force bool `json:"force"`
}

// ScaffoldFileReport describes the result for a single manifest entry.
type ScaffoldFileReport struct {
	Path             string `json:"path"`
	Action           string `json:"action"`
	InstalledVersion *int   `json:"installedVersion"`
	CurrentVersion   int    `json:"currentVersion"`
	Group            string `json:"group"`
}

// ScaffoldCIOut is the output for the scaffold_ci tool.
type ScaffoldCIOut struct {
	Warnings   []string             `json:"warnings"`
	Files      []ScaffoldFileReport `json:"files"`
	Protection RulesetCheckResult   `json:"protection"`
}

// scaffoldExtractVersion extracts a version number from content using the
// given regex. Returns 1 if no match is found, mirroring the JS default.
func scaffoldExtractVersion(content string, re *regexp.Regexp) int {
	m := re.FindStringSubmatch(content)
	if m == nil || len(m) < 2 {
		return 1
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return 1
	}
	return v
}

// scaffoldCI is the core logic, separated for testability.
func scaffoldCI(root string, force bool) (ScaffoldCIOut, error) {
	payloads := Payloads()

	var warnings []string
	var files []ScaffoldFileReport

	for _, entry := range scaffoldManifest {
		srcContent, ok := payloads[entry.PayloadKey]
		if !ok {
			// Should never happen with embedded payloads; degrade gracefully.
			return ScaffoldCIOut{}, &mcpserver.InfraError{
				Msg: fmt.Sprintf("embedded payload %q not found", entry.PayloadKey),
			}
		}

		currentVersion := scaffoldExtractVersion(string(srcContent), entry.VersionRegex)

		destPath := filepath.Join(root, entry.Dest)
		destExists := scaffoldFileExists(destPath)

		var legacyPath string
		legacyExists := false
		if entry.LegacyDest != "" {
			legacyPath = filepath.Join(root, entry.LegacyDest)
			legacyExists = scaffoldFileExists(legacyPath)
		}

		var installedVersion *int
		var action string

		if destExists {
			destContent, err := os.ReadFile(destPath)
			if err != nil {
				return ScaffoldCIOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("read %s: %s", destPath, err.Error()),
					Cause: err,
				}
			}
			v := scaffoldExtractVersion(string(destContent), entry.VersionRegex)
			installedVersion = &v
		} else if legacyExists {
			legacyContent, err := os.ReadFile(legacyPath)
			if err != nil {
				return ScaffoldCIOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("read %s: %s", legacyPath, err.Error()),
					Cause: err,
				}
			}
			v := scaffoldExtractVersion(string(legacyContent), entry.VersionRegex)
			installedVersion = &v
		}

		// Determine action (write mode, not check-only).
		if !destExists && legacyExists && force {
			// Migration: delete legacy .js, install new .cjs.
			if err := os.Remove(legacyPath); err != nil {
				return ScaffoldCIOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("remove legacy %s: %s", legacyPath, err.Error()),
					Cause: err,
				}
			}
			action = "migrated"
		} else if !destExists && legacyExists {
			action = "outdated"
			warnings = append(warnings,
				fmt.Sprintf("Legacy file found: %s → use --force to migrate to %s", entry.LegacyDest, entry.Dest))
		} else if !destExists {
			action = "created"
		} else if force {
			action = "overwritten"
		} else if installedVersion != nil && *installedVersion < currentVersion {
			action = "outdated"
			warnings = append(warnings,
				fmt.Sprintf("%s is outdated (installed: v%d, current: v%d). Use --force to update.",
					entry.Dest, *installedVersion, currentVersion))
		} else {
			action = "skipped"
		}

		if action == "created" || action == "overwritten" || action == "migrated" {
			destDir := filepath.Dir(destPath)
			if err := os.MkdirAll(destDir, 0755); err != nil {
				return ScaffoldCIOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("mkdir %s: %s", destDir, err.Error()),
					Cause: err,
				}
			}
			if err := os.WriteFile(destPath, srcContent, 0644); err != nil {
				return ScaffoldCIOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("write %s: %s", destPath, err.Error()),
					Cause: err,
				}
			}
		}

		files = append(files, ScaffoldFileReport{
			Path:             entry.Dest,
			Action:           action,
			InstalledVersion: installedVersion,
			CurrentVersion:   currentVersion,
			Group:            entry.Group,
		})
	}

	if warnings == nil {
		warnings = []string{}
	}

	return ScaffoldCIOut{
		Warnings:   warnings,
		Files:      files,
		Protection: checkBranchProtection(root, execx.Run),
	}, nil
}

// --- branch protection check ---

// scaffoldExecFunc matches execx.Run's signature, letting tests substitute a
// fake for the real git/gh binaries.
type scaffoldExecFunc func(name string, args []string, opts execx.Options) (string, error)

// RulesetCheckResult reports whatever branch protection exists on the
// project's default branch. It is purely informational: whether protection
// requires a bypass depends on the repo's configured changelogMethod
// ("push" is blocked by protection; "pr" and "skip" are not), which this
// check does not have access to.
type RulesetCheckResult struct {
	HasRulesets    bool     `json:"hasRulesets"`
	HasClassicProt bool     `json:"hasClassicProtection"`
	DefaultBranch  string   `json:"defaultBranch"`
	RulesetNames   []string `json:"rulesetNames"`
	Notes          []string `json:"notes"`
}

// checkBranchProtection queries GitHub (via `gh api`) for rulesets and
// classic branch protection on dir's default branch. Every failure mode (no
// git remote, gh not installed/authenticated, non-GitHub remote, API error)
// degrades to a result with an explanatory note — this check must never
// fail scaffold_ci itself.
func checkBranchProtection(dir string, execRun scaffoldExecFunc) RulesetCheckResult {
	result := RulesetCheckResult{
		Notes:        []string{},
		RulesetNames: []string{},
	}

	originURL, err := execRun("git", []string{"remote", "get-url", "origin"}, execx.Options{Dir: dir})
	if err != nil || originURL == "" {
		result.Notes = append(result.Notes, "no git remote 'origin' found — skipping branch protection check")
		return result
	}
	owner, repo, err := ghx.ParseRemoteOwner(originURL)
	if err != nil {
		result.Notes = append(result.Notes, fmt.Sprintf("could not parse owner/repo from remote %q: %s", originURL, err.Error()))
		return result
	}

	defaultBranch, err := execRun("gh", []string{"api", fmt.Sprintf("repos/%s/%s", owner, repo), "--jq", ".default_branch"}, execx.Options{Dir: dir})
	if err != nil || defaultBranch == "" {
		result.Notes = append(result.Notes, "could not reach GitHub via gh api (is gh installed and authenticated?) — skipping branch protection check")
		return result
	}
	result.DefaultBranch = defaultBranch

	rulesetsRaw, err := execRun("gh", []string{"api", fmt.Sprintf("repos/%s/%s/rulesets", owner, repo)}, execx.Options{Dir: dir})
	if err != nil {
		result.Notes = append(result.Notes, "could not query repository rulesets (insufficient gh permissions, or none configured)")
	} else {
		var rulesets []struct {
			Name string `json:"name"`
		}
		if jsonErr := json.Unmarshal([]byte(rulesetsRaw), &rulesets); jsonErr == nil {
			for _, rs := range rulesets {
				result.RulesetNames = append(result.RulesetNames, rs.Name)
			}
			result.HasRulesets = len(rulesets) > 0
		} else {
			result.Notes = append(result.Notes, fmt.Sprintf("could not parse rulesets response: %s", jsonErr.Error()))
		}
	}

	_, protErr := execRun("gh", []string{"api", fmt.Sprintf("repos/%s/%s/branches/%s/protection", owner, repo, defaultBranch)}, execx.Options{Dir: dir})
	result.HasClassicProt = protErr == nil

	if result.HasRulesets || result.HasClassicProt {
		result.Notes = append(result.Notes, fmt.Sprintf(
			"branch protection is active on %q — tagging and GitHub Releases work normally; if changelogMethod is \"push\", the direct push will be blocked — use \"pr\" or \"skip\" instead",
			result.DefaultBranch))
	} else {
		result.Notes = append(result.Notes, fmt.Sprintf("no branch protection detected on %q", result.DefaultBranch))
	}

	return result
}

// --- verify_tag_ancestry ---

// VerifyTagAncestryIn is the input for the verify_tag_ancestry tool.
type VerifyTagAncestryIn struct {
	Tag string `json:"tag"`
}

// VerifyTagAncestryOut is the output for the verify_tag_ancestry tool.
type VerifyTagAncestryOut struct {
	OK      bool   `json:"ok"`
	Details string `json:"details"`
}

// verifyTagAncestry is the core logic, separated for testability.
func verifyTagAncestry(root, tag string) (VerifyTagAncestryOut, error) {
	if tag == "" {
		return VerifyTagAncestryOut{
			OK:      false,
			Details: "tag is required",
		}, nil
	}

	// Verify the tag exists.
	_, err := execx.Run("git", []string{"rev-parse", "--verify", "refs/tags/" + tag}, execx.Options{Dir: root})
	if err != nil {
		return VerifyTagAncestryOut{
			OK:      false,
			Details: fmt.Sprintf("unknown tag: '%s' does not exist in this repository (refs/tags/%s not found)", tag, tag),
		}, nil
	}

	// Check ancestry: is tag an ancestor of HEAD?
	_, err = execx.Run("git", []string{"merge-base", "--is-ancestor", tag, "HEAD"}, execx.Options{Dir: root})
	if err == nil {
		return VerifyTagAncestryOut{
			OK:      true,
			Details: fmt.Sprintf("Tag '%s' is an ancestor of HEAD.", tag),
		}, nil
	}

	// Distinguish exit code 1 (non-ancestor) from higher codes (git error).
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return VerifyTagAncestryOut{
			OK:      false,
			Details: fmt.Sprintf("Tag '%s' is not an ancestor of HEAD. The release commit landed on a different branch. Delete the tag (git push origin :refs/tags/%s; git tag -d %s) and re-run version step on the correct branch.", tag, tag, tag),
		}, nil
	}

	// Git error (exit > 1 or other failure).
	return VerifyTagAncestryOut{
		OK:      false,
		Details: fmt.Sprintf("git merge-base error for tag '%s': %s", tag, err.Error()),
	}, nil
}

// --- Registration ---

// RegisterScaffoldTools registers scaffold_ci and verify_tag_ancestry on the
// server.
func RegisterScaffoldTools(s *mcpserver.Server) {
	mcpserver.Register(s, "scaffold_ci",
		"INTERNAL — called by sdlc skills only. Deterministically copies CI scripts and workflow files into a user project from embedded payloads.",
		func(ctx mcpserver.Ctx, in ScaffoldCIIn) (ScaffoldCIOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return ScaffoldCIOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
					Cause: err,
				}
			}
			return scaffoldCI(root, in.Force)
		},
	)

	mcpserver.Register(s, "verify_tag_ancestry",
		"INTERNAL — called by sdlc skills only. Verify a git tag is an ancestor of HEAD.",
		func(ctx mcpserver.Ctx, in VerifyTagAncestryIn) (VerifyTagAncestryOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return VerifyTagAncestryOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
					Cause: err,
				}
			}
			return verifyTagAncestry(root, in.Tag)
		},
	)
}

// scaffoldFileExists returns true if path exists and is not a directory.
func scaffoldFileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
