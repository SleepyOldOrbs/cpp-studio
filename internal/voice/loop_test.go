package voice

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"cpp-studio/internal/engine"
	"cpp-studio/internal/wav"
)

func testLoop(t *testing.T) (*Loop, *engine.Fake) {
	t.Helper()
	fake := engine.NewFake()
	fake.Handle("whisper", func(spec engine.Spec) (engine.Result, error) {
		return engine.Result{Stdout: []byte("transcribed text\n")}, nil
	})
	fake.Handle("audio", func(spec engine.Spec) (engine.Result, error) {
		return engine.Result{Output: wav.SyntheticTone(160)}, nil
	})
	loop := &Loop{
		Engines: fake,
		Chat: func(ctx context.Context, message string) (string, error) {
			return "reply to: " + message, nil
		},
	}
	return loop, fake
}

func TestLoopWithAudioInput(t *testing.T) {
	loop, fake := testLoop(t)

	result, err := loop.Run(context.Background(), Request{WAV: wav.SyntheticTone(160)})
	if err != nil {
		t.Fatalf("run loop: %v", err)
	}
	if result.Transcript != "transcribed text" {
		t.Fatalf("unexpected transcript %q", result.Transcript)
	}
	if result.Reply != "reply to: transcribed text" {
		t.Fatalf("unexpected reply %q", result.Reply)
	}
	if err := wav.ValidateBytes(result.Audio); err != nil {
		t.Fatalf("expected WAV audio, got %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 2 || calls[0].Engine != "whisper" || calls[1].Engine != "audio" {
		t.Fatalf("expected whisper then audio, got %+v", calls)
	}
}

func TestLoopWithTypedMessage(t *testing.T) {
	loop, fake := testLoop(t)

	result, err := loop.Run(context.Background(), Request{Message: "  hello there  "})
	if err != nil {
		t.Fatalf("run loop: %v", err)
	}
	if result.Transcript != "hello there" {
		t.Fatalf("unexpected transcript %q", result.Transcript)
	}
	if result.Reply != "reply to: hello there" {
		t.Fatalf("unexpected reply %q", result.Reply)
	}
	if calls := fake.Calls(); len(calls) != 1 || calls[0].Engine != "audio" {
		t.Fatalf("typed message should skip transcription, got %+v", calls)
	}
}

func TestLoopRejectsEmptyInput(t *testing.T) {
	loop, _ := testLoop(t)
	_, err := loop.Run(context.Background(), Request{Message: "   "})
	if !errors.Is(err, ErrNoInput) {
		t.Fatalf("expected ErrNoInput, got %v", err)
	}
}

func TestLoopPropagatesChatFailure(t *testing.T) {
	loop, fake := testLoop(t)
	loop.Chat = func(ctx context.Context, message string) (string, error) {
		return "", fmt.Errorf("llama upstream request failed: connection refused")
	}
	_, err := loop.Run(context.Background(), Request{Message: "hello"})
	if err == nil || err.Error() != "llama upstream request failed: connection refused" {
		t.Fatalf("expected chat failure, got %v", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("chat failure should not invoke speech, got %+v", calls)
	}
}

func TestLoopPropagatesEngineFailure(t *testing.T) {
	loop, fake := testLoop(t)
	fake.Handle("audio", func(spec engine.Spec) (engine.Result, error) {
		return engine.Result{}, &engine.Error{Kind: engine.KindEngineFailure, Message: "audio speech command failed: boom"}
	})
	_, err := loop.Run(context.Background(), Request{Message: "hello"})
	var engErr *engine.Error
	if !errors.As(err, &engErr) || engErr.Kind != engine.KindEngineFailure {
		t.Fatalf("expected engine failure, got %v", err)
	}
}

func TestLoopRejectsEmptyTranscription(t *testing.T) {
	loop, fake := testLoop(t)
	fake.Handle("whisper", func(spec engine.Spec) (engine.Result, error) {
		return engine.Result{Stdout: []byte("   \n")}, nil
	})
	_, err := loop.Run(context.Background(), Request{WAV: wav.SyntheticTone(160)})
	var engErr *engine.Error
	if !errors.As(err, &engErr) || engErr.Kind != engine.KindEngineFailure {
		t.Fatalf("expected engine failure for empty transcript, got %v", err)
	}
}
