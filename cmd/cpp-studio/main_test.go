package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cpp-studio/internal/config"
)

func TestRunReturnsServerBindError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: port},
		Engines: map[string]config.EngineConfig{
			"helper": {
				Command:                os.Args[0],
				Args:                   []string{"-test.run=TestHelperProcess", "--", "sleep"},
				StartupTimeoutSeconds:  2,
				ShutdownTimeoutSeconds: 2,
			},
		},
	}
	path := writeRunConfig(t, cfg)

	err = run([]string{"--config", path, "--run-seconds", "5"})
	if err == nil || !strings.Contains(err.Error(), "server error") {
		t.Fatalf("expected server bind error, got %v", err)
	}
}

func TestOpenSessionLogCreatesPrivateTimestampedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	now := time.Date(2026, time.August, 9, 7, 30, 45, 0, time.UTC)
	file, path, err := openSessionLog(dir, now, 1234)
	if err != nil {
		t.Fatalf("open session log: %v", err)
	}
	if _, err := file.WriteString("diagnostic line\n"); err != nil {
		_ = file.Close()
		t.Fatalf("write session log: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close session log: %v", err)
	}
	if filepath.Base(path) != "cpp-studio-20260809T073045Z-pid1234.log" {
		t.Fatalf("unexpected log name %q", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	if string(data) != "diagnostic line\n" {
		t.Fatalf("unexpected log contents %q", data)
	}
}

func TestHelperProcess(t *testing.T) {
	args := os.Args
	for i, arg := range args {
		if arg == "--" && i+1 < len(args) && args[i+1] == "sleep" {
			fmt.Println("helper ready")
			time.Sleep(30 * time.Second)
			os.Exit(0)
		}
	}
}

func writeRunConfig(t *testing.T, cfg config.Config) string {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
