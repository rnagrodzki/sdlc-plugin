package tools

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/frontmatter"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// toolCatalog lists the registered MCP tools and the action enum of each
// tool. *mcpserver.Server implements it. The dead-reference scan reads it to
// check tool and action names that a rule cites.
type toolCatalog interface {
	ToolNames() []string
	ToolActions(name string) ([]string, bool)
}

// deadReference is one path, tool, action, flag or step name in a rule that
// does not exist. Line is set for markdown surfaces only; a guardrail
// description has no line in its own text.
type deadReference struct {
	Surface string `json:"surface"`
	RuleID  string `json:"ruleId"` // guardrail id, dimension name, or instruction file name
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Kind    string `json:"kind"` // path | tool | action | flag | step
	Token   string `json:"token"`
	Reason  string `json:"reason"`
}

// Limits of the dead-reference scan. A file over deadRefMaxFileBytes is
// skipped, a repository with more tracked files than deadRefMaxTrackedFiles
// gets no path check, and the list keeps at most deadRefMaxHits entries.
const (
	deadRefMaxHits         = 200
	deadRefMaxTrackedFiles = 100000
	deadRefMaxFileBytes    = 1 << 20
)

// hardenGitLsFiles returns the tracked files of the repository at
// contentRoot, from `git ls-files -z`. Unit tests replace it with a fake;
// integration tests use the real one.
var hardenGitLsFiles = func(contentRoot string) ([]string, error) {
	out, err := execx.Run("git", []string{"ls-files", "-z"}, execx.Options{Dir: contentRoot})
	if err != nil {
		return nil, err
	}
	parts := strings.Split(out, "\x00")
	files := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

// deadRefExecutable returns the path of the running executable. The plugin-root
// walk starts at its directory; tests replace it to start the walk inside a
// temporary plugin tree.
var deadRefExecutable = os.Executable

// errDeadRefFileTooLarge is the cause text for a rule file or SKILL.md that is
// larger than deadRefMaxFileBytes.
var errDeadRefFileTooLarge = errors.New("larger than 1 MiB")

var (
	// deadRefBacktickRe finds backticked text on one line.
	deadRefBacktickRe = regexp.MustCompile("`([^`\n]+)`")
	// deadRefToolRe finds an MCP tool name: the plugin's full tool prefix,
	// or a snake_case name with at least one underscore followed by "(".
	// A name that follows "." (a method call) or a letter is not a match.
	deadRefToolRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.])(?:mcp__plugin_sdlc_sdlc__([a-z0-9_]+)|([a-z][a-z0-9]*(?:_[a-z0-9]+)+)\()`)
	// deadRefActionRe finds a call that names an action: name({action:"a".
	deadRefActionRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.])([a-z][a-z0-9_]*)\(\s*\{\s*["']?action["']?\s*:\s*["']([^"']+)["']`)
	// deadRefStepRe finds "<skill> Step N" or "/sdlc:<skill> Step N".
	deadRefStepRe = regexp.MustCompile(`(?:^|\s)((?:/sdlc:)?([a-z][a-z0-9-]*)\s+Step\s+(\d+(?:\.\d+)?[a-z]?))\b`)
	// deadRefFlagRe finds a flag name in a skill's argument-hint.
	deadRefFlagRe = regexp.MustCompile(`--[a-z0-9][a-z0-9-]*`)
	// deadRefLineSuffixRe matches a ":N" or ":N-M" line suffix of a cited path.
	deadRefLineSuffixRe = regexp.MustCompile(`:\d+(?:-\d+)?$`)
	// deadRefExtRe matches a file extension: letters and digits only.
	deadRefExtRe = regexp.MustCompile(`^[A-Za-z0-9]{1,10}$`)
)

// deadRefSource is one rule text to scan, with the identity that each hit
// carries.
type deadRefSource struct {
	surface  string
	ruleID   string
	file     string
	text     string
	markdown bool
}

