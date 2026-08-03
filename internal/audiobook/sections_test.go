package audiobook

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPlanDramaBoxSectionsPacksParagraphsAndAssignsMetadata(t *testing.T) {
	first := repeatedWords("alpha", 90)
	second := repeatedWords("bravo", 90)
	third := repeatedWords("charlie", 20)
	source := first + "\n\n" + second + "\n\n" + third
	entropy := append([]byte{1, 2, 3, 4, 5, 6, 7, 8}, bytes.Repeat([]byte{0xff}, 8)...)

	sections, err := planDramaBoxSections(source, bytes.NewReader(entropy))
	if err != nil {
		t.Fatalf("plan DramaBox sections: %v", err)
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %d, want 2: %#v", len(sections), sections)
	}

	firstEnd := len(first) + 2 + len(second)
	wantRanges := [][2]int64{{0, int64(firstEnd)}, {int64(firstEnd + 2), int64(len(source))}}
	wantSeeds := []Seed{0x05060708, 0x7fffffff}
	for i, section := range sections {
		wantID := fmt.Sprintf("section-%04d", i+1)
		if section.ID != wantID {
			t.Errorf("section %d id = %q, want %q", i, section.ID, wantID)
		}
		if section.StartByte != wantRanges[i][0] || section.EndByte != wantRanges[i][1] {
			t.Errorf("section %d range = [%d,%d), want [%d,%d)", i, section.StartByte, section.EndByte, wantRanges[i][0], wantRanges[i][1])
		}
		text := source[section.StartByte:section.EndByte]
		sum := sha256.Sum256([]byte(text))
		if section.TextSHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("section %d hash = %q, want hash of exact source slice", i, section.TextSHA256)
		}
		if section.Seed != wantSeeds[i] {
			t.Errorf("section %d seed = %d, want %d", i, section.Seed, wantSeeds[i])
		}
		if section.Status != SectionStatusPending {
			t.Errorf("section %d status = %q, want pending", i, section.Status)
		}
	}
}

func TestPlanDramaBoxSectionsSplitsOversizedParagraphAtSentences(t *testing.T) {
	source := repeatedWords("first", 100) + " " + repeatedWords("second", 100) + " " + repeatedWords("third", 100)

	sections, err := planDramaBoxSections(source, bytes.NewReader(make([]byte, 24)))
	if err != nil {
		t.Fatalf("plan DramaBox sections: %v", err)
	}
	if len(sections) != 3 {
		t.Fatalf("sections = %d, want one per whole sentence: %#v", len(sections), sections)
	}
	for i, section := range sections {
		text := source[section.StartByte:section.EndByte]
		if got := len(strings.Fields(text)); got != 100 {
			t.Errorf("section %d words = %d, want 100", i, got)
		}
		if !strings.HasSuffix(text, ".") {
			t.Errorf("section %d did not preserve sentence boundary: %q", i, text)
		}
	}
}

func TestPlanDramaBoxSectionsFallsBackToRuneSafeWordBoundaries(t *testing.T) {
	source := repeatedWords("café", 600)

	sections, err := planDramaBoxSections(source, bytes.NewReader(make([]byte, 8*4)))
	if err != nil {
		t.Fatalf("plan DramaBox sections: %v", err)
	}
	if len(sections) < 3 {
		t.Fatalf("sections = %d, want multiple word-bounded sections", len(sections))
	}
	var gotWords []string
	for i, section := range sections {
		text := source[section.StartByte:section.EndByte]
		if !utf8.ValidString(text) {
			t.Fatalf("section %d split a UTF-8 encoding: %q", i, text)
		}
		if got := len(strings.Fields(text)); got > dramaBoxHardMaxWords {
			t.Errorf("section %d words = %d, hard max = %d", i, got, dramaBoxHardMaxWords)
		}
		gotWords = append(gotWords, strings.Fields(text)...)
	}
	if wantWords := strings.Fields(source); !reflect.DeepEqual(gotWords, wantWords) {
		t.Fatalf("planned words changed or were dropped: got %d, want %d", len(gotWords), len(wantWords))
	}
}

func TestPlanDramaBoxSectionsRejectsPartialEntropyWithoutPartialPlan(t *testing.T) {
	sections, err := planDramaBoxSections("A fact.", bytes.NewReader(make([]byte, 7)))
	if err == nil || !strings.Contains(err.Error(), "assign seed for section 1") {
		t.Fatalf("expected section seed error, got %v", err)
	}
	if sections != nil {
		t.Fatalf("entropy failure returned a partial plan: %#v", sections)
	}

	sections, err = planDramaBoxSections(" \r\n\r\n ", bytes.NewReader(nil))
	if err != nil || len(sections) != 0 {
		t.Fatalf("empty source plan = %#v, %v; want no sections and no entropy read", sections, err)
	}
}

