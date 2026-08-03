package audiobook

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	DefaultSpeakerPhrase  = "A man"
	DefaultDeliveryPreset = "factual-documentary"
)

type DramaBoxPromptSpec struct {
	SpeakerPhrase     string `json:"speakerPhrase"`
	DeliveryPreset    string `json:"deliveryPreset"`
	AdvancedDirection string `json:"advancedDirection,omitempty"`
}

type PromptWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type PromptNormalization struct {
	Field      string `json:"field"`
	Original   string `json:"original"`
	Normalized string `json:"normalized"`
	Reason     string `json:"reason"`
}

type PromptEvaluation struct {
	Spec            DramaBoxPromptSpec    `json:"spec"`
	DeliveryText    string                `json:"deliveryText"`
	GeneratedPrompt string                `json:"generatedPrompt,omitempty"`
	Warnings        []PromptWarning       `json:"warnings"`
	Normalizations  []PromptNormalization `json:"normalizations"`
}

type PromptSectionPreview struct {
	StartByte       int64  `json:"startByte"`
	EndByte         int64  `json:"endByte"`
	GeneratedPrompt string `json:"generatedPrompt"`
}

var allowedSpeakerPhrases = map[string]bool{
	"A man": true, "A woman": true, "A young woman": true, "An elderly man": true, "A child": true,
}

var deliveryPresets = map[string]string{
	"factual-documentary": "speaks with warm, measured documentary delivery, clear diction, restrained emotion, and thoughtful pauses,",
	"clear-explainer":     "speaks clearly at a steady pace, with precise diction and restrained emphasis,",
	"calm-reading":        "speaks calmly and evenly, with natural pauses and restrained emotion,",
}

func SpeakerPhrases() []string {
	return []string{"A man", "A woman", "A young woman", "An elderly man", "A child"}
}

func DeliveryPresets() map[string]string {
	copy := make(map[string]string, len(deliveryPresets))
	for id, text := range deliveryPresets {
		copy[id] = text
	}
	return copy
}

func EvaluateDramaBoxPrompt(spec DramaBoxPromptSpec, source string) (PromptEvaluation, error) {
	spec.SpeakerPhrase = strings.TrimSpace(spec.SpeakerPhrase)
	spec.DeliveryPreset = strings.TrimSpace(spec.DeliveryPreset)
	if spec.SpeakerPhrase == "" {
		spec.SpeakerPhrase = DefaultSpeakerPhrase
	}
	if !allowedSpeakerPhrases[spec.SpeakerPhrase] {
		return PromptEvaluation{}, requestErrorf("DramaBox speaker phrase is not allowlisted")
	}
	if utf8.RuneCountInString(spec.AdvancedDirection) > MaxDirectionRunes {
		return PromptEvaluation{}, requestErrorf("audiobook direction must be at most %d characters", MaxDirectionRunes)
	}
	evaluation := PromptEvaluation{Spec: spec, Warnings: []PromptWarning{}, Normalizations: []PromptNormalization{}}
	advanced := strings.TrimSpace(spec.AdvancedDirection)
	if advanced != "" {
		if strings.ContainsAny(advanced, `"“”`) {
			return PromptEvaluation{}, requestErrorf("advanced DramaBox direction cannot contain double quotes")
		}
		normalized := strings.Join(strings.Fields(advanced), " ")
		if normalized != spec.AdvancedDirection {
			evaluation.Normalizations = append(evaluation.Normalizations, PromptNormalization{
				Field: "advancedDirection", Original: spec.AdvancedDirection, Normalized: normalized, Reason: "collapsed surrounding and repeated whitespace",
			})
		}
		evaluation.Spec.AdvancedDirection = normalized
		evaluation.Spec.DeliveryPreset = "advanced"
		evaluation.DeliveryText = ensurePromptComma(normalized)
		if evaluation.DeliveryText != normalized {
			evaluation.Normalizations = append(evaluation.Normalizations, PromptNormalization{
				Field: "advancedDirectionPunctuation", Original: normalized, Normalized: evaluation.DeliveryText,
				Reason: "replaced terminal punctuation with the comma required before the quoted speech span",
			})
		}
	} else {
		if spec.DeliveryPreset == "" {
			evaluation.Spec.DeliveryPreset = DefaultDeliveryPreset
		}
		preset, ok := deliveryPresets[evaluation.Spec.DeliveryPreset]
		if !ok {
			return PromptEvaluation{}, requestErrorf("unknown DramaBox delivery preset %q", evaluation.Spec.DeliveryPreset)
		}
		evaluation.DeliveryText = preset
	}
	lintPrompt(&evaluation, source)
	normalizedSource := normalizePromptQuotes(source)
	if normalizedSource != source {
		evaluation.Normalizations = append(evaluation.Normalizations, PromptNormalization{
			Field: "sourceText", Original: source, Normalized: normalizedSource,
			Reason: "changed double-quote punctuation to apostrophes so source stays inside one quoted speech span",
		})
	}
	if source != "" {
		separator := " "
		if evaluation.Spec.DeliveryPreset == "advanced" {
			separator = ", "
		}
		evaluation.GeneratedPrompt = evaluation.Spec.SpeakerPhrase + separator + evaluation.DeliveryText + ` "` + normalizedSource + `"`
	}
	return evaluation, nil
}

