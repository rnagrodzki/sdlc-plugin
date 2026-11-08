// Overlap check between OpenSpec changes.
//
// Two changes overlap when both carry a delta spec for the same capability,
// that is, both have a specs/<capability>/ folder. The change that merges
// second must rebase its delta, so ship_prepare warns about each pair at the
// run start.
package openspec

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Values of CapabilityOverlap.Source.
const (
	OverlapSourceActive = "active" // the other change is under openspec/changes/
	OverlapSourceStaged = "staged" // the other change is under the staging folder
)

// CapabilityOverlap names one capability that change and OtherChange both
// edit. Source is "active" or "staged": where OtherChange lives.
type CapabilityOverlap struct {
	Capability  string
	OtherChange string
	Source      string
}

// CapabilityOverlaps lists each pair of capability and other change that
// shares a delta spec folder with change. The other changes are the valid
// change folders under activeRoot/openspec/changes (not "archive") and under
// the staging folder of stagingRoot. A change is never paired with itself. A
// name that is not a valid change name (a ".lock" file, a ".tmp-<change>-<n>"
// folder) is skipped. A change in both places counts once, as "active".
//
// The result is sorted by capability, then by other change. A read error of
// one other change does not stop the call: skipped gets one entry for it, in
// the form `"<other>": <error>`. The delta specs of change itself are read
// from activeRoot; when change has no folder there, from the staging folder.
// With no delta specs of its own, the result is empty.
func CapabilityOverlaps(activeRoot, stagingRoot, change string) (overlaps []CapabilityOverlap, skipped []string) {
	activeDir := filepath.Join(activeRoot, "openspec", "changes")
	stagingDir := stagingParentDir(stagingRoot)

	ownCaps, err := readCapabilities(filepath.Join(activeDir, change))
	if errors.Is(err, fs.ErrNotExist) {
		ownCaps, err = readCapabilities(filepath.Join(stagingDir, change))
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, []string{fmt.Sprintf("%q: %v", change, err)}
	}
	if len(ownCaps) == 0 {
		return nil, nil
	}
	own := map[string]bool{}
	for _, c := range ownCaps {
		own[c] = true
	}

	seen := map[string]bool{change: true}
	for _, src := range []struct{ dir, source string }{
		{activeDir, OverlapSourceActive},
		{stagingDir, OverlapSourceStaged},
	} {
		entries, err := os.ReadDir(src.dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				skipped = append(skipped, fmt.Sprintf("%q: %v", src.dir, err))
			}
			continue
		}
		for _, e := range entries {
			other := e.Name()
			if !e.IsDir() || other == "archive" || !ValidChangeName(other) || seen[other] {
				continue
			}
			seen[other] = true
			caps, err := readCapabilities(filepath.Join(src.dir, other))
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					skipped = append(skipped, fmt.Sprintf("%q: %v", other, err))
				}
				continue
			}
			for _, c := range caps {
				if own[c] {
					overlaps = append(overlaps, CapabilityOverlap{Capability: c, OtherChange: other, Source: src.source})
				}
			}
		}
	}

	sort.Slice(overlaps, func(i, j int) bool {
		if overlaps[i].Capability != overlaps[j].Capability {
			return overlaps[i].Capability < overlaps[j].Capability
		}
		return overlaps[i].OtherChange < overlaps[j].OtherChange
	})
	return overlaps, skipped
}

// readCapabilities returns the names of the folders under changeDir/specs.
// A missing specs folder returns an error that wraps fs.ErrNotExist.
func readCapabilities(changeDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(changeDir, "specs"))
	if err != nil {
		return nil, err
	}
	var caps []string
	for _, e := range entries {
		if e.IsDir() {
			caps = append(caps, e.Name())
		}
	}
	return caps, nil
}
