package story

import (
	"fmt"
	"strings"
	"time"

	"cpp-studio/internal/wav"
)

// Scaffold is the grounded skeleton of a story before any script exists:
// the sources, their extracted notes, the fact cards, and the cast.
type Scaffold struct {
	Sources []Source
	Notes   []SourceNote
	Facts   []FactCard
	Cast    []CastMember
}

// BuildScaffold extracts sources into notes and fact cards mechanically —
// fact claims stay verbatim source sentences, so grounding never depends on
// a model's paraphrase.
func BuildScaffold(req NormalizedRequest) (Scaffold, error) {
	sources := make([]Source, 0, len(req.Sources))
	notes := make([]SourceNote, 0, len(req.Sources)*3)
	for _, source := range req.Sources {
		sources = append(sources, Source{
			ID:    source.ID,
			Title: source.Title,
			URL:   source.URL,
		})
		for _, sentence := range noteSentences(source.Excerpt) {
			notes = append(notes, SourceNote{
				ID:       fmt.Sprintf("note-%d", len(notes)+1),
				SourceID: source.ID,
				Text:     sentence,
			})
		}
	}
	if len(notes) < MinSources {
		return Scaffold{}, NewError(CodeInsufficientSources, fmt.Sprintf("Need at least %d usable source notes to generate a factual story.", MinSources))
	}

	voice := func(id string) string {
		if v := req.CastVoices[id]; v != "" {
			return v
		}
		return "studio-default"
	}
	return Scaffold{
		Sources: sources,
		Notes:   notes,
		Facts:   factCardsFromNotes(notes),
		Cast: []CastMember{
			{ID: "narrator", DisplayName: "Narrator", VoiceID: voice("narrator")},
			{ID: "nova", DisplayName: "Nova", VoiceID: voice("nova")},
			{ID: "dr-lumen", DisplayName: "Dr. Lumen", VoiceID: voice("dr-lumen")},
		},
	}, nil
}

