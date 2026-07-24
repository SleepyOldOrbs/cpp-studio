// Package audiobook turns a document into narrated audio: extract text from
// an uploaded file, chunk it into TTS-sized pieces, speak every chunk with a
// single narrator voice, and stitch the result into one WAV. The pipeline
// runs as a tracked job with per-chunk progress and cancellation.
package audiobook

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxDocumentBytes bounds an uploaded document.
const MaxDocumentBytes = 16 * 1024 * 1024

// MaxChunks bounds how many TTS chunks one audiobook may produce; documents
// beyond this are rejected rather than silently truncated.
const MaxChunks = 600

// DefaultChunkChars is the target size of one spoken chunk: two to four
// sentences, comfortably inside the TTS engine's per-run budget.
const DefaultChunkChars = 300

// Extract pulls plain text from an uploaded document. Supported: .txt and
// .md (UTF-8), .epub (XHTML chapters in spine order). PDFs need a real
// extraction library and are rejected with a pointer to the alternatives.
func Extract(filename string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("document is empty")
	}
	if len(data) > MaxDocumentBytes {
		return "", fmt.Errorf("document is %d bytes, max is %d", len(data), MaxDocumentBytes)
	}
	switch strings.ToLower(path.Ext(filename)) {
	case ".txt", ".md":
		return extractPlainText(data)
	case ".epub":
		return extractEPUB(data)
	case ".pdf":
		return "", fmt.Errorf("PDF extraction is not supported yet; export the book as .txt or .epub")
	default:
		return "", fmt.Errorf("unsupported document type %q; use .txt, .md, or .epub", path.Ext(filename))
	}
}

func extractPlainText(data []byte) (string, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(data) {
		return "", fmt.Errorf("document is not valid UTF-8 text")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("document contains no text")
	}
	return text, nil
}

// epub structures: META-INF/container.xml names the OPF package file, whose
// manifest maps ids to chapter files and whose spine orders them.
type epubContainer struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type epubPackage struct {
	Manifest []struct {
		ID   string `xml:"id,attr"`
		Href string `xml:"href,attr"`
	} `xml:"manifest>item"`
	Spine []struct {
		IDRef string `xml:"idref,attr"`
	} `xml:"spine>itemref"`
}

func extractEPUB(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open epub: %v", err)
	}
	files := make(map[string]*zip.File, len(reader.File))
	for _, f := range reader.File {
		files[path.Clean(f.Name)] = f
	}

	containerXML, err := readZipFile(files["META-INF/container.xml"])
	if err != nil {
		return "", fmt.Errorf("epub container: %v", err)
	}
	var container epubContainer
	if err := xml.Unmarshal(containerXML, &container); err != nil {
		return "", fmt.Errorf("parse epub container: %v", err)
	}
	if len(container.Rootfiles) == 0 {
		return "", fmt.Errorf("epub has no rootfile")
	}
	opfPath := path.Clean(container.Rootfiles[0].FullPath)
	opfXML, err := readZipFile(files[opfPath])
	if err != nil {
		return "", fmt.Errorf("epub package: %v", err)
	}
	var pkg epubPackage
	if err := xml.Unmarshal(opfXML, &pkg); err != nil {
		return "", fmt.Errorf("parse epub package: %v", err)
	}
	hrefByID := make(map[string]string, len(pkg.Manifest))
	for _, item := range pkg.Manifest {
		hrefByID[item.ID] = item.Href
	}

	opfDir := path.Dir(opfPath)
	var chapters []string
	for _, ref := range pkg.Spine {
		href, ok := hrefByID[ref.IDRef]
		if !ok {
			continue
		}
		chapterPath := path.Clean(path.Join(opfDir, href))
		chapterFile, ok := files[chapterPath]
		if !ok {
			continue
		}
		raw, err := readZipFile(chapterFile)
		if err != nil {
			continue
		}
		if text := htmlToText(string(raw)); text != "" {
			chapters = append(chapters, text)
		}
	}
	if len(chapters) == 0 {
		return "", fmt.Errorf("epub spine yielded no readable chapters")
	}
	return strings.Join(chapters, "\n\n"), nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("missing entry")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, MaxDocumentBytes))
}

var (
	scriptStyleRE = regexp.MustCompile(`(?is)<(script|style|head)[^>]*>.*?</(script|style|head)>`)
	blockCloseRE  = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|blockquote|section|article)>|<br\s*/?>`)
	tagRE         = regexp.MustCompile(`(?s)<[^>]*>`)
	blankRunRE    = regexp.MustCompile(`\n{3,}`)
	spaceRunRE    = regexp.MustCompile(`[ \t]+`)
)

// htmlToText strips markup, turning block boundaries into paragraph breaks.
func htmlToText(markup string) string {
	text := scriptStyleRE.ReplaceAllString(markup, " ")
	text = blockCloseRE.ReplaceAllString(text, "\n\n")
	text = tagRE.ReplaceAllString(text, " ")
	text = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&apos;", "'").Replace(text)
	text = spaceRunRE.ReplaceAllString(text, " ")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	text = strings.Join(lines, "\n")
	text = blankRunRE.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

var sentenceEndRE = regexp.MustCompile(`([.!?]["')\]]?)\s+`)

// Chunk splits text into narration-sized pieces: paragraphs are packed whole
// when they fit, split on sentence boundaries when they don't, and hard-cut
// only when a single sentence exceeds the budget.
func Chunk(text string, maxChars int) []string {
	if maxChars <= 0 {
		maxChars = DefaultChunkChars
	}
	var chunks []string
	var current strings.Builder

	flush := func() {
		if s := strings.TrimSpace(current.String()); s != "" {
			chunks = append(chunks, s)
		}
		current.Reset()
	}

	for _, paragraph := range strings.Split(text, "\n\n") {
		paragraph = strings.TrimSpace(strings.ReplaceAll(paragraph, "\n", " "))
		if paragraph == "" {
			continue
		}
		for _, sentence := range splitSentences(paragraph) {
			for len(sentence) > maxChars {
				// A single over-budget sentence: cut at the last space
				// before the limit, or hard-cut when there is none.
				cut := strings.LastIndex(sentence[:maxChars], " ")
				if cut <= 0 {
					cut = maxChars
				}
				flush()
				chunks = append(chunks, strings.TrimSpace(sentence[:cut]))
				sentence = strings.TrimSpace(sentence[cut:])
			}
			if current.Len() > 0 && current.Len()+len(sentence)+1 > maxChars {
				flush()
			}
			if current.Len() > 0 {
				current.WriteByte(' ')
			}
			current.WriteString(sentence)
		}
		// Paragraph boundaries end a chunk so narration pauses land there.
		flush()
	}
	flush()
	return chunks
}

func splitSentences(paragraph string) []string {
	marked := sentenceEndRE.ReplaceAllString(paragraph, "$1\x00")
	parts := strings.Split(marked, "\x00")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
