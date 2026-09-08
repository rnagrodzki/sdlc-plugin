package hooks

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/discovery"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// Path-trigger regexes, ported verbatim from post-tool-validate.js
// (including the legacy .claude/ alternation alongside the canonical
// .sdlc-v2/ location for dimensions and pr-template).
var (
	postToolValidateDimensionRe  = regexp.MustCompile(`[/\\]\.(?:claude|sdlc-v2|sdlc)[/\\]review-dimensions[/\\][^/\\]+\.ya?ml$`)
	postToolValidatePRTemplateRe = regexp.MustCompile(`[/\\]\.(?:claude|sdlc-v2|sdlc)[/\\]pr-template\.md$`)
	postToolValidatePlanRe       = regexp.MustCompile(`[/\\]plans[/\\][^/\\]+\.md$`)
)

// postToolValidate is the "post-tool-validate" hook handler (PostToolUse,
// matcher Edit|Write), ported from hooks/post-tool-validate.js. It tests the
// edited/written file's path against three regexes and, on a match, runs the
// corresponding validator in-process against the whole project tree (or, for
// plan files, against that one file). No match, or a match producing zero
// findings, is silent (exit-equivalent to the source's exit 0).
func postToolValidate(ctx HookCtx, event Event) (Output, error) {
	silent := Output{ExitCode: 0}

	toolInput, _ := event.Raw["tool_input"].(map[string]any)
	filePath, _ := toolInput["file_path"].(string)
	if filePath == "" {
		filePath, _ = toolInput["path"].(string)
	}
	if filePath == "" {
		return silent, nil
	}

	// Deliberate source ruling (JS source has an inline "KEEP: hook entry
	// point — do not change to resolveSdlcRoot()" comment): this hook's
	// project root is the raw process cwd, never the main worktree — unlike
	// block-askuserquestion-auto.go/pipeline_continue.go, which always
	// resolve ship state through worktree.MainRoot().
	root, err := os.Getwd()
	if err != nil {
		return silent, nil
	}

	var (
		findings []discovery.Finding
		verr     error
	)
	switch {
	case postToolValidateDimensionRe.MatchString(filePath):
		// No file arg: the dimensions validator scans .sdlc-v2/review-dimensions
		// itself rather than validating one file.
		findings, verr = tools.ValidateDimensionsAction(root)
	case postToolValidatePRTemplateRe.MatchString(filePath):
		// No file arg: the pr-template validator resolves the template
		// itself rather than validating the edited path directly.
		findings, verr = tools.ValidatePRTemplate(root)
	case postToolValidatePlanRe.MatchString(filePath):
		// Final is always false here: this hook never triggers the stricter
		// PF9/PF10 checks the real source only runs from other call sites.
		findings, verr = tools.ValidatePlanFormat(root, tools.ValidateIn{File: filePath, Final: false})
	default:
		return silent, nil
	}
	if verr != nil || len(findings) == 0 {
		return silent, nil
	}

	// Deviation from source (disclosed, fact sheet Flag 1 option 2): the JS
	// hook conveys blocking findings via exit code 2 + stderr with no JSON
	// payload at all; Output has no stderr channel, so this reports the same
	// findings as a decision/reason JSON payload instead.
	return Output{JSON: map[string]any{
		"decision": "block",
		"reason":   joinValidationFindings(findings),
	}, ExitCode: 0}, nil
}

// joinValidationFindings renders findings as one "ID: message" line each,
// for the "reason" string carried in postToolValidate's blocking payload.
func joinValidationFindings(findings []discovery.Finding) string {
	lines := make([]string, len(findings))
	for i, f := range findings {
		lines[i] = fmt.Sprintf("%s: %s", f.ID, f.Message)
	}
	return strings.Join(lines, "\n")
}
