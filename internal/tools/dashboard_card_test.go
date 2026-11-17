package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/t3"
)

// cardShipID is the run id of the ship run that most card tests read.
const cardShipID = "ship-feat-x-20261116T175538Z"

// cardBadRunIDSuggestion is the Suggestion of the bad runId error.
const cardBadRunIDSuggestion = "Pass the state file name of a ship or execute run without .json, for example ship-feat-x-20261116T175538Z."

// cardNotFoundSuggestion is the Suggestion of the run-not-found DomainError.
const cardNotFoundSuggestion = "Use the stateFile of ship_prepare or the filePath of execute_state init, without .json."

// cardRepo creates a git repo in a temp folder, makes it the current folder,
// and returns its path with symlinks resolved.
func cardRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	runGit(t, dir, "init", "-q", "-b", "main")
	t.Chdir(dir)
	return dir
}

// cardRunsDir returns the runs folder of root.
func cardRunsDir(root string) string {
	return filepath.Join(root, ".sdlc-v2", "runs")
}

// cardMkRuns creates the runs folder of root.
func cardMkRuns(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(cardRunsDir(root), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
}

// writeCardRun writes data as the run file <root>/.sdlc-v2/runs/<runID>.json.
func writeCardRun(t *testing.T, root, runID string, data map[string]any) {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cardMkRuns(t, root)
	if err := os.WriteFile(filepath.Join(cardRunsDir(root), runID+".json"), b, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// writeCardBadRun writes a run file named name that is not valid JSON.
func writeCardBadRun(t *testing.T, root, name string) {
	t.Helper()
	cardMkRuns(t, root)
	if err := os.WriteFile(filepath.Join(cardRunsDir(root), name), []byte("{"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// cardShipState returns a running ship state: one completed step and one
// step in progress, with no thread id, no issue and no end time.
func cardShipState() map[string]any {
	return map[string]any{
		"branch":    "feat/x",
		"startedAt": "2026-11-16T17:55:38Z",
		"steps": []any{
			map[string]any{"name": "execute", "status": "completed"},
			map[string]any{"name": "commit", "status": "in_progress"},
		},
	}
}

// connectDashboardCard returns a client session of a server that has only
// the dashboard_card tool. The client announces no extension.
func connectDashboardCard(t *testing.T) *mcp.ClientSession {
	t.Helper()
	return connectDashboardCardWith(t, nil)
}

// connectDashboardCardWith is connectDashboardCard with client options opts,
// for example the capabilities that the client announces in initialize.
func connectDashboardCardWith(t *testing.T, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	// A test of the registered tool never reads a real server record.
	dashboardCardStubServer(t, "", false)
	s := mcpserver.New("test", "0.0.0-test")
	RegisterDashboardCardTools(s)
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, opts)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// mcpAppsClient returns client options that announce the MCP Apps extension
// as the spec 2026-01-26 shows it.
func mcpAppsClient() *mcp.ClientOptions {
	caps := &mcp.ClientCapabilities{}
	caps.AddExtension(mcpserver.MCPAppsExtension, map[string]any{"mimeTypes": []any{mcpserver.MCPAppMIMEType}})
	return &mcp.ClientOptions{Capabilities: caps}
}

// callRegisteredDashboardCard calls the registered dashboard_card tool over an
// in-memory MCP session and returns the result and its text. The client
// announces no extension.
func callRegisteredDashboardCard(t *testing.T, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	return callDashboardCardOn(t, connectDashboardCard(t), args)
}

// callDashboardCardOn calls dashboard_card on the client session c and
// returns the result and its text.
func callDashboardCardOn(t *testing.T, c *mcp.ClientSession, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := c.CallTool(context.Background(), &mcp.CallToolParams{Name: "dashboard_card", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return res, text.Text
}

// cardErrText is the rendered text of a failed dashboard_card call.
func cardErrText(code, msg, suggestion string) string {
	return "# dashboard_card — error (" + code + ")\n\n## What happened\n" + msg + "\n\n## Do this\n" + suggestion + "\n"
}

// cardBadRunIDMsg is the Msg of the bad runId error for runID and n.
func cardBadRunIDMsg(runID string, n int) string {
	return fmt.Sprintf("runId %s is not a ship or execute run name: ship- or execute-, then 1 to %d characters of A-Z a-z 0-9 . _ - (got %d characters after the prefix)", runID, t3.MaxRunSlugLen, n)
}

// cardShipText is the text block of the cardShipState run for a client with
// no card, with dashboardLine as its last line.
func cardShipText(dashboardLine string) string {
	return "SHIP  [running]\n" +
		cardShipID + "\n" +
		"Branch:    feat/x\n" +
		"Started:   2026-11-16T17:55:38Z\n" +
		"Completed: not recorded\n" +
		"\n" +
		"STEPS                                   1 of 2 done\n" +
		"[#>]\n" +
		"  [x] execute            completed\n" +
		"  [>] commit             in_progress\n" +
		"\n" +
		"ISSUES   none\n" +
		dashboardLine
}

// cardRunningText is the text of the cardShipState run with the Next text next,
// for a client that shows the card: it has no text field.
func cardRunningText(next string) string {
	return cardRunningTextWith(next, "", "")
}

// cardNoCardRunningText is the text of the cardShipState run for a client with
// no card and no dashboard server: the text block and the no-server next.
func cardNoCardRunningText() string {
	return cardRunningTextWith(cardNoViewNext(cardShipID), cardShipText("Dashboard: not running"), "")
}

// cardRunningTextWith is the text of the cardShipState run with the Next text
// next. A non-empty block adds the text field, and a non-empty url the
// dashboardUrl field.
func cardRunningTextWith(next, block, url string) string {
	extra := ""
	if block != "" {
		extra += "- text:\n```\n" + block + "\n```\n"
	}
	if url != "" {
		extra += "- dashboardUrl: " + url + "\n"
	}
	return "# dashboard_card — ok\n\n" +
		"**Next:** " + next + "\n\n" +
		"## Summary\nship run " + cardShipID + " on branch feat/x is running.\n\n" +
		"## Fields\n" +
		"- runId: " + cardShipID + "\n" +
		"- kind: ship\n" +
		"- branch: feat/x\n" +
		"- status: running\n" +
		"- final: false\n" +
		"- startedAt: 2026-11-16T17:55:38Z\n" +
		"- completedAt: (none)\n" + extra + "\n" +
		"## steps[0]\n- name: execute\n- status: completed\n\n" +
		"## steps[1]\n- name: commit\n- status: in_progress\n\n" +
		"## issues\n(none)\n"
}

// cardViewNext is the Next text of a client that shows the card view.
func cardViewNext(runID, status string) string {
	return fmt.Sprintf("Card shows run %s (%s). Continue the current step.", runID, status)
}

// cardNoViewNextServer is the Next text of a client that shows no card view,
// when the dashboard server runs.
const cardNoViewNextServer = "This client shows no card. Print the text field in the chat unchanged, in a code block. Call dashboard_card again at the next card point of this run."

// cardNoViewNext is the Next text of a client that shows no card view, when no
// dashboard server runs.
func cardNoViewNext(runID string) string {
	return "This client shows no card. Print the text field in the chat unchanged, in a code block. If no dashboard ensure ran in this run, call dashboard({action:\"ensure\", runId:\"" + runID + "\"}) and print its link with #" + runID + ". If ensure fails, print one warning line and continue with no link. Call dashboard_card again at the next card point of this run."
}

// dashboardCardStubServer sets the dashboardCardServerURL seam for the test:
// the server runs at url when running is true, else no server runs. Every test
// of the card for a client with no card calls it, so no test reads a real
// server record.
func dashboardCardStubServer(t *testing.T, url string, running bool) {
	t.Helper()
	old := dashboardCardServerURL
	dashboardCardServerURL = func() (string, bool) { return url, running }
	t.Cleanup(func() { dashboardCardServerURL = old })
}

// ---------------------------------------------------------------------------
// Error table: one row for each row of the contract error table
// ---------------------------------------------------------------------------

// TestDashboardCard_ErrorTable calls the registered tool once for each row of
// the error table and asserts the whole rendered text and isError.
func TestDashboardCard_ErrorTable(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, root string)
		thread  string
		runID   string
		isError bool
		want    func(root string) string
	}{
		{
			name:    "bad runId: plan- prefix",
			runID:   "plan-feat-x-20261116T175538Z",
			isError: true,
			want: func(string) string {
				return cardErrText("domain", cardBadRunIDMsg("plan-feat-x-20261116T175538Z", 23), cardBadRunIDSuggestion)
			},
		},
		{
			name:    "bad runId: no known prefix counts the whole value",
			runID:   "commit-x-20261116T175538Z",
			isError: true,
			want: func(string) string {
				return cardErrText("domain", cardBadRunIDMsg("commit-x-20261116T175538Z", 25), cardBadRunIDSuggestion)
			},
		},
		{
			name:    "bad runId: character of the wrong class",
			runID:   "ship-a b",
			isError: true,
			want: func(string) string {
				return cardErrText("domain", cardBadRunIDMsg("ship-a b", 3), cardBadRunIDSuggestion)
			},
		},
		{
			name: "runs folder cannot be listed",
			setup: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Dir(cardRunsDir(root)), 0o755); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
				if err := os.WriteFile(cardRunsDir(root), []byte("x"), 0o644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
			},
			runID:   cardShipID,
			isError: true,
			want: func(root string) string {
				_, listErr := state.List(root)
				return cardErrText("infra", "cannot list runs: "+listErr.Error(),
					"Check that "+cardRunsDir(root)+" is a readable folder, then call again.")
			},
		},
		{
			name:    "run not found, no file skipped",
			setup:   func(t *testing.T, root string) { writeCardRun(t, root, "ship-other-20261116T175538Z", cardShipState()) },
			runID:   cardShipID,
			isError: true,
			want: func(string) string {
				return cardErrText("domain", "run "+cardShipID+" not found", cardNotFoundSuggestion)
			},
		},
		{
			name:    "run not found, files skipped",
			setup:   func(t *testing.T, root string) { writeCardBadRun(t, root, "ship-bad-20261116T175538Z.json") },
			runID:   cardShipID,
			isError: true,
			want: func(root string) string {
				return cardErrText("data", "run "+cardShipID+" not found — 1 run files could not be read",
					"Open the run files in "+cardRunsDir(root)+" and check their JSON. The dashboard skips the same files.")
			},
		},
		{
			name: "run of another T3 thread",
			setup: func(t *testing.T, root string) {
				st := cardShipState()
				st[t3.StateKey] = "thread-a"
				writeCardRun(t, root, cardShipID, st)
			},
			thread:  "thread-b",
			runID:   cardShipID,
			isError: true,
			want: func(string) string {
				return cardErrText("domain", "run "+cardShipID+" belongs to another T3 thread", "Pass the runId of a run of this thread: the stateFile of ship_prepare or the filePath of execute_state init, without .json. Continue the current step.")
			},
		},
		{
			name: "run thread id of the wrong type in a T3 thread",
			setup: func(t *testing.T, root string) {
				st := cardShipState()
				st[t3.StateKey] = 42
				writeCardRun(t, root, cardShipID, st)
			},
			thread:  "thread-b",
			runID:   cardShipID,
			isError: true,
			want: func(root string) string {
				return cardErrText("data", "run "+cardShipID+" has t3ThreadId of type float64, expected string",
					"Fix t3ThreadId in "+filepath.Join(cardRunsDir(root), cardShipID+".json")+": a thread id string, or remove the key. Continue the current step.")
			},
		},
		{
			name:  "plain client, no T3 thread: ok with the no-view next text",
			setup: func(t *testing.T, root string) { writeCardRun(t, root, cardShipID, cardShipState()) },
			runID: cardShipID,
			want:  func(string) string { return cardNoCardRunningText() },
		},
		{
			name:   "plain client in T3: card next text (T3 fallback)",
			setup:  func(t *testing.T, root string) { writeCardRun(t, root, cardShipID, cardShipState()) },
			thread: "thread-b",
			runID:  cardShipID,
			want:   func(string) string { return cardRunningText(cardViewNext(cardShipID, "running")) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := cardRepo(t)
			t.Setenv(t3.EnvThreadID, tt.thread)
			if tt.setup != nil {
				tt.setup(t, root)
			}
			res, text := callRegisteredDashboardCard(t, map[string]any{"runId": tt.runID})
			if res.IsError != tt.isError {
				t.Errorf("isError = %v, want %v", res.IsError, tt.isError)
			}
			if want := tt.want(root); text != want {
				t.Errorf("text =\n%s\nwant\n%s", text, want)
			}
			if tt.isError && res.StructuredContent != nil {
				t.Errorf("structuredContent = %v, want none on an error", res.StructuredContent)
			}
		})
	}
}

// TestDashboardCard_ErrorClassAndCause pins the Go class, Msg, Suggestion and
// cause of each error of the handler.
func TestDashboardCard_ErrorClassAndCause(t *testing.T) {
	t.Run("bad runId is a DomainError with no cause", func(t *testing.T) {
		dashboardCardStubServer(t, "", false)
		_, err := dashboardCard(t.TempDir(), "", false, DashboardCardIn{RunID: "plan-x"})
		var de *mcpserver.DomainError
		if !errors.As(err, &de) {
			t.Fatalf("err = %T %v, want *DomainError", err, err)
		}
		if de.Msg != cardBadRunIDMsg("plan-x", 1) || de.Suggestion != cardBadRunIDSuggestion || de.Cause != nil {
			t.Errorf("got %+v", de)
		}
	})
	t.Run("runs folder is a file is an InfraError that keeps the cause", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(cardRunsDir(root)), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(cardRunsDir(root), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		dashboardCardStubServer(t, "", false)
		_, err := dashboardCard(root, "", false, DashboardCardIn{RunID: cardShipID})
		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("err = %T %v, want *InfraError", err, err)
		}
		if !errors.Is(ie.Cause, syscall.ENOTDIR) {
			t.Errorf("cause = %v, want ENOTDIR", ie.Cause)
		}
		if !strings.HasPrefix(ie.Msg, "cannot list runs: ") || !strings.HasSuffix(ie.Msg, ie.Cause.Error()) {
			t.Errorf("Msg = %q, want the cause after the prefix", ie.Msg)
		}
		if want := "Check that " + cardRunsDir(root) + " is a readable folder, then call again."; ie.Suggestion != want {
			t.Errorf("Suggestion = %q, want %q", ie.Suggestion, want)
		}
	})
	t.Run("missing run is a DomainError with no cause", func(t *testing.T) {
		dashboardCardStubServer(t, "", false)
		_, err := dashboardCard(t.TempDir(), "", false, DashboardCardIn{RunID: cardShipID})
		var de *mcpserver.DomainError
		if !errors.As(err, &de) {
			t.Fatalf("err = %T %v, want *DomainError", err, err)
		}
		if de.Msg != "run "+cardShipID+" not found" || de.Suggestion != cardNotFoundSuggestion || de.Cause != nil {
			t.Errorf("got %+v", de)
		}
	})
	t.Run("missing run with skipped files is a DataError with no cause", func(t *testing.T) {
		root := t.TempDir()
		writeCardBadRun(t, root, "ship-bad-20261116T175538Z.json")
		dashboardCardStubServer(t, "", false)
		_, err := dashboardCard(root, "", false, DashboardCardIn{RunID: cardShipID})
		var de *mcpserver.DataError
		if !errors.As(err, &de) {
			t.Fatalf("err = %T %v, want *DataError", err, err)
		}
		wantSug := "Open the run files in " + cardRunsDir(root) + " and check their JSON. The dashboard skips the same files."
		if de.Msg != "run "+cardShipID+" not found — 1 run files could not be read" || de.Suggestion != wantSug || de.Cause != nil {
			t.Errorf("got %+v", de)
		}
	})
	t.Run("run of another thread is a DomainError with no cause", func(t *testing.T) {
		root := t.TempDir()
		st := cardShipState()
		st[t3.StateKey] = "thread-a"
		writeCardRun(t, root, cardShipID, st)
		_, err := dashboardCard(root, "thread-b", true, DashboardCardIn{RunID: cardShipID})
		var de *mcpserver.DomainError
		if !errors.As(err, &de) {
			t.Fatalf("err = %T %v, want *DomainError", err, err)
		}
		if de.Msg != "run "+cardShipID+" belongs to another T3 thread" || de.Suggestion != "Pass the runId of a run of this thread: the stateFile of ship_prepare or the filePath of execute_state init, without .json. Continue the current step." || de.Cause != nil {
			t.Errorf("got %+v", de)
		}
	})
}

// TestDashboardCard_SkippedCount pins the count of the DataError for 1, 2 and
// 3 unreadable run files.
func TestDashboardCard_SkippedCount(t *testing.T) {
	for _, n := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("%d skipped", n), func(t *testing.T) {
			root := cardRepo(t)
			for i := range n {
				writeCardBadRun(t, root, fmt.Sprintf("ship-bad%d-20261116T175538Z.json", i))
			}
			_, text := callRegisteredDashboardCard(t, map[string]any{"runId": cardShipID})
			want := cardErrText("data", fmt.Sprintf("run %s not found — %d run files could not be read", cardShipID, n),
				"Open the run files in "+cardRunsDir(root)+" and check their JSON. The dashboard skips the same files.")
			if text != want {
				t.Errorf("text =\n%s\nwant\n%s", text, want)
			}
		})
	}
}

// TestDashboardCard_ThreadRules pins the thread check: it runs only when both
// the process and the run have a thread id.
func TestDashboardCard_ThreadRules(t *testing.T) {
	tests := []struct {
		name      string
		process   string
		run       any // stored t3ThreadId; nil leaves the key out
		wantError bool
	}{
		{name: "same thread is ok", process: "thread-a", run: "thread-a"},
		{name: "run with no thread id is ok in T3", process: "thread-a"},
		{name: "run thread id that is not a string is refused in T3", process: "thread-a", run: 42, wantError: true},
		{name: "run thread id that is not a string is ok outside T3", run: 42},
		{name: "run of a thread outside T3 is ok", run: "thread-a"},
		{name: "other thread is refused", process: "thread-b", run: "thread-a", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			st := cardShipState()
			if tt.run != nil {
				st[t3.StateKey] = tt.run
			}
			writeCardRun(t, root, cardShipID, st)
			dashboardCardStubServer(t, "", false)
			out, err := dashboardCard(root, tt.process, tt.process != "", DashboardCardIn{RunID: cardShipID})
			if (err != nil) != tt.wantError {
				t.Fatalf("err = %v, wantError %v", err, tt.wantError)
			}
			if err != nil {
				return
			}
			wantThread, _ := tt.run.(string)
			if out.T3ThreadID != wantThread {
				t.Errorf("T3ThreadID = %q, want %q", out.T3ThreadID, wantThread)
			}
		})
	}
}

// TestDashboardCard_WrongTypeThreadID pins the DataError for a stored
// t3ThreadId that is not a string, in a T3 Code thread.
func TestDashboardCard_WrongTypeThreadID(t *testing.T) {
	root := t.TempDir()
	st := cardShipState()
	st[t3.StateKey] = 42
	writeCardRun(t, root, cardShipID, st)
	_, err := dashboardCard(root, "thread-a", true, DashboardCardIn{RunID: cardShipID})
	var de *mcpserver.DataError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want *mcpserver.DataError", err)
	}
	if want := "run " + cardShipID + " has t3ThreadId of type float64, expected string"; de.Msg != want {
		t.Errorf("Msg = %q, want %q", de.Msg, want)
	}
	if !strings.HasPrefix(de.Suggestion, "Fix t3ThreadId in ") || !strings.HasSuffix(de.Suggestion, ": a thread id string, or remove the key. Continue the current step.") {
		t.Errorf("Suggestion = %q", de.Suggestion)
	}
}

