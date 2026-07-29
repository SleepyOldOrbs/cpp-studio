package story

import "time"

const (
	DefaultRootDir          = "out/stories"
	MaxRequestBodyBytes     = 512 * 1024
	MaxSubjectChars         = 200
	MinTargetSeconds        = 30
	MaxTargetSeconds        = 300
	MinSources              = 3
	MaxSources              = 5
	MaxSourceTitleChars     = 200
	MaxSourceURLChars       = 2048
	MaxSourceExcerptChars   = 12000
	MaxPremiseChars         = 2000
	MaxStyleChars           = 1000
	MaxScriptLineTextChars  = 2000
	MaxScriptLines          = 60
	MinCastMembers          = 2
	MaxCastMembers          = 6
	MaxCastNameChars        = 60
	MaxCastRoleChars        = 200
	MaxScenes               = 40
	MaxSceneTitleChars      = 200
	MaxScenePremiseChars    = 2000
	MaxGeneratedWAVBytes    = 32 * 1024 * 1024
	StoryArtifactName       = "story.wav"
	DefaultRetryAfterMillis = 500
)

// Mode selects the writing contract a story is held to.
//
// ModeGrounded is the documentary contract this desk was built for: the
// request supplies sources, the scaffold turns them into fact cards, and
// every script line must cite one. ModeSketch is the opposite contract —
// premise and style in, invention encouraged, no sources and no citations —
// so a cloned cast can perform new material. The mode travels with the
// request into the manifest so a stored story still says which rules it was
// written under.
const (
	ModeGrounded = "grounded"
	ModeSketch   = "sketch"
)

type Status string

const (
	StatusQueued            Status = "queued"
	StatusExtractingSources Status = "extracting_sources"
	StatusPlanning          Status = "planning"
	StatusScripting         Status = "scripting"
	StatusSynthesizing      Status = "synthesizing"
	StatusStitching         Status = "stitching"
	StatusComplete          Status = "complete"
	StatusFailed            Status = "failed"
	StatusCancelled         Status = "cancelled"
)

type ErrorCode string

const (
	CodeInvalidRequest         ErrorCode = "invalid_request"
	CodeInvalidSubject         ErrorCode = "invalid_subject"
	CodeUnsupportedMode        ErrorCode = "unsupported_mode"
	CodeUnsupportedSourceMode  ErrorCode = "unsupported_source_mode"
	CodeUnsupportedVoiceMode   ErrorCode = "unsupported_voice_mode"
	CodeSourceTitleRequired    ErrorCode = "source_title_required"
	CodeMissingSourceExcerpt   ErrorCode = "missing_source_excerpt"
	CodeInsufficientSources    ErrorCode = "insufficient_sources"
	CodeSourceLimitExceeded    ErrorCode = "source_limit_exceeded"
	CodeSourceURLTooLarge      ErrorCode = "source_url_too_large"
	CodeSourceExcerptTooLarge  ErrorCode = "source_excerpt_too_large"
	CodeTargetSecondsInvalid   ErrorCode = "target_seconds_invalid"
	CodeStoryBusy              ErrorCode = "story_busy"
	CodeEngineBusy             ErrorCode = "engine_busy"
	CodeNotFound               ErrorCode = "not_found"
	CodeCannotCancel           ErrorCode = "cannot_cancel"
	CodeStoreFailure           ErrorCode = "store_failure"
	CodeSynthesisFailure       ErrorCode = "synthesis_failure"
	CodeGroundingFailure       ErrorCode = "grounding_failure"
	CodeInvalidScript          ErrorCode = "invalid_script"
	CodeInvalidScenes          ErrorCode = "invalid_scenes"
	CodeSceneNotFound          ErrorCode = "scene_not_found"
	CodeLineNotFound           ErrorCode = "line_not_found"
	CodeTakeNotFound           ErrorCode = "take_not_found"
	CodeStaleTake              ErrorCode = "stale_take"
	CodeExportUnavailable      ErrorCode = "export_unavailable"
	CodeMasteringFailure       ErrorCode = "mastering_failure"
	CodeNothingToRender        ErrorCode = "nothing_to_render"
	CodeArtifactNotFound       ErrorCode = "artifact_not_found"
	CodeUnsupportedArtifact    ErrorCode = "unsupported_artifact"
	CodeInvalidArtifactRequest ErrorCode = "invalid_artifact_request"
)

