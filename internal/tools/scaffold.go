package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// --- scaffold_ci ---

// scaffoldManifestEntry maps an embedded payload to its project destination.
type scaffoldManifestEntry struct {
	// PayloadKey is the key in Payloads() (e.g. "check-changelog.cjs").
	PayloadKey string
	// Dest is the destination relative to project root.
	Dest string
	// LegacyDest is the legacy destination (old .js extension) for migration.
	LegacyDest string
	// VersionRegex extracts the version constant from installed file content.
	VersionRegex *regexp.Regexp
	// Group categorizes the entry (changelog or release).
	Group string
}

// scaffoldManifest is the file manifest mirroring scaffold-ci.js's MANIFEST.
var scaffoldManifest = []scaffoldManifestEntry{
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
	Force          bool `json:"force,omitempty" jsonschema_description:"Overwrite every CI script and workflow file that already exists, current ones too, and migrate legacy .js files. Local edits in current files are lost. Missing files are created in every mode. Cannot be combined with updateOutdated."`
	UpdateOutdated bool `json:"updateOutdated,omitempty" jsonschema_description:"Overwrite only the CI files whose installed version is below the bundled version, and migrate legacy .js files. A file with no version marker counts as version 1, so it is overwritten and the output has a warning for it. Local edits in an overwritten file are lost. Current files stay unchanged. Missing files are created in every mode. Cannot be combined with force."`
}

// scaffoldOptions holds the overwrite modes of scaffoldCIWith. At most one of
// the two fields may be true.
type scaffoldOptions struct {
	Force          bool
	UpdateOutdated bool
}

// ciFileTooLargeSuggestion is the recovery text for errCIFileTooLarge. The
// read limit of one installed CI file is maxCheckedFileBytes: the largest
// bundled payload is far below it, so a larger file is not a plugin CI file.
const ciFileTooLargeSuggestion = "The installed file is larger than " + maxCheckedFileText + ", so it is not a plugin CI file. Restore the file with git or delete it. Then run the same call again."

// Overwrite modes echoed in ScaffoldCIOut.Mode.
const (
	scaffoldModeDefault        = "default"
	scaffoldModeForce          = "force"
	scaffoldModeUpdateOutdated = "updateOutdated"
)

// ScaffoldFileReport describes the result for a single manifest entry.
type ScaffoldFileReport struct {
	Path             string `json:"path"`
	Action           string `json:"action" jsonschema_description:"One of created, overwritten, migrated (written by this call), outdated (not written; a warning names the fix) or skipped (not written)."`
	InstalledVersion *int   `json:"installedVersion"`
	CurrentVersion   int    `json:"currentVersion"`
	Group            string `json:"group"`
}

// ScaffoldCIOut is the output for the scaffold_ci tool.
type ScaffoldCIOut struct {
	Root       string               `json:"root" jsonschema_description:"Absolute path of the worktree the CI files were written under (the active git worktree)."`
	Mode       string               `json:"mode" jsonschema_description:"Overwrite mode of this call: default (no overwrite input), force or updateOutdated. The overwritten action means a rewrite of every existing file under force, and a rewrite of an outdated file under updateOutdated."`
	Warnings   []string             `json:"warnings"`
	Files      []ScaffoldFileReport `json:"files"`
	Protection RulesetCheckResult   `json:"protection"`
	Next       string               `json:"next" jsonschema_description:"Actionable next-step guidance after scaffolding completes."`
}

