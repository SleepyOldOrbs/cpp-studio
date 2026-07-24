package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"cpp-studio/internal/config"
)

func TestManagerStartsCapturesLogsAndStops(t *testing.T) {
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"helper": {
				Command:                os.Args[0],
				Args:                   []string{"-test.run=TestHelperProcess", "--", "sleep"},
				StartupTimeoutSeconds:  2,
				ShutdownTimeoutSeconds: 2,
			},
		},
	}
	manager := NewManager(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}

	engine := waitForEngine(t, manager, "helper", func(engine EngineHealth) bool {
		return len(engine.LogTail) > 0 && engine.LogTail[0] == "helper ready"
	})
	if !engine.Ready {
		t.Fatalf("engine not ready: %+v", engine)
	}
	if engine.PID == 0 {
		t.Fatalf("expected child pid")
	}
	if len(engine.LogTail) == 0 || engine.LogTail[0] != "helper ready" {
		t.Fatalf("expected captured helper log, got %#v", engine.LogTail)
	}

	if err := manager.StopAll(ctx); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
}

func TestManagerWaitsForHTTPHealth(t *testing.T) {
	port := freePort(t)
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"http-helper": {
				Command:                os.Args[0],
				Args:                   []string{"-test.run=TestHelperProcess", "--", "http", fmt.Sprint(port)},
				HealthURL:              fmt.Sprintf("http://127.0.0.1:%d/health", port),
				StartupTimeoutSeconds:  5,
				ShutdownTimeoutSeconds: 2,
			},
		},
	}
	manager := NewManager(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	defer manager.StopAll(context.Background())

	health := manager.Health()
	engine := health.Engines["http-helper"]
	if engine.Status != StatusReady {
		t.Fatalf("expected ready status, got %+v", engine)
	}
}

func TestSubprocessEnginesAreNotStartedAsDaemons(t *testing.T) {
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"audio": {
				Command: os.Args[0],
				Args:    []string{"-test.run=TestHelperProcess", "--", "sleep"},
				Mode:    "subprocess",
			},
		},
	}
	manager := NewManager(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}

	engine := manager.Health().Engines["audio"]
	if !engine.Ready || engine.Status != StatusReady {
		t.Fatalf("expected subprocess engine to be ready without daemon start, got %+v", engine)
	}
	if engine.PID != 0 {
		t.Fatalf("subprocess engine should not have a daemon pid, got %+v", engine)
	}
}

func TestStopKillsProcessWithCanceledContext(t *testing.T) {
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"helper": {
				Command:                os.Args[0],
				Args:                   []string{"-test.run=TestHelperProcess", "--", "sleep"},
				StartupTimeoutSeconds:  2,
				ShutdownTimeoutSeconds: 2,
			},
		},
	}
	manager := NewManager(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}

	stopCtx, stopCancel := context.WithCancel(context.Background())
	stopCancel()
	if err := manager.Stop(stopCtx, "helper"); err != nil {
		t.Fatalf("Stop() with canceled context error = %v", err)
	}
	if engine := manager.Health().Engines["helper"]; engine.Status != StatusStopped {
		t.Fatalf("expected stopped status, got %+v", engine)
	}
}

func TestStartAllStopsEnginesOnStartupFailure(t *testing.T) {
	port := freePort(t)
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"a-ready": {
				Command:                os.Args[0],
				Args:                   []string{"-test.run=TestHelperProcess", "--", "sleep"},
				StartupTimeoutSeconds:  2,
				ShutdownTimeoutSeconds: 2,
			},
			"b-never-ready": {
				Command:                os.Args[0],
				Args:                   []string{"-test.run=TestHelperProcess", "--", "sleep"},
				HealthURL:              fmt.Sprintf("http://127.0.0.1:%d/health", port),
				StartupTimeoutSeconds:  1,
				ShutdownTimeoutSeconds: 2,
			},
		},
	}
	manager := NewManager(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := manager.StartAll(ctx)
	if err == nil {
		t.Fatalf("expected startup failure")
	}

	health := manager.Health()
	for name, engine := range health.Engines {
		if engine.Status != StatusStopped {
			t.Fatalf("engine %q was not stopped after rollback: %+v", name, engine)
		}
	}
}

