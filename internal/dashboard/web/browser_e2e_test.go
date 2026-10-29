//go:build integration

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/rnagrodzki/sdlc-plugin/internal/attention"
	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// Every wait of the browser test is a poll with a limit. A wait checks its
// condition each browserStepEvery and fails the test after browserStepLimit.
const (
	browserStepLimit = 10 * time.Second
	browserStepEvery = 100 * time.Millisecond
	// browserSnapshotEvery is the snapshot poll interval of the server, so the
	// page sees a change of a file soon.
	browserSnapshotEvery = 200 * time.Millisecond
)

// What the test writes to the temp repos.
const (
	browserBranch      = "feat/browser" // the running ship run and its plan and ship History rows
	browserPlanSlug    = "browser-topic"
	browserBranchA     = "fix/a" // the History row that the failed delete names
	browserBranchB     = "fix/b" // the History row that the real delete removes
	browserShipSession = "s-ship"

	browserTitleLocal = "Local fix: lock the save path"
	browserTitlePR1   = "PR comment: check for nil"
	// browserPR1Place is the title of the detail viewer for the row of browserTitlePR1:
	// the severity, then the file and the line, as seeded in browserSeedShip.
	browserPR1Place = "medium: internal/api/handler.go:17"
	browserTitlePR2 = "PR comment: rename the helper"
	// browserPRPrefix starts the title of every PR comment row.
	browserPRPrefix = "PR comment:"
)

// What the Activity tab Preplans box steps (A1 to A3) seed and look for.
const (
	browserFullSlug = "full-topic" // the topic file that the collector reads whole
	browserBigSlug  = "big-topic"  // the topic file that is over the read cap
	// browserFullGoal is the one sentence of the ## Goal section of full-topic.md.
	browserFullGoal = "Show every preplan on the Activity tab with its proposal counts."
	// browserFullCounts starts the meta line of the full-topic row: the live,
	// kept and dropped rows of the ## Decisions table of full-topic.md.
	browserFullCounts = "live 3 · kept 1 · dropped 1"
	// browserFullCheck is the Guardrail check line of full-topic.md. Its counts
	// match the ## Decisions table: 3 live rows (2 pass, 1 kept) and 1 dropped row.
	browserFullCheck = "**Guardrail check:** 12 guardrails, 4 proposals: 2 pass, 1 kept, 1 dropped. Last check: 2026-10-29T07:58:25Z."
	// browserBigTopicSize is the size of big-topic.md in bytes: 1 MiB. It is
	// above the cap of the collector (dashboardPreplanSummaryMax in
	// internal/tools/dashboard_preplans.go: 256 KiB, the largest size in bytes
	// of one topic file that the collector reads whole for its summary). It is
	// below the 8 MiB read limit of preplan_check, so the file is a topic file
	// that the tools accept.
	browserBigTopicSize = 1 << 20
	// browserUnreadWord is in the label of every part of a Preplans box row that
	// the collector did not read.
	browserUnreadWord = "unreadable"
	// browserBigUnreadMeta is how many times browserUnreadWord is in the meta
	// line of the big-topic row: the proposals, check, open questions, gaps and
	// cut parts. The goal part is the sixth unread name, on the goal line.
	browserBigUnreadMeta = 5
)

// The CSS selectors of the Activity tab Preplans box.
const (
	browserSelActivityTab = "#tab-activity"
	// browserSelBoxTitle is the title h2 of the box. Only the Preplans box has
	// the class act-wide, and the title text is "Preplans (n)".
	browserSelBoxTitle = ".act-wide .list-title"
	browserSelRow      = ".act-preplan"
	browserSelRowTitle = ".pp-name"
	browserSelRowGoal  = ".act-text"
	browserSelRowMeta  = ".act-meta"
)

// browserWaitSeed names one open question wait that no pipeline owns.
type browserWaitSeed struct{ session, tool, branch string }

var (
	browserWaitA = browserWaitSeed{"s-wait-a", "toolu_wait_a", "feat/other-a"}
	browserWaitB = browserWaitSeed{"s-wait-b", "toolu_wait_b", "feat/other-b"}
)

// key is the value of data-wait-dismiss on the Dismiss button of the wait.
func (w browserWaitSeed) key() string { return w.session + "|" + w.tool }

// file is the record file of the wait in repo root. Both IDs hold only
// characters that attention.Write keeps.
func (w browserWaitSeed) file(root string) string {
	return filepath.Join(attention.Dir(root), w.session+"-"+w.tool+".json")
}

// browserTS formats t as the dashboard files do.
func browserTS(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// browserJSON returns v as JSON. The text is also a valid JavaScript literal
// and a valid quoted CSS string for the plain values that the test uses.
func browserJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// browserPath returns the path of a Chrome or Chromium binary. With none, the
// test fails when CI is set, so a CI runner without a browser cannot pass by
// skipping, and it skips on a developer machine.
func browserPath(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	const macApp = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(macApp); err == nil {
		return macApp
	}
	const hint = "no Chrome or Chromium found on PATH; install google-chrome or chromium (macOS: brew install --cask google-chrome)"
	if os.Getenv("CI") != "" {
		t.Fatal(hint)
	}
	t.Skip(hint)
	return ""
}

// browserPoll calls check each browserStepEvery until it returns true. It
// reports false when browserStepLimit passes first.
func browserPoll(check func() bool) bool {
	deadline := time.Now().Add(browserStepLimit)
	for {
		if check() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(browserStepEvery)
	}
}

// browserWaitFor fails the test when check does not turn true in time.
func browserWaitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	if !browserPoll(check) {
		t.Fatalf("waited %s for %s", browserStepLimit, what)
	}
}

// browserCollector wraps the snapshot collector. While frozen, it answers with
// the last snapshot and reads no file, so the test can change a file that the
// page has not seen yet.
type browserCollector struct {
	mu     sync.Mutex
	frozen bool
	last   tools.DashboardSnapshot
}

func (c *browserCollector) collect(roots []string, at time.Time) tools.DashboardSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.frozen {
		c.last = tools.CollectDashboardSnapshot(roots, at, testVersion)
	}
	return c.last
}

// setFrozen starts or ends the freeze. It waits for a collect in progress, so
// a freeze keeps the snapshot from before the call.
func (c *browserCollector) setFrozen(frozen bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frozen = frozen
}

