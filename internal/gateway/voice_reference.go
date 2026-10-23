package gateway

import (
	"encoding/json"
	"net/http"
	"strings"

	"cpp-studio/internal/voice"
)

func (r *router) handleClonedCharacterCreate(w http.ResponseWriter, req *http.Request, actorID string) {
	req.Body = http.MaxBytesReader(w, req.Body, voice.MaxReferenceWAVBytes)
	data, ok := readUploadedWAV(w, req)
	if !ok {
		return
	}
	// The cloning screen prepares the transcript before choosing a destination.
	character, err := r.voices.CreateClonedCharacterVoice(actorID, req.FormValue("name"), req.FormValue("transcript"), data, parseCloneSource(req))
	if err != nil {
		writeCharacterVoiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(characterVoiceSummaryOf(character))
}

func (r *router) handleReferenceAssignment(w http.ResponseWriter, req *http.Request, id string) {
	if req.Method != http.MethodPut && req.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, PUT")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path, _, err := r.voices.ResolveReference(id)
	if err != nil {
		writeCharacterVoiceError(w, err)
		return
	}
	if req.Method == http.MethodGet {
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeFile(w, req, path)
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, voice.MaxReferenceWAVBytes)
	data, ok := readUploadedWAV(w, req)
	if !ok {
		return
	}
	transcript := strings.TrimSpace(req.FormValue("transcript"))
	if transcript == "" {
		transcript, err = r.transcribe(req.Context(), data)
		if err != nil {
			writeEngineError(w, err)
			return
		}
	}
	if err := r.voices.AssignReference(id, transcript, data, parseCloneSource(req)); err != nil {
		writeCharacterVoiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"assigned": id})
}
