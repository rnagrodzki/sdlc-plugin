package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// fakeSetup is the four Setup functions of Options. Each one records its
// arguments and returns the output and error that the test sets.
type fakeSetup struct {
	err error // returned by the state, save and create functions

	fieldsCalls int
	stateRoots  []string
	saveRoots   []string
	saveIns     []tools.DashboardSetupSaveIn
	createRoots []string

	stateOut  tools.DashboardSetupStateOut
	saveOut   tools.DashboardSetupSaveOut
	createOut tools.DashboardSetupCreateOut
}

// fields counts the call and returns an empty fields output.
func (f *fakeSetup) fields() tools.DashboardSetupFieldsOut {
	f.fieldsCalls++
	return tools.DashboardSetupFieldsOut{}
}

// state records the root and returns stateOut and err.
func (f *fakeSetup) state(root string) (tools.DashboardSetupStateOut, error) {
	f.stateRoots = append(f.stateRoots, root)
	return f.stateOut, f.err
}

// save records the root and the input and returns saveOut and err.
func (f *fakeSetup) save(root string, in tools.DashboardSetupSaveIn) (tools.DashboardSetupSaveOut, error) {
	f.saveRoots = append(f.saveRoots, root)
	f.saveIns = append(f.saveIns, in)
	return f.saveOut, f.err
}

// create records the root and returns createOut and err.
func (f *fakeSetup) create(root string) (tools.DashboardSetupCreateOut, error) {
	f.createRoots = append(f.createRoots, root)
	return f.createOut, f.err
}

// calls returns the number of calls to the state, save and create functions.
func (f *fakeSetup) calls() int {
	return len(f.stateRoots) + len(f.saveRoots) + len(f.createRoots)
}

// newSetupHandler builds a request handler whose Setup functions are the ones
// of f. The only registered repo is /repo/a.
func newSetupHandler(t *testing.T, f *fakeSetup) *handler {
	t.Helper()
	o := testOptions(t, &fakeCollector{repo: "a"})
	o.SetupFields, o.SetupState, o.SetupSave, o.SetupCreate = f.fields, f.state, f.save, f.create
	return newHandler(context.Background(), o, testToken, 4242, time.Unix(1000, 0).UTC(), func() {})
}

// setupErrorBody is the JSON error of a Setup route, with the fields list of a
// SETUP_INVALID answer.
type setupErrorBody struct {
	Error struct {
		Code       string                  `json:"code"`
		Message    string                  `json:"message"`
		Suggestion string                  `json:"suggestion"`
		Fields     []tools.SetupFieldError `json:"fields"`
	} `json:"error"`
}