// browserOptions wires the server like dashboardOptions in cmd/sdlc/main.go,
// with a pre-bound listener, two fixed repos and the wrapped collector. The
// route functions come from WithToolRoutes, the same wiring as production.
func browserOptions(ln net.Listener, roots []string, c *browserCollector) Options {
	return WithToolRoutes(Options{
		Port:    ln.Addr().(*net.TCPAddr).Port,
		Version: testVersion,
		Token:   NewToken(),
		Roots: func(at time.Time) ([]dashboard.Root, error) {
			out := make([]dashboard.Root, 0, len(roots))
			for _, r := range roots {
				out = append(out, dashboard.Root{Root: r, LastSeen: at})
			}
			return out, nil
		},
		Collect: c.collect,
		Listen:  func(string, string) (net.Listener, error) { return ln, nil },
		Health: func(int, time.Duration) (dashboard.Health, error) {
			return dashboard.Health{}, errors.New("unexpected Health call")
		},
	}, tools.ClearCache)
}

// browserRunsPath is the runs.jsonl file of repo root.
func browserRunsPath(root string) string {
	return history.NewFileWriter(paths.HistoryDir(root)).RunsPath()
}

// browserWriteRuns writes rows to runs.jsonl of root, one JSON object each line.
func browserWriteRuns(t *testing.T, root string, rows []history.RunRecord) {
	t.Helper()
	var buf bytes.Buffer
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	e2eWriteFile(t, browserRunsPath(root), buf.String())
}

// browserRunRows reads the rows of runs.jsonl of root.
func browserRunRows(t *testing.T, root string) []history.RunRecord {
	t.Helper()
	data, err := os.ReadFile(browserRunsPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var rows []history.RunRecord
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r history.RunRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("runs.jsonl line %q: %v", line, err)
		}
		rows = append(rows, r)
	}
	return rows
}

// browserRunBranches lists the branch of each row of runs.jsonl, in file order.
func browserRunBranches(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, r := range browserRunRows(t, root) {
		out = append(out, r.Branch)
	}
	return out
}

// browserDropRun removes the rows of branch from runs.jsonl of root, as a
// second session would.
func browserDropRun(t *testing.T, root, branch string) {
	t.Helper()
	var kept []history.RunRecord
	for _, r := range browserRunRows(t, root) {
		if r.Branch != branch {
			kept = append(kept, r)
		}
	}
	browserWriteRuns(t, root, kept)
}

// browserFix returns one data.healing.fixProgress record.
func browserFix(origin, severity, file string, line int, title, status string, at time.Time) map[string]any {
	return map[string]any{
		"origin": origin, "severity": severity, "file": file, "line": line,
		"title": title, "status": status, "firstAt": browserTS(at), "updatedAt": browserTS(at),
	}
}

// browserShip is the running ship state of the test. write stores Data with
// the current fix records, so the next snapshot differs.
type browserShip struct {
	st    *state.State
	fixes []any
}

// browserSeedShip writes a running ship state with one fixed local finding,
// one PR comment in work, one finding below the threshold, and the review
// total that the finding counts need. state.Write is the only writer, so the
// file is the one that the dashboard reads.
func browserSeedShip(t *testing.T, root string, now time.Time) *browserShip {
	t.Helper()
	st, err := state.Init(root, "ship", browserBranch, browserShipSession)
	if err != nil {
		t.Fatal(err)
	}
	st.Data["branch"] = browserBranch
	st.Data["startedAt"] = browserTS(now.Add(-30 * time.Minute))
	st.Data["steps"] = []any{
		map[string]any{"name": "execute", "status": tools.StepCompleted, "startedAt": browserTS(now.Add(-30 * time.Minute)), "completedAt": browserTS(now.Add(-20 * time.Minute))},
		map[string]any{"name": "commit", "status": tools.StepCompleted, "startedAt": browserTS(now.Add(-20 * time.Minute)), "completedAt": browserTS(now.Add(-19 * time.Minute))},
		map[string]any{"name": "review", "status": tools.StepInProgress, "startedAt": browserTS(now.Add(-18 * time.Minute))},
	}
	st.Data["deferredFindings"] = []any{
		map[string]any{
			"title": "Below threshold: spelling", "severity": "low", "file": "internal/util/names.go", "line": 3,
			"reason": history.ReasonBelowThreshold, "description": "A spelling nit.",
		},
	}
	s := &browserShip{st: st}
	s.fixes = []any{
		browserFix("local-review", "high", "internal/store/save.go", 42, browserTitleLocal, "fixed", now.Add(-12*time.Minute)),
		browserFix("pr-comment", "medium", "internal/api/handler.go", 17, browserTitlePR1, "fixing", now.Add(-6*time.Minute)),
	}
	s.write(t)
	return s
}

// write stores the ship state with the current fix records.
func (s *browserShip) write(t *testing.T) {
	t.Helper()
	s.st.Data["healing"] = map[string]any{
		// Two review findings: the fixed one and the one below the threshold.
		"reviewTotal": map[string]any{"total": 2, "dimensions": 1},
		"fixed":       []any{map[string]any{"origin": "local-review", "severity": "high", "file": "internal/store/save.go", "line": 42, "title": browserTitleLocal}},
		"fixProgress": s.fixes,
	}
	if err := state.Write(s.st); err != nil {
		t.Fatal(err)
	}
}

// addPR adds one PR comment fix and writes the state.
func (s *browserShip) addPR(t *testing.T, title string, at time.Time) {
	t.Helper()
	s.fixes = append(s.fixes, browserFix("pr-comment", "low", "internal/api/routes.go", 9, title, "queued", at))
	s.write(t)
}

// browserSeedRepoA writes everything that repo A shows: the preplan topic
// files (the two of browserSeedPreplanSummary too), runs.jsonl with a plan
// row, a ship row and the two rows that the History steps delete, the running
// ship state, and one lone wait. The times are near now, so the 24-hour
// windows of the snapshot hold them.
func browserSeedRepoA(t *testing.T, root string, now time.Time) *browserShip {
	t.Helper()
	topic := filepath.Join(root, paths.DataDir, paths.PreplanSubdir, browserPlanSlug+".md")
	// PreplanStatuses[1] is "ready for plan"; the test changes it to the last status.
	e2eWriteFile(t, topic, "# Preplan: Browser topic\n\n**Status:** "+tools.PreplanStatuses[1]+"\n")
	// browser-long started 2 hours before now and changes now, so it has a
	// duration. browser-none has no Started line, so its duration is a dash.
	started := map[string]string{
		"browser-long": "\n**Started:** " + now.Add(-2*time.Hour).Format(time.RFC3339) + "\n",
		"browser-none": "",
	}
	for slug, line := range started {
		p := filepath.Join(root, paths.DataDir, paths.PreplanSubdir, slug+".md")
		e2eWriteFile(t, p, "# Preplan: "+slug+"\n\n**Status:** "+tools.PreplanStatuses[1]+"\n"+line)
	}

	browserWriteRuns(t, root, []history.RunRecord{
		{Timestamp: browserTS(now.Add(-70 * time.Minute)), Skill: "plan", Branch: browserBranch, Outcome: "done", DurationMs: 600000, StartedAt: browserTS(now.Add(-80 * time.Minute))},
		{Timestamp: browserTS(now.Add(-60 * time.Minute)), Skill: "ship", Branch: browserBranch, Outcome: "success", DurationMs: 1200000},
		{Timestamp: browserTS(now.Add(-50 * time.Minute)), Skill: "commit", Branch: browserBranchA, Outcome: "success", DurationMs: 30000},
		{Timestamp: browserTS(now.Add(-40 * time.Minute)), Skill: "commit", Branch: browserBranchB, Outcome: "success", DurationMs: 30000},
	})

	browserSeedPreplanSummary(t, root)

	ship := browserSeedShip(t, root, now)
	browserSeedWait(t, root, browserWaitA, now)
	return ship
}