type StoryError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (e *StoryError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func NewError(code ErrorCode, message string) *StoryError {
	return &StoryError{Code: code, Message: message}
}

type CreateRequest struct {
	Subject string `json:"subject"`
	// Mode is "grounded" (default) or "sketch"; see the Mode constants.
	Mode string `json:"mode,omitempty"`
	// Premise and Style steer sketch mode: Premise is the situation to
	// play, Style is the tone to play it in. Both are ignored in grounded
	// mode, where the sources do that job.
	Premise       string `json:"premise,omitempty"`
	Style         string `json:"style,omitempty"`
	TargetSeconds int    `json:"target_seconds,omitempty"`
	SourceMode    string `json:"source_mode,omitempty"`
	VoiceMode     string `json:"voice_mode,omitempty"`
	// Sources are required in grounded mode (3-5) and ignored in sketch
	// mode, which invents rather than reports.
	Sources []SourceInput `json:"sources"`
	// Cast defines the speakers (2-6). Empty means the default trio:
	// narrator, nova, dr-lumen.
	Cast []CastInput `json:"cast,omitempty"`
	// CastVoices assigns a stored voice id per cast member id; missing or
	// empty entries speak with the studio default voice. Only meaningful
	// for voice_mode "fixed".
	CastVoices map[string]string `json:"cast_voices,omitempty"`
	// Title and Script, when provided together with Script non-empty,
	// skip llama scripting and produce this exact script (the draft →
	// edit → produce flow). In grounded mode the lines must stay grounded
	// in the fact cards derived from Sources; in sketch mode they only
	// have to be speakable lines from cast members.
	Title  string       `json:"title,omitempty"`
	Script []ScriptLine `json:"script,omitempty"`
	// Scenes declares the episode's scene list when Script is a multi-scene
	// episode: every script line then names one of these via scene_id, in
	// contiguous runs following this order. Empty means the script is one
	// unnamed scene — which is what every sketch is.
	Scenes []SceneInput `json:"scenes,omitempty"`
}

// SceneInput is one declared scene of a submitted episode script.
type SceneInput struct {
	ID      string `json:"id,omitempty"`
	Title   string `json:"title,omitempty"`
	Premise string `json:"premise,omitempty"`
}

// CastInput is one user-defined speaker.
type CastInput struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Role    string `json:"role,omitempty"`
	VoiceID string `json:"voice_id,omitempty"`
}

// DraftResponse is a story written but not produced: everything needed to
// review and edit the script before synthesis.
type DraftResponse struct {
	Subject string `json:"subject"`
	// Mode echoes the contract the draft was written under, so an editor
	// knows whether fact citations are meaningful.
	Mode        string       `json:"mode"`
	Title       string       `json:"title"`
	Sources     []Source     `json:"sources"`
	SourceNotes []SourceNote `json:"source_notes"`
	FactCards   []FactCard   `json:"fact_cards"`
	Cast        []CastMember `json:"cast"`
	Scenes      []Scene      `json:"scenes,omitempty"`
	Script      []ScriptLine `json:"script"`
}

type SourceInput struct {
	ID      string `json:"id,omitempty"`
	Title   string `json:"title"`
	URL     string `json:"url,omitempty"`
	Excerpt string `json:"excerpt"`
}

type Source struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
}

type SourceNote struct {
	ID       string `json:"id"`
	SourceID string `json:"source_id"`
	Text     string `json:"text"`
}

type FactCard struct {
	ID            string   `json:"id"`
	SourceNoteIDs []string `json:"source_note_ids"`
	Claim         string   `json:"claim"`
	Conflicting   bool     `json:"conflicting"`
}

type CastMember struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role,omitempty"`
	VoiceID     string `json:"voice_id"`
}

type ScriptLine struct {
	// ID is the line's stable handle for the life of the story: takes hang
	// off it, and editing the text does not change it. Requests may omit
	// it (older clients, hand-written JSON); the manifest always has one.
	ID        string   `json:"id,omitempty"`
	SpeakerID string   `json:"speaker_id"`
	Text      string   `json:"text"`
	FactIDs   []string `json:"fact_ids"`
	// SceneID names the scene this line belongs to when the story declares
	// scenes. Lines of a scene are contiguous and follow the manifest's
	// scene order. Empty everywhere means the whole script is one unnamed
	// scene — every story written before episodes existed reads that way.
	SceneID string `json:"scene_id,omitempty"`
	// Takes are every recording ever made of this line, oldest first.
	// CurrentTake names the one that gets rendered.
	Takes       []Take `json:"takes,omitempty"`
	CurrentTake string `json:"current_take,omitempty"`
	// Muted drops the line from renders without deleting it.
	Muted bool `json:"muted,omitempty"`
	// GapBeforeMS and GapAfterMS adjust this line's timing in the render,
	// on top of the default inter-line gap. Negative values tighten.
	GapBeforeMS int `json:"gap_before_ms,omitempty"`
	GapAfterMS  int `json:"gap_after_ms,omitempty"`
}

// Scene is one contiguous run of script lines written and produced as a
// unit: a sketch is a one-scene episode, and an episode is a sequence of
// these. The id is the scene's stable identity — per-scene audio assets
// (beds, stings; stage 3) and any future per-scene artifact hang off it,
// which is why scenes are persisted entries rather than derived groupings.
type Scene struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	// Premise is what this scene plays, in the same sense as the story's
	// premise: the writer's brief for these lines, kept so a scene can be
	// rewritten later against the intent it was written under.
	Premise string `json:"premise,omitempty"`
}

