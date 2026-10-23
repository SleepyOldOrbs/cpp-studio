package gateway

import (
	"bytes"
	"cpp-studio/internal/engine"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"cpp-studio/internal/config"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/voice"
	"cpp-studio/internal/wav"
)

func TestAssignReferenceRoutesAndCharacterPlayback(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{})
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	r.voices = voice.NewStore(t.TempDir())
	audio := wav.SyntheticTone(1600)
	actor, err := r.voices.Save("Actor", "original", audio, false)
	if err != nil {
		t.Fatal(err)
	}
	character, err := r.voices.CreateCharacterVoice(actor.ID, "Character", "quiet")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ prefix, id string }{{"/v1/voices/", actor.ID}, {"/v1/character-voices/", character.ID}} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		file, _ := form.CreateFormFile("file", "reference.wav")
		file.Write(audio)
		form.WriteField("transcript", target.id)
		form.Close()
		req := httptest.NewRequest(http.MethodPut, target.prefix+target.id+"/reference", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("assignment %d: %s", rec.Code, rec.Body.String())
		}
		resolved, err := r.resolveVoice(target.id)
		if err != nil || resolved.RefText != target.id {
			t.Fatalf("wrong speech reference: %+v %v", resolved, err)
		}
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target.prefix+target.id+"/reference", nil))
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), audio) {
			t.Fatal("assigned playback mismatch")
		}
	}
	actorRef, _ := r.resolveVoice(actor.ID)
	if actorRef.RefText != actor.ID {
		t.Fatal("character assignment changed actor")
	}
}

func TestCreateClonedCharacterRouteUsesPreparedTranscript(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{})
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	r.voices = voice.NewStore(t.TempDir())
	actor, err := r.voices.Save("Actor", "original", wav.SyntheticTone(1600), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, transcript := range []string{"prepared words", ""} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		file, _ := form.CreateFormFile("file", "reference.wav")
		audio := wav.SyntheticTone(3200)
		file.Write(audio)
		form.WriteField("name", "Child")
		form.WriteField("transcript", transcript)
		form.Close()
		req := httptest.NewRequest(http.MethodPost, "/v1/voices/"+actor.ID+"/characters", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if transcript == "" {
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("missing transcript: %d %s", rec.Code, rec.Body.String())
			}
			continue
		}
		if rec.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
		}
		var created voice.CharacterVoice
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		resolved, err := r.resolveVoice(created.ID)
		if err != nil || resolved.RefText != transcript {
			t.Fatalf("reference: %+v %v", resolved, err)
		}
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/character-voices/"+created.ID+"/reference", nil))
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), audio) {
			t.Fatal("wrong child recording")
		}
	}
	children, _ := r.voices.ListCharacterVoices(actor.ID)
	if len(children) != 1 {
		t.Fatal("invalid request created a child")
	}
	resolved, _ := r.resolveVoice(actor.ID)
	if resolved.RefText != "original" {
		t.Fatal("parent changed")
	}
}

func TestPreparedReferenceSpeechDoesNotPublishVoiceAndCleansUp(t *testing.T) {
	for _, scenario := range []string{"success", "engine failure", "invalid WAV", "empty transcript", "mixed identity"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := testConfig(map[string]config.EngineConfig{"audio": {Command: "audio-cli"}})
			r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
			r.voices = voice.NewStore(t.TempDir())
			actor, err := r.voices.Save("Original", "original words", wav.SyntheticTone(1600), false)
			if err != nil {
				t.Fatal(err)
			}
			originalPath, _, _ := r.voices.ResolveReference(actor.ID)
			audio := wav.SyntheticTone(3200)
			stagedPath := ""
			fake := engine.NewFake()
			fake.Handle("audio", func(spec engine.Spec) (engine.Result, error) {
				stagedPath = spec.OverrideArgs["--voice-ref"]
				data, err := os.ReadFile(stagedPath)
				if err != nil || !bytes.Equal(data, audio) || spec.OverrideArgs["--reference-text"] != "fresh transcript" {
					t.Fatalf("wrong preview reference: %v %v", spec.OverrideArgs, err)
				}
				if stagedPath == originalPath {
					t.Fatal("audition overwrote saved reference")
				}
				if scenario == "engine failure" {
					return engine.Result{}, errors.New("engine failed")
				}
				return engine.Result{Output: audio}, nil
			})
			r.engines = fake
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			file, _ := form.CreateFormFile("file", "reference.wav")
			if scenario == "invalid WAV" {
				file.Write([]byte("not audio"))
			} else {
				file.Write(audio)
			}
			form.WriteField("input", "new preview words")
			form.WriteField("model", "audio")
			if scenario != "empty transcript" {
				form.WriteField("transcript", "fresh transcript")
			}
			if scenario == "mixed identity" {
				form.WriteField("voice", actor.ID)
			}
			form.Close()
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", &body)
			req.Header.Set("Content-Type", form.FormDataContentType())
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if scenario == "success" {
				if rec.Code != http.StatusOK {
					t.Fatalf("speech: %d %s", rec.Code, rec.Body.String())
				}
				if err := wav.ValidateBytes(rec.Body.Bytes()); err != nil {
					t.Fatal(err)
				}
			} else if rec.Code < 400 {
				t.Fatalf("expected failure: %d", rec.Code)
			}
			if stagedPath != "" {
				if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
					t.Fatalf("temporary reference retained: %v", err)
				}
			}
			path, transcript, err := r.voices.ResolveReference(actor.ID)
			if err != nil || path != originalPath || transcript != "original words" {
				t.Fatal("saved voice changed during preview")
			}
			voices, err := r.voices.List()
			if err != nil || len(voices) != 1 {
				t.Fatalf("preview created a voice: %v %v", voices, err)
			}
		})
	}
}
