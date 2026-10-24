package audiobook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cpp-studio/internal/engine"
	"cpp-studio/internal/wav"
)

var ErrExportUnavailable = errors.New("M4B export needs a configured FFmpeg with AAC encoding")

type EncodeM4BFunc func(ctx context.Context, input, output, metadata, cover string, format wav.Format) error

type M4BExportRequest struct {
	RenderID     string `json:"renderId"`
	MasterSHA256 string `json:"masterSha256"`
}

type ExportChapter struct {
	Title       string `json:"title"`
	StartSample int64  `json:"startSample"`
	EndSample   int64  `json:"endSample"`
}

type M4BExportPlan struct {
	RenderID      string          `json:"renderId"`
	MasterSHA256  string          `json:"masterSha256"`
	SampleRate    uint32          `json:"sampleRate"`
	Channels      uint16          `json:"channels"`
	Chapters      []ExportChapter `json:"chapters"`
	SingleChapter bool            `json:"singleChapter"`
	Metadata      BookMetadata    `json:"metadata"`
	Cover         *BookCover      `json:"cover,omitempty"`
	Available     bool            `json:"available"`
}

type ExportRecord struct {
	UnavailableReason string          `json:"unavailableReason,omitempty"`
	Format            string          `json:"format"`
	RenderID          string          `json:"renderId,omitempty"`
	MasterSHA256      string          `json:"masterSha256"`
	Metadata          BookMetadata    `json:"metadata"`
	Cover             *BookCover      `json:"cover,omitempty"`
	Chapters          []ExportChapter `json:"chapters"`
	SampleRate        uint32          `json:"sampleRate"`
	File              string          `json:"file"`
	URL               string          `json:"url"`
	SHA256            string          `json:"sha256"`
	Bytes             int64           `json:"bytes"`
	CreatedAt         time.Time       `json:"createdAt"`
}

