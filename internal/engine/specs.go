package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"cpp-studio/internal/wav"
)

const (
	// DefaultSpeechEngineID is the studio's backward-compatible speech lane.
	DefaultSpeechEngineID = "audio"
	// DramaBoxSpeechEngineID is the audio.cpp expressive, text-only-capable
	// speech lane used by audiobooks.
	DramaBoxSpeechEngineID = "dramabox"
	// VoiceConversionEngineID is the audio.cpp source-to-target voice lane.
	VoiceConversionEngineID = "voiceconvert"
	// MusicEngineID is the audio.cpp ACE-Step music generation/edit lane.
	MusicEngineID = "music"

	DefaultTranscriptionTimeout   = 120 * time.Second
	DefaultSpeechTimeout          = 180 * time.Second
	DefaultVoiceConversionTimeout = 900 * time.Second
	DefaultMusicTimeout           = 1800 * time.Second
	DefaultImageTimeout           = 300 * time.Second
	DefaultDiarizationTimeout     = 300 * time.Second
	// DefaultImportTimeout is generous because it covers a network download
	// of a whole episode, not a local inference run.
	DefaultImportTimeout = 900 * time.Second
	// DefaultTranscodeTimeout covers encoding a long audiobook; spoken-word
	// audio encodes far faster than real time, so this is slack, not a
	// target.
	DefaultTranscodeTimeout = 600 * time.Second

	MaxSpeechOutputBytes = 32 * 1024 * 1024
	MaxImageOutputBytes  = 32 * 1024 * 1024
	// MaxImportOutputBytes bounds a fetched recording. 192 MB is far past
	// the Extractor's ~30-minute editor cap in any sane audio-only format,
	// while still refusing someone who points the importer at a film.
	MaxImportOutputBytes = 192 * 1024 * 1024
	// MaxDecodedAudioBytes bounds a decode. Mono 16-bit at 48 kHz is about
	// 5.5 MB a minute, so this is roughly 35 minutes — past the Extractor's
	// own 30-minute editor cap, which is the real limit a caller will hit.
	MaxDecodedAudioBytes = 192 * 1024 * 1024
	// MaxDecodeUploadBytes bounds what may be sent for decoding. Video
	// containers are the reason this is generous: the audio track inside a
	// large file is usually small.
	MaxDecodeUploadBytes = 1024 * 1024 * 1024
	MaxImageDimension    = 2048
	maxImagePixels       = MaxImageDimension * MaxImageDimension
)

// SpeechEngineAllowsTextOnly reports whether a resident speech server may
// omit voice_ref. Qwen requires a configured reference; DramaBox does not.
func SpeechEngineAllowsTextOnly(engineName string) bool {
	return engineName == DramaBoxSpeechEngineID
}

// Voice identifies a cloned voice reference for speech synthesis: the
// reference WAV on disk plus its transcript. A nil *Voice means the config
// default voice.
type Voice struct {
	RefWAVPath string
	RefText    string
}

// VoiceConversionSpec invokes audio.cpp's offline voice-conversion task.
// The gateway owns and validates the two WAV paths; the engine module owns
// their CLI mapping and the converted-output contract.
func VoiceConversionSpec(sourcePath string, targetVoicePath string) Spec {
	return Spec{
		Engine:        VoiceConversionEngineID,
		Label:         "audio.cpp voice conversion command",
		Timeout:       DefaultVoiceConversionTimeout,
		InputPath:     sourcePath,
		OutputPattern: "cpp-studio-voice-conversion-*.wav",
		OutputLabel:   "converted voice wav",
		BuildArgs: func(inPath, outPath string) []string {
			return []string{"--audio", inPath, "--voice-ref", targetVoicePath, "--out", outPath}
		},
		ValidateOutput: func(path string) error {
			if err := wav.ValidateFile(path); err != nil {
				return fmt.Errorf("produced invalid WAV: %v", err)
			}
			if err := validateFileSize(path, MaxDecodedAudioBytes, "converted voice wav"); err != nil {
				return fmt.Errorf("produced oversized WAV: %v", err)
			}
			return nil
		},
	}
}

// MusicGenerationRequest is the bounded ACE-Step control surface exposed by
// CPP Studio. It intentionally does not accept arbitrary native request
// options: paths and flags remain owned by the gateway and this package.
type MusicGenerationRequest struct {
	Route           string
	Prompt          string
	Lyrics          string
	DurationSeconds float64
	Seed            int
	Steps           int
	GuidanceScale   float64
	TrackName       string
	RepaintStart    float64
	RepaintEnd      float64
	RepaintMode     string
	RepaintStrength float64
}

