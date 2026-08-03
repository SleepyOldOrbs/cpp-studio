package voice

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cpp-studio/internal/wav"
)

const (
	DefaultVoicesRootDir  = "out/voices"
	MaxReferenceWAVBytes  = 32 * 1024 * 1024
	MaxVoiceNameChars     = 80
	MaxVoiceTranscriptLen = 4000
	referenceWAVName      = "ref.wav"
)

// Clone is one stored cloned voice: a reference WAV on disk plus the
// transcript the TTS engine conditions on. Protected voices refuse
// deletion — for curated voices that should survive library housekeeping.
type Clone struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Transcript string    `json:"transcript"`
	CreatedAt  time.Time `json:"created_at"`
	Protected  bool      `json:"protected,omitempty"`
	// Source says where this voice came from, when the studio knows: the
	// recording, the seconds inside it, and the speaker tag. A voice
	// outlives every clip it was cut from, so the one place this can be
	// answered later is here.
	Source *CloneSource `json:"source,omitempty"`
	// Analysis is computed once from the immutable reference and persisted.
	// Older manifests omit it and are upgraded lazily on first load.
	Analysis *ReferenceAnalysis `json:"analysis,omitempty"`
}

// ReferenceAnalysis records PCM facts and an honestly labelled heuristic
// speech estimate. It never claims the optional VAD ran.
type ReferenceAnalysis struct {
	ContentSHA256          string    `json:"content_sha256"`
	DurationSeconds        float64   `json:"duration_seconds"`
	UsableSpeechSeconds    float64   `json:"usable_speech_seconds"`
	SampleRate             uint32    `json:"sample_rate"`
	Channels               uint16    `json:"channels"`
	BitsPerSample          uint16    `json:"bits_per_sample"`
	PeakAmplitude          float64   `json:"peak_amplitude"`
	ClippedSampleRatio     float64   `json:"clipped_sample_ratio"`
	RMS                    float64   `json:"rms"`
	LeadingLowEnergyRatio  float64   `json:"leading_low_energy_ratio"`
	TrailingLowEnergyRatio float64   `json:"trailing_low_energy_ratio"`
	TotalLowEnergyRatio    float64   `json:"total_low_energy_ratio"`
	Method                 string    `json:"method"`
	VADStatus              string    `json:"vad_status"`
	VADError               string    `json:"vad_error,omitempty"`
	Fitness                string    `json:"fitness"`
	Warnings               []string  `json:"warnings,omitempty"`
	AnalyzedAt             time.Time `json:"analyzed_at"`
}

// CloneSource is the provenance of a cloned voice. The Extractor already
// knows all of it at the moment it mints a voice; recording it is the
// difference between a library that can say whose voice this is and one
// that cannot.
type CloneSource struct {
	Name     string  `json:"name"`
	StartSec float64 `json:"start_sec,omitempty"`
	EndSec   float64 `json:"end_sec,omitempty"`
	Speaker  string  `json:"speaker,omitempty"`
}

// ErrProtected reports a deletion attempt on a protected voice.
var ErrProtected = errors.New("voice is protected and cannot be deleted")

// Store persists cloned voices, one directory per voice holding ref.wav and
// manifest.json, in the same shape as the story store.
type Store struct {
	rootDir string
	vad     VADAnalyzer
}

// VADAnalyzer returns an optional measured spoken duration. The caller owns
// the configured VAD capability; Store falls back to PCM facts on any error.
type VADAnalyzer func([]byte) (time.Duration, error)

type StoreOptions struct {
	AnalyzeVAD VADAnalyzer
}

func NewStore(rootDir string) *Store {
	return NewStoreWithOptions(rootDir, StoreOptions{})
}

func NewStoreWithOptions(rootDir string, options StoreOptions) *Store {
	if rootDir == "" {
		rootDir = DefaultVoicesRootDir
	}
	return &Store{rootDir: rootDir, vad: options.AnalyzeVAD}
}

// Save validates and persists a new cloned voice, returning it with a fresh
// ID. The name and transcript arrive already trimmed by the caller.
// Protected voices refuse later deletion.
func (s *Store) Save(name string, transcript string, refWAV []byte, protected bool) (Clone, error) {
	return s.SaveWithSource(name, transcript, refWAV, protected, nil)
}

