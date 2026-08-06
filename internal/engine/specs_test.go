package engine

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"cpp-studio/internal/wav"
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

func TestSpeechVoiceSpecForTargetsNamedEngine(t *testing.T) {
	spec := SpeechVoiceSpecFor("dramabox", "hello", nil)
	if spec.Engine != "dramabox" {
		t.Fatalf("expected dramabox engine, got %q", spec.Engine)
	}
	if spec.Label != "dramabox speech command" {
		t.Fatalf("unexpected label %q", spec.Label)
	}
	legacy := SpeechVoiceSpec("hello", nil)
	if legacy.Engine != "audio" {
		t.Fatalf("legacy wrapper changed engine: %q", legacy.Engine)
	}
}

func TestVoiceConversionSpecMapsSourceTargetAndOutput(t *testing.T) {
	spec := VoiceConversionSpec(`C:\clips\source.wav`, `C:\voices\target.wav`)

	if spec.Engine != VoiceConversionEngineID {
		t.Fatalf("expected %q engine, got %q", VoiceConversionEngineID, spec.Engine)
	}
	if spec.InputPath != `C:\clips\source.wav` {
		t.Fatalf("unexpected source path %q", spec.InputPath)
	}
	if spec.OutputPattern != "cpp-studio-voice-conversion-*.wav" {
		t.Fatalf("unexpected output pattern %q", spec.OutputPattern)
	}
	want := []string{"--audio", `C:\clips\source.wav`, "--voice-ref", `C:\voices\target.wav`, "--out", "converted.wav"}
	if got := spec.BuildArgs(spec.InputPath, "converted.wav"); !reflect.DeepEqual(got, want) {
		t.Fatalf("conversion args = %v, want %v", got, want)
	}
}

func TestMusicGenerationSpecMapsCuratedACEControls(t *testing.T) {
	request := MusicGenerationRequest{
		Route:           "repaint",
		Prompt:          "replace the chorus with bright synth pop",
		Lyrics:          "We rise into the light",
		DurationSeconds: 30,
		Seed:            42,
		Steps:           8,
		GuidanceScale:   1.5,
		TrackName:       "chorus",
		RepaintStart:    8,
		RepaintEnd:      16,
		RepaintMode:     "balanced",
		RepaintStrength: 0.5,
	}
	spec := MusicGenerationSpec(`C:\music\source.wav`, request)

	if spec.Engine != MusicEngineID {
		t.Fatalf("expected %q engine, got %q", MusicEngineID, spec.Engine)
	}
	if spec.InputPath != `C:\music\source.wav` {
		t.Fatalf("unexpected source path %q", spec.InputPath)
	}
	if spec.OutputPattern != "cpp-studio-music-*.wav" {
		t.Fatalf("unexpected output pattern %q", spec.OutputPattern)
	}
	want := []string{
		"--task-route", "repaint",
		"--text", "replace the chorus with bright synth pop",
		"--lyrics", "We rise into the light",
		"--duration-seconds", "30",
		"--seed", "42",
		"--num-inference-steps", "8",
		"--guidance-scale", "1.5",
		"--track-name", "chorus",
		"--repaint-start", "8",
		"--repaint-end", "16",
		"--repaint-mode", "balanced",
		"--repaint-strength", "0.5",
		"--audio", `C:\music\source.wav`,
		"--out", "song.wav",
	}
	if got := spec.BuildArgs(spec.InputPath, "song.wav"); !reflect.DeepEqual(got, want) {
		t.Fatalf("music args = %v, want %v", got, want)
	}
}

func TestMusicGenerationSpecOmitsRandomSeedSentinel(t *testing.T) {
	request := MusicGenerationRequest{
		Route:           "text2music",
		Prompt:          "classic opera",
		DurationSeconds: 30,
		Seed:            -1,
		Steps:           8,
		GuidanceScale:   1,
	}
	args := MusicGenerationSpec("", request).BuildArgs("", "music.wav")
	for i, arg := range args {
		if arg == "--seed" || (i > 0 && args[i-1] == "--seed" && arg == "-1") {
			t.Fatalf("random seed sentinel leaked into audio.cpp CLI args: %v", args)
		}
	}
}

