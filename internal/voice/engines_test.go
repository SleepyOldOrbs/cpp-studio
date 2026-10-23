package voice

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"cpp-studio/internal/config"
	"cpp-studio/internal/engine"
	"cpp-studio/internal/lifecycle"
)

type voiceLifecycle struct {
	health    lifecycle.GatewayHealth
	calls     []string
	failStart bool
}

func (m *voiceLifecycle) Health() lifecycle.GatewayHealth { return m.health }
func (m *voiceLifecycle) Suspend(_ context.Context, name string) error {
	m.calls = append(m.calls, "stop "+name)
	m.health.Engines[name] = lifecycle.EngineHealth{Status: lifecycle.StatusStopped}
	return nil
}
func (m *voiceLifecycle) Start(_ context.Context, name string) error {
	m.calls = append(m.calls, "start "+name)
	if m.failStart {
		return errors.New("load failed")
	}
	m.health.Engines[name] = lifecycle.EngineHealth{Status: lifecycle.StatusReady, PID: 1}
	return nil
}
func voiceEngineFixture() (map[string]config.EngineConfig, *engine.Fake, *voiceLifecycle) {
	configs := map[string]config.EngineConfig{}
	m := &voiceLifecycle{health: lifecycle.GatewayHealth{Engines: map[string]lifecycle.EngineHealth{}}}
	for _, name := range []string{"audio", "llama", "sd", "vision", "whisper"} {
		configs[name] = config.EngineConfig{Mode: "server"}
		m.health.Engines[name] = lifecycle.EngineHealth{Status: lifecycle.StatusReady, PID: 1}
	}
	configs["omnivoice"] = config.EngineConfig{Mode: "subprocess"}
	return configs, engine.NewFake(), m
}

func TestVoiceEngineSetMatchesTypedAndRecordedTurns(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		configs, invoker, manager := voiceEngineFixture()
		release, err := PrepareEngines(context.Background(), invoker, manager, configs, "omnivoice", recorded)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"stop audio", "stop sd", "stop vision"}
		if !recorded {
			want = append(want, "stop whisper")
		}
		if !reflect.DeepEqual(manager.calls, want) {
			t.Fatalf("calls = %v, want %v", manager.calls, want)
		}
		for name := range configs {
			if unlock, ok := invoker.Reserve(name); ok {
				unlock()
				t.Fatalf("%s was not held for the turn", name)
			}
		}
		release()
		for name := range configs {
			unlock, ok := invoker.Reserve(name)
			if !ok {
				t.Fatalf("%s reservation leaked", name)
			}
			unlock()
		}
	}
}

func TestVoiceEnginePreparationDoesNotInterruptBusyWork(t *testing.T) {
	configs, invoker, manager := voiceEngineFixture()
	unlock, _ := invoker.Reserve("vision")
	defer unlock()
	_, err := PrepareEngines(context.Background(), invoker, manager, configs, "omnivoice", false)
	var engErr *engine.Error
	if !errors.As(err, &engErr) || engErr.Kind != engine.KindBusy {
		t.Fatalf("error = %v", err)
	}
	if len(manager.calls) != 0 {
		t.Fatalf("changed engines during active work: %v", manager.calls)
	}
	if release, ok := invoker.Reserve("audio"); !ok {
		t.Fatal("partial reservation leaked")
	} else {
		release()
	}
}

func TestVoiceEnginePreparationStopsBeforeLoadingAndReleasesOnFailure(t *testing.T) {
	configs, invoker, manager := voiceEngineFixture()
	manager.health.Engines["audio"] = lifecycle.EngineHealth{Status: lifecycle.StatusStopped}
	manager.failStart = true
	_, err := PrepareEngines(context.Background(), invoker, manager, configs, "audio", false)
	if err == nil {
		t.Fatal("expected load failure")
	}
	want := []string{"stop sd", "stop vision", "stop whisper", "start audio"}
	if !reflect.DeepEqual(manager.calls, want) {
		t.Fatalf("calls = %v", manager.calls)
	}
	for name := range configs {
		release, ok := invoker.Reserve(name)
		if !ok {
			t.Fatalf("%s reservation leaked", name)
		}
		release()
	}
}

func TestVoiceEnginePreparationCancellationChangesNothing(t *testing.T) {
	configs, invoker, manager := voiceEngineFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PrepareEngines(ctx, invoker, manager, configs, "audio", false); err == nil {
		t.Fatal("expected cancellation")
	}
	if len(manager.calls) != 0 {
		t.Fatal(manager.calls)
	}
}
