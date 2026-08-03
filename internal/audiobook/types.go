package audiobook

import (
	"time"

	"cpp-studio/internal/engine"
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

// Seed is shared with the engine transport and retains decimal-string JSON.
type Seed = engine.Seed

type SynthesisOptions = engine.SynthesisOptions
type SynthesisRequest = engine.SynthesisRequest

// Attempt describes one immutable synthesis output for a Section.
type Attempt struct {
	ID                    string            `json:"id"`
	ParentAttemptID       string            `json:"parentAttemptId,omitempty"`
	Seed                  Seed              `json:"seed"`
	RequestedSeed         Seed              `json:"requestedSeed"`
	ActualSeed            *Seed             `json:"actualSeed,omitempty"`
	SeedStatus            string            `json:"seedStatus,omitempty"`
	CheckpointFingerprint string            `json:"checkpointFingerprint"`
	AudioFile             string            `json:"audioFile"`
	AudioSHA256           string            `json:"audioSha256"`
	TranscriptFile        string            `json:"transcriptFile,omitempty"`
	VerificationFile      string            `json:"verificationFile,omitempty"`
	Selected              bool              `json:"selected"`
	CreatedAt             time.Time         `json:"createdAt"`
	Options               *SynthesisOptions `json:"options,omitempty"`
	SynthesisMS           float64           `json:"synthesisMs,omitempty"`
	VerificationMS        float64           `json:"verificationMs,omitempty"`
	DurationMS            int64             `json:"durationMs,omitempty"`
	VerificationStatus    SectionStatus     `json:"verificationStatus,omitempty"`
	DeterministicMatch    *bool             `json:"deterministicMatch,omitempty"`
}

type RenderRevision struct {
	ID               string            `json:"id"`
	ArtifactFile     string            `json:"artifactFile"`
	ArtifactURL      string            `json:"artifactUrl"`
	SelectedAttempts map[string]string `json:"selectedAttempts"`
	CreatedAt        time.Time         `json:"createdAt"`
	DurationSeconds  int               `json:"durationSeconds"`
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
	VerificationFile      string        `json:"verificationFile,omitempty"`
	DurationMS            int64         `json:"durationMs,omitempty"`
	Attempts              []Attempt     `json:"attempts,omitempty"`
}

// VerificationSummary records aggregate fidelity-checking state.
type VerificationSummary struct {
	Mode             VerificationMode   `json:"mode"`
	Status           VerificationStatus `json:"status"`
	VerifiedSections int                `json:"verifiedSections"`
	FlaggedSections  int                `json:"flaggedSections"`
	ReportFile       string             `json:"reportFile,omitempty"`
	Error            string             `json:"error,omitempty"`
}

// Manifest describes either a legacy finished audiobook or a versioned durable
// Audiobook production. Versioned-only fields are omitted for legacy manifests.
type Manifest struct {
	SchemaVersion            int                  `json:"schemaVersion,omitempty"`
	ID                       string               `json:"id"`
	Title                    string               `json:"title"`
	VoiceID                  string               `json:"voiceId,omitempty"`
	EngineID                 string               `json:"engine,omitempty"`
	Direction                string               `json:"direction,omitempty"`
	PromptSpec               DramaBoxPromptSpec   `json:"promptSpec,omitempty"`
	PromptWarnings           []PromptWarning      `json:"promptWarnings,omitempty"`
	AIGenerated              bool                 `json:"aiGenerated,omitempty"`
	Watermark                string               `json:"watermark,omitempty"`
	BenchmarkProfileIdentity string               `json:"benchmarkProfileIdentity,omitempty"`
	Chunks                   int                  `json:"chunks"`
	DurationSeconds          int                  `json:"durationSeconds"`
	CreatedAt                time.Time            `json:"createdAt"`
	ArtifactURL              string               `json:"artifactUrl"`
	Status                   ProductionStatus     `json:"status,omitempty"`
	SourceFile               string               `json:"sourceFile,omitempty"`
	SourceSHA256             string               `json:"sourceSha256,omitempty"`
	SynthesisFingerprint     string               `json:"synthesisFingerprint,omitempty"`
	SectionPolicyVersion     int                  `json:"sectionPolicyVersion,omitempty"`
	PromptPolicyVersion      int                  `json:"promptPolicyVersion,omitempty"`
	Sections                 []Section            `json:"sections,omitempty"`
	Verification             *VerificationSummary `json:"verification,omitempty"`
	ResolvedOptions          *SynthesisOptions    `json:"resolvedOptions,omitempty"`
	SynthesisIdentity        *SynthesisIdentity   `json:"synthesisIdentity,omitempty"`
	RenderRevisions          []RenderRevision     `json:"renderRevisions,omitempty"`
	CurrentRenderID          string               `json:"currentRenderId,omitempty"`
}