func TestMusicAnalysisSpecUsesDeterministicACEAnalyzeRoute(t *testing.T) {
	spec := MusicAnalysisSpec(`C:\music\source.wav`, 1234)
	want := []string{"--task-route", "analyze", "--text", "analyze", "--seed", "1234", "--audio", `C:\music\source.wav`}
	if spec.Engine != MusicEngineID || spec.OutputPattern != "" {
		t.Fatalf("unexpected analysis spec: %+v", spec)
	}
	if got := spec.BuildArgs(spec.InputPath, ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("analysis args = %v, want %v", got, want)
	}
}

func TestResolveDramaBoxSynthesisOptionsFillsDefaultsAndAllowsCuratedOverrides(t *testing.T) {
	got, err := ResolveSynthesisOptions(DramaBoxSpeechEngineID, `{"guidance_scale":3.25,"cross_fade_duration_sec":0}`)
	if err != nil {
		t.Fatalf("resolve options: %v", err)
	}
	want := DefaultDramaBoxOptions()
	want.GuidanceScale = 3.25
	want.CrossFadeDurationSec = 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved options = %+v, want %+v", got, want)
	}

	legacy, err := ResolveSynthesisOptions(DefaultSpeechEngineID, "")
	if err != nil || legacy != (SynthesisOptions{}) {
		t.Fatalf("legacy options changed: %+v, %v", legacy, err)
	}
}

func TestResolveDramaBoxSynthesisOptionsRejectsUnsafeOrAmbiguousJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "unknown", raw: `{"command":"calc.exe"}`, want: "unknown synthesis option"},
		{name: "seed", raw: `{"seed":"42"}`, want: "unknown synthesis option"},
		{name: "duplicate", raw: `{"guidance_scale":2,"guidance_scale":3}`, want: "duplicate synthesis option"},
		{name: "array", raw: `[]`, want: "JSON object"},
		{name: "steps too high", raw: `{"num_inference_steps":101}`, want: "between 1 and 100"},
		{name: "crossfade too long", raw: `{"audio_chunk_duration_sec":1,"cross_fade_duration_sec":1}`, want: "half audio_chunk_duration_sec"},
		{name: "trailing value", raw: `{} {}`, want: "unexpected JSON value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveSynthesisOptions(DramaBoxSpeechEngineID, test.raw)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ResolveSynthesisOptions(%s) error = %v, want %q", test.raw, err, test.want)
			}
		})
	}
	if _, err := ResolveSynthesisOptions(DefaultSpeechEngineID, `{}`); err == nil || !strings.Contains(err.Error(), "only supported with dramabox") {
		t.Fatalf("legacy narrator accepted options: %v", err)
	}
}

func TestDramaBoxTypedRequestMapsToSubprocessAndServerContracts(t *testing.T) {
	options := DefaultDramaBoxOptions()
	options.Seed = Seed(^uint64(0))
	request := SynthesisRequest{Text: "A fact.", EngineID: DramaBoxSpeechEngineID, Options: options}

	spec := SpeechVoiceSpecForRequest(request, nil)
	args := spec.BuildArgs("", "out.wav")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--seed 18446744073709551615",
		"--num-inference-steps 30",
		"--guidance-scale 2.5",
		"--request-option audio_chunk_threshold_sec=45",
		"--request-option audio_chunk_duration_sec=37",
		"--request-option cross_fade_duration_sec=0.05",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("subprocess args missing %q: %v", want, args)
		}
	}

	payload, err := MarshalSpeechServerRequest("tts-1", request, nil, nil)
	if err != nil {
		t.Fatalf("marshal server request: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode server request: %v", err)
	}
	if body["seed"] != "18446744073709551615" || body["num_inference_steps"] != float64(30) || body["guidance_scale"] != 2.5 {
		t.Fatalf("top-level server mapping wrong: %s", payload)
	}
	nested, ok := body["options"].(map[string]any)
	if !ok || nested["audio_chunk_threshold_sec"] != float64(45) || nested["audio_chunk_duration_sec"] != float64(37) || nested["cross_fade_duration_sec"] != 0.05 {
		t.Fatalf("nested server mapping wrong: %s", payload)
	}
}

