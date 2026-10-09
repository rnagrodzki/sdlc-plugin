package tools

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// dashCommandEvent returns a "command" dashboardEvent that ran cmd at the
// given number of minutes before dashNow.
func dashCommandEvent(minutesAgo int, cmd string) dashboardEvent {
	return dashboardEvent{
		sessionID: "s1",
		at:        dashNow.Add(-time.Duration(minutesAgo) * time.Minute),
		kind:      "command",
		text:      cmd,
		command:   cmd,
	}
}

// dashCommandGroupLabels returns the label of each group, in order.
func dashCommandGroupLabels(groups []DashboardCommandGroup) []string {
	out := make([]string, len(groups))
	for i, g := range groups {
		out[i] = g.Label
	}
	return out
}

// TestCommandPrograms_Table pins the programs that commandPrograms reads from
// each command: the split rules, the quote rules, the skipped words, and the
// commands that run no program.
func TestCommandPrograms_Table(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want []string
	}{
		{"and list repeats one program", "git add . && git commit", []string{"git"}},
		{"git status and git diff", "git status && git diff", []string{"git"}},
		{"semicolon inside double quotes", `git commit -m "a; b"`, []string{"git"}},
		{"operators inside single quotes", `git commit -m 'a | b && c || d'`, []string{"git"}},
		{"escaped quote inside double quotes", `git commit -m "say \"hi\"; rm"`, []string{"git"}},
		{"escaped semicolon outside quotes", `echo a\;b`, []string{"echo"}},
		{"heredoc body is not a command", "cat <<EOF\nrm -rf x\nEOF", []string{"cat"}},
		{"only the first line counts", "git status\nrm -rf x", []string{"git"}},
		{"carriage return line end", "git status\r\nrm x", []string{"git"}},
		{"leading comment line is skipped", "# check the tree\ngit status", []string{"git"}},
		{"trailing comment ends the line", "git status # && rm x", []string{"git"}},
		{"sudo and assignment are skipped", "sudo FOO=1 make", []string{"make"}},
		{"assignment then program", "FOO=1 go test ./...", []string{"go"}},
		{"assignment with a quoted space", `FOO="a b" go test`, []string{"go"}},
		{"operator inside an assignment value", `FOO="a|b" make`, []string{"make"}},
		{"two programs", "go build && ./bin", []string{"go", "./bin"}},
		{"three programs of a pipe", "cat a | grep x | wc", []string{"cat", "grep", "wc"}},
		{"three programs with a flag", "cat a | grep x | wc -l", []string{"cat", "grep", "wc"}},
		{"repeats collapse to the first use", "cat a | cat b | grep x", []string{"cat", "grep"}},
		{"or list and semicolon", "a || b ; c", []string{"a", "b", "c"}},
		{"empty parts are dropped", "a;;b;", []string{"a", "b"}},
		{"redirect with ampersand is no split", "ls 2>&1 | head", []string{"ls", "head"}},
		{"quoted program name", `"git" status`, []string{"git"}},
		{"unterminated quote", `git commit -m "a; rm`, []string{"git"}},
		{"empty command", "", nil},
		{"blank command", "  \n\t ", nil},
		{"assignment only", "FOO=1", nil},
		{"two assignments only", "FOO=1 BAR=2", nil},
		{"sudo only", "sudo", nil},
		{"comment only", "# nothing", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := commandPrograms(tc.cmd)
			if !slices.Equal(got, tc.want) {
				t.Errorf("commandPrograms(%q) = %q, want %q", tc.cmd, got, tc.want)
			}
		})
	}
}

// TestGroupSessionCommands_Labels checks the label and the named programs of
// a group for each rule: one program, two programs, three programs, an
// assignment before a program, and no program.
func TestGroupSessionCommands_Labels(t *testing.T) {
	cases := []struct {
		name         string
		cmd          string
		wantLabel    string
		wantPrograms []string
	}{
		{"git status and git diff", "git status && git diff", "git", []string{"git"}},
		{"three programs", "cat a | grep x | wc -l", "cat + grep + wc", []string{"cat", "grep", "wc"}},
		{"assignment before go", "FOO=1 go test ./...", "go", []string{"go"}},
		{"two programs group under the first", "go build && ./bin", "go", []string{"go"}},
		{"empty command", "", "(other)", []string{}},
		{"assignment only", "FOO=1", "(other)", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			groups := groupSessionCommands([]dashboardEvent{dashCommandEvent(1, tc.cmd)})
			if len(groups) != 1 {
				t.Fatalf("groups = %+v, want one group", groups)
			}
			g := groups[0]
			if g.Label != tc.wantLabel {
				t.Errorf("label = %q, want %q", g.Label, tc.wantLabel)
			}
			if g.Programs == nil || !slices.Equal(g.Programs, tc.wantPrograms) {
				t.Errorf("programs = %#v, want %#v (non-nil)", g.Programs, tc.wantPrograms)
			}
			if g.Count != 1 || g.Share != 1 || !g.Majority {
				t.Errorf("group = %+v, want count 1, share 1, majority", g)
			}
		})
	}

	t.Run("one and two program commands share a group", func(t *testing.T) {
		groups := groupSessionCommands([]dashboardEvent{
			dashCommandEvent(2, "go test ./..."),
			dashCommandEvent(1, "go build && ./bin"),
		})
		if len(groups) != 1 || groups[0].Label != "go" || groups[0].Count != 2 {
			t.Fatalf("groups = %+v, want one group go with count 2", groups)
		}
		if !slices.Equal(groups[0].Programs, []string{"go"}) {
			t.Errorf("programs = %v, want [go]", groups[0].Programs)
		}
	})
}

