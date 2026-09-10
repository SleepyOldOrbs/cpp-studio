package audiobook

import (
	"context"
	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeletionBlockedDuringRetryAndAttemptRender(t *testing.T) {
	root := t.TempDir()
	registry := jobs.NewRegistry()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var calls atomic.Int32
	manager := NewManager(ManagerOptions{RootDir: root, Jobs: registry, SynthesizeDetailed: func(_ context.Context, request SynthesisRequest) (SynthesisResult, error) {
		if calls.Add(1) > 1 {
			close(entered)
			<-release
		}
		actual := request.Options.Seed
		return SynthesisResult{Audio: wav.SyntheticTone(800), ActualSeed: &actual}, nil
	}})
	waitIdle := func() {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			manager.mu.Lock()
			idle := manager.activeID == ""
			manager.mu.Unlock()
			if idle {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("job did not release active production")
	}
	id, _, err := manager.Submit(context.Background(), Request{Text: "A recoverable fact.", EngineID: DramaBoxEngineID, Verification: VerificationModeOff})
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, registry, id)
	waitIdle()
	jobID, err := manager.RetrySection(context.Background(), id, "section-0001", RetryModeReproduce)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not enter synthesis")
	}
	if err := manager.Delete(id); !errors.Is(err, ErrProductionActive) {
		t.Errorf("delete during retry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, id)); err != nil {
		t.Errorf("active book removed: %v", err)
	}
	unblock()
	waitForAudiobookJob(t, registry, jobID)
	waitIdle()

	manifest, _, err := manager.store.LoadDurableFinal(id)
	if err != nil {
		t.Fatal(err)
	}
	renderEntered := make(chan struct{})
	renderRelease := make(chan struct{})
	var renderOnce sync.Once
	unblockRender := func() { renderOnce.Do(func() { close(renderRelease) }) }
	t.Cleanup(unblockRender)
	// Gate the actual render just before its immutable revision is committed.
	manager.now = func() time.Time { close(renderEntered); <-renderRelease; return time.Now().UTC() }
	renderID, err := manager.SelectAttempt(context.Background(), id, "section-0001", manifest.Sections[0].Attempts[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-renderEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("attempt render did not reach publication")
	}
	if err := manager.Delete(id); !errors.Is(err, ErrProductionActive) {
		t.Errorf("delete during render: %v", err)
	}
	unblockRender()
	waitForAudiobookJob(t, registry, renderID)
	waitIdle()
	if err := manager.Delete(id); err != nil {
		t.Fatalf("delete after completion: %v", err)
	}
}
