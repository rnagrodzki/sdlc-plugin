//go:build darwin || linux

package dashboard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	// priorityHelperEnv names the environment variable that turns the test
	// binary into the child process of the priority tests. Its value is the
	// helper mode.
	priorityHelperEnv = "DASHBOARD_PRIORITY_HELPER"
	// helperReal is the helper mode that keeps the real setPriority.
	helperReal = "real"
	// helperFail is the helper mode that replaces setPriority with a function
	// that always fails and never changes a nice value.
	helperFail = "fail"
	// priorityResultPrefix starts the one line that the child writes to stdout.
	priorityResultPrefix = "lower-priority-result"
	// priorityWait bounds every wait for the child and for the nice value.
	priorityWait = 5 * time.Second
	// priorityPoll is the pause between two polls of ps.
	priorityPoll = 20 * time.Millisecond
)

// errForcedPriority is the error of the setPriority function in helperFail mode.
var errForcedPriority = errors.New("forced priority failure")

// TestLowerPriorityHelperProcess is the body of the child process of the
// priority tests. A normal test run has no helper mode and returns at once.
// In helper mode it calls LowerPriority, writes one result line to stdout,
// and waits until the parent closes stdin so the parent can read the nice
// value of a live process.
func TestLowerPriorityHelperProcess(t *testing.T) {
	mode := os.Getenv(priorityHelperEnv)
	if mode == "" {
		return
	}
	var calls, which, who, prio int
	syscallSet := setPriority
	setPriority = func(w, u, p int) error {
		calls++
		which, who, prio = w, u, p
		if mode == helperFail {
			return errForcedPriority
		}
		return syscallSet(w, u, p)
	}
	err := LowerPriority()
	fmt.Printf("%s err=%v calls=%d which=%d who=%d prio=%d\n",
		priorityResultPrefix, err, calls, which, who, prio)
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// startPriorityHelper starts the test binary as a child process in the given
// helper mode and returns its pid and its result line. The child leads its
// own group (new session) when newSession is true. Else it stays in the group
// of the test process and does not lead it. A cleanup ends the child.
func startPriorityHelper(t *testing.T, mode string, newSession bool) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLowerPriorityHelperProcess$")
	cmd.Env = append(os.Environ(), priorityHelperEnv+"="+mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: newSession}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	timer := time.AfterFunc(priorityWait, func() { _ = cmd.Process.Kill() })
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
		timer.Stop()
	})

	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadString('\n')
		if strings.HasPrefix(line, priorityResultPrefix) {
			return cmd.Process.Pid, strings.TrimSpace(line)
		}
		if err != nil {
			t.Fatalf("helper wrote no result line: %v", err)
		}
	}
}

// psNice returns the nice value of the process pid, as ps reports it.
func psNice(pid int) (int, error) {
	out, err := exec.Command("ps", "-o", "ni=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, fmt.Errorf("ps -o ni= -p %d: %w", pid, err)
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// TestLowerPriority_LeaderGetsNice10 runs a real child process that leads its
// group. LowerPriority returns nil and ps shows the nice value 10 for the child.
func TestLowerPriority_LeaderGetsNice10(t *testing.T) {
	pid, got := startPriorityHelper(t, helperReal, true)
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
		t.Fatalf("child pgid = %d, %v; want the pid %d (the child leads its group)", pgid, err, pid)
	}
	want := fmt.Sprintf("%s err=<nil> calls=1 which=%d who=0 prio=%d",
		priorityResultPrefix, syscall.PRIO_PGRP, dashboardNice)
	if got != want {
		t.Errorf("result = %q, want %q", got, want)
	}

	deadline := time.Now().Add(priorityWait)
	for {
		nice, err := psNice(pid)
		if err == nil && nice == dashboardNice {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ps nice of the child = %d, %v; want %d", nice, err, dashboardNice)
		}
		time.Sleep(priorityPoll)
	}
}

// TestLowerPriority_FollowerKeepsNice runs a child process that does not lead
// its group. LowerPriority returns nil, never calls setPriority, and the child
// keeps the nice value of the test process.
func TestLowerPriority_FollowerKeepsNice(t *testing.T) {
	pid, got := startPriorityHelper(t, helperFail, false)
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid == pid {
		t.Fatalf("child pgid = %d, %v; want a group that the child does not lead", pgid, err)
	}
	want := priorityResultPrefix + " err=<nil> calls=0 which=0 who=0 prio=0"
	if got != want {
		t.Errorf("result = %q, want %q", got, want)
	}

	parentNice, err := psNice(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	childNice, err := psNice(pid)
	if err != nil {
		t.Fatal(err)
	}
	if childNice != parentNice {
		t.Errorf("child nice = %d, want the nice value of the test process: %d", childNice, parentNice)
	}
}

// TestLowerPriority_ReturnsSetPriorityError runs a child process that leads
// its group, with a setPriority that fails. LowerPriority calls it with
// PRIO_PGRP, 0 and dashboardNice, and returns its error unchanged.
func TestLowerPriority_ReturnsSetPriorityError(t *testing.T) {
	pid, got := startPriorityHelper(t, helperFail, true)
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
		t.Fatalf("child pgid = %d, %v; want the pid %d (the child leads its group)", pgid, err, pid)
	}
	want := fmt.Sprintf("%s err=%v calls=1 which=%d who=0 prio=%d",
		priorityResultPrefix, errForcedPriority, syscall.PRIO_PGRP, dashboardNice)
	if got != want {
		t.Errorf("result = %q, want %q", got, want)
	}
}
