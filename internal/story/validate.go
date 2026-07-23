package story

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type NormalizedRequest struct {
	Subject       string
	TargetSeconds int
	SourceMode    string
	VoiceMode     string
	Sources       []SourceInput
	CastVoices    map[string]string
}

// CastMemberIDs is the fixed speaker trio every story script uses.
var CastMemberIDs = []string{"narrator", "nova", "dr-lumen"}

func ValidateCreateRequest(req CreateRequest) (NormalizedRequest, error) {
	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		return NormalizedRequest{}, NewError(CodeInvalidSubject, "subject is required")
	}
	if utf8.RuneCountInString(subject) > MaxSubjectChars {
		return NormalizedRequest{}, NewError(CodeInvalidSubject, fmt.Sprintf("subject must be at most %d characters", MaxSubjectChars))
	}

	targetSeconds := req.TargetSeconds
	if targetSeconds != 0 && (targetSeconds < MinTargetSeconds || targetSeconds > MaxTargetSeconds) {
		return NormalizedRequest{}, NewError(CodeTargetSecondsInvalid, fmt.Sprintf("target_seconds must be between %d and %d", MinTargetSeconds, MaxTargetSeconds))
	}
	if targetSeconds == 0 {
		targetSeconds = 90
	}

	sourceMode := strings.TrimSpace(req.SourceMode)
	if sourceMode == "" {
		sourceMode = "curated"
	}
	if sourceMode != "curated" {
		return NormalizedRequest{}, NewError(CodeUnsupportedSourceMode, "source_mode must be curated")
	}

	voiceMode := strings.TrimSpace(req.VoiceMode)
	if voiceMode == "" {
		voiceMode = "placeholder"
	}
	if voiceMode != "placeholder" && voiceMode != "fixed" {
		return NormalizedRequest{}, NewError(CodeUnsupportedVoiceMode, "voice_mode must be placeholder or fixed")
	}

	if len(req.Sources) < MinSources {
		return NormalizedRequest{}, NewError(CodeInsufficientSources, fmt.Sprintf("Need at least %d usable source excerpts to generate a factual story.", MinSources))
	}
	if len(req.Sources) > MaxSources {
		return NormalizedRequest{}, NewError(CodeSourceLimitExceeded, fmt.Sprintf("sources must include at most %d entries", MaxSources))
	}

	sources := make([]SourceInput, 0, len(req.Sources))
	for i, source := range req.Sources {
		id := strings.TrimSpace(source.ID)
		if id == "" {
			id = fmt.Sprintf("src-%d", i+1)
		}
		title := strings.TrimSpace(source.Title)
		if title == "" {
			return NormalizedRequest{}, NewError(CodeSourceTitleRequired, fmt.Sprintf("sources[%d].title is required", i))
		}
		if utf8.RuneCountInString(title) > MaxSourceTitleChars {
			return NormalizedRequest{}, NewError(CodeSourceTitleRequired, fmt.Sprintf("sources[%d].title must be at most %d characters", i, MaxSourceTitleChars))
		}
		url := strings.TrimSpace(source.URL)
		if utf8.RuneCountInString(url) > MaxSourceURLChars {
			return NormalizedRequest{}, NewError(CodeSourceURLTooLarge, fmt.Sprintf("sources[%d].url must be at most %d characters", i, MaxSourceURLChars))
		}
		excerpt := strings.TrimSpace(source.Excerpt)
		if excerpt == "" {
			return NormalizedRequest{}, NewError(CodeMissingSourceExcerpt, fmt.Sprintf("sources[%d].excerpt is required; URLs are metadata only in v1", i))
		}
		if utf8.RuneCountInString(excerpt) > MaxSourceExcerptChars {
			return NormalizedRequest{}, NewError(CodeSourceExcerptTooLarge, fmt.Sprintf("sources[%d].excerpt must be at most %d characters", i, MaxSourceExcerptChars))
		}
		sources = append(sources, SourceInput{
			ID:      id,
			Title:   title,
			URL:     url,
			Excerpt: excerpt,
		})
	}

	castVoices := make(map[string]string, len(req.CastVoices))
	for speakerID, voiceID := range req.CastVoices {
		voiceID = strings.TrimSpace(voiceID)
		if voiceID == "" {
			continue
		}
		known := false
		for _, id := range CastMemberIDs {
			if speakerID == id {
				known = true
				break
			}
		}
		if !known {
			return NormalizedRequest{}, NewError(CodeInvalidRequest, fmt.Sprintf("cast_voices key %q must be one of %s", speakerID, strings.Join(CastMemberIDs, ", ")))
		}
		castVoices[speakerID] = voiceID
	}

	return NormalizedRequest{
		Subject:       subject,
		TargetSeconds: targetSeconds,
		SourceMode:    sourceMode,
		VoiceMode:     voiceMode,
		Sources:       sources,
		CastVoices:    castVoices,
	}, nil
}
