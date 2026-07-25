package engine

import (
	"strings"
	"testing"
)

func TestSniffAudioContentType(t *testing.T) {
	tests := []struct {
		name string
		head []byte
		want string
		ok   bool
	}{
		{name: "ogg", head: []byte("OggS\x00\x02"), want: "audio/ogg", ok: true},
		{name: "flac", head: []byte("fLaC\x00\x00"), want: "audio/flac", ok: true},
		{name: "wav", head: []byte("RIFF\x24\x08\x00\x00WAVE"), want: "audio/wav", ok: true},
		{name: "mp3 with id3 tag", head: []byte("ID3\x03\x00\x00"), want: "audio/mpeg", ok: true},
		{name: "bare mpeg frame sync", head: []byte{0xFF, 0xFB, 0x90, 0x00}, want: "audio/mpeg", ok: true},
		{name: "webm", head: []byte{0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x00}, want: "audio/webm", ok: true},
		{name: "m4a magic at offset 4", head: []byte("\x00\x00\x00\x20ftypM4A "), want: "audio/mp4", ok: true},
		{name: "html error page", head: []byte("<!doctype html>"), ok: false},
		{name: "too short", head: []byte{0xFF}, ok: false},
		{name: "empty", head: nil, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SniffAudioContentType(tt.head)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("SniffAudioContentType = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestImportAudioSpecArgs(t *testing.T) {
	spec := ImportAudioSpec("https://example.com/episode")
	if spec.Engine != "ytdlp" {
		t.Fatalf("unexpected engine %q", spec.Engine)
	}
	args := spec.BuildArgs("", `C:\Temp\cpp-studio-import-123`)
	joined := strings.Join(args, " ")
	// --force-overwrites is load-bearing: the runner creates the output file
	// first, and yt-dlp treats an existing file as already downloaded.
	for _, want := range []string{"--no-simulate", "--print", "--force-overwrites", "--no-playlist"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %s in args, got %v", want, args)
		}
	}
	if args[len(args)-1] != "https://example.com/episode" {
		t.Fatalf("expected the URL last, got %v", args)
	}
	if args[len(args)-3] != "-o" || args[len(args)-2] != `C:\Temp\cpp-studio-import-123` {
		t.Fatalf("expected -o <outPath> before the URL, got %v", args)
	}
}

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

func TestParseDiarization(t *testing.T) {
	stdout := []byte(`OfflineSpeakerDiarizationConfig(segmentation=...)
Started
0.031 -- 6.578 speaker_00
8.401 -- 14.408 speaker_01
not a span line
9.9 -- 1.1 speaker_02
15.877 -- 21.327 speaker_00
`)
	spans := ParseDiarization(stdout)
	if len(spans) != 3 {
		t.Fatalf("expected 3 spans (noise and inverted range skipped), got %+v", spans)
	}
	if spans[0].Speaker != 0 || spans[1].Speaker != 1 || spans[2].Speaker != 0 {
		t.Fatalf("speakers wrong: %+v", spans)
	}
	if spans[1].Start != 8.401 || spans[1].End != 14.408 {
		t.Fatalf("timing wrong: %+v", spans[1])
	}
	if got := ParseDiarization(nil); len(got) != 0 {
		t.Fatalf("expected no spans from empty stdout, got %+v", got)
	}
}