// TestGroupSessionCommands_ShareAndMajority checks the order of the groups,
// the share rounded to 2 decimals, and that only the largest group is marked
// as the majority.
func TestGroupSessionCommands_ShareAndMajority(t *testing.T) {
	events := []dashboardEvent{
		dashCommandEvent(60, "git status"),
		dashCommandEvent(50, "go test"),
		dashCommandEvent(40, "git diff"),
		dashCommandEvent(30, "make"),
		dashCommandEvent(20, "go build"),
		dashCommandEvent(10, "git log"),
	}
	groups := groupSessionCommands(events)

	if got, want := dashCommandGroupLabels(groups), []string{"git", "go", "make"}; !slices.Equal(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	want := []struct {
		count    int
		share    float64
		majority bool
		lastAt   string
	}{
		{3, 0.5, true, "2026-10-07T09:50:00Z"},
		{2, 0.33, false, "2026-10-07T09:40:00Z"},
		{1, 0.17, false, "2026-10-07T09:30:00Z"},
	}
	for i, w := range want {
		g := groups[i]
		if g.Count != w.count || g.Share != w.share || g.Majority != w.majority || g.LastAt != w.lastAt {
			t.Errorf("group %d = %+v, want count %d share %v majority %v lastAt %s", i, g, w.count, w.share, w.majority, w.lastAt)
		}
	}
}

// TestGroupSessionCommands_TieGoesToNewest checks that two groups with the
// same count are ordered by their newest command, and that the newest gets
// the majority mark. A tie in the time too is ordered by label.
func TestGroupSessionCommands_TieGoesToNewest(t *testing.T) {
	t.Run("the group with the newest command stays first", func(t *testing.T) {
		groups := groupSessionCommands([]dashboardEvent{
			dashCommandEvent(50, "git status"),
			dashCommandEvent(10, "git diff"),
			dashCommandEvent(40, "go build"),
			dashCommandEvent(20, "go test"),
		})
		if got, want := dashCommandGroupLabels(groups), []string{"git", "go"}; !slices.Equal(got, want) {
			t.Fatalf("labels = %v, want %v", got, want)
		}
		if !groups[0].Majority || groups[1].Majority {
			t.Errorf("majority = %v/%v, want true/false", groups[0].Majority, groups[1].Majority)
		}
	})

	t.Run("a newer second group moves first", func(t *testing.T) {
		groups := groupSessionCommands([]dashboardEvent{
			dashCommandEvent(50, "git status"),
			dashCommandEvent(40, "git diff"),
			dashCommandEvent(30, "go build"),
			dashCommandEvent(5, "go test"),
		})
		if got, want := dashCommandGroupLabels(groups), []string{"go", "git"}; !slices.Equal(got, want) {
			t.Fatalf("labels = %v, want %v", got, want)
		}
		if !groups[0].Majority || groups[1].Majority {
			t.Errorf("majority = %v/%v, want true/false", groups[0].Majority, groups[1].Majority)
		}
		if groups[0].Share != 0.5 || groups[1].Share != 0.5 {
			t.Errorf("shares = %v/%v, want 0.5/0.5", groups[0].Share, groups[1].Share)
		}
	})

	t.Run("same time orders by label", func(t *testing.T) {
		groups := groupSessionCommands([]dashboardEvent{
			dashCommandEvent(5, "zsh -c x"),
			dashCommandEvent(5, "awk x"),
		})
		if got, want := dashCommandGroupLabels(groups), []string{"awk", "zsh"}; !slices.Equal(got, want) {
			t.Fatalf("labels = %v, want %v", got, want)
		}
		if !groups[0].Majority || groups[1].Majority {
			t.Errorf("majority = %v/%v, want true/false", groups[0].Majority, groups[1].Majority)
		}
	})
}

// TestGroupSessionCommands_NoCommands checks that a session without any
// command gets an empty list that is not nil and that marshals as [].
func TestGroupSessionCommands_NoCommands(t *testing.T) {
	for name, events := range map[string][]dashboardEvent{
		"nil events": nil,
		"prompt and mcp only": {
			{sessionID: "s1", at: dashNow, kind: "prompt", text: "hello", command: "git status"},
			{sessionID: "s1", at: dashNow, kind: "mcp", text: "execute_state"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			groups := groupSessionCommands(events)
			if groups == nil || len(groups) != 0 {
				t.Fatalf("groups = %#v, want an empty non-nil list", groups)
			}
			b, err := json.Marshal(groups)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != "[]" {
				t.Errorf("JSON = %s, want []", b)
			}
		})
	}
}

// TestGroupSessionCommands_IgnoresOtherKinds checks that only "command" events
// count, also in the total that the share divides by.
func TestGroupSessionCommands_IgnoresOtherKinds(t *testing.T) {
	groups := groupSessionCommands([]dashboardEvent{
		dashCommandEvent(3, "git status"),
		{sessionID: "s1", at: dashNow, kind: "prompt", text: "go test", command: "go test"},
		{sessionID: "s1", at: dashNow, kind: "mcp", text: "go test"},
	})
	if len(groups) != 1 || groups[0].Label != "git" || groups[0].Count != 1 || groups[0].Share != 1 {
		t.Errorf("groups = %+v, want one group git with count 1 and share 1", groups)
	}
}

// TestGroupSessionCommands_RedactsProgramNames checks that a program name
// passes through the same redaction as the timeline text.
func TestGroupSessionCommands_RedactsProgramNames(t *testing.T) {
	secret := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdef"
	groups := groupSessionCommands([]dashboardEvent{dashCommandEvent(1, secret+" --flag")})
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want one group", groups)
	}
	g := groups[0]
	if strings.Contains(g.Label, "eyJhbGci") || strings.Contains(strings.Join(g.Programs, " "), "eyJhbGci") {
		t.Errorf("group = %+v, want the JWT redacted", g)
	}
	if !strings.Contains(g.Label, "REDACTED") {
		t.Errorf("label = %q, want the redactor's replacement", g.Label)
	}
}