// browserFullTopic returns the text of full-topic.md. It is a topic file that
// passes the format checker of preplan_write (rules P1 to P11), so the
// collector reads every summary field: the goal sentence, 3 live rows (1 kept),
// 1 dropped row, the check time, 1 open question and no unread name. The file
// has no **Started:** line, so its duration is 0.
func browserFullTopic() string {
	const decisions = "| # | Decision | Reason |\n" +
		"|---|---|---|\n" +
		"| 1 | Show the box on the Activity tab | The user compares preplans there. |\n" +
		"| 2 | Read each topic file once | Kept with warning: the read costs time on a big file. |\n" +
		"| 3 | Show the counts of the proposals | The counts tell how far the preplan is. |\n" +
		"| 4 | Dropped: Add a bin button to the box | Dropped by: the Preplans tab keeps the controls. |\n"
	sections := []struct{ name, body string }{
		{"Goal", browserFullGoal + "\n"},
		{"Problem and benefit", "The Activity tab shows no preplan today.\n"},
		{"Users and effect", "The user sees each preplan next to the other work.\n"},
		{"Risk if wrong", "The box shows a wrong count.\n"},
		{"Non-goals", "None stated.\n"},
		{"Flows", "No flow change.\n"},
		{"Decisions", decisions},
		{"Open questions", "- Which repos does the box list?\n"},
	}
	var b strings.Builder
	b.WriteString("# Preplan: " + browserFullSlug + "\n\n**Status:** " + tools.PreplanStatuses[1] + "\n" + browserFullCheck + "\n")
	for _, s := range sections {
		b.WriteString("\n## " + s.name + "\n\n" + s.body)
	}
	return b.String()
}

// browserSeedPreplanSummary writes the two topic files of the Activity tab
// Preplans box to root: full-topic.md (browserFullTopic) and big-topic.md. The
// head of big-topic.md is a valid topic head, so its row shows in the list.
// The whole file is browserBigTopicSize bytes, so the collector does not read
// it whole.
func browserSeedPreplanSummary(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, paths.DataDir, paths.PreplanSubdir)
	e2eWriteFile(t, filepath.Join(dir, browserFullSlug+".md"), browserFullTopic())
	head := "# Preplan: " + browserBigSlug + "\n\n**Status:** " + tools.PreplanStatuses[1] + "\n\n"
	e2eWriteFile(t, filepath.Join(dir, browserBigSlug+".md"), head+strings.Repeat("x", browserBigTopicSize-len(head)))
}

// browserSeedWait writes the question wait w to root. Its session is not the
// session of any pipeline, so the snapshot lists it as a lone wait.
func browserSeedWait(t *testing.T, root string, w browserWaitSeed, now time.Time) {
	t.Helper()
	err := attention.Write(root, attention.Record{
		Kind:      attention.KindQuestion,
		SessionID: w.session,
		ToolUseID: w.tool,
		Branch:    w.branch,
		Header:    "Pick a path",
		Text:      "Which option do you want?",
		AskedAt:   browserTS(now.Add(-2 * time.Minute)),
	})
	if err != nil {
		t.Fatal(err)
	}
}

// What browserSeedHardenShip writes and the harden tile test looks for.
const (
	browserHardenRationale = "Two reviews found the same gap."
	browserHardenPatch     = `id = "auth-check"`
	// browserHardenNoWhy is the text of an edit of an old record, which has no rationale.
	browserHardenNoWhy = "No rationale recorded."
	// browserHardenBad is the marker of the run record that is not an object.
	browserHardenBad = "1 harden record could not be read. The other records show below."
)

// browserSeedHardenShip writes a ship run whose harden step has two run
// records: one valid record with a new-form and an old-form edit, and one
// entry that is not an object.
func browserSeedHardenShip(t *testing.T, root string, now time.Time) {
	t.Helper()
	st, err := state.Init(root, "ship", "feat/harden", "s-harden")
	if err != nil {
		t.Fatal(err)
	}
	st.Data["branch"] = "feat/harden"
	st.Data["startedAt"] = browserTS(now.Add(-30 * time.Minute))
	st.Data["steps"] = []any{
		map[string]any{"name": "review", "status": tools.StepCompleted, "startedAt": browserTS(now.Add(-30 * time.Minute)), "completedAt": browserTS(now.Add(-20 * time.Minute))},
		map[string]any{"name": "harden", "status": tools.StepCompleted, "startedAt": browserTS(now.Add(-20 * time.Minute)), "completedAt": browserTS(now.Add(-10 * time.Minute))},
	}
	st.Data["healing"] = map[string]any{"hardened": []any{
		map[string]any{"phase": "done", "trigger": "cluster:src/auth.ts", "classification": "user-code",
			"applied": []any{
				map[string]any{"surface": "plan-guardrails", "action": "add", "targetFile": ".sdlc-v2/config.toml",
					"rationale": browserHardenRationale, "patch": browserHardenPatch},
				map[string]any{"surface": "review-dimensions", "action": "strengthen", "targetFile": ".sdlc-v2/review-dimensions/auth-checks.md"},
			},
			"skipped": 0, "recordedAt": browserTS(now.Add(-11 * time.Minute))},
		"not an object",
	}}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}
}

// browserPage drives one Chrome tab. Each method fails the test on an error.
type browserPage struct {
	t   *testing.T
	ctx context.Context
}

// browserEval runs the JavaScript expression js in the page and decodes its
// value into T. It limits the call to browserStepLimit.
func browserEval[T any](p *browserPage, js string) (T, error) {
	tctx, cancel := context.WithTimeout(p.ctx, browserStepLimit)
	defer cancel()
	return chromedp.Run(tctx, chromedp.Evaluate[T](js))
}