// MusicGenerationSpec invokes audio.cpp's offline ACE-Step generation task.
// sourcePath is blank for text-to-music and may be present for complete; the
// gateway enforces which edit routes require source audio.
func MusicGenerationSpec(sourcePath string, request MusicGenerationRequest) Spec {
	return Spec{
		Engine:        MusicEngineID,
		Label:         "audio.cpp music generation command",
		Timeout:       DefaultMusicTimeout,
		InputPath:     sourcePath,
		OutputPattern: "cpp-studio-music-*.wav",
		OutputLabel:   "generated music wav",
		BuildArgs: func(inPath, outPath string) []string {
			args := []string{
				"--task-route", request.Route,
				"--text", request.Prompt,
			}
			if request.Lyrics != "" {
				args = append(args, "--lyrics", request.Lyrics)
			}
			args = append(args, "--duration-seconds", strconv.FormatFloat(request.DurationSeconds, 'f', -1, 64))
			if request.Seed >= 0 {
				args = append(args, "--seed", strconv.Itoa(request.Seed))
			}
			args = append(args,
				"--num-inference-steps", strconv.Itoa(request.Steps),
				"--guidance-scale", strconv.FormatFloat(request.GuidanceScale, 'f', -1, 64),
			)
			if request.TrackName != "" {
				args = append(args, "--track-name", request.TrackName)
			}
			if request.Route == "repaint" {
				args = append(args,
					"--repaint-start", strconv.FormatFloat(request.RepaintStart, 'f', -1, 64),
					"--repaint-end", strconv.FormatFloat(request.RepaintEnd, 'f', -1, 64),
					"--repaint-mode", request.RepaintMode,
					"--repaint-strength", strconv.FormatFloat(request.RepaintStrength, 'f', -1, 64),
				)
			}
			if inPath != "" {
				args = append(args, "--audio", inPath)
			}
			return append(args, "--out", outPath)
		},
		ValidateOutput: func(path string) error {
			if err := wav.ValidateFile(path); err != nil {
				return fmt.Errorf("produced invalid WAV: %v", err)
			}
			if err := validateFileSize(path, MaxDecodedAudioBytes, "generated music wav"); err != nil {
				return fmt.Errorf("produced oversized WAV: %v", err)
			}
			return nil
		},
	}
}

// MusicAnalysisSpec asks ACE-Step's planner to infer a source caption and
// musical metadata. The CLI writes the JSON payload as text_output on stdout.
func MusicAnalysisSpec(sourcePath string, seed int) Spec {
	return Spec{
		Engine:    MusicEngineID,
		Label:     "audio.cpp music source analysis command",
		Timeout:   DefaultMusicTimeout,
		InputPath: sourcePath,
		BuildArgs: func(inPath, _ string) []string {
			return []string{"--task-route", "analyze", "--text", "analyze", "--seed", strconv.Itoa(seed), "--audio", inPath}
		},
	}
}

// SpeechSpec invokes the "audio" engine: --text <input> --out <wav path>.
// The engine must produce a valid WAV of at most MaxSpeechOutputBytes.
// The input is transliterated to ASCII first: audiocpp_cli parses argv via
// the Windows ANSI codepage, so any non-ASCII byte in --text reaches the
// engine as invalid UTF-8 and the request fails.
func SpeechSpec(input string) Spec {
	return SpeechVoiceSpec(input, nil)
}

// SpeechVoiceSpec is SpeechSpec speaking with a cloned voice: the voice's
// reference WAV and transcript override the config default --voice-ref and
// --reference-text. The transcript is sanitized like the spoken text (same
// ANSI argv constraint).
func SpeechVoiceSpec(input string, voice *Voice) Spec {
	return SpeechVoiceSpecFor(DefaultSpeechEngineID, input, voice)
}

// SpeechVoiceSpecFor builds the common audio.cpp CLI contract for a named
// speech engine. The legacy wrappers continue to select "audio".
func SpeechVoiceSpecFor(engineName string, input string, voice *Voice) Spec {
	text := sanitizeSpeechText(input)
	var overrides map[string]string
	if voice != nil {
		overrides = map[string]string{
			"--voice-ref":      voice.RefWAVPath,
			"--reference-text": sanitizeSpeechText(voice.RefText),
		}
	}
	return Spec{
		Engine:        engineName,
		Label:         engineName + " speech command",
		Timeout:       DefaultSpeechTimeout,
		OutputPattern: "cpp-studio-speech-*.wav",
		OutputLabel:   "generated wav",
		OverrideArgs:  overrides,
		BuildArgs: func(_, outPath string) []string {
			return []string{"--text", text, "--out", outPath}
		},
		ValidateOutput: func(path string) error {
			if err := wav.ValidateFile(path); err != nil {
				return fmt.Errorf("produced invalid WAV: %v", err)
			}
			if err := validateFileSize(path, MaxSpeechOutputBytes, "generated wav"); err != nil {
				return fmt.Errorf("produced oversized WAV: %v", err)
			}
			return nil
		},
	}
}

