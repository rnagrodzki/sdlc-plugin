//go:build unix

package dashboard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// spawnDetached starts exe with args in a new session (Setsid), so the
// server outlives the MCP process or hook that started it. Stdin reads
// /dev/null; stdout and stderr append to logPath. The working directory is
// the log's directory, so the server does not pin a repo worktree. The
// process handle is released; the server runs until a person stops it.
func spawnDetached(exe string, args []string, logPath string) (int, error) {
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("create %s: %w", dir, err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", logPath, err)
	}
	defer logFile.Close()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devNull.Close()

	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Stdin = devNull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// The process already runs; a Release failure must not report the start
	// as failed (the caller would then report no server while one runs).
	_ = cmd.Process.Release()
	return pid, nil
}
