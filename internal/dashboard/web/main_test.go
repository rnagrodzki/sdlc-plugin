package web

import (
	"os"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/t3"
)

func TestMain(m *testing.M) {
	// Tests can run inside a T3 Code thread; keep the run-state stamp off.
	os.Unsetenv(t3.EnvThreadID)
	os.Exit(m.Run())
}
