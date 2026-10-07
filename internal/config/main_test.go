package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain isolates every test in this package from whatever user-level
// config file (~/.sdlc/local.toml) happens to exist on the machine running
// the tests. Without this, a developer or CI box that has that file would
// have its contents silently merged into every Read/ReadSection call in
// this package, making test results depend on who runs them. Pointing
// SDLC_USER_CONFIG at a path inside a fresh, empty temp directory makes
// UserConfigPath resolve to a file that does not exist, which
// ReadLocalLayers treats as an empty user layer — the same "no user config"
// case every test in this package expects by default. A test that wants to
// exercise the user layer overrides this with t.Setenv(UserConfigPathEnv,
// ...), which Go automatically restores after that test.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sdlc-config-test-usercfg-*")
	if err != nil {
		panic(err)
	}

	os.Setenv(UserConfigPathEnv, filepath.Join(dir, "unused-local.toml"))

	code := m.Run()

	os.RemoveAll(dir)
	os.Unsetenv(UserConfigPathEnv)
	os.Exit(code)
}