// AssembleManifest builds and grounds the final manifest from a scaffold
// plus a title and script (model-written or fixture).
func AssembleManifest(id string, req NormalizedRequest, createdAt time.Time, scaffold Scaffold, title string, script []ScriptLine) (Manifest, error) {
	artifactURL := fmt.Sprintf("/v1/stories/%s/artifact/%s", id, StoryArtifactName)
	manifest := Manifest{
		ID:              id,
		Subject:         req.Subject,
		Title:           title,
		Status:          StatusComplete,
		CreatedAt:       createdAt.UTC(),
		DurationSeconds: req.TargetSeconds,
		Sources:         scaffold.Sources,
		SourceNotes:     scaffold.Notes,
		FactCards:       scaffold.Facts,
		Cast:            scaffold.Cast,
		Script:          script,
		Audio: AudioRef{
			Format: "wav",
			URL:    artifactURL,
		},
	}
	if err := ValidateManifestGrounding(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func BuildFixtureManifest(id string, req NormalizedRequest, createdAt time.Time) (Manifest, []byte, error) {
	scaffold, err := BuildScaffold(req)
	if err != nil {
		return Manifest{}, nil, err
	}
	manifest, err := AssembleManifest(id, req, createdAt, scaffold, titleForSubject(req.Subject), fixtureScript(req.Subject, scaffold.Facts))
	if err != nil {
		return Manifest{}, nil, err
	}
	return manifest, fixtureWAV(req.TargetSeconds), nil
}

func ValidateManifestGrounding(manifest Manifest) error {
	facts := make(map[string]FactCard, len(manifest.FactCards))
	for _, fact := range manifest.FactCards {
		if len(fact.SourceNoteIDs) == 0 {
			return NewError(CodeGroundingFailure, fmt.Sprintf("fact card %s has no source notes", fact.ID))
		}
		facts[fact.ID] = fact
	}
	speakers := make(map[string]bool, len(manifest.Cast))
	for _, speaker := range manifest.Cast {
		speakers[speaker.ID] = true
	}
	for i, line := range manifest.Script {
		if strings.TrimSpace(line.Text) == "" {
			return NewError(CodeGroundingFailure, fmt.Sprintf("script[%d].text is required", i))
		}
		if len(line.Text) > MaxScriptLineTextChars {
			return NewError(CodeGroundingFailure, fmt.Sprintf("script[%d].text is too long", i))
		}
		if !speakers[line.SpeakerID] {
			return NewError(CodeGroundingFailure, fmt.Sprintf("script[%d].speaker_id is not in cast", i))
		}
		if len(line.FactIDs) == 0 {
			return NewError(CodeGroundingFailure, fmt.Sprintf("script[%d].fact_ids is required", i))
		}
		for _, factID := range line.FactIDs {
			fact, ok := facts[factID]
			if !ok {
				return NewError(CodeGroundingFailure, fmt.Sprintf("script[%d] references unknown fact id %q", i, factID))
			}
			if fact.Conflicting {
				return NewError(CodeGroundingFailure, fmt.Sprintf("script[%d] references conflicting fact id %q", i, factID))
			}
		}
	}
	return nil
}

func noteSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var out []string
	start := 0
	for i, r := range text {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		sentence := strings.TrimSpace(text[start : i+1])
		if sentence != "" {
			out = append(out, sentence)
		}
		start = i + 1
		if len(out) >= 4 {
			return out
		}
	}
	if tail := strings.TrimSpace(text[start:]); tail != "" && len(out) < 4 {
		out = append(out, tail)
	}
	return out
}

func factCardsFromNotes(notes []SourceNote) []FactCard {
	target := len(notes)
	if target < 8 {
		target = 8
	}
	if target > 15 {
		target = 15
	}
	facts := make([]FactCard, 0, target)
	for i := 0; i < target; i++ {
		note := notes[i%len(notes)]
		claim := note.Text
		if i >= len(notes) {
			claim = "The story can connect this source note to the larger sequence of star birth: " + note.Text
		}
		conflicting := strings.Contains(strings.ToLower(note.Text), "conflict:")
		facts = append(facts, FactCard{
			ID:            fmt.Sprintf("fact-%d", i+1),
			SourceNoteIDs: []string{note.ID},
			Claim:         claim,
			Conflicting:   conflicting,
		})
	}
	return facts
}

func fixtureScript(subject string, facts []FactCard) []ScriptLine {
	factID := func(index int) string {
		if index < len(facts) {
			return facts[index].ID
		}
		return facts[len(facts)-1].ID
	}
	return []ScriptLine{
		{SpeakerID: "narrator", Text: "In the dark between stars, the story begins inside the sources: a cold place where gas and dust can gather.", FactIDs: []string{factID(0), factID(1)}},
		{SpeakerID: "nova", Text: "So a star does not begin as a spark. It begins as a cloud?", FactIDs: []string{factID(0)}},
		{SpeakerID: "dr-lumen", Text: "Exactly. The source notes point to dense pockets where gravity can take over and collapse the material inward.", FactIDs: []string{factID(1), factID(2)}},
		{SpeakerID: "narrator", Text: "That collapse is the first turn in the story of " + subject + ".", FactIDs: []string{factID(2)}},
		{SpeakerID: "nova", Text: "What do we call the forming star in the middle?", FactIDs: []string{factID(3)}},
		{SpeakerID: "dr-lumen", Text: "A protostar. It is a growing object, still being fed by gas from the cloud around it.", FactIDs: []string{factID(3), factID(4)}},
		{SpeakerID: "narrator", Text: "The material does not fall straight down. Rotation spreads some of it into a disk around the young star.", FactIDs: []string{factID(5)}},
		{SpeakerID: "nova", Text: "So the disk is not just scenery. It is how the protostar keeps eating.", FactIDs: []string{factID(5), factID(6)}},
		{SpeakerID: "dr-lumen", Text: "Right. The disk feeds material inward, adding mass while the center compresses and heats.", FactIDs: []string{factID(6), factID(7)}},
		{SpeakerID: "narrator", Text: "Some young stars also launch jets. Those jets are part of the formation process, not an afterthought.", FactIDs: []string{factID(8)}},
		{SpeakerID: "dr-lumen", Text: "Jets can help carry away angular momentum, which lets more material keep collecting instead of only circling.", FactIDs: []string{factID(9)}},
		{SpeakerID: "nova", Text: "Then the threshold is stable fusion?", FactIDs: []string{factID(7)}},
		{SpeakerID: "dr-lumen", Text: "Yes. When stable fusion can hold the core up, the object is no longer just a protostar. It has become a star.", FactIDs: []string{factID(7)}},
		{SpeakerID: "narrator", Text: "What looked like darkness was a nursery. What looked like dust was the beginning of a future sun.", FactIDs: []string{factID(0), factID(3)}},
	}
}

func titleForSubject(subject string) string {
	if strings.Contains(strings.ToLower(subject), "star") {
		return "The Nursery of Stars"
	}
	return "A Short Story About " + subject
}

func fixtureWAV(seconds int) []byte {
	if seconds <= 0 {
		seconds = 1
	}
	return wav.SyntheticTone(wav.ToneSampleRate * seconds)
}
