// validate action plan_gates.
//
// plan_gates answers the plan gates that are counts and lookups, so no
// sub-agent has to compute them: G3 (dependency order), G4 (two tasks modify
// one path with no dependency path between them), G8 (Verify field), G9 (task
// size), G10 (Modify paths exist), G12 (dependency targets exist), plus the
// G18 floor (a Contract with no code block, table or diagram). It reads the
// plan file and stats each Modify path, both against the root validateRoot
// returns (the ACTIVE worktree). It reads no config and writes nothing.

package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/rnagrodzki/sdlc-plugin/internal/discovery"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// planGatesGateIDs is the one list of gates that plan_gates owns. plan_prepare
// returns it in gateChecks. G18 is not in the list: the content-coverage lane
// owns G18, but plan_gates also reports the G18 floor (finding id G18) outside
// these gate IDs.
var planGatesGateIDs = []string{"G3", "G4", "G8", "G9", "G10", "G12"}

const (
	planGatesMaxTaskFiles    = 5   // G9: more than 5 distinct paths is a finding
	planGatesMaxTrivialShare = 0.8 // G9: a share above 0.8 is a finding
)

// taskFile is one bullet of a task's **Files:** block.
type taskFile struct{ Kind, Path string } // Kind: Create | Modify | Test, or "" for another word

// slashPathRe matches an inline-code path that holds a slash but has no
// extension, such as a folder ("internal/dir/") or "./Makefile". A plain
// inline-code word with neither a slash nor an extension ("Foo") is not a path.
var slashPathRe = regexp.MustCompile("`([a-zA-Z0-9_\\-.]*/[a-zA-Z0-9_\\-./]*)`")

// taskFileShape is the bullet shape that parseTaskFiles reads, for messages.
const taskFileShape = "- Modify: `path/to/file.go` (a file with no extension: `./Makefile`)"

// parseTaskFiles reads a task's **Files:** block. The block runs from the
// **Files:** line to the first "\n**", "\n### ", "\n---" or "\n## ". found is
// false when the body has no **Files:** line.
//
// Each bullet gives one taskFile: Kind is the word before the bullet's first
// colon when it is Create, Modify or Test (else ""). Path is the first
// inline-code path with an extension (backtickPathRe); when the bullet has
// none, it is the first inline-code path with a slash (slashPathRe). The
// extension path wins, so a "— what changes" note with a slash word ("the
// `read/write` helper in `a.go`") still yields a.go. Path is cleaned with
// filepath.Clean, so "./a.go" and "a.go" are one path.
//
// A Create, Modify or Test bullet with no path is not a taskFile: its text is
// returned in unparsed, so plan_gates can report it. Other bullets with no
// path are skipped.
//
// waveComputeParseFiles (execute_waves.go) also calls it, so scheduling and
// plan_gates read the same paths.
func parseTaskFiles(body string) (files []taskFile, unparsed []string, found bool) {
	block, found := extractDelimitedBlock(body, psFilesBlockStartRe, []string{"\n**", "\n### ", "\n---", "\n## "})
	if !found {
		return nil, nil, false
	}
	for _, m := range psFileLineBulletRe.FindAllStringSubmatch(block, -1) {
		text := m[1]
		kind := taskFileKind(text)
		pm := backtickPathRe.FindStringSubmatch(text)
		if pm == nil {
			pm = slashPathRe.FindStringSubmatch(text)
		}
		if pm == nil {
			if kind != "" {
				unparsed = append(unparsed, strings.TrimSpace(text))
			}
			continue
		}
		files = append(files, taskFile{Kind: kind, Path: filepath.Clean(pm[1])})
	}
	return files, unparsed, true
}

// taskFileKind returns the word before the first colon of a Files bullet
// when it is Create, Modify or Test, else "".
func taskFileKind(bullet string) string {
	idx := strings.Index(bullet, ":")
	if idx < 0 {
		return ""
	}
	switch word := strings.TrimSpace(bullet[:idx]); word {
	case "Create", "Modify", "Test":
		return word
	}
	return ""
}