// VoiceDesignSpec invokes the "voicedesign" engine (Qwen3-TTS VoiceDesign):
// --instruct <description> --text <sample> --out <wav path>. The engine
// creates a brand-new voice from the natural-language instruction and speaks
// the sample text with it — no reference audio involved. Both text arguments
// cross the same ANSI argv boundary as speech, so both are sanitized.
func VoiceDesignSpec(instruct string, sampleText string) Spec {
	return instructDesignSpec("voicedesign", instruct, sampleText)
}

// OmniVoiceDesignSpec invokes the "omnivoice" engine, whose voice design
// takes the same --instruct/--text/--out shape. OmniVoice expects
// comma-separated speaker attributes (gender, age, pitch, whisper, English
// accent, Chinese dialect) rather than free prose.
func OmniVoiceDesignSpec(instruct string, sampleText string) Spec {
	return instructDesignSpec("omnivoice", instruct, sampleText)
}

// VoxCPMDesignSpec invokes the "voxcpm2" engine. VoxCPM2 has no --instruct
// flag: the voice description rides in the synthesis text as a leading
// parenthesised style block, "(description)sample text".
func VoxCPMDesignSpec(instruct string, sampleText string) Spec {
	// Drop parentheses from the description so it cannot close the style
	// block early.
	instruction := strings.NewReplacer("(", " ", ")", " ").Replace(sanitizeSpeechText(instruct))
	instruction = strings.Join(strings.Fields(instruction), " ")
	text := "(" + instruction + ")" + sanitizeSpeechText(sampleText)
	spec := designSpecShell("voxcpm2")
	spec.BuildArgs = func(_, outPath string) []string {
		return []string{"--text", text, "--out", outPath}
	}
	return spec
}

func instructDesignSpec(engineName string, instruct string, sampleText string) Spec {
	instruction := sanitizeSpeechText(instruct)
	sample := sanitizeSpeechText(sampleText)
	spec := designSpecShell(engineName)
	spec.BuildArgs = func(_, outPath string) []string {
		return []string{"--instruct", instruction, "--text", sample, "--out", outPath}
	}
	return spec
}

func designSpecShell(engineName string) Spec {
	return Spec{
		Engine:        engineName,
		Label:         engineName + " voice design command",
		Timeout:       DefaultSpeechTimeout,
		OutputPattern: "cpp-studio-voice-design-*.wav",
		OutputLabel:   "designed voice wav",
		ValidateOutput: func(path string) error {
			if err := wav.ValidateFile(path); err != nil {
				return fmt.Errorf("produced invalid WAV: %v", err)
			}
			if err := validateFileSize(path, MaxSpeechOutputBytes, "designed voice wav"); err != nil {
				return fmt.Errorf("produced oversized WAV: %v", err)
			}
			return nil
		},
	}
}

// TranscriptionSpec invokes the "whisper" engine: -f <wav path>. The input
// must be a valid WAV; the transcript is returned on stdout.
func TranscriptionSpec(wavBytes []byte) Spec {
	if wavBytes == nil {
		wavBytes = []byte{}
	}
	return Spec{
		Engine:        "whisper",
		Label:         "whisper transcription command",
		Timeout:       DefaultTranscriptionTimeout,
		Input:         wavBytes,
		InputPattern:  "cpp-studio-transcription-*",
		ValidateInput: wav.ValidateFile,
		BuildArgs: func(inPath, _ string) []string {
			return []string{"-f", inPath}
		},
	}
}

// MaxSortformerDiarizationDuration is audio.cpp's architectural graph limit.
const MaxSortformerDiarizationDuration = 120 * time.Second

// DiarizationProvider identifies the engine contract used for a diarization
// result, including how its stdout must be parsed.
type DiarizationProvider string

const (
	DiarizationProviderSortformer DiarizationProvider = "sortformer"
	DiarizationProviderSherpa     DiarizationProvider = "sherpa-onnx"
)

// CanUseSortformer reports whether a request fits the packaged model and its
// fixed graph. A positive speaker count requests sherpa's exact clustering.
func CanUseSortformer(format wav.Format, duration time.Duration, numSpeakers int) bool {
	return numSpeakers == 0 && format.SampleRate == 16000 && format.Channels == 1 &&
		format.BitsPerSample == 16 && duration <= MaxSortformerDiarizationDuration
}

