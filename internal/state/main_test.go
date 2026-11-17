package state

import (
	"os"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/t3"
)

// TestMain unsets the T3 Code thread variable before any test runs. Tests can
// run inside a T3 Code thread, and Init stamps the thread id into every new
// state file. Without this, a test that expects a file with no t3ThreadId key
// would fail on a developer machine inside T3 Code.
func TestMain(m *testing.M) {
	os.Unsetenv(t3.EnvThreadID)
	os.Exit(m.Run())
}