// CIScriptDriftEntry reports how one CI scaffold script/workflow compares to
// the version scaffold_ci would install. It is produced by ciScriptDrift
// (Task 3, R2) and surfaced both in SetupPrepareOut.CIScriptDrift and by the
// "ci_script_drift" validate action.
type CIScriptDriftEntry struct {
	Script           string `json:"script"`
	InstalledVersion int    `json:"installedVersion"`
	CurrentVersion   int    `json:"currentVersion"`
	Action           string `json:"action"` // outdated|missing|current
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

// scaffoldEntryState is the on-disk state of one manifest entry, read by
// scaffoldEntryVersions.
type scaffoldEntryState struct {
	// currentVersion is the version of the embedded payload.
	currentVersion int
	// installedVersion is the version of the installed file (dest first,
	// else legacy); nil when neither file exists.
	installedVersion *int
	// installedMarker is true when the installed file has a version marker.
	// Without one, installedVersion is 1.
	installedMarker          bool
	destExists, legacyExists bool
	destPath, legacyPath     string
}

// scaffoldEntryVersions reads the state of one manifest entry: the payload
// version, and the installed version from whichever of destPath/legacyPath
// exists on disk, dest taking priority. It is the shared read-only step
// behind scaffoldCIWith (which goes on to decide a write action) and
// ciScriptDrift (a pure comparison used by setup_prepare and the
// "ci_script_drift" validate action). Every error it returns is a plain Go
// error; scaffoldVersionReadError turns it into the tool error.
func scaffoldEntryVersions(root string, entry scaffoldManifestEntry, srcContent []byte) (scaffoldEntryState, error) {
	st := scaffoldEntryState{
		currentVersion: scaffoldExtractVersion(string(srcContent), entry.VersionRegex),
		destPath:       filepath.Join(root, entry.Dest),
	}

	var err error
	if st.destExists, err = scaffoldRegularFileExists(st.destPath); err != nil {
		return st, err
	}
	if entry.LegacyDest != "" {
		st.legacyPath = filepath.Join(root, entry.LegacyDest)
		if st.legacyExists, err = scaffoldRegularFileExists(st.legacyPath); err != nil {
			return st, err
		}
	}

	readPath := ""
	switch {
	case st.destExists:
		readPath = st.destPath
	case st.legacyExists:
		readPath = st.legacyPath
	}
	if readPath != "" {
		content, rerr := readInstalledCIFile(readPath)
		if rerr != nil {
			return st, fmt.Errorf("read %s: %w", readPath, rerr)
		}
		st.installedMarker = entry.VersionRegex.Match(content)
		v := scaffoldExtractVersion(string(content), entry.VersionRegex)
		st.installedVersion = &v
	}

	return st, nil
}

// errCIFileTooLarge marks an installed CI file above maxCheckedFileBytes.
var errCIFileTooLarge = errors.New("installed CI file is too large")

// readInstalledCIFile reads at most maxCheckedFileBytes of the file at path.
// A file of exactly that size is read in full. A larger file gives an error
// that wraps errCIFileTooLarge and states the size of the file.
func readInstalledCIFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCheckedFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCheckedFileBytes {
		if info, serr := f.Stat(); serr == nil {
			return nil, fmt.Errorf("%w: %d bytes, over the %d byte limit", errCIFileTooLarge, info.Size(), maxCheckedFileBytes)
		}
		return nil, fmt.Errorf("%w: over the %d byte limit", errCIFileTooLarge, maxCheckedFileBytes)
	}
	return data, nil
}

// scaffoldVersionReadError turns the error of scaffoldEntryVersions into the
// error a tool returns. A file above the read limit gives a DataError. Any
// other error gives an InfraError with the given retry text.
func scaffoldVersionReadError(entry scaffoldManifestEntry, verr error, suggestion string) error {
	if errors.Is(verr, errCIFileTooLarge) {
		return &mcpserver.DataError{
			Msg:        fmt.Sprintf("check installed version of CI script %s: %s", entry.Dest, verr.Error()),
			Suggestion: ciFileTooLargeSuggestion,
			Cause:      verr,
		}
	}
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("check installed version of CI script %s: %s", entry.Dest, verr.Error()),
		Suggestion: suggestion,
		Cause:      verr,
	}
}