// TestDashboardCard_ClientSupport pins the Next text for each kind of client
// over a real MCP session: the client capabilities of initialize reach the
// handler. A client that announces the MCP Apps extension gets the card text,
// in or out of T3 Code. A client that announces nothing gets the no-view
// text, still with the run data and no error, unless the process runs in a
// T3 Code thread (the T3 fallback).
func TestDashboardCard_ClientSupport(t *testing.T) {
	emptySettings := &mcp.ClientCapabilities{}
	emptySettings.AddExtension(mcpserver.MCPAppsExtension, nil)
	otherExt := &mcp.ClientCapabilities{}
	otherExt.AddExtension("io.example/other", nil)

	tests := []struct {
		name   string
		opts   *mcp.ClientOptions
		thread string
		want   string
		noCard bool // the result has the text block of a client with no card
	}{
		{name: "MCP Apps client outside T3", opts: mcpAppsClient(), want: cardViewNext(cardShipID, "running")},
		{name: "MCP Apps client in T3", opts: mcpAppsClient(), thread: "thread-a", want: cardViewNext(cardShipID, "running")},
		{name: "extension with empty settings", opts: &mcp.ClientOptions{Capabilities: emptySettings}, want: cardViewNext(cardShipID, "running")},
		{name: "plain client outside T3", want: cardNoViewNext(cardShipID), noCard: true},
		{name: "other extension only", opts: &mcp.ClientOptions{Capabilities: otherExt}, want: cardNoViewNext(cardShipID), noCard: true},
		{name: "plain client in T3 (T3 fallback)", thread: "thread-a", want: cardViewNext(cardShipID, "running")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := cardRepo(t)
			t.Setenv(t3.EnvThreadID, tt.thread)
			writeCardRun(t, root, cardShipID, cardShipState())

			res, text := callDashboardCardOn(t, connectDashboardCardWith(t, tt.opts), map[string]any{"runId": cardShipID})
			if res.IsError {
				t.Fatalf("isError = true, want a result:\n%s", text)
			}
			want := cardRunningText(tt.want)
			if tt.noCard {
				want = cardNoCardRunningText()
			}
			if text != want {
				t.Errorf("text =\n%s\nwant\n%s", text, want)
			}
			got := cardStructured(t, res)
			if _, has := got["text"]; has != tt.noCard {
				t.Errorf("structuredContent has text = %v, want %v", has, tt.noCard)
			}
			if got["next"] != tt.want {
				t.Errorf("structuredContent next = %v, want %q", got["next"], tt.want)
			}
			if got["runId"] != cardShipID || got["status"] != "running" || len(got["steps"].([]any)) != 2 {
				t.Errorf("structuredContent = %#v, want the run data", got)
			}
		})
	}
}

