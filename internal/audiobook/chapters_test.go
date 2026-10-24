package audiobook

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExtractDocumentChapters(t *testing.T) {
	for _, tc := range []struct {
		name, filename, input string
		titles                []string
		starts, ends          []int
	}{
		{"plain", "book.txt", "One story.", []string{"Chapter 1"}, []int{0}, []int{10}},
		{"unicode and front matter", "book.md", "Foreword.\n\n# Café\nFirst.\n\n# End\nLast.", []string{"Introduction", "Café", "End"}, []int{0, 11, 27}, []int{11, 27, 38}},
		{"normalised BOM CRLF", "book.md", "\ufeff# One\r\nText.", []string{"One"}, []int{0}, []int{11}},
		{"fenced headings", "book.md", "```\n# Not a chapter\n```\n# Real\nText.", []string{"Introduction", "Real"}, nil, nil},
		{"tilde fence", "book.md", "~~~text\n# Not a chapter\n~~~\n# Real\nText.", []string{"Introduction", "Real"}, nil, nil},
		{"indented delimiter is code", "book.md", "Intro.\n\n    ```\n\n# Real Chapter\nNarratable words.", []string{"Introduction", "Real Chapter"}, nil, nil},
		{"tabbed delimiter is code", "book.md", "Intro.\n\n\t```\n\n# Real Chapter\nNarratable words.", []string{"Introduction", "Real Chapter"}, nil, nil},
		{"blank prefix and closing hashes", "book.md", "\n\n# Title ###\nText.", []string{"Title"}, []int{0}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ExtractDocument(tc.filename, []byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			var titles []string
			end := 0
			for index, chapter := range doc.Chapters {
				titles = append(titles, chapter.Title)
				if chapter.StartByte != end || chapter.EndByte <= chapter.StartByte || chapter.EndByte > len(doc.Text) || !utf8.ValidString(doc.Text[chapter.StartByte:chapter.EndByte]) {
					t.Fatalf("invalid range %+v in %q", chapter, doc.Text)
				}
				if chapter.ID == "" || (index > 0 && chapter.ID == doc.Chapters[index-1].ID) {
					t.Fatal("unstable chapter identity")
				}
				if tc.starts != nil && chapter.StartByte != tc.starts[index] {
					t.Fatalf("chapter %d start=%d, want %d", index, chapter.StartByte, tc.starts[index])
				}
				if tc.ends != nil && chapter.EndByte != tc.ends[index] {
					t.Fatalf("chapter %d end=%d, want %d", index, chapter.EndByte, tc.ends[index])
				}
				end = chapter.EndByte
			}
			if !reflect.DeepEqual(titles, tc.titles) || end != len(doc.Text) {
				t.Fatalf("chapters %v, want %v; end %d/%d", titles, tc.titles, end, len(doc.Text))
			}
			legacy, err := Extract(tc.filename, []byte(tc.input))
			if err != nil || doc.Text != legacy {
				t.Fatalf("canonical source changed: %q vs %q (%v)", doc.Text, legacy, err)
			}
		})
	}
	for _, invalid := range [][]byte{nil, []byte("  \n"), {0xff, 0xfe}} {
		if _, err := ExtractDocument("book.txt", invalid); err == nil {
			t.Fatal("invalid document accepted")
		}
	}
}

func TestExtractDocumentEPUBKeepsSpineChaptersAndUntitledText(t *testing.T) {
	epub := buildEPUB(t, map[string]string{
		"first":  `<html><head><title>Ignored title</title></head><body><h1>Café &amp; sea</h1><p>First words.</p></body></html>`,
		"second": `<html><body><p>Untitled ending.</p></body></html>`,
	}, []string{"second", "first"})
	doc, err := ExtractDocument("book.epub", epub)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Chapters) != 2 || doc.Chapters[0].Title != "Chapter 1" || doc.Chapters[1].Title != "Café & sea" {
		t.Fatalf("chapters %+v", doc.Chapters)
	}
	if doc.Text != "Untitled ending.\n\nCafé & sea\n\nFirst words." {
		t.Fatal(doc.Text)
	}
	if doc.Chapters[0].StartByte != 0 || doc.Chapters[0].EndByte != doc.Chapters[1].StartByte || doc.Chapters[1].EndByte != len(doc.Text) {
		t.Fatal(doc.Chapters)
	}
	if !strings.Contains(doc.Text[doc.Chapters[1].StartByte:doc.Chapters[1].EndByte], "First words.") {
		t.Fatal("chapter source lost")
	}
	legacy, err := Extract("book.epub", epub)
	if err != nil || legacy != doc.Text {
		t.Fatal("legacy extraction changed", err)
	}
}
