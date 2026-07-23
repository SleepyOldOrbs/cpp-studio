package engine

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"cpp-studio/internal/wav"
)

const (
	DefaultTranscriptionTimeout = 120 * time.Second
	DefaultSpeechTimeout        = 180 * time.Second
	DefaultImageTimeout         = 300 * time.Second

	MaxSpeechOutputBytes = 32 * 1024 * 1024
	MaxImageOutputBytes  = 32 * 1024 * 1024
	MaxImageDimension    = 2048
	maxImagePixels       = MaxImageDimension * MaxImageDimension
)

// Voice identifies a cloned voice reference for speech synthesis: the
// reference WAV on disk plus its transcript. A nil *Voice means the config
// default voice.
type Voice struct {
	RefWAVPath string
	RefText    string
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
	text := sanitizeSpeechText(input)
	var overrides map[string]string
	if voice != nil {
		overrides = map[string]string{
			"--voice-ref":      voice.RefWAVPath,
			"--reference-text": sanitizeSpeechText(voice.RefText),
		}
	}
	return Spec{
		Engine:        "audio",
		Label:         "audio speech command",
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

// ImageSpec invokes the "sd" engine: --prompt <prompt> --output <png path>
// plus --width/--height when both are positive. The engine must produce a
// decodable PNG within MaxImageDimension and MaxImageOutputBytes.
func ImageSpec(prompt string, width, height int) Spec {
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

func validatePNGFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open generated image: %v", err)
	}
	defer file.Close()

	header := make([]byte, 8)
	if _, err := io.ReadFull(file, header); err != nil {
		return fmt.Errorf("unsupported image file: expected PNG signature")
	}
	pngSignature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if !bytes.Equal(header, pngSignature) {
		return fmt.Errorf("unsupported image file: expected PNG signature")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind generated image: %v", err)
	}
	cfg, err := png.DecodeConfig(file)
	if err != nil {
		return fmt.Errorf("decode PNG metadata: %v", err)
	}
	if err := ValidateImageDimensions(cfg.Width, cfg.Height); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind generated image: %v", err)
	}
	if _, err := png.Decode(file); err != nil {
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