// TestDashboardCard_SlugBoundary builds each runId from t3.MaxRunSlugLen: a
// 0-character and a 201-character slug are refused, a 1-character slug passes
// the check, and a 200-character slug reads a real run.
func TestDashboardCard_SlugBoundary(t *testing.T) {
	const ts = "-20261116T175538Z"
	slug200 := strings.Repeat("a", t3.MaxRunSlugLen-len(ts)) + ts
	slug201 := "a" + slug200

	t.Run("0 characters are refused", func(t *testing.T) {
		cardRepo(t)
		t.Setenv(t3.EnvThreadID, "")
		_, text := callRegisteredDashboardCard(t, map[string]any{"runId": "ship-"})
		if want := cardErrText("domain", cardBadRunIDMsg("ship-", 0), cardBadRunIDSuggestion); text != want {
			t.Errorf("text =\n%s\nwant\n%s", text, want)
		}
	})
	t.Run("1 character passes the check", func(t *testing.T) {
		cardRepo(t)
		t.Setenv(t3.EnvThreadID, "")
		_, text := callRegisteredDashboardCard(t, map[string]any{"runId": "ship-a"})
		if want := cardErrText("domain", "run ship-a not found", cardNotFoundSuggestion); text != want {
			t.Errorf("text =\n%s\nwant\n%s", text, want)
		}
	})
	t.Run("200 characters read the run", func(t *testing.T) {
		root := cardRepo(t)
		t.Setenv(t3.EnvThreadID, "")
		runID := "execute-" + slug200
		writeCardRun(t, root, runID, map[string]any{"branch": "b", "startedAt": "2026-11-16T17:55:38Z"})
		res, _ := callRegisteredDashboardCard(t, map[string]any{"runId": runID})
		if res.IsError {
			t.Fatalf("isError = true, want a card for a %d-character slug", len(slug200))
		}
		got, _ := res.StructuredContent.(map[string]any)
		if got["runId"] != runID {
			t.Errorf("runId = %v, want %s", got["runId"], runID)
		}
	})
	t.Run("201 characters are refused", func(t *testing.T) {
		cardRepo(t)
		t.Setenv(t3.EnvThreadID, "")
		runID := "ship-" + slug201
		_, text := callRegisteredDashboardCard(t, map[string]any{"runId": runID})
		if want := cardErrText("domain", cardBadRunIDMsg(runID, t3.MaxRunSlugLen+1), cardBadRunIDSuggestion); text != want {
			t.Errorf("text =\n%s\nwant\n%s", text, want)
		}
	})
}

// ---------------------------------------------------------------------------
// Data table: empty and absent values, and the filled form of each field
// ---------------------------------------------------------------------------

// cardStructured returns the structuredContent of res as JSON-decoded values.
func cardStructured(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structuredContent: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal structuredContent: %v", err)
	}
	return got
}

// TestDashboardCard_EmptyValues covers each row of the empty and absent value
// table: steps [] and (none), issues {} and (none), completedAt null and
// (none), and no t3ThreadId key and no line.
func TestDashboardCard_EmptyValues(t *testing.T) {
	root := cardRepo(t)
	t.Setenv(t3.EnvThreadID, "")
	writeCardRun(t, root, cardShipID, map[string]any{"branch": "feat/x", "startedAt": "2026-11-16T17:55:38Z"})

	res, text := callRegisteredDashboardCard(t, map[string]any{"runId": cardShipID})
	emptyBlock := "SHIP  [running]\n" +
		cardShipID + "\n" +
		"Branch:    feat/x\n" +
		"Started:   2026-11-16T17:55:38Z\n" +
		"Completed: not recorded\n" +
		"\n" +
		"STEPS\n" +
		"No steps yet.\n" +
		"\n" +
		"ISSUES   none\n" +
		"Dashboard: not running"
	wantText := "# dashboard_card — ok\n\n" +
		"**Next:** " + cardNoViewNext(cardShipID) + "\n\n" +
		"## Summary\nship run " + cardShipID + " on branch feat/x is running.\n\n" +
		"## Fields\n" +
		"- runId: " + cardShipID + "\n" +
		"- kind: ship\n" +
		"- branch: feat/x\n" +
		"- status: running\n" +
		"- final: false\n" +
		"- startedAt: 2026-11-16T17:55:38Z\n" +
		"- completedAt: (none)\n" +
		"- text:\n```\n" + emptyBlock + "\n```\n\n" +
		"## steps\n(none)\n\n" +
		"## issues\n(none)\n"
	if text != wantText {
		t.Errorf("text =\n%s\nwant\n%s", text, wantText)
	}
	if strings.Contains(text, "null") || strings.Contains(text, "t3ThreadId") {
		t.Errorf("text has null or a t3ThreadId line:\n%s", text)
	}

	want := map[string]any{
		"summary":     "ship run " + cardShipID + " on branch feat/x is running.",
		"runId":       cardShipID,
		"kind":        "ship",
		"branch":      "feat/x",
		"status":      "running",
		"final":       false,
		"startedAt":   "2026-11-16T17:55:38Z",
		"completedAt": nil,
		"steps":       []any{},
		"issues":      map[string]any{},
		"next":        cardNoViewNext(cardShipID),
		"text":        emptyBlock,
	}
	if got := cardStructured(t, res); !reflect.DeepEqual(got, want) {
		t.Errorf("structuredContent = %#v, want %#v", got, want)
	}
}

