// CLI wrappers for the read-only `openspec` subcommands that emit
// structured JSON: `list`, `list --specs`, `status`, and `instructions`.
// Like Detect in openspec.go, these never reimplement CLI internals — every
// field is decoded verbatim from the CLI's own JSON output. Shapes below
// were verified against openspec CLI 1.13.2 in a throwaway project (see
// design.md "OpenSpec CLI 1.13.2 facts").
package openspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
)

// ErrCLINotFound is returned by List, ListSpecs, Status, and Instructions
// when the `openspec` binary is not present on PATH.
var ErrCLINotFound = errors.New("openspec CLI not found on PATH")

// runCLI is the process-execution seam for this file's wrappers: a package
// variable, rather than a direct execx.RunAllowExit call, so a test can
// substitute it to exercise the JSON-decode/error-detection logic below
// without needing a PATH stub binary.
var runCLI = func(dir string, args ...string) (stdout, stderr string, exit int, err error) {
	return execx.RunAllowExit("openspec", args, execx.Options{Dir: dir})
}

// CLIWarning is a non-fatal warning entry `openspec list --json` reports
// alongside an otherwise successful change list, e.g. a grouped change
// directory (openspec/changes/<group>/<name>/) that the CLI will never load
// as a change in its own right.
type CLIWarning struct {
	Code    string   `json:"code"`
	Name    string   `json:"name"`
	Nested  []string `json:"nested,omitempty"`
	Message string   `json:"message"`
}

// ListedChange is one entry of `openspec list --json`'s changes[].
type ListedChange struct {
	Name           string `json:"name"`
	CompletedTasks int    `json:"completedTasks"`
	TotalTasks     int    `json:"totalTasks"`
	Status         string `json:"status"`
}

// ListResult is the decoded result of `openspec list --json`. A grouped
// change directory still appears in Changes as the wrapping directory's own
// entry, exactly as the CLI reports it: this wrapper never filters it out,
// since that requires cross-referencing Warnings by name, which is caller
// policy (D4 in design.md), not CLI-decode logic.
type ListResult struct {
	Changes  []ListedChange `json:"changes"`
	Warnings []CLIWarning   `json:"warnings"`
}

// ListedSpec is one entry of `openspec list --specs --json`'s specs[].
type ListedSpec struct {
	ID               string `json:"id"`
	RequirementCount int    `json:"requirementCount"`
}

// ArtifactStatus is one artifact entry of `openspec status --change <c>
// --json`, merging that command's artifacts[] (id, outputPath, requires)
// with the matching entry of its artifactPaths map (existingOutputPaths) —
// the CLI reports these two facts about the same artifact in two different
// places of its JSON.
type ArtifactStatus struct {
	ID                  string
	OutputPath          string
	Requires            []string
	ExistingOutputPaths []string
}

// StatusResult is the decoded result of `openspec status --change <c>
// --json`, with Artifacts kept in the CLI's own artifacts[] array order.
type StatusResult struct {
	ChangeName string
	SchemaName string
	Artifacts  []ArtifactStatus
}

// ArtifactInstructions is the decoded result of `openspec instructions
// <artifact> --change <c> --json`. Context and Rules are populated only
// when the project's openspec config sets, respectively, a project-wide
// `context` string and a per-artifact `rules` list; the CLI omits either
// field entirely when unset, which decodes here as a zero value (empty
// string / nil slice).
type ArtifactInstructions struct {
	ArtifactID  string   `json:"artifactId"`
	OutputPath  string   `json:"outputPath"`
	Template    string   `json:"template"`
	Instruction string   `json:"instruction"`
	Context     string   `json:"context"`
	Rules       []string `json:"rules"`
}

// validChangeNameRe is OpenSpec's own change-name convention: lowercase
// kebab-case, no leading/trailing/doubled hyphen, no path separators.
var validChangeNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidChangeName reports whether name is a valid bare OpenSpec change name
// — lowercase kebab-case with no path separators — matching what `openspec
// new change` accepts. A grouped name such as "grp/demo" is rejected here
// for the same reason the CLI itself rejects it when passed to `status` or
// `instructions`: "Change name cannot contain path separators".
func ValidChangeName(name string) bool {
	return validChangeNameRe.MatchString(name)
}

