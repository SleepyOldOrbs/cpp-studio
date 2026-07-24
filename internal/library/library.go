// Package library is the studio's persistent output shelf: audio takes and
// images the user chooses to keep, stored on disk with metadata so they
// survive restarts and can be browsed, replayed, and downloaded later.
// Stories and cloned voices keep their own purpose-built stores; the library
// holds the outputs that previously evaporated with the browser tab.
package library

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cpp-studio/internal/wav"
)

const DefaultRootDir = "out/library"

// MaxItemBytes bounds one saved artifact (matches the story WAV cap).
const MaxItemBytes = 64 * 1024 * 1024

// MaxNameLength bounds the human-readable item name.
const MaxNameLength = 120

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// kinds maps each supported item kind to its artifact filename and a
// validator that rejects bytes that are not what the kind claims.
var kinds = map[string]struct {
	filename string
	validate func([]byte) error
}{
	"audio": {"audio.wav", wav.ValidateBytes},
	"image": {"image.png", func(data []byte) error {
		if len(data) < len(pngSignature) || !bytes.Equal(data[:len(pngSignature)], pngSignature) {
			return fmt.Errorf("expected PNG signature")
		}
		return nil
	}},
}

// Item is one saved output.
type Item struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Filename  string            `json:"filename"`
	Bytes     int64             `json:"bytes"`
	Meta      map[string]string `json:"meta,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
}

type Store struct {
	rootDir string
	now     func() time.Time
}

func NewStore(rootDir string) *Store {
	if rootDir == "" {
		rootDir = DefaultRootDir
	}
	return &Store{rootDir: rootDir, now: func() time.Time { return time.Now().UTC() }}
}

// Save persists one artifact with metadata, atomically (temp dir + rename).
func (s *Store) Save(kind, name string, data []byte, meta map[string]string) (Item, error) {
	spec, ok := kinds[kind]
	if !ok {
		return Item{}, fmt.Errorf("unsupported library kind %q", kind)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Item{}, fmt.Errorf("item name is required")
	}
	if len(name) > MaxNameLength {
		return Item{}, fmt.Errorf("item name is longer than %d characters", MaxNameLength)
	}
	if len(data) == 0 {
		return Item{}, fmt.Errorf("item data is empty")
	}
	if len(data) > MaxItemBytes {
		return Item{}, fmt.Errorf("item is %d bytes, max is %d", len(data), MaxItemBytes)
	}
	if err := spec.validate(data); err != nil {
		return Item{}, fmt.Errorf("invalid %s data: %v", kind, err)
	}

	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return Item{}, fmt.Errorf("mint item id: %w", err)
	}
	item := Item{
		ID:        fmt.Sprintf("lib_%s_%s", s.now().Format("20060102_150405"), hex.EncodeToString(suffix)),
		Kind:      kind,
		Name:      name,
		Filename:  spec.filename,
		Bytes:     int64(len(data)),
		Meta:      meta,
		CreatedAt: s.now(),
	}

	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return Item{}, fmt.Errorf("create library dir: %w", err)
	}
	tmpDir := filepath.Join(s.rootDir, "."+item.ID+".tmp")
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return Item{}, fmt.Errorf("create temp item dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, spec.filename), data, 0o644); err != nil {
		return Item{}, fmt.Errorf("write item artifact: %w", err)
	}
	encoded, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return Item{}, fmt.Errorf("encode item metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "item.json"), append(encoded, '\n'), 0o644); err != nil {
		return Item{}, fmt.Errorf("write item metadata: %w", err)
	}
	if err := os.Rename(tmpDir, filepath.Join(s.rootDir, item.ID)); err != nil {
		return Item{}, fmt.Errorf("finalize item dir: %w", err)
	}
	return item, nil
}

func (s *Store) Get(id string) (Item, bool, error) {
	if err := validateItemID(id); err != nil {
		return Item{}, false, nil
	}
	data, err := os.ReadFile(filepath.Join(s.rootDir, id, "item.json"))
	if os.IsNotExist(err) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, fmt.Errorf("read item metadata: %w", err)
	}
	var item Item
	if err := json.Unmarshal(data, &item); err != nil {
		return Item{}, false, fmt.Errorf("decode item metadata: %w", err)
	}
	return item, true, nil
}

// List returns every saved item, newest first.
func (s *Store) List() ([]Item, error) {
	entries, err := os.ReadDir(s.rootDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read library dir: %w", err)
	}
	items := make([]Item, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		item, ok, err := s.Get(entry.Name())
		if err != nil || !ok {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

// ArtifactPath resolves an item's artifact file for serving.
func (s *Store) ArtifactPath(id string) (string, string, error) {
	item, ok, err := s.Get(id)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("library item not found")
	}
	path := filepath.Join(s.rootDir, id, item.Filename)
	if _, err := os.Stat(path); err != nil {
		return "", "", fmt.Errorf("library artifact not found")
	}
	return path, item.Filename, nil
}

func (s *Store) Delete(id string) error {
	if err := validateItemID(id); err != nil {
		return fmt.Errorf("library item not found")
	}
	if _, ok, err := s.Get(id); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("library item not found")
	}
	return os.RemoveAll(filepath.Join(s.rootDir, id))
}

func validateItemID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || filepath.IsAbs(id) {
		return fmt.Errorf("invalid item id")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return fmt.Errorf("invalid item id")
	}
	return nil
}