// browserMust evaluates js and fails the test on an error.
func browserMust[T any](p *browserPage, what, js string) T {
	p.t.Helper()
	v, err := browserEval[T](p, js)
	if err != nil {
		p.t.Fatalf("%s: %v", what, err)
	}
	return v
}

// do runs the chromedp steps, limited to browserStepLimit.
func (p *browserPage) do(what string, steps ...chromedp.Action[chromedp.Void]) {
	p.t.Helper()
	tctx, cancel := context.WithTimeout(p.ctx, browserStepLimit)
	defer cancel()
	if err := chromedp.Do(tctx, steps...); err != nil {
		p.t.Fatalf("%s: %v", what, err)
	}
}

// click clicks the first element that matches the CSS selector sel. The click
// is a real mouse click, so the element must be visible.
func (p *browserPage) click(what, sel string) {
	p.t.Helper()
	p.do("click "+what+" ("+sel+")", chromedp.Click(chromedp.CSS(sel)))
}

// fail stops the test with msg and the state of the page: the start of its
// text, the confirm dialog, and the API requests that it made.
func (p *browserPage) fail(format string, args ...any) {
	p.t.Helper()
	text, _ := browserEval[string](p, `document.body.innerText.slice(0, 600)`)
	state, _ := browserEval[string](p, `JSON.stringify({
		confirmOpen: document.getElementById('confirm-dialog').open,
		confirmText: document.getElementById('confirm-text').textContent,
		confirmBusy: typeof confirmFlow !== 'undefined' && !!confirmFlow && confirmFlow.busy,
		requests: performance.getEntriesByType('resource')
			.filter(function (r) { return r.name.indexOf('/api/') !== -1; })
			.map(function (r) { return [r.name.replace(location.origin, ''), Math.round(r.startTime), Math.round(r.responseEnd), r.responseStatus]; })
	})`)
	p.t.Fatalf("%s\npage state: %s\npage text: %q", fmt.Sprintf(format, args...), state, text)
}

// waitFor polls check until it is true. It fails the test with the state of
// the page when browserStepLimit passes first.
func (p *browserPage) waitFor(what string, check func() bool) {
	p.t.Helper()
	if !browserPoll(check) {
		p.fail("waited %s for %s", browserStepLimit, what)
	}
}

// wait polls the JavaScript expression js until it is true. It fails the test
// with the last error of the script and the state of the page when
// browserStepLimit passes first.
func (p *browserPage) wait(what, js string) {
	p.t.Helper()
	var last error
	ok := browserPoll(func() bool {
		v, err := browserEval[bool](p, js)
		last = err
		return err == nil && v
	})
	if !ok {
		p.fail("waited %s for %s (last error: %v)", browserStepLimit, what, last)
	}
}

// count returns the number of elements that match the CSS selector sel.
func (p *browserPage) count(sel string) int {
	p.t.Helper()
	return browserMust[int](p, "count "+sel, `document.querySelectorAll(`+browserJSON(sel)+`).length`)
}

// text returns the text of the first element that matches sel, or "".
func (p *browserPage) text(sel string) string {
	p.t.Helper()
	return browserMust[string](p, "text of "+sel, `(function () { var n = document.querySelector(`+browserJSON(sel)+`); return n ? n.textContent : ''; })()`)
}

// listenClose starts to watch the close event of the dialog with the given id.
// The close event fires after dialog.open turns false, and app.js clears its
// confirm flow in that event. A step that opens the next dialog must wait for
// the event with waitClosed, else the late event can end the next flow. The
// watcher is added after the listeners of app.js, so they have run when it
// fires.
func (p *browserPage) listenClose(id string) {
	p.t.Helper()
	browserMust[bool](p, "listen for close of "+id, `(function () {
		window.__closed = false;
		document.getElementById(`+browserJSON(id)+`).addEventListener('close', function () { window.__closed = true; }, { once: true });
		return true;
	})()`)
}

// waitClosed waits for the close event that listenClose watches.
func (p *browserPage) waitClosed(id string) {
	p.t.Helper()
	p.wait("the close event of "+id, `window.__closed === true && !document.getElementById(`+browserJSON(id)+`).open`)
}

// browserPreplanPartJS returns a JavaScript expression for the text of the
// part (a CSS selector inside the row) of the Activity tab Preplans box row
// whose title starts with slug. The expression is "" when no row has that
// title.
func browserPreplanPartJS(slug, part string) string {
	return `(function () {
		var rows = document.querySelectorAll(` + browserJSON(browserSelRow) + `);
		for (var i = 0; i < rows.length; i++) {
			var title = rows[i].querySelector(` + browserJSON(browserSelRowTitle) + `);
			if (title && title.textContent.indexOf(` + browserJSON(slug) + `) === 0) {
				var node = rows[i].querySelector(` + browserJSON(part) + `);
				return node ? node.textContent : '';
			}
		}
		return '';
	})()`
}

// browserCountTopicFiles returns the number of topic files in the preplan
// folder of root.
func browserCountTopicFiles(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, paths.DataDir, paths.PreplanSubdir))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			n++
		}
	}
	return n
}

// browserCheckPreplanBox runs the steps A1 to A3: the Preplans box of the
// Activity tab. The collector read full-topic.md whole, so its row shows the
// goal sentence and the counts. big-topic.md is over the read cap of the
// collector, so every part of its row shows an unread label. A row that the
// collector read must show no unread label. root is repo A.
func browserCheckPreplanBox(page *browserPage, root string) {
	t := page.t

	// Step A1: the Activity tab shows the box with one row for each topic file.
	t.Log("step A1: Activity Preplans box title")
	files := browserCountTopicFiles(t, root)
	title := "Preplans (" + strconv.Itoa(files) + ")"
	page.click("the Activity tab", browserSelActivityTab)
	page.wait("the title of the Preplans box", `(function () {
		var n = document.querySelector(`+browserJSON(browserSelBoxTitle)+`);
		return !!n && n.textContent === `+browserJSON(title)+`;
	})()`)
	if got := page.count(browserSelRow); got != files {
		t.Fatalf("Preplans box rows = %d; want %d, one for each topic file", got, files)
	}

	// Step A2: the row of the file that the collector read whole.
	t.Log("step A2: full-topic row")
	goal := browserPreplanPartJS(browserFullSlug, browserSelRowGoal)
	meta := browserPreplanPartJS(browserFullSlug, browserSelRowMeta)
	page.wait("the goal sentence in the full-topic row", goal+` === `+browserJSON(browserFullGoal))
	page.wait("the counts in the full-topic row", meta+`.indexOf(`+browserJSON(browserFullCounts)+`) === 0`)
	if got := browserMust[string](page, "the full-topic row text", goal+` + ' ' + `+meta); strings.Contains(got, browserUnreadWord) {
		t.Fatalf("full-topic row = %q; want no %q label, the collector read the whole file", got, browserUnreadWord)
	}

	// Step A3: the row of the file that is over the read cap.
	t.Log("step A3: big-topic row")
	bigGoal := browserPreplanPartJS(browserBigSlug, browserSelRowGoal)
	page.wait("the unread label in the goal line of the big-topic row", bigGoal+`.indexOf(`+browserJSON(browserUnreadWord)+`) !== -1`)
	bigMeta := browserMust[string](page, "the big-topic meta line", browserPreplanPartJS(browserBigSlug, browserSelRowMeta))
	if got := strings.Count(bigMeta, browserUnreadWord); got != browserBigUnreadMeta {
		t.Fatalf("big-topic meta line = %q; want %d parts with %q, got %d", bigMeta, browserBigUnreadMeta, browserUnreadWord, got)
	}
}

