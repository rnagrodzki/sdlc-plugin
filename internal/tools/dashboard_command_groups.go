package tools

import (
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

// dashboardCommandOtherLabel is the label of the group for commands that run
// no program: an empty command, or one that holds only "VAR=x" words.
const dashboardCommandOtherLabel = "(other)"

// commandPrograms returns the programs that cmd runs, in order of first use,
// without repeats. Only the first line that is not blank or a comment counts,
// so the body of a heredoc is not read as a command. The line is split at
// "|", "&&", "||" and ";" outside single and double quotes. In each part, the
// words "VAR=x" and "sudo" are skipped, and the next word is the program,
// kept as written. It returns nil when cmd runs no program.
func commandPrograms(cmd string) []string {
	var programs []string
	for _, words := range commandSegments(commandFirstLine(cmd)) {
		for _, w := range words {
			if w == "sudo" || shipReportEnvAssignRe.MatchString(w) {
				continue
			}
			if !slices.Contains(programs, w) {
				programs = append(programs, w)
			}
			break
		}
	}
	return programs
}

// commandFirstLine returns the first line of cmd that is not blank and does
// not start with "#", with the space around it trimmed. It returns "" when
// there is no such line.
func commandFirstLine(cmd string) string {
	for rest := cmd; rest != ""; {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

// commandSegments splits line at "|", "&&", "||" and ";" outside single and
// double quotes, and returns the words of each part with the quotes removed.
// A backslash outside single quotes escapes the next character. A "#" at the
// start of a word outside quotes starts a comment that ends the line. A part
// with no word is dropped.
func commandSegments(line string) [][]string {
	var (
		segments [][]string
		words    []string
		cur      strings.Builder
		inWord   bool
		quote    rune
	)
	flushWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	endSegment := func() {
		flushWord()
		if len(words) > 0 {
			segments = append(segments, words)
			words = nil
		}
	}

	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '\\' && i+1 < len(rs):
				i++
				cur.WriteRune(rs[i])
			case r == '"':
				quote = 0
			default:
				cur.WriteRune(r)
			}
		case r == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inWord = true
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case unicode.IsSpace(r):
			flushWord()
		case r == '#' && !inWord:
			endSegment()
			return segments
		case r == '|' || r == ';':
			endSegment()
		case r == '&' && i+1 < len(rs) && rs[i+1] == '&':
			endSegment()
			i++
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	endSegment()
	return segments
}

// commandGroupLabel returns the group label for a command that runs
// programs, and the programs that the label names. A command with no program
// is "(other)" and names none. A command with one or two programs groups
// under the first. A command with three or more programs groups under all of
// them, joined with " + ". The names pass through dashboardPreview, the same
// redaction and cut that the timeline text gets.
func commandGroupLabel(programs []string) (string, []string) {
	if len(programs) == 0 {
		return dashboardCommandOtherLabel, []string{}
	}
	named := programs[:1]
	if len(programs) >= 3 {
		named = programs
	}
	out := make([]string, len(named))
	for i, p := range named {
		out[i] = dashboardPreview(p)
	}
	return strings.Join(out, " + "), out
}

// groupSessionCommands groups the "command" events of one session by program
// label. It reads the full command of each event, so a caller passes all
// events of the session, not the capped timeline. The result is sorted by
// count, then by newest command, then by label, and its first group is the
// majority. It returns an empty list, never nil, when there is no command.
func groupSessionCommands(events []dashboardEvent) []DashboardCommandGroup {
	type agg struct {
		group DashboardCommandGroup
		last  time.Time
	}
	byLabel := map[string]*agg{}
	var order []*agg
	total := 0
	for _, e := range events {
		if e.kind != "command" {
			continue
		}
		total++
		label, programs := commandGroupLabel(commandPrograms(e.command))
		a, ok := byLabel[label]
		if !ok {
			a = &agg{group: DashboardCommandGroup{Label: label, Programs: programs}}
			byLabel[label] = a
			order = append(order, a)
		}
		a.group.Count++
		if e.at.After(a.last) {
			a.last = e.at
		}
	}

	groups := make([]DashboardCommandGroup, 0, len(order))
	if total == 0 {
		return groups
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.group.Count != b.group.Count {
			return a.group.Count > b.group.Count
		}
		if !a.last.Equal(b.last) {
			return a.last.After(b.last)
		}
		return a.group.Label < b.group.Label
	})
	for _, a := range order {
		a.group.Share = math.Round(float64(a.group.Count)/float64(total)*100) / 100
		a.group.LastAt = dashboardFormatTime(a.last)
		groups = append(groups, a.group)
	}
	groups[0].Majority = true
	return groups
}
