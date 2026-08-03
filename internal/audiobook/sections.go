package audiobook

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// CurrentSectionPolicyVersion changes whenever the stable DramaBox source
	// partitioning rules change.
	CurrentSectionPolicyVersion = 2

	dramaBoxWordsPerMinute = 150
	dramaBoxTargetSeconds  = 75
	dramaBoxHardMaxSeconds = 110
	dramaBoxTargetWords    = dramaBoxWordsPerMinute * dramaBoxTargetSeconds / 60
	dramaBoxHardMaxWords   = dramaBoxWordsPerMinute * dramaBoxHardMaxSeconds / 60

	// Normal English reaches the word cap long before this guard. The byte cap
	// bounds pathological unbroken tokens without changing ordinary planning.
	dramaBoxHardMaxBytes = 32 * 1024
)

var (
	paragraphBreakRE = regexp.MustCompile(`(\r?\n[ \t]*\r?\n)+`)
	// This expression is owned by section policy v1. It intentionally does not
	// share the legacy Chunk regex, whose changes must not alter durable ranges.
	dramaBoxSentenceEndRE = regexp.MustCompile(`([.!?]["')\]]?)\s+`)
	errWordCountLimit     = errors.New("word count limit reached")
)

type sourceRange struct {
	start int
	end   int
}

// PlanDramaBoxSections partitions the exact canonical source into resumable
// app-level sections and assigns each section one cryptographically random
// seed. Ranges are half-open byte offsets into canonicalSource.
func PlanDramaBoxSections(canonicalSource string) ([]Section, error) {
	return planDramaBoxSections(canonicalSource, rand.Reader)
}

func planDramaBoxSections(canonicalSource string, entropy io.Reader) ([]Section, error) {
	ranges, err := buildDramaBoxRanges(canonicalSource)
	if err != nil {
		return nil, err
	}
	sections := make([]Section, 0, len(ranges))
	for i, sourceRange := range ranges {
		seed, err := readSectionSeed(entropy)
		if err != nil {
			return nil, fmt.Errorf("assign seed for section %d: %w", i+1, err)
		}
		text := canonicalSource[sourceRange.start:sourceRange.end]
		sum := sha256.Sum256([]byte(text))
		sections = append(sections, Section{
			ID:         fmt.Sprintf("section-%04d", i+1),
			StartByte:  int64(sourceRange.start),
			EndByte:    int64(sourceRange.end),
			TextSHA256: hex.EncodeToString(sum[:]),
			Seed:       seed,
			Status:     SectionStatusPending,
		})
	}
	return sections, nil
}

func readSectionSeed(entropy io.Reader) (Seed, error) {
	seedBytes := make([]byte, 8)
	if _, err := io.ReadFull(entropy, seedBytes); err != nil {
		return 0, err
	}
	// DramaBox release-0.5 parses its seed with parse_int_option. Keep
	// cryptographic entropy inside the positive signed 32-bit domain that the
	// native runtime actually accepts.
	seed := Seed(binary.BigEndian.Uint64(seedBytes) & math.MaxInt32)
	if seed == 0 {
		seed = 1
	}
	return seed, nil
}

type rangePacker struct {
	packed       []sourceRange
	current      sourceRange
	currentWords int
}

func (p *rangePacker) add(unit sourceRange, words int) error {
	if p.current.end == 0 {
		p.current = unit
		p.currentWords = words
		return nil
	}
	if p.currentWords+words <= dramaBoxTargetWords && unit.end-p.current.start <= dramaBoxHardMaxBytes {
		p.current.end = unit.end
		p.currentWords += words
		return nil
	}
	if err := p.flush(); err != nil {
		return err
	}
	p.current = unit
	p.currentWords = words
	return nil
}

func (p *rangePacker) flush() error {
	if p.current.end == 0 {
		return nil
	}
	p.packed = append(p.packed, p.current)
	p.current = sourceRange{}
	p.currentWords = 0
	if len(p.packed) > MaxChunks {
		return requestErrorf("document needs more than %d DramaBox sections; narrate it in parts", MaxChunks)
	}
	return nil
}