// TestDashboardSessionCommandGroups_CountEveryCommand checks that the session
// build counts all commands of the session, not only the newest 50 events of
// the timeline: 40 old git commands and 20 new go commands give 40 and 20, not
// the 30 and 20 that the kept events would give.
func TestDashboardSessionCommandGroups_CountEveryCommand(t *testing.T) {
	root := dashRoot(t)
	lines := make([]string, 60)
	for i := 0; i < 60; i++ {
		cmd := fmt.Sprintf("git log -%d", i)
		if i >= 40 {
			cmd = fmt.Sprintf("go test -count=%d", i)
		}
		ts := dashNow.Add(-time.Duration(60-i) * time.Minute).Format(time.RFC3339)
		b, _ := json.Marshal(cmd)
		lines[i] = fmt.Sprintf(`{"ts":"%s","branch":"feat/x","command":%s,"sessionId":"s1"}`, ts, b)
	}
	dashWriteJSONL(t, cliEvidencePath(root), lines...)

	sessions := dashboardSessionsFromEvidence(root, dashNow)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	s := sessions[0]
	if len(s.Timeline) != dashboardSessionTimelineMax || s.Counts.Commands != 60 {
		t.Fatalf("timeline = %d, commands = %d, want %d and 60", len(s.Timeline), s.Counts.Commands, dashboardSessionTimelineMax)
	}
	if len(s.CommandGroups) != 2 {
		t.Fatalf("groups = %+v, want 2", s.CommandGroups)
	}
	git, goG := s.CommandGroups[0], s.CommandGroups[1]
	if git.Label != "git" || git.Count != 40 || git.Share != 0.67 || !git.Majority {
		t.Errorf("git group = %+v, want git 40 0.67 majority", git)
	}
	if goG.Label != "go" || goG.Count != 20 || goG.Share != 0.33 || goG.Majority {
		t.Errorf("go group = %+v, want go 20 0.33 no majority", goG)
	}
	if want := dashNow.Add(-time.Minute).Format(time.RFC3339); goG.LastAt != want {
		t.Errorf("go lastAt = %q, want %q", goG.LastAt, want)
	}
}

