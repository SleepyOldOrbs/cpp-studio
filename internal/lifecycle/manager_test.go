package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
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

func TestSetVariantSwapsArgsAndRestartsARunningServer(t *testing.T) {
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"whisper": {
				Command:        os.Args[0],
				Mode:           "server",
				DefaultVariant: "large",
				Variants: map[string]config.EngineVariant{
					"large": {Label: "large-v3 (best)", Args: []string{"-test.run=TestHelperProcess", "--", "sleep"}},
					"turbo": {Args: []string{"-test.run=TestHelperProcess", "--", "sleep", "turbo"}},
				},
				StartupTimeoutSeconds:  2,
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

	// Boots on the default variant, and the listing knows which is active.
	engine := waitForEngine(t, manager, "whisper", func(e EngineHealth) bool { return e.PID != 0 })
	if engine.Variant != "large" {
		t.Fatalf("expected the default variant active, got %q", engine.Variant)
	}
	variants, ok := manager.Variants("whisper")
	if !ok || len(variants) != 2 {
		t.Fatalf("expected two variants, got %+v ok=%v", variants, ok)
	}
	if variants[0].ID != "large" || !variants[0].Active || variants[0].Label != "large-v3 (best)" {
		t.Fatalf("unexpected first variant %+v", variants[0])
	}
	if variants[1].ID != "turbo" || variants[1].Active || variants[1].Label != "turbo" {
		t.Fatalf("unexpected second variant %+v", variants[1])
	}
	firstPID := engine.PID

	// Switching restarts the server on the new args: same engine name, new
	// process, new active variant.
	if err := manager.SetVariant(ctx, "whisper", "turbo"); err != nil {
		t.Fatalf("SetVariant returned error: %v", err)
	}
	engine = waitForEngine(t, manager, "whisper", func(e EngineHealth) bool { return e.PID != 0 && e.PID != firstPID })
	if engine.Variant != "turbo" {
		t.Fatalf("expected turbo active after the switch, got %q", engine.Variant)
	}

	// Re-selecting the active variant must not churn the process.
	secondPID := engine.PID
	if err := manager.SetVariant(ctx, "whisper", "turbo"); err != nil {
		t.Fatalf("SetVariant no-op returned error: %v", err)
	}
	if got := manager.Health().Engines["whisper"].PID; got != secondPID {
		t.Fatalf("re-selecting the active variant restarted the engine: %d -> %d", secondPID, got)
	}

	if err := manager.SetVariant(ctx, "whisper", "nonexistent"); err == nil {
		t.Fatalf("expected an unknown variant to be refused")
	}
	if _, ok := manager.Variants("nope"); ok {
		t.Fatalf("expected no variants for an unknown engine")
	}
}

func TestSetVariantOnAStoppedEngineJustSwaps(t *testing.T) {
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"whisper": {
				Command:        os.Args[0],
				Mode:           "server",
				DefaultVariant: "large",
				Variants: map[string]config.EngineVariant{
					"large": {Args: []string{"-test.run=TestHelperProcess", "--", "sleep"}},
					"turbo": {Args: []string{"-test.run=TestHelperProcess", "--", "sleep", "turbo"}},
				},
				StartupTimeoutSeconds:  2,
				ShutdownTimeoutSeconds: 2,
			},
		},
	}
	manager := NewManager(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Never started: the swap records the choice and nothing launches.
	if err := manager.SetVariant(ctx, "whisper", "turbo"); err != nil {
		t.Fatalf("SetVariant returned error: %v", err)
	}
	engine := manager.Health().Engines["whisper"]
	if engine.Variant != "turbo" || engine.PID != 0 {
		t.Fatalf("expected a silent swap on a stopped engine, got %+v", engine)
	}
}

