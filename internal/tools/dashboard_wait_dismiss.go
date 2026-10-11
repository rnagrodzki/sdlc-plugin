package tools

import (
	"fmt"

	"github.com/rnagrodzki/sdlc-plugin/internal/attention"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// dashboardDismissWaitWriteSuggestion is the Suggestion of a failed remove of
// a wait record.
const dashboardDismissWaitWriteSuggestion = "Check write permission on .sdlc-v2/evidence/attention/ and retry."

// dashboardDismissWaitInputError returns the DomainError for a request that
// breaks an input rule of DashboardDismissWait, or nil for a good request.
func dashboardDismissWaitInputError(kind, sessionID, toolUseID string) error {
	var msg string
	switch {
	case kind != attention.KindQuestion && kind != attention.KindPermission:
		msg = fmt.Sprintf("The kind %q is not %q or %q", kind, attention.KindQuestion, attention.KindPermission)
	case sessionID == "":
		msg = "The sessionId field is required"
	case kind == attention.KindQuestion && toolUseID == "":
		msg = "A question wait needs its toolUseId"
	case kind == attention.KindPermission && toolUseID != "":
		msg = "A permission wait has no toolUseId"
	default:
		return nil
	}
	return &mcpserver.DomainError{Msg: msg, Suggestion: dashboardDeleteReloadSuggestion}
}

// DashboardDismissWait deletes one wait record of root, from the attention
// folder .sdlc-v2/evidence/attention/. A question wait is the file
// <sid>-<toolUseId>.json. A permission wait is the file <sid>-permission.json.
// Any other record of the session stays. A record that is already gone gives
// AlreadyGone and no error.
//
// An empty toolUseId for a question is an error: attention.DeleteQuestion
// would delete every question of the session. The names go through the
// attention package, so a "/" in an ID becomes "-" in the file name.
//
// Errors: an input that breaks a rule gives a DomainError. A remove that fails
// gives an InfraError.
func DashboardDismissWait(root, kind, sessionID, toolUseID string) (DashboardDeleteOut, error) {
	if err := dashboardDismissWaitInputError(kind, sessionID, toolUseID); err != nil {
		return DashboardDeleteOut{}, err
	}

	removed, err := attention.Remove(root, kind, sessionID, toolUseID)
	if err != nil {
		return DashboardDeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("Could not dismiss the wait: %s", err),
			Suggestion: dashboardDismissWaitWriteSuggestion,
			Cause:      err,
		}
	}
	if !removed {
		return DashboardDeleteOut{AlreadyGone: true, Message: "The wait is already gone."}, nil
	}
	return DashboardDeleteOut{Deleted: true, Message: "The wait is dismissed."}, nil
}
