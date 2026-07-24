package demo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndex(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("expected HTML content type, got %q", contentType)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "cpp-studio local studio") {
		t.Fatalf("expected index HTML marker, got %q", body)
	}
	if !strings.Contains(body, "/v1/images/generations") {
		t.Fatalf("expected image route marker, got %q", body)
	}
	if !strings.Contains(body, "/v1/stories") {
		t.Fatalf("expected story route marker, got %q", body)
	}
	if !strings.Contains(body, "imageErrorBox") {
		t.Fatalf("expected image error marker, got %q", body)
	}
	if !strings.Contains(body, "storyLibraryButton") {
		t.Fatalf("expected story library marker, got %q", body)
	}
	if !strings.Contains(body, "/v1/voices") {
		t.Fatalf("expected voice clone route marker, got %q", body)
	}
	if !strings.Contains(body, "voiceLibrary") {
		t.Fatalf("expected voice library marker, got %q", body)
	}
	if !strings.Contains(body, "voiceSelect") {
		t.Fatalf("expected voice select marker, got %q", body)
	}
	if !strings.Contains(body, "cloneSpeakForm") {
		t.Fatalf("expected speak form marker, got %q", body)
	}
	if !strings.Contains(body, "clearAllButton") {
		t.Fatalf("expected clear all marker, got %q", body)
	}
	if !strings.Contains(body, "wavSaveButton") {
		t.Fatalf("expected recording save marker, got %q", body)
	}
	if !strings.Contains(body, "describeImageButton") {
		t.Fatalf("expected image description marker, got %q", body)
	}
	if !strings.Contains(body, "/v1/images/descriptions") {
		t.Fatalf("expected image description route marker, got %q", body)
	}
	if !strings.Contains(body, "designForm") {
		t.Fatalf("expected voice designer marker, got %q", body)
	}
	if !strings.Contains(body, "/v1/voices/design") {
		t.Fatalf("expected voice design route marker, got %q", body)
	}
	if !strings.Contains(body, "designModelSelect") {
		t.Fatalf("expected design model selector marker, got %q", body)
	}
	if !strings.Contains(body, "designEngineInput") {
		t.Fatalf("expected design engine input marker, got %q", body)
	}
	if !strings.Contains(body, "castList") {
		t.Fatalf("expected story cast editor marker, got %q", body)
	}
	if !strings.Contains(body, "storyDraftButton") {
		t.Fatalf("expected story draft marker, got %q", body)
	}
	if !strings.Contains(body, "scriptEditor") {
		t.Fatalf("expected script editor marker, got %q", body)
	}
	if !strings.Contains(body, "tabBar") {
		t.Fatalf("expected tab bar marker, got %q", body)
	}
	if !strings.Contains(body, "/v1/models/catalog") {
		t.Fatalf("expected models catalog route marker, got %q", body)
	}
	if !strings.Contains(body, "modelsList") {
		t.Fatalf("expected models list marker, got %q", body)
	}
	if !strings.Contains(body, "modelsVerifyButton") {
		t.Fatalf("expected verify-all marker, got %q", body)
	}
	if !strings.Contains(body, "logToggleButton") {
		t.Fatalf("expected log drawer marker, got %q", body)
	}
	if !strings.Contains(body, "profilesRow") {
		t.Fatalf("expected VRAM profiles marker, got %q", body)
	}
	if !strings.Contains(body, "enginesErrorBox") {
		t.Fatalf("expected engines error marker, got %q", body)
	}
	if !strings.Contains(body, "jobsList") {
		t.Fatalf("expected jobs list marker, got %q", body)
	}
	if !strings.Contains(body, "libraryList") {
		t.Fatalf("expected library list marker, got %q", body)
	}
	if !strings.Contains(body, "libraryImageButton") {
		t.Fatalf("expected save-to-library marker, got %q", body)
	}
	if !strings.Contains(body, "/v1/audiobooks") {
		t.Fatalf("expected audiobook route marker, got %q", body)
	}
	if !strings.Contains(body, "audiobookShelf") {
		t.Fatalf("expected audiobook shelf marker, got %q", body)
	}
	if !strings.Contains(body, "extractCanvas") {
		t.Fatalf("expected extractor waveform marker, got %q", body)
	}
	if !strings.Contains(body, "extractTimeline") {
		t.Fatalf("expected extractor timeline marker, got %q", body)
	}
	if !strings.Contains(body, "format=segments") {
		t.Fatalf("expected segments route marker, got %q", body)
	}
	if !strings.Contains(body, "extractDiarizeButton") {
		t.Fatalf("expected diarization button marker, got %q", body)
	}
	if !strings.Contains(body, "extractCastButton") {
		t.Fatalf("expected clone-the-cast marker, got %q", body)
	}
}

func TestHandlerServesAssets(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		contentType string
		needle      string
	}{
		{
			name:        "javascript",
			path:        "/app.js",
			contentType: "javascript",
			needle:      "refreshStoryLibrary",
		},
		{
			name:        "javascript voice clone",
			path:        "/app.js",
			contentType: "javascript",
			needle:      "refreshVoices",
		},
		{
			name:        "css",
			path:        "/styles.css",
			contentType: "text/css",
			needle:      ".story-library-item",
		},
		{
			name:        "css voice clone",
			path:        "/styles.css",
			contentType: "text/css",
			needle:      ".voice-item",
		},
		{
			name:        "javascript models catalog",
			path:        "/app.js",
			contentType: "javascript",
			needle:      "refreshModels",
		},
		{
			name:        "css tab bar",
			path:        "/styles.css",
			contentType: "text/css",
			needle:      ".tab-bar",
		},
		{
			name:        "javascript engine controls",
			path:        "/app.js",
			contentType: "javascript",
			needle:      "controlEngine",
		},
		{
			name:        "css profiles row",
			path:        "/styles.css",
			contentType: "text/css",
			needle:      ".profiles-row",
		},
		{
			name:        "javascript library",
			path:        "/app.js",
			contentType: "javascript",
			needle:      "refreshLibrary",
		},
		{
			name:        "css library",
			path:        "/styles.css",
			contentType: "text/css",
			needle:      ".library-item",
		},
		{
			name:        "javascript audiobook",
			path:        "/app.js",
			contentType: "javascript",
			needle:      "refreshAudiobooks",
		},
		{
			name:        "javascript extractor",
			path:        "/app.js",
			contentType: "javascript",
			needle:      "drawExtractWave",
		},
		{
			name:        "css extractor",
			path:        "/styles.css",
			contentType: "text/css",
			needle:      ".extract-segment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)

			Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, tt.contentType) {
				t.Fatalf("expected content type containing %q, got %q", tt.contentType, contentType)
			}
			if body := rec.Body.String(); !strings.Contains(body, tt.needle) {
				t.Fatalf("expected asset marker %q, got %q", tt.needle, body)
			}
		})
	}
}
