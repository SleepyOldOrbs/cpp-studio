package engine

import (
	"encoding/json"
	"testing"
)

func TestDrama3CharacterDirectionAndReferenceMappings(t *testing.T) {
	request := SynthesisRequest{EngineID: "fish-drama3", Text: "[sighs] The café is closed.", Direction: "quiet [whispering]"}
	voice := &Voice{RefWAVPath: "actor.wav", RefText: "Reference café."}
	want := "[quiet (whispering)] [sighs] The café is closed."
	spec := SpeechVoiceSpecForRequest(request, voice)
	if got := spec.BuildArgs("", "take.wav")[1]; got != want {
		t.Fatalf("text = %q", got)
	}
	if spec.OverrideArgs["--voice-ref"] != "actor.wav" || spec.OverrideArgs["--reference-text"] != voice.RefText {
		t.Fatal(spec.OverrideArgs)
	}
	body, err := MarshalSpeechServerRequest("tts", request, voice, nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload serverSpeechRequest
	if err = json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Input != want || payload.VoiceRef != "actor.wav" || payload.ReferenceText != voice.RefText {
		t.Fatal(payload)
	}
	request.References = []Voice{*voice}
	spec = SpeechVoiceSpecForRequest(request, nil)
	if spec.BuildArgs("", "take.wav")[1] != want || len(spec.OverrideArgs) != 0 {
		t.Fatal("multi-reference lost direction")
	}
}
