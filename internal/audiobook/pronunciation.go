package audiobook

import (
	"cpp-studio/internal/pronunciation"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

func spokenSection(source string, section Section) string {
	if section.SpokenSHA256 != "" {
		return section.SpokenText
	}
	return source[section.StartByte:section.EndByte]
}
func spokenHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Corrected units are partitioned after one whole-chapter resolution. Matched
// phrases and replacements are atomic; byte ranges always refer to the source.
func correctedChapterSections(source string, offset int, chapterID string, req Request) ([]Section, error) {
	result, err := pronunciation.ResolveDetailed(source, req.Language, req.Pronunciation.Rules)
	if err != nil {
		return nil, err
	}
	return partitionCorrectedResolution(result, offset, chapterID, req.EngineID)
}

func partitionCorrectedResolution(result pronunciation.Resolution, offset int, chapterID, engineID string) ([]Section, error) {
	source := result.Source
	if len(result.Spoken) > 32<<20 {
		return nil, requestErrorf("resolved chapter exceeds 32 MiB; narrate it in parts")
	}
	var sections []Section
	type cursor struct{ s, p, match int }
	start, pos, lastBreak := cursor{}, cursor{}, cursor{}
	runes, words, bytesCount := 0, 0, 0
	inWord := false
	emit := func(end cursor) error {
		original, spoken := source[start.s:end.s], result.Spoken[start.p:end.p]
		if strings.TrimSpace(spoken) != "" {
			sections = append(sections, Section{ChapterID: chapterID, StartByte: int64(offset + start.s), EndByte: int64(offset + end.s), TextSHA256: spokenHash(original), SpokenText: spoken, SpokenSHA256: spokenHash(spoken), Status: SectionStatusPending})
		}
		if len(sections) > MaxChunks {
			return requestErrorf("resolved document needs more than %d sections", MaxChunks)
		}
		start, pos, lastBreak = end, end, end
		runes, words, bytesCount = 0, 0, 0
		inWord = false
		return nil
	}
	for pos.s < len(source) {
		next := pos
		if pos.match < len(result.Substitutions) && result.Substitutions[pos.match].SourceStart == pos.s {
			span := result.Substitutions[pos.match]
			next.s, next.p = span.SourceEnd, span.SpokenEnd
			next.match++
		} else {
			_, size := utf8.DecodeRuneInString(source[pos.s:])
			next.s += size
			next.p += size
		}
		atom := result.Spoken[pos.p:next.p]
		addRunes, addWords := 0, 0
		nextInWord := inWord
		for _, r := range atom {
			addRunes++
			if unicode.IsSpace(r) {
				nextInWord = false
			} else {
				if !nextInWord {
					addWords++
				}
				nextInWord = true
			}
		}
		tooLarge := runes+addRunes > DefaultChunkChars
		if engineID == DramaBoxEngineID {
			tooLarge = words+addWords > dramaBoxTargetWords || bytesCount+len(atom) > dramaBoxHardMaxBytes
		}
		if tooLarge {
			if pos.s == start.s {
				return nil, requestErrorf("pronunciation replacement exceeds the narrator limit")
			}
			end := pos
			if lastBreak.s > start.s {
				end = lastBreak
			}
			if err := emit(end); err != nil {
				return nil, err
			}
			continue
		}
		runes += addRunes
		words += addWords
		bytesCount += len(atom)
		inWord = nextInWord
		pos = next
		if !inWord {
			lastBreak = pos
		}
	}
	if pos.s > start.s {
		if err := emit(pos); err != nil {
			return nil, err
		}
	}
	return sections, nil
}

func planCorrectedSections(req Request, entropy io.Reader) ([]Section, error) {
	chapters := []Chapter{{StartByte: 0, EndByte: len(req.Text)}}
	if req.documentPlan != nil {
		if err := validateDocumentPlan(req.Text, req.documentPlan); err != nil {
			return nil, err
		}
		chapters = req.documentPlan.Chapters
	}
	var sections []Section
	for _, chapter := range chapters {
		local, err := correctedChapterSections(req.Text[chapter.StartByte:chapter.EndByte], chapter.StartByte, chapter.ID, req)
		if err != nil {
			return nil, err
		}
		sections = append(sections, local...)
		if len(sections) > MaxChunks {
			return nil, requestErrorf("resolved document needs more than %d sections", MaxChunks)
		}
	}
	for i := range sections {
		sections[i].ID = fmt.Sprintf("section-%04d", i+1)
		if entropy != nil {
			seed, err := readSectionSeed(entropy)
			if err != nil {
				return nil, err
			}
			sections[i].Seed = seed
		}
	}
	return sections, nil
}
