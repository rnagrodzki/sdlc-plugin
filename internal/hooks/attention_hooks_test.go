package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/attention"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// withAttentionSeams points the git seam of gitseam.go at root and branch for
// the test's duration, so the handlers resolve both without a real git repo.
// branchErr, when non-nil, makes the branch lookup fail. branchCalls counts
// the branch lookups.
func withAttentionSeams(t *testing.T, root, branch string, branchErr error) *int {
	t.Helper()
	oldMain, oldActive, oldBranch := mainRootFunc, activeRootFunc, currentBranchFunc
	calls := 0
	mainRootFunc = func() (string, error) { return root, nil }
	activeRootFunc = func() (string, error) { return root, nil }
	currentBranchFunc = func(string) (string, error) {
		calls++
		return branch, branchErr
	}
	t.Cleanup(func() { mainRootFunc, activeRootFunc, currentBranchFunc = oldMain, oldActive, oldBranch })
	return &calls
}

// notificationEvent returns a Notification event with the given
// notification_type and message. An empty notificationType leaves the key out.
func notificationEvent(notificationType, message string) Event {
	raw := map[string]any{
		"hook_event_name": "Notification",
		"message":         message,
	}
	if notificationType != "" {
		raw["notification_type"] = notificationType
	}
	return Event{Raw: raw}
}

// dataRoot returns a temp directory that holds a data directory (.sdlc-v2/).
func dataRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))
	return root
}