// TestDashboardSessionCommandGroups_ReadFullCommand checks that the parser
// reads the full command and not the 120-rune timeline preview: the second
// and third program sit after the cut.
func TestDashboardSessionCommandGroups_ReadFullCommand(t *testing.T) {
	root := dashRoot(t)
	cmd := "echo " + strings.Repeat("x", 150) + " | grep y | wc -l"
	b, _ := json.Marshal(cmd)
	dashWriteJSONL(t, cliEvidencePath(root),
		fmt.Sprintf(`{"ts":"2026-10-07T09:00:00Z","branch":"feat/x","command":%s,"sessionId":"s1"}`, b))

	sessions := dashboardSessionsFromEvidence(root, dashNow)
	if len(sessions) != 1 || len(sessions[0].CommandGroups) != 1 {
		t.Fatalf("sessions = %+v, want one session with one group", sessions)
	}
	g := sessions[0].CommandGroups[0]
	if g.Label != "echo + grep + wc" || !slices.Equal(g.Programs, []string{"echo", "grep", "wc"}) {
		t.Errorf("group = %+v, want echo + grep + wc", g)
	}
	if text := sessions[0].Timeline[0].Text; strings.Contains(text, "grep") {
		t.Errorf("timeline text = %q, want the 120-rune preview without the tail", text)
	}
}

// TestDashboardSessionCommandGroups_JSON checks the JSON of a session: the
// group keys and their order, the share as a number, and [] for a session
// with no command.
func TestDashboardSessionCommandGroups_JSON(t *testing.T) {
	root := dashRoot(t)
	dashWriteJSONL(t, cliEvidencePath(root),
		`{"ts":"2026-10-07T09:00:00Z","branch":"feat/x","command":"go test ./...","sessionId":"with-command"}`)
	dashWriteJSONL(t, userInputPath(root),
		`{"ts":"2026-10-07T09:10:00Z","text":"hello","sessionId":"prompt-only","kind":"prompt"}`)

	byID := map[string]string{}
	for _, s := range dashboardSessionsFromEvidence(root, dashNow) {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		byID[s.ID] = string(b)
	}

	wantGroup := `"commandGroups":[{"label":"go","programs":["go"],"count":1,"share":1,"majority":true,"lastAt":"2026-10-07T09:00:00Z"}]`
	if !strings.Contains(byID["with-command"], wantGroup) {
		t.Errorf("session JSON = %s, want it to contain %s", byID["with-command"], wantGroup)
	}
	if !strings.Contains(byID["prompt-only"], `"commandGroups":[]`) {
		t.Errorf("session JSON = %s, want commandGroups []", byID["prompt-only"])
	}
	if strings.Contains(byID["with-command"], `"command":`) {
		t.Errorf("session JSON = %s, want no raw command field", byID["with-command"])
	}
}

// TestDashboardSessionCommandGroups_FixtureMatchesTimeline checks that the
// command groups of every session in the shared snapshot fixture are the
// groups that groupSessionCommands builds from the command events of the
// timeline of the session, so the fixture does not drift from the grouping.
func TestDashboardSessionCommandGroups_FixtureMatchesTimeline(t *testing.T) {
	snap := dashboardFixtureDecode(t, dashboardFixtureRead(t))
	checked, withGroups := 0, 0
	for _, repo := range snap.Repos {
		for _, s := range repo.Sessions {
			var events []dashboardEvent
			for _, e := range s.Timeline {
				at, ok := dashboardParseTime(e.At)
				if !ok {
					t.Fatalf("%s %s: timeline time %q does not parse", repo.Name, s.ID, e.At)
				}
				events = append(events, dashboardEvent{sessionID: s.ID, at: at, kind: e.Kind, text: e.Text, command: e.Text})
			}
			want := groupSessionCommands(events)
			if len(want) != len(s.CommandGroups) {
				t.Errorf("%s %s: fixture has %d groups, want %d", repo.Name, s.ID, len(s.CommandGroups), len(want))
				continue
			}
			for i := range want {
				if want[i].Label != s.CommandGroups[i].Label || want[i].Count != s.CommandGroups[i].Count ||
					want[i].Share != s.CommandGroups[i].Share || want[i].Majority != s.CommandGroups[i].Majority ||
					want[i].LastAt != s.CommandGroups[i].LastAt || !slices.Equal(want[i].Programs, s.CommandGroups[i].Programs) {
					t.Errorf("%s %s: group %d = %+v, want %+v", repo.Name, s.ID, i, s.CommandGroups[i], want[i])
				}
			}
			if s.Counts.Commands != 0 && len(want) == 0 {
				t.Errorf("%s %s: counts hold commands but the timeline holds none", repo.Name, s.ID)
			}
			if len(s.CommandGroups) > 0 {
				withGroups++
			}
			checked++
		}
	}
	if checked == 0 {
		t.Error("fixture holds no session")
	}
	if withGroups == 0 {
		t.Error("fixture holds no session with command groups")
	}
}
