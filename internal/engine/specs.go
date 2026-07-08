package engine

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"os"
	"strconv"
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

// SpeechSpec invokes the "audio" engine: --text <input> --out <wav path>.
// The engine must produce a valid WAV of at most MaxSpeechOutputBytes.
func SpeechSpec(input string) Spec {
	return Spec{
		Engine:        "audio",
		Label:         "audio speech command",
		Timeout:       DefaultSpeechTimeout,
		OutputPattern: "cpp-studio-speech-*.wav",
		OutputLabel:   "generated wav",
		BuildArgs: func(_, outPath string) []string {
			return []string{"--text", input, "--out", outPath}
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