// ciScriptDrift is a read-only version comparison (Task 3, R2) over the same
// scaffoldManifest scaffoldCI installs from. It never writes files —
// scaffold_ci({updateOutdated:true}) is the remediation surfaced by both
// SetupPrepareOut.CIScriptDrift and the "ci_script_drift" validate action.
// A legacy (.js) file is reported as "outdated" (it always needs an overwrite
// mode to migrate); an entry with neither file installed is reported as "missing"
// rather than folded into "outdated", so callers can distinguish "never
// scaffolded" from "stale".
func ciScriptDrift(root string) ([]CIScriptDriftEntry, error) {
	payloads := Payloads()

	entries := make([]CIScriptDriftEntry, 0, len(scaffoldManifest))
	for _, entry := range scaffoldManifest {
		srcContent, ok := payloads[entry.PayloadKey]
		if !ok {
			// Should never happen with embedded payloads; degrade gracefully.
			return nil, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("embedded payload %q not found", entry.PayloadKey),
				Suggestion: fmt.Sprintf("The sdlc plugin is missing its embedded file %q. Update or reinstall the plugin, then re-run the same setup_prepare or validate call.", entry.PayloadKey),
			}
		}

		st, verr := scaffoldEntryVersions(root, entry, srcContent)
		if verr != nil {
			return nil, scaffoldVersionReadError(entry, verr,
				"Check read permission on the CI script file named in the message above, then re-run the same setup_prepare or validate call.")
		}

		installed := 0
		var action string
		switch {
		case st.destExists:
			installed = *st.installedVersion
			if installed < st.currentVersion {
				action = "outdated"
			} else {
				action = "current"
			}
		case st.legacyExists:
			installed = *st.installedVersion
			action = "outdated"
		default:
			action = "missing"
		}

		entries = append(entries, CIScriptDriftEntry{
			Script:           entry.Dest,
			InstalledVersion: installed,
			CurrentVersion:   st.currentVersion,
			Action:           action,
		})
	}

	return entries, nil
}

// defaultReleaseSecret is the secret name baked into the embedded payloads'
// token-chain fallback (steps.release-token.outputs.token ||
// secrets.RELEASE_TOKEN || secrets.GITHUB_TOKEN). scaffoldCI only rewrites
// secrets.RELEASE_TOKEN to a configured pushAuth.secretName when that name
// differs from this default — an unset secretName, or one matching it,
// means the payload's own RELEASE_TOKEN fallback already does the right
// thing.
const defaultReleaseSecret = "RELEASE_TOKEN"

// pushAuthWorkflowKeys are the payload keys whose CI identity is affected by
// a configured pushAuth.secretName (R10): each authenticates git push (via
// actions/checkout's persisted credential) and gh CLI calls (via the
// GH_TOKEN env var) through the token chain (GitHub App token ->
// secrets.RELEASE_TOKEN -> secrets.GITHUB_TOKEN), regardless of
// version.method. verify-release-intent.yml and other GITHUB_TOKEN-using
// workflows are read-only/non-push and intentionally excluded.
var pushAuthWorkflowKeys = map[string]bool{
	"release-on-main.yml": true,
	"promote-release.yml": true,
}

// secretNamePattern is GitHub's secret-name syntax: letters, digits and
// underscores, no leading digit. The GITHUB_ prefix is reserved and checked
// separately (Go regexp has no lookahead).
var secretNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateSecretName rejects a pushAuth.secretName that is not a legal
// GitHub Actions secret name. scaffoldCI splices the name verbatim into
// generated workflow YAML, so a bad value must fail loudly here instead of
// producing a broken workflow that only fails later in GitHub Actions.
func validateSecretName(name string) error {
	if !secretNamePattern.MatchString(name) || strings.HasPrefix(strings.ToUpper(name), "GITHUB_") {
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("version.pushAuth.secretName %q is not a valid GitHub Actions secret name", name),
			Suggestion: "Use only letters, digits and underscores, do not start with a digit or GITHUB_ (for example RELEASE_TOKEN). Fix it in .sdlc-v2/config.toml, then run scaffold_ci again.",
		}
	}
	return nil
}

// scaffoldCI runs scaffoldCIWith with the real git and gh binaries. force
// true selects the force mode; false selects the default mode, which writes
// only missing files.
func scaffoldCI(root string, force bool) (ScaffoldCIOut, error) {
	return scaffoldCIWith(root, scaffoldOptions{Force: force}, execx.Run)
}

