package audiobook

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// CurrentManifestSchemaVersion is written by new durable Audiobook productions.
const CurrentManifestSchemaVersion = 2

// ProductionStatus identifies the durable phase of an Audiobook production.
type ProductionStatus string

const (
	ProductionStatusSynthesizing ProductionStatus = "synthesizing"
	ProductionStatusVerifying    ProductionStatus = "verifying"
	ProductionStatusStitching    ProductionStatus = "stitching"
	ProductionStatusInterrupted  ProductionStatus = "interrupted"
	ProductionStatusComplete     ProductionStatus = "complete"
)

// SectionStatus identifies how far one planned source section has advanced.
type SectionStatus string

const (
	SectionStatusPending     SectionStatus = "pending"
	SectionStatusSynthesized SectionStatus = "synthesized"
	SectionStatusVerified    SectionStatus = "verified"
	SectionStatusFlagged     SectionStatus = "flagged"
)

// VerificationMode records the user's fidelity-checking requirement.
type VerificationMode string

const (
	VerificationModeAuto     VerificationMode = "auto"
	VerificationModeRequired VerificationMode = "required"
	VerificationModeOff      VerificationMode = "off"
)

// VerificationStatus records the aggregate fidelity result for a production.
type VerificationStatus string

const (
	VerificationStatusPending     VerificationStatus = "pending"
	VerificationStatusPassed      VerificationStatus = "passed"
	VerificationStatusFlagged     VerificationStatus = "flagged"
	VerificationStatusUnavailable VerificationStatus = "unavailable"
	VerificationStatusSkipped     VerificationStatus = "skipped"
)

// Seed is persisted as a decimal JSON string so the full uint64 range survives
// JavaScript and other JSON consumers that cannot represent it exactly as a number.
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

// Attempt describes one immutable synthesis output for a Section.
type Attempt struct {
	ID                    string    `json:"id"`
	ParentAttemptID       string    `json:"parentAttemptId,omitempty"`
	Seed                  Seed      `json:"seed"`
	CheckpointFingerprint string    `json:"checkpointFingerprint"`
	AudioFile             string    `json:"audioFile"`
	AudioSHA256           string    `json:"audioSha256"`
	TranscriptFile        string    `json:"transcriptFile,omitempty"`
	VerificationFile      string    `json:"verificationFile,omitempty"`
	Selected              bool      `json:"selected"`
	CreatedAt             time.Time `json:"createdAt"`
}

// Section binds a stable canonical-source byte range to its durable outputs.
// Its output fields project the selected Attempt once attempts are recorded.
type Section struct {
	ID                    string        `json:"id"`
	StartByte             int64         `json:"startByte"`
	EndByte               int64         `json:"endByte"`
	TextSHA256            string        `json:"textSha256"`
	Seed                  Seed          `json:"seed"`
	CheckpointFingerprint string        `json:"checkpointFingerprint"`
	Status                SectionStatus `json:"status"`
	AudioFile             string        `json:"audioFile,omitempty"`
	AudioSHA256           string        `json:"audioSha256,omitempty"`
	TranscriptFile        string        `json:"transcriptFile,omitempty"`
	DurationMS            int64         `json:"durationMs,omitempty"`
	Attempts              []Attempt     `json:"attempts,omitempty"`
}

// VerificationSummary records aggregate fidelity-checking state.
type VerificationSummary struct {
	Mode             VerificationMode   `json:"mode"`
	Status           VerificationStatus `json:"status"`
	VerifiedSections int                `json:"verifiedSections"`
	FlaggedSections  int                `json:"flaggedSections"`
}

// Manifest describes either a legacy finished audiobook or a versioned durable
// Audiobook production. Versioned-only fields are omitted for legacy manifests.
type Manifest struct {
	SchemaVersion        int                  `json:"schemaVersion,omitempty"`
	ID                   string               `json:"id"`
	Title                string               `json:"title"`
	VoiceID              string               `json:"voiceId,omitempty"`
	EngineID             string               `json:"engine,omitempty"`
	Direction            string               `json:"direction,omitempty"`
	Chunks               int                  `json:"chunks"`
	DurationSeconds      int                  `json:"durationSeconds"`
	CreatedAt            time.Time            `json:"createdAt"`
	ArtifactURL          string               `json:"artifactUrl"`
	Status               ProductionStatus     `json:"status,omitempty"`
	SourceFile           string               `json:"sourceFile,omitempty"`
	SourceSHA256         string               `json:"sourceSha256,omitempty"`
	SynthesisFingerprint string               `json:"synthesisFingerprint,omitempty"`
	SectionPolicyVersion int                  `json:"sectionPolicyVersion,omitempty"`
	PromptPolicyVersion  int                  `json:"promptPolicyVersion,omitempty"`
	Sections             []Section            `json:"sections,omitempty"`
	Verification         *VerificationSummary `json:"verification,omitempty"`
}
