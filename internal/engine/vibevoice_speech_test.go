package engine

import (
	"encoding/json"
	"testing"
)

func TestVibeVoiceSpeechFormatsPlainTextAndPreservesDialogue(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"plain text", "Brother! May I have some oats?", "Speaker 1: Brother! May I have some oats?"},
		{"paragraphs", "Hello.\n\nMore oats, please.", "Speaker 1: Hello. More oats, please."},
		{"dialogue", "Speaker 1: Hello.\nSpeaker 2: Good morning.", "Speaker 1: Hello.\nSpeaker 2: Good morning."},
		{"lowercase label", "speaker 1: Hello.", "speaker 1: Hello."},
		{"unicode", "Café, please.", "Speaker 1: Café, please."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := SynthesisRequest{EngineID: "vibevoice", Text: tc.input}
			spec := SpeechVoiceSpecForRequest(request, nil)
			if got := spec.BuildArgs("", "out.wav")[1]; got != tc.want {
				t.Fatalf("CLI text = %q, want %q", got, tc.want)
			}
			data, err := MarshalSpeechServerRequest("tts", request, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var payload serverSpeechRequest
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Input != tc.want {
				t.Fatalf("resident text = %q, want %q", payload.Input, tc.want)
			}
		})
	}
}
