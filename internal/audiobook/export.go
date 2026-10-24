package audiobook

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type BookMetadata struct {
	Title       string `json:"title"`
	Author      string `json:"author"`
	Narrator    string `json:"narrator"`
	Year        string `json:"year"`
	Genre       string `json:"genre"`
	Description string `json:"description"`
}

type BookCover struct {
	File        string `json:"file"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"contentType"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

func validateBookMetadata(metadata BookMetadata) error {
	for _, field := range []struct {
		value string
		limit int
	}{{metadata.Title, 256}, {metadata.Author, 256}, {metadata.Narrator, 256}, {metadata.Genre, 256}, {metadata.Description, 4000}} {
		if !utf8.ValidString(field.value) || utf8.RuneCountInString(field.value) > field.limit || strings.ContainsRune(field.value, 0) {
			return requestErrorf("book detail exceeds its text limit or contains invalid text")
		}
	}
	if metadata.Year != "" {
		if len(metadata.Year) != 4 {
			return requestErrorf("year must be empty or four digits")
		}
		for _, digit := range metadata.Year {
			if digit < '0' || digit > '9' {
				return requestErrorf("year must be empty or four digits")
			}
		}
	}
	return nil
}

func (s *Store) editableFinalBook(id string) (Manifest, error) {
	if validateBookID(id) != nil {
		return Manifest{}, ErrProductionNotFound
	}
	book, ok, err := loadManifest(filepath.Join(s.rootDir, id, manifestFileName))
	if err != nil {
		return Manifest{}, err
	}
	if !ok {
		return Manifest{}, ErrProductionNotFound
	}
	if book.Status != "" && book.Status != ProductionStatusComplete {
		return Manifest{}, ErrProductionActive
	}
	return book, nil
}

func (s *Store) UpdateBookMetadata(id string, metadata BookMetadata) error {
	if err := validateBookMetadata(metadata); err != nil {
		return err
	}
	book, err := s.editableFinalBook(id)
	if err != nil {
		return err
	}
	book.Metadata = &metadata
	if strings.TrimSpace(metadata.Title) != "" {
		book.Title = metadata.Title
	}
	return s.SaveManifestFinal(book)
}

func (s *Store) SetBookCover(id string, data []byte) error {
	if len(data) == 0 || len(data) > 10*1024*1024 {
		return requestErrorf("cover must be a PNG or JPEG no larger than 10 MiB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 20_000_000 {
		return requestErrorf("cover must be a valid PNG or JPEG with no more than 20 million pixels")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return requestErrorf("cover image is incomplete or malformed")
	}
	book, err := s.editableFinalBook(id)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	ext, contentType := ".png", "image/png"
	if format == "jpeg" {
		ext, contentType = ".jpg", "image/jpeg"
	}
	cover := &BookCover{File: "cover-" + hash + ext, SHA256: hash, ContentType: contentType, Width: config.Width, Height: config.Height}
	// Retained content-addressed covers let an export keep its original
	// snapshot while a later details edit selects a different image.
	if err := writeFileAtomic(filepath.Join(s.rootDir, id, cover.File), data); err != nil {
		return err
	}
	book.Cover = cover
	return s.SaveManifestFinal(book)
}

func (s *Store) BookCoverPath(id string) (string, string, error) {
	book, err := s.editableFinalBook(id)
	if err != nil {
		return "", "", err
	}
	if book.Cover == nil {
		return "", "", ErrProductionNotFound
	}
	cover := book.Cover
	ext := ".png"
	if cover.ContentType == "image/jpeg" {
		ext = ".jpg"
	} else if cover.ContentType != "image/png" {
		return "", "", ErrStoreCorrupt
	}
	if decoded, err := hex.DecodeString(cover.SHA256); err != nil || len(decoded) != sha256.Size || cover.File != "cover-"+cover.SHA256+ext {
		return "", "", ErrStoreCorrupt
	}
	path := filepath.Join(s.rootDir, id, cover.File)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", ErrProductionNotFound
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != cover.SHA256 {
		return "", "", fmt.Errorf("%w: cover hash does not match", ErrStoreCorrupt)
	}
	return path, cover.ContentType, nil
}

func (m *Manager) UpdateBookMetadata(id string, metadata BookMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeProductionID == id {
		return ErrProductionActive
	}
	return m.store.UpdateBookMetadata(id, metadata)
}

func (m *Manager) SetBookCover(id string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeProductionID == id {
		return ErrProductionActive
	}
	return m.store.SetBookCover(id, data)
}

func (m *Manager) BookCoverPath(id string) (string, string, error) { return m.store.BookCoverPath(id) }