// browserSnapshotOf reads GET /api/snapshot from the server at base.
func browserSnapshotOf(t *testing.T, base string) tools.DashboardSnapshot {
	t.Helper()
	resp, err := http.Get(base + "/api/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var snap tools.DashboardSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode /api/snapshot: %v", err)
	}
	return snap
}

// TestBrowserE2E drives the real page in headless Chrome against web.Serve on
// a temp repo, with the real write functions. Every step changes a file and
// reads the result from disk, or reads it from the page.
//
// Steps:
//  1. Preplans: the status menu writes the new **Status:** line, and the
//     repo header sorts the table A to Z and keeps the focus. A duration
//     cell shows a time or a dash, and the duration header sorts.
//  2. History: a delete of a row that is already gone shows the server
//     message and changes no file; a delete of a row removes it.
//  3. Pipelines: lone waits come before the ship block, a repo scope that has
//     only a lone wait shows the card and no empty text, the card has Dismiss
//     in its banner bar and the repo name near the branch, and Dismiss
//     removes the record file.
//  4. Review queue: the ship run has a review tile and no received-review tile
//     or section button, a row opens in the detail viewer, and the source
//     filter stays after a new snapshot.
//  5. Dismiss of the wait of repo A: a bad request shows the server message and
//     removes nothing; a good request removes the record file.
//
// With no Chrome the test fails when CI is set, else it skips.
func TestBrowserE2E(t *testing.T) {
	isolateCacheDir(t)
	chrome := browserPath(t)
	stderrBuf := captureStderr(t)
	shortTimings(t, browserSnapshotEvery, time.Second)

	now := time.Now().UTC()
	repoA, repoB := t.TempDir(), t.TempDir()
	ship := browserSeedRepoA(t, repoA, now)
	browserSeedWait(t, repoB, browserWaitB, now)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	collector := &browserCollector{}
	o := browserOptions(ln, []string{repoA, repoB}, collector)
	base := "http://" + ln.Addr().String()

	// Cleanups run last in, first out: the server stops first, then the log is
	// read, and captureStderr puts the old writer back.
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("server stderr:\n%s", stderrBuf.String())
		}
	})
	serveCtx, stopServe := context.WithCancel(context.Background())
	serveDone := make(chan int, 1)
	go func() { serveDone <- Serve(serveCtx, o) }()
	t.Cleanup(func() {
		stopServe()
		select {
		case <-serveDone:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return at cleanup")
		}
	})
	browserWaitFor(t, "GET /api/health", func() bool {
		resp, err := http.Get(base + "/api/health")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chrome),
		chromedp.NoSandbox,
		chromedp.UserDataDir(t.TempDir()),
		chromedp.WindowSize(1400, 1000),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(cancelAlloc)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelCtx)
	// Run with no step starts the browser. Later steps use a context with a limit.
	if err := chromedp.Do(ctx); err != nil {
		t.Fatalf("start Chrome: %v", err)
	}
	page := &browserPage{t: t, ctx: ctx}

	page.do("open the dashboard", chromedp.Navigate(base+"/"))
	page.wait("the first snapshot", `document.getElementById('conn').getAttribute('data-state') === 'snapshot'`)

	// Step 1: the status menu of a preplan writes the topic file.
	t.Log("step 1: preplan status")
	page.click("the Preplans tab", "#tab-preplans")
	menu := `select[data-pp-set=` + browserJSON(browserPlanSlug) + `]`
	page.wait("the status menu of the preplan", `!!document.querySelector(`+browserJSON(menu)+`)`)
	paused := tools.PreplanStatuses[2]
	// SetValue fires no change event, so the script sets the value and sends one.
	got := browserMust[string](page, "set the status", `(function () {
		var menu = document.querySelector(`+browserJSON(menu)+`);
		menu.value = `+browserJSON(paused)+`;
		menu.dispatchEvent(new Event('change', { bubbles: true }));
		return menu.value;
	})()`)
	if got != paused {
		t.Fatalf("menu value = %q; want %q", got, paused)
	}
	topic := filepath.Join(repoA, paths.DataDir, paths.PreplanSubdir, browserPlanSlug+".md")
	page.waitFor("the topic file to hold the new status", func() bool {
		b, err := os.ReadFile(topic)
		return err == nil && strings.Contains(string(b), "**Status:** "+paused)
	})

	// Step 1b: a status that the server refuses. The dialog shows the error,
	// the file keeps its status, and the menu goes back to that status.
	t.Log("step 1b: refused preplan status")
	page.listenClose("confirm-dialog")
	browserMust[bool](page, "set a status that the server refuses", `(function () {
		var menu = document.querySelector(`+browserJSON(menu)+`);
		var option = document.createElement('option');
		option.value = 'bogus';
		option.textContent = 'bogus';
		menu.appendChild(option);
		menu.value = 'bogus';
		menu.dispatchEvent(new Event('change', { bubbles: true }));
		return menu.value === 'bogus';
	})()`)
	page.wait("the status error in the dialog", `document.getElementById('confirm-dialog').open && document.getElementById('confirm-text').textContent.indexOf('is not a preplan status') !== -1`)
	page.click("Close", "#confirm-cancel")
	page.waitClosed("confirm-dialog")
	page.wait("the menu to show the status of the file again", `document.querySelector(`+browserJSON(menu)+`).value === `+browserJSON(paused))
	if b, err := os.ReadFile(topic); err != nil || !strings.Contains(string(b), "**Status:** "+paused) || strings.Contains(string(b), "bogus") {
		t.Errorf("topic file after the refused status = %q (err %v); want the status %q", b, err, paused)
	}

	// Step 1c: a click on the repo header sorts the Preplans table A to Z,
	// and the focus stays on the header after the redraw.
	t.Log("step 1c: preplan repo sort")
	page.click("the repo sort header", `button[data-pp-sort="repo"]`)
	page.wait("the repo sort", `document.querySelector('button[data-pp-sort="repo"]').closest('th').getAttribute('aria-sort') === 'ascending'`)
	page.wait("the focus on the repo header", `document.activeElement && document.activeElement.getAttribute('data-pp-sort') === 'repo'`)

	// Step 1d: the duration cells and the duration sort.
	t.Log("step 1d: preplan duration")
	row := func(slug string) string {
		return `document.querySelector('select[data-pp-set="` + slug + `"]').closest('tr')`
	}
	if got := browserMust[string](page, "the long duration", row("browser-long")+`.querySelector('td.h-dur').textContent`); !regexp.MustCompile(`^\d+h \d{2}m$`).MatchString(got) {
		t.Fatalf("long duration = %q; want <h>h <mm>m from the Started line", got)
	}
	if got := browserMust[string](page, "the empty duration", row("browser-none")+`.querySelector('td.h-dur').textContent`); got != "—" {
		t.Fatalf("empty duration = %q; want —", got)
	}
	page.click("the duration sort header", `button[data-pp-sort="duration"]`)
	page.wait("the longest row first", `document.querySelector('.pp tbody tr select[data-pp-set]').getAttribute('data-pp-set') === 'browser-long'`)
	page.wait("the duration sort", `document.querySelector('button[data-pp-sort="duration"]').closest('th').getAttribute('aria-sort') === 'descending'`)

	// Steps A1 to A3: the Preplans box of the Activity tab.
	browserCheckPreplanBox(page, repoA)

	// Step 2: History.
	t.Log("step 2: history delete")
	page.click("the History tab", "#tab-history")
	const picks = `input[data-hist-pick]`
	page.wait("four History rows", `document.querySelectorAll('`+picks+`').length === 4`)
	pick := func(branch string) string { return `input[data-hist-pick*=` + browserJSON(branch) + `]` }
	deleteEnabled := `!!document.querySelector('[data-hist-delete]') && !document.querySelector('[data-hist-delete]').disabled`

	// 2a: a row that another session already deleted. The collector is frozen,
	// so the page still shows the row when the click happens.
	collector.setFrozen(true)
	page.click("the row "+browserBranchA, pick(browserBranchA))
	browserDropRun(t, repoA, browserBranchA)
	afterDrop, err := os.ReadFile(browserRunsPath(repoA))
	if err != nil {
		t.Fatal(err)
	}
	page.wait("Delete selected to turn on", deleteEnabled)
	page.click("Delete selected", "[data-hist-delete]")
	page.wait("the confirm dialog", `document.getElementById('confirm-dialog').open`)
	page.click("Confirm", "#confirm-ok")
	page.wait("the server message in the dialog", `document.getElementById('confirm-text').textContent.indexOf('No History row matches') !== -1`)
	if msg := page.text("#confirm-text"); !strings.Contains(msg, `branch "`+browserBranchA+`"`) {
		t.Errorf("dialog text = %q; want the key of the row with branch %q", msg, browserBranchA)
	}
	if onDisk, err := os.ReadFile(browserRunsPath(repoA)); err != nil || !bytes.Equal(onDisk, afterDrop) {
		t.Errorf("runs.jsonl after the failed delete = %q (err %v); want it unchanged: %q", onDisk, err, afterDrop)
	}
	page.listenClose("confirm-dialog")
	page.click("Close", "#confirm-cancel")
	page.waitClosed("confirm-dialog")
	collector.setFrozen(false)
	page.wait("three History rows", `document.querySelectorAll('`+picks+`').length === 3`)

	// 2b: a row that exists. The file loses exactly that row.
	before := browserRunBranches(t, repoA)
	page.click("the row "+browserBranchB, pick(browserBranchB))
	page.wait("Delete selected to turn on", deleteEnabled)
	page.click("Delete selected", "[data-hist-delete]")
	page.wait("the confirm dialog", `document.getElementById('confirm-dialog').open`)
	page.listenClose("confirm-dialog")
	page.click("Confirm", "#confirm-ok")
	page.waitFor("runs.jsonl to lose one row", func() bool {
		return len(browserRunBranches(t, repoA)) == len(before)-1
	})
	for _, b := range browserRunBranches(t, repoA) {
		if b != browserBranch {
			t.Errorf("runs.jsonl branches after the delete = %v; want only %q", browserRunBranches(t, repoA), browserBranch)
			break
		}
	}
	page.waitClosed("confirm-dialog")
	page.wait("two History rows", `document.querySelectorAll('`+picks+`').length === 2`)

	// Step 3: Pipelines. The two lone waits come before the ship block.
	t.Log("step 3: lone waits and Dismiss")
	page.click("the Pipelines tab", "#tab-pipelines")
	page.wait("two lone waits before the ship block", `(function () {
		var kids = Array.prototype.slice.call(document.getElementById('feed').children);
		var block = kids.findIndex(function (k) { return k.hasAttribute('data-key'); });
		var waits = kids.map(function (k, i) { return k.hasAttribute('data-lone-wait') ? i : -1; }).filter(function (i) { return i >= 0; });
		return block >= 0 && waits.length === 2 && waits[waits.length - 1] < block;
	})()`)

	// Scope to repo B, which has a lone wait and no pipeline: the card shows and
	// the empty text does not.
	chipB := `#filter-chips [data-root=` + browserJSON(repoB) + `]`
	page.click("the chip of repo B", chipB)
	page.wait("only the wait of repo B, with no empty text", `(function () {
		var feed = document.getElementById('feed');
		var waits = feed.querySelectorAll('[data-lone-wait]');
		return waits.length === 1 &&
			waits[0].querySelector('[data-wait-dismiss]').getAttribute('data-repo') === `+browserJSON(repoB)+` &&
			feed.querySelectorAll('[data-key]').length === 0 &&
			feed.querySelectorAll('[data-empty]').length === 0;
	})()`)
	page.wait("Dismiss in the banner bar and the repo name near the branch", `(function () {
		var card = document.querySelector('#feed [data-lone-wait]');
		var button = card.querySelector('[data-wait-dismiss]');
		var branch = card.querySelector('.pipe-branch').getBoundingClientRect();
		var repo = card.querySelector('.pipe-head .pipe-repo').getBoundingClientRect();
		return !!button.closest('.attn-bar') && repo.left - branch.right < 40;
	})()`)
	page.click("Dismiss of the wait of repo B", `#feed [data-wait-dismiss=`+browserJSON(browserWaitB.key())+`]`)
	page.waitFor("the record file of repo B to go", func() bool {
		_, err := os.Stat(browserWaitB.file(repoB))
		return os.IsNotExist(err)
	})
	page.wait("the empty text of the scope", `document.getElementById('feed').querySelectorAll('[data-empty]').length === 1 && document.querySelectorAll('#feed [data-lone-wait]').length === 0`)
	if _, err := os.Stat(browserWaitA.file(repoA)); err != nil {
		t.Errorf("the record file of repo A after the dismiss in repo B: %v; want it to stay", err)
	}
	page.click("the chip of repo B", chipB)
	page.wait("the ship block and the wait of repo A", `document.querySelectorAll('#feed > [data-key]').length === 1 && document.querySelectorAll('#feed [data-lone-wait]').length === 1`)

	// Step 4: the review tile of the ship run.
	t.Log("step 4: review queue")
	shipSnap := browserSnapshotOf(t, base)
	var rrDetail bool
	for _, repo := range shipSnap.Repos {
		for _, p := range repo.Pipelines {
			for _, s := range p.Steps {
				if s.Name == "received-review" && s.Detail != nil {
					rrDetail = true
				}
			}
		}
	}
	if !rrDetail {
		t.Fatal("the snapshot has no received-review step with a detail; the page check below would prove nothing")
	}
	const reviewTile = `#feed [data-key] details[data-section="review"]`
	page.wait("the review tile with the queue", `!!document.querySelector('`+reviewTile+` .review-queue')`)
	if n := page.count(`#feed [data-section="received-review"]`); n != 0 {
		t.Errorf("elements with data-section received-review = %d; want 0 (no tile, no section button)", n)
	}
	if n := page.count(`#feed [data-key] button.station[data-section="review"]`); n != 1 {
		t.Errorf("review station buttons = %d; want 1", n)
	}
	page.wait("the received-review step as a plain station", `(function () {
		var found = Array.prototype.filter.call(document.querySelectorAll('#feed [data-key] .station'), function (s) {
			return (s.getAttribute('title') || '').indexOf('received-review') === 0;
		});
		return found.length === 1 && found[0].tagName === 'DIV' && !found[0].hasAttribute('data-station');
	})()`)

	// A queue row opens in the detail viewer.
	if !browserMust[bool](page, "tile open", `document.querySelector('`+reviewTile+`').open`) {
		page.click("the review tile", reviewTile+` > summary`)
	}
	const firstRow = `#feed button.q-row[data-detail^="queue-row:"]`
	rowTitle := page.text(firstRow + ` .fix-title`)
	if rowTitle == "" {
		t.Fatal("the first queue row has no title")
	}
	page.click("the first queue row", firstRow)
	// The viewer title is the severity and the place of the finding. The title of the row is in its text.
	page.wait("the row in the detail viewer", `document.getElementById('detail-dialog').open &&
		document.getElementById('detail-title').textContent === `+browserJSON(browserPR1Place)+` &&
		document.getElementById('detail-content').textContent.indexOf(`+browserJSON(rowTitle)+`) !== -1`)
	page.listenClose("detail-dialog")
	page.click("Close of the detail viewer", "#detail-close")
	page.waitClosed("detail-dialog")

	// The source filter stays after a new snapshot.
	prChip := `#feed button[data-q-src="pr"]`
	allChip := `#feed button[data-q-src="all"]`
	onlyPR := func(n int) string {
		return `(function () {
			var rows = document.querySelectorAll('#feed .q-row .fix-title');
			return document.querySelector('` + prChip + `').getAttribute('aria-pressed') === 'true' &&
				document.querySelector('` + allChip + `').getAttribute('aria-pressed') === 'false' &&
				document.querySelector('` + prChip + ` .chip-n').textContent === '` + fmt.Sprint(n) + `' &&
				rows.length === ` + fmt.Sprint(n) + ` &&
				Array.prototype.every.call(rows, function (r) { return r.textContent.indexOf(` + browserJSON(browserPRPrefix) + `) === 0; });
		})()`
	}
	page.click("the PR comments chip", prChip)
	page.wait("one PR comment row under the filter", onlyPR(1))
	ship.addPR(t, browserTitlePR2, time.Now())
	page.wait("two PR comment rows after the new snapshot, still under the filter", onlyPR(2))

	// Dismiss with a bad request shows the server message and removes nothing.
	t.Log("step 5: Dismiss with a bad request, then a good one")
	dismissA := `#feed [data-wait-dismiss=` + browserJSON(browserWaitA.key()) + `]`
	page.wait("the Dismiss button of repo A", `!!document.querySelector(`+browserJSON(dismissA)+`)`)
	// The kind comes from the data-wait-kind attribute, so a changed attribute is a bad request.
	browserMust[bool](page, "set a bad kind", `(function () { document.querySelector(`+browserJSON(dismissA)+`).setAttribute('data-wait-kind', 'bogus'); return true; })()`)
	page.click("Dismiss with a bad kind", dismissA)
	page.wait("the server message in the dialog", `document.getElementById('confirm-dialog').open && document.getElementById('confirm-text').textContent.indexOf('bogus') !== -1`)
	if _, err := os.Stat(browserWaitA.file(repoA)); err != nil {
		t.Errorf("the record file after the bad Dismiss: %v; want it to stay", err)
	}
	page.listenClose("confirm-dialog")
	page.click("Close", "#confirm-cancel")
	page.waitClosed("confirm-dialog")

	// A good Dismiss removes the record file and the card.
	browserMust[bool](page, "set the kind back", `(function () { document.querySelector(`+browserJSON(dismissA)+`).setAttribute('data-wait-kind', `+browserJSON(attention.KindQuestion)+`); return true; })()`)
	page.click("Dismiss", dismissA)
	page.waitFor("the record file of repo A to go", func() bool {
		_, err := os.Stat(browserWaitA.file(repoA))
		return os.IsNotExist(err)
	})
	page.wait("the card to go", `document.querySelectorAll('#feed [data-lone-wait]').length === 0`)
}

