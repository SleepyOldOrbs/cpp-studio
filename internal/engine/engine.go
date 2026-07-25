// Package engine owns the invocation contract for the native *.cpp engines:
// which CLI flags each engine kind takes, what output a run must produce, and
// how a single run reserves the engine, stages temp files, invokes the
// command, validates the result, and records success or failure.
//
// The Invoker interface is the seam the gateway and the voice loop cross;
// Runner is the subprocess adapter and Fake is the in-memory test adapter.
package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"cpp-studio/internal/config"
	"cpp-studio/internal/lifecycle"
)

const maxSubprocessLogBytes = 1024 * 1024

// FailureKind classifies a failed run so the HTTP layer can map it to a
// status code without knowing how the run failed internally.
type FailureKind int

const (
	// KindNotConfigured: the named engine is absent from the config.
	KindNotConfigured FailureKind = iota
	// KindBusy: the engine is reserved by another run.
	KindBusy
	// KindInternal: temp files could not be staged.
	KindInternal
	// KindInvalidInput: the caller-supplied input failed validation.
	KindInvalidInput
	// KindEngineFailure: the engine crashed or produced invalid output.
	KindEngineFailure
)

// Error is the failure surface of a run.
type Error struct {
	Kind    FailureKind
	Message string
}

func (e *Error) Error() string { return e.Message }

// Spec describes one engine invocation. Construct specs with SpeechSpec,
// TranscriptionSpec, or ImageSpec so the per-engine CLI contract stays in
// this package.
type Spec struct {
	// Engine is the config name of the engine, e.g. "audio".
	Engine string
	// Label prefixes failure messages, e.g. "audio speech command".
	Label string
	// Timeout applies when the engine config has no requestTimeoutSeconds.
	Timeout time.Duration
	// Input, when non-nil, is written to a temp file before the run.
	Input []byte
	// InputPattern names the temp input file (os.CreateTemp pattern).
	InputPattern string
	// InputPath and OutputPath name real files the engine reads and writes
	// directly, instead of round-tripping their contents through Input and
	// Result.Output. Use them when the payload is a whole recording rather
	// than a sentence: transcoding a half-hour audiobook has no reason to
	// hold it in memory twice. When OutputPath is set the runner leaves the
	// file where it is and Result.Output stays nil.
	InputPath  string
	OutputPath string
	// ValidateInput rejects bad input before the engine runs (KindInvalidInput).
	ValidateInput func(path string) error
	// OutputPattern, when non-empty, creates a temp output file whose bytes
	// are read back into Result.Output after validation.
	OutputPattern string
	// OutputLabel names the output in read-failure messages, e.g. "generated wav".
	OutputLabel string
	// ValidateOutput rejects invalid engine output (KindEngineFailure).
	ValidateOutput func(path string) error
	// BuildArgs produces the extra CLI args; inPath/outPath are "" when unused.
	BuildArgs func(inPath, outPath string) []string
	// OverrideArgs replaces the value that follows a matching flag in the
	// configured args, or appends the flag+value pair when the flag is absent.
	// This is how a per-run value beats a config default even for engines
	// whose parsers take the first occurrence of a flag (audiocpp_cli).
	OverrideArgs map[string]string
}

// Result is the outcome of a successful run.
type Result struct {
	Stdout  []byte
	Stderr  []byte
	Output  []byte
	Elapsed time.Duration
}

// Invoker is the seam callers cross to run an engine once or reserve it.
// RunReserved is Run for callers that already hold the engine's slot via
// Reserve.
type Invoker interface {
	Run(ctx context.Context, spec Spec) (Result, error)
	RunReserved(ctx context.Context, spec Spec) (Result, error)
	Reserve(name string) (release func(), ok bool)
}

// StatusRecorder receives run outcomes; *lifecycle.Manager satisfies it.
type StatusRecorder interface {
	MarkSuccess(name string)
	MarkFailure(name string, status lifecycle.Status, lastErr string)
}

// Runner is the subprocess adapter: it satisfies Invoker by launching the
// configured engine command once per run.
type Runner struct {
	engines  map[string]config.EngineConfig
	recorder StatusRecorder
	busy     map[string]chan struct{}
	gpu      chan struct{}
}

