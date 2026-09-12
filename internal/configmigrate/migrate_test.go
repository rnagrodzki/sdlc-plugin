package configmigrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// writeFile creates path (and its parent dirs) with the given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// mkSdlcDir creates an empty .sdlc-v2 directory (a scaffold with no config
// files at all — the "dir exists but no TOML" shape).
func mkSdlcDir(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, paths.DataDir), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", paths.DataDir, err)
	}
}

// ---------------------------------------------------------------------------
// detectProjectVersion / detectLocalVersion
// ---------------------------------------------------------------------------

func TestDetectProjectVersion(t *testing.T) {
	t.Run("toml exists -> v1", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
		ver, exists := detectProjectVersion(root)
		if !exists || ver != 1 {
			t.Errorf("detectProjectVersion() = (%d, %v), want (1, true)", ver, exists)
		}
	})

	t.Run("legacy json only -> v0 stale", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{"schemaVersion": 6}`)
		ver, exists := detectProjectVersion(root)
		if !exists || ver != 0 {
			t.Errorf("detectProjectVersion() = (%d, %v), want (0, true)", ver, exists)
		}
	})

	t.Run("empty sdlc dir -> v0 stale", func(t *testing.T) {
		root := t.TempDir()
		mkSdlcDir(t, root)
		ver, exists := detectProjectVersion(root)
		if !exists || ver != 0 {
			t.Errorf("detectProjectVersion() = (%d, %v), want (0, true)", ver, exists)
		}
	})

	t.Run("nothing at all -> missing", func(t *testing.T) {
		root := t.TempDir()
		ver, exists := detectProjectVersion(root)
		if exists || ver != 0 {
			t.Errorf("detectProjectVersion() = (%d, %v), want (0, false)", ver, exists)
		}
	})
}

func TestDetectLocalVersion(t *testing.T) {
	t.Run("toml exists -> v1", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")
		ver, exists := detectLocalVersion(root)
		if !exists || ver != 1 {
			t.Errorf("detectLocalVersion() = (%d, %v), want (1, true)", ver, exists)
		}
	})

	t.Run("legacy json only -> v0 stale", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "local.json"), `{"schemaVersion": 6}`)
		ver, exists := detectLocalVersion(root)
		if !exists || ver != 0 {
			t.Errorf("detectLocalVersion() = (%d, %v), want (0, true)", ver, exists)
		}
	})

	t.Run("nothing at all -> missing", func(t *testing.T) {
		root := t.TempDir()
		ver, exists := detectLocalVersion(root)
		if exists || ver != 0 {
			t.Errorf("detectLocalVersion() = (%d, %v), want (0, false)", ver, exists)
		}
	})
}

// ---------------------------------------------------------------------------
// Verify
// ---------------------------------------------------------------------------

func TestVerify(t *testing.T) {
	t.Run("fresh project -> nil", func(t *testing.T) {
		root := t.TempDir()
		if err := Verify(root); err != nil {
			t.Errorf("Verify() = %v, want nil", err)
		}
	})

	t.Run("toml current -> nil", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
		if err := Verify(root); err != nil {
			t.Errorf("Verify() = %v, want nil", err)
		}
	})

	t.Run("legacy json -> ErrVersionStale", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{"schemaVersion": 6}`)
		err := Verify(root)
		if !errors.Is(err, ErrVersionStale) {
			t.Fatalf("Verify() = %v, want ErrVersionStale", err)
		}
		if !strings.Contains(err.Error(), "/setup") {
			t.Errorf("Verify() error = %q, want it to mention /setup", err.Error())
		}
	})
}

// ---------------------------------------------------------------------------
// Migrate
// ---------------------------------------------------------------------------