// TestHandler_SetupRoutes pins the answers of the four Setup routes: the
// payload of each success and each row of the error table.
func TestHandler_SetupRoutes(t *testing.T) {
	t.Run("fields", func(t *testing.T) {
		f := &fakeSetup{}
		rec := do(newSetupHandler(t, f), "GET", "/api/setup-fields", testHost, nil)
		if rec.Code != 200 || f.fieldsCalls != 1 {
			t.Fatalf("status = %d, calls = %d; want 200 and 1 call", rec.Code, f.fieldsCalls)
		}
		if got := strings.TrimSpace(rec.Body.String()); !strings.HasPrefix(got, `{"sections":`) {
			t.Errorf("body = %q; want a sections key", got)
		}
	})

	t.Run("state ok", func(t *testing.T) {
		f := &fakeSetup{stateOut: tools.DashboardSetupStateOut{Root: "/repo/a", State: tools.SetupStateReady}}
		rec := do(newSetupHandler(t, f), "GET", "/api/setup-state?repo=%2Frepo%2Fa%2F", testHost, nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d (body %q); want 200", rec.Code, rec.Body.String())
		}
		if !reflect.DeepEqual(f.stateRoots, []string{"/repo/a"}) {
			t.Errorf("state roots = %v; want the cleaned display root", f.stateRoots)
		}
		var got tools.DashboardSetupStateOut
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.State != tools.SetupStateReady || got.Root != "/repo/a" {
			t.Errorf("body = %q (%v); want the state out of the function", rec.Body.String(), err)
		}
	})

	t.Run("save ok", func(t *testing.T) {
		f := &fakeSetup{saveOut: tools.DashboardSetupSaveOut{
			OK:       true,
			Written:  []tools.DashboardSetupWrite{{Section: "ship", File: "/repo/a/.sdlc-v2/local.toml"}},
			Failed:   []tools.DashboardSetupFailure{},
			Warnings: []string{},
			CIFiles:  []string{},
		}}
		body := `{"repo":"/repo/a","changes":[{"section":"ship","field":"bump","op":"set","value":"minor"}],"targets":{"ship":"project"}}`
		rec := postJSON(newSetupHandler(t, f), "/api/setup-save", body, mutationHeader())
		if rec.Code != 200 {
			t.Fatalf("status = %d (body %q); want 200", rec.Code, rec.Body.String())
		}
		want := tools.DashboardSetupSaveIn{
			Changes: []tools.DashboardSetupChange{{Section: "ship", Field: "bump", Op: "set", Value: "minor"}},
			Targets: map[string]string{"ship": "project"},
		}
		if !reflect.DeepEqual(f.saveRoots, []string{"/repo/a"}) || !reflect.DeepEqual(f.saveIns, []tools.DashboardSetupSaveIn{want}) {
			t.Errorf("save calls = %v %+v; want one call with %+v", f.saveRoots, f.saveIns, want)
		}
		for _, key := range []string{`"ok":true`, `"written":[{"section":"ship"`, `"failed":[]`, `"warnings":[]`, `"ciFiles":[]`} {
			if !strings.Contains(rec.Body.String(), key) {
				t.Errorf("body %q lacks %s", rec.Body.String(), key)
			}
		}
	})

	t.Run("save partly written is 200 with ok false", func(t *testing.T) {
		f := &fakeSetup{saveOut: tools.DashboardSetupSaveOut{
			OK:     false,
			Failed: []tools.DashboardSetupFailure{{Section: "ship", File: "/repo/a/.sdlc-v2/local.toml", Message: "disk full"}},
		}}
		rec := postJSON(newSetupHandler(t, f), "/api/setup-save", `{"repo":"/repo/a","changes":[]}`, mutationHeader())
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), "disk full") {
			t.Errorf("status = %d, body = %q; want 200 with ok false and the failure", rec.Code, rec.Body.String())
		}
	})

	t.Run("create ok", func(t *testing.T) {
		f := &fakeSetup{createOut: tools.DashboardSetupCreateOut{
			OK: true, Created: []string{".sdlc-v2/config.toml"}, Changed: []string{}, Errors: []string{},
		}}
		rec := postJSON(newSetupHandler(t, f), "/api/setup-create", `{"repo":"/repo/a"}`, mutationHeader())
		if rec.Code != 200 || !reflect.DeepEqual(f.createRoots, []string{"/repo/a"}) {
			t.Fatalf("status = %d, roots = %v; want 200 and one call", rec.Code, f.createRoots)
		}
		if !strings.Contains(rec.Body.String(), `"created":[".sdlc-v2/config.toml"]`) || !strings.Contains(rec.Body.String(), `"errors":[]`) {
			t.Errorf("body = %q", rec.Body.String())
		}
	})

	runSetupErrorRows(t)
	runSetupRepoErrors(t)
}