// deadRefSkill is the loaded state of one skill's SKILL.md. usable is false
// when the file could not be read; flagsOK is false when its frontmatter did
// not parse or has no argument-hint, and then the flag check of the skill is
// skipped.
type deadRefSkill struct {
	usable  bool
	flagsOK bool
	flags   map[string]bool
	content string
}

// deadRefScanner holds what each check needs: the tracked files, the tool
// catalog, and the skills directory of the plugin. A nil field turns its
// check off; the skip note for it is already recorded.
type deadRefScanner struct {
	files     []string
	pathsOK   bool
	cat       toolCatalog
	toolNames map[string]bool
	skillsDir string
	known     map[string]bool
	skills    map[string]*deadRefSkill
	skips     *[]string
}

// scanDeadReferences checks the text of the loaded surfaces: guardrail descriptions
// (plan and execute), review-dimension bodies, and Copilot instruction bodies.
// It returns at most deadRefMaxHits references, the count of the rest, and one
// sentence for each check it skipped. It never fails: a check that cannot run
// is a skip note. Both lists are empty slices, never nil.
func scanDeadReferences(contentRoot string, s hardenSurfaces, cat toolCatalog) (refs []deadReference, truncated int, skips []string) {
	refs = []deadReference{}
	skips = []string{}
	sc := newDeadRefScanner(contentRoot, cat, &skips)
	for _, src := range deadRefSources(contentRoot, s, &skips) {
		refs = append(refs, sc.scanSource(src)...)
	}
	if len(refs) > deadRefMaxHits {
		truncated = len(refs) - deadRefMaxHits
		refs = refs[:deadRefMaxHits]
	}
	return refs, truncated, skips
}

// newDeadRefScanner prepares the three inputs of the scan. A missing tool
// catalog, a failed or oversize `git ls-files`, or a plugin root that cannot be
// found each add one skip note and turn their checks off.
func newDeadRefScanner(contentRoot string, cat toolCatalog, skips *[]string) *deadRefScanner {
	sc := &deadRefScanner{skips: skips, skills: map[string]*deadRefSkill{}}

	if cat == nil {
		*skips = append(*skips, "tool and action checks skipped: no tool catalog")
	} else {
		sc.cat = cat
		sc.toolNames = map[string]bool{}
		for _, n := range cat.ToolNames() {
			sc.toolNames[n] = true
		}
	}

	files, err := hardenGitLsFiles(contentRoot)
	switch {
	case err != nil:
		*skips = append(*skips, fmt.Sprintf("path check skipped: git ls-files failed: %s", err.Error()))
	case len(files) > deadRefMaxTrackedFiles:
		*skips = append(*skips, fmt.Sprintf("path check skipped: more than %d tracked files", deadRefMaxTrackedFiles))
	default:
		sc.files = files
		sc.pathsOK = true
	}

	skillsDir, known, note := loadDeadRefSkillNames()
	if note != "" {
		*skips = append(*skips, note)
	} else {
		sc.skillsDir = skillsDir
		sc.known = known
	}
	return sc
}

// loadDeadRefSkillNames finds the plugin's skills directory and lists the
// skill names in it. The plugin root comes from CLAUDE_PLUGIN_ROOT, else from
// a walk up from the running executable's directory (the working directory
// when the executable path is unknown), as resolveErrorReportSkill does. It
// returns a skip note instead of the names when either step fails.
func loadDeadRefSkillNames() (string, map[string]bool, string) {
	root := os.Getenv("CLAUDE_PLUGIN_ROOT")
	if root == "" {
		start := ""
		if exe, err := deadRefExecutable(); err == nil {
			start = filepath.Dir(exe)
		} else if cwd, cerr := os.Getwd(); cerr == nil {
			start = cwd
		}
		found, ok := "", false
		if start != "" {
			found, ok = findPluginRootFrom(start)
		}
		if !ok {
			return "", nil, "flag and step checks skipped: plugin root not found"
		}
		root = found
	}
	dir := filepath.Join(root, "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, fmt.Sprintf("flag and step checks skipped: read %s: %s", dir, err.Error())
	}
	known := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			known[e.Name()] = true
		}
	}
	return dir, known, ""
}

