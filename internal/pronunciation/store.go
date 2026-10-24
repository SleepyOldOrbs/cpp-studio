package pronunciation

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const MaxDocumentBytes = 2 << 20

type Document struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}
type Store struct {
	mu   sync.Mutex
	path string
}

func NewStore(path string) *Store {
	if path == "" {
		path = filepath.Join("out", "pronunciation.json")
	}
	return &Store{path: path}
}
func (s *Store) Load() (Document, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.load() }
func (s *Store) load() (Document, error) {
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return Document{Version: 1, Rules: []Rule{}}, nil
	}
	if err != nil {
		return Document{}, err
	}
	defer f.Close()
	return decode(f)
}
func decode(r io.Reader) (Document, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxDocumentBytes+1))
	if err != nil {
		return Document{}, err
	}
	if len(data) > MaxDocumentBytes {
		return Document{}, fmt.Errorf("pronunciation JSON exceeds 2 MiB")
	}
	var doc Document
	if err = json.Unmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("invalid pronunciation JSON: %w", err)
	}
	return canonical(doc)
}
func canonical(doc Document) (Document, error) {
	if doc.Version != 1 {
		return doc, fmt.Errorf("unsupported pronunciation document version")
	}
	frozen, err := Freeze(doc.Rules)
	return Document{Version: 1, Rules: frozen.Rules}, err
}
func (s *Store) Replace(doc Document) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := canonical(doc)
	if err != nil {
		return doc, err
	}
	return s.save(doc)
}
func (s *Store) save(doc Document) (Document, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return doc, err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return doc, err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".pronunciation-*.json")
	if err != nil {
		return doc, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return doc, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return doc, err
	}
	if err = f.Close(); err != nil {
		return doc, err
	}
	return doc, os.Rename(f.Name(), s.path)
}
func (s *Store) Import(reader io.Reader, mode string) (Document, error) {
	if mode != "replace" && mode != "merge" {
		return Document{}, fmt.Errorf("choose replace or merge explicitly")
	}
	incoming, err := decode(reader)
	if err != nil {
		return incoming, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode == "merge" {
		current, err := s.load()
		if err != nil {
			return current, err
		}
		for _, r := range incoming.Rules {
			found := false
			for _, old := range current.Rules {
				if old.Term == r.Term && old.Language == r.Language {
					if old.Replacement != r.Replacement {
						return current, fmt.Errorf("conflicting rule for %s; use replace to change it", r.Term)
					}
					found = true
					break
				}
			}
			if !found {
				current.Rules = append(current.Rules, r)
			}
		}
		incoming, err = canonical(current)
		if err != nil {
			return incoming, err
		}
	}
	return s.save(incoming)
}
