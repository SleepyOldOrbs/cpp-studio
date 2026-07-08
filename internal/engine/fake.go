package engine

import (
	"context"
	"fmt"
	"sync"
)

// Fake is the in-memory adapter at the engine seam. Tests register one
// handler per engine name; Run records every spec it receives.
type Fake struct {
	mu       sync.Mutex
	busy     map[string]bool
	handlers map[string]func(spec Spec) (Result, error)
	calls    []Spec
}

func NewFake() *Fake {
	return &Fake{
		busy:     make(map[string]bool),
		handlers: make(map[string]func(spec Spec) (Result, error)),
	}
}

// Handle registers the behaviour for one engine name.
func (f *Fake) Handle(name string, handler func(spec Spec) (Result, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[name] = handler
}

// Calls returns the specs Run has received, in order.
func (f *Fake) Calls() []Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Spec{}, f.calls...)
}

func (f *Fake) Reserve(name string) (func(), bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy[name] {
		return nil, false
	}
	f.busy[name] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.busy[name] = false
		})
	}, true
}

func (f *Fake) Run(ctx context.Context, spec Spec) (Result, error) {
	f.mu.Lock()
	handler, ok := f.handlers[spec.Engine]
	f.calls = append(f.calls, spec)
	f.mu.Unlock()
	if !ok {
		return Result{}, &Error{Kind: KindNotConfigured, Message: fmt.Sprintf("engine %q is not configured", spec.Engine)}
	}
	release, reserved := f.Reserve(spec.Engine)
	if !reserved {
		return Result{}, &Error{Kind: KindBusy, Message: fmt.Sprintf("engine %q is busy", spec.Engine)}
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return Result{}, &Error{Kind: KindEngineFailure, Message: err.Error()}
	}
	return handler(spec)
}
