package dashboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// isolateUserConfig points config.UserConfigPath at a file that does not
// exist, inside a throwaway temp dir, so the test never reads the real
// developer's ~/.sdlc/local.toml.
func isolateUserConfig(t *testing.T) {
	t.Helper()
	t.Setenv(config.UserConfigPathEnv, filepath.Join(t.TempDir(), "unused-user-local.toml"))
}

func writeLocalTOML(t *testing.T, root string, cfg map[string]any) {
	t.Helper()
	dir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	path := filepath.Join(dir, paths.LocalConfigFile)
	if err := fsx.AtomicWriteTOML(path, cfg); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestReadSettingsDefaultsWhenNoSection(t *testing.T) {
	isolateUserConfig(t)
	root := t.TempDir()

	got, err := ReadSettings(root)
	if err != nil {
		t.Fatalf("ReadSettings: unexpected error: %v", err)
	}
	want := Settings{AutoStart: false, Port: 7385}
	if got != want {
		t.Errorf("ReadSettings = %+v, want %+v", got, want)
	}
}

func TestReadSettingsDefaultsWhenLocalFileHasNoDashboardSection(t *testing.T) {
	isolateUserConfig(t)
	root := t.TempDir()
	writeLocalTOML(t, root, map[string]any{
		"ship": map[string]any{"auto": true},
	})

	got, err := ReadSettings(root)
	if err != nil {
		t.Fatalf("ReadSettings: unexpected error: %v", err)
	}
	want := Settings{AutoStart: false, Port: 7385}
	if got != want {
		t.Errorf("ReadSettings = %+v, want %+v", got, want)
	}
}

func TestReadSettingsReadsConfiguredValues(t *testing.T) {
	isolateUserConfig(t)
	root := t.TempDir()
	writeLocalTOML(t, root, map[string]any{
		"dashboard": map[string]any{"autoStart": true, "port": 8123},
	})

	got, err := ReadSettings(root)
	if err != nil {
		t.Fatalf("ReadSettings: unexpected error: %v", err)
	}
	want := Settings{AutoStart: true, Port: 8123}
	if got != want {
		t.Errorf("ReadSettings = %+v, want %+v", got, want)
	}
}

func TestReadSettingsPortOutOfRangeIsDomainError(t *testing.T) {
	isolateUserConfig(t)
	root := t.TempDir()
	writeLocalTOML(t, root, map[string]any{
		"dashboard": map[string]any{"port": 80},
	})

	_, err := ReadSettings(root)
	if err == nil {
		t.Fatal("ReadSettings: expected an error for an out-of-range port, got nil")
	}
	var domErr *mcpserver.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("ReadSettings error = %v (%T), want *mcpserver.DomainError", err, err)
	}
	if !strings.Contains(domErr.Msg, "port") || !strings.Contains(domErr.Msg, "1024") || !strings.Contains(domErr.Msg, "65535") {
		t.Errorf("DomainError.Msg = %q, want it to name the key and the 1024-65535 range", domErr.Msg)
	}
	if !strings.Contains(domErr.Suggestion, "local.toml") || !strings.Contains(domErr.Suggestion, ".sdlc") {
		t.Errorf("DomainError.Suggestion = %q, want it to name both local config files", domErr.Suggestion)
	}
}

func TestReadSettingsParseErrorIsNotTreatedAsUnconfigured(t *testing.T) {
	isolateUserConfig(t)
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	path := filepath.Join(dir, paths.LocalConfigFile)
	if err := fsx.AtomicWriteBytes(path, []byte("not = [valid toml")); err != nil {
		t.Fatalf("write malformed local.toml: %v", err)
	}

	_, err := ReadSettings(root)
	if err == nil {
		t.Fatal("ReadSettings: expected an error for a malformed local.toml, got nil")
	}
	if errors.Is(err, config.ErrNotFound) {
		t.Errorf("ReadSettings: a TOML parse error must not be config.ErrNotFound, got %v", err)
	}
}
