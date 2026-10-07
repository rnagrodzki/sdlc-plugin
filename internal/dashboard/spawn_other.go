//go:build !unix

package dashboard

// spawnDetached is not available outside Unix: the dashboard needs a
// detached session (Setsid) to outlive the process that starts it.
func spawnDetached(exe string, args []string, logPath string) (int, error) {
	return 0, ErrUnsupported
}