// runSetupErrorRows runs the table of errors that a Setup function returns,
// for each of the three routes that call one.
func runSetupErrorRows(t *testing.T) {
	invalid1 := &tools.DashboardSetupInvalid{Fields: []tools.SetupFieldError{{Section: "ship", Field: "bump", Path: "ship.bump", Message: "bad"}}}
	invalid2 := &tools.DashboardSetupInvalid{Fields: []tools.SetupFieldError{
		{Section: "ship", Field: "bump", Path: "ship.bump", Message: "bad"},
		{Section: "ship", Field: "draft", Path: "ship.draft", Message: "worse"},
	}}
	invalidNil := &tools.DashboardSetupInvalid{}
	notReady := &tools.DashboardSetupNotReady{State: tools.SetupStateNoConfig, Suggestion: "Select Create config."}
	notReadyBare := &tools.DashboardSetupNotReady{State: tools.SetupStateNoConfig}
	domain := &mcpserver.DomainError{Msg: "changes holds too many entries", Suggestion: "Send fewer."}
	domainBare := &mcpserver.DomainError{Msg: "rule broken"}
	data := &mcpserver.DataError{Msg: "cannot read config.toml", Suggestion: "Check the file."}

	type row struct {
		name, route string
		err         error
		status      int
		code        string
		message     string
		suggestion  string
		fields      int
	}
	rows := []row{
		{"state: domain error", "state", domain, 400, codeBadRequest, "changes holds too many entries", "Send fewer.", 0},
		{"state: domain error without suggestion", "state", domainBare, 400, codeBadRequest, "rule broken", suggestBadRequest, 0},
		{"state: read fails with a typed error", "state", data, 500, codeSetupReadFailed, "cannot read config.toml", "Check the file.", 0},
		{"state: read fails with a plain error", "state", errors.New("boom"), 500, codeSetupReadFailed, "boom", suggestReadLog, 0},
		{"save: domain error", "save", domain, 400, codeBadRequest, "changes holds too many entries", "Send fewer.", 0},
		{"save: one refused value", "save", invalid1, 400, codeSetupInvalid, "1 field has a value that the config check refuses", "Correct each field, then save again. No file changed.", 1},
		{"save: two refused values", "save", invalid2, 400, codeSetupInvalid, "2 fields have a value that the config check refuses", "Correct each field, then save again. No file changed.", 2},
		{"save: refused with a nil fields list", "save", invalidNil, 400, codeSetupInvalid, "0 fields have a value that the config check refuses", "Correct each field, then save again. No file changed.", 0},
		{"save: state does not allow", "save", notReady, 409, codeSetupNotReady, notReady.Error(), "Select Create config.", 0},
		{"save: state does not allow, no suggestion", "save", notReadyBare, 409, codeSetupNotReady, notReadyBare.Error(), suggestReloadPage, 0},
		{"save: fails with a typed error", "save", data, 500, codeSetupSaveFailed, "cannot read config.toml", "Check the file.", 0},
		{"save: fails with a plain error", "save", errors.New("boom"), 500, codeSetupSaveFailed, "boom", suggestReadLog, 0},
		{"create: domain error", "create", domain, 400, codeBadRequest, "changes holds too many entries", "Send fewer.", 0},
		{"create: state does not allow", "create", notReady, 409, codeSetupNotReady, notReady.Error(), "Select Create config.", 0},
		{"create: fails with a typed error", "create", data, 500, codeSetupCreateFailed, "cannot read config.toml", "Check the file.", 0},
		{"create: fails with a plain error", "create", errors.New("boom"), 500, codeSetupCreateFailed, "boom", suggestReadLog, 0},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			logged := captureStderr(t)
			h := newSetupHandler(t, &fakeSetup{err: tc.err})
			var rec = do(h, "GET", "/api/setup-state?repo=%2Frepo%2Fa", testHost, nil)
			switch tc.route {
			case "save":
				rec = postJSON(h, "/api/setup-save", `{"repo":"/repo/a","changes":[]}`, mutationHeader())
			case "create":
				rec = postJSON(h, "/api/setup-create", `{"repo":"/repo/a"}`, mutationHeader())
			}
			wantAPIError(t, rec, tc.status, tc.code)
			var body setupErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Message != tc.message || body.Error.Suggestion != tc.suggestion {
				t.Errorf("message = %q, suggestion = %q; want %q, %q", body.Error.Message, body.Error.Suggestion, tc.message, tc.suggestion)
			}
			if len(body.Error.Fields) != tc.fields {
				t.Errorf("fields = %+v; want %d", body.Error.Fields, tc.fields)
			}
			if tc.code == codeSetupInvalid && tc.fields == 0 && !strings.Contains(rec.Body.String(), `"fields":[]`) {
				t.Errorf("body %q; want an empty fields list, not null", rec.Body.String())
			}
			if tc.fields > 0 && !strings.Contains(rec.Body.String(), `"path":"ship.bump"`) {
				t.Errorf("body %q lacks the field path", rec.Body.String())
			}
			if tc.status == 500 && !strings.Contains(logged.String(), tc.err.Error()) {
				t.Errorf("the cause was not logged; log = %q", logged.String())
			}
		})
	}

}

