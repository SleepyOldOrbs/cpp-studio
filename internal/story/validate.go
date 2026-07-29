package story

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type NormalizedRequest struct {
	Subject string
	// Mode is always resolved: ModeGrounded or ModeSketch, never empty.
	Mode string
	// Premise and Style are sketch-mode steering, empty in grounded mode.
	Premise       string
	Style         string
	TargetSeconds int
	SourceMode    string
	VoiceMode     string
	// Sources is nil in sketch mode.
	Sources []SourceInput
	// Cast is the resolved speaker list (default trio when none supplied).
	Cast []CastMember
	// CastVoices maps cast member id -> stored voice id ("" absent means
	// the studio default).
	CastVoices map[string]string
	// Title/Script, when Script is non-empty, bypass the script writer.
	Title  string
	Script []ScriptLine
	// Scenes is the resolved scene list of a multi-scene script, empty for
	// the one-unnamed-scene case.
	Scenes []Scene
}

// DefaultCast is the trio used when a request defines no cast.
func DefaultCast() []CastMember {
	return []CastMember{
		{ID: "narrator", DisplayName: "Narrator", Role: "sets scenes and links ideas"},
		{ID: "nova", DisplayName: "Nova", Role: "asks curious questions"},
		{ID: "dr-lumen", DisplayName: "Dr. Lumen", Role: "explains clearly"},
	}
}

// slugifyCastID turns a display name into a stable speaker id.
func slugifyCastID(name string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizeCast(inputs []CastInput) ([]CastMember, error) {
	if len(inputs) == 0 {
		return DefaultCast(), nil
	}
	if len(inputs) < MinCastMembers {
		return nil, NewError(CodeInvalidRequest, fmt.Sprintf("cast must include at least %d members", MinCastMembers))
	}
	if len(inputs) > MaxCastMembers {
		return nil, NewError(CodeInvalidRequest, fmt.Sprintf("cast must include at most %d members", MaxCastMembers))
	}
	seen := make(map[string]bool, len(inputs))
	cast := make([]CastMember, 0, len(inputs))
	for i, input := range inputs {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			return nil, NewError(CodeInvalidRequest, fmt.Sprintf("cast[%d].name is required", i))
		}
		if utf8.RuneCountInString(name) > MaxCastNameChars {
			return nil, NewError(CodeInvalidRequest, fmt.Sprintf("cast[%d].name must be at most %d characters", i, MaxCastNameChars))
		}
		role := strings.TrimSpace(input.Role)
		if utf8.RuneCountInString(role) > MaxCastRoleChars {
			return nil, NewError(CodeInvalidRequest, fmt.Sprintf("cast[%d].role must be at most %d characters", i, MaxCastRoleChars))
		}
		id := strings.TrimSpace(input.ID)
		if id == "" {
			id = slugifyCastID(name)
		}
		if id == "" {
			return nil, NewError(CodeInvalidRequest, fmt.Sprintf("cast[%d].name yields no usable speaker id", i))
		}
		if seen[id] {
			return nil, NewError(CodeInvalidRequest, fmt.Sprintf("cast contains duplicate speaker id %q", id))
		}
		seen[id] = true
		cast = append(cast, CastMember{ID: id, DisplayName: name, Role: role, VoiceID: strings.TrimSpace(input.VoiceID)})
	}
	return cast, nil
}

// normalizeScenes resolves a declared scene list: ids come from the input,
// or are slugged from titles the same way cast ids are slugged from names,
// so the draft flow can hand titles in and get referenceable ids back.
func normalizeScenes(inputs []SceneInput) ([]Scene, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if len(inputs) > MaxScenes {
		return nil, NewError(CodeInvalidScenes, fmt.Sprintf("scenes must include at most %d entries", MaxScenes))
	}
	seen := make(map[string]bool, len(inputs))
	scenes := make([]Scene, 0, len(inputs))
	for i, input := range inputs {
		title := strings.TrimSpace(input.Title)
		if utf8.RuneCountInString(title) > MaxSceneTitleChars {
			return nil, NewError(CodeInvalidScenes, fmt.Sprintf("scenes[%d].title must be at most %d characters", i, MaxSceneTitleChars))
		}
		premise := strings.TrimSpace(input.Premise)
		if utf8.RuneCountInString(premise) > MaxScenePremiseChars {
			return nil, NewError(CodeInvalidScenes, fmt.Sprintf("scenes[%d].premise must be at most %d characters", i, MaxScenePremiseChars))
		}
		id := strings.TrimSpace(input.ID)
		if id == "" {
			id = slugifyCastID(title)
		}
		if id == "" {
			return nil, NewError(CodeInvalidScenes, fmt.Sprintf("scenes[%d] needs an id or a title", i))
		}
		// Scene ids share the story-id alphabet so a scene can safely name
		// on-disk artifacts later (stage 3 assets) without a second rule.
		if err := validateStoryID(id); err != nil {
			return nil, NewError(CodeInvalidScenes, fmt.Sprintf("scenes[%d].id may use letters, digits, dash and underscore only", i))
		}
		if seen[id] {
			return nil, NewError(CodeInvalidScenes, fmt.Sprintf("scenes contains duplicate scene id %q", id))
		}
		seen[id] = true
		scenes = append(scenes, Scene{ID: id, Title: title, Premise: premise})
	}
	return scenes, nil
}

