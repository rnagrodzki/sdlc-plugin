package hooks

import (
	"os"
	"path/filepath"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/attention"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// permissionPromptType is the notification_type of a Notification event that
// reports a wait for a tool permission decision.
const permissionPromptType = "permission_prompt"

// permissionWaitHeader is the Header of every permission wait record.
const permissionWaitHeader = "Permission"

// recordPermissionWait is the "record-permission-wait" hook handler
// (Notification, matcher permission_prompt). It writes the permission wait
// record of the session: Header "Permission" and Text from the notification
// "message" (attention.Write redacts and caps it). A second prompt of the same
// session replaces the first record.
//
// It writes nothing, and returns no error, when notification_type is not
// "permission_prompt", the session ID is empty, the root or branch does not
// resolve, or the repo has no data directory (.sdlc-v2/). A write failure is
// returned, so Run reports it on stderr; the exit code stays 0 and stdout stays
// empty on every path.
func recordPermissionWait(ctx HookCtx, event Event) (Output, error) {
	silent := Output{}
	notificationType, _ := event.Raw["notification_type"].(string)
	if notificationType != permissionPromptType || ctx.SessionID == "" {
		return silent, nil
	}
	root, branch, ok := resolveRootBranch()
	if !ok {
		return silent, nil
	}
	if _, err := os.Stat(filepath.Join(root, paths.DataDir)); err != nil {
		return silent, nil
	}
	message, _ := event.Raw["message"].(string)
	err := attention.Write(root, attention.Record{
		Kind:      attention.KindPermission,
		SessionID: ctx.SessionID,
		Branch:    branch,
		Header:    permissionWaitHeader,
		Text:      message,
		AskedAt:   time.Now().UTC().Format(time.RFC3339),
	})
	return silent, err
}

// closePermissionWait is the "close-permission-wait" hook handler (PostToolUse
// and PostToolUseFailure, both async). It deletes the permission wait record of
// the session: a finished tool call means the permission decision is made.
//
// It runs on every tool call, so it does the least work possible: one root
// lookup and one file remove. It does not resolve the branch and does not
// create the attention folder. With no record it writes nothing. It returns a
// delete error, so Run reports it on stderr; the exit code stays 0 and stdout
// stays empty on every path.
func closePermissionWait(ctx HookCtx, _ Event) (Output, error) {
	return deleteWaits(ctx, attention.DeletePermission)
}

// closeSessionWaits is the "close-session-waits" hook handler (SessionEnd). It
// deletes every wait record of the session, of any kind, because a session
// that has ended cannot answer a question or a permission prompt. It returns a
// delete error, so Run reports it on stderr; the exit code stays 0 and stdout
// stays empty on every path.
func closeSessionWaits(ctx HookCtx, _ Event) (Output, error) {
	return deleteWaits(ctx, attention.DeleteSession)
}

// deleteWaits resolves the main worktree root and calls del for the session of
// ctx. It does nothing when the session ID is empty or the root does not
// resolve (a directory outside a git repo has no wait records). It returns the
// error of del.
func deleteWaits(ctx HookCtx, del func(mainRoot, sessionID string) error) (Output, error) {
	if ctx.SessionID == "" {
		return Output{}, nil
	}
	root, err := mainRootFunc()
	if err != nil {
		return Output{}, nil
	}
	return Output{}, del(root, ctx.SessionID)
}