// TestRecordPermissionWait covers every branch of recordPermissionWait.
func TestRecordPermissionWait(t *testing.T) {
	t.Run("permission_prompt: writes the permission record with the message", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)

		out, err := recordPermissionWait(HookCtx{SessionID: "s1"},
			notificationEvent("permission_prompt", "Claude needs your permission to use Bash"))
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		if files := attentionFiles(t, root); len(files) != 1 || files[0] != "s1-permission.json" {
			t.Fatalf("attention files = %v, want [s1-permission.json]", files)
		}
		recs, err := attention.List(root, time.Now(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) != 1 {
			t.Fatalf("got %d listed records, want 1: %+v", len(recs), recs)
		}
		r := recs[0]
		if r.Kind != attention.KindPermission {
			t.Errorf("Kind = %q, want %q", r.Kind, attention.KindPermission)
		}
		if r.Header != "Permission" {
			t.Errorf("Header = %q, want Permission", r.Header)
		}
		if r.Text != "Claude needs your permission to use Bash" {
			t.Errorf("Text = %q, want the notification message", r.Text)
		}
		if r.SessionID != "s1" || r.Branch != "feat/pw" {
			t.Errorf("SessionID, Branch = %q, %q, want s1, feat/pw", r.SessionID, r.Branch)
		}
		if r.ToolUseID != "" {
			t.Errorf("ToolUseID = %q, want empty for a permission record", r.ToolUseID)
		}
		if r.AskedAt == "" {
			t.Error("AskedAt is empty, want a timestamp")
		}
	})

	t.Run("message with an email: the stored text is redacted", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)

		if _, err := recordPermissionWait(HookCtx{SessionID: "s1"},
			notificationEvent("permission_prompt", "Allow Bash for dev@example.com")); err != nil {
			t.Fatal(err)
		}
		recs, err := attention.List(root, time.Now(), 0)
		if err != nil || len(recs) != 1 {
			t.Fatalf("List = %+v, %v, want 1 record", recs, err)
		}
		if strings.Contains(recs[0].Text, "dev@example.com") {
			t.Errorf("Text = %q, want the email redacted", recs[0].Text)
		}
		if !strings.Contains(recs[0].Text, "[email:REDACTED]") {
			t.Errorf("Text = %q, want the [email:REDACTED] marker", recs[0].Text)
		}
	})

	t.Run("second prompt of the session replaces the first record", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)

		for _, msg := range []string{"first prompt", "second prompt"} {
			if _, err := recordPermissionWait(HookCtx{SessionID: "s1"}, notificationEvent("permission_prompt", msg)); err != nil {
				t.Fatal(err)
			}
		}
		recs, err := attention.List(root, time.Now(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) != 1 || recs[0].Text != "second prompt" {
			t.Errorf("records = %+v, want one record with text %q", recs, "second prompt")
		}
	})

	t.Run("other notification types and a missing type write nothing", func(t *testing.T) {
		for _, nt := range []string{"idle_prompt", "auth_success", "elicitation_dialog", ""} {
			root := dataRoot(t)
			withAttentionSeams(t, root, "feat/pw", nil)

			out, err := recordPermissionWait(HookCtx{SessionID: "s1"}, notificationEvent(nt, "Claude is waiting"))
			if err != nil {
				t.Fatalf("type %q: %v", nt, err)
			}
			assertSilent(t, out)
			if files := attentionFiles(t, root); len(files) != 0 {
				t.Errorf("type %q: attention files = %v, want none", nt, files)
			}
		}
	})

	t.Run("nil envelope writes nothing", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)

		out, err := recordPermissionWait(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		if files := attentionFiles(t, root); len(files) != 0 {
			t.Errorf("attention files = %v, want none", files)
		}
	})

	t.Run("empty session ID writes nothing", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)

		out, err := recordPermissionWait(HookCtx{}, notificationEvent("permission_prompt", "needs permission"))
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		if files := attentionFiles(t, root); len(files) != 0 {
			t.Errorf("attention files = %v, want none", files)
		}
	})

	t.Run("branch does not resolve: writes nothing", func(t *testing.T) {
		root := dataRoot(t)
		// chdir puts a data directory at the relative path too, so only the
		// unresolved-branch arm can stop the write.
		chdir(t, root)
		withAttentionSeams(t, root, "", errors.New("detached"))

		out, err := recordPermissionWait(HookCtx{SessionID: "s1"}, notificationEvent("permission_prompt", "needs permission"))
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		if files := attentionFiles(t, root); len(files) != 0 {
			t.Errorf("attention files = %v, want none", files)
		}
	})

	t.Run("root does not resolve: writes nothing", func(t *testing.T) {
		root := dataRoot(t)
		// chdir puts a data directory at the relative path too, so only the
		// unresolved-root arm can stop the write.
		chdir(t, root)
		withAttentionSeams(t, root, "feat/pw", nil)
		mainRootFunc = func() (string, error) { return "", errors.New("not a git repository") }

		out, err := recordPermissionWait(HookCtx{SessionID: "s1"}, notificationEvent("permission_prompt", "needs permission"))
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		if files := attentionFiles(t, root); len(files) != 0 {
			t.Errorf("attention files = %v, want none", files)
		}
	})

	t.Run("repo without a data directory: writes nothing and creates nothing", func(t *testing.T) {
		root := t.TempDir()
		withAttentionSeams(t, root, "feat/pw", nil)

		out, err := recordPermissionWait(HookCtx{SessionID: "s1"}, notificationEvent("permission_prompt", "needs permission"))
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		if _, err := os.Stat(filepath.Join(root, paths.DataDir)); !os.IsNotExist(err) {
			t.Errorf("data dir stat err = %v, want not-exist", err)
		}
	})

	t.Run("write failure is returned and the output stays silent", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)
		breakAttentionDir(t, root)

		out, err := recordPermissionWait(HookCtx{SessionID: "s1"}, notificationEvent("permission_prompt", "needs permission"))
		if err == nil {
			t.Fatal("err = nil, want the attention write error")
		}
		assertSilent(t, out)
	})
}