// normalizeSources applies the grounded-mode source contract: 3-5 entries,
// each with a title and an excerpt (URLs stay attribution metadata). Sketch
// mode has no sources to check, so whatever the caller sent is dropped.
func normalizeSources(mode string, inputs []SourceInput) ([]SourceInput, error) {
	if mode == ModeSketch {
		return nil, nil
	}
	if len(inputs) < MinSources {
		return nil, NewError(CodeInsufficientSources, fmt.Sprintf("Need at least %d usable source excerpts to generate a factual story.", MinSources))
	}
	if len(inputs) > MaxSources {
		return nil, NewError(CodeSourceLimitExceeded, fmt.Sprintf("sources must include at most %d entries", MaxSources))
	}

	sources := make([]SourceInput, 0, len(inputs))
	for i, source := range inputs {
		id := strings.TrimSpace(source.ID)
		if id == "" {
			id = fmt.Sprintf("src-%d", i+1)
		}
		title := strings.TrimSpace(source.Title)
		if title == "" {
			return nil, NewError(CodeSourceTitleRequired, fmt.Sprintf("sources[%d].title is required", i))
		}
		if utf8.RuneCountInString(title) > MaxSourceTitleChars {
			return nil, NewError(CodeSourceTitleRequired, fmt.Sprintf("sources[%d].title must be at most %d characters", i, MaxSourceTitleChars))
		}
		url := strings.TrimSpace(source.URL)
		if utf8.RuneCountInString(url) > MaxSourceURLChars {
			return nil, NewError(CodeSourceURLTooLarge, fmt.Sprintf("sources[%d].url must be at most %d characters", i, MaxSourceURLChars))
		}
		excerpt := strings.TrimSpace(source.Excerpt)
		if excerpt == "" {
			return nil, NewError(CodeMissingSourceExcerpt, fmt.Sprintf("sources[%d].excerpt is required; URLs are metadata only in v1", i))
		}
		if utf8.RuneCountInString(excerpt) > MaxSourceExcerptChars {
			return nil, NewError(CodeSourceExcerptTooLarge, fmt.Sprintf("sources[%d].excerpt must be at most %d characters", i, MaxSourceExcerptChars))
		}
		sources = append(sources, SourceInput{
			ID:      id,
			Title:   title,
			URL:     url,
			Excerpt: excerpt,
		})
	}
	return sources, nil
}

