package voice

import (
	"context"
	"fmt"
	"sort"

	"cpp-studio/internal/config"
	"cpp-studio/internal/engine"
	"cpp-studio/internal/lifecycle"
)

// EngineLifecycle is the lifecycle control needed to prepare one voice turn.
type EngineLifecycle interface {
	Health() lifecycle.GatewayHealth
	Suspend(context.Context, string) error
	Start(context.Context, string) error
}

// PrepareEngines holds the existing engine reservations through the entire turn.
// All reservations are acquired before any process changes, so active jobs are
// never interrupted. The turn must invoke its engines through RunReserved.
func PrepareEngines(ctx context.Context, invoker engine.Invoker, manager EngineLifecycle, configs map[string]config.EngineConfig, speech string, recorded bool) (func(), error) {
	keep := map[string]bool{"llama": true, speech: true}
	if recorded {
		keep["whisper"] = true
	}
	for name := range keep {
		if _, ok := configs[name]; !ok {
			return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: fmt.Sprintf("engine %q is not configured", name)}
		}
	}
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)
	var releases []func()
	release := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, name := range names {
		unlock, ok := invoker.Reserve(name)
		if !ok {
			release()
			return nil, &engine.Error{Kind: engine.KindBusy, Message: fmt.Sprintf("engine %q is busy; finish the active task before starting voice chat", name)}
		}
		releases = append(releases, unlock)
	}
	fail := func(err error) (func(), error) {
		release()
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: "prepare voice chat: " + err.Error()}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	health := manager.Health()
	// Free unrelated residents before loading the selected speech engine.
	for _, name := range names {
		if !keep[name] && configs[name].Mode != "subprocess" && health.Engines[name].PID != 0 && health.Engines[name].Status != lifecycle.StatusStopped {
			if err := manager.Suspend(ctx, name); err != nil {
				return fail(err)
			}
		}
	}
	for _, name := range names {
		if keep[name] && configs[name].Mode != "subprocess" && health.Engines[name].Status == lifecycle.StatusStopped {
			if err := manager.Start(ctx, name); err != nil {
				return fail(err)
			}
		}
	}
	return release, nil
}