// runSetupRepoErrors runs the request errors that the three routes share: an
// absent, too long or unknown repo and a body that is not JSON.
func runSetupRepoErrors(t *testing.T) {
	t.Run("repo errors", func(t *testing.T) {
		long := strings.Repeat("a", maxDetailRepo+1)
		cases := []struct {
			name, method, path, body string
			status                   int
			code                     string
		}{
			{"state: repo absent", "GET", "/api/setup-state", "", 400, codeBadRequest},
			{"state: repo too long", "GET", "/api/setup-state?repo=" + url.QueryEscape(long), "", 400, codeBadRequest},
			{"state: repo not shown", "GET", "/api/setup-state?repo=%2Frepo%2Fother", "", 404, codeRepoNotFound},
			{"save: repo absent", "POST", "/api/setup-save", `{"changes":[]}`, 400, codeBadRequest},
			{"save: repo too long", "POST", "/api/setup-save", `{"repo":"` + long + `","changes":[]}`, 400, codeBadRequest},
			{"save: repo not shown", "POST", "/api/setup-save", `{"repo":"/repo/other","changes":[]}`, 404, codeRepoNotFound},
			{"save: body not JSON", "POST", "/api/setup-save", `{"repo"`, 400, codeBadRequest},
			{"create: repo absent", "POST", "/api/setup-create", `{}`, 400, codeBadRequest},
			{"create: repo too long", "POST", "/api/setup-create", `{"repo":"` + long + `"}`, 400, codeBadRequest},
			{"create: repo not shown", "POST", "/api/setup-create", `{"repo":"/repo/other"}`, 404, codeRepoNotFound},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				f := &fakeSetup{}
				h := newSetupHandler(t, f)
				var rec = do(h, tc.method, tc.path, testHost, nil)
				if tc.method == "POST" {
					rec = postJSON(h, tc.path, tc.body, mutationHeader())
				}
				body := wantAPIError(t, rec, tc.status, tc.code)
				if tc.code == codeRepoNotFound && body.Error.Message != repoNotFoundMessage("/repo/other") {
					t.Errorf("message = %q", body.Error.Message)
				}
				if f.calls() != 0 {
					t.Errorf("a Setup function ran %d times; want 0", f.calls())
				}
			})
		}
	})
}