func NewRunner(engines map[string]config.EngineConfig, recorder StatusRecorder) *Runner {
	busy := make(map[string]chan struct{}, len(engines))
	for name := range engines {
		busy[name] = make(chan struct{}, 1)
	}
	return &Runner{engines: engines, recorder: recorder, busy: busy, gpu: make(chan struct{}, 1)}
}

// acquireGPU serializes runs of engines marked gpu: true across engine
// names, so two heavy GPU jobs (e.g. sd and audio) never race for VRAM.
// Unlike the per-engine slot it waits instead of failing, turning a
// cross-engine collision into brief queueing.
func (r *Runner) acquireGPU(ctx context.Context) (func(), error) {
	select {
	case r.gpu <- struct{}{}:
		return func() { <-r.gpu }, nil
	case <-ctx.Done():
		return nil, &Error{Kind: KindBusy, Message: fmt.Sprintf("waiting for the GPU: %v", ctx.Err())}
	}
}

// Reserve takes the engine's single-run slot. It reports false when the
// engine is already reserved; the release func must be called exactly once.
func (r *Runner) Reserve(name string) (func(), bool) {
	lock, ok := r.busy[name]
	if !ok {
		return func() {}, true
	}
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, true
	default:
		return nil, false
	}
}

func (r *Runner) Run(ctx context.Context, spec Spec) (Result, error) {
	release, ok := r.Reserve(spec.Engine)
	if !ok {
		return Result{}, &Error{Kind: KindBusy, Message: fmt.Sprintf("engine %q is busy", spec.Engine)}
	}
	defer release()
	return r.RunReserved(ctx, spec)
}

// RunReserved runs without taking the engine's single-run slot; the caller
// already holds it via Reserve (the story pipeline reserves audio for the
// whole job, then synthesizes line by line through this path). GPU-marked
// engines still take the shared GPU slot per run.
func (r *Runner) RunReserved(ctx context.Context, spec Spec) (Result, error) {
	engineCfg, ok := r.engines[spec.Engine]
	if !ok {
		return Result{}, &Error{Kind: KindNotConfigured, Message: fmt.Sprintf("engine %q is not configured", spec.Engine)}
	}

	// A spec that names its own files skips the byte seam entirely. Every
	// other spec hands over small payloads — a sentence of speech, one WAV
	// to transcribe — but a transcode reads and writes whole recordings, and
	// buffering those through memory twice is a cost with no purpose.
	inPath := spec.InputPath
	outPath := spec.OutputPath

	if spec.Input != nil {
		in, err := os.CreateTemp("", spec.InputPattern)
		if err != nil {
			return Result{}, &Error{Kind: KindInternal, Message: fmt.Sprintf("create temp input: %v", err)}
		}
		inPath = in.Name()
		defer os.Remove(inPath)
		if _, err := in.Write(spec.Input); err != nil {
			_ = in.Close()
			return Result{}, &Error{Kind: KindInternal, Message: fmt.Sprintf("save input: %v", err)}
		}
		if err := in.Close(); err != nil {
			return Result{}, &Error{Kind: KindInternal, Message: fmt.Sprintf("close temp input: %v", err)}
		}
		if spec.ValidateInput != nil {
			if err := spec.ValidateInput(inPath); err != nil {
				return Result{}, &Error{Kind: KindInvalidInput, Message: err.Error()}
			}
		}
	}

	if spec.OutputPattern != "" {
		out, err := os.CreateTemp("", spec.OutputPattern)
		if err != nil {
			return Result{}, &Error{Kind: KindInternal, Message: fmt.Sprintf("create temp output: %v", err)}
		}
		outPath = out.Name()
		if err := out.Close(); err != nil {
			_ = os.Remove(outPath)
			return Result{}, &Error{Kind: KindInternal, Message: fmt.Sprintf("close temp output: %v", err)}
		}
		defer os.Remove(outPath)
	}

	if engineCfg.GPU {
		releaseGPU, err := r.acquireGPU(ctx)
		if err != nil {
			return Result{}, err
		}
		defer releaseGPU()
	}

	stdout, stderr, elapsed, err := runCommand(ctx, engineCfg, spec.Timeout, spec.OverrideArgs, spec.BuildArgs(inPath, outPath))
	if err != nil {
		return Result{}, r.fail(spec, commandFailure(spec.Label+" failed", err, stdout, stderr))
	}
	if spec.ValidateOutput != nil {
		if err := spec.ValidateOutput(outPath); err != nil {
			return Result{}, r.fail(spec, fmt.Sprintf("%s %v", spec.Label, err))
		}
	}

	result := Result{Stdout: stdout, Stderr: stderr, Elapsed: elapsed}
	// A spec that named its own OutputPath wanted the file, not the bytes.
	if outPath != "" && spec.OutputPath == "" {
		data, err := os.ReadFile(outPath)
		if err != nil {
			return Result{}, r.fail(spec, fmt.Sprintf("read %s: %v", spec.OutputLabel, err))
		}
		result.Output = data
	}
	r.recorder.MarkSuccess(spec.Engine)
	return result, nil
}

