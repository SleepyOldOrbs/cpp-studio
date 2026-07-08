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
}
