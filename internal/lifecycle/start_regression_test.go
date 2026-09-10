package lifecycle

import (
	"context"
	"cpp-studio/internal/config"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentStartsOwnExactlyOneProcess(t *testing.T) {
	m := NewManager(config.Config{Engines: map[string]config.EngineConfig{"helper": {Command: os.Args[0], Args: []string{"-test.run=TestHelperProcess", "--", "sleep"}}}})
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	start := make(chan struct{})
	results := make(chan error, 16)
	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() { defer group.Done(); <-start; results <- m.Start(context.Background(), "helper") }()
	}
	close(start)
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful starts, want exactly one", successes)
	}
	if err := m.Stop(context.Background(), "helper"); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background(), "helper"); err != nil {
		t.Fatalf("restart: %v", err)
	}
}

func TestFailedStartReleasesProcessSlot(t *testing.T) {
	m := NewManager(config.Config{Engines: map[string]config.EngineConfig{"helper": {Command: filepath.Join(t.TempDir(), "missing")}}})
	if err := m.Start(context.Background(), "helper"); err == nil {
		t.Fatal("expected failed start")
	}
	m.mu.Lock()
	e := m.engines["helper"]
	cleared := e.cmd == nil && e.cancel == nil && e.done == nil
	e.cfg.Command = os.Args[0]
	e.cfg.Args = []string{"-test.run=TestHelperProcess", "--", "sleep"}
	m.mu.Unlock()
	if !cleared {
		t.Fatal("failed start retained ownership")
	}
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	if err := m.Start(context.Background(), "helper"); err != nil {
		t.Fatal(err)
	}
}
