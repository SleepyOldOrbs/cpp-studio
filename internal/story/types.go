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
	MaxScriptLineTextChars  = 2000
	MaxGeneratedWAVBytes    = 32 * 1024 * 1024
	StoryArtifactName       = "story.wav"
	DefaultRetryAfterMillis = 500
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
	CodeGroundingFailure       ErrorCode = "grounding_failure"
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
	Subject       string        `json:"subject"`
	TargetSeconds int           `json:"target_seconds,omitempty"`
	SourceMode    string        `json:"source_mode,omitempty"`
	VoiceMode     string        `json:"voice_mode,omitempty"`
	Sources       []SourceInput `json:"sources"`
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
	VoiceID     string `json:"voice_id"`
}

type ScriptLine struct {
	SpeakerID string   `json:"speaker_id"`
	Text      string   `json:"text"`
	FactIDs   []string `json:"fact_ids"`
}

type AudioRef struct {
	Format string `json:"format"`
	URL    string `json:"url"`
}

type Manifest struct {
	ID              string       `json:"id"`
	Subject         string       `json:"subject"`
	Title           string       `json:"title"`
	Status          Status       `json:"status"`
	CreatedAt       time.Time    `json:"created_at"`
	DurationSeconds int          `json:"duration_seconds"`
	Sources         []Source     `json:"sources"`
	SourceNotes     []SourceNote `json:"source_notes"`
	FactCards       []FactCard   `json:"fact_cards"`
	Cast            []CastMember `json:"cast"`
	Script          []ScriptLine `json:"script"`
	Audio           AudioRef     `json:"audio"`
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
	Title           string    `json:"title"`
	Status          Status    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	DurationSeconds int       `json:"duration_seconds"`
	ArtifactURL     string    `json:"artifact_url"`
}

type ListResponse struct {
	Stories []Summary `json:"stories"`
}