func TestSetVariantRevertsWhenTheNewVariantCannotStart(t *testing.T) {
	port := freePort(t)
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"sd": {
				Command:        os.Args[0],
				Mode:           "server",
				HealthURL:      fmt.Sprintf("http://127.0.0.1:%d/health", port),
				DefaultVariant: "works",
				Variants: map[string]config.EngineVariant{
					// The working variant serves the health endpoint; the
					// broken one just sleeps, so its health check times out —
					// the same shape as a model file that is not there yet.
					"works":  {Args: []string{"-test.run=TestHelperProcess", "--", "http", fmt.Sprint(port)}},
					"broken": {Args: []string{"-test.run=TestHelperProcess", "--", "sleep"}},
				},
				StartupTimeoutSeconds:  2,
				ShutdownTimeoutSeconds: 2,
			},
		},
	}
	manager := NewManager(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	defer manager.StopAll(context.Background())

	err := manager.SetVariant(ctx, "sd", "broken")
	if err == nil {
		t.Fatalf("expected the broken variant to fail")
	}
	if !strings.Contains(err.Error(), "reverted to \"works\"") {
		t.Fatalf("expected the error to say it reverted, got %v", err)
	}
	engine := waitForEngine(t, manager, "sd", func(e EngineHealth) bool { return e.Ready })
	if engine.Variant != "works" {
		t.Fatalf("expected the previous variant back in service, got %q", engine.Variant)
	}
}

// byomTestConfig builds a helper-process engine whose byomDir is a temp
// directory seeded with the given files.
func byomTestConfig(t *testing.T, files ...string) (config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stub"), 0o644); err != nil {
			t.Fatalf("seed byom file: %v", err)
		}
	}
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"llama": {
				Command:        os.Args[0],
				Mode:           "server",
				DefaultVariant: "base",
				Variants: map[string]config.EngineVariant{
					"base": {Label: "studio default", Args: []string{"-test.run=TestHelperProcess", "--", "sleep"}},
				},
				ByomDir:                dir,
				ByomArgs:               []string{"-test.run=TestHelperProcess", "--", "sleep", "{model}"},
				StartupTimeoutSeconds:  2,
				ShutdownTimeoutSeconds: 2,
			},
		},
	}
	return cfg, dir
}