// scaffoldCIWith is the core logic of scaffold_ci. Tests pass a fake execRun
// for the branch protection check.
func scaffoldCIWith(root string, opts scaffoldOptions, execRun scaffoldExecFunc) (ScaffoldCIOut, error) {
	if opts.Force && opts.UpdateOutdated {
		return ScaffoldCIOut{}, &mcpserver.DomainError{
			Msg:        "force and updateOutdated cannot both be true",
			Suggestion: "Pass only one of the two inputs. Use updateOutdated true to update outdated files and keep current ones. Use force true to rewrite every file. Then run scaffold_ci again.",
		}
	}
	mode := scaffoldModeDefault
	switch {
	case opts.Force:
		mode = scaffoldModeForce
	case opts.UpdateOutdated:
		mode = scaffoldModeUpdateOutdated
	}
	payloads := Payloads()

	// pushAuth.secretName is read tolerantly (raw section, not the
	// fully-validated VersionSection) so scaffold_ci keeps working even when
	// version config is absent or fails validation for reasons unrelated to
	// CI auth (e.g. neither tag nor versionFile enabled yet).
	var warnings []string
	var pushAuthSecret string
	if versionRaw, err := config.ReadSection(root, "version"); err != nil && !errors.Is(err, config.ErrNotFound) {
		warnings = append(warnings, fmt.Sprintf("reading version config: %s", err.Error()))
	} else if versionRaw != nil {
		if pa, ok := versionRaw["pushAuth"].(map[string]any); ok {
			if s, ok := pa["secretName"].(string); ok {
				pushAuthSecret = s
			}
		}
	}
	// rewriteSecret fires for any version.method whenever a non-default
	// secret name is configured — an unset secretName, or one equal to
	// defaultReleaseSecret, leaves the payload's own RELEASE_TOKEN fallback
	// untouched.
	rewriteSecret := pushAuthSecret != "" && pushAuthSecret != defaultReleaseSecret
	if rewriteSecret {
		if err := validateSecretName(pushAuthSecret); err != nil {
			return ScaffoldCIOut{}, err
		}
	}
	var files []ScaffoldFileReport

	for _, entry := range scaffoldManifest {
		srcContent, ok := payloads[entry.PayloadKey]
		if !ok {
			// Should never happen with embedded payloads; degrade gracefully.
			return ScaffoldCIOut{}, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("embedded payload %q not found", entry.PayloadKey),
				Suggestion: fmt.Sprintf("The sdlc plugin is missing its embedded file %q. Update or reinstall the plugin, then run scaffold_ci again.", entry.PayloadKey),
			}
		}

		if rewriteSecret && pushAuthWorkflowKeys[entry.PayloadKey] {
			srcContent = bytes.ReplaceAll(srcContent,
				[]byte("secrets."+defaultReleaseSecret), []byte("secrets."+pushAuthSecret))
		}

		st, verr := scaffoldEntryVersions(root, entry, srcContent)
		if verr != nil {
			return ScaffoldCIOut{}, scaffoldVersionReadError(entry, verr,
				"Check read permission on the CI script file named in the message above, then retry scaffold_ci.")
		}

		// Determine action (write mode, not check-only).
		var action string
		switch {
		case !st.destExists && st.legacyExists && mode != scaffoldModeDefault:
			// Migration: install new .cjs, then delete legacy .js.
			action = "migrated"
		case !st.destExists && st.legacyExists:
			action = "outdated"
			warnings = append(warnings,
				fmt.Sprintf("Legacy file found: %s → run scaffold_ci with updateOutdated true to migrate to %s", entry.LegacyDest, entry.Dest))
		case !st.destExists:
			action = "created"
		case mode == scaffoldModeForce:
			action = "overwritten"
		case st.installedVersion != nil && *st.installedVersion < st.currentVersion:
			if mode == scaffoldModeUpdateOutdated {
				action = "overwritten"
				if !st.installedMarker {
					warnings = append(warnings,
						fmt.Sprintf("%s had no version marker, so it counted as v1 and was overwritten with v%d. Git keeps the old content of a committed file.",
							entry.Dest, st.currentVersion))
				}
			} else {
				action = "outdated"
				warnings = append(warnings,
					fmt.Sprintf("%s is outdated (installed: v%d, current: v%d). Run scaffold_ci with updateOutdated true to update.",
						entry.Dest, *st.installedVersion, st.currentVersion))
			}
		default:
			action = "skipped"
		}

		if action == "created" || action == "overwritten" || action == "migrated" {
			destDir := filepath.Dir(st.destPath)
			if err := os.MkdirAll(destDir, 0755); err != nil {
				return ScaffoldCIOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("mkdir %s: %s", destDir, err.Error()),
					Suggestion: fmt.Sprintf("Remove any file that blocks the folder %s and make its parent writable. Then run scaffold_ci again.", destDir),
					Cause:      err,
				}
			}
			// An overwrite keeps the permission bits of the existing file.
			perm := os.FileMode(0644)
			if st.destExists {
				if info, err := os.Stat(st.destPath); err == nil {
					perm = info.Mode().Perm()
				}
			}
			if err := fsx.AtomicWriteBytesPerm(st.destPath, srcContent, perm); err != nil {
				return ScaffoldCIOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("write %s: %s", st.destPath, err.Error()),
					Suggestion: fmt.Sprintf("Check write permission on %s and its folder, and free disk space on the project root. Then run scaffold_ci again.", st.destPath),
					Cause:      err,
				}
			}
		}
		// The legacy file is deleted only after the new file is in place, so
		// a failed write keeps the legacy file.
		if action == "migrated" {
			if err := os.Remove(st.legacyPath); err != nil {
				return ScaffoldCIOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("remove legacy %s: %s (the new file %s is already written)", st.legacyPath, err.Error(), st.destPath),
					Suggestion: fmt.Sprintf("Delete %s by hand or make its folder writable. The new file is already in place, so the next scaffold_ci run does not need to migrate it.", st.legacyPath),
					Cause:      err,
				}
			}
		}

		files = append(files, ScaffoldFileReport{
			Path:             entry.Dest,
			Action:           action,
			InstalledVersion: st.installedVersion,
			CurrentVersion:   st.currentVersion,
			Group:            entry.Group,
		})
	}

	if warnings == nil {
		warnings = []string{}
	}

	protection := checkBranchProtection(root, execRun)

	return ScaffoldCIOut{
		Root:       root,
		Mode:       mode,
		Warnings:   warnings,
		Files:      files,
		Protection: protection,
		Next:       scaffoldNextGuidance(protection, pushAuthSecret),
	}, nil
}

