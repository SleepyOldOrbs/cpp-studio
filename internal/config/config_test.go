package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {
    "llama": {
      "command": "llama-server",
      "healthURL": "http://127.0.0.1:8733/health"
    }
  }
}`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadRejectsInvalidRuntimeValues(t *testing.T) {
	tests := map[string]string{
		"bad-port": `{
  "gateway": {"host": "127.0.0.1", "port": 70000},
  "engines": {"llama": {"command": "llama-server"}}
}`,
		"bad-health-url": `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {"llama": {"command": "llama-server", "healthUrl": "127.0.0.1:8733/health"}}
}`,
		"negative-timeout": `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {"llama": {"command": "llama-server", "startupTimeoutSeconds": -1}}
}`,
		"bad-mode": `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {"llama": {"command": "llama-server", "mode": "daemon"}}
}`,
		"negative-request-timeout": `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {"llama": {"command": "llama-server", "requestTimeoutSeconds": -1}}
}`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, body))
			if err == nil {
				t.Fatalf("expected config validation error")
			}
		})
	}
}

func TestCheckCommandsRejectsMissingCommand(t *testing.T) {
	cfg := Config{
		Gateway: GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]EngineConfig{
			"missing": {Command: "definitely-not-a-real-cpp-studio-command"},
		},
	}
	if err := cfg.CheckCommands(nil); err == nil {
		t.Fatalf("expected missing command error")
	}
}

func TestCheckCommandsResolvesRelativeToWorkingDir(t *testing.T) {
	var probed []string
	lookPath := func(path string) (string, error) {
		probed = append(probed, path)
		return path, nil
	}
	cfg := Config{
		Engines: map[string]EngineConfig{
			"relative": {Command: `bin\engine.exe`, WorkingDir: `C:\engines`},
			"bare":     {Command: "engine"},
		},
	}
	if err := cfg.CheckCommands(lookPath); err != nil {
		t.Fatalf("check commands: %v", err)
	}
	joined := strings.Join(probed, ";")
	if !strings.Contains(joined, filepath.Join(`C:\engines`, `bin\engine.exe`)) {
		t.Fatalf("expected workingDir-joined probe, got %v", probed)
	}
	if !strings.Contains(joined, "engine") {
		t.Fatalf("expected bare-name probe, got %v", probed)
	}
}

func TestLoadCheckedRejectsMissingCommand(t *testing.T) {
	path := writeConfig(t, `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {"llama": {"command": "definitely-not-a-real-cpp-studio-command"}}
}`)
	if _, err := LoadChecked(path); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing command error, got %v", err)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
