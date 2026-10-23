package lifecycle

import (
	"context"
	"os"
	"testing"

	"cpp-studio/internal/config"
)

func TestSuspendedResidentResumesButExplicitStopStaysStopped(t *testing.T) {
	cfg := config.Config{Engines: map[string]config.EngineConfig{
		"speech": {Command: os.Args[0], Args: []string{"-test.run=TestHelperProcess", "--", "sleep"}, Mode: "server"},
	}}
	m := NewManager(cfg)
	ctx := context.Background()
	defer m.StopAll(ctx)
	if err := m.Start(ctx, "speech"); err != nil {
		t.Fatal(err)
	}
	if err := m.Suspend(ctx, "speech"); err != nil {
		t.Fatal(err)
	}
	if health := m.Health().Engines["speech"]; health.Ready || health.Status != StatusStopped {
		t.Fatal(health)
	}
	if err := m.ResumeSuspended(ctx, "speech"); err != nil {
		t.Fatal(err)
	}
	if health := m.Health().Engines["speech"]; health.PID == 0 || !health.Ready {
		t.Fatal(health)
	}
	if err := m.Suspend(ctx, "speech"); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx, "speech"); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeSuspended(ctx, "speech"); err != nil {
		t.Fatal(err)
	}
	if health := m.Health().Engines["speech"]; health.Ready || health.Status != StatusStopped {
		t.Fatal(health)
	}
}
