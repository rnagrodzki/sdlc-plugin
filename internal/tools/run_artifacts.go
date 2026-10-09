package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// runReportExts are the formats the report writers produce. A temp file left
// by an interrupted write (<name>.tmp-*) has none of them, so it never counts
// as a report.
var runReportExts = []string{"md", "json"}

// reportOwnerRE matches a file name in reports/ that belongs to a run:
// ship-<id>-report.<ext> (ship run) or <id>-report.<ext> (execute run).
// <id> is the alphabet execRunID produces: digits and 'T'.
var reportOwnerRE = regexp.MustCompile(`^(?:ship-)?([0-9T]+)-report\.(?:md|json)$`)

// RunArtifacts lists the on-disk artifacts of one run. Every path is absolute
// and exists when ResolveRunArtifacts returns it.
type RunArtifacts struct {
	StateFile string   // runs/<prefix>-<slug>-<ts>.json
	Keep      []string // reports, execute ledger: archive moves them
	Working   []string // execute progress dir, plan .evidence dir: archive deletes them
}

// execRunID derives the per-run id from data.startedAt with every character
// other than digits and 'T' removed, for example 20261008T120000. It returns
// "" when startedAt is absent or empty. It is the only copy of the id rule:
// the per-run directory, the ledger folder and the report file names all use
// its result.
func execRunID(data map[string]any) string {
	startedAt, _ := data["startedAt"].(string)
	if startedAt == "" {
		return ""
	}
	return execNonDigitTRE.ReplaceAllString(startedAt, "")
}

// bareRunName is the only name rule for a run id from a request. It returns
// an error for "", ".", "..", a name with "/" or "\", or a name for which
// filepath.Base(id) != id. A name that passes joins under a directory without
// escaping it and without resolving to the directory itself.
func bareRunName(id string) error {
	if id == "" || id == "." || id == ".." {
		return fmt.Errorf("invalid run name %q: must be a bare name, not empty, \".\" or \"..\"", id)
	}
	if strings.ContainsAny(id, `/\`) || filepath.Base(id) != id {
		return fmt.Errorf("invalid run name %q: must not contain a path separator", id)
	}
	return nil
}

// ReviewLedgerDir returns runs/ledger/<name>/ for a review row id such as
// review-20261008T120000Z. It does not check that the folder exists. It
// returns an error for a name that fails bareRunName or does not start with
// "review-" followed by at least one more character, so it never returns
// runs/ledger/ itself and never returns the folder of an execute run.
func ReviewLedgerDir(root, name string) (string, error) {
	if err := bareRunName(name); err != nil {
		return "", err
	}
	if !strings.HasPrefix(name, dashboardReviewPrefix) || len(name) == len(dashboardReviewPrefix) {
		return "", fmt.Errorf("invalid review run name %q: must start with %q and name a run", name, dashboardReviewPrefix)
	}
	return ledgerDir(root, name), nil
}

// ReportOwner maps a file name in reports/ to the execRunID value of the run
// that owns it. It accepts ship-<id>-report.<ext> and <id>-report.<ext> and
// returns ("", false) for any other name.
func ReportOwner(name string) (runID string, ok bool) {
	m := reportOwnerRE.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ResolveRunArtifacts lists the artifacts of the run that st describes.
//
//	ship:    Keep = reports/ship-<id>-report.<ext>
//	execute: Keep = runs/ledger/<id>/, reports/<id>-report.<ext>; Working = runs/<id>/
//	plan:    Keep = runs/<state base name>.evidence/brief.md; Working = runs/<state base name>.evidence/
//
// <id> is execRunID(st.Data). The function returns only paths that exist; a
// missing path is not an error. It returns an error and no paths when the run
// has no usable id (ship or execute state without startedAt, plan state
// without a file name) or when the state kind has no per-run artifacts.
func ResolveRunArtifacts(root string, st *state.State) (RunArtifacts, error) {
	if st == nil {
		return RunArtifacts{}, errors.New("resolve run artifacts: state is nil")
	}

	var keep, working []string
	switch st.Prefix {
	case "ship":
		id := execRunID(st.Data)
		if id == "" {
			return RunArtifacts{}, errNoRunID(st)
		}
		keep = reportPaths(root, "ship-"+id)
	case "execute":
		id := execRunID(st.Data)
		if id == "" {
			return RunArtifacts{}, errNoRunID(st)
		}
		keep = append([]string{ledgerDir(root, id)}, reportPaths(root, id)...)
		working = []string{filepath.Join(root, paths.DataDir, paths.RunsSubdir, id)}
	case "plan":
		name := state.RunID(st)
		if err := bareRunName(name); err != nil {
			return RunArtifacts{}, fmt.Errorf("resolve run artifacts for %q: %w", st.Path, err)
		}
		evidence := state.EvidenceDir(root, name)
		keep = []string{filepath.Join(evidence, "brief.md")}
		working = []string{evidence}
	default:
		return RunArtifacts{}, fmt.Errorf("resolve run artifacts for %q: state kind %q has no per-run artifacts", st.Path, st.Prefix)
	}

	var out RunArtifacts
	var err error
	if st.Path != "" {
		if out.StateFile, err = existingPath(st.Path); err != nil {
			return RunArtifacts{}, err
		}
	}
	if out.Keep, err = existingPaths(keep); err != nil {
		return RunArtifacts{}, err
	}
	if out.Working, err = existingPaths(working); err != nil {
		return RunArtifacts{}, err
	}
	return out, nil
}

// errNoRunID returns the error for a ship or execute state whose startedAt is
// missing or has no digits. The message names the state file and the state kind.
func errNoRunID(st *state.State) error {
	return fmt.Errorf("resolve run artifacts for %q: %s state has no startedAt, so the run id is empty", st.Path, st.Prefix)
}

// reportPaths returns the candidate report files <reports>/<stem>-report.<ext>
// for every format in runReportExts.
func reportPaths(root, stem string) []string {
	dir := filepath.Join(root, paths.DataDir, paths.ReportsSubdir)
	out := make([]string, 0, len(runReportExts))
	for _, ext := range runReportExts {
		out = append(out, filepath.Join(dir, stem+"-report."+ext))
	}
	return out
}

// existingPath returns p when it exists and "" when it does not. Any other
// stat failure is an error. It does not follow a final symlink.
func existingPath(p string) (string, error) {
	if _, err := os.Lstat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("resolve run artifacts: stat %s: %w", p, err)
	}
	return p, nil
}

// existingPaths filters candidates down to the ones that exist, in order.
func existingPaths(candidates []string) ([]string, error) {
	var out []string
	for _, p := range candidates {
		found, err := existingPath(p)
		if err != nil {
			return nil, err
		}
		if found != "" {
			out = append(out, found)
		}
	}
	return out, nil
}
