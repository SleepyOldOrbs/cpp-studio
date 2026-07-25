package story

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cpp-studio/internal/wav"
)

// The take room. A produced story used to be one stitched WAV: if one line
// read badly the only move was to regenerate the episode. Here every line
// keeps its own recordings, the manifest says which one is current, and a
// render is an immutable revision stitched from those choices.

// LinePatch is an edit to one line's production settings. Nil fields are
// left alone, so a caller can mute a line without restating its timing.
type LinePatch struct {
	// Text rewrites what the line says. Changing it deselects the current
	// take — the recording no longer matches the words — so the line must
	// be retaken or muted before the story can render again.
	Text        *string `json:"text,omitempty"`
	CurrentTake *string `json:"current_take,omitempty"`
	Muted       *bool   `json:"muted,omitempty"`
	GapBeforeMS *int    `json:"gap_before_ms,omitempty"`
	GapAfterMS  *int    `json:"gap_after_ms,omitempty"`
}

// MaxGapMS bounds per-line timing nudges. Half a second of tightening is
// past what any inter-line gap can absorb, and three seconds of silence is
// already a dramatic pause.
const (
	MinGapMS = -500
	MaxGapMS = 3000
)

// AssignLineIDs gives every script line a stable id, keeping any the caller
// already supplied. Ids are what takes hang off, so they must survive the
// text being edited.
func AssignLineIDs(script []ScriptLine) []ScriptLine {
	used := make(map[string]bool, len(script))
	for _, line := range script {
		if line.ID != "" {
			used[line.ID] = true
		}
	}
	next := 1
	for i := range script {
		if script[i].ID != "" {
			continue
		}
		for {
			candidate := fmt.Sprintf("line-%03d", next)
			next++
			if !used[candidate] {
				script[i].ID = candidate
				used[candidate] = true
				break
			}
		}
	}
	return script
}

// stitchTakes joins per-line clips into one WAV, honouring each line's mute
// and timing. Muted lines contribute nothing at all — not even their gap.
func stitchTakes(clips [][]byte, script []ScriptLine) ([]byte, error) {
	if len(clips) != len(script) {
		return nil, fmt.Errorf("have %d clips for %d lines", len(clips), len(script))
	}
	var (
		kept []([]byte)
		gaps []time.Duration
		// previous is the last line actually kept, which is not always the
		// line before this one: a muted line contributes nothing to the
		// render, so its trailing nudge must not survive it either.
		previous *ScriptLine
	)
	for i, line := range script {
		if line.Muted || len(clips[i]) == 0 {
			continue
		}
		gap := lineGap + time.Duration(line.GapBeforeMS)*time.Millisecond
		if previous != nil {
			// The kept predecessor's trailing nudge and this line's leading
			// nudge both apply to the single silence between them.
			gap += time.Duration(previous.GapAfterMS) * time.Millisecond
		}
		if gap < 0 {
			gap = 0
		}
		kept = append(kept, clips[i])
		gaps = append(gaps, gap)
		previous = &script[i]
	}
	if len(kept) == 0 {
		return nil, NewError(CodeNothingToRender, "every line is muted or unrecorded")
	}
	return wav.ConcatenateGaps(kept, gaps)
}

// retainFirstRender persists the takes behind a freshly produced story and
// archives its stitch as revision 1, then republishes the manifest naming
// them. The story is already on disk and playable when this runs, so a
// failure here costs the take room, not the episode.
func (m *Manager) retainFirstRender(manifest *Manifest, clips [][]byte, audio []byte, createdAt time.Time) error {
	if len(clips) == len(manifest.Script) {
		for i := range manifest.Script {
			if len(clips[i]) == 0 {
				continue
			}
			voiceID := castVoiceFor(manifest.Cast, manifest.Script[i].SpeakerID)
			take, err := m.storeTake(manifest.ID, manifest.Script[i].ID, "take-001", clips[i], voiceID, manifest.Script[i].Text, createdAt)
			if err != nil {
				return err
			}
			manifest.Script[i].Takes = []Take{take}
			manifest.Script[i].CurrentTake = take.ID
		}
	}
	if err := m.store.SaveRender(manifest.ID, 1, audio); err != nil {
		return err
	}
	manifest.Renders = []Render{{
		Revision:        1,
		CreatedAt:       createdAt.UTC(),
		DurationSeconds: manifest.DurationSeconds,
		Bytes:           len(audio),
		URL:             RenderURL(manifest.ID, 1),
	}}
	return m.store.SaveManifest(*manifest)
}

// storeTake writes one take and describes it. The voice and the exact text
// are recorded on the take rather than looked up later, so a take stays
// explainable after the line is edited or the cast is re-voiced.
func (m *Manager) storeTake(storyID string, lineID string, takeID string, audio []byte, voiceID string, text string, createdAt time.Time) (Take, error) {
	url, err := m.store.SaveTake(storyID, lineID, takeID, audio)
	if err != nil {
		return Take{}, err
	}
	durationMS := 0
	if d, err := wav.Duration(audio); err == nil {
		durationMS = int(d / time.Millisecond)
	}
	return Take{
		ID:         takeID,
		VoiceID:    voiceID,
		Text:       text,
		CreatedAt:  createdAt.UTC(),
		DurationMS: durationMS,
		Bytes:      len(audio),
		URL:        url,
	}, nil
}