func BuildStructuredDramaBoxPrompt(spec DramaBoxPromptSpec, source string) (string, error) {
	evaluation, err := EvaluateDramaBoxPrompt(spec, source)
	if err != nil {
		return "", err
	}
	return evaluation.GeneratedPrompt, nil
}

// PreviewDramaBoxPromptSections applies the real source-range policy without
// allocating seeds or durable work, then renders exactly what each native call
// will receive.
func PreviewDramaBoxPromptSections(spec DramaBoxPromptSpec, source string) ([]PromptSectionPreview, error) {
	if source == "" {
		return []PromptSectionPreview{}, nil
	}
	ranges, err := buildDramaBoxRanges(source)
	if err != nil {
		return nil, err
	}
	previews := make([]PromptSectionPreview, 0, len(ranges))
	for _, sourceRange := range ranges {
		prompt, err := BuildStructuredDramaBoxPrompt(spec, source[sourceRange.start:sourceRange.end])
		if err != nil {
			return nil, err
		}
		previews = append(previews, PromptSectionPreview{StartByte: int64(sourceRange.start), EndByte: int64(sourceRange.end), GeneratedPrompt: prompt})
	}
	return previews, nil
}

func ensurePromptComma(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, ".;:,")
	return value + ","
}

func lintPrompt(evaluation *PromptEvaluation, source string) {
	direction := strings.ToLower(evaluation.DeliveryText)
	for _, role := range []string{"radio host", "teacher", "detective", "nurse", "narrator"} {
		if strings.Contains(direction, role) {
			evaluation.Warnings = append(evaluation.Warnings, PromptWarning{Code: "role-noun", Message: fmt.Sprintf("%q can be spoken aloud; use delivery language instead of a role", role)})
		}
	}
	if evaluation.Spec.DeliveryPreset == "advanced" && (strings.Count(evaluation.DeliveryText, ",") >= 5 || len(strings.Fields(evaluation.DeliveryText)) > 24) {
		evaluation.Warnings = append(evaluation.Warnings, PromptWarning{Code: "stacked-description", Message: "The delivery description is long or adjective-heavy and may reduce prompt reliability."})
	}
	combined := strings.ToLower(evaluation.DeliveryText + " " + source)
	for _, cue := range []string{"[laugh", "(laugh", "hahaha", "hehehe", "[sigh", "(sigh", "[gasp", "(gasp"} {
		if strings.Contains(combined, cue) {
			evaluation.Warnings = append(evaluation.Warnings, PromptWarning{Code: "paralinguistic-cue", Message: "Paralinguistic spellings or actions can become audible in factual narration."})
			break
		}
	}
}
