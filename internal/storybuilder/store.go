// Package storybuilder owns durable Story Builder Project state.
package storybuilder

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRootDir = "out/story-builder-projects"
	MaxNameLength  = 120
	manifestName   = "project.json"
)

var (
	ErrInvalid  = errors.New("invalid Story Builder Project")
	ErrNotFound = errors.New("Story Builder Project not found")
	ErrConflict = errors.New("Story Builder Project revision conflict")
)

// Project is one separately saved Story Builder production. Timeline state is
// added to this aggregate by later vertical slices.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type StoreOptions struct {
	WriteFileAtomic func(path string, data []byte) error
}

type Store struct {
	mu              sync.Mutex
	rootDir         string
	now             func() time.Time
	writeFileAtomic func(path string, data []byte) error
}

func NewStore(rootDir string) *Store {
	return NewStoreWithOptions(rootDir, StoreOptions{})
}

func NewStoreWithOptions(rootDir string, options StoreOptions) *Store {
	if rootDir == "" {
		rootDir = DefaultRootDir
	}
	write := options.WriteFileAtomic
	if write == nil {
		write = writeFileAtomic
	}
	return &Store{
		rootDir:         rootDir,
		now:             func() time.Time { return time.Now().UTC() },
		writeFileAtomic: write,
	}
}

func (s *Store) Create(name string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name, err := validateName(name)
	if err != nil {
		return Project{}, err
	}
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return Project{}, fmt.Errorf("create Story Builder Projects directory: %w", err)
	}

	now := s.now()
	for attempt := 0; attempt < 5; attempt++ {
		id, err := newProjectID(now)
		if err != nil {
			return Project{}, err
		}
		project := Project{ID: id, Name: name, Revision: 1, CreatedAt: now, UpdatedAt: now}
		finalDir := filepath.Join(s.rootDir, project.ID)
		if _, err := os.Stat(finalDir); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return Project{}, fmt.Errorf("stat Story Builder Project: %w", err)
		}

		stagingDir, err := os.MkdirTemp(s.rootDir, "."+project.ID+".staging-")
		if err != nil {
			return Project{}, fmt.Errorf("stage Story Builder Project: %w", err)
		}
		published := false
		defer func() {
			if !published {
				_ = os.RemoveAll(stagingDir)
			}
		}()
		data, err := encodeProject(project)
		if err != nil {
			return Project{}, err
		}
		if err := os.WriteFile(filepath.Join(stagingDir, manifestName), data, 0o644); err != nil {
			return Project{}, fmt.Errorf("write Story Builder Project: %w", err)
		}
		if err := os.Rename(stagingDir, finalDir); err != nil {
			return Project{}, fmt.Errorf("publish Story Builder Project: %w", err)
		}
		published = true
		return project, nil
	}
	return Project{}, fmt.Errorf("mint unique Story Builder Project id")
}

func (s *Store) Get(id string) (Project, bool, error) {
	if !validProjectID(id) {
		return Project{}, false, nil
	}
	data, err := os.ReadFile(filepath.Join(s.rootDir, id, manifestName))
	if os.IsNotExist(err) {
		return Project{}, false, nil
	}
	if err != nil {
		return Project{}, false, fmt.Errorf("read Story Builder Project: %w", err)
	}
	var project Project
	if err := json.Unmarshal(data, &project); err != nil {
		return Project{}, false, fmt.Errorf("decode Story Builder Project: %w", err)
	}
	if project.ID != id || project.Revision < 1 {
		return Project{}, false, fmt.Errorf("decode Story Builder Project: invalid manifest identity")
	}
	return project, true, nil
}

func (s *Store) List() ([]Project, error) {
	entries, err := os.ReadDir(s.rootDir)
	if os.IsNotExist(err) {
		return []Project{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list Story Builder Projects: %w", err)
	}
	projects := make([]Project, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		project, ok, err := s.Get(entry.Name())
		if err != nil {
			return nil, err
		}
		if ok {
			projects = append(projects, project)
		}
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].UpdatedAt.Equal(projects[j].UpdatedAt) {
			return projects[i].ID > projects[j].ID
		}
		return projects[i].UpdatedAt.After(projects[j].UpdatedAt)
	})
	return projects, nil
}

func (s *Store) Update(id string, revision int, name string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validProjectID(id) {
		return Project{}, ErrNotFound
	}
	name, err := validateName(name)
	if err != nil {
		return Project{}, err
	}
	project, ok, err := s.Get(id)
	if err != nil {
		return Project{}, err
	}
	if !ok {
		return Project{}, ErrNotFound
	}
	if revision != project.Revision {
		return Project{}, ErrConflict
	}
	project.Name = name
	project.Revision++
	project.UpdatedAt = s.now()
	data, err := encodeProject(project)
	if err != nil {
		return Project{}, err
	}
	if err := s.writeFileAtomic(filepath.Join(s.rootDir, id, manifestName), data); err != nil {
		return Project{}, fmt.Errorf("save Story Builder Project: %w", err)
	}
	return project, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validProjectID(id) {
		return ErrNotFound
	}
	if _, ok, err := s.Get(id); err != nil {
		return err
	} else if !ok {
		return ErrNotFound
	}
	if err := os.RemoveAll(filepath.Join(s.rootDir, id)); err != nil {
		return fmt.Errorf("delete Story Builder Project: %w", err)
	}
	return nil
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > MaxNameLength {
		return "", ErrInvalid
	}
	return name, nil
}

func validProjectID(id string) bool {
	if id == "" || strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) || filepath.IsAbs(id) {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func newProjectID(now time.Time) (string, error) {
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("mint Story Builder Project id: %w", err)
	}
	return fmt.Sprintf("sbp_%s_%s", now.Format("20060102_150405"), hex.EncodeToString(suffix)), nil
}

func encodeProject(project Project) ([]byte, error) {
	data, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Story Builder Project: %w", err)
	}
	return append(data, '\n'), nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	deadline := time.Now().Add(time.Second)
	for {
		err := os.Rename(tmpPath, path)
		if err == nil {
			return nil
		}
		if runtime.GOOS != "windows" || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