// TestHandler_SetupPostGuards pins that both POST routes refuse a wrong
// Origin, an absent token, a wrong content type and a body over 8 KiB, like
// the other change routes, and call nothing.
func TestHandler_SetupPostGuards(t *testing.T) {
	routes := []struct{ path, body string }{
		{"/api/setup-save", `{"repo":"/repo/a","changes":[]}`},
		{"/api/setup-create", `{"repo":"/repo/a"}`},
	}
	with := func(k, v string) map[string]string {
		m := mutationHeader()
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name   string
		header map[string]string
		pad    int
		status int
		code   string
	}{
		{"good request passes", mutationHeader(), 0, 200, ""},
		{"missing origin", with("Origin", ""), 0, 403, "FORBIDDEN_ORIGIN"},
		{"foreign origin", with("Origin", "http://evil.example"), 0, 403, "FORBIDDEN_ORIGIN"},
		{"missing token", with(tokenHeader, ""), 0, 403, "FORBIDDEN_TOKEN"},
		{"wrong token", with(tokenHeader, strings.Repeat("f", 64)), 0, 403, "FORBIDDEN_TOKEN"},
		{"missing content type", with("Content-Type", ""), 0, 415, "BAD_CONTENT_TYPE"},
		{"text content type", with("Content-Type", "text/plain"), 0, 415, "BAD_CONTENT_TYPE"},
		{"body over 8 KiB", mutationHeader(), maxMutationBody + 1, 413, "BODY_TOO_LARGE"},
	}
	for _, rt := range routes {
		for _, tc := range cases {
			t.Run(strings.TrimPrefix(rt.path, "/api/")+" "+tc.name, func(t *testing.T) {
				f := &fakeSetup{}
				h := newSetupHandler(t, f)
				body := rt.body
				if tc.pad > 0 {
					body = strings.TrimSuffix(rt.body, "}") + `,"pad":"` + strings.Repeat("x", tc.pad) + `"}`
				}
				rec := postJSON(h, rt.path, body, tc.header)
				if tc.code == "" {
					if rec.Code != tc.status {
						t.Fatalf("status = %d (body %q); want %d", rec.Code, rec.Body.String(), tc.status)
					}
					if f.calls() != 1 {
						t.Errorf("function calls = %d; want 1", f.calls())
					}
					return
				}
				wantAPIError(t, rec, tc.status, tc.code)
				if f.calls() != 0 {
					t.Errorf("a refused request made %d calls; want 0", f.calls())
				}
			})
		}
	}
}

// TestHandler_SetupReadRoutesNeedNoToken pins that the two GET routes work
// without Origin and token, like the other read routes.
func TestHandler_SetupReadRoutesNeedNoToken(t *testing.T) {
	h := newSetupHandler(t, &fakeSetup{})
	for _, path := range []string{"/api/setup-fields", "/api/setup-state?repo=%2Frepo%2Fa"} {
		if rec := do(h, "GET", path, testHost, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d; want 200", path, rec.Code)
		}
	}
}

// setupE2EConfig is the config.toml that the e2e test writes. Its comments
// must stay after a save.
const setupE2EConfig = `# top comment of config.toml
[jira]
# the default project of Jira
defaultProject = "OLD"
`

// e2eSetupHandler builds a handler with the real Setup functions of
// internal/tools and one registered repo, root.
func e2eSetupHandler(t *testing.T, root string) *handler {
	t.Helper()
	o := testOptions(t, &fakeCollector{})
	o.Roots = func(time.Time) ([]dashboard.Root, error) {
		return []dashboard.Root{{Root: root, LastSeen: e2eFixedNow}}, nil
	}
	o.SetupFields, o.SetupState = tools.DashboardSetupFields, tools.DashboardSetupState
	o.SetupSave, o.SetupCreate = tools.DashboardSetupSave, tools.DashboardSetupCreate
	return newHandler(context.Background(), o, testToken, 4242, e2eFixedNow, func() {})
}

// e2eSetupState reads GET /api/setup-state of root.
func e2eSetupState(t *testing.T, h *handler, root string) tools.DashboardSetupStateOut {
	t.Helper()
	rec := do(h, "GET", "/api/setup-state?repo="+url.QueryEscape(root), testHost, nil)
	if rec.Code != 200 {
		t.Fatalf("GET setup-state = %d (body %q)", rec.Code, rec.Body.String())
	}
	var out tools.DashboardSetupStateOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("setup-state body %q: %v", rec.Body.String(), err)
	}
	return out
}

