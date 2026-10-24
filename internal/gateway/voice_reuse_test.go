package gateway

import (
	"context"
	"cpp-studio/internal/audiobook"
	"cpp-studio/internal/config"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/voice"
	"cpp-studio/internal/wav"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVoiceReuseValidatesTargetEngineAndCharacterOwnedReference(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"audio": {Command: "audio", Mode: "subprocess"}, "dramabox": {Command: "drama", Mode: "subprocess"}, "omnivoice": {Command: "omni", Mode: "subprocess"}})
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	r.voices = voice.NewStore(filepath.Join(t.TempDir(), "voices"))
	actor, err := r.voices.Save("Reader", "Reference words.", wav.SyntheticTone(192000), false)
	if err != nil {
		t.Fatal(err)
	}
	character, err := r.voices.CreateCharacterVoice(actor.ID, "Quiet reader", "whisper")
	if err != nil {
		t.Fatal(err)
	}
	own := wav.SyntheticTone(200000)
	if err = r.voices.AssignReference(character.ID, "Character reference words.", own, nil); err != nil {
		t.Fatal(err)
	}
	identity, err := r.resolveAudiobookVoice(context.Background(), character.ID)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(own)
	if identity.ID != character.ID || identity.ReferenceSHA256 != hex.EncodeToString(sum[:]) || identity.Reference.RefText != "Character reference words." {
		t.Fatal(identity)
	}
	if _, err := r.audiobooks.Preview(context.Background(), audiobook.Request{EngineID: "audio", VoiceID: character.ID}); err == nil {
		t.Fatal("changing narrator silently dropped Character Voice direction")
	}
	refResponse := httptest.NewRecorder()
	r.handleReferenceAssignment(refResponse, httptest.NewRequest("GET", "/v1/character-voices/"+character.ID+"/reference", nil), character.ID)
	if refResponse.Code != 200 || sha256.Sum256(refResponse.Body.Bytes()) != sum {
		t.Fatal("character reference audition used wrong recording", refResponse.Code)
	}
	for _, tc := range []struct {
		id, target, engine, want string
		code                     int
	}{
		{actor.ID, "audiobook", "audio", actor.ID, 200},
		{character.ID, "audiobook", "audio", "direction", 400},
		{character.ID, "audiobook", "dramabox", "whisper", 200},
		{character.ID, "story-builder", "omnivoice", character.ID, 200},
		{actor.ID, "story-builder", "omnivoice", character.ID, 200},
		{character.ID, "story-builder", "missing", "engine", 400},
		{"voice_missing", "audiobook", "audio", "unavailable", 400},
	} {
		w := httptest.NewRecorder()
		r.handleVoiceReuse(w, httptest.NewRequest("GET", "/v1/voices/reuse?voice="+url.QueryEscape(tc.id)+"&target="+tc.target+"&engine="+tc.engine, nil))
		if w.Code != tc.code || !strings.Contains(strings.ToLower(w.Body.String()), strings.ToLower(tc.want)) {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
	path, _, _ := r.voices.ResolveReference(character.ID)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if r.voiceReuseStatus(character.ID, "audiobook") == "" {
		t.Fatal("missing reference reuse enabled")
	}
	w := httptest.NewRecorder()
	r.handleVoiceReuse(w, httptest.NewRequest("GET", "/v1/voices/reuse?voice="+character.ID+"&target=audiobook&engine=dramabox", nil))
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}
