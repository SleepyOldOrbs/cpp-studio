package storybuilder

import (
	"strings"
	"unicode/utf8"

	"cpp-studio/internal/story"
	"cpp-studio/internal/wav"
)

const importedStaleDialogueDurationMS int64 = 2400

type StoryImportSpeaker struct {
	ID                        string `json:"id"`
	DisplayName               string `json:"display_name"`
	SourceActorVoiceID        string `json:"source_actor_voice_id,omitempty"`
	SuggestedCharacterVoiceID string `json:"suggested_character_voice_id,omitempty"`
}

type StoryImportPreview struct {
	StoryID  string               `json:"story_id"`
	Title    string               `json:"title"`
	Speakers []StoryImportSpeaker `json:"speakers"`
}

type StorySpeakerMapping struct {
	SpeakerID        string `json:"speaker_id"`
	CharacterVoiceID string `json:"character_voice_id"`
}

type StoryImportRequest struct {
	Mappings []StorySpeakerMapping `json:"mappings"`
}

type StoryCharacterVoiceLister func(actorVoiceID string) ([]VoiceIdentity, error)

// StoryImporter opens a retained Story as a separately owned Story Builder
// Project. It only reads Story state; the project and copied takes are
// published together beneath the Story Builder store.
type StoryImporter struct {
	stories             *story.Manager
	projects            *Store
	listCharacterVoices StoryCharacterVoiceLister
}

func NewStoryImporter(stories *story.Manager, projects *Store, list StoryCharacterVoiceLister) *StoryImporter {
	return &StoryImporter{stories: stories, projects: projects, listCharacterVoices: list}
}

func (i *StoryImporter) Preview(storyID string) (StoryImportPreview, error) {
	manifest, ok, err := i.stories.LoadRetained(storyID)
	if err != nil {
		return StoryImportPreview{}, err
	}
	if !ok {
		return StoryImportPreview{}, ErrStoryNotFound
	}
	preview := StoryImportPreview{StoryID: manifest.ID, Title: manifest.Title, Speakers: make([]StoryImportSpeaker, 0, len(manifest.Cast))}
	for _, member := range manifest.Cast {
		speaker := StoryImportSpeaker{ID: member.ID, DisplayName: member.DisplayName, SourceActorVoiceID: member.VoiceID}
		if member.VoiceID != "" && i.listCharacterVoices != nil {
			characters, listErr := i.listCharacterVoices(member.VoiceID)
			if listErr != nil {
				return StoryImportPreview{}, listErr
			}
			if len(characters) == 1 && characters[0].ActorVoiceID == member.VoiceID {
				speaker.SuggestedCharacterVoiceID = characters[0].CharacterVoiceID
			}
		}
		preview.Speakers = append(preview.Speakers, speaker)
	}
	return preview, nil
}