// TestClosePermissionWait covers every branch of closePermissionWait.
func TestClosePermissionWait(t *testing.T) {
	t.Run("deletes the permission record of the session and keeps the others", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)
		seedAttention(t, root,
			attention.Record{Kind: attention.KindPermission, SessionID: "s1", Branch: "b"},
			attention.Record{Kind: attention.KindQuestion, SessionID: "s1", ToolUseID: "tu1", Branch: "b"},
			attention.Record{Kind: attention.KindPermission, SessionID: "s2", Branch: "b"},
		)

		out, err := closePermissionWait(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		got := attentionFiles(t, root)
		want := []string{"s1-tu1.json", "s2-permission.json"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("attention files = %v, want %v", got, want)
		}
	})

	t.Run("no record and no attention folder: no write, no folder, one root lookup, no branch lookup", func(t *testing.T) {
		root := dataRoot(t)
		branchCalls := withAttentionSeams(t, root, "feat/pw", nil)
		rootCalls := 0
		inner := mainRootFunc
		mainRootFunc = func() (string, error) {
			rootCalls++
			return inner()
		}

		out, err := closePermissionWait(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		if _, err := os.Stat(attention.Dir(root)); !os.IsNotExist(err) {
			t.Errorf("attention dir stat err = %v, want not-exist (the hook must not create it)", err)
		}
		if rootCalls != 1 {
			t.Errorf("root lookups = %d, want 1", rootCalls)
		}
		if *branchCalls != 0 {
			t.Errorf("branch lookups = %d, want 0", *branchCalls)
		}
	})

	t.Run("attention folder without a record: folder content unchanged", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)
		seedAttention(t, root, attention.Record{Kind: attention.KindPermission, SessionID: "s2", Branch: "b"})

		if _, err := closePermissionWait(HookCtx{SessionID: "s1"}, Event{}); err != nil {
			t.Fatal(err)
		}
		if got := attentionFiles(t, root); len(got) != 1 || got[0] != "s2-permission.json" {
			t.Errorf("attention files = %v, want [s2-permission.json]", got)
		}
	})

	t.Run("empty session ID deletes nothing", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)
		seedAttention(t, root, attention.Record{Kind: attention.KindPermission, SessionID: "s1", Branch: "b"})

		out, err := closePermissionWait(HookCtx{}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		if got := attentionFiles(t, root); len(got) != 1 {
			t.Errorf("attention files = %v, want the record kept", got)
		}
	})

	t.Run("root does not resolve: silent and no error", func(t *testing.T) {
		old := mainRootFunc
		mainRootFunc = func() (string, error) { return "", errors.New("not a git repo") }
		t.Cleanup(func() { mainRootFunc = old })

		out, err := closePermissionWait(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		assertSilent(t, out)
	})

	t.Run("delete failure is returned and the output stays silent", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)
		breakAttentionDir(t, root)

		out, err := closePermissionWait(HookCtx{SessionID: "s1"}, Event{})
		if err == nil {
			t.Fatal("err = nil, want the attention delete error")
		}
		assertSilent(t, out)
	})
}

// TestCloseSessionWaits covers every branch of closeSessionWaits.
func TestCloseSessionWaits(t *testing.T) {
	t.Run("deletes every record of the session and keeps other sessions", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)
		seedAttention(t, root,
			attention.Record{Kind: attention.KindPermission, SessionID: "s1", Branch: "b"},
			attention.Record{Kind: attention.KindQuestion, SessionID: "s1", ToolUseID: "tu1", Branch: "b"},
			attention.Record{Kind: attention.KindQuestion, SessionID: "s1", ToolUseID: "tu2", Branch: "b"},
			attention.Record{Kind: attention.KindPermission, SessionID: "s2", Branch: "b"},
			attention.Record{Kind: attention.KindQuestion, SessionID: "s2", ToolUseID: "tu1", Branch: "b"},
		)

		out, err := closeSessionWaits(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)

		got := attentionFiles(t, root)
		want := []string{"s2-permission.json", "s2-tu1.json"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("attention files = %v, want %v", got, want)
		}
	})

	t.Run("no attention folder: no error and no folder created", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)

		out, err := closeSessionWaits(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
		if _, err := os.Stat(attention.Dir(root)); !os.IsNotExist(err) {
			t.Errorf("attention dir stat err = %v, want not-exist", err)
		}
	})

	t.Run("empty session ID deletes nothing", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)
		seedAttention(t, root, attention.Record{Kind: attention.KindPermission, SessionID: "s1", Branch: "b"})

		if _, err := closeSessionWaits(HookCtx{}, Event{}); err != nil {
			t.Fatal(err)
		}
		if got := attentionFiles(t, root); len(got) != 1 {
			t.Errorf("attention files = %v, want the record kept", got)
		}
	})

	t.Run("root does not resolve: silent and no error", func(t *testing.T) {
		old := mainRootFunc
		mainRootFunc = func() (string, error) { return "", errors.New("not a git repo") }
		t.Cleanup(func() { mainRootFunc = old })

		out, err := closeSessionWaits(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		assertSilent(t, out)
	})

	t.Run("delete failure is returned and the output stays silent", func(t *testing.T) {
		root := dataRoot(t)
		withAttentionSeams(t, root, "feat/pw", nil)
		breakAttentionDir(t, root)

		out, err := closeSessionWaits(HookCtx{SessionID: "s1"}, Event{})
		if err == nil {
			t.Fatal("err = nil, want the attention read error")
		}
		assertSilent(t, out)
	})
}