// scaffoldNextGuidance builds the actionable next-step guidance surfaced in
// ScaffoldCIOut.Next. Branch protection/rulesets block the default
// GITHUB_TOKEN from pushing release commits and tags (GH013) regardless of
// version.method, so whenever protection is detected the guidance offers
// the same three mitigations — a GitHub App token, an admin PAT stored
// under the configured pushAuth.secretName (or defaultReleaseSecret when
// unset), or switching to version.method = "pr" — rather than branching on
// which method is currently configured. With no protection detected, a
// configured non-default secretName still gets a note naming the secret the
// release workflows read, so its setup is not silently dropped.
func scaffoldNextGuidance(protection RulesetCheckResult, secretName string) string {
	const promoteNote = "promote-release.yml was also scaffolded — use Actions > SDLC Promote Release to promote an RC to a final release without creating a PR."

	if !protection.HasRulesets && !protection.HasClassicProt {
		next := "No branch protection detected. CI scripts are installed and both \"push\" and \"pr\" delivery methods will work. "
		if secretName != "" && secretName != defaultReleaseSecret {
			next += fmt.Sprintf("The release workflows read secret %s first (then fall back to GITHUB_TOKEN): "+
				"store a GitHub App token or fine-grained PAT (Contents, Pull requests, Actions: read/write) under that name, "+
				"and add its identity to the bypass list if you add rulesets later. ", secretName)
		}
		return next + promoteNote
	}

	secretRef := secretName
	if secretRef == "" {
		secretRef = defaultReleaseSecret
	}

	return fmt.Sprintf(
		"Branch protection/rulesets detected on %q. GITHUB_TOKEN cannot bypass rulesets,\n"+
			"so release pushes (version bump commit, release tags) will be rejected (GH013).\n"+
			"Pick one:\n"+
			"  1. GitHub App (recommended): create an App with Contents, Pull requests and Actions\n"+
			"     read/write; install it on this repo; set repo variable RELEASE_APP_CLIENT_ID and\n"+
			"     secret RELEASE_APP_PRIVATE_KEY; add the App to the bypass list of every ruleset\n"+
			"     that covers %q and release tags.\n"+
			"  2. Admin PAT: create a fine-grained PAT (Contents, Pull requests, Actions: read/write)\n"+
			"     for a user whose role is on the bypass list; store it as secret %s.\n"+
			"     PATs expire - rotate before expiry.\n"+
			"  3. method = \"pr\": release commits go through a PR. Works only if release PRs can\n"+
			"     merge without a human approval, or a human merges them.\n"+
			"Do not require status checks on all branches (~ALL); require them on ~DEFAULT_BRANCH only.\n"+
			"See https://github.com/rnagrodzki/sdlc-plugin/blob/main/docs/versioning.md#protected-branches-and-rulesets. "+
			promoteNote,
		protection.DefaultBranch, protection.DefaultBranch, secretRef)
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
			"branch protection is active on %q — release pushes need a token on the ruleset bypass list (see scaffold next steps); tag rulesets need the same bypass entry",
			result.DefaultBranch))
	} else {
		result.Notes = append(result.Notes, fmt.Sprintf("no branch protection detected on %q", result.DefaultBranch))
	}

	return result
}

