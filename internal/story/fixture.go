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
// a model's paraphrase. A sketch has nothing to extract: its scaffold is the
// cast alone.
func BuildScaffold(req NormalizedRequest) (Scaffold, error) {
	if req.Mode == ModeSketch {
		return Scaffold{Cast: castOrDefault(req.Cast)}, nil
	}
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

	return Scaffold{
		Sources: sources,
		Notes:   notes,
		Facts:   factCardsFromNotes(notes),
		Cast:    castOrDefault(req.Cast),
	}, nil
}

// castOrDefault falls back to the default trio speaking in the studio
// default voice when a request named no cast.
func castOrDefault(cast []CastMember) []CastMember {
	if len(cast) > 0 {
		return cast
	}
	cast = DefaultCast()
	for i := range cast {
		cast[i].VoiceID = "studio-default"
	}
	return cast
}

// AssembleManifest builds and validates the final manifest from a scaffold
// plus a title and script (model-written or fixture). The request's mode
// decides which contract the script is held to.
func AssembleManifest(id string, req NormalizedRequest, createdAt time.Time, scaffold Scaffold, title string, script []ScriptLine) (Manifest, error) {
	artifactURL := fmt.Sprintf("/v1/stories/%s/artifact/%s", id, StoryArtifactName)
	// Every stored line needs a stable id before it can own takes.
	script = AssignLineIDs(append([]ScriptLine{}, script...))
	manifest := Manifest{
		ID:              id,
		Subject:         req.Subject,
		Mode:            req.Mode,
		Premise:         req.Premise,
		Style:           req.Style,
		Title:           title,
		Status:          StatusComplete,
		CreatedAt:       createdAt.UTC(),
		DurationSeconds: req.TargetSeconds,
		Sources:         scaffold.Sources,
		SourceNotes:     scaffold.Notes,
		FactCards:       scaffold.Facts,
		Cast:            scaffold.Cast,
		Scenes:          req.Scenes,
		Script:          script,
		Audio: AudioRef{
			Format: "wav",
			URL:    artifactURL,
		},
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func BuildFixtureManifest(id string, req NormalizedRequest, createdAt time.Time) (Manifest, []byte, error) {
	scaffold, err := BuildScaffold(req)
	if err != nil {
		return Manifest{}, nil, err
	}
	manifest, err := AssembleManifest(id, req, createdAt, scaffold, titleForRequest(req), FixtureScript(req, scaffold))
	if err != nil {
		return Manifest{}, nil, err
	}
	return manifest, fixtureWAV(req.TargetSeconds), nil
}

// FixtureScript is the deterministic script for a request with no model
// behind it: grounded requests get the cited star-birth dialogue, sketches
// get citation-free banter.
func FixtureScript(req NormalizedRequest, scaffold Scaffold) []ScriptLine {
	if req.Mode == ModeSketch {
		return fixtureSketchScript(req.Subject, scaffold.Cast)
	}
	return fixtureScriptForCast(req.Subject, scaffold.Facts, scaffold.Cast)
}

// ValidateManifest is the final gate before a story is stored, dispatching
// on the mode the story was written under. Sketches are held to the script's
// shape alone; grounded stories additionally have to cite their facts. Both
// modes are held to the scene invariants first — scenes are structure, not
// contract.
func ValidateManifest(manifest Manifest) error {
	if err := validateScenes(manifest); err != nil {
		return err
	}
	if manifest.Mode == ModeSketch {
		return validateScriptShape(manifest, CodeInvalidScript)
	}
	return ValidateManifestGrounding(manifest)
}

// validateScenes holds a manifest to the scene invariants: either no scenes
// at all (the whole script is one unnamed scene — every pre-episode story),
// or every line names a declared scene, lines form one contiguous run per
// scene, and runs follow the declared order. The invariants are what let
// everything downstream — grouping, checkpoints, later per-scene assets —
// treat "the lines of scene N" as a simple slice of the script.
func validateScenes(manifest Manifest) error {
	if len(manifest.Scenes) == 0 {
		for i, line := range manifest.Script {
			if line.SceneID != "" {
				return NewError(CodeInvalidScenes, fmt.Sprintf("script[%d] names scene %q but the story declares no scenes", i, line.SceneID))
			}
		}
		return nil
	}
	order := make(map[string]int, len(manifest.Scenes))
	for i, scene := range manifest.Scenes {
		if scene.ID == "" {
			return NewError(CodeInvalidScenes, fmt.Sprintf("scenes[%d].id is required", i))
		}
		if _, dup := order[scene.ID]; dup {
			return NewError(CodeInvalidScenes, fmt.Sprintf("scenes contains duplicate scene id %q", scene.ID))
		}
		order[scene.ID] = i
	}
	last := -1
	for i, line := range manifest.Script {
		if line.SceneID == "" {
			return NewError(CodeInvalidScenes, fmt.Sprintf("script[%d].scene_id is required when scenes are declared", i))
		}
		index, ok := order[line.SceneID]
		if !ok {
			return NewError(CodeInvalidScenes, fmt.Sprintf("script[%d] names unknown scene %q", i, line.SceneID))
		}
		if index != last {
			// A new run must be exactly the next declared scene: a smaller
			// index is a scene resuming after other scenes interrupted it, a
			// jump would leave the skipped scene without lines.
			if index != last+1 {
				return NewError(CodeInvalidScenes, fmt.Sprintf("script[%d]: scene %q is out of order; lines must follow the declared scene order in contiguous runs", i, line.SceneID))
			}
			last = index
		}
	}
	if last != len(manifest.Scenes)-1 {
		return NewError(CodeInvalidScenes, fmt.Sprintf("scene %q has no script lines", manifest.Scenes[last+1].ID))
	}
	return nil
}

// validateScriptShape is the floor both modes share: every line is speakable
// and belongs to a cast member.
func validateScriptShape(manifest Manifest, code ErrorCode) error {
	speakers := make(map[string]bool, len(manifest.Cast))
	for _, speaker := range manifest.Cast {
		speakers[speaker.ID] = true
	}
	for i, line := range manifest.Script {
		if strings.TrimSpace(line.Text) == "" {
			return NewError(code, fmt.Sprintf("script[%d].text is required", i))
		}
		if len(line.Text) > MaxScriptLineTextChars {
			return NewError(code, fmt.Sprintf("script[%d].text is too long", i))
		}
		if !speakers[line.SpeakerID] {
			return NewError(code, fmt.Sprintf("script[%d].speaker_id is not in cast", i))
		}
	}
	return nil
}

// ValidateManifestGrounding is the grounded-mode contract: on top of the
// shared script shape, every line cites at least one known, non-conflicting
// fact card.
func ValidateManifestGrounding(manifest Manifest) error {
	facts := make(map[string]FactCard, len(manifest.FactCards))
	for _, fact := range manifest.FactCards {
		if len(fact.SourceNoteIDs) == 0 {
			return NewError(CodeGroundingFailure, fmt.Sprintf("fact card %s has no source notes", fact.ID))
		}
		facts[fact.ID] = fact
	}
	if err := validateScriptShape(manifest, CodeGroundingFailure); err != nil {
		return err
	}
	for i, line := range manifest.Script {
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

// fixtureScriptForCast is the deterministic fixture dialogue reassigned to
// whatever cast the request defined: line i speaks as cast[i % len(cast)],
// so grounding holds for any speaker set.
func fixtureScriptForCast(subject string, facts []FactCard, cast []CastMember) []ScriptLine {
	script := fixtureScript(subject, facts)
	if len(cast) == 0 {
		return script
	}
	known := make(map[string]bool, len(cast))
	for _, member := range cast {
		known[member.ID] = true
	}
	for i := range script {
		if !known[script[i].SpeakerID] {
			script[i].SpeakerID = cast[i%len(cast)].ID
		}
	}
	return script
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

// fixtureSketchScript is the deterministic sketch dialogue: no fact ids,
// speakers rotating through whatever cast the request defined, so the
// fixture gateway can produce a sketch end to end without a model.
func fixtureSketchScript(subject string, cast []CastMember) []ScriptLine {
	if len(cast) == 0 {
		cast = DefaultCast()
	}
	texts := []string{
		"Right, I've been thinking about " + subject + ", and I've come to a conclusion.",
		"Oh, this should be good. Go on then.",
		"It's a scandal, is what it is. Nobody warned me.",
		"Nobody warns anybody. That's the whole business model.",
		"I did warn you. Twice. In writing.",
		"Letters don't count if you post them to yourself.",
		"They count double, actually. It's called planning.",
		"Well. I suppose that settles it.",
		"It settles nothing, and I shall be back tomorrow to say so again.",
	}
	script := make([]ScriptLine, 0, len(texts))
	for i, text := range texts {
		script = append(script, ScriptLine{
			SpeakerID: cast[i%len(cast)].ID,
			Text:      text,
		})
	}
	return script
}

// titleForRequest names a story the way its mode would.
func titleForRequest(req NormalizedRequest) string {
	if req.Mode == ModeSketch {
		return "A Sketch About " + req.Subject
	}
	return titleForSubject(req.Subject)
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