// TestE2E_SetupSaveAndCreate runs the Setup routes with the real functions of
// internal/tools on real files: a save of one field, a refused save, and the
// create of a repo with no config.
func TestE2E_SetupSaveAndCreate(t *testing.T) {
	t.Setenv(config.UserConfigPathEnv, filepath.Join(t.TempDir(), "local.toml"))

	t.Run("save one field and read it back", func(t *testing.T) {
		root := t.TempDir()
		configPath := filepath.Join(root, ".sdlc-v2", "config.toml")
		e2eWriteFile(t, configPath, setupE2EConfig)
		h := e2eSetupHandler(t, root)

		body, _ := json.Marshal(map[string]any{
			"repo":    root,
			"changes": []map[string]any{{"section": "jira", "field": "defaultProject", "op": "set", "value": "NEW"}},
		})
		rec := postJSON(h, "/api/setup-save", string(body), mutationHeader())
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
			t.Fatalf("save = %d (body %q); want 200 with ok true", rec.Code, rec.Body.String())
		}

		state := e2eSetupState(t, h, root)
		if state.State != tools.SetupStateReady {
			t.Errorf("state = %q; want ready", state.State)
		}
		if got := state.Values["jira"]["defaultProject"]; got.Value != "NEW" || got.Source != "config" {
			t.Errorf("jira.defaultProject = %+v; want NEW from config", got)
		}
		onDisk, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, comment := range []string{"# top comment of config.toml", "# the default project of Jira"} {
			if !strings.Contains(string(onDisk), comment) {
				t.Errorf("config.toml lost the comment %q:\n%s", comment, onDisk)
			}
		}
	})

	t.Run("refused value changes no file", func(t *testing.T) {
		root := t.TempDir()
		configPath := filepath.Join(root, ".sdlc-v2", "config.toml")
		e2eWriteFile(t, configPath, setupE2EConfig)
		localPath := filepath.Join(root, ".sdlc-v2", "local.toml")
		h := e2eSetupHandler(t, root)

		body := `{"repo":` + e2eMarshal(t, root) + `,"changes":[{"section":"ship","field":"bump","op":"set","value":"huge"}],"targets":{"ship":"project"}}`
		rec := postJSON(h, "/api/setup-save", body, mutationHeader())
		wantAPIError(t, rec, 400, codeSetupInvalid)
		var got setupErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Error.Fields) != 1 || got.Error.Fields[0].Path != "ship.bump" {
			t.Errorf("fields = %+v; want one entry for ship.bump", got.Error.Fields)
		}
		onDisk, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read config.toml: %v", err)
		}
		if string(onDisk) != setupE2EConfig {
			t.Errorf("config.toml changed:\n%s", onDisk)
		}
		if _, err := os.Stat(localPath); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("local.toml exists after a refused save: %v", err)
		}
	})

	t.Run("create the config of a repo with no config", func(t *testing.T) {
		root := t.TempDir()
		h := e2eSetupHandler(t, root)
		if state := e2eSetupState(t, h, root); state.State != tools.SetupStateNoConfig {
			t.Fatalf("state before = %q; want no-config", state.State)
		}

		rec := postJSON(h, "/api/setup-create", `{"repo":`+e2eMarshal(t, root)+`}`, mutationHeader())
		if rec.Code != 200 {
			t.Fatalf("create = %d (body %q); want 200", rec.Code, rec.Body.String())
		}
		var out tools.DashboardSetupCreateOut
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.OK || len(out.Errors) != 0 {
			t.Fatalf("create body = %q (%v); want ok with no errors", rec.Body.String(), err)
		}
		if state := e2eSetupState(t, h, root); state.State != tools.SetupStateReady {
			t.Errorf("state after = %q; want ready", state.State)
		}
		if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "config.toml")); err != nil {
			t.Errorf("config.toml: %v", err)
		}
	})
}