// rawTaskBodies splits content into the raw (fences kept) body of each task,
// keyed by task number. A body starts at its "### Task N:" heading and ends at
// the next task heading or the next "## " heading, or at the end of content.
// Headings inside a column-0 fence do not count: fenceLines is the rule
// stripFences uses, so the task set matches extractTasks. Unlike extractTasks,
// the last body stops at a "## " section such as "## Verification Scorecard".
// A repeated task number keeps its first body.
func rawTaskBodies(content string) map[int]string {
	lines := strings.Split(content, "\n")
	inFence := fenceLines(lines)

	bodies := map[int]string{}
	cur, start := -1, 0
	flush := func(end int) {
		if cur < 0 {
			return
		}
		if _, ok := bodies[cur]; !ok {
			bodies[cur] = strings.Join(lines[start:end], "\n")
		}
		cur = -1
	}
	for i, line := range lines {
		if inFence[i] {
			continue
		}
		if m := taskHeaderRe.FindStringSubmatch(line); m != nil {
			flush(i)
			cur, _ = strconv.Atoi(m[1])
			start = i
			continue
		}
		if strings.HasPrefix(line, "## ") {
			flush(i)
		}
	}
	flush(len(lines))
	return bodies
}

var (
	// anyFenceLineRe is a fence line at any indent: a Contract fence often
	// sits under a list bullet. fenceOpenRe (validators.go) matches column 0
	// only, so it misses such a fence.
	anyFenceLineRe = regexp.MustCompile("^[ \\t]*```")
	// tableSeparatorRe is the start of a Markdown table separator line at any
	// indent. psTableSepRowRe and psHeaderSepRe (plan_support.go) need the
	// separator at column 0 (psTableSepRowRe also needs the whole line to
	// match), so they miss a table under a list bullet.
	tableSeparatorRe = regexp.MustCompile(`^[ \t]*\|[ \t]*:?-{3,}`)
)

// contractBlock returns the lines of the raw body's **Contract:** block: from
// the column-0 **Contract:** line outside a fence to the next column-0
// "**<Word>:**" line outside a fence, or the end of the body. found is false
// when the body has no **Contract:** line. snapshotPlan (plan_support.go) has
// its own Contract extractor for the material-change compare, but it reads
// the fence-stripped body of extractTasks, where the code blocks this check
// looks for are already gone. This one reads the raw body and skips a
// "**Word:**" line inside a fence, so such a line does not end the block.
func contractBlock(rawBody string) (block []string, found bool) {
	inFence := false
	for _, line := range strings.Split(rawBody, "\n") {
		isFence := anyFenceLineRe.MatchString(line)
		switch {
		case found && !inFence && !isFence && fieldLabelRe.MatchString(line):
			return block, true
		case found:
			block = append(block, line)
		case !inFence && strings.HasPrefix(line, "**Contract:**"):
			found = true
		}
		if isFence {
			inFence = !inFence
		}
	}
	return block, found
}

// contractHasVisual reports whether the raw body's **Contract:** block holds a
// fence line (three backticks at any indent, which also covers a mermaid
// diagram) or a table separator line ("|---" at any indent). A body with no
// **Contract:** block returns false.
func contractHasVisual(rawBody string) bool {
	block, _ := contractBlock(rawBody)
	for _, line := range block {
		if anyFenceLineRe.MatchString(line) || tableSeparatorRe.MatchString(line) {
			return true
		}
	}
	return false
}

// taskDependsOnPath reports whether a **Depends on** chain leads from task
// from to task to (from depends on to, directly or through other tasks).
func taskDependsOnPath(deps map[int][]int, from, to int) bool {
	seen := map[int]bool{from: true}
	queue := []int{from}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, d := range deps[n] {
			if d == to {
				return true
			}
			if !seen[d] {
				seen[d] = true
				queue = append(queue, d)
			}
		}
	}
	return false
}

