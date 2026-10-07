package skillcheck

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
)

// TestMain isolates every test in this package from whatever user-level
// config file (~/.sdlc/local.toml) happens to exist on the machine running
// the tests. Without this, a developer's or CI machine's real local config
// would be silently merged into every config.Read/ReadSection call made by
// the production code this package's tests exercise, making test results
// depend on who runs them. Pointing SDLC_USER_CONFIG at a path inside a
// fresh, empty temp directory makes config.UserConfigPath resolve to a file
// that does not exist, which ReadLocalLayers treats as an empty user layer —
// the same "no user config" case every test in this package expects by
// default.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sdlc-user-config-")
	if err != nil {
		panic(err)
	}

	os.Setenv(config.UserConfigPathEnv, filepath.Join(dir, "local.toml"))

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