func TestDramaBoxServerMappingPreservesExplicitZeroGuidance(t *testing.T) {
	options := DefaultDramaBoxOptions()
	options.GuidanceScale = 0
	request := SynthesisRequest{Text: "A fact.", EngineID: DramaBoxSpeechEngineID, Options: options}
	payload, err := MarshalSpeechServerRequest("tts", request, nil, nil)
	if err != nil {
		t.Fatalf("marshal server request: %v", err)
	}
	if !strings.Contains(string(payload), `"guidance_scale":0`) {
		t.Fatalf("explicit zero guidance was omitted: %s", payload)
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

func TestSortformerDiarizationSpecUsesAudioFlagAndSizedGraph(t *testing.T) {
	spec := SortformerDiarizationSpec([]byte("wav"), 28*time.Second)
	if spec.Engine != "diarize" {
		t.Fatalf("engine = %q, want diarize", spec.Engine)
	}
	want := []string{"--session-option", "session_len_sec=30", "--audio", "input.wav"}
	if got := spec.BuildArgs("input.wav", ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestCanUseSortformer(t *testing.T) {
	compatible := wav.Format{Channels: 1, SampleRate: 16000, BitsPerSample: 16}
	if !CanUseSortformer(compatible, 120*time.Second, 0) {
		t.Fatal("expected compatible 120-second automatic request to use Sortformer")
	}
	if CanUseSortformer(compatible, 121*time.Second, 0) {
		t.Fatal("expected long request to use sherpa")
	}
	if CanUseSortformer(compatible, time.Second, 5) {
		t.Fatal("expected explicit speaker count to use sherpa")
	}
	incompatible := compatible
	incompatible.SampleRate = 24000
	if CanUseSortformer(incompatible, time.Second, 0) {
		t.Fatal("expected incompatible sample rate to use sherpa")
	}
}

func TestSherpaDiarizationSpecPinsSpeakerCount(t *testing.T) {
	spec := SherpaDiarizationSpec([]byte("wav"), 5)
	if spec.Engine != "diarize-sherpa" {
		t.Fatalf("engine = %q, want diarize-sherpa", spec.Engine)
	}
	want := []string{"--clustering.num-clusters=5", "input.wav"}
	if got := spec.BuildArgs("input.wav", ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestParseSortformerDiarization(t *testing.T) {
	stdout := []byte(`family=sortformer_diar
speaker_turns=[{"start_sample":1280,"end_sample":126720,"speaker_id":"SPEAKER_00","confidence":0.8},{"start_sample":135680,"end_sample":227840,"speaker_id":"SPEAKER_01","confidence":0.9}]
ggml_cuda_init: found 1 CUDA device
`)
	spans, err := ParseSortformerDiarization(stdout)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %+v", spans)
	}
	if spans[0].Start != 0.08 || spans[0].End != 7.92 || spans[0].Speaker != 0 {
		t.Fatalf("first span wrong: %+v", spans[0])
	}
	if spans[1].Start != 8.48 || spans[1].End != 14.24 || spans[1].Speaker != 1 {
		t.Fatalf("second span wrong: %+v", spans[1])
	}
	if _, err := ParseSortformerDiarization([]byte("no turns here")); err == nil {
		t.Fatal("expected missing speaker_turns output to fail")
	}
	empty, err := ParseSortformerDiarization([]byte("family=sortformer_diar\ntask=diar\nmode=offline\n"))
	if err != nil || len(empty) != 0 {
		t.Fatalf("valid zero-turn result = (%+v, %v), want empty success", empty, err)
	}
}
