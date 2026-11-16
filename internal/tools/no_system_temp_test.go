package tools

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// systemTempPattern matches a write to the temp folder of the system: an
// os.MkdirTemp / os.CreateTemp / mkdirTempFunc call whose folder argument is
// "" (the system temp folder), and any use of os.TempDir. A same-folder
// temp-then-rename write passes a folder, so it does not match.
var systemTempPattern = regexp.MustCompile(`(os\.MkdirTemp|os\.CreateTemp|mkdirTempFunc)\(""|os\.TempDir\b`)

// TestNoSystemTempWrites walks ../../internal and ../../cmd, skips *_test.go
// and comment lines, and fails on each match of systemTempPattern with file:line.
// Tool files go to .sdlc-v2/scratch/ of the repo (paths.ScratchDir), never to
// the temp folder of the system.
func TestNoSystemTempWrites(t *testing.T) {
	for _, dir := range []string{"../../internal", "../../cmd"} {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for n := 1; sc.Scan(); n++ {
				line := strings.TrimSpace(sc.Text())
				if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "/*") {
					continue
				}
				if systemTempPattern.MatchString(line) {
					t.Errorf("%s:%d: writes to the system temp folder; use paths.ScratchDir: %s", path, n, line)
				}
			}
			return sc.Err()
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}