func TestHealthDegradedPrecedence(t *testing.T) {
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"failed":   {Command: os.Args[0]},
			"starting": {Command: os.Args[0]},
		},
	}
	manager := NewManager(cfg)
	manager.mu.Lock()
	manager.engines["failed"].setStatusLocked(StatusFailed, "boom")
	manager.engines["starting"].setStatusLocked(StatusStarting, "")
	manager.mu.Unlock()

	health := manager.Health()
	if health.Status != "degraded" {
		t.Fatalf("expected degraded to win, got %+v", health)
	}
}

func TestCaptureLogsHandlesLongLines(t *testing.T) {
	engine := &engineProcess{logs: newLogRing(5)}
	line := strings.Repeat("x", 128*1024)

	engine.captureLogs(strings.NewReader(line + "\n"))

	logs := engine.logs.snapshot()
	if len(logs) != 1 || logs[0] != line {
		t.Fatalf("expected long log line to be captured, got %d lines", len(logs))
	}
}

func TestHealthJSONShape(t *testing.T) {
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"configured": {Command: os.Args[0]},
		},
	}
	manager := NewManager(cfg)
	data, err := json.Marshal(manager.Health())
	if err != nil {
		t.Fatalf("marshal health: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("empty health JSON")
	}
}

func TestCrashedEngineCanBeRestarted(t *testing.T) {
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"crasher": {
				Command:                os.Args[0],
				Args:                   []string{"-test.run=TestHelperProcess", "--", "crash"},
				StartupTimeoutSeconds:  2,
				ShutdownTimeoutSeconds: 2,
			},
		},
	}
	manager := NewManager(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.Start(ctx, "crasher"); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	waitForEngine(t, manager, "crasher", func(engine EngineHealth) bool {
		return engine.Status == StatusCrashed
	})

	// The crash must release the process slot so a restart is accepted
	// rather than rejected with "already started".
	if err := manager.Start(ctx, "crasher"); err != nil {
		t.Fatalf("restart after crash error = %v", err)
	}
	waitForEngine(t, manager, "crasher", func(engine EngineHealth) bool {
		return engine.Status == StatusCrashed
	})
	if err := manager.StopAll(ctx); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
}

func TestEngineSurvivesCallerContextCancellation(t *testing.T) {
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"helper": {
				Command:                os.Args[0],
				Args:                   []string{"-test.run=TestHelperProcess", "--", "sleep"},
				StartupTimeoutSeconds:  2,
				ShutdownTimeoutSeconds: 2,
			},
		},
	}
	manager := NewManager(cfg)

	// An HTTP handler's request context is canceled the moment the response
	// is written; the engine it started must keep running regardless.
	ctx, cancel := context.WithCancel(context.Background())
	if err := manager.Start(ctx, "helper"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	cancel()

	time.Sleep(300 * time.Millisecond)
	engine := manager.Health().Engines["helper"]
	if engine.Status != StatusRunning && engine.Status != StatusReady {
		t.Fatalf("engine did not survive caller cancellation: %+v", engine)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := manager.StopAll(stopCtx); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
}

func TestHelperProcess(t *testing.T) {
	args := os.Args
	for i, arg := range args {
		if arg == "--" && i+1 < len(args) {
			switch args[i+1] {
			case "sleep":
				fmt.Println("helper ready")
				time.Sleep(30 * time.Second)
				os.Exit(0)
			case "crash":
				fmt.Println("helper crashing")
				os.Exit(1)
			case "http":
				if i+2 >= len(args) {
					os.Exit(2)
				}
				mux := http.NewServeMux()
				mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte("ok"))
				})
				fmt.Println("http helper ready")
				_ = http.ListenAndServe("127.0.0.1:"+args[i+2], mux)
				os.Exit(0)
			}
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*netTCPAddr).Port
}

type netTCPAddr = net.TCPAddr

func waitForEngine(t *testing.T, manager *Manager, name string, ready func(EngineHealth) bool) EngineHealth {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var engine EngineHealth
	for time.Now().Before(deadline) {
		engine = manager.Health().Engines[name]
		if ready(engine) {
			return engine
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("engine %q did not reach expected state: %+v", name, engine)
	return EngineHealth{}
}
