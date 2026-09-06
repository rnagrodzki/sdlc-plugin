package tools

import (
	"errors"
	"fmt"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
)

// ---------------------------------------------------------------------------
// Guardrail surface loader (harden-surfaces.js::loadGuardrails port)
// ---------------------------------------------------------------------------

// surfaceGuardrail is one guardrail entry as exposed on harden_prepare's
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

// guardrailsPreflight runs validateGuardrailsAction (Task 36,
// internal/tools/validators.go, unexported, called directly in-process
// since this file shares its package) against the "plan" and "execute"
// guardrail sections and formats every resulting finding as a
// manifest-ready error string prefixed "existing-<section>-guardrails: ",
// mirroring source's R16 pre-flight loop:
//
//	for (const section of ['plan', 'execute']) {
//	  const result = validateGuardrailsConfig(projectRoot, section);
//	  for (const err of result.errors) {
//	    preflightErrors.push(`existing-${section}-guardrails: ${err}`);
//	  }
//	}
//
// validateGuardrailsAction returns structured findings rather than
// throwing, so both a real read failure (the Go equivalent of source's
// catch) and a non-empty findings slice are folded into the same
// prefixed-string shape here — the adaptation called out in this task's
// fact sheet dependency note.
func guardrailsPreflight(root string) []string {
	var errs []string
	for _, section := range []string{"plan", "execute"} {
		findings, err := validateGuardrailsAction(root, ValidateIn{Action: "guardrails", Section: section})
		if err != nil {
			errs = append(errs, fmt.Sprintf("existing-%s-guardrails: %s", section, err.Error()))
			continue
		}
		for _, f := range findings {
			errs = append(errs, fmt.Sprintf("existing-%s-guardrails: %s", section, f.Message))
		}
	}
	return errs
}
