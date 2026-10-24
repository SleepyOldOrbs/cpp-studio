package gateway

import (
	"cpp-studio/internal/pronunciation"
	"cpp-studio/internal/storybuilder"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPronunciationHTTPStoreAndPreview(t *testing.T) {
	r := &router{pronunciation: pronunciation.NewStore(filepath.Join(t.TempDir(), "rules.json"))}
	for _, tc := range []struct {
		method, path, body, want string
		code                     int
	}{
		{"PUT", "/v1/pronunciation", `{"version":1,"rules":[{"term":"James","replacement":"Jaymz","language":"en"}]}`, `Jaymz`, 200},
		{"GET", "/v1/pronunciation", "", `James`, 200},
		{"POST", "/v1/pronunciation/preview", `{"source":"é James","language":"en"}`, `"spoken":"é Jaymz"`, 200},
		{"POST", "/v1/pronunciation/import", `{"version":1,"rules":[]}`, `choose`, 400},
		{"POST", "/v1/pronunciation/import?mode=replace", `{`, `invalid`, 400},
		{"GET", "/v1/pronunciation", "", `James`, 200},
	} {
		w := httptest.NewRecorder()
		r.handlePronunciation(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	_ = http.MethodGet
}

func TestStoryBuilderHTTPPronunciationSnapshotUpdate(t *testing.T) {
	s := storybuilder.NewStore(t.TempDir())
	p, err := s.Create("Names")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := pronunciation.Freeze([]pronunciation.Rule{{Term: "James", Replacement: "Jaymz", Language: "en"}})
	data, _ := json.Marshal(map[string]any{"name": p.Name, "revision": p.Revision, "tracks": p.Tracks, "apply_pronunciation": true, "pronunciation": snapshot, "language": "en"})
	r := &router{storyBuilderProjects: s}
	w := httptest.NewRecorder()
	r.handleStoryBuilderProject(w, httptest.NewRequest("PUT", "/v1/story-builder-projects/"+p.ID, strings.NewReader(string(data))))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Jaymz") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPronunciationPreviewAcceptsSupportedProjectClipCount(t *testing.T) {
	r := &router{pronunciation: pronunciation.NewStore(filepath.Join(t.TempDir(), "rules.json"))}
	sources := make([]string, storybuilder.MaxClips)
	for i := range sources {
		sources[i] = "James."
	}
	data, _ := json.Marshal(map[string]any{"sources": sources, "language": "en", "rules": []pronunciation.Rule{{Term: "James", Replacement: "Jaymz"}}})
	w := httptest.NewRecorder()
	r.handlePronunciation(w, httptest.NewRequest("POST", "/v1/pronunciation/preview", strings.NewReader(string(data))))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var output struct {
		Lines []pronunciation.Resolution `json:"lines"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &output); err != nil || len(output.Lines) != storybuilder.MaxClips {
		t.Fatal(len(output.Lines), err)
	}
}