func TestMigrate(t *testing.T) {
	t.Run("fresh project -> no-op report, no error", func(t *testing.T) {
		root := t.TempDir()
		report, err := Migrate(root, Options{})
		if err != nil {
			t.Fatalf("Migrate() error = %v, want nil", err)
		}
		if report == nil || report.Migrated {
			t.Errorf("Migrate() report = %+v, want zero-valued", report)
		}
	})

	t.Run("toml current -> no-op report, no error", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
		writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")
		report, err := Migrate(root, Options{})
		if err != nil {
			t.Fatalf("Migrate() error = %v, want nil", err)
		}
		if report == nil || report.Migrated {
			t.Errorf("Migrate() report = %+v, want zero-valued", report)
		}
	})

	t.Run("legacy json project -> clear TOML-required error", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{"schemaVersion": 6}`)
		report, err := Migrate(root, Options{})
		if report != nil {
			t.Errorf("Migrate() report = %+v, want nil on error", report)
		}
		if !errors.Is(err, ErrVersionStale) {
			t.Fatalf("Migrate() error = %v, want ErrVersionStale", err)
		}
		if !strings.Contains(err.Error(), "TOML config required") || !strings.Contains(err.Error(), "/setup") {
			t.Errorf("Migrate() error = %q, want it to name TOML and /setup", err.Error())
		}
	})

	t.Run("legacy json local only -> clear TOML-required error", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
		writeFile(t, filepath.Join(root, paths.DataDir, "local.json"), `{"schemaVersion": 6}`)
		_, err := Migrate(root, Options{})
		if !errors.Is(err, ErrVersionStale) {
			t.Fatalf("Migrate() error = %v, want ErrVersionStale", err)
		}
	})
}

// ---------------------------------------------------------------------------
// MigrateWithBackup
// ---------------------------------------------------------------------------

func TestMigrateWithBackup_MissingConfig(t *testing.T) {
	root := t.TempDir()
	changes, backupPath, err := MigrateWithBackup(root)
	if changes != nil || backupPath != "" {
		t.Errorf("MigrateWithBackup() = (%v, %q, _), want (nil, \"\", _)", changes, backupPath)
	}
	if !errors.Is(err, ErrConfigMissing) {
		t.Fatalf("MigrateWithBackup() error = %v, want ErrConfigMissing", err)
	}
	if !strings.Contains(err.Error(), "/setup") {
		t.Errorf("MigrateWithBackup() error = %q, want it to mention /setup", err.Error())
	}
}

func TestMigrateWithBackup_CurrentConfig_NoOp(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "")

	changes, backupPath, err := MigrateWithBackup(root)
	if err != nil {
		t.Fatalf("MigrateWithBackup() error = %v, want nil", err)
	}
	if changes != nil || backupPath != "" {
		t.Errorf("MigrateWithBackup() = (%v, %q, nil), want (nil, \"\", nil)", changes, backupPath)
	}
}

func TestMigrateWithBackup_StaleProjectConfig_NoAutoMigration(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{"schemaVersion": 6}`)

	changes, backupPath, err := MigrateWithBackup(root)
	if changes != nil || backupPath != "" {
		t.Errorf("MigrateWithBackup() = (%v, %q, _), want (nil, \"\", _)", changes, backupPath)
	}
	if !errors.Is(err, ErrVersionStale) {
		t.Fatalf("MigrateWithBackup() error = %v, want ErrVersionStale", err)
	}
	if !strings.Contains(err.Error(), "TOML config required") || !strings.Contains(err.Error(), "/setup") {
		t.Errorf("MigrateWithBackup() error = %q, want it to name TOML and /setup", err.Error())
	}

	// No .bak file must be written — there is no automated JSON->TOML
	// migration path, so nothing should be touched on disk.
	entries, _ := os.ReadDir(filepath.Join(root, paths.DataDir))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bak") {
			t.Errorf("found unexpected backup file %s; MigrateWithBackup must not write one", e.Name())
		}
	}
}

func TestMigrateWithBackup_StaleLocalConfig_NoAutoMigration(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(root, paths.DataDir, "local.json"), `{"schemaVersion": 6}`)

	_, backupPath, err := MigrateWithBackup(root)
	if backupPath != "" {
		t.Errorf("MigrateWithBackup() backupPath = %q, want \"\"", backupPath)
	}
	if !errors.Is(err, ErrVersionStale) {
		t.Fatalf("MigrateWithBackup() error = %v, want ErrVersionStale", err)
	}
}
