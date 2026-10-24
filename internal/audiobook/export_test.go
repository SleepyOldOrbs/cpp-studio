package audiobook

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func legacyExportBook(t *testing.T) (*Store, string) {
	t.Helper()
	store := NewStore(t.TempDir())
	id := "book_20261024_120000_001"
	if err := os.MkdirAll(filepath.Join(store.rootDir, id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(filepath.Join(store.rootDir, id, manifestFileName), Manifest{ID: id, Title: "Original title", SynthesisFingerprint: "untouched"}); err != nil {
		t.Fatal(err)
	}
	return store, id
}

func testCoverPNG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	cover := image.NewRGBA(image.Rect(0, 0, 4, 3))
	cover.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&data, cover); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestBookDetailsRoundTripWithoutChangingSynthesis(t *testing.T) {
	store, id := legacyExportBook(t)
	metadata := BookMetadata{Title: "Café at sea", Author: "A. Writer", Narrator: "Ada", Year: "2026", Genre: "Fiction", Description: "First line.\nSecond; # = \\ line."}
	if err := store.UpdateBookMetadata(id, metadata); err != nil {
		t.Fatal(err)
	}
	book, ok, err := loadManifest(filepath.Join(store.rootDir, id, manifestFileName))
	if err != nil || !ok || book.Metadata == nil || !reflect.DeepEqual(*book.Metadata, metadata) || book.Title != metadata.Title || book.SynthesisFingerprint != "untouched" {
		t.Fatal(book, ok, err)
	}
	for _, invalid := range []BookMetadata{{Year: "２０２６"}, {Year: "20260"}, {Author: strings.Repeat("x", 257)}, {Description: strings.Repeat("x", 4001)}} {
		if err := store.UpdateBookMetadata(id, invalid); !IsRequestError(err) {
			t.Fatal("invalid details accepted", invalid, err)
		}
	}
	after, _, _ := loadManifest(filepath.Join(store.rootDir, id, manifestFileName))
	if !reflect.DeepEqual(after, book) {
		t.Fatal("invalid details changed the saved book")
	}
}

func TestBookCoverValidationPreservesPreviousCover(t *testing.T) {
	store, id := legacyExportBook(t)
	cover := testCoverPNG(t)
	if err := store.SetBookCover(id, cover); err != nil {
		t.Fatal(err)
	}
	book, _, err := loadManifest(filepath.Join(store.rootDir, id, manifestFileName))
	if err != nil || book.Cover == nil || book.Cover.Width != 4 || book.Cover.Height != 3 || book.Cover.ContentType != "image/png" {
		t.Fatal(book, err)
	}
	path := filepath.Join(store.rootDir, id, book.Cover.File)
	before, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, cover) {
		t.Fatal("cover not owned by the book", err)
	}
	for _, invalid := range [][]byte{nil, []byte("not an image"), cover[:len(cover)-12], make([]byte, 10*1024*1024+1)} {
		if err := store.SetBookCover(id, invalid); !IsRequestError(err) {
			t.Fatal("invalid cover accepted", err)
		}
	}
	tooManyPixels := append([]byte(nil), cover...)
	binary.BigEndian.PutUint32(tooManyPixels[16:20], 5000)
	binary.BigEndian.PutUint32(tooManyPixels[20:24], 4001)
	binary.BigEndian.PutUint32(tooManyPixels[29:33], crc32.ChecksumIEEE(tooManyPixels[12:29]))
	if err := store.SetBookCover(id, tooManyPixels); !IsRequestError(err) {
		t.Fatal("oversized dimensions accepted", err)
	}
	after, _, _ := loadManifest(filepath.Join(store.rootDir, id, manifestFileName))
	if !reflect.DeepEqual(after, book) {
		t.Fatal("bad cover replaced manifest")
	}
	saved, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(saved) != sha256.Sum256(before) {
		t.Fatal("bad cover changed original bytes", err)
	}
}

func TestJPEGBookCoverAndCorruptPathRejection(t *testing.T) {
	store, id := legacyExportBook(t)
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 4, 3)), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBookCover(id, data.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, contentType, err := store.BookCoverPath(id); err != nil || contentType != "image/jpeg" {
		t.Fatal(contentType, err)
	}
	book, _, _ := loadManifest(filepath.Join(store.rootDir, id, manifestFileName))
	book.Cover.File = "../../outside.jpg"
	if err := store.SaveManifestFinal(book); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BookCoverPath(id); !errors.Is(err, ErrStoreCorrupt) {
		t.Fatal("unsafe cover path accepted", err)
	}
}
