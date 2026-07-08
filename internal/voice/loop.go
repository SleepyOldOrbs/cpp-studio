// Package voice orchestrates the voice loop server-side: transcription ->
// chat -> speech behind one interface, in the same shape as the story
// pipeline. The engine seam and the chat function are injected, so the whole
// loop is testable without native binaries.
package voice

import (
	"context"
	"errors"
	"strings"

	"cpp-studio/internal/engine"
)

// ChatFunc produces the assistant reply for one user message.
type ChatFunc func(ctx context.Context, message string) (string, error)

// Request carries either recorded audio or a typed message.
type Request struct {
	// WAV is the recorded/uploaded audio; nil means use Message instead.
	WAV []byte
	// Message is the typed fallback when no audio is supplied.
	Message string
}

// Result is one full turn of the loop.
type Result struct {
	Transcript string
	Reply      string
	// Audio is the spoken reply as WAV bytes.
	Audio []byte
}

// ErrNoInput: the request carried neither audio nor message text.
var ErrNoInput = errors.New("record audio, choose a WAV, or enter a typed message")

// Loop runs transcribe -> chat -> speak as one unit.
type Loop struct {
	Engines engine.Invoker
	Chat    ChatFunc
}

func (l *Loop) Run(ctx context.Context, req Request) (Result, error) {
	transcript := strings.TrimSpace(req.Message)
	if req.WAV != nil {
		res, err := l.Engines.Run(ctx, engine.TranscriptionSpec(req.WAV))
		if err != nil {
			return Result{}, err
		}
		transcript = strings.TrimSpace(string(res.Stdout))
		if transcript == "" {
			return Result{}, &engine.Error{Kind: engine.KindEngineFailure, Message: "transcription returned no text"}
		}
	}
	if transcript == "" {
		return Result{}, ErrNoInput
	}

	reply, err := l.Chat(ctx, transcript)
	if err != nil {
		return Result{}, err
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return Result{}, &engine.Error{Kind: engine.KindEngineFailure, Message: "chat returned no assistant reply"}
	}

	speech, err := l.Engines.Run(ctx, engine.SpeechSpec(reply))
	if err != nil {
		return Result{}, err
	}
	return Result{Transcript: transcript, Reply: reply, Audio: speech.Output}, nil
}