// SaveWithSource is Save with the voice's provenance attached. Callers that
// know where the reference came from — the Extractor, above all — should use
// this so the library can answer the question later.
func (s *Store) SaveWithSource(name string, transcript string, refWAV []byte, protected bool, source *CloneSource) (Clone, error) {
	if name == "" {
		return Clone{}, fmt.Errorf("voice name is required")
	}
	if len(name) > MaxVoiceNameChars {
		return Clone{}, fmt.Errorf("voice name cannot exceed %d characters", MaxVoiceNameChars)
	}
	if transcript == "" {
		return Clone{}, fmt.Errorf("voice transcript is required")
	}
	if len(transcript) > MaxVoiceTranscriptLen {
		return Clone{}, fmt.Errorf("voice transcript cannot exceed %d characters", MaxVoiceTranscriptLen)
	}
	if len(refWAV) > MaxReferenceWAVBytes {
		return Clone{}, fmt.Errorf("reference wav is %d bytes, max is %d bytes", len(refWAV), MaxReferenceWAVBytes)
	}
	if err := wav.ValidateBytes(refWAV); err != nil {
		return Clone{}, fmt.Errorf("reference wav: %w", err)
	}
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return Clone{}, fmt.Errorf("create voices dir: %w", err)
	}

	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return Clone{}, fmt.Errorf("generate voice id: %w", err)
	}
	clone := Clone{
		ID:         fmt.Sprintf("voice_%s_%s", time.Now().UTC().Format("20060102_150405"), hex.EncodeToString(suffix)),
		Name:       name,
		Transcript: transcript,
		CreatedAt:  time.Now().UTC(),
		Protected:  protected,
		Source:     source,
		Analysis:   analyzeReference(refWAV, s.vad),
	}

	tmpDir := filepath.Join(s.rootDir, "."+clone.ID+".tmp")
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return Clone{}, fmt.Errorf("create temp voice dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, referenceWAVName), refWAV, 0o644); err != nil {
		return Clone{}, fmt.Errorf("write reference wav: %w", err)
	}
	data, err := json.MarshalIndent(clone, "", "  ")
	if err != nil {
		return Clone{}, fmt.Errorf("encode voice manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return Clone{}, fmt.Errorf("write voice manifest: %w", err)
	}
	if err := os.Rename(tmpDir, filepath.Join(s.rootDir, clone.ID)); err != nil {
		return Clone{}, fmt.Errorf("finalize voice dir: %w", err)
	}
	return clone, nil
}

func (s *Store) Load(id string) (Clone, bool, error) {
	if err := validateVoiceID(id); err != nil {
		return Clone{}, false, nil
	}
	data, err := os.ReadFile(filepath.Join(s.rootDir, id, "manifest.json"))
	if os.IsNotExist(err) {
		return Clone{}, false, nil
	}
	if err != nil {
		return Clone{}, false, fmt.Errorf("read voice manifest: %w", err)
	}
	var clone Clone
	if err := json.Unmarshal(data, &clone); err != nil {
		return Clone{}, false, fmt.Errorf("decode voice manifest: %w", err)
	}
	if clone.Analysis == nil {
		refWAV, readErr := os.ReadFile(filepath.Join(s.rootDir, id, referenceWAVName))
		if readErr != nil {
			return Clone{}, false, fmt.Errorf("analyze voice reference: %w", readErr)
		}
		clone.Analysis = analyzeReference(refWAV, s.vad)
		updated, marshalErr := json.MarshalIndent(clone, "", "  ")
		if marshalErr != nil {
			return Clone{}, false, fmt.Errorf("encode analyzed voice manifest: %w", marshalErr)
		}
		if writeErr := os.WriteFile(filepath.Join(s.rootDir, id, "manifest.json"), append(updated, '\n'), 0o644); writeErr != nil {
			return Clone{}, false, fmt.Errorf("persist voice analysis: %w", writeErr)
		}
	}
	return clone, true, nil
}