// TestDashboardCard_FilledValues covers the filled form of each field: a
// completed ship run with a thread id, steps, issues of two severities, an
// end time, and final true.
func TestDashboardCard_FilledValues(t *testing.T) {
	root := cardRepo(t)
	t.Setenv(t3.EnvThreadID, "thread-a")
	writeCardRun(t, root, cardShipID, map[string]any{
		"branch":              "feat/x",
		"startedAt":           "2026-11-16T17:55:38Z",
		"pipelineStatus":      "completed",
		"pipelineCompletedAt": "2026-11-16T18:10:00Z",
		t3.StateKey:           "thread-a",
		"steps": []any{
			map[string]any{"name": "execute", "status": "completed"},
			map[string]any{"name": "review", "status": "failed", "error": "boom"},
		},
		"issues": []any{
			map[string]any{"severity": "warning", "text": "w1"},
			map[string]any{"severity": "warning", "text": "w2"},
		},
	})

	res, text := callRegisteredDashboardCard(t, map[string]any{"runId": cardShipID, "final": true})
	next := cardViewNext(cardShipID, "completed")
	wantText := "# dashboard_card — ok\n\n" +
		"**Next:** " + next + "\n\n" +
		"## Summary\nship run " + cardShipID + " on branch feat/x is completed.\n\n" +
		"## Fields\n" +
		"- runId: " + cardShipID + "\n" +
		"- kind: ship\n" +
		"- branch: feat/x\n" +
		"- status: completed\n" +
		"- final: true\n" +
		"- t3ThreadId: thread-a\n" +
		"- startedAt: 2026-11-16T17:55:38Z\n" +
		"- completedAt: 2026-11-16T18:10:00Z\n\n" +
		"## steps[0]\n- name: execute\n- status: completed\n\n" +
		"## steps[1]\n- name: review\n- status: failed\n\n" +
		"## issues\n- high: 1\n- medium: 2\n"
	if text != wantText {
		t.Errorf("text =\n%s\nwant\n%s", text, wantText)
	}

	want := map[string]any{
		"summary":     "ship run " + cardShipID + " on branch feat/x is completed.",
		"runId":       cardShipID,
		"kind":        "ship",
		"branch":      "feat/x",
		"status":      "completed",
		"final":       true,
		"t3ThreadId":  "thread-a",
		"startedAt":   "2026-11-16T17:55:38Z",
		"completedAt": "2026-11-16T18:10:00Z",
		"steps": []any{
			map[string]any{"name": "execute", "status": "completed"},
			map[string]any{"name": "review", "status": "failed"},
		},
		"issues": map[string]any{"high": float64(1), "medium": float64(2)},
		"next":   next,
	}
	if got := cardStructured(t, res); !reflect.DeepEqual(got, want) {
		t.Errorf("structuredContent = %#v, want %#v", got, want)
	}
}

// TestDashboardCard_ExecuteRun pins the whole text and the structured content
// of the card of an execute run: one step for each wave, built by
// dashboardStatePipeline.
func TestDashboardCard_ExecuteRun(t *testing.T) {
	root := cardRepo(t)
	t.Setenv(t3.EnvThreadID, "")
	const runID = "execute-feat-y-20261116T175538Z"
	writeCardRun(t, root, runID, map[string]any{
		"branch":         "feat/y",
		"startedAt":      "2026-11-16T17:55:38Z",
		"runStatus":      "completed",
		"runCompletedAt": "2026-11-16T18:00:00Z",
		"waves": []any{
			map[string]any{"number": 1, "status": "completed", "tasks": []any{map[string]any{"id": "1", "status": "completed"}}},
			map[string]any{"number": 2, "status": "partial"},
		},
	})
	res, text := callRegisteredDashboardCard(t, map[string]any{"runId": runID, "final": true})

	summary := "execute run " + runID + " on branch feat/y is completed."
	block := "EXECUTE  [completed]\n" +
		runID + "\n" +
		"Branch:    feat/y\n" +
		"Started:   2026-11-16T17:55:38Z\n" +
		"Completed: 2026-11-16T18:00:00Z\n" +
		"\n" +
		"STEPS                                   1 of 2 done\n" +
		"[#!]\n" +
		"  [x] wave 1             1/1 tasks  completed\n" +
		"  [!] wave 2             0/0 tasks  failed\n" +
		"\n" +
		"ISSUES   high 1\n" +
		"Dashboard: not running"
	wantText := "# dashboard_card — ok\n\n" +
		"**Next:** " + cardNoViewNext(runID) + "\n\n" +
		"## Summary\n" + summary + "\n\n" +
		"## Fields\n" +
		"- runId: " + runID + "\n" +
		"- kind: execute\n" +
		"- branch: feat/y\n" +
		"- status: completed\n" +
		"- final: true\n" +
		"- startedAt: 2026-11-16T17:55:38Z\n" +
		"- completedAt: 2026-11-16T18:00:00Z\n" +
		"- text:\n```\n" + block + "\n```\n\n" +
		"## steps[0]\n- name: wave 1\n- status: completed\n- tasksDone: 1\n- tasksTotal: 1\n\n" +
		"## steps[1]\n- name: wave 2\n- status: failed\n- tasksDone: 0\n- tasksTotal: 0\n\n" +
		"## issues\n- high: 1\n"
	if text != wantText {
		t.Errorf("text =\n%s\nwant\n%s", text, wantText)
	}

	want := map[string]any{
		"summary":     summary,
		"runId":       runID,
		"kind":        "execute",
		"branch":      "feat/y",
		"status":      "completed",
		"final":       true,
		"startedAt":   "2026-11-16T17:55:38Z",
		"completedAt": "2026-11-16T18:00:00Z",
		"steps": []any{
			map[string]any{"name": "wave 1", "status": "completed", "tasksDone": float64(1), "tasksTotal": float64(1)},
			map[string]any{"name": "wave 2", "status": "failed", "tasksDone": float64(0), "tasksTotal": float64(0)},
		},
		"issues": map[string]any{"high": float64(1)},
		"next":   cardNoViewNext(runID),
		"text":   block,
	}
	if got := cardStructured(t, res); !reflect.DeepEqual(got, want) {
		t.Errorf("structuredContent = %#v, want %#v", got, want)
	}
}

// TestDashboardCard_Snapshot pins the snapshot rule: the output echoes the
// snapshot input, and final wins when both are true.
func TestDashboardCard_Snapshot(t *testing.T) {
	root := t.TempDir()
	writeCardRun(t, root, cardShipID, cardShipState())
	cases := []struct {
		final, snapshot         bool
		wantFinal, wantSnapshot bool
	}{
		{false, false, false, false},
		{false, true, false, true},
		{true, false, true, false},
		{true, true, true, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("final=%v snapshot=%v", tc.final, tc.snapshot), func(t *testing.T) {
			dashboardCardStubServer(t, "", false)
			out, err := dashboardCard(root, "", false, DashboardCardIn{RunID: cardShipID, Final: tc.final, Snapshot: tc.snapshot})
			if err != nil {
				t.Fatalf("dashboardCard: %v", err)
			}
			if out.Final != tc.wantFinal || out.Snapshot != tc.wantSnapshot {
				t.Errorf("final, snapshot = %v, %v, want %v, %v", out.Final, out.Snapshot, tc.wantFinal, tc.wantSnapshot)
			}
		})
	}
}

// TestDashboardCard_SnapshotStructured checks that the registered tool puts
// snapshot true in structuredContent for a middle card.
func TestDashboardCard_SnapshotStructured(t *testing.T) {
	root := cardRepo(t)
	t.Setenv(t3.EnvThreadID, "")
	writeCardRun(t, root, cardShipID, cardShipState())
	res, _ := callRegisteredDashboardCard(t, map[string]any{"runId": cardShipID, "snapshot": true})
	if got := cardStructured(t, res)["snapshot"]; got != true {
		t.Errorf("structuredContent snapshot = %v, want true", got)
	}
}

