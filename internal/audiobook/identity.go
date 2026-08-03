package audiobook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	"cpp-studio/internal/engine"
)

const CurrentPromptPolicyVersion = 1

// EngineIdentity is the resolved native runtime/model configuration selected
// for one production. Fingerprint changes whenever audio-affecting config does.
type EngineIdentity struct {
	ID          string `json:"id"`
	Mode        string `json:"mode"`
	ModelID     string `json:"modelId"`
	Fingerprint string `json:"fingerprint"`
}

// VoiceIdentity records the authorized reference selected for production.
// Reference is runtime-only; manifests retain its content identity, not a host path.
type VoiceIdentity struct {
	ID                       string        `json:"id"`
	Fingerprint              string        `json:"fingerprint"`
	ReferenceSHA256          string        `json:"referenceSha256,omitempty"`
	UsableSpeechSeconds      float64       `json:"usableSpeechSeconds,omitempty"`
	FitnessMethod            string        `json:"fitnessMethod,omitempty"`
	FitnessWarnings          []string      `json:"fitnessWarnings,omitempty"`
	DramaBoxIneligibleReason string        `json:"dramaboxIneligibleReason,omitempty"`
	Reference                *engine.Voice `json:"-"`
}

// SynthesisIdentity is the immutable base identity used to decide whether
// durable sections can belong to the same audiobook production. Section seeds
// are deliberately excluded and enter each checkpoint fingerprint instead.
type SynthesisIdentity struct {
	Fingerprint          string           `json:"fingerprint"`
	SourceSHA256         string           `json:"sourceSha256"`
	Engine               EngineIdentity   `json:"engine"`
	Voice                VoiceIdentity    `json:"voice"`
	Direction            string           `json:"direction,omitempty"`
	Options              SynthesisOptions `json:"options"`
	SectionPolicyVersion int              `json:"sectionPolicyVersion"`
	PromptPolicyVersion  int              `json:"promptPolicyVersion"`
}

type ResolveEngineFunc func(ctx context.Context, engineID string) (EngineIdentity, error)
type ResolveVoiceFunc func(ctx context.Context, voiceID string) (VoiceIdentity, error)

// ResolvedRequest is a normalized request plus the runtime identities frozen
// by the Manager before planning or engine reservation.
type ResolvedRequest struct {
	Request Request
	Engine  EngineIdentity
	Voice   VoiceIdentity
}

func buildSynthesisIdentity(req Request, resolvedEngine EngineIdentity, resolvedVoice VoiceIdentity) SynthesisIdentity {
	sourceSum := sha256.Sum256([]byte(req.Text))
	options := req.Options
	options.Seed = 0
	identity := SynthesisIdentity{
		SourceSHA256:         hex.EncodeToString(sourceSum[:]),
		Engine:               resolvedEngine,
		Voice:                resolvedVoice,
		Direction:            req.Direction,
		Options:              options,
		SectionPolicyVersion: CurrentSectionPolicyVersion,
		PromptPolicyVersion:  CurrentPromptPolicyVersion,
	}

	h := sha256.New()
	add := func(value string) {
		_, _ = fmt.Fprintf(h, "%d:%s", len(value), value)
	}
	add(identity.SourceSHA256)
	add(identity.Engine.ID)
	add(identity.Engine.Mode)
	add(identity.Engine.ModelID)
	add(identity.Engine.Fingerprint)
	add(identity.Voice.ID)
	add(identity.Voice.Fingerprint)
	add(identity.Voice.ReferenceSHA256)
	add(identity.Direction)
	add(engine.CompactOptionsJSON(identity.Options))
	add(strconv.Itoa(identity.SectionPolicyVersion))
	add(strconv.Itoa(identity.PromptPolicyVersion))
	identity.Fingerprint = hex.EncodeToString(h.Sum(nil))
	return identity
}

func prepareSectionCheckpoints(identity SynthesisIdentity, sections []Section) []Section {
	prepared := make([]Section, len(sections))
	copy(prepared, sections)
	for i := range prepared {
		prepared[i].AudioFile = "sections/" + prepared[i].ID + ".wav"
		prepared[i].CheckpointFingerprint = sectionCheckpointFingerprint(identity.Fingerprint, prepared[i])
	}
	return prepared
}

func sectionCheckpointFingerprint(baseFingerprint string, section Section) string {
	h := sha256.New()
	add := func(value string) {
		_, _ = fmt.Fprintf(h, "%d:%s", len(value), value)
	}
	add(baseFingerprint)
	add(section.ID)
	add(strconv.FormatInt(section.StartByte, 10))
	add(strconv.FormatInt(section.EndByte, 10))
	add(section.TextSHA256)
	add(strconv.FormatUint(uint64(section.Seed), 10))
	return hex.EncodeToString(h.Sum(nil))
}