// SortformerDiarizationSpec invokes audio.cpp's CUDA Sortformer engine. The
// fixed graph window is sized to the request: 20 seconds is the model default,
// then 30-second steps up to the model's 120-second architectural limit.
func SortformerDiarizationSpec(wavBytes []byte, duration time.Duration) Spec {
	if wavBytes == nil {
		wavBytes = []byte{}
	}
	seconds := int(math.Ceil(duration.Seconds()))
	if seconds <= 20 {
		seconds = 20
	} else {
		seconds = ((seconds + 29) / 30) * 30
	}
	if seconds > 120 {
		seconds = 120
	}
	return Spec{
		Engine:        "diarize",
		Label:         "Sortformer speaker diarization command",
		Timeout:       DefaultDiarizationTimeout,
		Input:         wavBytes,
		InputPattern:  "cpp-studio-diarize-sortformer-*.wav",
		ValidateInput: wav.ValidateFile,
		BuildArgs: func(inPath, _ string) []string {
			return []string{
				"--session-option", fmt.Sprintf("session_len_sec=%d", seconds),
				"--audio", inPath,
			}
		},
	}
}

// SherpaDiarizationSpec invokes the "diarize-sherpa" fallback engine. The
// model flags live in the config args and the input WAV is the single
// positional argument. numSpeakers > 0 pins the cluster count (sherpa gives
// --clustering.num-clusters precedence over the configured threshold).
func SherpaDiarizationSpec(wavBytes []byte, numSpeakers int) Spec {
	if wavBytes == nil {
		wavBytes = []byte{}
	}
	return Spec{
		Engine:        "diarize-sherpa",
		Label:         "sherpa-onnx speaker diarization command",
		Timeout:       DefaultDiarizationTimeout,
		Input:         wavBytes,
		InputPattern:  "cpp-studio-diarize-sherpa-*.wav",
		ValidateInput: wav.ValidateFile,
		BuildArgs: func(inPath, _ string) []string {
			var args []string
			if numSpeakers > 0 {
				args = append(args, fmt.Sprintf("--clustering.num-clusters=%d", numSpeakers))
			}
			return append(args, inPath)
		},
	}
}

// ImportAudioSpec invokes the optional "ytdlp" engine: the user's own yt-dlp
// binary fetches the audio behind a URL into a temp file the gateway hands
// straight to the Extractor. Format selection (-f bestaudio/...) lives in the
// config args, since which formats a site offers — and which the browser can
// decode — is the operator's call.
//
// The three flags here are contract, not preference: --no-simulate keeps
// --print from turning the run into a dry run, --print emits the source title
// on stdout so a clip can carry its provenance, and --force-overwrites is
// required because the spec runner has already created the (empty) temp
// output file, which yt-dlp would otherwise treat as an existing download and
// skip.
func ImportAudioSpec(sourceURL string) Spec {
	return Spec{
		Engine:        "ytdlp",
		Label:         "yt-dlp import command",
		Timeout:       DefaultImportTimeout,
		OutputPattern: "cpp-studio-import-*",
		OutputLabel:   "imported audio",
		BuildArgs: func(_, outPath string) []string {
			return []string{
				"--no-simulate",
				"--print", "%(title)s",
				"--force-overwrites",
				"--no-playlist",
				"-o", outPath,
				sourceURL,
			}
		},
		ValidateOutput: func(path string) error {
			if err := validateFileSize(path, MaxImportOutputBytes, "imported audio"); err != nil {
				return fmt.Errorf("fetched oversized audio: %v", err)
			}
			head, err := readFileHead(path, 16)
			if err != nil {
				return fmt.Errorf("read imported audio: %v", err)
			}
			if _, ok := SniffAudioContentType(head); !ok {
				return fmt.Errorf("fetched a file the browser cannot decode as audio")
			}
			return nil
		},
	}
}

// AudioFormat is a delivery format the optional "ffmpeg" engine can encode.
// WAV is what the studio produces; these are what you can actually send to
// someone.
type AudioFormat struct {
	// ID is the wire name and the file extension.
	ID string `json:"id"`
	// Encoder is the ffmpeg encoder that must exist in the operator's
	// build. A binary without it is a binary that cannot make this format,
	// which is worth knowing before a long job rather than after it.
	Encoder string `json:"encoder"`
	// ContentType serves the finished file.
	ContentType string `json:"content_type"`
	// DefaultBitrate is used when a request names no bitrate.
	DefaultBitrate string `json:"default_bitrate"`
	Label          string `json:"label"`
}

// AudioFormats are the delivery formats the studio offers. MP3 and Opus are
// compact lossy delivery encodings; FLAC preserves the PCM losslessly.
var AudioFormats = []AudioFormat{
	{ID: "mp3", Encoder: "libmp3lame", ContentType: "audio/mpeg", DefaultBitrate: "128k", Label: "MP3"},
	{ID: "opus", Encoder: "libopus", ContentType: "audio/ogg", DefaultBitrate: "64k", Label: "Opus"},
	{ID: "flac", Encoder: "flac", ContentType: "audio/flac", Label: "FLAC"},
}

