// Package voice orchestrates the voice loop server-side: transcription ->
// chat -> speech behind one interface, in the same shape as the story
// pipeline. The engine seam, the chat function, and (optionally) the
// transcription function are injected, so the whole loop is testable
// without native binaries.
package voice

import (
	"context"
	"errors"
	"strings"

	"cpp-studio/internal/engine"
)

// Turn is one prior exchange in the conversation.
type Turn struct {
	Role string `json:"role"` // "user" or "assistant"
	Text string `json:"text"`
}

// ChatFunc produces the assistant reply for one user message, given the
// prior conversation turns in order.
type ChatFunc func(ctx context.Context, history []Turn, message string) (string, error)

// TranscribeFunc produces the transcript for recorded audio. When nil, the
// loop falls back to the engine seam's whisper subprocess.
type TranscribeFunc func(ctx context.Context, wav []byte) (string, error)

// Request carries either recorded audio or a typed message, plus the prior
// conversation turns the reply should be grounded in.
type Request struct {
	// WAV is the recorded/uploaded audio; nil means use Message instead.
	WAV []byte
	// Message is the typed fallback when no audio is supplied.
	Message string
	// History holds the prior turns, oldest first.
	History []Turn
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
	Engines    engine.Invoker
	Chat       ChatFunc
	Transcribe TranscribeFunc
}

func (l *Loop) Run(ctx context.Context, req Request) (Result, error) {
	transcript := strings.TrimSpace(req.Message)
	if req.WAV != nil {
		text, err := l.transcribe(ctx, req.WAV)
		if err != nil {
			return Result{}, err
		}
		transcript = strings.TrimSpace(text)
		if transcript == "" {
			return Result{}, &engine.Error{Kind: engine.KindEngineFailure, Message: "transcription returned no text"}
		}
	}
	if transcript == "" {
		return Result{}, ErrNoInput
	}

	reply, err := l.Chat(ctx, req.History, transcript)
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

func (l *Loop) transcribe(ctx context.Context, wavBytes []byte) (string, error) {
	if l.Transcribe != nil {
		return l.Transcribe(ctx, wavBytes)
	}
	res, err := l.Engines.Run(ctx, engine.TranscriptionSpec(wavBytes))
	if err != nil {
		return "", err
	}
	return string(res.Stdout), nil
}