func TestVariantsListsByomModelsAfterConfiguredOnes(t *testing.T) {
	cfg, dir := byomTestConfig(t, "beta.gguf", "Alpha.GGUF", "notes.txt")
	if err := os.Mkdir(filepath.Join(dir, "nested.gguf"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(cfg)

	variants, ok := manager.Variants("llama")
	if !ok || len(variants) != 3 {
		t.Fatalf("expected base + two byom entries, got %+v ok=%v", variants, ok)
	}
	if variants[0].ID != "base" || !variants[0].Active || variants[0].ModelPath != "" {
		t.Fatalf("expected the configured variant first and active, got %+v", variants[0])
	}
	if variants[1].ID != "byom:Alpha.GGUF" || variants[1].Label != "Alpha" {
		t.Fatalf("unexpected first byom entry %+v", variants[1])
	}
	if variants[2].ID != "byom:beta.gguf" || variants[2].ModelPath != filepath.Join(dir, "beta.gguf") {
		t.Fatalf("unexpected second byom entry %+v", variants[2])
	}
}

func TestVariantsIgnoresAMissingByomDir(t *testing.T) {
	cfg, dir := byomTestConfig(t)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(cfg)
	variants, ok := manager.Variants("llama")
	if !ok || len(variants) != 1 || variants[0].ID != "base" {
		t.Fatalf("expected the configured variant alone, got %+v ok=%v", variants, ok)
	}
}

func TestSetVariantStartsAByomModelWithSubstitutedArgs(t *testing.T) {
	cfg, dir := byomTestConfig(t, "brought.gguf")
	manager := NewManager(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	defer manager.StopAll(context.Background())
	first := waitForEngine(t, manager, "llama", func(e EngineHealth) bool { return e.PID != 0 })

	if err := manager.SetVariant(ctx, "llama", "byom:brought.gguf"); err != nil {
		t.Fatalf("SetVariant returned error: %v", err)
	}
	engine := waitForEngine(t, manager, "llama", func(e EngineHealth) bool { return e.PID != 0 && e.PID != first.PID })
	if engine.Variant != "byom:brought.gguf" {
		t.Fatalf("expected the byom variant active, got %q", engine.Variant)
	}
	manager.mu.Lock()
	args := append([]string{}, manager.engines["llama"].cfg.Args...)
	manager.mu.Unlock()
	want := filepath.Join(dir, "brought.gguf")
	if args[len(args)-1] != want {
		t.Fatalf("expected the model path substituted into args, got %v", args)
	}
}

func TestSetVariantRejectsByomTraversalAndNonGGUFNames(t *testing.T) {
	cfg, _ := byomTestConfig(t, "real.gguf", "notes.txt")
	manager := NewManager(cfg)
	ctx := context.Background()

	bad := []string{
		"byom:",
		"byom:../real.gguf",
		"byom:..\\real.gguf",
		"byom:sub/real.gguf",
		"byom:sub\\real.gguf",
		"byom:notes.txt",
	}
	for _, id := range bad {
		if err := manager.SetVariant(ctx, "llama", id); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Fatalf("expected %q to be rejected as invalid, got %v", id, err)
		}
	}
}

func TestSetVariantRejectsAMissingByomFile(t *testing.T) {
	cfg, _ := byomTestConfig(t)
	manager := NewManager(cfg)
	if err := manager.SetVariant(context.Background(), "llama", "byom:ghost.gguf"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a missing byom file to be refused, got %v", err)
	}
}

func TestSetVariantAppliesAndClearsExtraArgs(t *testing.T) {
	cfg, _ := byomTestConfig(t, "moe.gguf")
	manager := NewManager(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	defer manager.StopAll(context.Background())
	first := waitForEngine(t, manager, "llama", func(e EngineHealth) bool { return e.PID != 0 })

	if err := manager.SetVariant(ctx, "llama", "byom:moe.gguf", "--cpu-moe"); err != nil {
		t.Fatalf("SetVariant with extra returned error: %v", err)
	}
	engine := waitForEngine(t, manager, "llama", func(e EngineHealth) bool { return e.PID != 0 && e.PID != first.PID })
	if engine.Remedy != "--cpu-moe" {
		t.Fatalf("expected the remedy recorded in health, got %+v", engine)
	}
	manager.mu.Lock()
	args := append([]string{}, manager.engines["llama"].cfg.Args...)
	manager.mu.Unlock()
	if args[len(args)-1] != "--cpu-moe" {
		t.Fatalf("expected the remedy appended to args, got %v", args)
	}

	// Same selection again: a no-op, same process.
	remedyPID := engine.PID
	if err := manager.SetVariant(ctx, "llama", "byom:moe.gguf", "--cpu-moe"); err != nil {
		t.Fatalf("SetVariant no-op returned error: %v", err)
	}
	if got := manager.Health().Engines["llama"].PID; got != remedyPID {
		t.Fatalf("re-selecting the same remedy restarted the engine: %d -> %d", remedyPID, got)
	}

	// Same model without the remedy: that is a different launch — restart.
	if err := manager.SetVariant(ctx, "llama", "byom:moe.gguf"); err != nil {
		t.Fatalf("SetVariant without extra returned error: %v", err)
	}
	engine = waitForEngine(t, manager, "llama", func(e EngineHealth) bool { return e.PID != 0 && e.PID != remedyPID })
	if engine.Remedy != "" {
		t.Fatalf("expected the remedy cleared, got %+v", engine)
	}
}

func TestSetVariantRevertsWhenTheByomModelCannotStart(t *testing.T) {
	port := freePort(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.gguf"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]config.EngineConfig{
			"llama": {
				Command:        os.Args[0],
				Mode:           "server",
				HealthURL:      fmt.Sprintf("http://127.0.0.1:%d/health", port),
				DefaultVariant: "works",
				Variants: map[string]config.EngineVariant{
					"works": {Args: []string{"-test.run=TestHelperProcess", "--", "http", fmt.Sprint(port)}},
				},
				// The byom launch just sleeps, so its health check times
				// out — the same shape as a model llama-server rejects.
				ByomDir:                dir,
				ByomArgs:               []string{"-test.run=TestHelperProcess", "--", "sleep", "{model}"},
				StartupTimeoutSeconds:  2,
				ShutdownTimeoutSeconds: 2,
			},
		},
	}
	manager := NewManager(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := manager.StartAll(ctx); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	defer manager.StopAll(context.Background())

	err := manager.SetVariant(ctx, "llama", "byom:broken.gguf", "--cpu-moe")
	if err == nil {
		t.Fatalf("expected the byom variant to fail")
	}
	if !strings.Contains(err.Error(), "reverted to \"works\"") {
		t.Fatalf("expected the error to say it reverted, got %v", err)
	}
	engine := waitForEngine(t, manager, "llama", func(e EngineHealth) bool { return e.Ready })
	if engine.Variant != "works" || engine.Remedy != "" {
		t.Fatalf("expected the previous variant back with no remedy, got %+v", engine)
	}
}
