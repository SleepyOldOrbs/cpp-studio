package audiobook

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSeedJSONRoundTripUsesDecimalString(t *testing.T) {
	want := Seed(math.MaxUint64)

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if got := string(encoded); got != `"18446744073709551615"` {
		t.Fatalf("seed JSON = %s, want a decimal string", got)
	}

	var decoded Seed
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal seed: %v", err)
	}
	if decoded != want {
		t.Fatalf("decoded seed = %d, want %d", decoded, want)
	}
}

func TestSeedJSONRejectsLossyOrOutOfRangeValues(t *testing.T) {
	for _, input := range []string{
		`18446744073709551615`,
		`"18446744073709551616"`,
		`"-1"`,
		`"+1"`,
		`"01"`,
		`"not-a-seed"`,
	} {
		var seed Seed
		if err := json.Unmarshal([]byte(input), &seed); err == nil {
			t.Errorf("json.Unmarshal(%s) succeeded, want an error", input)
		}
	}
}

func TestAttemptUsesExplicitParentAttemptID(t *testing.T) {
	attempt := Attempt{ParentAttemptID: "attempt-0001"}

	encoded, err := json.Marshal(attempt)
	if err != nil {
		t.Fatalf("marshal attempt: %v", err)
	}
	if !strings.Contains(string(encoded), `"parentAttemptId":"attempt-0001"`) {
		t.Fatalf("attempt JSON has ambiguous parent field: %s", encoded)
	}
}

func TestVersionTwoManifestJSONRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, time.August, 3, 10, 0, 0, 0, time.UTC)
	want := Manifest{
		SchemaVersion:        CurrentManifestSchemaVersion,
		ID:                   "book_20260803_100000_001",
		Title:                "A factual book",
		VoiceID:              "voice_001",
		EngineID:             DramaBoxEngineID,
		Direction:            "Measured documentary narration.",
		Status:               ProductionStatusSynthesizing,
		CreatedAt:            createdAt,
		SourceFile:           "source.txt",
		SourceSHA256:         strings.Repeat("a", 64),
		SynthesisFingerprint: strings.Repeat("b", 64),
		SectionPolicyVersion: 1,
		PromptPolicyVersion:  1,
		Sections: []Section{{
			ID:                    "section-0001",
			StartByte:             0,
			EndByte:               42,
			TextSHA256:            strings.Repeat("c", 64),
			Seed:                  Seed(math.MaxUint64),
			CheckpointFingerprint: strings.Repeat("d", 64),
			Status:                SectionStatusSynthesized,
			AudioFile:             "sections/section-0001.wav",
			AudioSHA256:           strings.Repeat("e", 64),
			DurationMS:            12_345,
			Attempts: []Attempt{{
				ID:                    "attempt-0001",
				Seed:                  Seed(math.MaxUint64),
				CheckpointFingerprint: strings.Repeat("d", 64),
				AudioFile:             "sections/section-0001.wav",
				AudioSHA256:           strings.Repeat("e", 64),
				Selected:              true,
				CreatedAt:             createdAt,
			}},
		}},
		Verification: &VerificationSummary{
			Mode:             VerificationModeAuto,
			Status:           VerificationStatusPending,
			VerifiedSections: 0,
			FlaggedSections:  0,
		},
	}

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	jsonText := string(encoded)
	for _, fragment := range []string{
		`"schemaVersion":2`,
		`"status":"synthesizing"`,
		`"attempts":[`,
		`"seed":"18446744073709551615"`,
	} {
		if !strings.Contains(jsonText, fragment) {
			t.Fatalf("manifest JSON missing %s: %s", fragment, jsonText)
		}
	}

	var got Manifest
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest round trip mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestLegacyManifestRemainsReadableWithoutVersionedFields(t *testing.T) {
	legacyJSON := `{
  "id": "book_20260802_090000_001",
  "title": "Legacy book",
  "voiceId": "voice_legacy",
  "engine": "audio",
  "chunks": 3,
  "durationSeconds": 42,
  "createdAt": "2026-08-02T09:00:00Z",
  "artifactUrl": "/v1/audiobooks/book_20260802_090000_001/artifact/book.wav"
}`

	var manifest Manifest
	if err := json.Unmarshal([]byte(legacyJSON), &manifest); err != nil {
		t.Fatalf("unmarshal legacy manifest: %v", err)
	}
	if manifest.SchemaVersion != 0 || manifest.Status != "" || len(manifest.Sections) != 0 || manifest.Verification != nil {
		t.Fatalf("legacy manifest gained versioned state: %#v", manifest)
	}
	if manifest.ID != "book_20260802_090000_001" || manifest.Title != "Legacy book" || manifest.Chunks != 3 {
		t.Fatalf("legacy manifest fields changed: %#v", manifest)
	}

	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal legacy manifest: %v", err)
	}
	jsonText := string(encoded)
	for _, field := range []string{`"schemaVersion"`, `"status"`, `"sections"`, `"verification"`} {
		if strings.Contains(jsonText, field) {
			t.Fatalf("legacy JSON unexpectedly contains %s: %s", field, jsonText)
		}
	}
}