// Retake resynthesizes one line of a stored story and makes the new take
// current. The story must already be produced; the audio engine is reserved
// for the duration exactly as a full production would.
func (m *Manager) Retake(ctx context.Context, storyID string, lineID string) (Manifest, Take, error) {
	if m.synthesize == nil {
		return Manifest{}, Take{}, NewError(CodeSynthesisFailure, "no audio engine is configured for synthesis")
	}
	// Held across the whole load-synthesize-save sequence: two concurrent
	// retakes would otherwise both mint take-002 over each other.
	lock := m.editLock(storyID)
	lock.Lock()
	defer lock.Unlock()

	manifest, ok, err := m.store.Load(storyID)
	if err != nil {
		return Manifest{}, Take{}, NewError(CodeStoreFailure, err.Error())
	}
	if !ok {
		return Manifest{}, Take{}, NewError(CodeNotFound, "story not found")
	}
	index := lineIndex(manifest.Script, lineID)
	if index < 0 {
		return Manifest{}, Take{}, NewError(CodeLineNotFound, "line not found in this story")
	}
	line := manifest.Script[index]
	voiceID := castVoiceFor(manifest.Cast, line.SpeakerID)

	if m.reserveEngine != nil {
		release, reserved := m.reserveEngine(ctx, "audio")
		if !reserved {
			return Manifest{}, Take{}, NewError(CodeEngineBusy, "engine \"audio\" is busy")
		}
		defer release()
	}
	audio, err := m.synthesize(ctx, line.Text, voiceID)
	if err != nil {
		return Manifest{}, Take{}, NewError(CodeSynthesisFailure, err.Error())
	}

	take, err := m.storeTake(storyID, lineID, nextTakeID(line.Takes), audio, voiceID, line.Text, m.now())
	if err != nil {
		return Manifest{}, Take{}, NewError(CodeStoreFailure, err.Error())
	}
	manifest.Script[index].Takes = append(manifest.Script[index].Takes, take)
	manifest.Script[index].CurrentTake = take.ID
	if err := m.store.SaveManifest(manifest); err != nil {
		return Manifest{}, Take{}, NewError(CodeStoreFailure, err.Error())
	}
	m.publishManifest(manifest)
	return manifest, take, nil
}

// EditLine applies a production edit to one line: which take is current,
// whether it is muted, and its timing. It never touches audio.
func (m *Manager) EditLine(storyID string, lineID string, patch LinePatch) (Manifest, error) {
	lock := m.editLock(storyID)
	lock.Lock()
	defer lock.Unlock()

	manifest, ok, err := m.store.Load(storyID)
	if err != nil {
		return Manifest{}, NewError(CodeStoreFailure, err.Error())
	}
	if !ok {
		return Manifest{}, NewError(CodeNotFound, "story not found")
	}
	index := lineIndex(manifest.Script, lineID)
	if index < 0 {
		return Manifest{}, NewError(CodeLineNotFound, "line not found in this story")
	}

	line := &manifest.Script[index]
	if patch.Text != nil {
		text := strings.TrimSpace(*patch.Text)
		if text == "" {
			return Manifest{}, NewError(CodeInvalidRequest, "line text cannot be empty; mute the line instead")
		}
		if len(text) > MaxScriptLineTextChars {
			return Manifest{}, NewError(CodeInvalidRequest, fmt.Sprintf("line text must be at most %d characters", MaxScriptLineTextChars))
		}
		if text != line.Text {
			line.Text = text
			// Every existing take says the old words. Deselect rather than
			// silently render audio that contradicts the script; the takes
			// stay on disk, and reselecting one is still possible if the
			// text is changed back.
			line.CurrentTake = ""
		}
	}
	if patch.CurrentTake != nil {
		take := takeByID(line.Takes, *patch.CurrentTake)
		if take == nil {
			return Manifest{}, NewError(CodeTakeNotFound, fmt.Sprintf("line %s has no take %q", lineID, *patch.CurrentTake))
		}
		if take.Text != line.Text {
			return Manifest{}, NewError(CodeStaleTake, fmt.Sprintf("take %s was recorded against different words; retake line %s", take.ID, lineID))
		}
		line.CurrentTake = take.ID
	}
	if patch.Muted != nil {
		line.Muted = *patch.Muted
	}
	if patch.GapBeforeMS != nil {
		if *patch.GapBeforeMS < MinGapMS || *patch.GapBeforeMS > MaxGapMS {
			return Manifest{}, NewError(CodeInvalidRequest, fmt.Sprintf("gap_before_ms must be between %d and %d", MinGapMS, MaxGapMS))
		}
		line.GapBeforeMS = *patch.GapBeforeMS
	}
	if patch.GapAfterMS != nil {
		if *patch.GapAfterMS < MinGapMS || *patch.GapAfterMS > MaxGapMS {
			return Manifest{}, NewError(CodeInvalidRequest, fmt.Sprintf("gap_after_ms must be between %d and %d", MinGapMS, MaxGapMS))
		}
		line.GapAfterMS = *patch.GapAfterMS
	}
	if err := m.store.SaveManifest(manifest); err != nil {
		return Manifest{}, NewError(CodeStoreFailure, err.Error())
	}
	m.publishManifest(manifest)
	return manifest, nil
}