// --- verify_tag_ancestry ---

// VerifyTagAncestryIn is the input for the verify_tag_ancestry tool.
type VerifyTagAncestryIn struct {
	Tag string `json:"tag" jsonschema_description:"Git tag to verify is an ancestor of HEAD."`
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
			Details: fmt.Sprintf("Tag '%s' is not an ancestor of HEAD. The release commit landed on a different branch. Delete the tag (git push origin :refs/tags/%s; git tag -d %s) and re-run the release workflow on the correct branch.", tag, tag, tag),
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
		"INTERNAL — called by sdlc skills only. Deterministically copies CI scripts and workflow files into a user project from embedded payloads. Writes into the active git worktree (git rev-parse --show-toplevel) and returns that path as root. "+
			"Every mode creates missing files. force rewrites every existing file; updateOutdated rewrites only outdated files; with neither, existing files stay unchanged. Both modes migrate a legacy .js file: the new .cjs file is written first, then the legacy file is deleted. An overwrite keeps the permission bits of the existing file. "+
			"Output: mode echoes the overwrite mode (default, force or updateOutdated); files[].action is created, overwritten, migrated, outdated or skipped. "+
			"Errors: force plus updateOutdated is a DomainError (nothing is read or written); an installed CI file above "+maxCheckedFileText+" is a DataError; a failed read, folder creation, write or legacy file deletion is an InfraError, and files written before it stay on disk.",
		mcpserver.Annotations{
			Title:       "Scaffold CI workflow files",
			ReadOnly:    false,
			Destructive: true,
			Idempotent:  true,
			OpenWorld:   true,
		},
		func(ctx mcpserver.Ctx, in ScaffoldCIIn) (ScaffoldCIOut, error) {
			root, err := worktree.ActiveRoot()
			if err != nil {
				return ScaffoldCIOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("resolve project root: %s", err.Error()),
					Suggestion: "Run `git rev-parse --show-toplevel` in this directory to see why it failed — git may be missing or this isn't a git repository. Fix that, then retry scaffold_ci.",
					Cause:      err,
				}
			}
			return scaffoldCIWith(root, scaffoldOptions{Force: in.Force, UpdateOutdated: in.UpdateOutdated}, execx.Run)
		},
	)

	mcpserver.Register(s, "verify_tag_ancestry",
		"INTERNAL — called by sdlc skills only. Verify a git tag is an ancestor of HEAD.",
		mcpserver.Annotations{
			Title:      "Check release tag ancestry",
			ReadOnly:   true,
			Idempotent: true,
			OpenWorld:  false,
		},
		func(ctx mcpserver.Ctx, in VerifyTagAncestryIn) (VerifyTagAncestryOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return VerifyTagAncestryOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("resolve project root: %s", err.Error()),
					Suggestion: "Run `git worktree list --porcelain` in this directory to see why it failed — git may be missing or this isn't a git repository. Fix that, then retry verify_tag_ancestry.",
					Cause:      err,
				}
			}
			return verifyTagAncestry(root, in.Tag)
		},
	)
}

// scaffoldRegularFileExists reports whether path exists and is not a
// directory. A path that does not exist, or whose parent is not a folder,
// gives false and no error. Any other stat error (for example a permission
// error or a symlink loop) is returned, so a caller never takes an
// unreadable file for a missing one.
func scaffoldRegularFileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !info.IsDir(), nil
}