func analyzeReference(refWAV []byte, vad VADAnalyzer) *ReferenceAnalysis {
	hash := sha256.Sum256(refWAV)
	result := &ReferenceAnalysis{
		ContentSHA256: hex.EncodeToString(hash[:]), Method: "pcm-heuristic-v1", VADStatus: "not-configured",
		Fitness: "good", AnalyzedAt: time.Now().UTC(),
	}
	format, _, decodeErr := wav.Decode(refWAV)
	if decodeErr == nil {
		result.SampleRate = format.SampleRate
		result.Channels = format.Channels
		result.BitsPerSample = format.BitsPerSample
	}
	if duration, durationErr := wav.Duration(refWAV); durationErr == nil {
		result.DurationSeconds = duration.Seconds()
	}
	analysis, err := wav.AnalyzePCM16(refWAV)
	if err != nil {
		result.Fitness = "unsupported"
		result.Warnings = []string{err.Error()}
		return result
	}
	result.DurationSeconds = analysis.Duration.Seconds()
	result.UsableSpeechSeconds = analysis.UsableSpeechDuration.Seconds()
	result.SampleRate = analysis.Format.SampleRate
	result.Channels = analysis.Format.Channels
	result.BitsPerSample = analysis.Format.BitsPerSample
	result.PeakAmplitude = analysis.PeakAmplitude
	result.ClippedSampleRatio = analysis.ClippedSampleRatio
	result.RMS = analysis.RMS
	result.LeadingLowEnergyRatio = analysis.LeadingLowEnergyRatio
	result.TrailingLowEnergyRatio = analysis.TrailingLowEnergyRatio
	result.TotalLowEnergyRatio = analysis.TotalLowEnergyRatio
	if result.ClippedSampleRatio > 0.01 {
		result.Warnings = append(result.Warnings, "more than 1% of samples are clipped")
	}
	if result.TotalLowEnergyRatio > 0.4 {
		result.Warnings = append(result.Warnings, "more than 40% of the reference is low energy")
	}
	if result.RMS < 0.02 {
		result.Warnings = append(result.Warnings, "reference level is very low")
	}
	if len(result.Warnings) > 0 {
		result.Fitness = "warning"
	}
	if vad != nil {
		spoken, vadErr := vad(refWAV)
		if vadErr != nil {
			result.VADStatus = "failed"
			result.VADError = vadErr.Error()
			result.Warnings = append(result.Warnings, "configured VAD failed; usable speech uses the PCM heuristic")
			result.Fitness = "warning"
		} else if spoken < 0 || spoken > analysis.Duration {
			result.VADStatus = "failed"
			result.VADError = "reported spoken duration is outside the reference duration"
			result.Warnings = append(result.Warnings, "configured VAD returned an invalid duration; usable speech uses the PCM heuristic")
			result.Fitness = "warning"
		} else {
			result.UsableSpeechSeconds = spoken.Seconds()
			result.Method = "configured-vad+pcm-v1"
			result.VADStatus = "used"
		}
	}
	return result
}

func (s *Store) List() ([]Clone, error) {
	entries, err := os.ReadDir(s.rootDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read voices dir: %w", err)
	}
	clones := make([]Clone, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		clone, ok, err := s.Load(entry.Name())
		if err != nil || !ok {
			continue
		}
		clones = append(clones, clone)
	}
	sort.Slice(clones, func(i, j int) bool {
		return clones[i].CreatedAt.After(clones[j].CreatedAt)
	})
	return clones, nil
}

// ReferencePath returns the absolute path of a voice's reference WAV, for
// handing to the speech engine and for serving playback.
func (s *Store) ReferencePath(id string) (string, error) {
	if err := validateVoiceID(id); err != nil {
		return "", err
	}
	path := filepath.Join(s.rootDir, id, referenceWAVName)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("voice reference not found")
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := wav.ValidateHeader(file); err != nil {
		return "", fmt.Errorf("voice reference is not a valid WAV")
	}
	return filepath.Abs(path)
}

func (s *Store) Delete(id string) error {
	if err := validateVoiceID(id); err != nil {
		return fmt.Errorf("voice not found")
	}
	if clone, ok, err := s.Load(id); err != nil {
		return err
	} else if ok && clone.Protected {
		return ErrProtected
	}
	if err := os.RemoveAll(filepath.Join(s.rootDir, id)); err != nil {
		return fmt.Errorf("delete voice dir: %w", err)
	}
	return nil
}

func validateVoiceID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || filepath.IsAbs(id) {
		return fmt.Errorf("invalid voice id")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("invalid voice id")
	}
	return nil
}
