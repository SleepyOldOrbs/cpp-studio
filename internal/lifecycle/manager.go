package lifecycle

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
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
	Name          string     `json:"name"`
	Mode          string     `json:"mode,omitempty"`
	Status        Status     `json:"status"`
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