// Take is one recording of one line, kept on disk so a bad read can be
// replaced without regenerating the episode. VoiceID and Text record what
// this take actually was, so a take stays explainable after the line is
// edited or the cast is re-voiced.
type Take struct {
	ID         string    `json:"id"`
	VoiceID    string    `json:"voice_id"`
	Text       string    `json:"text"`
	CreatedAt  time.Time `json:"created_at"`
	DurationMS int       `json:"duration_ms"`
	Bytes      int       `json:"bytes"`
	// URL serves this take's WAV for audition in the take room.
	URL string `json:"url"`
}

// Render is one published stitch of the current takes. Renders are
// immutable: re-rendering adds a revision rather than overwriting, so a
// story you already shared stays what you shared.
type Render struct {
	Revision        int       `json:"revision"`
	CreatedAt       time.Time `json:"created_at"`
	DurationSeconds int       `json:"duration_seconds"`
	Bytes           int       `json:"bytes"`
	URL             string    `json:"url"`
	// Recipe is exactly what went into this revision. The script stays
	// editable after a render, so without a snapshot an "immutable" render
	// would be immutable bytes with no surviving explanation of what they
	// are. One entry per line that was actually used.
	Recipe []RenderLine `json:"recipe,omitempty"`
	// Master records the levelling and loudness work behind this revision,
	// or is absent when no measurement engine was configured and the render
	// went out as it was stitched.
	Master *Master `json:"master,omitempty"`
	// Exports are delivery encodings of this exact revision. They hang off
	// the render rather than the story because that is what they are an
	// encoding of; re-rendering does not invalidate them, it just means the
	// newest revision has none yet.
	Exports []Export `json:"exports,omitempty"`
}

// Export is one delivery encoding of a render revision.
type Export struct {
	Format    string    `json:"format"`
	Bitrate   string    `json:"bitrate"`
	Bytes     int       `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"url"`
}

// RenderLine is one line as it stood when a revision was published.
type RenderLine struct {
	LineID      string `json:"line_id"`
	TakeID      string `json:"take_id"`
	SpeakerID   string `json:"speaker_id"`
	VoiceID     string `json:"voice_id,omitempty"`
	Text        string `json:"text"`
	GapBeforeMS int    `json:"gap_before_ms,omitempty"`
	GapAfterMS  int    `json:"gap_after_ms,omitempty"`
}

type AudioRef struct {
	Format string `json:"format"`
	URL    string `json:"url"`
}

type Manifest struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	// Mode records which contract wrote this story. Older manifests
	// predate the field; empty reads as grounded.
	Mode            string       `json:"mode,omitempty"`
	Premise         string       `json:"premise,omitempty"`
	Style           string       `json:"style,omitempty"`
	Title           string       `json:"title"`
	Status          Status       `json:"status"`
	CreatedAt       time.Time    `json:"created_at"`
	DurationSeconds int          `json:"duration_seconds"`
	Sources         []Source     `json:"sources"`
	SourceNotes     []SourceNote `json:"source_notes"`
	FactCards       []FactCard   `json:"fact_cards"`
	Cast            []CastMember `json:"cast"`
	// Scenes orders the episode's scenes; script lines reference them by
	// scene_id in contiguous runs. Empty on every manifest written before
	// episodes existed and on plain sketches: an absent scene list reads as
	// one unnamed scene holding the whole script.
	Scenes []Scene      `json:"scenes,omitempty"`
	Script []ScriptLine `json:"script"`
	Audio  AudioRef     `json:"audio"`
	// Renders is the revision history. Empty on manifests written before
	// the take room; Audio still points at the current render either way.
	Renders []Render `json:"renders,omitempty"`
}

type CreateResponse struct {
	ID        string `json:"id"`
	Status    Status `json:"status"`
	StatusURL string `json:"status_url"`
}

type StatusResponse struct {
	ID           string      `json:"id"`
	Status       Status      `json:"status"`
	Stage        Status      `json:"stage"`
	Progress     float64     `json:"progress"`
	Error        *StoryError `json:"error"`
	ArtifactURL  *string     `json:"artifact_url"`
	RetryAfterMS int         `json:"retry_after_ms,omitempty"`
	Manifest     *Manifest   `json:"manifest,omitempty"`
}

type Summary struct {
	ID              string    `json:"id"`
	Subject         string    `json:"subject"`
	Mode            string    `json:"mode,omitempty"`
	Title           string    `json:"title"`
	Status          Status    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	DurationSeconds int       `json:"duration_seconds"`
	ArtifactURL     string    `json:"artifact_url"`
}

type ListResponse struct {
	Stories []Summary `json:"stories"`
}