// deadRefSources returns the rule texts in a fixed order: plan guardrails,
// execute guardrails, review dimensions, Copilot instructions. A dimension or
// instruction file that is over 1 MiB or not readable adds a skip note and is
// left out.
func deadRefSources(contentRoot string, s hardenSurfaces, skips *[]string) []deadRefSource {
	configFile := filepath.Join(contentRoot, paths.DataDir, paths.ConfigFile)
	var out []deadRefSource
	for _, g := range s.PlanGuardrails {
		out = append(out, deadRefSource{surface: "plan-guardrails", ruleID: g.ID, file: configFile, text: g.Description})
	}
	for _, g := range s.ExecuteGuardrails {
		out = append(out, deadRefSource{surface: "execute-guardrails", ruleID: g.ID, file: configFile, text: g.Description})
	}
	for _, d := range s.ReviewDimensions {
		content, err := readDeadRefFile(d.Path)
		if err != nil {
			*skips = append(*skips, fmt.Sprintf("%s skipped: %s", d.Path, err.Error()))
			continue
		}
		id := d.Name
		if id == "" {
			id = strings.TrimSuffix(filepath.Base(d.Path), ".md")
		}
		out = append(out, deadRefSource{surface: "review-dimensions", ruleID: id, file: d.Path, text: content, markdown: true})
	}
	for _, c := range s.CopilotInstructions {
		content, err := readDeadRefFile(c.Path)
		if err != nil {
			*skips = append(*skips, fmt.Sprintf("%s skipped: %s", c.Path, err.Error()))
			continue
		}
		out = append(out, deadRefSource{surface: "copilot-instructions", ruleID: c.Name, file: c.Path, text: content, markdown: true})
	}
	return out
}

// readDeadRefFile reads a file of at most deadRefMaxFileBytes bytes. A larger
// file gives errDeadRefFileTooLarge; it never reads more than the cap plus
// one byte.
func readDeadRefFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, deadRefMaxFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > deadRefMaxFileBytes {
		return "", errDeadRefFileTooLarge
	}
	return string(data), nil
}

// scanSource checks each backticked token of one rule text, line by line. It
// reports a line number for markdown sources only, and reports one token of
// one kind at most once for each line.
func (sc *deadRefScanner) scanSource(src deadRefSource) []deadReference {
	var out []deadReference
	seen := map[string]bool{}
	for i, line := range strings.Split(src.text, "\n") {
		lineNo := 0
		if src.markdown {
			lineNo = i + 1
		}
		exampleLine := strings.Contains(strings.ToLower(line), "example") || strings.Contains(line, "e.g.")
		for _, m := range deadRefBacktickRe.FindAllStringSubmatch(line, -1) {
			for _, hit := range sc.checkToken(m[1], exampleLine) {
				key := fmt.Sprintf("%d|%s|%s", lineNo, hit.kind, hit.token)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, deadReference{
					Surface: src.surface,
					RuleID:  src.ruleID,
					File:    src.file,
					Line:    lineNo,
					Kind:    hit.kind,
					Token:   hit.token,
					Reason:  hit.reason,
				})
			}
		}
	}
	return out
}

// deadRefHit is one dead token found in a backticked span.
type deadRefHit struct {
	kind   string
	token  string
	reason string
}

// checkToken runs the path, tool, action, flag and step checks on one
// backticked span. exampleLine turns the path check off for that span.
func (sc *deadRefScanner) checkToken(tok string, exampleLine bool) []deadRefHit {
	var hits []deadRefHit
	if sc.pathsOK && !exampleLine {
		if h, ok := sc.checkPath(tok); ok {
			hits = append(hits, h)
		}
	}
	if sc.cat != nil {
		hits = append(hits, sc.checkTools(tok)...)
		hits = append(hits, sc.checkActions(tok)...)
	}
	if sc.known != nil {
		hits = append(hits, sc.checkFlags(tok)...)
		hits = append(hits, sc.checkSteps(tok)...)
	}
	return hits
}

