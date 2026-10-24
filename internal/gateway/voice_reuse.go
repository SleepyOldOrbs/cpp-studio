package gateway

import (
	"cpp-studio/internal/audiobook"
	"cpp-studio/internal/engine"
	"cpp-studio/internal/voice"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type voiceReuseChoice struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	DisabledReason string `json:"disabledReason,omitempty"`
}
type voiceReuseResponse struct {
	VoiceID     string                       `json:"voiceId"`
	ActorID     string                       `json:"actorId"`
	CharacterID string                       `json:"characterId,omitempty"`
	Name        string                       `json:"name"`
	Direction   string                       `json:"direction,omitempty"`
	Analysis    *voice.ReferenceAnalysis     `json:"analysis,omitempty"`
	AudioURL    string                       `json:"audioUrl"`
	PromptSpec  audiobook.DramaBoxPromptSpec `json:"promptSpec,omitempty"`
	Characters  []voiceReuseChoice           `json:"characters,omitempty"`
}

func (r *router) loadReuseVoice(id string) (voice.Clone, *voice.CharacterVoice, error) {
	var character *voice.CharacterVoice
	actorID := id
	if strings.HasPrefix(id, "character_") {
		child, ok, err := r.voices.LoadCharacterVoice(id)
		if err != nil || !ok {
			return voice.Clone{}, nil, fmt.Errorf("Character Voice unavailable; choose an existing voice")
		}
		character = &child
		actorID = child.ActorVoiceID
	}
	actor, ok, err := r.voices.Load(actorID)
	if err != nil || !ok {
		return voice.Clone{}, nil, fmt.Errorf("Actor Voice unavailable; choose an existing voice")
	}
	return actor, character, nil
}

func (r *router) voiceReuseStatus(id, target string) string {
	actor, character, err := r.loadReuseVoice(id)
	if err != nil {
		return err.Error()
	}
	if _, _, err = r.voices.ResolveReference(id); err != nil {
		return "Reference unavailable; reassign a recording in Voice cloning"
	}
	switch target {
	case "audiobook":
		if character != nil && character.Direction != "" {
			if _, ok := r.cfg.Engines[audiobook.DramaBoxEngineID]; !ok {
				return "Configure the expressive audiobook engine to use this Character Voice's direction"
			}
			analysis := actor.Analysis
			if character.Reference != nil {
				analysis = character.Reference.Analysis
			}
			if analysis == nil || analysis.Fitness == "unsupported" || analysis.UsableSpeechSeconds < 10 {
				return "Expressive narration needs at least 10 seconds of usable speech; reassign a longer reference"
			}
			if _, err := audiobook.NormalizeRequest(audiobook.Request{EngineID: audiobook.DramaBoxEngineID, Direction: character.Direction}); err != nil {
				return err.Error()
			}
		} else {
			if _, a := r.cfg.Engines[audiobook.DefaultEngineID]; !a {
				if _, b := r.cfg.Engines[audiobook.DramaBoxEngineID]; !b {
					return "Configure an audiobook engine in Engines"
				}
			}
		}
	case "story-builder":
		if character == nil {
			children, err := r.voices.ListCharacterVoices(actor.ID)
			if err != nil || len(children) == 0 {
				return "Create a Character Voice under this actor before assigning dialogue"
			}
			for _, child := range children {
				if r.voiceReuseStatus(child.ID, target) == "" {
					return ""
				}
			}
			return "No compatible Character Voice; configure a dialogue engine or edit its direction"
		}
		for _, id := range []string{"omnivoice", "fish-drama3"} {
			if _, ok := r.cfg.Engines[id]; ok && engine.ValidateSpeechDirection(id, character.Direction) == nil {
				return ""
			}
		}
		return "Configure a compatible dialogue engine or edit this character's direction in Voice cloning"
	default:
		return "Unknown voice reuse target"
	}
	return ""
}

// The receiving tool revalidates the pending ID and its selected engine. This
// endpoint reads owner state; assignment remains an explicit tool action.
func (r *router) handleVoiceReuse(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	fail := func(err error) { writeJSONError(w, http.StatusBadRequest, err.Error()) }
	id, target, engineID := req.URL.Query().Get("voice"), req.URL.Query().Get("target"), req.URL.Query().Get("engine")
	actor, character, err := r.loadReuseVoice(id)
	if err != nil {
		fail(err)
		return
	}
	if _, _, err = r.voices.ResolveReference(id); err != nil {
		fail(fmt.Errorf("Reference unavailable; reassign a recording in Voice cloning"))
		return
	}
	result := voiceReuseResponse{VoiceID: id, ActorID: actor.ID, Name: actor.Name, Analysis: actor.Analysis, AudioURL: "/v1/voices/" + actor.ID + "/audio"}
	if character != nil {
		result.CharacterID = character.ID
		result.Name = actor.Name + " / " + character.Name
		result.Direction = character.Direction
		result.AudioURL = "/v1/character-voices/" + id + "/reference"
		if character.Reference != nil {
			result.Analysis = character.Reference.Analysis
		}
	}
	switch target {
	case "audiobook":
		if _, ok := r.cfg.Engines[engineID]; !ok {
			fail(fmt.Errorf("Selected audiobook engine is unavailable; choose an engine in Audiobook Builder"))
			return
		}
		if character != nil && character.Direction != "" && engineID != audiobook.DramaBoxEngineID {
			fail(fmt.Errorf("This Character Voice's direction needs the expressive narrator; choose it in Audiobook Builder, then retry"))
			return
		}
		resolved, err := r.audiobooks.Preview(req.Context(), audiobook.Request{EngineID: engineID, VoiceID: id, Direction: result.Direction})
		if err != nil {
			fail(err)
			return
		}
		result.PromptSpec = resolved.Request.PromptSpec
	case "story-builder":
		if engineID != "omnivoice" && engineID != "fish-drama3" {
			fail(fmt.Errorf("Choose a supported dialogue engine in Story Builder"))
			return
		}
		if _, ok := r.cfg.Engines[engineID]; !ok {
			fail(fmt.Errorf("Selected dialogue engine is unavailable; configure it in Engines"))
			return
		}
		if character == nil {
			children, err := r.voices.ListCharacterVoices(actor.ID)
			if err != nil {
				fail(err)
				return
			}
			for _, child := range children {
				reason := ""
				if err := engine.ValidateSpeechDirection(engineID, child.Direction); err != nil {
					reason = err.Error()
				}
				if _, _, err := r.voices.ResolveReference(child.ID); err != nil {
					reason = "Reference unavailable; reassign a recording"
				}
				result.Characters = append(result.Characters, voiceReuseChoice{child.ID, child.Name, reason})
			}
			if len(result.Characters) == 0 {
				fail(fmt.Errorf("Create a Character Voice under this actor before assigning dialogue"))
				return
			}
		} else if err := engine.ValidateSpeechDirection(engineID, character.Direction); err != nil {
			fail(err)
			return
		}
	default:
		fail(fmt.Errorf("Unknown voice reuse target"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