// cardStepCounts returns "name status done/total" for each step of out. A
// step with no counters gives "name status -".
func cardStepCounts(out DashboardCardOut) []string {
	var got []string
	for _, s := range out.Steps {
		if s.TasksDone == nil || s.TasksTotal == nil {
			got = append(got, fmt.Sprintf("%s %s -", s.Name, s.Status))
			continue
		}
		got = append(got, fmt.Sprintf("%s %s %d/%d", s.Name, s.Status, *s.TasksDone, *s.TasksTotal))
	}
	return got
}

// cardExecState returns an execute state on branch that started at
// startedAt: wave 1 completed with 2 completed tasks, wave 2 partial with 5
// tasks (3 completed, 1 failed, 1 skipped-dependency), planned wave 3 with 4
// tasks that did not start, and planned task 9 in no wave (the queued step).
func cardExecState(branch, startedAt string) map[string]any {
	task := func(id, status string) map[string]any { return map[string]any{"id": id, "status": status} }
	return map[string]any{
		"branch":    branch,
		"startedAt": startedAt,
		"waves": []any{
			map[string]any{"number": 1, "status": "completed", "tasks": []any{task("1", "completed"), task("2", "completed")}},
			map[string]any{"number": 2, "status": "partial", "tasks": []any{
				task("3", "completed"), task("4", "completed"), task("5", "completed"),
				task("6", "failed"), task("7", "skipped-dependency"),
			}},
		},
		"plannedWaves": []any{
			map[string]any{"number": 3, "taskIds": []any{"10", "11", "12", "13"}},
		},
		"plannedTasks": []any{
			map[string]any{"id": "9", "name": "queued task"},
		},
	}
}

// TestDashboardCard_WaveCounters covers the counter cases of an execute run:
// a failed or skipped-dependency task is not done, a planned wave that did not
// start has 0 of its planned count, and the queued step has no counters.
func TestDashboardCard_WaveCounters(t *testing.T) {
	root := t.TempDir()
	const runID = "execute-feat-y-20261116T175538Z"
	writeCardRun(t, root, runID, cardExecState("feat/y", "2026-11-16T17:55:38Z"))

	dashboardCardStubServer(t, "", false)
	out, err := dashboardCard(root, "", false, DashboardCardIn{RunID: runID})
	if err != nil {
		t.Fatalf("dashboardCard: %v", err)
	}
	want := []string{
		"wave 1 completed 2/2",
		"wave 2 failed 3/5",
		"wave 3 pending 0/4",
		"queued pending -",
	}
	if got := cardStepCounts(out); !reflect.DeepEqual(got, want) {
		t.Errorf("steps = %v, want %v", got, want)
	}
	for _, s := range out.Steps {
		if s.Waves != nil {
			t.Errorf("step %s has waves %v, want none on an execute run", s.Name, s.Waves)
		}
	}
}

// cardShipWaveRows returns "name status done/total" for each waves row of
// the ship execute step of out, or nil when the step has no waves.
func cardShipWaveRows(t *testing.T, out DashboardCardOut) []string {
	t.Helper()
	for _, s := range out.Steps {
		if s.Name != "execute" {
			continue
		}
		if s.TasksDone != nil || s.TasksTotal != nil {
			t.Errorf("ship execute step has counters %v/%v, want none", s.TasksDone, s.TasksTotal)
		}
		if s.Waves == nil {
			return nil
		}
		got := []string{}
		for _, w := range s.Waves {
			got = append(got, fmt.Sprintf("%s %s %d/%d", w.Name, w.Status, w.TasksDone, w.TasksTotal))
		}
		return got
	}
	t.Fatalf("no execute step in %+v", out.Steps)
	return nil
}

// TestDashboardCard_ShipWaves covers the join of a ship run to its execute
// run: the rule of the dashboard page decides which execute run, if any,
// gives the waves of the ship execute step.
func TestDashboardCard_ShipWaves(t *testing.T) {
	joinedRows := []string{"wave 1 completed 2/2", "wave 2 failed 3/5", "wave 3 pending 0/4"}
	completedShip := func() map[string]any {
		s := cardShipState()
		s["pipelineStatus"] = "completed"
		s["pipelineCompletedAt"] = "2026-11-16T18:30:00Z"
		return s
	}
	type execRun struct {
		id   string
		data map[string]any
	}
	cases := []struct {
		name  string
		ship  map[string]any
		execs []execRun
		want  []string
	}{
		{
			name:  "execute run in the ship window joins",
			ship:  cardShipState(),
			execs: []execRun{{"execute-feat-x-20261116T175600Z", cardExecState("feat/x", "2026-11-16T17:56:00Z")}},
			want:  joinedRows,
		},
		{
			name: "two execute runs in the window: the newer one joins",
			ship: cardShipState(),
			execs: []execRun{
				{"execute-feat-x-20261116T175600Z", map[string]any{"branch": "feat/x", "startedAt": "2026-11-16T17:56:00Z", "waves": []any{
					map[string]any{"number": 1, "status": "failed", "tasks": []any{map[string]any{"id": "1", "status": "failed"}}},
				}}},
				{"execute-feat-x-20261116T180000Z", cardExecState("feat/x", "2026-11-16T18:00:00Z")},
			},
			want: joinedRows,
		},
		{
			name:  "execute run on another branch does not join",
			ship:  cardShipState(),
			execs: []execRun{{"execute-feat-z-20261116T175600Z", cardExecState("feat/z", "2026-11-16T17:56:00Z")}},
			want:  nil,
		},
		{
			name:  "execute run before the ship start does not join",
			ship:  cardShipState(),
			execs: []execRun{{"execute-feat-x-20261116T170000Z", cardExecState("feat/x", "2026-11-16T17:00:00Z")}},
			want:  nil,
		},
		{
			name:  "execute run after the ship end does not join",
			ship:  completedShip(),
			execs: []execRun{{"execute-feat-x-20261116T190000Z", cardExecState("feat/x", "2026-11-16T19:00:00Z")}},
			want:  nil,
		},
		{
			name:  "no execute run",
			ship:  cardShipState(),
			execs: nil,
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeCardRun(t, root, cardShipID, tc.ship)
			for _, e := range tc.execs {
				writeCardRun(t, root, e.id, e.data)
			}
			dashboardCardStubServer(t, "", false)
			out, err := dashboardCard(root, "", false, DashboardCardIn{RunID: cardShipID})
			if err != nil {
				t.Fatalf("dashboardCard: %v", err)
			}
			if got := cardShipWaveRows(t, out); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("execute waves = %v, want %v", got, tc.want)
			}
			// The join adds no issue of the execute run to the ship card.
			if len(out.Issues) != 0 {
				t.Errorf("issues = %v, want none: the ship run has no issue", out.Issues)
			}
		})
	}
}

// TestDashboardCard_ShipWavesStructured pins the structured content and the
// text of a ship card with a joined execute run, and that a ship run with no
// joined execute run gives the same structured content as before the join.
func TestDashboardCard_ShipWavesStructured(t *testing.T) {
	base := func(steps []any, text string) map[string]any {
		return map[string]any{
			"summary":     "ship run " + cardShipID + " on branch feat/x is running.",
			"runId":       cardShipID,
			"kind":        "ship",
			"branch":      "feat/x",
			"status":      "running",
			"final":       false,
			"startedAt":   "2026-11-16T17:55:38Z",
			"completedAt": nil,
			"steps":       steps,
			"issues":      map[string]any{},
			"next":        cardNoViewNext(cardShipID),
			"text":        text,
		}
	}

	t.Run("no joined execute run", func(t *testing.T) {
		root := cardRepo(t)
		t.Setenv(t3.EnvThreadID, "")
		writeCardRun(t, root, cardShipID, cardShipState())
		writeCardRun(t, root, "execute-feat-z-20261116T175600Z", cardExecState("feat/z", "2026-11-16T17:56:00Z"))
		res, _ := callRegisteredDashboardCard(t, map[string]any{"runId": cardShipID})
		want := base([]any{
			map[string]any{"name": "execute", "status": "completed"},
			map[string]any{"name": "commit", "status": "in_progress"},
		}, cardShipText("Dashboard: not running"))
		if got := cardStructured(t, res); !reflect.DeepEqual(got, want) {
			t.Errorf("structuredContent = %#v, want %#v", got, want)
		}
	})

	t.Run("joined execute run", func(t *testing.T) {
		root := cardRepo(t)
		t.Setenv(t3.EnvThreadID, "")
		writeCardRun(t, root, cardShipID, cardShipState())
		writeCardRun(t, root, "execute-feat-x-20261116T175600Z", cardExecState("feat/x", "2026-11-16T17:56:00Z"))
		res, text := callRegisteredDashboardCard(t, map[string]any{"runId": cardShipID})
		joinedText := "SHIP  [running]\n" +
			cardShipID + "\n" +
			"Branch:    feat/x\n" +
			"Started:   2026-11-16T17:55:38Z\n" +
			"Completed: not recorded\n" +
			"\n" +
			"STEPS                                   1 of 2 done\n" +
			"[#>]\n" +
			"  [x] execute            completed\n" +
			"        wave 1   2/2 tasks   completed\n" +
			"        wave 2   3/5 tasks   failed\n" +
			"        wave 3   0/4 tasks   pending\n" +
			"  [>] commit             in_progress\n" +
			"\n" +
			"ISSUES   none\n" +
			"Dashboard: not running"
		wave := func(name, status string, done, total int) map[string]any {
			return map[string]any{"name": name, "status": status, "tasksDone": float64(done), "tasksTotal": float64(total)}
		}
		want := base([]any{
			map[string]any{"name": "execute", "status": "completed", "waves": []any{
				wave("wave 1", "completed", 2, 2),
				wave("wave 2", "failed", 3, 5),
				wave("wave 3", "pending", 0, 4),
			}},
			map[string]any{"name": "commit", "status": "in_progress"},
		}, joinedText)
		if got := cardStructured(t, res); !reflect.DeepEqual(got, want) {
			t.Errorf("structuredContent = %#v, want %#v", got, want)
		}
		for _, line := range []string{"wave 1", "wave 2", "wave 3"} {
			if !strings.Contains(text, line) {
				t.Errorf("text has no %q:\n%s", line, text)
			}
		}
	})
}