// TestRun_PermissionWaitLifecycle runs the three hooks through Run with stdin
// envelopes in a real git repo: a prompt writes the record, a finished tool
// call removes it, a second prompt writes it again, and the end of the session
// removes every record of the session.
func TestRun_PermissionWaitLifecycle(t *testing.T) {
	root := gitFixture(t, "feat/pw-lifecycle")
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))

	run := func(name, stdin string) {
		t.Helper()
		var out bytes.Buffer
		if code := Run(name, strings.NewReader(stdin), &out); code != 0 {
			t.Fatalf("Run(%s) exit code = %d, want 0", name, code)
		}
		if out.Len() != 0 {
			t.Fatalf("Run(%s) wrote stdout %q, want none", name, out.String())
		}
	}
	notification := `{"session_id":"s1","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`

	run("record-permission-wait", notification)
	recs, err := attention.List(root, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("after the prompt: %d records, want 1: %+v", len(recs), recs)
	}
	r := recs[0]
	if r.Kind != "permission" || r.Header != "Permission" || r.Text != "Claude needs your permission to use Bash" ||
		r.SessionID != "s1" || r.Branch != "feat/pw-lifecycle" {
		t.Errorf("record = %+v, want the permission record of s1 on feat/pw-lifecycle", r)
	}

	run("close-permission-wait", `{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Bash"}`)
	if files := attentionFiles(t, root); len(files) != 0 {
		t.Errorf("after PostToolUse: attention files = %v, want none", files)
	}

	run("record-permission-wait", notification)
	seedAttention(t, root, attention.Record{Kind: attention.KindQuestion, SessionID: "s1", ToolUseID: "tu1", Branch: "b"})
	run("close-permission-wait", `{"session_id":"s1","hook_event_name":"PostToolUseFailure","tool_name":"Bash"}`)
	if files := attentionFiles(t, root); len(files) != 1 || files[0] != "s1-tu1.json" {
		t.Errorf("after PostToolUseFailure: attention files = %v, want only the question record", files)
	}

	run("record-permission-wait", notification)
	run("close-session-waits", `{"session_id":"s1","hook_event_name":"SessionEnd"}`)
	if files := attentionFiles(t, root); len(files) != 0 {
		t.Errorf("after SessionEnd: attention files = %v, want none", files)
	}
}