// TestBrowserE2E_HardenTile drives the real page against a snapshot that holds
// a harden step, and reads the harden tile. It has its own temp repos: repo
// repoH holds the ship run of browserSeedHardenShip, and repo repoA holds the
// seed of TestBrowserE2E, so the test also shows that a ship pipeline without
// a harden step is not changed by the tile.
//
// The tile of repoH must show the rationale of the new-form edit, the text
// for the old-form edit, the toggle of the patch, and the marker for the run
// record that cannot be read. The patch opens on a click.
//
// With no Chrome the test fails when CI is set, else it skips.
func TestBrowserE2E_HardenTile(t *testing.T) {
	isolateCacheDir(t)
	chrome := browserPath(t)
	stderrBuf := captureStderr(t)
	shortTimings(t, browserSnapshotEvery, time.Second)

	now := time.Now().UTC()
	repoH, repoA := t.TempDir(), t.TempDir()
	browserSeedHardenShip(t, repoH, now)
	browserSeedRepoA(t, repoA, now)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	o := browserOptions(ln, []string{repoA, repoH}, &browserCollector{})
	base := "http://" + ln.Addr().String()

	// Cleanups run last in, first out: the server stops first, then the log is
	// read, and captureStderr puts the old writer back.
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("server stderr:\n%s", stderrBuf.String())
		}
	})
	serveCtx, stopServe := context.WithCancel(context.Background())
	serveDone := make(chan int, 1)
	go func() { serveDone <- Serve(serveCtx, o) }()
	t.Cleanup(func() {
		stopServe()
		select {
		case <-serveDone:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return at cleanup")
		}
	})
	browserWaitFor(t, "GET /api/health", func() bool {
		resp, err := http.Get(base + "/api/health")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	// The snapshot must hold the harden detail, else the page checks below prove nothing.
	var harden *tools.DashboardHarden
	for _, repo := range browserSnapshotOf(t, base).Repos {
		if repo.Root != repoH {
			continue
		}
		for _, p := range repo.Pipelines {
			for _, s := range p.Steps {
				if s.Name == "harden" && s.Detail != nil {
					harden = s.Detail.Harden
				}
			}
		}
	}
	if harden == nil {
		t.Fatal("the snapshot has no harden step with a detail for the harden repo; the page checks below would prove nothing")
	}
	if len(harden.Edits) != 2 || len(harden.Clusters) != 1 || harden.Malformed != 1 {
		t.Fatalf("snapshot harden: %d edits, %d run records, %d malformed; want 2, 1 and 1", len(harden.Edits), len(harden.Clusters), harden.Malformed)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chrome),
		chromedp.NoSandbox,
		chromedp.UserDataDir(t.TempDir()),
		chromedp.WindowSize(1400, 1000),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(cancelAlloc)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelCtx)
	// Run with no step starts the browser. Later steps use a context with a limit.
	if err := chromedp.Do(ctx); err != nil {
		t.Fatalf("start Chrome: %v", err)
	}
	page := &browserPage{t: t, ctx: ctx}

	page.do("open the dashboard", chromedp.Navigate(base+"/"))
	page.wait("the first snapshot", `document.getElementById('conn').getAttribute('data-state') === 'snapshot'`)
	page.click("the Pipelines tab", "#tab-pipelines")

	// inBlock wraps body in a function that finds the pipeline block of the repo
	// with the given root. The body returns false when the block is not there yet.
	inBlock := func(root, body string) string {
		return `(function () {
			var block = Array.prototype.filter.call(document.querySelectorAll('#feed > [data-key]'), function (b) {
				return b.getAttribute('data-key').indexOf(` + browserJSON(root+"\n") + `) === 0;
			})[0];
			if (!block) return false;
			` + body + `
		})()`
	}
	const hardenTile = `details[data-section="harden"]`
	page.wait("the harden tile in the pipeline of the harden repo", inBlock(repoH, `
		var tile = block.querySelector('`+hardenTile+`');
		return !!tile && tile.open && !!tile.querySelector('.hd-edit');`))

	var tile struct {
		Whys    []string `json:"whys"`    // the .hd-why text of each edit, in page order
		Patches []string `json:"patches"` // the summary text of each patch toggle
		Bad     []string `json:"bad"`     // the text of each marker for a record that cannot be read
		Edits   int      `json:"edits"`
	}
	raw := browserMust[string](page, "read the harden tile", inBlock(repoH, `
		var tile = block.querySelector('`+hardenTile+`');
		var texts = function (sel) {
			return Array.prototype.map.call(tile.querySelectorAll(sel), function (n) { return n.textContent; });
		};
		return JSON.stringify({
			whys: texts('.hd-why'),
			patches: texts('details.hd-patch > summary'),
			bad: texts('.hd-bad'),
			edits: tile.querySelectorAll('.hd-edits .hd-edit').length
		});`))
	if err := json.Unmarshal([]byte(raw), &tile); err != nil {
		t.Fatalf("decode the harden tile %q: %v", raw, err)
	}
	if tile.Edits != 2 {
		t.Errorf("edits in the harden tile = %d; want 2 (the new-form and the old-form edit)", tile.Edits)
	}
	if !slices.Equal(tile.Whys, []string{browserHardenRationale, browserHardenNoWhy}) {
		t.Errorf("rationale texts = %q; want the rationale %q, then %q", tile.Whys, browserHardenRationale, browserHardenNoWhy)
	}
	if len(tile.Patches) != 1 || !strings.HasPrefix(tile.Patches[0], "patch") {
		t.Errorf("patch toggles = %q; want one toggle that starts with \"patch\" (the old-form edit has none)", tile.Patches)
	}
	if !slices.Equal(tile.Bad, []string{browserHardenBad}) {
		t.Errorf("malformed markers = %q; want %q", tile.Bad, browserHardenBad)
	}

	// The patch is behind the toggle. A click opens it and shows the diff.
	const patchToggle = `#feed [data-key] ` + hardenTile + ` details.hd-patch > summary`
	page.click("the patch toggle", patchToggle)
	page.wait("the patch to open", inBlock(repoH, `
		var patch = block.querySelector('`+hardenTile+` details.hd-patch');
		return !!patch && patch.open && patch.querySelector('.hd-diff').textContent === `+browserJSON(browserHardenPatch)+`;`))

	// The pipeline of the other repo still shows its steps and has no harden tile.
	page.wait("the steps of the ship pipeline of repo A", inBlock(repoA, `
		var names = Array.prototype.map.call(block.querySelectorAll('.station'), function (s) {
			return (s.getAttribute('title') || '').split(',')[0];
		});
		return ['execute', 'commit', 'review'].every(function (n) { return names.indexOf(n) !== -1; }) &&
			!!block.querySelector('details[data-section="review"]') &&
			block.querySelectorAll('`+hardenTile+`').length === 0;`))
}