// TestDashboardCard_FindReturnsList checks that dashboardCardFind returns the
// run and every state of its one state.List call, and nil states on error.
func TestDashboardCard_FindReturnsList(t *testing.T) {
	root := t.TempDir()
	writeCardRun(t, root, cardShipID, cardShipState())
	writeCardRun(t, root, "execute-feat-x-20261116T175600Z", cardExecState("feat/x", "2026-11-16T17:56:00Z"))
	writeCardBadRun(t, root, "execute-feat-x-20261116T175700Z.json")

	st, all, err := dashboardCardFind(root, cardShipID)
	if err != nil {
		t.Fatalf("dashboardCardFind: %v", err)
	}
	if state.RunID(st) != cardShipID {
		t.Errorf("run = %s, want %s", state.RunID(st), cardShipID)
	}
	list, err := state.List(root)
	if err != nil {
		t.Fatalf("state.List: %v", err)
	}
	if len(all) != len(list.States) || len(all) != 2 {
		t.Errorf("states = %d, want %d (the readable run files)", len(all), len(list.States))
	}

	if _, all, err := dashboardCardFind(root, "ship-absent-20261116T175538Z"); err == nil || all != nil {
		t.Errorf("missing run: states = %v, err = %v, want nil states and an error", all, err)
	}
}

// TestDashboardCard_ShipWavesUnreadableFile checks the read failure rule: a
// found ship run joins its execute run, and an unreadable run file next to
// them is ignored.
func TestDashboardCard_ShipWavesUnreadableFile(t *testing.T) {
	root := t.TempDir()
	writeCardRun(t, root, cardShipID, cardShipState())
	writeCardRun(t, root, "execute-feat-x-20261116T175600Z", cardExecState("feat/x", "2026-11-16T17:56:00Z"))
	writeCardBadRun(t, root, "execute-feat-x-20261116T175700Z.json")

	dashboardCardStubServer(t, "", false)
	out, err := dashboardCard(root, "", false, DashboardCardIn{RunID: cardShipID})
	if err != nil {
		t.Fatalf("dashboardCard: %v", err)
	}
	want := []string{"wave 1 completed 2/2", "wave 2 failed 3/5", "wave 3 pending 0/4"}
	if got := cardShipWaveRows(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("execute waves = %v, want %v", got, want)
	}
}

// TestDashboardCard_ReadsMainWorktree runs the registered tool in a linked
// worktree that holds a different run with the same id: the card shows the
// run of the main worktree.
func TestDashboardCard_ReadsMainWorktree(t *testing.T) {
	mainRoot := cardRepo(t)
	t.Setenv(t3.EnvThreadID, "")
	writeFile(t, filepath.Join(mainRoot, "README.md"), "x\n")
	runGit(t, mainRoot, "add", "README.md")
	runGit(t, mainRoot, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")
	mainState := cardShipState()
	mainState["branch"] = "main-branch"
	writeCardRun(t, mainRoot, cardShipID, mainState)

	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, mainRoot, "worktree", "add", "-q", "-b", "feat/x", linked)
	other := cardShipState()
	other["branch"] = "linked-branch"
	writeCardRun(t, linked, cardShipID, other)
	t.Chdir(linked)

	res, _ := callRegisteredDashboardCard(t, map[string]any{"runId": cardShipID})
	if got := cardStructured(t, res)["branch"]; got != "main-branch" {
		t.Errorf("branch = %v, want main-branch (the run of the main worktree)", got)
	}
}

// cardTree returns the path, size and modification time of each entry under
// root.
func cardTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		tree[path] = fmt.Sprintf("%d %s", info.Size(), info.ModTime().Format(time.RFC3339Nano))
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	return tree
}

// TestDashboardCard_WritesNoFile calls the registered tool for a ship run and
// for an execute run whose wave names a progress folder, and asserts that no
// file under the repo was added, removed or changed.
func TestDashboardCard_WritesNoFile(t *testing.T) {
	root := cardRepo(t)
	t.Setenv(t3.EnvThreadID, "thread-a")
	writeCardRun(t, root, cardShipID, cardShipState())
	const execID = "execute-feat-x-20261116T175538Z"
	writeCardRun(t, root, execID, map[string]any{
		"branch":    "feat/x",
		"startedAt": "2026-11-16T17:55:38Z",
		"waves":     []any{map[string]any{"number": 1, "status": "in_progress", "runId": "20261116T175538"}},
	})
	progress := filepath.Join(cardRunsDir(root), "20261116T175538", "progress")
	if err := os.MkdirAll(progress, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(progress, "1.json"), "{}")

	before := cardTree(t, root)
	for _, runID := range []string{cardShipID, execID} {
		res, _ := callRegisteredDashboardCard(t, map[string]any{"runId": runID})
		if res.IsError {
			t.Fatalf("%s: isError = true, want a card", runID)
		}
	}
	if after := cardTree(t, root); !reflect.DeepEqual(before, after) {
		t.Errorf("tree changed:\nbefore %v\nafter  %v", before, after)
	}
}

// ---------------------------------------------------------------------------
// Parity with the snapshot
// ---------------------------------------------------------------------------

// cardOutcome is the outcome of the card for one run id: "ok" or the error
// code of the failed call.
func cardOutcome(root, runID string) string {
	if _, err := dashboardCard(root, "", false, DashboardCardIn{RunID: runID}); err != nil {
		code, _, _ := mcpserver.ErrorParts(err)
		return code
	}
	return "ok"
}

// snapshotOutcome is the outcome of the snapshot for one run id: "listed",
// "not listed", or "repo error".
func snapshotOutcome(root, runID string) string {
	repo := collectDashboardRepo(root, time.Now())
	if repo.Error != "" {
		return "repo error"
	}
	for _, p := range repo.Pipelines {
		if p.ID == runID {
			return "listed"
		}
	}
	return "not listed"
}