func (r *Runner) fail(spec Spec, message string) error {
	r.recorder.MarkFailure(spec.Engine, lifecycle.StatusCrashed, message)
	return &Error{Kind: KindEngineFailure, Message: message}
}

// RequestTimeout resolves the per-run timeout: the engine's configured
// requestTimeoutSeconds, or fallback when unset.
func RequestTimeout(cfg config.EngineConfig, fallback time.Duration) time.Duration {
	if cfg.RequestTimeoutSeconds <= 0 {
		return fallback
	}
	return time.Duration(cfg.RequestTimeoutSeconds) * time.Second
}

// applyArgOverrides rewrites the value that follows each overridden flag in
// args, then appends any override whose flag never appeared (sorted for
// determinism). In-place replacement is required because audiocpp_cli takes
// the first occurrence of a flag, so appending alone cannot beat a config
// default.
func applyArgOverrides(args []string, overrides map[string]string) []string {
	out := append([]string{}, args...)
	if len(overrides) == 0 {
		return out
	}
	replaced := make(map[string]bool, len(overrides))
	for i := 0; i+1 < len(out); i++ {
		if value, ok := overrides[out[i]]; ok {
			out[i+1] = value
			replaced[out[i]] = true
			i++
		}
	}
	missing := make([]string, 0, len(overrides))
	for flag := range overrides {
		if !replaced[flag] {
			missing = append(missing, flag)
		}
	}
	sort.Strings(missing)
	for _, flag := range missing {
		out = append(out, flag, overrides[flag])
	}
	return out
}

func runCommand(ctx context.Context, engineCfg config.EngineConfig, fallback time.Duration, overrides map[string]string, extraArgs []string) ([]byte, []byte, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout(engineCfg, fallback))
	defer cancel()

	args := applyArgOverrides(engineCfg.Args, overrides)
	args = append(args, extraArgs...)
	cmd := exec.CommandContext(ctx, engineCfg.Command, args...)
	cmd.Dir = engineCfg.WorkingDir

	stdout := newLimitedBuffer(maxSubprocessLogBytes)
	stderr := newLimitedBuffer(maxSubprocessLogBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	started := time.Now()
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), time.Since(started), err
}

func commandFailure(prefix string, err error, stdout []byte, stderr []byte) string {
	parts := []string{fmt.Sprintf("%s: %v", prefix, err)}
	if out := strings.TrimSpace(string(stdout)); out != "" {
		parts = append(parts, "stdout: "+out)
	}
	if out := strings.TrimSpace(string(stderr)); out != "" {
		parts = append(parts, "stderr: "+out)
	}
	return strings.Join(parts, "; ")
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

func newLimitedBuffer(limit int64) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if int64(b.buf.Len()) < b.limit {
		remaining := b.limit - int64(b.buf.Len())
		if int64(len(p)) > remaining {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
			return len(p), nil
		}
		_, _ = b.buf.Write(p)
		return len(p), nil
	}
	b.truncated = true
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	if !b.truncated {
		return b.buf.Bytes()
	}
	out := append([]byte{}, b.buf.Bytes()...)
	out = append(out, []byte("\n[truncated]")...)
	return out
}
