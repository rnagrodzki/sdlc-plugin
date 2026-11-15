package web

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// Codes of the errors that the four Setup routes raise.
const (
	// codeSetupInvalid is the code of a save with a value that the config check
	// refuses.
	codeSetupInvalid = "SETUP_INVALID"
	// codeSetupNotReady is the code of a call that the state of the repo does
	// not allow.
	codeSetupNotReady = "SETUP_NOT_READY"
	// codeSetupReadFailed is the code of a failed read of the Setup state.
	codeSetupReadFailed = "SETUP_READ_FAILED"
	// codeSetupSaveFailed is the code of a save that fails with an error.
	codeSetupSaveFailed = "SETUP_SAVE_FAILED"
	// codeSetupCreateFailed is the code of a create that fails with an error.
	codeSetupCreateFailed = "SETUP_CREATE_FAILED"
)

// setupSaveRequest is the body of POST /api/setup-save.
type setupSaveRequest struct {
	Repo                       string `json:"repo"`
	tools.DashboardSetupSaveIn        // changes, targets
}

// setupCreateRequest is the body of POST /api/setup-create.
type setupCreateRequest struct {
	Repo string `json:"repo"`
}

// serveSetupFields returns the setup sections and their fields. It only reads
// and takes no repo, so it needs no token.
func (h *handler) serveSetupFields(w http.ResponseWriter, r *http.Request) {
	if h.o.SetupFields == nil {
		writeNotConfigured(w, "Setup fields")
		return
	}
	writeJSON(w, http.StatusOK, h.o.SetupFields())
}

// serveSetupState returns the config state and values of one registered repo.
// It only reads, so it needs no token. It reads only the query key repo; it
// does not use detailQuery, which refuses an empty id.
func (h *handler) serveSetupState(w http.ResponseWriter, r *http.Request) {
	if h.o.SetupState == nil {
		writeNotConfigured(w, "Setup state")
		return
	}
	repo := r.URL.Query().Get("repo")
	if repoTooLong(w, repo) {
		return
	}
	root, ok := h.displayRoot(w, now(), repo)
	if !ok {
		return
	}
	out, err := h.o.SetupState(root)
	if err != nil {
		writeSetupError(w, codeSetupReadFailed, "setup state", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// serveSetupSave saves changed fields of the Setup form in one registered
// repo (serveWriteWith). Its errors go through writeSetupError. A save that
// wrote only some sections is a 200 with ok false.
func (h *handler) serveSetupSave(w http.ResponseWriter, r *http.Request) {
	serveWriteWith(h, w, r, "Setup save", h.o.SetupSave != nil,
		func(req setupSaveRequest) string { return req.Repo },
		func(root string, req setupSaveRequest) (any, error) {
			return h.o.SetupSave(root, req.DashboardSetupSaveIn)
		},
		func(w http.ResponseWriter, err error) { writeSetupError(w, codeSetupSaveFailed, "setup save", err) })
}

// serveSetupCreate creates the first config files of one registered repo
// (serveWriteWith). Its errors go through writeSetupError.
func (h *handler) serveSetupCreate(w http.ResponseWriter, r *http.Request) {
	serveWriteWith(h, w, r, "Setup create", h.o.SetupCreate != nil,
		func(req setupCreateRequest) string { return req.Repo },
		func(root string, _ setupCreateRequest) (any, error) { return h.o.SetupCreate(root) },
		func(w http.ResponseWriter, err error) { writeSetupError(w, codeSetupCreateFailed, "setup create", err) })
}

// writeSetupError maps an error of a Setup function to a status and a code.
// failCode is the code of the 500 answer for the route (read, save or create).
//   - *tools.DashboardSetupInvalid: 400 SETUP_INVALID with the fields list.
//   - *tools.DashboardSetupNotReady: 409 SETUP_NOT_READY.
//   - DomainError: 400 BAD_REQUEST.
//   - any other error: 500 with failCode, written to the server log.
func writeSetupError(w http.ResponseWriter, failCode, what string, err error) {
	var invalid *tools.DashboardSetupInvalid
	if errors.As(err, &invalid) {
		message := tools.SetupRefusedText(len(invalid.Fields)) + " by the config check"
		fields := invalid.Fields
		if fields == nil {
			fields = []tools.SetupFieldError{}
		}
		type invalidError struct {
			Code       string                  `json:"code"`
			Message    string                  `json:"message"`
			Suggestion string                  `json:"suggestion"`
			Fields     []tools.SetupFieldError `json:"fields"`
		}
		writeJSON(w, http.StatusBadRequest, map[string]invalidError{"error": {
			Code:       codeSetupInvalid,
			Message:    message,
			Suggestion: "Correct each field, then save again. No file changed.",
			Fields:     fields,
		}})
		return
	}
	var notReady *tools.DashboardSetupNotReady
	if errors.As(err, &notReady) {
		suggestion := notReady.Suggestion
		if suggestion == "" {
			suggestion = suggestReloadPage
		}
		writeAPIError(w, http.StatusConflict, codeSetupNotReady, notReady.Error(), suggestion)
		return
	}
	var de *mcpserver.DomainError
	if errors.As(err, &de) {
		suggestion := de.Suggestion
		if suggestion == "" {
			suggestion = suggestBadRequest
		}
		writeAPIError(w, http.StatusBadRequest, codeBadRequest, de.Msg, suggestion)
		return
	}
	message, suggestion := apiErrorText(err)
	fmt.Fprintf(stderr, "sdlc dashboard: %s: %v\n", what, err)
	writeAPIError(w, http.StatusInternalServerError, failCode, message, suggestion)
}