// planGateFinding is one finding before it is sorted and converted.
type planGateFinding struct {
	gate     string
	task     int // 0 for a plan-level finding
	msg      string
	severity string // "" for the gate's own severity (planGateSeverity)
}

// planGateSeverity: G8 and G9 are warnings, every other gate is an error. A
// finding added with warn is a warning whatever its gate.
func planGateSeverity(gate string) string {
	if gate == "G8" || gate == "G9" {
		return "warning"
	}
	return "error"
}

func planGateNumber(gate string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(gate, "G"))
	return n
}

// planGateCollector gathers the findings of one plan_gates run before they are
// sorted and converted.
type planGateCollector struct{ out []planGateFinding }

func (c *planGateCollector) add(gate string, task int, format string, args ...any) {
	c.out = append(c.out, planGateFinding{gate: gate, task: task, msg: fmt.Sprintf(format, args...)})
}

// warn adds a finding with severity warning, whatever the gate's own severity.
func (c *planGateCollector) warn(gate string, task int, format string, args ...any) {
	c.out = append(c.out, planGateFinding{gate: gate, task: task, msg: fmt.Sprintf(format, args...), severity: "warning"})
}

// findings sorts the collected findings by gate number, then task number, then
// message, and converts them to discovery findings for the plan at filePath.
func (c *planGateCollector) findings(filePath string) []discovery.Finding {
	sort.SliceStable(c.out, func(a, b int) bool {
		if ga, gb := planGateNumber(c.out[a].gate), planGateNumber(c.out[b].gate); ga != gb {
			return ga < gb
		}
		if c.out[a].task != c.out[b].task {
			return c.out[a].task < c.out[b].task
		}
		return c.out[a].msg < c.out[b].msg
	})
	findings := make([]discovery.Finding, 0, len(c.out))
	for _, f := range c.out {
		sev := f.severity
		if sev == "" {
			sev = planGateSeverity(f.gate)
		}
		findings = append(findings, discovery.Finding{ID: f.gate, Severity: sev, Message: f.msg, Path: filePath})
	}
	return findings
}

// planGatesPlan is the parsed plan that every gate check reads. The slices
// refs, files, unparsed and hasFiles run parallel to tasks.
type planGatesPlan struct {
	root     string
	content  string
	tasks    []planTask
	pos      map[int]int   // task number -> position of its first heading
	deps     map[int][]int // task number -> Depends on targets
	refs     [][]int
	files    [][]taskFile
	unparsed [][]string // Create/Modify/Test bullets with no path
	hasFiles []bool     // the task has a **Files:** block
}

func newPlanGatesPlan(root, content string, tasks []planTask) planGatesPlan {
	p := planGatesPlan{
		root:     root,
		content:  content,
		tasks:    tasks,
		pos:      map[int]int{},
		deps:     map[int][]int{},
		refs:     make([][]int, len(tasks)),
		files:    make([][]taskFile, len(tasks)),
		unparsed: make([][]string, len(tasks)),
		hasFiles: make([]bool, len(tasks)),
	}
	for i, t := range tasks {
		if _, ok := p.pos[t.Number]; !ok {
			p.pos[t.Number] = i
		}
		p.refs[i] = waveComputeParseDependsOn(t.Body) // parseDependsOnRefs, shared with PF4
		p.deps[t.Number] = append(p.deps[t.Number], p.refs[i]...)
		p.files[i], p.unparsed[i], p.hasFiles[i] = parseTaskFiles(t.Body)
	}
	return p
}

