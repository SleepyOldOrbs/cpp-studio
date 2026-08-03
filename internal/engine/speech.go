package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	DefaultDramaBoxInferenceSteps    = 30
	DefaultDramaBoxGuidanceScale     = 2.5
	DefaultDramaBoxChunkThresholdSec = 45.0
	DefaultDramaBoxChunkDurationSec  = 37.0
	DefaultDramaBoxCrossFadeSec      = 0.05
	MaxDramaBoxInferenceSteps        = 100
	MaxDramaBoxGuidanceScale         = 8.0
	MaxDramaBoxChunkSeconds          = 300.0
	MaxDramaBoxCrossFadeSeconds      = 2.0
	maxSynthesisOptionsJSONBytes     = 4096
)

// Seed is encoded as a decimal JSON string so the complete uint64 range is
// preserved across browser and manifest boundaries.
type Seed uint64

func (s Seed) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatUint(uint64(s), 10))
}

func (s *Seed) UnmarshalJSON(data []byte) error {
	var encoded string
	if err := json.Unmarshal(data, &encoded); err != nil {
		return fmt.Errorf("decode seed string: %w", err)
	}
	value, err := strconv.ParseUint(encoded, 10, 64)
	if err != nil {
		return fmt.Errorf("decode seed value: %w", err)
	}
	if strconv.FormatUint(value, 10) != encoded {
		return fmt.Errorf("decode seed value: %q is not canonical decimal", encoded)
	}
	*s = Seed(value)
	return nil
}

// SynthesisOptions is the complete effective option set used for one speech
// invocation. Seed is assigned by the audiobook planner, never by HTTP input.
type SynthesisOptions struct {
	Seed                   Seed    `json:"seed,omitempty"`
	NumInferenceSteps      int     `json:"num_inference_steps"`
	GuidanceScale          float64 `json:"guidance_scale"`
	AudioChunkThresholdSec float64 `json:"audio_chunk_threshold_sec"`
	AudioChunkDurationSec  float64 `json:"audio_chunk_duration_sec"`
	CrossFadeDurationSec   float64 `json:"cross_fade_duration_sec"`
}

// SynthesisRequest replaces the positional audiobook speech callback. Options
// are empty for the legacy audio narrator and fully resolved for DramaBox.
type SynthesisRequest struct {
	Text     string           `json:"text,omitempty"`
	VoiceID  string           `json:"voice_id,omitempty"`
	EngineID string           `json:"engine_id"`
	Options  SynthesisOptions `json:"options,omitempty"`
}

// DefaultDramaBoxOptions pins the effective release-0.5 values. Persisting
// every value prevents a later upstream default from changing Resume identity.
func DefaultDramaBoxOptions() SynthesisOptions {
	return SynthesisOptions{
		NumInferenceSteps:      DefaultDramaBoxInferenceSteps,
		GuidanceScale:          DefaultDramaBoxGuidanceScale,
		AudioChunkThresholdSec: DefaultDramaBoxChunkThresholdSec,
		AudioChunkDurationSec:  DefaultDramaBoxChunkDurationSec,
		CrossFadeDurationSec:   DefaultDramaBoxCrossFadeSec,
	}
}

