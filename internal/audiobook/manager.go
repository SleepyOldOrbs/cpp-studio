package audiobook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cpp-studio/internal/engine"
	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

const DefaultRootDir = "out/audiobooks"

const (
	// DefaultEngineID preserves the original audiobook narrator when a
	// caller does not opt into an expressive backend.
	DefaultEngineID = engine.DefaultSpeechEngineID
	// DramaBoxEngineID is the only expressive engine accepted by the
	// audiobook contract in this release.
	DramaBoxEngineID = engine.DramaBoxSpeechEngineID
	// MaxDirectionRunes bounds user-authored delivery direction before it is
	// repeated for every narration chunk.
	MaxDirectionRunes = 500
	// DefaultDramaBoxDirection keeps factual narration restrained when the
	// user selects DramaBox without writing custom direction.
	DefaultDramaBoxDirection = "Warm, measured documentary narration with clear diction, restrained emotion, and thoughtful pauses."
)

// ArtifactName is the stitched narration file inside an audiobook's dir.
const ArtifactName = "book.wav"

// chunkGap is the silence between narrated chunks; paragraph pacing comes
// from chunk boundaries, so the gap stays short.
const chunkGap = 400 * time.Millisecond

// artifactPad is the lead/trail silence around the finished narration.
const artifactPad = 300 * time.Millisecond

// ReserveEngineFunc reserves the audio engine for the whole narration run.
type ReserveEngineFunc func(ctx context.Context, name string) (func(), bool)

// SynthesizeFunc speaks one chunk through engineID; voiceID "" means that
// engine's default or, for DramaBox, its supported text-only mode.
type SynthesizeFunc func(ctx context.Context, text string, voiceID string, engineID string) ([]byte, error)

type ManagerOptions struct {
	RootDir       string
	ReserveEngine ReserveEngineFunc
	Synthesize    SynthesizeFunc
	Jobs          *jobs.Registry
	Now           func() time.Time
}

// Request carries narration intent. NormalizeRequest and Submit validate and
// normalize its engine and direction before any job starts.
type Request struct {
	Title     string
	Text      string
	VoiceID   string
	EngineID  string
	Direction string
}

type Manager struct {
	mu            sync.Mutex
	rootDir       string
	reserveEngine ReserveEngineFunc
	synthesize    SynthesizeFunc
	registry      *jobs.Registry
	now           func() time.Time
	counter       int
	activeID      string
	cancels       map[string]context.CancelFunc
}

func NewManager(opts ManagerOptions) *Manager {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	rootDir := opts.RootDir
	if rootDir == "" {
		rootDir = DefaultRootDir
	}
	return &Manager{
		rootDir:       rootDir,
		reserveEngine: opts.ReserveEngine,
		synthesize:    opts.Synthesize,
		registry:      opts.Jobs,
		now:           now,
		cancels:       make(map[string]context.CancelFunc),
	}
}

// RequestError identifies invalid audiobook intent. HTTP callers map these
// errors to 400, while active-job and reservation conflicts remain 409.
type RequestError struct {
	message string
}

func (e *RequestError) Error() string { return e.message }

func requestErrorf(format string, args ...any) error {
	return &RequestError{message: fmt.Sprintf(format, args...)}
}

// IsRequestError reports whether err is safe to classify as invalid input.
func IsRequestError(err error) bool {
	var requestErr *RequestError
	return errors.As(err, &requestErr)
}

// NormalizeRequest applies the public engine/direction policy without
// touching the document. It is shared by the HTTP boundary and Manager so
// direct callers receive the same validation.
func NormalizeRequest(req Request) (Request, error) {
	req.EngineID = strings.ToLower(strings.TrimSpace(req.EngineID))
	if req.EngineID == "" {
		req.EngineID = DefaultEngineID
	}
	if req.EngineID != DefaultEngineID && req.EngineID != DramaBoxEngineID {
		return Request{}, requestErrorf("audiobook engine must be audio or dramabox, got %q", req.EngineID)
	}
	req.Direction = strings.TrimSpace(req.Direction)
	if utf8.RuneCountInString(req.Direction) > MaxDirectionRunes {
		return Request{}, requestErrorf("audiobook direction must be at most %d characters", MaxDirectionRunes)
	}
	if req.EngineID == DefaultEngineID && req.Direction != "" {
		return Request{}, requestErrorf("audiobook direction is only supported with dramabox")
	}
	if req.EngineID == DramaBoxEngineID && req.Direction == "" {
		req.Direction = DefaultDramaBoxDirection
	}
	return req, nil
}

