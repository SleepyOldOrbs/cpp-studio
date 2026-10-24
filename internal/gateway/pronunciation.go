package gateway

import (
	"cpp-studio/internal/pronunciation"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (r *router) handlePronunciation(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.pronunciation == nil {
		http.Error(w, "pronunciation store unavailable", 503)
		return
	}
	var result any
	var err error
	switch {
	case req.URL.Path == "/v1/pronunciation" && req.Method == http.MethodGet:
		result, err = r.pronunciation.Load()
	case req.URL.Path == "/v1/pronunciation" && req.Method == http.MethodPut:
		result, err = r.pronunciation.Import(req.Body, "replace")
	case req.URL.Path == "/v1/pronunciation/import" && req.Method == http.MethodPost:
		result, err = r.pronunciation.Import(req.Body, req.URL.Query().Get("mode"))
	case req.URL.Path == "/v1/pronunciation/preview" && req.Method == http.MethodPost:
		var input struct {
			Sources  []string              `json:"sources"`
			Source   string                `json:"source"`
			Language string                `json:"language"`
			Rules    *[]pronunciation.Rule `json:"rules"`
		}
		data, readErr := io.ReadAll(io.LimitReader(req.Body, pronunciation.MaxDocumentBytes+1))
		if readErr != nil || len(data) > pronunciation.MaxDocumentBytes {
			err = fmt.Errorf("preview is too large")
			break
		}
		if err = json.Unmarshal(data, &input); err != nil {
			break
		}
		var rules []pronunciation.Rule
		if input.Rules != nil {
			rules = *input.Rules
		} else {
			var doc pronunciation.Document
			doc, err = r.pronunciation.Load()
			rules = doc.Rules
		}
		if err != nil {
			break
		}
		if strings.TrimSpace(input.Language) == "" {
			err = fmt.Errorf("choose an explicit language code for preview")
			break
		}
		resolution, resolveErr := pronunciation.ResolveDetailed(input.Source, input.Language, rules)
		err = resolveErr
		snapshot, freezeErr := pronunciation.Freeze(rules)
		if err == nil {
			err = freezeErr
		}
		result = struct {
			pronunciation.Resolution
			Snapshot pronunciation.Snapshot     `json:"snapshot"`
			Lines    []pronunciation.Resolution `json:"lines,omitempty"`
		}{Resolution: resolution, Snapshot: snapshot}
		if input.Sources != nil {
			if len(input.Sources) > 1000 {
				err = fmt.Errorf("preview at most 1000 dialogue lines at a time")
				break
			}
			lines := make([]pronunciation.Resolution, 0, len(input.Sources))
			for _, source := range input.Sources {
				line, lineErr := pronunciation.ResolveDetailed(source, input.Language, rules)
				if lineErr != nil {
					err = lineErr
					break
				}
				lines = append(lines, line)
			}
			result = struct {
				Snapshot pronunciation.Snapshot     `json:"snapshot"`
				Lines    []pronunciation.Resolution `json:"lines"`
			}{snapshot, lines}
		}
	default:
		http.Error(w, "method or pronunciation route not found", 404)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func decodePronunciationSnapshot(value string) (*pronunciation.Snapshot, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	if len(value) > pronunciation.MaxDocumentBytes {
		return nil, fmt.Errorf("pronunciation snapshot is too large")
	}
	var snapshot pronunciation.Snapshot
	if err := json.Unmarshal([]byte(value), &snapshot); err != nil {
		return nil, fmt.Errorf("invalid pronunciation snapshot: %w", err)
	}
	return pronunciation.ValidateSnapshot(&snapshot)
}