// TestDashboardCard_ParityWithSnapshot runs each row of the parity table. One
// runs folder holds one entry for each file row. Rows that need a readable
// folder run first; then the folder gets the unreadable entries, which make
// every missing run a DataError. The folder rows need their own roots.
func TestDashboardCard_ParityWithSnapshot(t *testing.T) {
	type row struct {
		name, runID, snapshot, card string
	}
	check := func(t *testing.T, root string, rows []row) {
		t.Helper()
		for _, r := range rows {
			t.Run(r.name, func(t *testing.T) {
				if got := snapshotOutcome(root, r.runID); got != r.snapshot {
					t.Errorf("snapshot = %s, want %s", got, r.snapshot)
				}
				dashboardCardStubServer(t, "", false)
				if got := cardOutcome(root, r.runID); got != r.card {
					t.Errorf("card = %s, want %s", got, r.card)
				}
			})
		}
	}

	t.Run("runs folder missing", func(t *testing.T) {
		check(t, t.TempDir(), []row{{"missing folder", cardShipID, "not listed", "domain"}})
	})
	t.Run("runs folder is a file", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(cardRunsDir(root)), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(cardRunsDir(root), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		check(t, root, []row{{"folder is a file", cardShipID, "repo error", "infra"}})
	})

	root := t.TempDir()
	dir := cardRunsDir(root)
	writeCardRun(t, root, "ship-valid-20261116T175538Z", cardShipState())
	if err := os.Mkdir(filepath.Join(dir, "ship-dir-20261116T175538Z.json"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ship-stray.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	target := filepath.Join(t.TempDir(), "target.json")
	b, err := json.Marshal(cardShipState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(target, b, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "ship-link-20261116T175538Z.json")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	shape := cardShipState()
	shape[t3.StateKey] = 42
	writeCardRun(t, root, "ship-shape-20261116T175538Z", shape)
	// File size: neither reader has a bound, so a 1 MiB run file reads.
	large := cardShipState()
	large["pad"] = strings.Repeat("x", 1<<20)
	writeCardRun(t, root, "ship-large-20261116T175538Z", large)

	t.Run("readable folder", func(t *testing.T) {
		check(t, root, []row{
			{"file absent", "ship-absent-20261116T175538Z", "not listed", "domain"},
			{"directory", "ship-dir-20261116T175538Z", "not listed", "domain"},
			{"stray name", "ship-stray", "not listed", "domain"},
			{"symlink to a valid run file", "ship-link-20261116T175538Z", "listed", "ok"},
			{"t3ThreadId not a string", "ship-shape-20261116T175538Z", "listed", "ok"},
			{"large file", "ship-large-20261116T175538Z", "listed", "ok"},
			{"valid run", "ship-valid-20261116T175538Z", "listed", "ok"},
		})
	})

	writeCardBadRun(t, root, "ship-badjson-20261116T175538Z.json")
	if err := os.Symlink(filepath.Join(root, "gone.json"), filepath.Join(dir, "ship-dangling-20261116T175538Z.json")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	t.Run("folder with unreadable files", func(t *testing.T) {
		check(t, root, []row{
			{"bad JSON", "ship-badjson-20261116T175538Z", "not listed", "data"},
			{"dangling symlink", "ship-dangling-20261116T175538Z", "not listed", "data"},
			{"file absent next to unreadable files", "ship-absent-20261116T175538Z", "not listed", "data"},
			{"symlink to a valid run file", "ship-link-20261116T175538Z", "listed", "ok"},
		})
	})
}

// ---------------------------------------------------------------------------
// View and registration
// ---------------------------------------------------------------------------

// TestDashboardCardCheckView pins the size limit and the network rule of the
// view, and that the embedded view passes both.
func TestDashboardCardCheckView(t *testing.T) {
	if err := dashboardCardCheckView(dashboardCardHTML); err != nil {
		t.Fatalf("embedded view: %v", err)
	}
	if len(dashboardCardHTML) > dashboardCardMaxHTML {
		t.Fatalf("embedded view is %d bytes, above %d", len(dashboardCardHTML), dashboardCardMaxHTML)
	}

	t.Run("size at the limit passes", func(t *testing.T) {
		if err := dashboardCardCheckView(strings.Repeat("a", dashboardCardMaxHTML)); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})
	t.Run("size one byte over the limit fails", func(t *testing.T) {
		err := dashboardCardCheckView(strings.Repeat("a", dashboardCardMaxHTML+1))
		want := fmt.Sprintf("dashboard_card view is %d bytes, above the limit of %d", dashboardCardMaxHTML+1, dashboardCardMaxHTML)
		if err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	})
	for _, tt := range []struct{ text, match string }{
		{`<img src="x.png">`, `src=`},
		{`<script SRC = "a.js"></script>`, `SRC =`},
		{`<a href="https://example.com">`, `href="https:`},
		{`<a href='http://example.com'>`, `href='http:`},
		{`<link href=//cdn.example.com/a.css>`, `href=//`},
		{`fetch ('/api')`, `fetch (`},
		{`new XMLHttpRequest()`, `XMLHttpRequest`},
	} {
		t.Run("refuses "+tt.text, func(t *testing.T) {
			err := dashboardCardCheckView("<html>" + tt.text + "</html>")
			want := fmt.Sprintf("dashboard_card view loads an asset or calls the network: %q", tt.match)
			if err == nil || err.Error() != want {
				t.Errorf("err = %v, want %q", err, want)
			}
		})
	}
	t.Run("a local href passes", func(t *testing.T) {
		if err := dashboardCardCheckView(`<a href="#steps">steps</a>`); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})
}

// TestDashboardCard_Registration pins the description, annotations, view
// _meta and view resource of the registered tool.
func TestDashboardCard_Registration(t *testing.T) {
	c := connectDashboardCard(t)
	ctx := context.Background()
	resp, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(resp.Tools) != 1 || resp.Tools[0].Name != "dashboard_card" {
		t.Fatalf("tools = %v, want only dashboard_card", resp.Tools)
	}
	tool := resp.Tools[0]
	wantDesc := "Show one ship or execute run as an MCP App card (view ui://sdlc/dashboard-card) in a client that supports MCP Apps. Other clients get the card as a text block, plus the dashboard link of the run when the dashboard runs. Requires: runId. Optional: final (end card), snapshot (middle card, no refresh). Reads the run state files in <main-worktree>/.sdlc-v2/runs and the dashboard server record, and writes nothing. Call it at each card point of a top-level ship run or a standalone execute run. Errors: DomainError for a bad runId, a missing run, or a run of another T3 thread. DataError when the run is not found and some run files could not be read, or, in a T3 Code thread, when the run stores a t3ThreadId that is not a string. InfraError when the runs folder cannot be listed. A dashboard server that cannot be read is not an error: the card says that the dashboard is not running."
	if tool.Description != wantDesc {
		t.Errorf("Description = %q, want %q", tool.Description, wantDesc)
	}
	// The runId description states the slug limit as a literal; it must
	// follow t3.MaxRunSlugLen.
	schema, _ := tool.InputSchema.(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	runIDProp, _ := props["runId"].(map[string]any)
	wantRunID := fmt.Sprintf("Plain text: the state file name of a ship or execute run, without .json: ship- or execute-, then 1 to %d characters of A-Z a-z 0-9 . _ -. A plan- run id is refused. Example: ship-feat-x-20261116T175538Z", t3.MaxRunSlugLen)
	if got := runIDProp["description"]; got != wantRunID {
		t.Errorf("runId description = %v, want %q", got, wantRunID)
	}
	a := tool.Annotations
	if a == nil || a.Title != "Show a run card" || !a.ReadOnlyHint || !a.IdempotentHint ||
		a.DestructiveHint == nil || *a.DestructiveHint || a.OpenWorldHint == nil || *a.OpenWorldHint {
		t.Errorf("annotations = %+v, want read-only, idempotent, not destructive, not open-world", a)
	}
	if got := tool.Meta["ui"]; !reflect.DeepEqual(got, map[string]any{"resourceUri": "ui://sdlc/dashboard-card"}) {
		t.Errorf("_meta.ui = %#v", got)
	}

	res, err := c.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://sdlc/dashboard-card"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(res.Contents) != 1 || res.Contents[0].Text != dashboardCardHTML || res.Contents[0].MIMEType != mcpserver.MCPAppMIMEType {
		t.Errorf("resource contents do not hold the embedded view")
	}
	// The view calls the tool again by name to refresh.
	if !strings.Contains(dashboardCardHTML, "var TOOL = '"+dashboardCardToolName+"';") {
		t.Errorf("view does not call the tool %s", dashboardCardToolName)
	}
}

// TestRegisterDashboardCardTools_BadViewPanics swaps in a view that fails
// dashboardCardCheckView: registration panics with the check error and
// leaves no tool and no resource.
func TestRegisterDashboardCardTools_BadViewPanics(t *testing.T) {
	orig := dashboardCardHTML
	t.Cleanup(func() { dashboardCardHTML = orig })
	dashboardCardHTML = strings.Repeat("a", dashboardCardMaxHTML+1)

	s := mcpserver.New("test", "0.0.0-test")
	var got any
	func() {
		defer func() { got = recover() }()
		RegisterDashboardCardTools(s)
	}()
	want := fmt.Sprintf("tools: register dashboard_card: dashboard_card view is %d bytes, above the limit of %d", dashboardCardMaxHTML+1, dashboardCardMaxHTML)
	if got != want {
		t.Fatalf("panic = %v, want %q", got, want)
	}

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	c, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	tools, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) != 0 {
		t.Errorf("tools = %d, want 0", len(tools.Tools))
	}
	if _, err := c.ReadResource(ctx, &mcp.ReadResourceParams{URI: dashboardCardViewURI}); err == nil {
		t.Error("ReadResource of the view: err = nil, want no such resource")
	}
}

// ---------------------------------------------------------------------------
// Text block and dashboard link for a client with no card
// ---------------------------------------------------------------------------

// TestDashboardCard_TextGolden pins the text block of the example ship run of
// the plan: a running ship run with 4 steps, a high issue and a joined execute
// run with 2 waves, and a dashboard server that runs. The golden file holds the
// block with one trailing newline.
func TestDashboardCard_TextGolden(t *testing.T) {
	root := t.TempDir()
	const (
		runID = "ship-feat-x-20261116T175538Z"
		url   = "http://127.0.0.1:55551"
	)
	writeCardRun(t, root, runID, map[string]any{
		"branch":    "feat/x",
		"startedAt": "2026-11-16T17:55:38Z",
		"steps": []any{
			map[string]any{"name": "execute", "status": "completed"},
			map[string]any{"name": "commit", "status": "in_progress"},
			map[string]any{"name": "review", "status": "pending"},
			map[string]any{"name": "pr", "status": "pending"},
		},
		"issues": []any{map[string]any{"severity": "error", "text": "e1"}},
	})
	task := func(id, status string) map[string]any { return map[string]any{"id": id, "status": status} }
	writeCardRun(t, root, "execute-feat-x-20261116T175600Z", map[string]any{
		"branch":    "feat/x",
		"startedAt": "2026-11-16T17:56:00Z",
		"waves": []any{
			map[string]any{"number": 1, "status": "completed", "tasks": []any{task("1", "completed"), task("2", "completed")}},
			map[string]any{"number": 2, "status": "completed", "tasks": []any{task("3", "completed"), task("4", "completed"), task("5", "completed")}},
		},
	})
	dashboardCardStubServer(t, url, true)

	out, err := dashboardCard(root, "", false, DashboardCardIn{RunID: runID})
	if err != nil {
		t.Fatalf("dashboardCard: %v", err)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "dashboard_card_text_ship.golden"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got := out.Text + "\n"; got != string(golden) {
		t.Errorf("text =\n%s\nwant golden\n%s", got, golden)
	}
	if out.DashboardURL != url+"#"+runID {
		t.Errorf("DashboardURL = %q, want %q", out.DashboardURL, url+"#"+runID)
	}
	if out.Next != cardNoViewNextServer {
		t.Errorf("Next = %q, want the server-runs text", out.Next)
	}
}

// TestDashboardCard_TextMarkers pins the marker and the bar character of each
// step status, and the done count: completed and skipped steps are done.
func TestDashboardCard_TextMarkers(t *testing.T) {
	statuses := []string{"completed", "in_progress", "pending", "skipped", "failed", "weird"}
	steps := make([]DashboardCardStep, 0, len(statuses))
	for i, s := range statuses {
		steps = append(steps, DashboardCardStep{Name: fmt.Sprintf("s%d", i), Status: s})
	}
	text := dashboardCardText(DashboardCardOut{Kind: "ship", RunID: "r", Status: "running", Steps: steps, Issues: map[string]int{}})
	for _, want := range []string{
		"STEPS                                   2 of 6 done\n",
		"[#>.#!.]\n",
		"  [x] s0                 completed\n",
		"  [>] s1                 in_progress\n",
		"  [ ] s2                 pending\n",
		"  [-] s3                 skipped\n",
		"  [!] s4                 failed\n",
		"  [ ] s5                 weird\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text has no %q:\n%s", want, text)
		}
	}
}

// TestDashboardCard_TextHeader pins the header state: [stopped] only for an
// end card whose run still says running, else the run status.
func TestDashboardCard_TextHeader(t *testing.T) {
	cases := []struct {
		name          string
		status        string
		final         bool
		kind, wantTop string
	}{
		{"running live", "running", false, "ship", "SHIP  [running]\n"},
		{"running end card", "running", true, "ship", "SHIP  [stopped]\n"},
		{"completed end card", "completed", true, "execute", "EXECUTE  [completed]\n"},
		{"failed", "failed", false, "execute", "EXECUTE  [failed]\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := dashboardCardText(DashboardCardOut{Kind: tc.kind, RunID: "r", Status: tc.status, Final: tc.final})
			if !strings.HasPrefix(text, tc.wantTop) {
				t.Errorf("text = %q, want prefix %q", text, tc.wantTop)
			}
		})
	}
}

// TestDashboardCard_TextIssuesAndEmptyValues covers ISSUES none, issues sorted
// by key, an empty branch and start time, a completed time, a step name over
// 18 characters, and a run with 0 steps.
func TestDashboardCard_TextIssuesAndEmptyValues(t *testing.T) {
	done := "2026-11-16T18:10:00Z"
	t.Run("empty branch and start, no steps, no issue", func(t *testing.T) {
		got := dashboardCardText(DashboardCardOut{Kind: "ship", RunID: "r", Status: "running"})
		want := "SHIP  [running]\n" +
			"r\n" +
			"Branch:    not recorded\n" +
			"Started:   not recorded\n" +
			"Completed: not recorded\n" +
			"\n" +
			"STEPS\n" +
			"No steps yet.\n" +
			"\n" +
			"ISSUES   none\n" +
			"Dashboard: not running"
		if got != want {
			t.Errorf("text =\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("issues sorted, completed time, long step name", func(t *testing.T) {
		long := "a-step-name-over-18-chars"
		got := dashboardCardText(DashboardCardOut{
			Kind: "ship", RunID: "r", Status: "completed", Branch: "b", StartedAt: "s", CompletedAt: &done,
			Steps:        []DashboardCardStep{{Name: long, Status: "completed"}},
			Issues:       map[string]int{"medium": 2, "high": 1},
			DashboardURL: "http://127.0.0.1:1#r",
		})
		want := "SHIP  [completed]\n" +
			"r\n" +
			"Branch:    b\n" +
			"Started:   s\n" +
			"Completed: " + done + "\n" +
			"\n" +
			"STEPS                                   1 of 1 done\n" +
			"[#]\n" +
			"  [x] " + long + " completed\n" +
			"\n" +
			"ISSUES   high 1, medium 2\n" +
			"Dashboard: http://127.0.0.1:1#r"
		if got != want {
			t.Errorf("text =\n%s\nwant\n%s", got, want)
		}
	})
}

// TestDashboardCard_ServerCases covers the dashboard link of a client with no
// card, and that a client with a card gets neither text nor dashboardUrl.
func TestDashboardCard_ServerCases(t *testing.T) {
	root := t.TempDir()
	writeCardRun(t, root, cardShipID, cardShipState())

	t.Run("server runs", func(t *testing.T) {
		dashboardCardStubServer(t, "http://127.0.0.1:55551", true)
		out, err := dashboardCard(root, "", false, DashboardCardIn{RunID: cardShipID})
		if err != nil {
			t.Fatalf("dashboardCard: %v", err)
		}
		link := "http://127.0.0.1:55551#" + cardShipID
		if out.DashboardURL != link || out.Next != cardNoViewNextServer {
			t.Errorf("DashboardURL, Next = %q, %q", out.DashboardURL, out.Next)
		}
		if !strings.HasSuffix(out.Text, "\nDashboard: "+link) {
			t.Errorf("text does not end with the link:\n%s", out.Text)
		}
	})
	t.Run("no server", func(t *testing.T) {
		dashboardCardStubServer(t, "http://127.0.0.1:55551", false)
		out, err := dashboardCard(root, "", false, DashboardCardIn{RunID: cardShipID})
		if err != nil {
			t.Fatalf("card call failed: %v", err)
		}
		if out.DashboardURL != "" || out.Next != cardNoViewNext(cardShipID) {
			t.Errorf("DashboardURL, Next = %q, %q", out.DashboardURL, out.Next)
		}
		if !strings.HasSuffix(out.Text, "\nDashboard: not running") {
			t.Errorf("text does not end with not running:\n%s", out.Text)
		}
	})
	t.Run("client with a card gets no text and no link", func(t *testing.T) {
		dashboardCardStubServer(t, "http://127.0.0.1:55551", true)
		out, err := dashboardCard(root, "", true, DashboardCardIn{RunID: cardShipID})
		if err != nil {
			t.Fatalf("dashboardCard: %v", err)
		}
		if out.Text != "" || out.DashboardURL != "" || out.Next != cardViewNext(cardShipID, "running") {
			t.Errorf("Text, DashboardURL, Next = %q, %q, %q", out.Text, out.DashboardURL, out.Next)
		}
	})
}

// TestDashboardCardServerFrom has one row for each row of the server table.
// The error rows use the real errors of os.ReadFile, the call under
// fsx.ReadJSON in dashboard.Status.
func TestDashboardCardServerFrom(t *testing.T) {
	dir := t.TempDir()
	_, folderErr := os.ReadFile(dir) // a folder at the record path
	if folderErr == nil {
		t.Fatal("reading a folder gave no error")
	}
	_, brokenLinkErr := os.ReadFile(filepath.Join(dir, "no-such-record")) // a broken link
	if brokenLinkErr == nil {
		t.Fatal("reading a missing file gave no error")
	}
	rows := []struct {
		name    string
		res     dashboard.Result
		err     error
		wantURL string
		wantOK  bool
	}{
		{name: "no record file", res: dashboard.Result{}},
		{name: "bad record JSON", err: errors.New("bad json")},
		{name: "probe fails or times out", res: dashboard.Result{URL: "http://127.0.0.1:1", Port: 1}},
		{name: "server answers", res: dashboard.Result{URL: "http://127.0.0.1:55551", Port: 55551, Running: true}, wantURL: "http://127.0.0.1:55551", wantOK: true},
		{name: "folder at the record path", err: folderErr},
		{name: "broken link at the record path", err: brokenLinkErr},
		{name: "link to a valid record, server answers", res: dashboard.Result{URL: "http://127.0.0.1:55551", Running: true}, wantURL: "http://127.0.0.1:55551", wantOK: true},
		{name: "running with no URL", res: dashboard.Result{Running: true}},
		{name: "error wins over a running result", res: dashboard.Result{URL: "http://127.0.0.1:1", Running: true}, err: errors.New("x")},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			url, ok := dashboardCardServerFrom(r.res, r.err)
			if url != r.wantURL || ok != r.wantOK {
				t.Errorf("got %q, %v, want %q, %v", url, ok, r.wantURL, r.wantOK)
			}
		})
	}
}