// BuildDramaBoxPrompt places delivery direction outside a single quoted
// source passage. Double-quote punctuation is changed to apostrophes so
// neither source nor direction can close that passage early; all source
// words remain unchanged.
func BuildDramaBoxPrompt(direction, chunk string) string {
	direction = strings.Join(strings.Fields(normalizePromptQuotes(direction)), " ")
	chunk = normalizePromptQuotes(chunk)
	return direction + ` "` + chunk + `"`
}

func normalizePromptQuotes(value string) string {
	return strings.NewReplacer(`"`, `'`, "“", `'`, "”", `'`).Replace(value)
}

// Submit validates, chunks, and starts a narration job. One audiobook runs
// at a time: narration monopolizes the audio engine for minutes.
func (m *Manager) Submit(ctx context.Context, req Request) (string, int, error) {
	if m.synthesize == nil {
		return "", 0, requestErrorf("audiobooks need a configured speech engine")
	}
	var err error
	req, err = NormalizeRequest(req)
	if err != nil {
		return "", 0, err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Untitled audiobook"
	}
	chunks := Chunk(req.Text, DefaultChunkChars)
	if len(chunks) == 0 {
		return "", 0, requestErrorf("document contains no narratable text")
	}
	if len(chunks) > MaxChunks {
		return "", 0, requestErrorf("document needs %d chunks, max is %d; narrate it in parts", len(chunks), MaxChunks)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeID != "" {
		return "", 0, fmt.Errorf("another audiobook is already narrating")
	}
	var release func()
	if m.reserveEngine != nil {
		var ok bool
		release, ok = m.reserveEngine(ctx, req.EngineID)
		if !ok {
			return "", 0, fmt.Errorf("engine %q is busy", req.EngineID)
		}
	}
	m.counter++
	id := fmt.Sprintf("book_%s_%03d", m.now().Format("20060102_150405"), m.counter)
	jobCtx, cancel := context.WithCancel(context.Background())
	m.activeID = id
	m.cancels[id] = cancel
	if m.registry != nil {
		m.registry.Track(id, "audiobook", cancel)
	}

	go m.run(jobCtx, id, title, req, chunks, release)
	return id, len(chunks), nil
}

func (m *Manager) run(ctx context.Context, id, title string, req Request, chunks []string, release func()) {
	defer func() {
		if release != nil {
			release()
		}
		m.mu.Lock()
		if m.activeID == id {
			m.activeID = ""
		}
		delete(m.cancels, id)
		m.mu.Unlock()
	}()

	if err := os.MkdirAll(m.rootDir, 0o755); err != nil {
		if m.registry != nil {
			m.registry.Fail(id, "create audiobook staging dir: "+err.Error())
		}
		return
	}
	clipDir, err := os.MkdirTemp(m.rootDir, "."+id+".clips-")
	if err != nil {
		if m.registry != nil {
			m.registry.Fail(id, "create audiobook staging dir: "+err.Error())
		}
		return
	}
	defer os.RemoveAll(clipDir)
	clipPaths := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		if ctx.Err() != nil {
			m.markCancelled(id)
			return
		}
		if m.registry != nil {
			detail := fmt.Sprintf("narrating chunk %d/%d", i+1, len(chunks))
			if req.EngineID != DefaultEngineID {
				detail += " with " + req.EngineID
			}
			m.registry.Update(id, float64(i)/float64(len(chunks)), detail)
		}
		text := chunk
		if req.EngineID == DramaBoxEngineID {
			text = BuildDramaBoxPrompt(req.Direction, chunk)
		}
		clip, err := m.synthesize(ctx, text, req.VoiceID, req.EngineID)
		if err != nil {
			if ctx.Err() != nil {
				m.markCancelled(id)
				return
			}
			if m.registry != nil {
				m.registry.Fail(id, fmt.Sprintf("narrate chunk %d/%d with %s: %v", i+1, len(chunks), req.EngineID, err))
			}
			return
		}
		clipPath := filepath.Join(clipDir, fmt.Sprintf("chunk-%04d.wav", i+1))
		if err := os.WriteFile(clipPath, clip, 0o600); err != nil {
			if m.registry != nil {
				m.registry.Fail(id, fmt.Sprintf("stage chunk %d/%d with %s: %v", i+1, len(chunks), req.EngineID, err))
			}
			return
		}
		clipPaths = append(clipPaths, clipPath)
	}

	if m.registry != nil {
		m.registry.Update(id, 0.97, "stitching")
	}
	gaps := make([]time.Duration, len(clipPaths))
	for i := range gaps {
		gaps[i] = chunkGap
	}
	stitched, err := wav.ConcatenateGapsFrom(len(clipPaths), gaps, func(i int) ([]byte, error) {
		return os.ReadFile(clipPaths[i])
	})
	if err != nil {
		if m.registry != nil {
			m.registry.Fail(id, "stitch narration: "+err.Error())
		}
		return
	}
	manifest := Manifest{
		ID:        id,
		Title:     title,
		VoiceID:   req.VoiceID,
		EngineID:  req.EngineID,
		Direction: req.Direction,
		Chunks:    len(chunks),
		CreatedAt: m.now(),
	}
	if duration, err := wav.Duration(stitched); err == nil {
		manifest.DurationSeconds = int(duration.Round(time.Second) / time.Second)
	}
	if padded, err := wav.PadSilence(stitched, artifactPad, artifactPad); err == nil {
		stitched = padded
	}
	manifest.ArtifactURL = "/v1/audiobooks/" + id + "/artifact/" + ArtifactName

	if ctx.Err() != nil {
		m.markCancelled(id)
		return
	}
	if err := m.save(manifest, stitched); err != nil {
		if m.registry != nil {
			m.registry.Fail(id, "save audiobook: "+err.Error())
		}
		return
	}
	if m.registry != nil {
		m.registry.Complete(id, map[string]string{"artifactUrl": manifest.ArtifactURL, "title": manifest.Title, "engine": manifest.EngineID})
	}
}