// TestRun_WaitHooksFailOpenOnStoreError runs the three hooks through Run while
// the attention folder is a regular file, so every write and delete fails. Run
// must still exit 0 with an empty stdout for each hook.
func TestRun_WaitHooksFailOpenOnStoreError(t *testing.T) {
	root := gitFixture(t, "feat/pw-err")
	mustMkdirAll(t, filepath.Join(root, paths.DataDir))
	breakAttentionDir(t, root)

	cases := []struct{ name, stdin string }{
		{"record-permission-wait", `{"session_id":"s1","hook_event_name":"Notification","notification_type":"permission_prompt","message":"needs permission"}`},
		{"close-permission-wait", `{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Bash"}`},
		{"close-session-waits", `{"session_id":"s1","hook_event_name":"SessionEnd"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := Run(tc.name, strings.NewReader(tc.stdin), &out); code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want empty", out.String())
			}
		})
	}
}

// hooksJSONEntry is one matcher entry of an event in hooks.json.
type hooksJSONEntry struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type    string   `json:"type"`
		Async   bool     `json:"async"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Timeout int      `json:"timeout"`
	} `json:"hooks"`
}

// loadHooksJSON parses plugins/sdlc/hooks/hooks.json into a map from event
// name to its matcher entries.
func loadHooksJSON(t *testing.T) map[string][]hooksJSONEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "plugins", "sdlc", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]hooksJSONEntry `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}
	return doc.Hooks
}

// TestHooksJSON_WiresRegistryHooks checks hooks.json against the registry and
// against the wiring of the wait hooks: every entry names a registry hook, the
// Notification entry matches permission_prompt, and the catch-all PostToolUse
// entry runs close-permission-wait asynchronously.
func TestHooksJSON_WiresRegistryHooks(t *testing.T) {
	events := loadHooksJSON(t)

	for event, entries := range events {
		for _, e := range entries {
			for _, h := range e.Hooks {
				if h.Type != "command" || h.Command != "${CLAUDE_PLUGIN_ROOT}/bin/sdlc-launcher.sh" {
					t.Errorf("%s: type %q command %q, want the sdlc-launcher.sh command form", event, h.Type, h.Command)
				}
				if len(h.Args) != 2 || h.Args[0] != "hook" {
					t.Errorf("%s: args = %v, want [hook <name>]", event, h.Args)
					continue
				}
				if _, ok := registry[h.Args[1]]; !ok {
					t.Errorf("%s: args[1] %q is not a registry key: %v", event, h.Args[1], registryKeys())
				}
			}
		}
	}

	// single returns the only hook of the only entry of event.
	single := func(event string) hooksJSONEntry {
		t.Helper()
		entries := events[event]
		if len(entries) != 1 || len(entries[0].Hooks) != 1 {
			t.Fatalf("%s: got %d entries, want exactly 1 entry with 1 hook", event, len(entries))
		}
		return entries[0]
	}

	t.Run("Notification: permission_prompt matcher, record-permission-wait", func(t *testing.T) {
		e := single("Notification")
		h := e.Hooks[0]
		if e.Matcher != "permission_prompt" {
			t.Errorf("matcher = %q, want permission_prompt", e.Matcher)
		}
		if h.Args[1] != "record-permission-wait" || h.Timeout != 10 {
			t.Errorf("hook = %v timeout %d, want record-permission-wait timeout 10", h.Args, h.Timeout)
		}
	})

	t.Run("PostToolUseFailure: async close-permission-wait", func(t *testing.T) {
		e := single("PostToolUseFailure")
		h := e.Hooks[0]
		if e.Matcher != "" {
			t.Errorf("matcher = %q, want none", e.Matcher)
		}
		if h.Args[1] != "close-permission-wait" || !h.Async || h.Timeout != 10 {
			t.Errorf("hook = %v async %v timeout %d, want async close-permission-wait timeout 10", h.Args, h.Async, h.Timeout)
		}
	})

	t.Run("SessionEnd: close-session-waits", func(t *testing.T) {
		e := single("SessionEnd")
		h := e.Hooks[0]
		if h.Args[1] != "close-session-waits" || h.Timeout != 10 {
			t.Errorf("hook = %v timeout %d, want close-session-waits timeout 10", h.Args, h.Timeout)
		}
	})

	t.Run("PostToolUse: exactly one catch-all entry, async close-permission-wait", func(t *testing.T) {
		var catchAll []hooksJSONEntry
		for _, e := range events["PostToolUse"] {
			if e.Matcher == "" {
				catchAll = append(catchAll, e)
			}
		}
		if len(catchAll) != 1 || len(catchAll[0].Hooks) != 1 {
			t.Fatalf("got %d catch-all PostToolUse entries, want exactly 1 with 1 hook: %+v", len(catchAll), catchAll)
		}
		h := catchAll[0].Hooks[0]
		if h.Args[1] != "close-permission-wait" || !h.Async || h.Timeout != 10 {
			t.Errorf("hook = %v async %v timeout %d, want async close-permission-wait timeout 10", h.Args, h.Async, h.Timeout)
		}
	})
}