// LookupAudioFormat finds a delivery format by id.
func LookupAudioFormat(id string) (AudioFormat, bool) {
	for _, format := range AudioFormats {
		if format.ID == id {
			return format, true
		}
	}
	return AudioFormat{}, false
}

// ValidateBitrate keeps the bitrate argument to something ffmpeg will
// accept and a listener would want: 32k to 320k.
func ValidateBitrate(bitrate string) error {
	if !strings.HasSuffix(bitrate, "k") {
		return fmt.Errorf("bitrate must be given in k, e.g. 128k")
	}
	value, err := strconv.Atoi(strings.TrimSuffix(bitrate, "k"))
	if err != nil {
		return fmt.Errorf("bitrate must be given in k, e.g. 128k")
	}
	if value < 32 || value > 320 {
		return fmt.Errorf("bitrate must be between 32k and 320k")
	}
	return nil
}

// TranscodeSpec invokes the optional "ffmpeg" engine to turn a produced WAV
// into a delivery format. Both paths are real files: a transcode is the one
// operation here whose payload can be a whole half-hour recording.
//
// -nostdin stops ffmpeg consuming the parent's stdin, -y overwrites the temp
// file the caller has already created, and -vn drops any cover art rather
// than failing on it.
func TranscodeSpec(inPath string, outPath string, format AudioFormat, bitrate string) Spec {
	return Spec{
		Engine:     "ffmpeg",
		Label:      "ffmpeg transcode command",
		Timeout:    DefaultTranscodeTimeout,
		InputPath:  inPath,
		OutputPath: outPath,
		BuildArgs: func(in, out string) []string {
			args := []string{
				"-nostdin", "-y",
				"-i", in,
				"-vn",
				"-c:a", format.Encoder,
			}
			if format.DefaultBitrate != "" {
				args = append(args, "-b:a", bitrate)
			}
			return append(args, out)
		},
		ValidateOutput: func(path string) error {
			return ValidateEncodedAudio(path, format)
		},
	}
}

// ValidateEncodedAudio checks that an encoder produced the requested delivery
// container rather than merely exiting successfully with a non-empty file.
func ValidateEncodedAudio(path string, format AudioFormat) error {
	head, err := readFileHead(path, 16)
	if err != nil {
		return fmt.Errorf("read encoded audio: %v", err)
	}
	contentType, ok := SniffAudioContentType(head)
	if !ok || contentType != format.ContentType {
		return fmt.Errorf("encoded audio is not %s", format.Label)
	}
	return nil
}

// ProbeEncodedAudioSpec asks ffmpeg to decode the complete delivery file to a
// null sink. Container sniffing rejects a wrong format early; this probe catches
// truncation or corrupt frames before the Store publishes the derived file.
func ProbeEncodedAudioSpec(path string) Spec {
	return Spec{
		Engine:    "ffmpeg",
		Label:     "ffmpeg encoded-audio validation command",
		Timeout:   DefaultTranscodeTimeout,
		InputPath: path,
		BuildArgs: func(in, _ string) []string {
			return []string{"-nostdin", "-v", "error", "-xerror", "-i", in, "-map", "0:a:0", "-f", "null", "-"}
		},
	}
}

// DecodeAudioSpec turns anything ffmpeg understands into the one thing every
// browser does: 16-bit PCM in a WAV. This is the escape hatch for the files
// the Extractor's client-side decoder refuses — old MPEG-1/2 radio rips,
// WMA, AC3, video containers with unusual audio tracks.
//
// Mono is not a compromise here: the Extractor mixes to mono the moment it
// loads anything, so a stereo decode would double the bytes crossing the
// wire to produce identical results. The source sample rate is kept, because
// a voice cloned from this audio deserves the quality that was in the file.
func DecodeAudioSpec(inPath string, outPath string) Spec {
	return Spec{
		Engine:     "ffmpeg",
		Label:      "ffmpeg decode command",
		Timeout:    DefaultTranscodeTimeout,
		InputPath:  inPath,
		OutputPath: outPath,
		BuildArgs: func(in, out string) []string {
			return []string{
				"-nostdin", "-y",
				"-i", in,
				"-vn",
				"-ac", "1",
				"-c:a", "pcm_s16le",
				out,
			}
		},
		ValidateOutput: func(path string) error {
			if err := wav.ValidateFile(path); err != nil {
				return fmt.Errorf("produced something that is not a WAV: %v", err)
			}
			if err := validateFileSize(path, MaxDecodedAudioBytes, "decoded audio"); err != nil {
				return fmt.Errorf("decoded to more audio than the editor can hold: %v", err)
			}
			return nil
		},
	}
}