// ResolveSynthesisOptions parses the advanced JSON escape hatch with an exact
// allowlist, rejects duplicate keys, fills defaults, and applies bounded
// release-0.5 validation. The seed is intentionally not an accepted key.
func ResolveSynthesisOptions(engineID, raw string) (SynthesisOptions, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if engineID == DramaBoxSpeechEngineID {
			return DefaultDramaBoxOptions(), nil
		}
		return SynthesisOptions{}, nil
	}
	if len(raw) > maxSynthesisOptionsJSONBytes {
		return SynthesisOptions{}, fmt.Errorf("synthesis options must be at most %d bytes", maxSynthesisOptionsJSONBytes)
	}
	if engineID != DramaBoxSpeechEngineID {
		return SynthesisOptions{}, fmt.Errorf("synthesis options are only supported with dramabox")
	}

	options := DefaultDramaBoxOptions()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return SynthesisOptions{}, fmt.Errorf("decode synthesis options: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return SynthesisOptions{}, fmt.Errorf("synthesis options must be a JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return SynthesisOptions{}, fmt.Errorf("decode synthesis option name: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return SynthesisOptions{}, fmt.Errorf("synthesis option names must be strings")
		}
		if seen[key] {
			return SynthesisOptions{}, fmt.Errorf("duplicate synthesis option %q", key)
		}
		seen[key] = true
		switch key {
		case "num_inference_steps":
			if err := decoder.Decode(&options.NumInferenceSteps); err != nil {
				return SynthesisOptions{}, optionDecodeError(key, err)
			}
		case "guidance_scale":
			if err := decoder.Decode(&options.GuidanceScale); err != nil {
				return SynthesisOptions{}, optionDecodeError(key, err)
			}
		case "audio_chunk_threshold_sec":
			if err := decoder.Decode(&options.AudioChunkThresholdSec); err != nil {
				return SynthesisOptions{}, optionDecodeError(key, err)
			}
		case "audio_chunk_duration_sec":
			if err := decoder.Decode(&options.AudioChunkDurationSec); err != nil {
				return SynthesisOptions{}, optionDecodeError(key, err)
			}
		case "cross_fade_duration_sec":
			if err := decoder.Decode(&options.CrossFadeDurationSec); err != nil {
				return SynthesisOptions{}, optionDecodeError(key, err)
			}
		default:
			return SynthesisOptions{}, fmt.Errorf("unknown synthesis option %q", key)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return SynthesisOptions{}, fmt.Errorf("decode synthesis options: %w", err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return SynthesisOptions{}, fmt.Errorf("decode synthesis options: %w", err)
		}
		return SynthesisOptions{}, fmt.Errorf("unexpected JSON value after synthesis options: %v", token)
	}
	if err := ValidateSynthesisOptions(engineID, options); err != nil {
		return SynthesisOptions{}, err
	}
	return options, nil
}

func optionDecodeError(key string, err error) error {
	return fmt.Errorf("synthesis option %q has an invalid value: %w", key, err)
}

// ValidateSynthesisOptions rejects unsupported combinations before production
// creation or native-engine invocation.
func ValidateSynthesisOptions(engineID string, options SynthesisOptions) error {
	if engineID != DramaBoxSpeechEngineID {
		if options != (SynthesisOptions{}) {
			return fmt.Errorf("synthesis options are only supported with dramabox")
		}
		return nil
	}
	if options.NumInferenceSteps < 1 || options.NumInferenceSteps > MaxDramaBoxInferenceSteps {
		return fmt.Errorf("num_inference_steps must be between 1 and %d", MaxDramaBoxInferenceSteps)
	}
	if !finiteBetween(options.GuidanceScale, 0, MaxDramaBoxGuidanceScale) {
		return fmt.Errorf("guidance_scale must be between 0 and %.0f", MaxDramaBoxGuidanceScale)
	}
	if !finiteBetween(options.AudioChunkThresholdSec, 1, MaxDramaBoxChunkSeconds) {
		return fmt.Errorf("audio_chunk_threshold_sec must be between 1 and %.0f", MaxDramaBoxChunkSeconds)
	}
	if !finiteBetween(options.AudioChunkDurationSec, 1, MaxDramaBoxChunkSeconds) {
		return fmt.Errorf("audio_chunk_duration_sec must be between 1 and %.0f", MaxDramaBoxChunkSeconds)
	}
	if !finiteBetween(options.CrossFadeDurationSec, 0, MaxDramaBoxCrossFadeSeconds) {
		return fmt.Errorf("cross_fade_duration_sec must be between 0 and %.0f", MaxDramaBoxCrossFadeSeconds)
	}
	if options.CrossFadeDurationSec*2 > options.AudioChunkDurationSec {
		return fmt.Errorf("cross_fade_duration_sec must not exceed half audio_chunk_duration_sec")
	}
	return nil
}

