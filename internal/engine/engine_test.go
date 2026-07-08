package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"cpp-studio/internal/config"
	"cpp-studio/internal/lifecycle"
)

type recorderCall struct {
	name    string
	success bool
	status  lifecycle.Status
	message string
}

type fakeRecorder struct {
	calls []recorderCall
}

func (r *fakeRecorder) MarkSuccess(name string) {
	r.calls = append(r.calls, recorderCall{name: name, success: true})
}

func (r *fakeRecorder) MarkFailure(name string, status lifecycle.Status, lastErr string) {
	r.calls = append(r.calls, recorderCall{name: name, status: status, message: lastErr})
}

func TestRunNotConfigured(t *testing.T) {
	runner := NewRunner(map[string]config.EngineConfig{}, &fakeRecorder{})
	_, err := runner.Run(context.Background(), SpeechSpec("hello"))
	var engErr *Error
	if !errors.As(err, &engErr) || engErr.Kind != KindNotConfigured {
		t.Fatalf("expected not-configured error, got %v", err)
	}
}

func TestReserveIsExclusive(t *testing.T) {
	runner := NewRunner(map[string]config.EngineConfig{
		"audio": {Command: "unused"},
	}, &fakeRecorder{})

	release, ok := runner.Reserve("audio")
	if !ok {
		t.Fatalf("expected first reserve to succeed")
	}
	if _, ok := runner.Reserve("audio"); ok {
		t.Fatalf("expected second reserve to fail while held")
	}
	release()
	release2, ok := runner.Reserve("audio")
	if !ok {
		t.Fatalf("expected reserve to succeed after release")
	}
	release2()

	// Unknown names are not serialized; reservation is a no-op.
	if _, ok := runner.Reserve("unknown"); !ok {
		t.Fatalf("expected unknown engine reserve to succeed")
	}
}

func TestRunBusy(t *testing.T) {
	recorder := &fakeRecorder{}
	runner := NewRunner(map[string]config.EngineConfig{
		"audio": {Command: "unused"},
	}, recorder)

	release, ok := runner.Reserve("audio")
	if !ok {
		t.Fatalf("expected reserve to succeed")
	}
	defer release()

	_, err := runner.Run(context.Background(), SpeechSpec("hello"))
	var engErr *Error
	if !errors.As(err, &engErr) || engErr.Kind != KindBusy {
		t.Fatalf("expected busy error, got %v", err)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("busy run should not record an outcome: %+v", recorder.calls)
	}
}

func TestRunRejectsInvalidInputWithoutInvoking(t *testing.T) {
	recorder := &fakeRecorder{}
	runner := NewRunner(map[string]config.EngineConfig{
		"whisper": {Command: "definitely-not-a-real-command"},
	}, recorder)

	_, err := runner.Run(context.Background(), TranscriptionSpec([]byte("not a wav")))
	var engErr *Error
	if !errors.As(err, &engErr) || engErr.Kind != KindInvalidInput {
		t.Fatalf("expected invalid-input error, got %v", err)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("invalid input should not record an outcome: %+v", recorder.calls)
	}
}

func TestRequestTimeout(t *testing.T) {
	if got := RequestTimeout(config.EngineConfig{}, 10*time.Second); got != 10*time.Second {
		t.Fatalf("expected fallback timeout, got %s", got)
	}
	if got := RequestTimeout(config.EngineConfig{RequestTimeoutSeconds: 3}, 10*time.Second); got != 3*time.Second {
		t.Fatalf("expected configured timeout, got %s", got)
	}
}

func TestFakeRecordsCallsAndSerializes(t *testing.T) {
	fake := NewFake()
	fake.Handle("audio", func(spec Spec) (Result, error) {
		return Result{Output: []byte("RIFFtestWAVE")}, nil
	})

	result, err := fake.Run(context.Background(), SpeechSpec("hello"))
	if err != nil {
		t.Fatalf("fake run: %v", err)
	}
	if string(result.Output) != "RIFFtestWAVE" {
		t.Fatalf("unexpected fake output %q", result.Output)
	}
	if calls := fake.Calls(); len(calls) != 1 || calls[0].Engine != "audio" {
		t.Fatalf("unexpected fake calls %+v", calls)
	}

	release, ok := fake.Reserve("audio")
	if !ok {
		t.Fatalf("expected fake reserve to succeed")
	}
	if _, err := fake.Run(context.Background(), SpeechSpec("again")); err == nil {
		t.Fatalf("expected busy error while reserved")
	}
	release()
	if _, err := fake.Run(context.Background(), SpeechSpec("again")); err != nil {
		t.Fatalf("expected run to succeed after release: %v", err)
	}
}
