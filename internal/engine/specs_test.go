package engine

import (
	"testing"
)

func TestSanitizeSpeechText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "ascii passes through",
			input: "The Sun will run out of fuel.",
			want:  "The Sun will run out of fuel.",
		},
		{
			name:  "typography transliterates",
			input: "It’s “fine” — mostly…",
			want:  `It's "fine" - mostly...`,
		},
		{
			name:  "accented letters transliterate",
			input: "café naïve Über",
			want:  "cafe naive Uber",
		},
		{
			name:  "unmapped runes drop and whitespace collapses",
			input: "star ⭐ formation\n\ttakes  time",
			want:  "star formation takes time",
		},
		{
			name:  "control characters drop",
			input: "one\x00two\x07three",
			want:  "onetwothree",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeSpeechText(tt.input); got != tt.want {
				t.Fatalf("sanitizeSpeechText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSpeechSpecSanitizesTextArg(t *testing.T) {
	spec := SpeechSpec("It’s time")

	args := spec.BuildArgs("", "out.wav")

	want := []string{"--text", "It's time", "--out", "out.wav"}
	if len(args) != len(want) {
		t.Fatalf("expected args %v, got %v", want, args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, args)
		}
	}
	if spec.OverrideArgs != nil {
		t.Fatalf("expected default speech spec to carry no overrides, got %v", spec.OverrideArgs)
	}
}

func TestSpeechVoiceSpecOverridesVoiceFlags(t *testing.T) {
	spec := SpeechVoiceSpec("hello", &Voice{
		RefWAVPath: `C:\voices\ref.wav`,
		RefText:    "It’s “my” voice",
	})

	if got := spec.OverrideArgs["--voice-ref"]; got != `C:\voices\ref.wav` {
		t.Fatalf("expected voice ref override, got %q", got)
	}
	if got := spec.OverrideArgs["--reference-text"]; got != `It's "my" voice` {
		t.Fatalf("expected sanitized reference text override, got %q", got)
	}
	if len(spec.OverrideArgs) != 2 {
		t.Fatalf("expected exactly two overrides, got %v", spec.OverrideArgs)
	}
}

func TestVoiceDesignSpecSanitizesArgs(t *testing.T) {
	spec := VoiceDesignSpec("Deep “gravelly” cowboy", "It’s a sample")

	args := spec.BuildArgs("", "out.wav")

	want := []string{"--instruct", `Deep "gravelly" cowboy`, "--text", "It's a sample", "--out", "out.wav"}
	if len(args) != len(want) {
		t.Fatalf("expected args %v, got %v", want, args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, args)
		}
	}
	if spec.Engine != "voicedesign" {
		t.Fatalf("expected voicedesign engine, got %q", spec.Engine)
	}
}

func TestOmniVoiceDesignSpecShape(t *testing.T) {
	spec := OmniVoiceDesignSpec("female, british accent", "A sample")

	if spec.Engine != "omnivoice" {
		t.Fatalf("expected omnivoice engine, got %q", spec.Engine)
	}
	args := spec.BuildArgs("", "out.wav")
	want := []string{"--instruct", "female, british accent", "--text", "A sample", "--out", "out.wav"}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, args)
		}
	}
}

func TestVoxCPMDesignSpecEmbedsDescriptionInText(t *testing.T) {
	spec := VoxCPMDesignSpec("Deep (gravelly) cowboy", "Howdy there")

	if spec.Engine != "voxcpm2" {
		t.Fatalf("expected voxcpm2 engine, got %q", spec.Engine)
	}
	args := spec.BuildArgs("", "out.wav")
	want := []string{"--text", "(Deep gravelly cowboy)Howdy there", "--out", "out.wav"}
	if len(args) != len(want) {
		t.Fatalf("expected args %v, got %v", want, args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, args)
		}
	}
}

func TestApplyArgOverrides(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		overrides map[string]string
		want      []string
	}{
		{
			name: "no overrides copies args",
			args: []string{"--voice-ref", "default.wav"},
			want: []string{"--voice-ref", "default.wav"},
		},
		{
			name:      "replaces existing flag value in place",
			args:      []string{"--task", "tts", "--voice-ref", "default.wav", "--reference-text", "default words"},
			overrides: map[string]string{"--voice-ref": "clone.wav", "--reference-text": "clone words"},
			want:      []string{"--task", "tts", "--voice-ref", "clone.wav", "--reference-text", "clone words"},
		},
		{
			name:      "appends missing flags sorted",
			args:      []string{"speech"},
			overrides: map[string]string{"--voice-ref": "clone.wav", "--reference-text": "clone words"},
			want:      []string{"speech", "--reference-text", "clone words", "--voice-ref", "clone.wav"},
		},
		{
			name:      "mixed replace and append",
			args:      []string{"--voice-ref", "default.wav"},
			overrides: map[string]string{"--voice-ref": "clone.wav", "--reference-text": "clone words"},
			want:      []string{"--voice-ref", "clone.wav", "--reference-text", "clone words"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyArgOverrides(tt.args, tt.overrides)
			if len(got) != len(tt.want) {
				t.Fatalf("expected args %v, got %v", tt.want, got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("expected args %v, got %v", tt.want, got)
				}
			}
		})
	}
}