// Loudness is one BS.1770 measurement of a recording.
//
// Integrated is the gated integrated loudness in LUFS — the number that
// answers "how loud does this feel overall". TruePeak is the inter-sample
// peak in dBTP, which is what actually clips a listener's decoder. Range is
// the loudness range in LU, a measure of how much the level moves about.
type Loudness struct {
	Integrated float64 `json:"integrated_lufs"`
	TruePeak   float64 `json:"true_peak_dbtp"`
	Range      float64 `json:"range_lu"`
}

// LoudnessSpec measures a WAV without producing one. ffmpeg's loudnorm
// filter in analysis mode implements BS.1770 properly, which is the reason
// to shell out rather than hand-roll K-weighting and gating: getting those
// subtly wrong and then confidently reporting the wrong number is worse
// than not measuring at all.
func LoudnessSpec(path string) Spec {
	return Spec{
		Engine:    "ffmpeg",
		Label:     "ffmpeg loudness measurement",
		Timeout:   DefaultTranscodeTimeout,
		InputPath: path,
		BuildArgs: func(in, _ string) []string {
			return []string{
				"-nostdin", "-hide_banner",
				"-i", in,
				"-af", "loudnorm=print_format=json",
				"-f", "null", "-",
			}
		},
	}
}

// ParseLoudness pulls the measurement out of loudnorm's report, which
// ffmpeg prints to stderr as a JSON object after its usual log chatter.
func ParseLoudness(stderr []byte) (Loudness, error) {
	text := string(stderr)
	start := strings.LastIndex(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return Loudness{}, fmt.Errorf("ffmpeg printed no loudness report")
	}
	var report struct {
		InputI   string `json:"input_i"`
		InputTP  string `json:"input_tp"`
		InputLRA string `json:"input_lra"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &report); err != nil {
		return Loudness{}, fmt.Errorf("loudness report was not valid JSON: %v", err)
	}
	parse := func(field, raw string) (float64, error) {
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return 0, fmt.Errorf("loudness report has no usable %s", field)
		}
		return value, nil
	}
	integrated, err := parse("input_i", report.InputI)
	if err != nil {
		return Loudness{}, err
	}
	truePeak, err := parse("input_tp", report.InputTP)
	if err != nil {
		return Loudness{}, err
	}
	// A silent or near-silent input reports -inf; treat that as unmeasurable
	// rather than letting an infinity propagate into a gain calculation.
	if math.IsInf(integrated, 0) || math.IsInf(truePeak, 0) {
		return Loudness{}, fmt.Errorf("input is silent, so it has no measurable loudness")
	}
	loudness := Loudness{Integrated: integrated, TruePeak: truePeak}
	if lra, err := parse("input_lra", report.InputLRA); err == nil && !math.IsInf(lra, 0) {
		loudness.Range = lra
	}
	return loudness, nil
}

// EncodersSpec asks ffmpeg what it can encode. "ffmpeg is configured" and
// "ffmpeg can make an MP3" are different claims: builds vary, and finding
// out after a job is worse than finding out before one.
func EncodersSpec() Spec {
	return Spec{
		Engine:  "ffmpeg",
		Label:   "ffmpeg encoder probe",
		Timeout: 30 * time.Second,
		BuildArgs: func(_, _ string) []string {
			return []string{"-nostdin", "-hide_banner", "-encoders"}
		},
	}
}

// ParseEncoders picks encoder names out of `ffmpeg -encoders` output, whose
// rows look like " A....D libmp3lame           MP3 (MPEG audio layer 3)".
func ParseEncoders(stdout []byte) map[string]bool {
	found := make(map[string]bool)
	for _, line := range strings.Split(string(stdout), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || len(fields[0]) < 2 {
			continue
		}
		// The first column is the capability flag block; a row that is not
		// flags followed by a name is a heading or the legend.
		if strings.ContainsAny(fields[0], " ") || !strings.HasPrefix(fields[0], "A") && !strings.HasPrefix(fields[0], "V") && !strings.HasPrefix(fields[0], "S") {
			continue
		}
		found[fields[1]] = true
	}
	return found
}

// audioSignature matches a container by the bytes it starts with. offset is
// where the magic sits: MP4/M4A carry theirs at byte 4, after the box length.
type audioSignature struct {
	offset      int
	magic       []byte
	contentType string
}

// audioSignatures covers exactly what browsers decode through Web Audio,
// which is the only consumer that matters here: whatever the importer
// fetches has to survive decodeAudioData in the Extractor.
var audioSignatures = []audioSignature{
	{0, []byte("OggS"), "audio/ogg"},
	{0, []byte("fLaC"), "audio/flac"},
	{0, []byte("RIFF"), "audio/wav"},
	{0, []byte("ID3"), "audio/mpeg"},
	{0, []byte{0x1A, 0x45, 0xDF, 0xA3}, "audio/webm"},
	{4, []byte("ftyp"), "audio/mp4"},
}

// SniffAudioContentType identifies a fetched container from its first bytes.
// A bare MPEG frame sync (no ID3 tag) is matched separately because it is a
// bit pattern rather than a fixed string.
func SniffAudioContentType(head []byte) (string, bool) {
	for _, sig := range audioSignatures {
		end := sig.offset + len(sig.magic)
		if len(head) >= end && bytes.Equal(head[sig.offset:end], sig.magic) {
			return sig.contentType, true
		}
	}
	if len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0 {
		return "audio/mpeg", true
	}
	return "", false
}

func readFileHead(path string, n int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	head := make([]byte, n)
	read, err := io.ReadFull(file, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return head[:read], nil
}

// DiarizationSpan is one contiguous stretch of a single speaker's speech.
// Speaker is the tool's anonymous cluster index (0, 1, 2, ...).
type DiarizationSpan struct {
	Start   float64
	End     float64
	Speaker int
}

// ParseDiarization extracts speaker spans from sherpa-onnx's stdout, which
// interleaves log lines with spans shaped `0.031 -- 6.578 speaker_00`.
// Unparseable lines are skipped rather than fatal: the tool logs freely.
func ParseDiarization(stdout []byte) []DiarizationSpan {
	var spans []DiarizationSpan
	for _, line := range strings.Split(string(stdout), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 4 || fields[1] != "--" || !strings.HasPrefix(fields[3], "speaker_") {
			continue
		}
		start, err1 := strconv.ParseFloat(fields[0], 64)
		end, err2 := strconv.ParseFloat(fields[2], 64)
		speaker, err3 := strconv.Atoi(strings.TrimPrefix(fields[3], "speaker_"))
		if err1 != nil || err2 != nil || err3 != nil || end < start {
			continue
		}
		spans = append(spans, DiarizationSpan{Start: start, End: end, Speaker: speaker})
	}
	return spans
}

// ParseSortformerDiarization extracts audio.cpp's speaker_turns JSON line and
// converts its 16 kHz sample offsets to the gateway's seconds-based spans.
func ParseSortformerDiarization(stdout []byte) ([]DiarizationSpan, error) {
	const prefix = "speaker_turns="
	familySeen := false
	taskSeen := false
	type turn struct {
		StartSample int64  `json:"start_sample"`
		EndSample   int64  `json:"end_sample"`
		SpeakerID   string `json:"speaker_id"`
	}
	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.TrimSpace(line)
		familySeen = familySeen || line == "family=sortformer_diar"
		taskSeen = taskSeen || line == "task=diar"
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		var turns []turn
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &turns); err != nil {
			return nil, fmt.Errorf("parse Sortformer speaker turns: %v", err)
		}
		spans := make([]DiarizationSpan, 0, len(turns))
		for _, item := range turns {
			if item.StartSample < 0 || item.EndSample < item.StartSample || !strings.HasPrefix(item.SpeakerID, "SPEAKER_") {
				return nil, fmt.Errorf("parse Sortformer speaker turn: invalid span or speaker id")
			}
			speaker, err := strconv.Atoi(strings.TrimPrefix(item.SpeakerID, "SPEAKER_"))
			if err != nil {
				return nil, fmt.Errorf("parse Sortformer speaker turn %q: %v", item.SpeakerID, err)
			}
			spans = append(spans, DiarizationSpan{
				Start:   float64(item.StartSample) / 16000,
				End:     float64(item.EndSample) / 16000,
				Speaker: speaker,
			})
		}
		return spans, nil
	}
	if familySeen && taskSeen {
		return []DiarizationSpan{}, nil
	}
	return nil, fmt.Errorf("Sortformer output did not contain speaker_turns")
}

// ParseDiarization parses stdout according to the selected engine contract.
func (provider DiarizationProvider) ParseDiarization(stdout []byte) ([]DiarizationSpan, error) {
	switch provider {
	case DiarizationProviderSortformer:
		return ParseSortformerDiarization(stdout)
	case DiarizationProviderSherpa:
		return ParseDiarization(stdout), nil
	default:
		return nil, fmt.Errorf("unknown diarization provider %q", provider)
	}
}

// ImageSpec invokes the "sd" engine: --prompt <prompt> --output <png path>
// plus --width/--height when both are positive. The engine must produce a
// decodable PNG within MaxImageDimension and MaxImageOutputBytes.
func ImageSpec(prompt string, width, height int, seed int64) Spec {
	return Spec{
		Engine:        "sd",
		Label:         "sd image generation command",
		Timeout:       DefaultImageTimeout,
		OutputPattern: "cpp-studio-image-*.png",
		OutputLabel:   "generated png",
		BuildArgs: func(_, outPath string) []string {
			args := []string{"--prompt", prompt, "--output", outPath}
			if width > 0 && height > 0 {
				args = append(args, "--width", strconv.Itoa(width), "--height", strconv.Itoa(height))
			}
			// The seed is always concrete by the time a spec is built — the
			// gateway rolls one when the request left it out — so a good
			// image is always reproducible.
			args = append(args, "--seed", strconv.FormatInt(seed, 10))
			return args
		},
		ValidateOutput: func(path string) error {
			if err := validateFileSize(path, MaxImageOutputBytes, "generated png"); err != nil {
				return fmt.Errorf("produced oversized PNG: %v", err)
			}
			if err := validatePNGFile(path); err != nil {
				return fmt.Errorf("produced invalid PNG: %v", err)
			}
			return nil
		},
	}
}

// speechTextReplacements maps typography and common accented letters that
// chat models emit to spoken-equivalent ASCII.
var speechTextReplacements = map[rune]string{
	'‘': "'", '’': "'", '‚': "'", '‛': "'",
	'“': `"`, '”': `"`, '„': `"`,
	'‐': "-", '‑': "-", '‒': "-", '–': "-",
	'—': " - ", '―': " - ", '…': "...",
	' ': " ", ' ': " ", ' ': " ", ' ': " ",
	'•': "-", '×': "x", '÷': "/",
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'æ': "ae",
	'ç': "c", 'è': "e", 'é': "e", 'ê': "e", 'ë': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ñ': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'œ': "oe",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ý': "y", 'ÿ': "y", 'ß': "ss",
	'À': "A", 'Á': "A", 'Â': "A", 'Ã': "A", 'Ä': "A", 'Å': "A", 'Æ': "AE",
	'Ç': "C", 'È': "E", 'É': "E", 'Ê': "E", 'Ë': "E",
	'Ì': "I", 'Í': "I", 'Î': "I", 'Ï': "I", 'Ñ': "N",
	'Ò': "O", 'Ó': "O", 'Ô': "O", 'Õ': "O", 'Ö': "O", 'Ø': "O", 'Œ': "OE",
	'Ù': "U", 'Ú': "U", 'Û': "U", 'Ü': "U", 'Ý': "Y",
}

// sanitizeSpeechText rewrites input so every byte is printable ASCII plus
// space. Mapped runes are transliterated; unmapped non-ASCII runes and
// control characters are dropped, with whitespace collapsed to single spaces.
func sanitizeSpeechText(input string) string {
	var b strings.Builder
	b.Grow(len(input))
	for _, r := range input {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r >= 0x20 && r < 0x7F:
			b.WriteRune(r)
		default:
			if repl, ok := speechTextReplacements[r]; ok {
				b.WriteString(repl)
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// ValidateImageDimensions enforces the shared caps on requested and
// produced image sizes.
func ValidateImageDimensions(width int, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("image dimensions must be positive")
	}
	if width > MaxImageDimension || height > MaxImageDimension {
		return fmt.Errorf("image dimensions must be at most %dx%d", MaxImageDimension, MaxImageDimension)
	}
	if width > maxImagePixels/height {
		return fmt.Errorf("image dimensions must contain at most %d pixels", maxImagePixels)
	}
	return nil
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

func validatePNGFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("open generated image: %v", err)
	}
	return ValidatePNGBytes(data)
}

// ValidatePNGBytes enforces the same guarantees as the file-based output
// validator on in-memory image bytes: within MaxImageOutputBytes, a genuine
// PNG, and within the dimension caps. Server-mode image engines return bytes
// over HTTP rather than a temp file, so they validate through here.
func ValidatePNGBytes(data []byte) error {
	if int64(len(data)) > MaxImageOutputBytes {
		return fmt.Errorf("produced oversized PNG: %d bytes exceeds %d", len(data), MaxImageOutputBytes)
	}
	if len(data) < len(pngSignature) || !bytes.Equal(data[:len(pngSignature)], pngSignature) {
		return fmt.Errorf("unsupported image file: expected PNG signature")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode PNG metadata: %v", err)
	}
	if err := ValidateImageDimensions(cfg.Width, cfg.Height); err != nil {
		return err
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("decode PNG image: %v", err)
	}
	return nil
}

func validateFileSize(path string, maxBytes int64, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %v", label, err)
	}
	if info.Size() > maxBytes {
		return fmt.Errorf("%s is %d bytes, max is %d bytes", label, info.Size(), maxBytes)
	}
	return nil
}