func (i *StoryImporter) Import(storyID string, request StoryImportRequest) (Project, error) {
	manifest, ok, err := i.stories.LoadRetained(storyID)
	if err != nil {
		return Project{}, err
	}
	if !ok {
		return Project{}, ErrStoryNotFound
	}
	mappings, err := i.resolveMappings(manifest, request.Mappings)
	if err != nil {
		return Project{}, err
	}

	tracks := make([]Track, len(manifest.Cast))
	trackBySpeaker := make(map[string]int, len(manifest.Cast))
	for index, member := range manifest.Cast {
		identity := mappings[member.ID]
		trackID, err := newTimelineID("track", i.projects.now())
		if err != nil {
			return Project{}, err
		}
		tracks[index] = Track{
			ID: trackID, Name: member.DisplayName, Type: TrackTypeDialogue, Order: index,
			CharacterVoiceID: identity.CharacterVoiceID, ActorVoiceID: identity.ActorVoiceID,
			VoiceFingerprint: identity.Fingerprint, Clips: []TimelineClip{},
		}
		trackBySpeaker[member.ID] = index
	}

	readyTakes := make(map[string][]byte)
	var cursorMS int64
	for lineIndex, line := range manifest.Script {
		trackIndex, exists := trackBySpeaker[line.SpeakerID]
		if !exists {
			return Project{}, ErrInvalid
		}
		gapMS := int64(story.DefaultLineGapMS + line.GapBeforeMS)
		if lineIndex > 0 {
			gapMS += int64(manifest.Script[lineIndex-1].GapAfterMS)
		}
		if gapMS < 0 {
			gapMS = 0
		}
		cursorMS += gapMS

		member := manifest.Cast[trackIndex]
		identity := mappings[member.ID]
		currentTake, hasTake := findStoryTake(line)
		durationMS := importedStaleDialogueDurationMS
		if hasTake && currentTake.DurationMS > 0 {
			durationMS = int64(currentTake.DurationMS)
		}
		clipID, err := newTimelineID("clip", i.projects.now())
		if err != nil {
			return Project{}, err
		}
		clip := TimelineClip{
			ID: clipID, Type: ClipTypeDialogue, Label: importLabel(line.Text), Text: line.Text,
			StartMS: cursorMS, DurationMS: durationMS, Status: DialogueStatusStale,
			CharacterVoiceID: identity.CharacterVoiceID, ActorVoiceID: identity.ActorVoiceID,
			VoiceFingerprint: identity.Fingerprint, SourceStoryID: manifest.ID,
			SourceStoryLineID: line.ID, SourceStoryTakeID: line.CurrentTake,
		}
		if hasTake && currentTake.Text == line.Text && currentTake.VoiceID == member.VoiceID &&
			member.VoiceID != "" && identity.ActorVoiceID == member.VoiceID {
			data, loadErr := i.stories.LoadRetainedTake(manifest.ID, line.ID, currentTake.ID)
			if loadErr == nil && wav.ValidateBytes(data) == nil {
				if duration, durationErr := wav.Duration(data); durationErr == nil && duration.Milliseconds() == durationMS {
					sourceID, mintErr := newTimelineID("take", i.projects.now())
					if mintErr != nil {
						return Project{}, mintErr
					}
					clip.Status = DialogueStatusReady
					clip.SourceID = sourceID
					clip.SourceDurationMS = durationMS
					clip.SourceOutMS = durationMS
					readyTakes[sourceID] = data
				}
			}
		}
		tracks[trackIndex].Clips = append(tracks[trackIndex].Clips, clip)
		cursorMS += durationMS
	}

	timelineDurationMS := int64(DefaultTimelineDurationMS)
	if cursorMS > timelineDurationMS {
		timelineDurationMS = cursorMS
	}
	return i.projects.createProject(importProjectName(manifest), timelineDurationMS, tracks, readyTakes)
}

func (i *StoryImporter) resolveMappings(manifest story.Manifest, requested []StorySpeakerMapping) (map[string]VoiceIdentity, error) {
	if i.projects.resolveCharacterVoice == nil || len(requested) != len(manifest.Cast) {
		return nil, ErrStoryMappingRequired
	}
	cast := make(map[string]struct{}, len(manifest.Cast))
	for _, member := range manifest.Cast {
		cast[member.ID] = struct{}{}
	}
	resolved := make(map[string]VoiceIdentity, len(requested))
	for _, mapping := range requested {
		mapping.SpeakerID = strings.TrimSpace(mapping.SpeakerID)
		mapping.CharacterVoiceID = strings.TrimSpace(mapping.CharacterVoiceID)
		if _, ok := cast[mapping.SpeakerID]; !ok || mapping.CharacterVoiceID == "" {
			return nil, ErrStoryMappingRequired
		}
		if _, duplicate := resolved[mapping.SpeakerID]; duplicate {
			return nil, ErrStoryMappingRequired
		}
		identity, ok, err := i.projects.resolveCharacterVoice(mapping.CharacterVoiceID)
		if err != nil {
			return nil, err
		}
		if !ok || identity.CharacterVoiceID != mapping.CharacterVoiceID {
			return nil, ErrCharacterVoiceNotFound
		}
		resolved[mapping.SpeakerID] = identity
	}
	return resolved, nil
}

func findStoryTake(line story.ScriptLine) (story.Take, bool) {
	if line.CurrentTake == "" {
		return story.Take{}, false
	}
	for _, take := range line.Takes {
		if take.ID == line.CurrentTake {
			return take, true
		}
	}
	return story.Take{}, false
}

func importProjectName(manifest story.Manifest) string {
	name := strings.TrimSpace(manifest.Title)
	if name == "" {
		name = strings.TrimSpace(manifest.Subject)
	}
	if name == "" {
		name = "Imported Story"
	}
	return truncateRunes(name, MaxNameLength)
}

func importLabel(text string) string {
	label := strings.TrimSpace(text)
	if label == "" {
		label = "Dialogue"
	}
	return truncateRunes(label, MaxNameLength)
}

func truncateRunes(value string, maximum int) string {
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maximum]))
}
