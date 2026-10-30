//go:build !darwin && !linux

package dashboard

// LowerPriority does nothing on this system: only darwin and linux lower the
// nice value of the process group. It always returns nil.
func LowerPriority() error {
	return nil
}
