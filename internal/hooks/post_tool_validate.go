package hooks

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/discovery"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
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
		// finalOnly holds checks that only run at --final (plan branch only).
		finalOnly []discovery.Finding
		verr      error
	)
	switch {
	case postToolValidateDimensionRe.MatchString(filePath):
		// No file arg: the dimensions validator scans .sdlc-v2/review-dimensions
		// itself rather than validating one file.
		//
		// Deviation from the raw-cwd root ruling above (KEEP: hook entry
		// point — do not change to resolveSdlcRoot()), for this branch only: dimension
		// files are git-tracked content, so per the root rule they must be
		// read from the ACTIVE worktree, not the raw process cwd -- otherwise
		// this hook and the validate/dimensions and review_prepare tools would
		// disagree about which dimension files exist inside a linked
		// worktree. Fail open to root (cwd) so a resolution error here never
		// silently disables this hook.
		dimRoot := root
		if activeRoot, aerr := worktree.ActiveRoot(); aerr == nil {
			dimRoot = activeRoot
		}
		findings, verr = tools.ValidateDimensionsAction(dimRoot)
	case postToolValidatePRTemplateRe.MatchString(filePath):
		// No file arg: the pr-template validator resolves the template
		// itself rather than validating the edited path directly.
		findings, verr = tools.ValidatePRTemplate(root)
	case postToolValidatePlanRe.MatchString(filePath):
		// PF9/PF10 only block at --final, so they never make this hook block
		// on their own. ValidatePlanFormatForHook returns them separately, and
		// only when something already blocks, so they can be shown as a
		// preview of what the next step will also reject.
		findings, finalOnly, verr = tools.ValidatePlanFormatForHook(root, filePath)
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
	reason := joinValidationFindings(findings)
	if len(finalOnly) > 0 {
		reason += "\n\n" + finalOnlyHeading + "\n" + joinValidationFindings(finalOnly)
	}
	return Output{JSON: map[string]any{
		"decision": "block",
		"reason":   reason,
	}, ExitCode: 0}, nil
}

// finalOnlyHeading introduces the findings of checks that run only at --final.
const finalOnlyHeading = "will fail at --final:"

// fixIndent aligns the continuation lines of a fix under the text that follows
// "  fix:  ", so a multi-line shape reads as one block.
const fixIndent = "        "

// joinValidationFindings renders each finding as its own block, blocks
// separated by a blank line, for the "reason" string carried in
// postToolValidate's blocking payload. It is shared by all three hook
// branches (dimensions, pr-template, plan):
//
//	PF7: Missing **Contract:** block: Task 3, Task 5
//	  file: /path/to/plan.md
//	  fix:  every task with a Create/Modify/Test bullet ...
//	          **Contract:**
//
// Extra lines of a multi-line message are indented under the first. The file
// and fix lines are omitted when the finding has no Path or Fix.
func joinValidationFindings(findings []discovery.Finding) string {
	blocks := make([]string, len(findings))
	for i, f := range findings {
		blocks[i] = renderFindingBlock(f)
	}
	return strings.Join(blocks, "\n\n")
}

func renderFindingBlock(f discovery.Finding) string {
	var b strings.Builder
	msgLines := strings.Split(f.Message, "\n")
	fmt.Fprintf(&b, "%s: %s", f.ID, msgLines[0])
	for _, line := range msgLines[1:] {
		b.WriteString("\n" + indentLine("  ", line))
	}
	if f.Path != "" {
		fmt.Fprintf(&b, "\n  file: %s", f.Path)
	}
	if f.Fix != "" {
		fixLines := strings.Split(f.Fix, "\n")
		fmt.Fprintf(&b, "\n  fix:  %s", fixLines[0])
		for _, line := range fixLines[1:] {
			b.WriteString("\n" + indentLine(fixIndent, line))
		}
	}
	return b.String()
}

// indentLine prefixes line with indent, leaving a blank line blank so the
// output carries no trailing whitespace.
func indentLine(indent, line string) string {
	if line == "" {
		return ""
	}
	return indent + line
}