// deadRefPathCandidate returns the path or glob that tok names, with a leading
// "./" and a ":N" or ":N-M" line suffix removed. A span with whitespace, a
// placeholder ("<", "{", "$", "~") or a URL is not a path. A plain path needs
// a "/" and a file extension; a glob needs a glob character and a "/" or a
// file extension.
func deadRefPathCandidate(tok string) (p string, glob bool, ok bool) {
	if strings.ContainsAny(tok, " \t") || strings.ContainsAny(tok, "<{$~") {
		return "", false, false
	}
	if strings.Contains(tok, "://") || strings.HasPrefix(tok, "www.") {
		return "", false, false
	}
	p = strings.TrimPrefix(tok, "./")
	p = deadRefLineSuffixRe.ReplaceAllString(p, "")
	if strings.ContainsAny(p, "*?[") {
		return p, true, strings.Contains(p, "/") || deadRefHasExt(p)
	}
	if !strings.Contains(p, "/") || strings.HasSuffix(p, "/") || !deadRefHasExt(p) {
		return "", false, false
	}
	return p, false, true
}

// deadRefHasExt reports whether the last segment of p ends in a file
// extension: a dot after the first character, then 1 to 10 letters or digits.
// "mcpserver.Register*" has none, so a symbol pattern is not a glob.
func deadRefHasExt(p string) bool {
	base := path.Base(p)
	dot := strings.LastIndex(base, ".")
	return dot > 0 && deadRefExtRe.MatchString(base[dot+1:])
}

// checkPath reports tok when it names a path or glob that no tracked file
// matches. A tracked file matches a plain path when it is equal to it or ends
// with "/" plus the path, so a path written relative to a subdirectory still
// matches. A glob matches the same way, through globToRegex. A glob that does
// not build as a regular expression (for example "src/[z-a].go") is treated as
// not dead and is not reported: the scan cannot tell what such a text names.
func (sc *deadRefScanner) checkPath(tok string) (deadRefHit, bool) {
	p, glob, ok := deadRefPathCandidate(tok)
	if !ok {
		return deadRefHit{}, false
	}
	if glob {
		re, err := regexp.Compile("^(?:.*/)?" + strings.TrimPrefix(globToRegex(p), "^"))
		if err != nil {
			return deadRefHit{}, false
		}
		for _, f := range sc.files {
			if re.MatchString(f) {
				return deadRefHit{}, false
			}
		}
		return deadRefHit{kind: "path", token: tok, reason: fmt.Sprintf("no tracked file matches the glob %s", p)}, true
	}
	for _, f := range sc.files {
		if f == p || strings.HasSuffix(f, "/"+p) {
			return deadRefHit{}, false
		}
	}
	return deadRefHit{kind: "path", token: tok, reason: fmt.Sprintf("no tracked file %s", p)}, true
}

// checkTools reports each tool name in tok that the catalog does not list:
// a name with the plugin's full tool prefix, or a snake_case name with an
// underscore that is called with "(". A bare snake_case word is not checked.
func (sc *deadRefScanner) checkTools(tok string) []deadRefHit {
	var hits []deadRefHit
	for _, m := range deadRefToolRe.FindAllStringSubmatch(tok, -1) {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if sc.toolNames[name] {
			continue
		}
		hits = append(hits, deadRefHit{kind: "tool", token: name, reason: fmt.Sprintf("no MCP tool named %s", name)})
	}
	return hits
}

// checkActions reports each action in a name({action:"a"}) call that the
// named tool does not accept. An unknown tool is left to checkTools, and a
// tool with no action enum is not checked.
func (sc *deadRefScanner) checkActions(tok string) []deadRefHit {
	var hits []deadRefHit
	for _, m := range deadRefActionRe.FindAllStringSubmatch(tok, -1) {
		name, action := m[1], m[2]
		actions, ok := sc.cat.ToolActions(name)
		if !ok || len(actions) == 0 || slices.Contains(actions, action) {
			continue
		}
		hits = append(hits, deadRefHit{kind: "action", token: action, reason: fmt.Sprintf("tool %s has no action %s", name, action)})
	}
	return hits
}

