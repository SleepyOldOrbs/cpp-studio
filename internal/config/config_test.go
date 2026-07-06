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

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
