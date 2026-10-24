package audiobook

import (
	"cpp-studio/internal/pronunciation"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DocumentPlan freezes editorial chapter identity alongside the resolved
// narration settings. It is owned by production, not by the upload handler.
type DocumentPlan struct {
	Digest                    string    `json:"digest"`
	ExtractionPolicyVersion   int       `json:"extractionPolicyVersion"`
	Chapters                  []Chapter `json:"chapters"`
	WordCount                 int       `json:"wordCount"`
	EstimatedListeningSeconds int       `json:"estimatedListeningSeconds"`
}

func cloneDocumentPlan(plan *DocumentPlan) *DocumentPlan {
	if plan == nil {
		return nil
	}
	copied := *plan
	copied.Chapters = append([]Chapter(nil), plan.Chapters...)
	return &copied
}

func validateDocumentPlan(text string, plan *DocumentPlan) error {
	if plan == nil {
		return nil
	}
	if plan.ExtractionPolicyVersion != ChapterExtractionPolicyVersion || len(plan.Chapters) == 0 || len(plan.Chapters) > MaxChunks {
		return fmt.Errorf("unsupported or oversized chapter plan")
	}
	end := 0
	for index, chapter := range plan.Chapters {
		if chapter.ID != fmt.Sprintf("chapter_%03d", index+1) || strings.TrimSpace(chapter.Title) == "" || chapter.StartByte != end || chapter.EndByte <= chapter.StartByte || chapter.EndByte > len(text) || !utf8.ValidString(text[chapter.StartByte:chapter.EndByte]) {
			return fmt.Errorf("invalid source range for chapter %d", index+1)
		}
		end = chapter.EndByte
	}
	if end != len(text) {
		return fmt.Errorf("chapter plan does not cover the canonical source")
	}
	return nil
}

func makeDocumentPlan(req Request, resolvedEngine EngineIdentity, resolvedVoice VoiceIdentity) (*DocumentPlan, error) {
	if req.documentPlan == nil {
		if req.Pronunciation != nil {
			if _, err := planCorrectedSections(req, nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if err := validateDocumentPlan(req.Text, req.documentPlan); err != nil {
		return nil, err
	}
	var corrected []Section
	if req.Pronunciation != nil {
		var err error
		corrected, err = planCorrectedSections(req, nil)
		if err != nil {
			return nil, err
		}
	} else if req.EngineID == DramaBoxEngineID {
		if _, err := documentSectionRanges(req.Text, req.documentPlan); err != nil {
			return nil, err
		}
	} else {
		units := 0
		for _, chapter := range req.documentPlan.Chapters {
			units += len(chapterTextChunks(req.Text[chapter.StartByte:chapter.EndByte], DefaultChunkChars))
		}
		if units > MaxChunks {
			return nil, requestErrorf("document needs more than %d chunks; narrate it in parts", MaxChunks)
		}
	}
	plan := cloneDocumentPlan(req.documentPlan)
	basis := cloneDocumentPlan(plan)
	basis.Digest = ""
	basis.WordCount = 0
	basis.EstimatedListeningSeconds = 0
	for index := range basis.Chapters {
		chapter := &basis.Chapters[index]
		chapter.WordCount = 0
		chapter.EstimatedListeningSeconds = 0
		chapter.Excerpt = ""
		chapter.SpokenExcerpt = ""
		chapter.PronunciationPreview = nil
	}
	baseRequest := req
	baseRequest.documentPlan = nil
	base := buildSynthesisIdentity(baseRequest, resolvedEngine, resolvedVoice)
	encoded, _ := json.Marshal(struct {
		Identity string
		Plan     *DocumentPlan
	}{base.Fingerprint, basis})
	sum := sha256.Sum256(encoded)
	plan.Digest = hex.EncodeToString(sum[:])
	plan.WordCount = 0
	for index := range plan.Chapters {
		chapter := &plan.Chapters[index]
		source := req.Text[chapter.StartByte:chapter.EndByte]
		chapter.WordCount = len(strings.Fields(source))
		chapter.EstimatedListeningSeconds = (chapter.WordCount*60 + 149) / 150
		if req.EngineID == DramaBoxEngineID {
			ranges, err := buildDramaBoxRanges(source)
			if err != nil {
				return nil, err
			}
			if len(ranges) > 0 {
				passage := []rune(source[ranges[0].start:ranges[0].end])
				if len(passage) > DefaultChunkChars {
					cut := DefaultChunkChars
					for index := cut - 1; index > 0; index-- {
						if unicode.IsSpace(passage[index]) {
							cut = index
							break
						}
					}
					passage = passage[:cut]
				}
				chapter.Excerpt = strings.TrimSpace(string(passage))
			}
		} else if chunks := chapterTextChunks(source, DefaultChunkChars); len(chunks) > 0 {
			chapter.Excerpt = chunks[0]
		}
		if req.Pronunciation != nil {
			for _, section := range corrected {
				if section.ChapterID != chapter.ID {
					continue
				}
				// Audition uses an atomic prefix of the actual first production unit.
				resolution, err := pronunciation.ResolveDetailed(source, req.Language, req.Pronunciation.Rules)
				if err != nil {
					return nil, err
				}
				first := slicePronunciation(resolution, int(section.StartByte)-chapter.StartByte, int(section.EndByte)-chapter.StartByte)
				short, err := partitionCorrectedResolution(first, int(section.StartByte), chapter.ID, DefaultEngineID)
				if err != nil {
					return nil, err
				}
				if len(short) > 0 {
					preview := slicePronunciation(resolution, int(short[0].StartByte)-chapter.StartByte, int(short[0].EndByte)-chapter.StartByte)
					chapter.Excerpt, chapter.SpokenExcerpt, chapter.PronunciationPreview = preview.Source, preview.Spoken, &preview
				}
				break
			}
		}
		plan.WordCount += chapter.WordCount
	}
	plan.EstimatedListeningSeconds = (plan.WordCount*60 + 149) / 150
	return plan, nil
}

func slicePronunciation(result pronunciation.Resolution, start, end int) pronunciation.Resolution {
	spokenStart, spokenEnd := start, end
	for _, span := range result.Substitutions {
		delta := (span.SpokenEnd - span.SpokenStart) - (span.SourceEnd - span.SourceStart)
		if span.SourceEnd <= start {
			spokenStart += delta
		}
		if span.SourceEnd <= end {
			spokenEnd += delta
		}
	}
	preview := pronunciation.Resolution{Source: result.Source[start:end], Spoken: result.Spoken[spokenStart:spokenEnd], Substitutions: []pronunciation.Substitution{}}
	for _, span := range result.Substitutions {
		if span.SourceStart >= start && span.SourceEnd <= end {
			span.SourceStart -= start
			span.SourceEnd -= start
			span.SpokenStart -= spokenStart
			span.SpokenEnd -= spokenStart
			preview.Substitutions = append(preview.Substitutions, span)
		}
	}
	return preview
}

// New chaptered books keep the legacy sentence/paragraph policy while applying
// its size budget to runes, so hard cuts never create invalid UTF-8.
func chapterTextChunks(text string, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = DefaultChunkChars
	}
	var chunks []string
	current := ""
	flush := func() {
		if strings.TrimSpace(current) != "" {
			chunks = append(chunks, strings.TrimSpace(current))
		}
		current = ""
	}
	for _, paragraph := range strings.Split(text, "\n\n") {
		paragraph = strings.TrimSpace(strings.ReplaceAll(paragraph, "\n", " "))
		for _, sentence := range splitSentences(paragraph) {
			runes := []rune(sentence)
			for len(runes) > maxRunes {
				cut := maxRunes
				for index := maxRunes - 1; index > 0; index-- {
					if unicode.IsSpace(runes[index]) {
						cut = index
						break
					}
				}
				flush()
				chunks = append(chunks, strings.TrimSpace(string(runes[:cut])))
				runes = []rune(strings.TrimSpace(string(runes[cut:])))
			}
			sentence = string(runes)
			if current != "" && utf8.RuneCountInString(current)+utf8.RuneCountInString(sentence)+1 > maxRunes {
				flush()
			}
			if current != "" {
				current += " "
			}
			current += sentence
		}
		flush()
	}
	return chunks
}

func documentSectionRanges(text string, plan *DocumentPlan) ([]sourceRange, error) {
	if plan == nil {
		return buildDramaBoxRanges(text)
	}
	if err := validateDocumentPlan(text, plan); err != nil {
		return nil, err
	}
	var ranges []sourceRange
	for _, chapter := range plan.Chapters {
		local, err := buildDramaBoxRanges(text[chapter.StartByte:chapter.EndByte])
		if err != nil {
			return nil, err
		}
		for _, bounds := range local {
			ranges = append(ranges, sourceRange{start: bounds.start + chapter.StartByte, end: bounds.end + chapter.StartByte})
		}
		if len(ranges) > MaxChunks {
			return nil, requestErrorf("document needs more than %d DramaBox sections; narrate it in parts", MaxChunks)
		}
	}
	return ranges, nil
}

func chapterIDForRange(plan *DocumentPlan, start, end int) string {
	if plan != nil {
		for _, chapter := range plan.Chapters {
			if start >= chapter.StartByte && end <= chapter.EndByte {
				return chapter.ID
			}
		}
	}
	return ""
}
