package shipmeta

import (
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// Todo mirrors the {content, activeForm, status} shape TodoWrite consumes,
// as produced by renderTodos() in scripts/lib/ship-todos.js. JSON tags match
// the TodoWrite field names for callers (Task 35 ship_state `todos` action)
// that marshal this for TodoWrite.
type Todo struct {
	Content    string `json:"content"`
	ActiveForm string `json:"activeForm"`
	Status     string `json:"status"`
}

// SubstepMap is the static substep list for each ship pipeline step —
// single source of truth; do not duplicate elsewhere (mirrors SUBSTEP_MAP in
// scripts/lib/ship-todos.js). The "execute" entry is overridden by
// execute task mirroring at the call site in the source implementation
// (ship-todos.js parseSubsteps) when plan tasks are available.
var SubstepMap = map[string][]string{
	"execute":             {"execute plan"},
	"commit":              {"stash unstaged", "generate message", "commit", "restore stash"},
	"review":              {"dispatch review dimensions", "collect verdicts"},
	"received-review":     {"fetch comments", "classify findings", "apply auto-fixes", "surface remaining"},
	"commit-fixes":        {"re-stage", "commit fixes"},
	"verify-openspec":     {"openspec validate --strict", "check result"},
	"archive-openspec":    {"validate", "run archive", "stage", "commit"},
	"pr":                  {"push branch", "draft body", "gh pr create", "apply labels"},
	"verify-pipeline":     {"poll checks", "fetch logs on failure", "analyze", "commit fix if any"},
	"await-remote-review": {"poll reviews", "dispatch received-review if actionable", "commit fix if any"},
	"learnings-commit":    {"append log"},
	"cleanup":             {"cleanup pipeline state"},
}

// TodosForStep renders the TodoWrite-shaped todo list for the whole ship
// pipeline with step treated as the current (in_progress) step. It mirrors
// stepTransition(state, stepName) in scripts/lib/ship-todos.js, i.e.
// renderTodos(state, {event: 'step', currentStep: step}) with every other
// renderTodos option (substep, markCompleted, failStep, planTasks) left at
// its zero value.
//
// Two intentional divergences from the source (see Task 19 DECISIONS):
//   - the source also returns a marker string
//     ("[task-tray] step ...: pending=N, ..."); this port returns only the
//     todos slice, since the contracted return type is []Todo.
//   - the source throws when state.steps is a non-array value
//     (R-SHIPTODOS-FAILLOUD); this port has no error return, so a
//     non-array (or absent) `steps` field is treated as "no step has a
//     recorded status" rather than raising.
func TodosForStep(step string, st *state.State) []Todo {
	var data map[string]any
	if st != nil {
		data = st.Data
	}

	pipelineSteps := flagSteps(data)
	if !containsString(pipelineSteps, "cleanup") {
		pipelineSteps = append(pipelineSteps, "cleanup")
	}
	statusByStep := stepStatuses(data)

	var todos []Todo
	for _, stepName := range pipelineSteps {
		substeps := SubstepMap[stepName]
		if len(substeps) == 0 {
			substeps = []string{stepName}
		}

		var baseStatus, labelSuffix string
		switch recordedStatus := statusByStep[stepName]; {
		case recordedStatus == "completed":
			baseStatus = "completed"
		case recordedStatus == "skipped":
			baseStatus, labelSuffix = "completed", " (skipped)"
		case recordedStatus == "failed":
			baseStatus, labelSuffix = "completed", " (failed)"
		case stepName == step:
			baseStatus = "in_progress"
		case recordedStatus == "in_progress":
			baseStatus = "in_progress"
		default:
			baseStatus = "pending"
		}

		for i, sub := range substeps {
			content := substepContent(stepName, sub) + labelSuffix
			status := baseStatus
			if stepName == step && baseStatus != "completed" {
				if i == 0 {
					status = "in_progress"
				} else {
					status = "pending"
				}
			}
			todos = append(todos, Todo{
				Content:    content,
				ActiveForm: capitalize(sub),
				Status:     status,
			})
		}
	}

	return todos
}

// flagSteps reads the configured pipeline step order from data["flags"]["steps"].
// Returns nil when absent or malformed (mirrors treating a missing flags.steps
// as an empty pipeline in the source).
func flagSteps(data map[string]any) []string {
	flags, ok := data["flags"].(map[string]any)
	if !ok {
		return nil
	}
	rawSteps, ok := flags["steps"].([]any)
	if !ok {
		return nil
	}
	steps := make([]string, 0, len(rawSteps))
	for _, s := range rawSteps {
		if name, ok := s.(string); ok {
			steps = append(steps, name)
		}
	}
	return steps
}

// stepStatuses reads per-step recorded status from data["steps"], an array of
// {name, status} entries. A non-array or absent `steps` field is treated as
// "no step has a recorded status" — see the R-SHIPTODOS-FAILLOUD divergence
// noted on TodosForStep.
func stepStatuses(data map[string]any) map[string]string {
	statuses := make(map[string]string)
	rawSteps, ok := data["steps"].([]any)
	if !ok {
		return statuses
	}
	for _, s := range rawSteps {
		entry, ok := s.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		status, _ := entry["status"].(string)
		if name != "" {
			statuses[name] = status
		}
	}
	return statuses
}

// containsString reports whether target is present in list.
func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

// substepContent renders the "<Step name>: <substep>" content label, mirroring
// the non-plan-task branch of substepLabels() in scripts/lib/ship-todos.js.
func substepContent(stepName, substep string) string {
	return capitalize(strings.ReplaceAll(stepName, "-", " ")) + ": " + substep
}

// capitalize upper-cases the first byte of s, mirroring
// str.charAt(0).toUpperCase() + str.slice(1) in scripts/lib/ship-todos.js.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
