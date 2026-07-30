package lifecycle

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"cpp-studio/internal/config"
)

const defaultStartupTimeout = 30 * time.Second
const defaultShutdownTimeout = 10 * time.Second

type Status string

const (
	StatusConfigured        Status = "configured"
	StatusStarting          Status = "starting"
	StatusRunning           Status = "running"
	StatusReady             Status = "ready"
	StatusStopped           Status = "stopped"
	StatusExited            Status = "exited"
	StatusFailed            Status = "failed"
	StatusMissingExecutable Status = "missing_executable"
	StatusMissingModel      Status = "missing_model"
	StatusPortInUse         Status = "port_in_use"
	StatusCrashed           Status = "crashed"
)

type EngineHealth struct {
	Name   string `json:"name"`
	Mode   string `json:"mode,omitempty"`
	Status Status `json:"status"`
	// Variant names the active argument set of an engine that declares
	// variants — which model whisper or sd is actually serving right now.
	Variant string `json:"variant,omitempty"`
	// Remedy is the extra arguments a byom variant was loaded with (for
	// example "--cpu-moe"), so health can say not just which model is
	// serving but how it was made to fit. Empty when none.
	Remedy        string     `json:"remedy,omitempty"`
	PID           int        `json:"pid,omitempty"`
	Ready         bool       `json:"ready"`
	LastError     string     `json:"lastError,omitempty"`
	StartedAt     time.Time  `json:"startedAt,omitempty"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	LogTail       []string   `json:"logTail,omitempty"`
}

type GatewayHealth struct {
	Status    string                  `json:"status"`
	Engines   map[string]EngineHealth `json:"engines"`
	UpdatedAt time.Time               `json:"updatedAt"`
}

type Manager struct {
	mu      sync.Mutex
	engines map[string]*engineProcess
	client  *http.Client
}

type engineProcess struct {
	name   string
	cfg    config.EngineConfig
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan error
	health EngineHealth
	logs   *logRing
	// activeExtra holds the remedy arguments the current variant was
	// loaded with, so re-selecting the same variant without them is a
	// restart, not a no-op.
	activeExtra []string
}

func NewManager(cfg config.Config) *Manager {
	engines := make(map[string]*engineProcess, len(cfg.Engines))
	for name, engineCfg := range cfg.Engines {
		mode := engineCfg.Mode
		if mode == "" {
			mode = "server"
		}
		health := EngineHealth{
			Name:      name,
			Mode:      mode,
			Status:    StatusConfigured,
			UpdatedAt: time.Now().UTC(),
		}
		if engineCfg.Mode == "subprocess" {
			health.Status = StatusReady
			health.Ready = true
		}
		// An engine with variants boots on its default set; the active
		// variant's args are simply the engine's args from here on.
		if len(engineCfg.Variants) > 0 {
			health.Variant = engineCfg.DefaultVariant
			engineCfg.Args = append([]string{}, engineCfg.Variants[engineCfg.DefaultVariant].Args...)
		}
		engines[name] = &engineProcess{
			name:   name,
			cfg:    engineCfg,
			health: health,
			logs:   newLogRing(100),
		}
	}
	return &Manager{
		engines: engines,
		client:  &http.Client{Timeout: 2 * time.Second},
	}
}

// VariantInfo describes one selectable argument set of an engine, for the
// console's model pickers.
type VariantInfo struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Active bool   `json:"active"`
	// ModelPath is the model file behind a synthesized byom variant, for
	// the gateway's fit preflight. Never serialized; empty for configured
	// variants.
	ModelPath string `json:"-"`
}

// byomPrefix marks variants synthesized from files in an engine's byomDir
// rather than declared in config. The id is the prefix plus the bare
// filename, so the picker's value round-trips to a file without any
// server-side session state.
const byomPrefix = "byom:"

// Variants lists an engine's argument sets in a stable order, or reports
// that the engine has none: configured variants first, then one entry per
// *.gguf file in byomDir. The directory scan happens outside the manager
// lock — byomDir may live on a slow network drive, and a stalled listing
// must never stall /health.
func (m *Manager) Variants(name string) ([]VariantInfo, bool) {
	m.mu.Lock()
	engine, ok := m.engines[name]
	if !ok || len(engine.cfg.Variants) == 0 {
		m.mu.Unlock()
		return nil, false
	}
	ids := make([]string, 0, len(engine.cfg.Variants))
	for id := range engine.cfg.Variants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]VariantInfo, 0, len(ids))
	for _, id := range ids {
		label := engine.cfg.Variants[id].Label
		if label == "" {
			label = id
		}
		out = append(out, VariantInfo{ID: id, Label: label, Active: id == engine.health.Variant})
	}
	active := engine.health.Variant
	byomDir := engine.cfg.ByomDir
	m.mu.Unlock()

	if byomDir == "" {
		return out, true
	}
	entries, err := os.ReadDir(byomDir)
	if err != nil {
		// A missing or unreadable byom directory is not a degraded
		// engine; the configured variants simply stand alone.
		return out, true
	}
	for _, entry := range entries {
		fname := entry.Name()
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(fname), ".gguf") {
			continue
		}
		out = append(out, VariantInfo{
			ID:        byomPrefix + fname,
			Label:     strings.TrimSuffix(fname, filepath.Ext(fname)),
			Active:    byomPrefix+fname == active,
			ModelPath: filepath.Join(byomDir, fname),
		})
	}
	return out, true
}

// byomVariantArgs resolves a byom variant id to the args that launch it:
// the engine's byomArgs template with {model} replaced by the file's path.
// The id must name a plain *.gguf file directly inside byomDir — anything
// resembling a path is rejected, so a crafted id can never reach outside
// the directory the operator chose to expose.
func byomVariantArgs(cfg config.EngineConfig, id string) ([]string, error) {
	if cfg.ByomDir == "" {
		return nil, fmt.Errorf("engine has no byom directory")
	}
	name := strings.TrimPrefix(id, byomPrefix)
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) ||
		!strings.EqualFold(filepath.Ext(name), ".gguf") {
		return nil, fmt.Errorf("byom model name %q is invalid", name)
	}
	path := filepath.Join(cfg.ByomDir, name)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("byom model %q not found", name)
	}
	args := make([]string, len(cfg.ByomArgs))
	for i, arg := range cfg.ByomArgs {
		args[i] = strings.ReplaceAll(arg, "{model}", path)
	}
	return args, nil
}

// SetVariant switches an engine to another of its argument sets. A running
// server restarts with the new args — that is the whole point, the same
// binary serving a different model. A degraded engine (crashed, missing
// model) is also started: the user's swap is an instruction to make it
// work, not to relabel a corpse. Only a deliberately stopped engine stays
// stopped. Switching to the already-active variant is a no-op.
//
// If the new variant fails to start — the classic case is a model file
// that has not finished downloading — the engine reverts to the variant
// that was serving before, so one bad pick never leaves image generation
// dead until someone digs through the Engines tab.
//
// A byom:<file> id launches the engine's byomArgs template against that
// file. Extra args are remedy flags the caller vouches for (the gateway
// only ever passes server-defined remedies); the same variant with
// different extra args counts as a different selection.
func (m *Manager) SetVariant(ctx context.Context, name string, id string, extra ...string) error {
	m.mu.Lock()
	engine, ok := m.engines[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown engine %q", name)
	}
	if len(engine.cfg.Variants) == 0 {
		m.mu.Unlock()
		return fmt.Errorf("engine %q has no variants", name)
	}
	var args []string
	if strings.HasPrefix(id, byomPrefix) {
		byomArgs, err := byomVariantArgs(engine.cfg, id)
		if err != nil {
			m.mu.Unlock()
			return fmt.Errorf("engine %q: %w", name, err)
		}
		args = byomArgs
	} else if variant, ok := engine.cfg.Variants[id]; ok {
		args = append([]string{}, variant.Args...)
	} else {
		m.mu.Unlock()
		return fmt.Errorf("engine %q has no variant %q", name, id)
	}
	if engine.health.Variant == id && slices.Equal(engine.activeExtra, extra) {
		m.mu.Unlock()
		return nil
	}
	args = append(args, extra...)
	previous := engine.health.Variant
	previousArgs := engine.cfg.Args
	previousExtra := engine.activeExtra
	previousRemedy := engine.health.Remedy
	wasRunning := engine.cmd != nil && engine.cmd.Process != nil
	shouldRun := wasRunning || isDegradedStatus(engine.health.Status)
	engine.cfg.Args = args
	engine.activeExtra = append([]string{}, extra...)
	engine.health.Variant = id
	engine.health.Remedy = strings.Join(extra, " ")
	engine.health.UpdatedAt = time.Now().UTC()
	m.mu.Unlock()

	if !shouldRun {
		return nil
	}
	if wasRunning {
		if err := m.Stop(ctx, name); err != nil {
			return fmt.Errorf("stop %q to switch variant: %w", name, err)
		}
	}
	err := m.Start(ctx, name)
	if err == nil {
		return nil
	}

	// The new variant would not come up. Put the old one back — best
	// effort, but it was serving moments ago, so it usually will again.
	m.mu.Lock()
	engine.cfg.Args = previousArgs
	engine.activeExtra = previousExtra
	engine.health.Variant = previous
	engine.health.Remedy = previousRemedy
	engine.health.UpdatedAt = time.Now().UTC()
	m.mu.Unlock()
	if wasRunning {
		if revertErr := m.Start(ctx, name); revertErr != nil {
			return fmt.Errorf("variant %q failed to start (%v) and reverting to %q also failed: %w", id, err, previous, revertErr)
		}
		return fmt.Errorf("variant %q failed to start; reverted to %q: %w", id, previous, err)
	}
	return fmt.Errorf("variant %q failed to start: %w", id, err)
}

func (m *Manager) StartAll(ctx context.Context) error {
	for _, name := range m.engineNames() {
		if err := m.Start(ctx, name); err != nil {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), m.maxShutdownTimeout())
			stopErr := m.StopAll(rollbackCtx)
			cancel()
			if stopErr != nil {
				return errors.Join(err, fmt.Errorf("rollback stop: %w", stopErr))
			}
			return err
		}
	}
	return nil
}

func (m *Manager) Start(ctx context.Context, name string) error {
	m.mu.Lock()
	engine, ok := m.engines[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown engine %q", name)
	}
	if engine.cfg.Mode == "subprocess" {
		engine.setStatusLocked(StatusReady, "")
		m.mu.Unlock()
		return nil
	}
	if engine.cmd != nil && engine.cmd.Process != nil {
		m.mu.Unlock()
		return fmt.Errorf("engine %q already started", name)
	}

	engine.setStatusLocked(StatusStarting, "")
	// The child's lifetime belongs to the manager, never to the caller: a
	// request-scoped ctx is canceled when its HTTP handler returns, which
	// would silently kill the engine moments after a successful start. The
	// caller's ctx still bounds the readiness wait below; Stop and shutdown
	// kill the process through the manager-owned cancel.
	cmdCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(cmdCtx, engine.cfg.Command, engine.cfg.Args...)
	cmd.Dir = engine.cfg.WorkingDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		engine.setStatusLocked(StatusFailed, err.Error())
		m.mu.Unlock()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		engine.setStatusLocked(StatusFailed, err.Error())
		m.mu.Unlock()
		return err
	}
	engine.cmd = cmd
	engine.cancel = cancel
	engine.done = make(chan error, 1)
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		engine.setStatusLocked(classifyStartError(err), err.Error())
		m.mu.Unlock()
		cancel()
		return err
	}

	m.mu.Lock()
	engine.health.PID = cmd.Process.Pid
	engine.health.StartedAt = time.Now().UTC()
	engine.setStatusLocked(StatusRunning, "")
	m.mu.Unlock()

	go engine.captureLogs(stdout)
	go engine.captureLogs(stderr)
	go m.watchExit(engine, cmd, engine.done)

	if engine.cfg.HealthURL == "" {
		return nil
	}
	if err := m.waitReady(ctx, engine); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), durationSeconds(engine.cfg.ShutdownTimeoutSeconds, defaultShutdownTimeout))
		stopErr := m.Stop(rollbackCtx, name)
		cancel()
		if stopErr != nil {
			return errors.Join(err, fmt.Errorf("rollback stop: %w", stopErr))
		}
		return err
	}
	return nil
}

func (m *Manager) StopAll(ctx context.Context) error {
	var errs []error
	for _, name := range m.engineNames() {
		if err := m.Stop(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) Stop(ctx context.Context, name string) error {
	m.mu.Lock()
	engine, ok := m.engines[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown engine %q", name)
	}
	cmd := engine.cmd
	cancel := engine.cancel
	done := engine.done
	timeout := durationSeconds(engine.cfg.ShutdownTimeoutSeconds, defaultShutdownTimeout)
	m.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if cancel != nil {
		cancel()
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-done
	case <-done:
	}

	m.mu.Lock()
	// Only finalize if no newer process claimed the slot while this stop was
	// waiting; watchExit clears the fields when the process exits, and a
	// concurrent Start may already have installed a replacement.
	if engine.cmd == nil || engine.cmd == cmd {
		engine.setStatusLocked(StatusStopped, "")
		engine.cmd = nil
		engine.cancel = nil
		engine.done = nil
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) Health() GatewayHealth {
	m.mu.Lock()
	defer m.mu.Unlock()

	engines := make(map[string]EngineHealth, len(m.engines))
	degraded := false
	starting := false
	for name, engine := range m.engines {
		health := engine.health
		health.LogTail = engine.logs.snapshot()
		engines[name] = health
		if isDegradedStatus(health.Status) {
			degraded = true
		} else if !health.Ready && health.Status != StatusStopped {
			// A deliberately stopped engine (VRAM profiles) is neutral: it is
			// neither on its way up nor degraded.
			starting = true
		}
	}
	overall := "ready"
	if degraded {
		overall = "degraded"
	} else if starting {
		overall = "starting"
	}
	return GatewayHealth{
		Status:    overall,
		Engines:   engines,
		UpdatedAt: time.Now().UTC(),
	}
}

func (m *Manager) MarkSuccess(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	engine, ok := m.engines[name]
	if !ok {
		return
	}
	now := time.Now().UTC()
	engine.health.LastSuccessAt = &now
	if engine.cfg.Mode == "subprocess" {
		engine.health.Status = StatusReady
		engine.health.Ready = true
		engine.health.LastError = ""
	}
	engine.health.UpdatedAt = now
}

func (m *Manager) MarkFailure(name string, status Status, lastErr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	engine, ok := m.engines[name]
	if !ok {
		return
	}
	engine.setStatusLocked(status, lastErr)
}

func (m *Manager) waitReady(ctx context.Context, engine *engineProcess) error {
	timeout := durationSeconds(engine.cfg.StartupTimeoutSeconds, defaultStartupTimeout)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			err := fmt.Errorf("engine %q health check timed out", engine.name)
			m.mu.Lock()
			engine.setStatusLocked(StatusFailed, err.Error())
			m.mu.Unlock()
			return err
		case <-tick.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, engine.cfg.HealthURL, nil)
			if err != nil {
				return err
			}
			resp, err := m.client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					m.mu.Lock()
					engine.health.Ready = true
					engine.setStatusLocked(StatusReady, "")
					m.mu.Unlock()
					return nil
				}
			}
		}
	}
}

func (m *Manager) watchExit(engine *engineProcess, cmd *exec.Cmd, done chan error) {
	err := cmd.Wait()
	done <- err
	close(done)

	m.mu.Lock()
	defer m.mu.Unlock()
	if engine.cmd != cmd {
		return
	}
	// The process is gone; release the slot so Start can relaunch a crashed
	// or exited engine. Stop's captured cmd/done references stay valid.
	engine.cmd = nil
	engine.cancel = nil
	engine.done = nil
	if engine.health.Status == StatusStopped {
		return
	}
	if err != nil {
		engine.setStatusLocked(classifyRuntimeError(err, engine.logs.snapshot()), err.Error())
		return
	}
	engine.setStatusLocked(StatusExited, "")
}

func (e *engineProcess) captureLogs(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		e.logs.add(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		e.logs.add("log capture error: " + err.Error())
	}
}

func (e *engineProcess) setStatusLocked(status Status, lastErr string) {
	e.health.Status = status
	e.health.Ready = status == StatusReady || (status == StatusRunning && e.cfg.HealthURL == "")
	e.health.LastError = lastErr
	e.health.UpdatedAt = time.Now().UTC()
}

func classifyStartError(err error) Status {
	if errors.Is(err, exec.ErrNotFound) {
		return StatusMissingExecutable
	}
	return StatusFailed
}

func classifyRuntimeError(err error, logs []string) Status {
	text := strings.ToLower(err.Error() + "\n" + strings.Join(logs, "\n"))
	switch {
	case strings.Contains(text, "address already in use"),
		strings.Contains(text, "bind:"),
		strings.Contains(text, "only one usage of each socket address"):
		return StatusPortInUse
	case strings.Contains(text, "model") && (strings.Contains(text, "not found") || strings.Contains(text, "no such file")):
		return StatusMissingModel
	default:
		return StatusCrashed
	}
}

func isDegradedStatus(status Status) bool {
	switch status {
	case StatusFailed, StatusExited, StatusMissingExecutable, StatusMissingModel, StatusPortInUse, StatusCrashed:
		return true
	default:
		return false
	}
}

func durationSeconds(value int, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Second
}

func (m *Manager) engineNames() []string {
	names := make([]string, 0, len(m.engines))
	for name := range m.engines {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *Manager) maxShutdownTimeout() time.Duration {
	maxTimeout := defaultShutdownTimeout
	for _, engine := range m.engines {
		timeout := durationSeconds(engine.cfg.ShutdownTimeoutSeconds, defaultShutdownTimeout)
		if timeout > maxTimeout {
			maxTimeout = timeout
		}
	}
	return maxTimeout
}

type logRing struct {
	mu    sync.Mutex
	lines []string
	limit int
}

func newLogRing(limit int) *logRing {
	return &logRing{limit: limit}
}

func (r *logRing) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
	if len(r.lines) > r.limit {
		copy(r.lines, r.lines[len(r.lines)-r.limit:])
		r.lines = r.lines[:r.limit]
	}
}

func (r *logRing) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}