func fileSHA256(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Caller holds Manager.mu so a snapshot cannot race another owner mutation.
func (m *Manager) m4bSource(ctx context.Context, id, renderID string) (M4BExportPlan, string, wav.Format, error) {
	if m.activeProductionID == id {
		return M4BExportPlan{}, "", wav.Format{}, ErrProductionActive
	}
	book, err := m.store.editableFinalBook(id)
	if err != nil {
		return M4BExportPlan{}, "", wav.Format{}, err
	}
	if renderID == "" {
		renderID = book.CurrentRenderID
	}
	file := ArtifactName
	rate := book.SampleRate
	bounds := book.ChapterBoundaries
	if len(book.RenderRevisions) > 0 {
		found := false
		for _, render := range book.RenderRevisions {
			if render.ID == renderID {
				if render.ID == "" || (render.ArtifactFile != ArtifactName && !validRenderFile(render.ArtifactFile)) {
					return M4BExportPlan{}, "", wav.Format{}, ErrStoreCorrupt
				}
				file, rate, bounds = render.ArtifactFile, render.SampleRate, render.ChapterBoundaries
				found = true
				break
			}
		}
		if !found {
			return M4BExportPlan{}, "", wav.Format{}, requestErrorf("choose a saved render revision")
		}
	} else if renderID != "" {
		return M4BExportPlan{}, "", wav.Format{}, requestErrorf("this legacy book has only its original master")
	}
	path := filepath.Join(m.rootDir, id, file)
	format, frames, err := wav.PCMFramesFile(path)
	if err != nil || frames <= 0 {
		return M4BExportPlan{}, "", wav.Format{}, fmt.Errorf("%w: master audio is unavailable", ErrStoreCorrupt)
	}
	hash, err := fileSHA256(ctx, path)
	if err != nil {
		return M4BExportPlan{}, "", wav.Format{}, err
	}
	plan := M4BExportPlan{RenderID: renderID, MasterSHA256: hash, SampleRate: format.SampleRate, Channels: format.Channels, Metadata: BookMetadata{Title: book.Title}, Available: m.encodeM4B != nil}
	if book.Metadata != nil {
		plan.Metadata = *book.Metadata
		if plan.Metadata.Title == "" {
			plan.Metadata.Title = book.Title
		}
	}
	if book.Cover != nil {
		cover := *book.Cover
		plan.Cover = &cover
	}
	if book.DocumentPlan == nil {
		plan.SingleChapter = true
		plan.Chapters = []ExportChapter{{Title: plan.Metadata.Title, EndSample: frames}}
	} else {
		if rate != format.SampleRate || len(bounds) != len(book.DocumentPlan.Chapters) {
			return M4BExportPlan{}, "", wav.Format{}, fmt.Errorf("%w: chapter timings are missing", ErrStoreCorrupt)
		}
		end := int64(0)
		for index, bound := range bounds {
			if bound.ChapterID != book.DocumentPlan.Chapters[index].ID || bound.StartSample != end || bound.EndSample <= bound.StartSample || bound.EndSample > frames {
				return M4BExportPlan{}, "", wav.Format{}, fmt.Errorf("%w: invalid chapter timing", ErrStoreCorrupt)
			}
			plan.Chapters = append(plan.Chapters, ExportChapter{Title: book.DocumentPlan.Chapters[index].Title, StartSample: bound.StartSample, EndSample: bound.EndSample})
			end = bound.EndSample
		}
		if end != frames {
			return M4BExportPlan{}, "", wav.Format{}, fmt.Errorf("%w: chapter timings do not cover the master", ErrStoreCorrupt)
		}
	}
	return plan, path, format, nil
}

func (m *Manager) PreviewM4B(ctx context.Context, id, renderID string) (M4BExportPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	plan, _, _, err := m.m4bSource(ctx, id, renderID)
	return plan, err
}

func m4bMetadata(plan M4BExportPlan) string {
	escape := strings.NewReplacer("\\", "\\\\", "=", "\\=", ";", "\\;", "#", "\\#", "\r", "\\\r", "\n", "\\\n")
	value := func(text string) string {
		encoded := escape.Replace(text)
		if strings.HasSuffix(encoded, "\\") {
			// FFmpeg's newer ffmetadec line reader treats an escaped final
			// backslash as continuation too. A NUL value terminator prevents
			// it consuming the next tag/chapter; both old and new readers
			// terminate the tag value here. This file is temporary codec input.
			encoded += "\x00"
		}
		return encoded
	}
	var text strings.Builder
	text.WriteString(";FFMETADATA1\n")
	for _, field := range [][2]string{{"title", plan.Metadata.Title}, {"artist", plan.Metadata.Author}, {"album_artist", plan.Metadata.Author}, {"album", plan.Metadata.Title}, {"composer", plan.Metadata.Narrator}, {"narrator", plan.Metadata.Narrator}, {"date", plan.Metadata.Year}, {"genre", plan.Metadata.Genre}, {"description", plan.Metadata.Description}, {"comment", plan.Metadata.Description}} {
		if field[1] != "" {
			fmt.Fprintf(&text, "%s=%s\n", field[0], value(field[1]))
		}
	}
	for _, chapter := range plan.Chapters {
		fmt.Fprintf(&text, "[CHAPTER]\nTIMEBASE=1/%d\nSTART=%d\nEND=%d\ntitle=%s\n", plan.SampleRate, chapter.StartSample, chapter.EndSample, value(chapter.Title))
	}
	return text.String()
}

func (m *Manager) ExportM4B(ctx context.Context, id string, request M4BExportRequest) (ExportRecord, error) {
	if m.encodeM4B == nil {
		return ExportRecord{}, ErrExportUnavailable
	}
	m.mu.Lock()
	plan, master, format, err := m.m4bSource(ctx, id, request.RenderID)
	if err != nil {
		m.mu.Unlock()
		return ExportRecord{}, err
	}
	if request.MasterSHA256 == "" || request.MasterSHA256 != plan.MasterSHA256 {
		m.mu.Unlock()
		return ExportRecord{}, requestErrorf("master changed; prepare the M4B export again")
	}
	stage, err := os.MkdirTemp(filepath.Join(m.rootDir, id), ".m4b-staging-")
	if err != nil {
		m.mu.Unlock()
		return ExportRecord{}, err
	}
	defer os.RemoveAll(stage)
	metadata := filepath.Join(stage, "metadata.txt")
	output := filepath.Join(stage, "book.m4b")
	cover := ""
	err = os.WriteFile(metadata, []byte(m4bMetadata(plan)), 0o600)
	if err == nil && plan.Cover != nil {
		var original string
		original, _, err = m.store.BookCoverPath(id)
		if err == nil {
			var data []byte
			data, err = os.ReadFile(original)
			if err == nil {
				cover = filepath.Join(stage, "cover"+filepath.Ext(original))
				err = os.WriteFile(cover, data, 0o600)
			}
		}
	}
	m.mu.Unlock()
	if err != nil {
		return ExportRecord{}, err
	}
	if err = m.encodeM4B(ctx, master, output, metadata, cover, format); err != nil {
		if ctx.Err() != nil {
			return ExportRecord{}, ctx.Err()
		}
		return ExportRecord{}, err
	}
	if err = ctx.Err(); err != nil {
		return ExportRecord{}, err
	}
	if err = engine.ValidateEncodedAudio(output, engine.AudioFormat{ID: "m4b", ContentType: "audio/mp4", Label: "M4B"}); err != nil {
		return ExportRecord{}, err
	}
	hash, err := fileSHA256(ctx, output)
	if err != nil {
		return ExportRecord{}, err
	}
	info, err := os.Stat(output)
	if err != nil {
		return ExportRecord{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeProductionID == id {
		return ExportRecord{}, ErrProductionActive
	}
	book, err := m.store.editableFinalBook(id)
	if err != nil {
		return ExportRecord{}, err
	}
	current, err := fileSHA256(ctx, master)
	if err != nil {
		return ExportRecord{}, err
	}
	if current != plan.MasterSHA256 {
		return ExportRecord{}, requestErrorf("master changed during export")
	}
	if err = ctx.Err(); err != nil {
		return ExportRecord{}, err
	}
	target := plan.RenderID
	if target == "" {
		target = "original"
	}
	if strings.ContainsAny(target, "/\\") {
		return ExportRecord{}, ErrStoreCorrupt
	}
	file := filepath.ToSlash(filepath.Join("exports", target+"-"+hash+".m4b"))
	final := filepath.Join(m.rootDir, id, filepath.FromSlash(file))
	if err = os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return ExportRecord{}, err
	}
	created := false
	if _, err = os.Stat(final); os.IsNotExist(err) {
		if err = os.Rename(output, final); err != nil {
			return ExportRecord{}, err
		}
		created = true
	} else if err != nil {
		return ExportRecord{}, err
	} else if existing, hashErr := fileSHA256(ctx, final); hashErr != nil || existing != hash {
		return ExportRecord{}, fmt.Errorf("%w: existing export file does not match its identity", ErrStoreCorrupt)
	}
	record := ExportRecord{Format: "m4b", RenderID: plan.RenderID, MasterSHA256: plan.MasterSHA256, Metadata: plan.Metadata, Cover: plan.Cover, Chapters: plan.Chapters, SampleRate: plan.SampleRate, File: file, URL: "/v1/audiobooks/" + id + "/exports/m4b?render=" + plan.RenderID, SHA256: hash, Bytes: info.Size(), CreatedAt: m.now()}
	replaced := false
	for index, previous := range book.Exports {
		if previous.RenderID == record.RenderID && previous.Format == "m4b" {
			book.Exports[index] = record
			replaced = true
			break
		}
	}
	if !replaced {
		book.Exports = append(book.Exports, record)
	}
	if err = m.store.SaveManifestFinal(book); err != nil {
		if created {
			_ = os.Remove(final)
		}
		return ExportRecord{}, err
	}
	return record, nil
}

func (m *Manager) ExportPath(id, renderID string) (string, ExportRecord, error) {
	book, err := m.store.editableFinalBook(id)
	if err != nil {
		return "", ExportRecord{}, err
	}
	for _, record := range book.Exports {
		if record.Format != "m4b" || record.RenderID != renderID {
			continue
		}
		if !validM4BExportFile(record) {
			return "", ExportRecord{}, ErrStoreCorrupt
		}
		path := filepath.Join(m.rootDir, id, filepath.FromSlash(record.File))
		info, err := os.Stat(path)
		if err != nil || info.Size() != record.Bytes {
			return "", ExportRecord{}, ErrProductionNotFound
		}
		if hash, err := fileSHA256(context.Background(), path); err != nil || hash != record.SHA256 {
			return "", ExportRecord{}, ErrStoreCorrupt
		}
		return path, record, nil
	}
	return "", ExportRecord{}, ErrProductionNotFound
}

func validM4BExportFile(record ExportRecord) bool {
	target := record.RenderID
	if target == "" {
		target = "original"
	}
	decoded, err := hex.DecodeString(record.SHA256)
	return record.Format == "m4b" && !strings.ContainsAny(target, "/\\") && err == nil && len(decoded) == sha256.Size && record.File == "exports/"+target+"-"+record.SHA256+".m4b"
}

// Read-time availability uses size/existence only; downloads verify the hash.
func (m *Manager) annotateBookExports(id string, book *Manifest) {
	for index := range book.Exports {
		record := &book.Exports[index]
		if record.Format != "m4b" {
			continue
		}
		record.UnavailableReason = ""
		if validateBookID(id) != nil || book.ID != id || !validM4BExportFile(*record) {
			record.UnavailableReason = "M4B export identity is invalid. Export this render again."
			continue
		}
		info, err := os.Stat(filepath.Join(m.rootDir, id, filepath.FromSlash(record.File)))
		if err != nil || !info.Mode().IsRegular() || info.Size() != record.Bytes {
			record.UnavailableReason = "M4B file is missing or changed. Export this render again."
		}
	}
}
