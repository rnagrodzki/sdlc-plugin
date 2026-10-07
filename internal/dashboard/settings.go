// Package dashboard reads the user's [dashboard] personal config section:
// the local sdlc dashboard, one web page on this computer that shows the
// pipelines of every registered repo.
package dashboard

import (
	"errors"
	"fmt"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// minPort and maxPort bound dashboard.port: the dashboard server only ever
// binds to loopback, but the port itself must still be a valid,
// non-privileged TCP port.
const (
	minPort = 1024
	maxPort = 65535
)

// defaultSettings is returned by ReadSettings when neither config file has
// a [dashboard] section.
var defaultSettings = Settings{AutoStart: false, Port: 7385}

// Settings holds the resolved [dashboard] personal config section.
type Settings struct {
	// AutoStart starts the local dashboard server when a Claude session
	// starts, printing its address, if the server is not already running.
	AutoStart bool
	// Port is the loopback port the dashboard server listens on.
	Port int
}

// ReadSettings reads the [dashboard] personal config section, anchored at
// mainRoot (the main worktree root). [dashboard] is a local section: it may
// live in .sdlc-v2/local.toml, in the user-level file (see
// config.UserConfigPath), or in both — the project file wins on any key
// both set.
//
// A [dashboard] section absent from both files is not an error: ReadSettings
// returns the documented defaults (AutoStart: false, Port: 7385). A TOML
// parse error in either file IS an error — it is never treated the same as
// "not configured". An out-of-range port returns a *mcpserver.DomainError
// naming the key and the valid range, with a Suggestion naming both files
// [dashboard] may live in.
func ReadSettings(mainRoot string) (Settings, error) {
	raw, err := config.ReadSection(mainRoot, "dashboard")
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return defaultSettings, nil
		}
		return Settings{}, err
	}

	settings := defaultSettings
	if v, ok := raw["autoStart"].(bool); ok {
		settings.AutoStart = v
	}
	if v, ok := raw["port"].(float64); ok {
		port := int(v)
		if port < minPort || port > maxPort {
			return Settings{}, &mcpserver.DomainError{
				Msg: fmt.Sprintf("config: dashboard.port = %d is out of range (must be %d-%d)", port, minPort, maxPort),
				Suggestion: fmt.Sprintf(
					"Set dashboard.port to a value between %d and %d in %s.",
					minPort, maxPort, config.LocalFilesLabel,
				),
			}
		}
		settings.Port = port
	}
	return settings, nil
}
