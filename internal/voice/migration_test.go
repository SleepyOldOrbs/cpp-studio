package voice

import (
	"bytes"
	"cpp-studio/internal/wav"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLegacyVoiceMigrationIsSerializedAndFailurePreservesManifest(t *testing.T) {
	s := NewStore(t.TempDir())
	clone, err := s.Save("Legacy", "reference words", wav.SyntheticTone(800), false)
	if err != nil {
		t.Fatal(err)
	}
	clone.Analysis = nil
	original, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.rootDir, clone.ID, "manifest.json")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	s.writeFileAtomic = func(string, []byte) error { return errors.New("injected publication failure") }
	if _, _, err := s.Load(clone.ID); err == nil {
		t.Fatal("expected publication failure")
	}
	preserved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(preserved, original) {
		t.Fatalf("legacy manifest changed: %v", err)
	}
	var writes atomic.Int32
	s.writeFileAtomic = func(path string, data []byte) error { writes.Add(1); return writeVoiceFileAtomic(path, data) }
	start := make(chan struct{})
	results := make(chan error, 16)
	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			loaded, ok, err := s.Load(clone.ID)
			if err == nil && (!ok || loaded.Analysis == nil) {
				err = errors.New("missing migrated voice")
			}
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
	if writes.Load() != 1 {
		t.Fatalf("migration published %d times", writes.Load())
	}
	if _, err := s.CreateCharacterVoice(clone.ID, "Character", "clear"); err != nil {
		t.Fatal(err)
	}
}
