package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// rootsSubdir is the Dir() subdirectory holding one JSON file per registered
// repo root (see RegisterRoot).
const rootsSubdir = "roots"

// serverRecordFile is the Dir() file holding the running dashboard server's
// ServerRecord (see ReadServerRecord / WriteServerRecord).
const serverRecordFile = "server.json"

// rootStaleAfter is how long a registered root may go without a RegisterRoot
// call before Roots drops it (and deletes its file) as stale.
const rootStaleAfter = 7 * 24 * time.Hour

// ErrNoServer is returned by ReadServerRecord when server.json does not
// exist: no dashboard server is currently recorded as running.
var ErrNoServer = errors.New("dashboard: no server record")

// Root is one repo root registered with the local dashboard, recorded as its
// own file under Dir()/roots.
type Root struct {
	Root     string    `json:"root"`
	LastSeen time.Time `json:"lastSeen"`
}

// ServerRecord describes the running local dashboard server, recorded at
// Dir()/server.json so every repo and session can discover it.
type ServerRecord struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"startedAt"`
	URL       string    `json:"url"`
}

// Dir returns the dashboard's cache directory: the "dashboard" subdirectory
// of the shared user cache root (see paths.CacheDir).
func Dir() string {
	return filepath.Join(paths.CacheDir(), "dashboard")
}

// LogPath returns Dir()/server.log: the file that receives the output of a
// dashboard server that Ensure starts in the background. A cache clear
// truncates it.
func LogPath() string {
	return filepath.Join(Dir(), "server.log")
}

// rootsDir returns Dir()/roots, the directory holding one file per
// registered repo root.
func rootsDir() string {
	return filepath.Join(Dir(), rootsSubdir)
}

// rootFilePath returns the path RegisterRoot and Roots use for root: a file
// named after the first 16 hex characters of sha256(root), so two sessions
// of the same repo always write the same file.
func rootFilePath(root string) string {
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(rootsDir(), hex.EncodeToString(sum[:])[:16]+".json")
}

// RegisterRoot records root as a live repo the dashboard should show,
// stamping its lastSeen as now. It writes Dir()/roots/<hash>.json
// atomically; two sessions of the same repo hash to the same file, so the
// second RegisterRoot call simply refreshes lastSeen rather than creating a
// duplicate entry.
func RegisterRoot(root string, now time.Time) error {
	dir := rootsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("dashboard: create %s: %w", dir, err)
	}
	entry := Root{Root: root, LastSeen: now}
	path := rootFilePath(root)
	if err := fsx.AtomicWriteJSON(path, entry); err != nil {
		return fmt.Errorf("dashboard: write %s: %w", path, err)
	}
	return nil
}

// Roots returns every registered repo root still considered live as of now:
// its .sdlc-v2 directory still exists, and its lastSeen is within
// rootStaleAfter (7 days). A root that fails either check is dropped from
// the result AND its file is deleted, so the registry self-cleans without a
// separate GC pass. The returned slice is never nil.
func Roots(now time.Time) ([]Root, error) {
	dir := rootsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Root{}, nil
		}
		return nil, fmt.Errorf("dashboard: read %s: %w", dir, err)
	}

	result := []Root{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())

		var r Root
		if err := fsx.ReadJSON(path, &r); err != nil {
			// A file that vanished or failed to parse between ReadDir and
			// ReadJSON (e.g. a concurrent write race) is not this call's
			// problem to report; skip it rather than failing the whole list.
			continue
		}

		stale := now.Sub(r.LastSeen) > rootStaleAfter
		_, statErr := os.Stat(filepath.Join(r.Root, paths.DataDir))
		missingDataDir := statErr != nil

		if stale || missingDataDir {
			os.Remove(path)
			continue
		}

		result = append(result, r)
	}
	return result, nil
}

// ReadServerRecord reads Dir()/server.json. It returns ErrNoServer (wrapped,
// so errors.Is matches) when the file does not exist — no dashboard server
// is currently recorded as running.
func ReadServerRecord() (ServerRecord, error) {
	path := filepath.Join(Dir(), serverRecordFile)
	var r ServerRecord
	if err := fsx.ReadJSON(path, &r); err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return ServerRecord{}, fmt.Errorf("dashboard: %s: %w", path, ErrNoServer)
		}
		return ServerRecord{}, fmt.Errorf("dashboard: read %s: %w", path, err)
	}
	return r, nil
}

// WriteServerRecord writes r to Dir()/server.json atomically, creating Dir()
// if needed. It replaces any previously recorded server.
func WriteServerRecord(r ServerRecord) error {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("dashboard: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, serverRecordFile)
	if err := fsx.AtomicWriteJSON(path, r); err != nil {
		return fmt.Errorf("dashboard: write %s: %w", path, err)
	}
	return nil
}

// RemoveServerRecord deletes Dir()/server.json, but only when the file's
// recorded PID equals pid — a stopped server must not delete a record a
// newer server has since written. A missing file, or one recorded under a
// different PID, is not an error.
func RemoveServerRecord(pid int) error {
	path := filepath.Join(Dir(), serverRecordFile)
	r, err := ReadServerRecord()
	if err != nil {
		if errors.Is(err, ErrNoServer) {
			return nil
		}
		return err
	}
	if r.PID != pid {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("dashboard: remove %s: %w", path, err)
	}
	return nil
}