// Render restitches a stored story from its current takes and publishes the
// result as a new revision. Earlier revisions stay on disk: what you already
// shared keeps playing what you shared.
func (m *Manager) Render(storyID string) (Manifest, Render, error) {
	// Two concurrent renders would otherwise both claim the same revision
	// number and overwrite an allegedly immutable file.
	lock := m.editLock(storyID)
	lock.Lock()
	defer lock.Unlock()

	manifest, ok, err := m.store.Load(storyID)
	if err != nil {
		return Manifest{}, Render{}, NewError(CodeStoreFailure, err.Error())
	}
	if !ok {
		return Manifest{}, Render{}, NewError(CodeNotFound, "story not found")
	}

	clips := make([][]byte, len(manifest.Script))
	var recipe []RenderLine
	for i, line := range manifest.Script {
		if line.Muted || line.CurrentTake == "" {
			continue
		}
		take := takeByID(line.Takes, line.CurrentTake)
		if take == nil {
			return Manifest{}, Render{}, NewError(CodeTakeNotFound, fmt.Sprintf("line %s selects take %q, which it does not have", line.ID, line.CurrentTake))
		}
		// A take recorded against different words is not this line any more.
		// Rendering it would quietly publish audio that contradicts the
		// script the manifest shows.
		if take.Text != line.Text {
			return Manifest{}, Render{}, NewError(CodeStaleTake, fmt.Sprintf("line %s was edited after take %s was recorded; retake it or mute the line", line.ID, take.ID))
		}
		audio, err := m.store.LoadTake(storyID, line.ID, line.CurrentTake)
		if err != nil {
			return Manifest{}, Render{}, err
		}
		clips[i] = audio
		recipe = append(recipe, RenderLine{
			LineID:      line.ID,
			TakeID:      take.ID,
			SpeakerID:   line.SpeakerID,
			VoiceID:     take.VoiceID,
			Text:        take.Text,
			GapBeforeMS: line.GapBeforeMS,
			GapAfterMS:  line.GapAfterMS,
		})
	}

	stitched, err := stitchTakes(clips, manifest.Script)
	if err != nil {
		return Manifest{}, Render{}, err
	}
	durationSeconds := manifest.DurationSeconds
	if d, err := wav.Duration(stitched); err == nil {
		durationSeconds = int(d.Round(time.Second) / time.Second)
	}
	if padded, err := wav.PadSilence(stitched, artifactPad, artifactPad); err == nil {
		stitched = padded
	}

	revision := nextRevision(manifest.Renders)
	if err := m.store.SaveRender(storyID, revision, stitched); err != nil {
		return Manifest{}, Render{}, NewError(CodeStoreFailure, err.Error())
	}
	render := Render{
		Revision:        revision,
		CreatedAt:       m.now().UTC(),
		DurationSeconds: durationSeconds,
		Bytes:           len(stitched),
		URL:             RenderURL(storyID, revision),
		Recipe:          recipe,
	}
	manifest.Renders = append(manifest.Renders, render)
	manifest.DurationSeconds = durationSeconds
	if err := m.store.SaveManifest(manifest); err != nil {
		return Manifest{}, Render{}, NewError(CodeStoreFailure, err.Error())
	}
	m.publishManifest(manifest)
	return manifest, render, nil
}

// publishManifest replaces the manifest a finished in-memory job is still
// serving. Status prefers the tracked job over the store for as long as the
// process lives, so without this a take-room edit would land on disk and
// stay invisible to GET /v1/stories/{id}.
func (m *Manager) publishManifest(manifest Manifest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[manifest.ID]
	if !ok {
		return
	}
	stored := manifest
	j.status.Manifest = &stored
	if url := manifest.Audio.URL; url != "" {
		artifactURL := url
		j.status.ArtifactURL = &artifactURL
	}
}

func lineIndex(script []ScriptLine, lineID string) int {
	for i, line := range script {
		if line.ID == lineID {
			return i
		}
	}
	return -1
}

func takeByID(takes []Take, id string) *Take {
	for i := range takes {
		if takes[i].ID == id {
			return &takes[i]
		}
	}
	return nil
}

func nextTakeID(takes []Take) string {
	return fmt.Sprintf("take-%03d", len(takes)+1)
}

func nextRevision(renders []Render) int {
	highest := 0
	for _, render := range renders {
		if render.Revision > highest {
			highest = render.Revision
		}
	}
	return highest + 1
}

func castVoiceFor(cast []CastMember, speakerID string) string {
	for _, member := range cast {
		if member.ID == speakerID {
			if member.VoiceID == "studio-default" {
				return ""
			}
			return member.VoiceID
		}
	}
	return ""
}