// cliStatusEntry is one element of the `{"status":[...]}` envelope the CLI
// emits when a command fails validation (invalid change name, unknown
// artifact, missing OpenSpec root, ...). This envelope can appear even
// alongside an otherwise-normal payload and even on a zero exit code, so
// every wrapper below checks for it independent of exit status rather than
// trusting exit code alone.
type cliStatusEntry struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type cliStatusEnvelope struct {
	Status []cliStatusEntry `json:"status"`
}

// cliResult runs an `openspec` subcommand through runCLI and returns its
// stdout once every failure mode has been ruled out: an absent binary
// (ErrCLINotFound), a non-exit exec failure, an embedded error-severity
// status entry (checked regardless of exit code), and finally a plain
// non-zero exit with no such entry.
func cliResult(dir string, args ...string) (string, error) {
	stdout, stderr, exit, err := runCLI(dir, args...)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", ErrCLINotFound
		}
		return "", fmt.Errorf("openspec %s: %w", strings.Join(args, " "), err)
	}

	trimmed := strings.TrimSpace(stdout)
	var envelope cliStatusEnvelope
	if jsonErr := json.Unmarshal([]byte(trimmed), &envelope); jsonErr == nil {
		for _, entry := range envelope.Status {
			if entry.Severity == "error" {
				return "", fmt.Errorf("openspec %s: %s: %s", strings.Join(args, " "), entry.Code, entry.Message)
			}
		}
	}

	if exit != 0 {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = trimmed
		}
		return "", fmt.Errorf("openspec %s: exit %d: %s", strings.Join(args, " "), exit, detail)
	}

	return stdout, nil
}

// List runs `openspec list --json` and decodes its changes and warnings.
func List(dir string) (ListResult, error) {
	out, err := cliResult(dir, "list", "--json")
	if err != nil {
		return ListResult{}, err
	}
	var result ListResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return ListResult{}, fmt.Errorf("openspec list --json: decode: %w", err)
	}
	return result, nil
}

// ListSpecs runs `openspec list --specs --json` and decodes the project's
// spec inventory.
func ListSpecs(dir string) ([]ListedSpec, error) {
	out, err := cliResult(dir, "list", "--specs", "--json")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Specs []ListedSpec `json:"specs"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil, fmt.Errorf("openspec list --specs --json: decode: %w", err)
	}
	return parsed.Specs, nil
}

// Status runs `openspec status --change <change> --json` and decodes the
// change's artifact graph, merging each artifacts[] entry with its matching
// artifactPaths[id].existingOutputPaths while preserving the CLI's
// artifacts[] order.
func Status(dir, change string) (StatusResult, error) {
	out, err := cliResult(dir, "status", "--change", change, "--json")
	if err != nil {
		return StatusResult{}, err
	}

	var parsed struct {
		ChangeName    string `json:"changeName"`
		SchemaName    string `json:"schemaName"`
		ArtifactPaths map[string]struct {
			ExistingOutputPaths []string `json:"existingOutputPaths"`
		} `json:"artifactPaths"`
		Artifacts []struct {
			ID         string   `json:"id"`
			OutputPath string   `json:"outputPath"`
			Requires   []string `json:"requires"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return StatusResult{}, fmt.Errorf("openspec status --change %s --json: decode: %w", change, err)
	}

	result := StatusResult{
		ChangeName: parsed.ChangeName,
		SchemaName: parsed.SchemaName,
	}
	for _, a := range parsed.Artifacts {
		result.Artifacts = append(result.Artifacts, ArtifactStatus{
			ID:                  a.ID,
			OutputPath:          a.OutputPath,
			Requires:            a.Requires,
			ExistingOutputPaths: parsed.ArtifactPaths[a.ID].ExistingOutputPaths,
		})
	}
	return result, nil
}

// Instructions runs `openspec instructions <artifact> --change <change>
// --json` and decodes the enriched authoring guidance for that artifact.
func Instructions(dir, artifact, change string) (ArtifactInstructions, error) {
	out, err := cliResult(dir, "instructions", artifact, "--change", change, "--json")
	if err != nil {
		return ArtifactInstructions{}, err
	}
	var result ArtifactInstructions
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return ArtifactInstructions{}, fmt.Errorf("openspec instructions %s --change %s --json: decode: %w", artifact, change, err)
	}
	return result, nil
}
