package tools

import (
	"errors"
	"fmt"
	"path"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// Guardrail surface loader (harden-surfaces.js::loadGuardrails port)
// ---------------------------------------------------------------------------

// surfaceGuardrail is one guardrail entry as exposed on the harden manifest's
// planGuardrails/executeGuardrails manifest surfaces.
//
// Named distinctly (and typed as a struct rather than plan.go's
// map[string]any) to avoid colliding with plan.go's own, pre-existing
// unexported loadGuardrails(mainRoot string) ([]map[string]any, string) —
// a different helper, with a different signature and a different purpose
// (plan_prepare's KD16 guardrail-list fold), that already lives in this
// same package.
type surfaceGuardrail struct {
	ID          string `json:"id"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

// surfaceLoadError is one non-fatal load failure recorded by any of the
// four harden-surfaces.js loader ports. It mirrors source's shared
// `errors` accumulator shape exactly: every loader does
// `errors.push({ surface, message })` on a soft failure, and
// harden-prepare.js's manifest.errors field is this same array, unmodified
// — so the wire shape here (surface, message) must match byte-for-byte.
type surfaceLoadError struct {
	Surface string `json:"surface"`
	Message string `json:"message"`
}

// loadSurfaceGuardrails is the Go port of harden-surfaces.js's
// loadGuardrails(projectRoot, sectionName, errors). It reads the named
// config section's guardrails list and maps each entry to {id, severity,
// description}, applying the same type-guarded defaults as source (a
// missing or wrong-typed field defaults rather than erroring: id -> "",
// severity -> "error", description -> ""). A missing section (or a
// guardrails value that isn't an array) is not an error — it yields an
// empty list, matching JS's `!sectionData || !Array.isArray(...)` guard and
// config.ErrNotFound handling already established by
// validateGuardrailsAction. A genuine read failure appends a
// {surface,message} entry to errs and also yields an empty list, mirroring
// source's try/catch around readSection.
func loadSurfaceGuardrails(root, section string, errs *[]surfaceLoadError) []surfaceGuardrail {
	data, err := config.ReadSection(root, section)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return []surfaceGuardrail{}
		}
		*errs = append(*errs, surfaceLoadError{
			Surface: section + "-guardrails",
			Message: fmt.Sprintf("readSection failed: %s", err.Error()),
		})
		return []surfaceGuardrail{}
	}

	raw, ok := data["guardrails"].([]any)
	if !ok {
		return []surfaceGuardrail{}
	}

	out := make([]surfaceGuardrail, 0, len(raw))
	for _, item := range raw {
		g, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, surfaceGuardrail{
			ID:          stringOrDefault(g["id"], ""),
			Severity:    stringOrDefault(g["severity"], "error"),
			Description: stringOrDefault(g["description"], ""),
		})
	}
	return out
}

// stringOrDefault returns v as a string when it decodes as one (mirroring
// JS's `typeof x === 'string' ? x : default`), else def. Shared by all four
// harden-surfaces.js loader ports for their JSON/YAML-decoded field
// defaults.
func stringOrDefault(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

// ---------------------------------------------------------------------------
// R16 guardrail pre-flight (harden-prepare.js lines 246-251, guardrails half)
// ---------------------------------------------------------------------------

// preflightFinding is one pre-flight check result that the harden manifest
// carries under preflightFindings. A finding does not stop the harden run:
// the harden orchestrator drafts a repair proposal for it.
type preflightFinding struct {
	// Surface is plan-guardrails, execute-guardrails or review-dimensions.
	Surface string `json:"surface"`
	// ID is the guardrail id, or the dimension name.
	ID string `json:"id"`
	// File is the repo-relative path of the file that holds the rule.
	File string `json:"file"`
	// Severity is error or warning.
	Severity string `json:"severity"`
	// Message is the validator message, unchanged.
	Message string `json:"message"`
	// Fix is the repair step. It is "" for dimension findings.
	Fix string `json:"fix"`
	// BytesOver is how many bytes the guardrail description is above
	// guardrailDescMaxBytes. Omitted when the description is not too long.
	BytesOver int `json:"bytesOver,omitempty"`
}

// guardrailsPreflight runs validateGuardrailsAction against the "plan" and
// "execute" guardrail sections under root and returns each finding as one
// preflightFinding. A guardrail finding does not stop the run. Only a config
// read or parse error stops it: the InfraError of validateGuardrailsAction
// (or of guardrailDescriptions) is returned unchanged, because no repair is
// possible without a parsed rule set.
func guardrailsPreflight(root string) ([]preflightFinding, error) {
	var out []preflightFinding
	for _, section := range []string{"plan", "execute"} {
		findings, err := validateGuardrailsAction(root, ValidateIn{Action: "guardrails", Section: section})
		if err != nil {
			return nil, err
		}
		if len(findings) == 0 {
			continue
		}
		descs, err := guardrailDescriptions(root, section)
		if err != nil {
			return nil, err
		}
		for _, f := range findings {
			out = append(out, preflightFinding{
				Surface:   section + "-guardrails",
				ID:        f.ID,
				File:      path.Join(paths.DataDir, paths.ConfigFile),
				Severity:  f.Severity,
				Message:   f.Message,
				Fix:       f.Fix,
				BytesOver: bytesOver(descs[f.ID]),
			})
		}
	}
	return out, nil
}

// guardrailDescriptions maps each guardrail id of the named config section
// under root to its description. A missing section gives an empty map. Any
// other read error is an InfraError with the same text as the one of
// validateGuardrailsAction. An entry with no string id or no string
// description is left out.
func guardrailDescriptions(root, section string) (map[string]string, error) {
	descs := map[string]string{}
	data, err := config.ReadSection(root, section)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return descs, nil
		}
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read %s guardrails section: %s", section, err.Error()),
			Suggestion: fmt.Sprintf("Check that config.toml's [%s] section is valid TOML and readable, then retry.", section),
			Cause:      err,
		}
	}
	raw, _ := data["guardrails"].([]any)
	for _, item := range raw {
		g, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, idOK := g["id"].(string)
		desc, descOK := g["description"].(string)
		if idOK && descOK {
			descs[id] = desc
		}
	}
	return descs, nil
}

// bytesOver returns how many bytes desc is above guardrailDescMaxBytes, or 0
// when desc is not longer than the limit.
func bytesOver(desc string) int {
	if over := len(desc) - guardrailDescMaxBytes; over > 0 {
		return over
	}
	return 0
}