func (m *Manager) markCancelled(id string) {
	if m.registry != nil {
		m.registry.MarkCancelled(id)
	}
}

func (m *Manager) save(manifest Manifest, audio []byte) error {
	if err := wav.ValidateBytes(audio); err != nil {
		return fmt.Errorf("stitched narration: %w", err)
	}
	if err := os.MkdirAll(m.rootDir, 0o755); err != nil {
		return fmt.Errorf("create audiobooks dir: %w", err)
	}
	tmpDir := filepath.Join(m.rootDir, "."+manifest.ID+".tmp")
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("create temp audiobook dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, ArtifactName), audio, 0o644); err != nil {
		return fmt.Errorf("write narration wav: %w", err)
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return os.Rename(tmpDir, filepath.Join(m.rootDir, manifest.ID))
}

// List returns finished audiobooks, newest first.
func (m *Manager) List() ([]Manifest, error) {
	entries, err := os.ReadDir(m.rootDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read audiobooks dir: %w", err)
	}
	out := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.rootDir, entry.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}
		out = append(out, manifest)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// ArtifactPath resolves an audiobook's WAV for serving.
func (m *Manager) ArtifactPath(id, filename string) (string, error) {
	if err := validateBookID(id); err != nil {
		return "", fmt.Errorf("audiobook not found")
	}
	if filename != ArtifactName {
		return "", fmt.Errorf("unsupported audiobook artifact")
	}
	path := filepath.Join(m.rootDir, id, filename)
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("audiobook artifact not found")
	}
	defer file.Close()
	if err := wav.ValidateHeader(file); err != nil {
		return "", fmt.Errorf("audiobook artifact is not a valid WAV")
	}
	return path, nil
}

func validateBookID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || filepath.IsAbs(id) {
		return fmt.Errorf("invalid audiobook id")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return fmt.Errorf("invalid audiobook id")
	}
	return nil
}