// checkFlags reports each --flag that follows a known skill name in tok and
// is not in that skill's argument-hint. The skill name is "/sdlc:<skill>"
// anywhere in the span, or a bare "<skill>" as its first word. A flag with no
// skill name before it is not checked.
func (sc *deadRefScanner) checkFlags(tok string) []deadRefHit {
	var hits []deadRefHit
	var skill string
	for i, w := range strings.Fields(tok) {
		if name, ok := strings.CutPrefix(w, "/sdlc:"); ok {
			skill = ""
			if sc.known[name] {
				skill = name
			}
			continue
		}
		if i == 0 && sc.known[w] {
			skill = w
			continue
		}
		if skill == "" || !strings.HasPrefix(w, "--") {
			continue
		}
		flag, _, _ := strings.Cut(w, "=")
		info := sc.skill(skill)
		if !info.usable || !info.flagsOK || info.flags[flag] {
			continue
		}
		hits = append(hits, deadRefHit{kind: "flag", token: flag, reason: fmt.Sprintf("skill %s has no flag %s in its argument-hint", skill, flag)})
	}
	return hits
}

// checkSteps reports each "<skill> Step N" in tok whose skill is known and
// whose SKILL.md has no "## Step N" heading (any heading level from two).
// A step with no skill name before it is not checked.
func (sc *deadRefScanner) checkSteps(tok string) []deadRefHit {
	var hits []deadRefHit
	for _, m := range deadRefStepRe.FindAllStringSubmatch(tok, -1) {
		phrase, skill, step := m[1], m[2], m[3]
		if !sc.known[skill] {
			continue
		}
		info := sc.skill(skill)
		if !info.usable {
			continue
		}
		heading := regexp.MustCompile(`(?m)^#{2,}\s.*\bStep\s+` + regexp.QuoteMeta(step) + `(?:$|[^0-9A-Za-z.]|\.[^0-9])`)
		if heading.MatchString(info.content) {
			continue
		}
		hits = append(hits, deadRefHit{kind: "step", token: phrase, reason: fmt.Sprintf("skill %s has no Step %s heading", skill, step)})
	}
	return hits
}

// skill loads and caches the SKILL.md of a known skill. A file over 1 MiB or
// not readable adds "<file> skipped: <cause>"; a frontmatter that does not
// parse adds "flag check skipped for <skill>: <error>" and keeps the step
// check; a frontmatter with no argument-hint adds "flag check skipped for
// <skill>: no argument-hint" and keeps the step check, because a skill that
// declares no hint gives no list of flags to compare with. Each note is added
// once.
func (sc *deadRefScanner) skill(name string) *deadRefSkill {
	if info, ok := sc.skills[name]; ok {
		return info
	}
	info := &deadRefSkill{flags: map[string]bool{}}
	sc.skills[name] = info
	file := filepath.Join(sc.skillsDir, name, "SKILL.md")
	content, err := readDeadRefFile(file)
	if err != nil {
		*sc.skips = append(*sc.skips, fmt.Sprintf("%s skipped: %s", file, err.Error()))
		return info
	}
	info.usable = true
	info.content = content
	meta, _, err := frontmatter.Parse([]byte(content))
	if err != nil {
		*sc.skips = append(*sc.skips, fmt.Sprintf("flag check skipped for %s: %s", name, err.Error()))
		return info
	}
	hint, hasHint := meta["argument-hint"].(string)
	if !hasHint {
		*sc.skips = append(*sc.skips, fmt.Sprintf("flag check skipped for %s: no argument-hint", name))
		return info
	}
	info.flagsOK = true
	for _, f := range deadRefFlagRe.FindAllString(hint, -1) {
		info.flags[f] = true
	}
	return info
}