func finiteBetween(value, minimum, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum && value <= maximum
}

// SpeechVoiceSpecForRequest owns the subprocess mapping for typed synthesis.
func SpeechVoiceSpecForRequest(request SynthesisRequest, voice *Voice) Spec {
	spec := SpeechVoiceSpecFor(request.EngineID, request.Text, voice)
	if request.EngineID != DramaBoxSpeechEngineID {
		return spec
	}
	options := request.Options
	spec.BuildArgs = func(_, outPath string) []string {
		return []string{
			"--text", sanitizeSpeechText(request.Text),
			"--seed", strconv.FormatUint(uint64(options.Seed), 10),
			"--num-inference-steps", strconv.Itoa(options.NumInferenceSteps),
			"--guidance-scale", strconv.FormatFloat(options.GuidanceScale, 'f', -1, 64),
			"--request-option", "audio_chunk_threshold_sec=" + strconv.FormatFloat(options.AudioChunkThresholdSec, 'f', -1, 64),
			"--request-option", "audio_chunk_duration_sec=" + strconv.FormatFloat(options.AudioChunkDurationSec, 'f', -1, 64),
			"--request-option", "cross_fade_duration_sec=" + strconv.FormatFloat(options.CrossFadeDurationSec, 'f', -1, 64),
			"--out", outPath,
		}
	}
	return spec
}

type serverSpeechRequest struct {
	Model             string         `json:"model"`
	Input             string         `json:"input"`
	VoiceRef          string         `json:"voice_ref,omitempty"`
	ReferenceText     string         `json:"reference_text,omitempty"`
	Seed              *Seed          `json:"seed,omitempty"`
	NumInferenceSteps int            `json:"num_inference_steps,omitempty"`
	GuidanceScale     *float64       `json:"guidance_scale,omitempty"`
	Options           map[string]any `json:"options,omitempty"`
}

// MarshalSpeechServerRequest owns the resident audio.cpp JSON mapping.
func MarshalSpeechServerRequest(model string, request SynthesisRequest, voice *Voice, defaultVoice *Voice) ([]byte, error) {
	selectedVoice := defaultVoice
	if voice != nil {
		selectedVoice = voice
	}
	payload := serverSpeechRequest{Model: model, Input: request.Text}
	if selectedVoice != nil {
		payload.VoiceRef = strings.ReplaceAll(selectedVoice.RefWAVPath, `\`, "/")
		payload.ReferenceText = selectedVoice.RefText
	}
	if request.EngineID == DramaBoxSpeechEngineID {
		payload.Seed = &request.Options.Seed
		payload.NumInferenceSteps = request.Options.NumInferenceSteps
		payload.GuidanceScale = &request.Options.GuidanceScale
		payload.Options = map[string]any{
			"audio_chunk_threshold_sec": request.Options.AudioChunkThresholdSec,
			"audio_chunk_duration_sec":  request.Options.AudioChunkDurationSec,
			"cross_fade_duration_sec":   request.Options.CrossFadeDurationSec,
		}
	}
	return json.Marshal(payload)
}

// DescribeSynthesisMapping returns a semantic preview without text, paths, or
// a user-controlled seed. It is informational; invocation uses the functions above.
func DescribeSynthesisMapping(mode string) map[string]any {
	if mode == "server" {
		return map[string]any{
			"top_level": []string{"seed", "num_inference_steps", "guidance_scale"},
			"options":   []string{"audio_chunk_threshold_sec", "audio_chunk_duration_sec", "cross_fade_duration_sec"},
		}
	}
	return map[string]any{
		"flags":           []string{"--seed", "--num-inference-steps", "--guidance-scale"},
		"request_options": []string{"audio_chunk_threshold_sec", "audio_chunk_duration_sec", "cross_fade_duration_sec"},
	}
}

// CompactOptionsJSON is used by fingerprints and diagnostics where a stable,
// complete representation is required.
func CompactOptionsJSON(options SynthesisOptions) string {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(options)
	return strings.TrimSpace(buf.String())
}