// validatePlanGates runs the plan_gates action. Findings are sorted by gate
// number, then task number, then message. A plan with no "### Task N:"
// heading returns a DomainError: no gate was checked, so an empty finding
// list would read as a clean pass.
func validatePlanGates(root string, in ValidateIn) ([]discovery.Finding, error) {
	filePath, content, err := readPlanFile(root, in.File, "plan_gates")
	if err != nil {
		return nil, err
	}
	tasks := extractTasks(content)
	if len(tasks) == 0 {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("plan_gates: no \"### Task N: <title>\" heading outside a code fence, so no gate was checked: %s", filePath),
			Suggestion: "Write each task heading as \"### Task N: <title>\" (validate plan_format reports the structure as PF2). Then retry plan_gates.",
		}
	}
	p := newPlanGatesPlan(root, content, tasks)
	var c planGateCollector
	planGateDependencies(p, &c)
	planGateSharedModify(p, &c)
	planGateVerify(p, &c)
	planGateTaskSize(p, &c)
	planGateFilesParsed(p, &c)
	if err := planGateModifyPaths(p, &c); err != nil {
		return nil, err
	}
	planGateContractVisual(p, &c)
	return c.findings(filePath), nil
}

// planGateDependencies is G3 and G12: each Depends on target exists (G12) and
// is listed before the task (G3).
func planGateDependencies(p planGatesPlan, c *planGateCollector) {
	for i, t := range p.tasks {
		seen := map[int]bool{}
		for _, m := range p.refs[i] {
			if seen[m] {
				continue
			}
			seen[m] = true
			mp, ok := p.pos[m]
			switch {
			case !ok:
				c.add("G12", t.Number, "Task %d: depends on nonexistent Task %d", t.Number, m)
			case m == t.Number:
				c.add("G3", t.Number, "Task %d: depends on itself", t.Number)
			case mp > i:
				c.add("G3", t.Number, "Task %d: depends on Task %d, which is not listed before it", t.Number, m)
			}
		}
	}
}

// planGateSharedModify is G4: two tasks that modify one path need a dependency
// path between them.
func planGateSharedModify(p planGatesPlan, c *planGateCollector) {
	var modPaths []string
	modifiers := map[string][]int{} // path -> task positions, one per task
	for i := range p.tasks {
		for _, f := range p.files[i] {
			if f.Kind != "Modify" {
				continue
			}
			list := modifiers[f.Path]
			if len(list) > 0 && list[len(list)-1] == i {
				continue
			}
			if len(list) == 0 {
				modPaths = append(modPaths, f.Path)
			}
			modifiers[f.Path] = append(list, i)
		}
	}
	for _, path := range modPaths {
		list := modifiers[path]
		for a := 0; a < len(list); a++ {
			for b := a + 1; b < len(list); b++ {
				n, m := p.tasks[list[a]].Number, p.tasks[list[b]].Number
				if n == m || taskDependsOnPath(p.deps, n, m) || taskDependsOnPath(p.deps, m, n) {
					continue
				}
				c.add("G4", n, "Task %d and Task %d both modify %s with no dependency path between them", n, m, path)
			}
		}
	}
}

// planGateVerify is G8: the Verify field, with the PF3 text after "Task N".
func planGateVerify(p planGatesPlan, c *planGateCollector) {
	for _, t := range p.tasks {
		if issue, bad := checkVerifyField(t); bad {
			c.add("G8", t.Number, "Task %d%s", t.Number, issue)
		}
	}
}

// planGateTaskSize is G9: files per task and the share of Trivial tasks.
func planGateTaskSize(p planGatesPlan, c *planGateCollector) {
	trivial := 0
	for i, t := range p.tasks {
		distinct := map[string]bool{}
		for _, f := range p.files[i] {
			distinct[f.Path] = true
		}
		if len(distinct) > planGatesMaxTaskFiles {
			c.add("G9", t.Number, "Task %d: touches %d files (limit %d)", t.Number, len(distinct), planGatesMaxTaskFiles)
		}
		if cx, _ := extractField(t.Body, "Complexity"); cx == "Trivial" {
			trivial++
		}
	}
	if float64(trivial)/float64(len(p.tasks)) > planGatesMaxTrivialShare {
		c.add("G9", 0, "Plan: %d of %d tasks are Trivial (limit %d%%)", trivial, len(p.tasks), int(planGatesMaxTrivialShare*100))
	}
}

