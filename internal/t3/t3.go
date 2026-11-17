// Package t3 detects a T3 Code agent process.
package t3

import (
	"os"
	"regexp"
	"strings"
)

// EnvThreadID is the variable T3 Code sets for each agent process.
const EnvThreadID = "T3CODE_THREAD_ID"

// StateKey is the run-state and snapshot key of the thread id.
const StateKey = "t3ThreadId"

// MaxThreadIDLen is the longest thread id that ThreadID accepts.
const MaxThreadIDLen = 128

// ThreadIDPattern is the accepted form. The state schemas use the same pattern.
const ThreadIDPattern = `^[A-Za-z0-9._-]{1,128}$`

// MaxRunSlugLen is the longest run id after its ship-, execute- or plan- prefix.
const MaxRunSlugLen = 200

// RunIDPattern matches a run id (state file name without .json) of the three kinds.
const RunIDPattern = `^(ship|execute|plan)-[A-Za-z0-9._-]{1,200}$`

// threadIDRe is ThreadIDPattern compiled once.
var threadIDRe = regexp.MustCompile(ThreadIDPattern)

// ThreadID returns the trimmed value of EnvThreadID when it matches
// ThreadIDPattern, else "". "" means "not in T3 Code".
func ThreadID() string {
	id := strings.TrimSpace(os.Getenv(EnvThreadID))
	if !threadIDRe.MatchString(id) {
		return ""
	}
	return id
}