func buildDramaBoxRanges(source string) ([]sourceRange, error) {
	if !utf8.ValidString(source) {
		return nil, requestErrorf("document is not valid UTF-8 text")
	}
	packer := rangePacker{}
	err := forEachParagraphRange(source, func(paragraph sourceRange) error {
		words := countWordsUpTo(source, paragraph, dramaBoxHardMaxWords)
		if withinDramaBoxHardLimits(paragraph, words) {
			return packer.add(paragraph, words)
		}
		return forEachSentenceRange(source, paragraph, func(sentence sourceRange) error {
			words := countWordsUpTo(source, sentence, dramaBoxHardMaxWords)
			if withinDramaBoxHardLimits(sentence, words) {
				return packer.add(sentence, words)
			}
			return forEachWordRange(source, sentence, func(word sourceRange) error {
				return forEachBoundedRuneRange(source, word, func(fragment sourceRange) error {
					return packer.add(fragment, 1)
				})
			})
		})
	})
	if err != nil {
		return nil, err
	}
	if err := packer.flush(); err != nil {
		return nil, err
	}
	return packer.packed, nil
}

func withinDramaBoxHardLimits(bounds sourceRange, words int) bool {
	return words <= dramaBoxHardMaxWords && bounds.end-bounds.start <= dramaBoxHardMaxBytes
}

func forEachParagraphRange(source string, visit func(sourceRange) error) error {
	start := 0
	for start < len(source) {
		match := paragraphBreakRE.FindStringIndex(source[start:])
		if match == nil {
			break
		}
		if sourceRange, ok := trimSourceRange(source, start, start+match[0]); ok {
			if err := visit(sourceRange); err != nil {
				return err
			}
		}
		start += match[1]
	}
	if sourceRange, ok := trimSourceRange(source, start, len(source)); ok {
		return visit(sourceRange)
	}
	return nil
}

func trimSourceRange(source string, start, end int) (sourceRange, bool) {
	trimmed := strings.TrimSpace(source[start:end])
	if trimmed == "" {
		return sourceRange{}, false
	}
	prefix := strings.Index(source[start:end], trimmed)
	start += prefix
	return sourceRange{start: start, end: start + len(trimmed)}, true
}

func forEachSentenceRange(source string, paragraph sourceRange, visit func(sourceRange) error) error {
	start := paragraph.start
	for start < paragraph.end {
		match := dramaBoxSentenceEndRE.FindStringIndex(source[start:paragraph.end])
		if match == nil {
			break
		}
		if sourceRange, ok := trimSourceRange(source, start, start+match[1]); ok {
			if err := visit(sourceRange); err != nil {
				return err
			}
		}
		start += match[1]
	}
	if sourceRange, ok := trimSourceRange(source, start, paragraph.end); ok {
		return visit(sourceRange)
	}
	return nil
}

func countWordsUpTo(source string, bounds sourceRange, limit int) int {
	count := 0
	_ = forEachWordRange(source, bounds, func(sourceRange) error {
		count++
		if count > limit {
			return errWordCountLimit
		}
		return nil
	})
	return count
}

func forEachWordRange(source string, bounds sourceRange, visit func(sourceRange) error) error {
	wordStart := -1
	for offset := bounds.start; offset < bounds.end; {
		r, size := utf8.DecodeRuneInString(source[offset:bounds.end])
		if unicode.IsSpace(r) {
			if wordStart >= 0 {
				if err := visit(sourceRange{start: wordStart, end: offset}); err != nil {
					return err
				}
				wordStart = -1
			}
		} else if wordStart < 0 {
			wordStart = offset
		}
		offset += size
	}
	if wordStart >= 0 {
		return visit(sourceRange{start: wordStart, end: bounds.end})
	}
	return nil
}

func forEachBoundedRuneRange(source string, bounds sourceRange, visit func(sourceRange) error) error {
	start := bounds.start
	for start < bounds.end {
		end := start
		for end < bounds.end {
			_, size := utf8.DecodeRuneInString(source[end:bounds.end])
			if end > start && end+size-start > dramaBoxHardMaxBytes {
				break
			}
			end += size
		}
		if err := visit(sourceRange{start: start, end: end}); err != nil {
			return err
		}
		start = end
	}
	return nil
}