// planGateFilesParsed is the G10 warning for Files bullets that G4, G9 and G10
// cannot read: a task with no **Files:** block, and a Create, Modify or Test
// bullet with no path. Each is a warning, not an error: a task can touch no
// file. The warning names the bullet and the shape parseTaskFiles reads.
func planGateFilesParsed(p planGatesPlan, c *planGateCollector) {
	for i, t := range p.tasks {
		if !p.hasFiles[i] {
			c.warn("G10", t.Number, "Task %d: no **Files:** block, so G4, G9 and G10 do not check this task", t.Number)
			continue
		}
		for _, text := range p.unparsed[i] {
			c.warn("G10", t.Number, "Task %d: Files bullet %q names no path, so G4, G9 and G10 skip it. Write it as %s", t.Number, text, taskFileShape)
		}
	}
}

// planGateStatReason describes a stat error that the Modify path itself
// causes. ok is false for any other error (for example permission denied).
func planGateStatReason(err error) (reason string, ok bool) {
	switch {
	case errors.Is(err, syscall.ENOTDIR):
		return "a parent in the path is a file, not a folder", true
	case errors.Is(err, syscall.ENAMETOOLONG):
		return "the path or one of its names is too long", true
	case errors.Is(err, syscall.ELOOP):
		return "the path has a loop of symbolic links", true
	}
	return "", false
}

// planGateModifyPaths is G10: each Modify path is a regular file in the
// project, or an earlier task creates it. A path that the plan text makes
// impossible to check (planGateStatReason) is a G10 finding, so the other
// gates still report. Any other stat failure returns an InfraError.
func planGateModifyPaths(p planGatesPlan, c *planGateCollector) error {
	created := map[string]bool{}
	for i, t := range p.tasks {
		checked := map[string]bool{}
		for _, f := range p.files[i] {
			if f.Kind != "Modify" || checked[f.Path] {
				continue
			}
			checked[f.Path] = true
			if !filepath.IsLocal(f.Path) {
				c.add("G10", t.Number, "Task %d: Modify path %s is outside the project", t.Number, f.Path)
				continue
			}
			fi, serr := os.Stat(filepath.Join(p.root, f.Path))
			switch {
			case serr == nil:
				if !fi.Mode().IsRegular() {
					c.add("G10", t.Number, "Task %d: Modify path %s is not a regular file", t.Number, f.Path)
				}
			case errors.Is(serr, fs.ErrNotExist):
				if !created[f.Path] {
					c.add("G10", t.Number, "Task %d: Modify path %s does not exist", t.Number, f.Path)
				}
			default:
				if reason, ok := planGateStatReason(serr); ok {
					c.add("G10", t.Number, "Task %d: Modify path %s cannot be checked: %s", t.Number, f.Path, reason)
					continue
				}
				return &mcpserver.InfraError{
					Msg:        fmt.Sprintf("plan_gates: cannot check Modify path %s: %s", f.Path, serr.Error()),
					Suggestion: fmt.Sprintf("Give this process read and search permission on each folder of %s (ls -ld on each parent shows it). Then retry plan_gates.", f.Path),
					Cause:      serr,
				}
			}
		}
		for _, f := range p.files[i] {
			if f.Kind == "Create" {
				created[f.Path] = true
			}
		}
	}
	return nil
}

// planGateContractVisual is the G18 floor: a Contract block holds a code
// block, table or diagram. A task with no Contract gets no finding here: PF7
// owns a missing Contract.
func planGateContractVisual(p planGatesPlan, c *planGateCollector) {
	raw := rawTaskBodies(p.content)
	done := map[int]bool{}
	for _, t := range p.tasks {
		if done[t.Number] {
			continue
		}
		done[t.Number] = true
		body, ok := raw[t.Number]
		if !ok {
			continue
		}
		if _, found := contractBlock(body); found && !contractHasVisual(body) {
			c.add("G18", t.Number, "Task %d: Contract has no code block, table or diagram", t.Number)
		}
	}
}
