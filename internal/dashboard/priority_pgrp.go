//go:build darwin || linux

// The build tag is not "unix": syscall.Getpgrp does not exist on solaris,
// illumos and aix. The file name has no GOOS suffix, because a "_linux"
// suffix limits a file to linux whatever its build tag says.

package dashboard

import "syscall"

// dashboardNice is the nice value of the dashboard server process group.
const dashboardNice = 10

// setPriority is syscall.Setpriority. Tests replace it to force an error.
var setPriority = syscall.Setpriority

// LowerPriority sets the nice value of the process group to dashboardNice
// when the calling process leads its group. Else it does nothing and returns
// nil. On Linux the nice value belongs to each thread, and PRIO_PGRP sets it
// for every thread of the group. darwin and linux only. Other systems return
// nil.
func LowerPriority() error {
	if syscall.Getpgrp() != syscall.Getpid() {
		return nil
	}
	return setPriority(syscall.PRIO_PGRP, 0, dashboardNice)
}