func ValidateCreateRequest(req CreateRequest) (NormalizedRequest, error) {
	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		return NormalizedRequest{}, NewError(CodeInvalidSubject, "subject is required")
	}
	if utf8.RuneCountInString(subject) > MaxSubjectChars {
		return NormalizedRequest{}, NewError(CodeInvalidSubject, fmt.Sprintf("subject must be at most %d characters", MaxSubjectChars))
	}

	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = ModeGrounded
	}
	if mode != ModeGrounded && mode != ModeSketch {
		return NormalizedRequest{}, NewError(CodeUnsupportedMode, fmt.Sprintf("mode must be %s or %s", ModeGrounded, ModeSketch))
	}

	premise := strings.TrimSpace(req.Premise)
	if utf8.RuneCountInString(premise) > MaxPremiseChars {
		return NormalizedRequest{}, NewError(CodeInvalidRequest, fmt.Sprintf("premise must be at most %d characters", MaxPremiseChars))
	}
	style := strings.TrimSpace(req.Style)
	if utf8.RuneCountInString(style) > MaxStyleChars {
		return NormalizedRequest{}, NewError(CodeInvalidRequest, fmt.Sprintf("style must be at most %d characters", MaxStyleChars))
	}
	if mode == ModeGrounded {
		// Grounded stories take their material from sources; carrying
		// invention hints into the manifest would misdescribe them.
		premise, style = "", ""
	}

	targetSeconds := req.TargetSeconds
	if targetSeconds != 0 && (targetSeconds < MinTargetSeconds || targetSeconds > MaxTargetSeconds) {
		return NormalizedRequest{}, NewError(CodeTargetSecondsInvalid, fmt.Sprintf("target_seconds must be between %d and %d", MinTargetSeconds, MaxTargetSeconds))
	}
	if targetSeconds == 0 {
		targetSeconds = 90
	}

	sourceMode := strings.TrimSpace(req.SourceMode)
	if mode == ModeSketch {
		// A sketch has no sources to run a mode on. Whatever the form left
		// in the field, the answer is none.
		sourceMode = "none"
	} else {
		if sourceMode == "" {
			sourceMode = "curated"
		}
		if sourceMode != "curated" {
			return NormalizedRequest{}, NewError(CodeUnsupportedSourceMode, "source_mode must be curated")
		}
	}

	voiceMode := strings.TrimSpace(req.VoiceMode)
	if voiceMode == "" {
		voiceMode = "placeholder"
	}
	if voiceMode != "placeholder" && voiceMode != "fixed" {
		return NormalizedRequest{}, NewError(CodeUnsupportedVoiceMode, "voice_mode must be placeholder or fixed")
	}

	sources, err := normalizeSources(mode, req.Sources)
	if err != nil {
		return NormalizedRequest{}, err
	}

	cast, err := normalizeCast(req.Cast)
	if err != nil {
		return NormalizedRequest{}, err
	}
	castIDs := make(map[string]bool, len(cast))
	for _, member := range cast {
		castIDs[member.ID] = true
	}

	// cast_voices entries land on the cast member with the matching id; the
	// cast list's own voice_id values are the other way of saying the same
	// thing.
	castVoices := make(map[string]string, len(cast))
	for i := range cast {
		if cast[i].VoiceID != "" {
			castVoices[cast[i].ID] = cast[i].VoiceID
		}
	}
	for speakerID, voiceID := range req.CastVoices {
		voiceID = strings.TrimSpace(voiceID)
		if voiceID == "" {
			continue
		}
		if !castIDs[speakerID] {
			return NormalizedRequest{}, NewError(CodeInvalidRequest, fmt.Sprintf("cast_voices key %q is not a cast member id", speakerID))
		}
		castVoices[speakerID] = voiceID
	}
	for i := range cast {
		if voiceID := castVoices[cast[i].ID]; voiceID != "" {
			cast[i].VoiceID = voiceID
		} else {
			cast[i].VoiceID = "studio-default"
		}
	}

	title := strings.TrimSpace(req.Title)
	if utf8.RuneCountInString(title) > MaxSubjectChars {
		return NormalizedRequest{}, NewError(CodeInvalidRequest, fmt.Sprintf("title must be at most %d characters", MaxSubjectChars))
	}
	if len(req.Script) > MaxScriptLines {
		return NormalizedRequest{}, NewError(CodeInvalidRequest, fmt.Sprintf("script must have at most %d lines", MaxScriptLines))
	}

	scenes, err := normalizeScenes(req.Scenes)
	if err != nil {
		return NormalizedRequest{}, err
	}
	// Scenes describe a submitted script; a scripted-by-model story has no
	// declared scenes yet.
	if len(scenes) > 0 && len(req.Script) == 0 {
		return NormalizedRequest{}, NewError(CodeInvalidScenes, "scenes require a submitted script")
	}
	// Run the scene↔line cross-checks on the request itself, not only in
	// ValidateManifest: a submitted script that violates them is knowably
	// doomed, and failing at POST time beats occupying the single story
	// slot just to fail the same way asynchronously. ValidateManifest stays
	// the final gate for scripts assembled later.
	if err := validateScenes(Manifest{Scenes: scenes, Script: req.Script}); err != nil {
		return NormalizedRequest{}, err
	}

	return NormalizedRequest{
		Subject:       subject,
		Mode:          mode,
		Premise:       premise,
		Style:         style,
		TargetSeconds: targetSeconds,
		SourceMode:    sourceMode,
		VoiceMode:     voiceMode,
		Sources:       sources,
		Cast:          cast,
		CastVoices:    castVoices,
		Title:         title,
		Script:        req.Script,
		Scenes:        scenes,
	}, nil
}