func TestPlanDramaBoxSectionsUsesUnicodeWhitespaceConsistently(t *testing.T) {
	for _, separator := range []string{"\u00a0", "\u2003"} {
		separator := separator
		t.Run(fmt.Sprintf("U+%04X", []rune(separator)[0]), func(t *testing.T) {
			source := repeatedWordsWithSeparator("café", 600, separator)
			sections, err := planDramaBoxSections(source, bytes.NewReader(make([]byte, 8*4)))
			if err != nil {
				t.Fatalf("plan DramaBox sections: %v", err)
			}

			var gotWords []string
			for i, section := range sections {
				text := source[section.StartByte:section.EndByte]
				if got := countWordsUpTo(text, sourceRange{end: len(text)}, dramaBoxHardMaxWords); got > dramaBoxHardMaxWords {
					t.Errorf("section %d words = %d, hard max = %d", i, got, dramaBoxHardMaxWords)
				}
				gotWords = append(gotWords, strings.Fields(text)...)
			}
			if wantWords := strings.Fields(source); !reflect.DeepEqual(gotWords, wantWords) {
				t.Fatalf("planned Unicode-separated words changed or were dropped: got %d, want %d", len(gotWords), len(wantWords))
			}
		})
	}
}

func TestPlanDramaBoxSectionsHonorsExactWordBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		wantChunks int
	}{
		{
			name:       "target inclusive",
			source:     repeatedWords("alpha", 100) + "\n\n" + repeatedWords("bravo", dramaBoxTargetWords-100),
			wantChunks: 1,
		},
		{
			name:       "target exceeded",
			source:     repeatedWords("alpha", 100) + "\n\n" + repeatedWords("bravo", dramaBoxTargetWords-99),
			wantChunks: 2,
		},
		{
			name:       "hard maximum inclusive",
			source:     repeatedWords("alpha", dramaBoxHardMaxWords),
			wantChunks: 1,
		},
		{
			name:       "hard maximum exceeded",
			source:     repeatedWords("alpha", dramaBoxHardMaxWords+1),
			wantChunks: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sections, err := planDramaBoxSections(test.source, bytes.NewReader(make([]byte, 8*test.wantChunks)))
			if err != nil {
				t.Fatalf("plan DramaBox sections: %v", err)
			}
			if len(sections) != test.wantChunks {
				t.Fatalf("sections = %d, want %d", len(sections), test.wantChunks)
			}
		})
	}
}

func TestPlanDramaBoxSectionsSplitsUnbrokenTokensAtRuneSafeByteLimit(t *testing.T) {
	source := strings.Repeat("é", dramaBoxHardMaxBytes)
	sections, err := planDramaBoxSections(source, bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatalf("plan DramaBox sections: %v", err)
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(sections))
	}
	for i, section := range sections {
		text := source[section.StartByte:section.EndByte]
		if len(text) > dramaBoxHardMaxBytes {
			t.Errorf("section %d bytes = %d, hard max = %d", i, len(text), dramaBoxHardMaxBytes)
		}
		if !utf8.ValidString(text) {
			t.Errorf("section %d is not valid UTF-8", i)
		}
	}
	if got := source[sections[0].StartByte:sections[0].EndByte] + source[sections[1].StartByte:sections[1].EndByte]; got != source {
		t.Fatal("unbroken token changed or lost bytes")
	}
}

func TestPlanDramaBoxSectionsRejectsTooManySectionsBeforeReadingEntropy(t *testing.T) {
	source := repeatedWords("word", (MaxChunks+1)*dramaBoxTargetWords)
	sections, err := planDramaBoxSections(source, bytes.NewReader(nil))
	if err == nil || !IsRequestError(err) || !strings.Contains(err.Error(), "more than 600") {
		t.Fatalf("expected bounded section-limit request error, got %v", err)
	}
	if sections != nil {
		t.Fatalf("section-limit failure returned a partial plan: %#v", sections)
	}
}

func TestDramaBoxWordCountStopsAfterHardLimit(t *testing.T) {
	source := repeatedWords("word", 10_000)
	if got := countWordsUpTo(source, sourceRange{end: len(source)}, dramaBoxHardMaxWords); got != dramaBoxHardMaxWords+1 {
		t.Fatalf("bounded word count = %d, want %d", got, dramaBoxHardMaxWords+1)
	}
}

func repeatedWords(word string, count int) string {
	return strings.TrimSpace(strings.Repeat(word+" ", count)) + "."
}

func repeatedWordsWithSeparator(word string, count int, separator string) string {
	return strings.TrimSuffix(strings.Repeat(word+separator, count), separator) + "."
}
