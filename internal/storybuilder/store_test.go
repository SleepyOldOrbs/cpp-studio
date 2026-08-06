package storybuilder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUserCanCreateRenameReloadAndDeleteProject(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)

	created, err := store.Create("The Lantern at Crow Point")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if created.ID == "" || created.Name != "The Lantern at Crow Point" || created.Revision != 1 {
		t.Fatalf("unexpected created project: %+v", created)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created project needs timestamps: %+v", created)
	}

	projects, err := store.List()
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != created.ID {
		t.Fatalf("unexpected project list: %+v", projects)
	}

	renamed, err := store.Update(created.ID, created.Revision, "Lantern — final edit")
	if err != nil {
		t.Fatalf("rename project: %v", err)
	}
	if renamed.Name != "Lantern — final edit" || renamed.Revision != 2 || !renamed.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("unexpected renamed project: %+v", renamed)
	}

	if _, err := store.Update(created.ID, created.Revision, "stale browser overwrite"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}

	restarted := NewStore(root)
	loaded, ok, err := restarted.Get(created.ID)
	if err != nil || !ok {
		t.Fatalf("reload project: ok=%v err=%v", ok, err)
	}
	if loaded.Name != renamed.Name || loaded.Revision != renamed.Revision || loaded.ID != created.ID ||
		!loaded.CreatedAt.Equal(renamed.CreatedAt) || !loaded.UpdatedAt.Equal(renamed.UpdatedAt) {
		t.Fatalf("reloaded project drifted: %+v", loaded)
	}
	sibling, err := restarted.Create("A separate production")
	if err != nil {
		t.Fatalf("create sibling project: %v", err)
	}
	sentinelPath := filepath.Join(root, "not-a-project.txt")
	if err := os.WriteFile(sentinelPath, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write root sentinel: %v", err)
	}

	if err := restarted.Delete(created.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if _, ok, err := restarted.Get(created.ID); err != nil || ok {
		t.Fatalf("deleted project still loads: ok=%v err=%v", ok, err)
	}
	if err := restarted.Delete(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing project error = %v, want ErrNotFound", err)
	}
	if _, ok, err := restarted.Get(sibling.ID); err != nil || !ok {
		t.Fatalf("delete removed sibling project: ok=%v err=%v", ok, err)
	}
	if data, err := os.ReadFile(sentinelPath); err != nil || string(data) != "keep" {
		t.Fatalf("delete changed root sentinel: data=%q err=%v", data, err)
	}
}

func TestConcurrentSavesOfOneRevisionAllowOneWinner(t *testing.T) {
	root := t.TempDir()
	created, err := NewStore(root).Create("Shared project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	enteredWrite := make(chan struct{}, 2)
	releaseWrite := make(chan struct{}, 2)
	store := NewStoreWithOptions(root, StoreOptions{
		WriteFileAtomic: func(path string, data []byte) error {
			enteredWrite <- struct{}{}
			<-releaseWrite
			return writeFileAtomic(path, data)
		},
	})
	results := make(chan error, 2)
	go func() {
		_, err := store.Update(created.ID, created.Revision, "first editor")
		results <- err
	}()
	<-enteredWrite
	go func() {
		_, err := store.Update(created.ID, created.Revision, "second editor")
		results <- err
	}()

	select {
	case <-enteredWrite:
		releaseWrite <- struct{}{}
		releaseWrite <- struct{}{}
		<-results
		<-results
		t.Fatal("two clients reached the write for the same revision")
	case <-time.After(50 * time.Millisecond):
		releaseWrite <- struct{}{}
	}

	errA, errB := <-results, <-results
	conflicts := 0
	for _, err := range []error{errA, errB} {
		if errors.Is(err, ErrConflict) {
			conflicts++
		} else if err != nil {
			t.Fatalf("unexpected save error: %v", err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("conflicts = %d, want 1 (errors %v, %v)", conflicts, errA, errB)
	}
}

func TestFailedUpdateLeavesLastValidProjectReadable(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	created, err := store.Create("Safe version")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	writes := 0
	failing := NewStoreWithOptions(root, StoreOptions{
		WriteFileAtomic: func(path string, data []byte) error {
			writes++
			if writes == 1 {
				return fmt.Errorf("injected disk failure")
			}
			return writeFileAtomic(path, data)
		},
	})
	if _, err := failing.Update(created.ID, created.Revision, "unsafe version"); err == nil {
		t.Fatal("expected injected update failure")
	}

	loaded, ok, err := NewStore(root).Get(created.ID)
	if err != nil || !ok {
		t.Fatalf("load after failed update: ok=%v err=%v", ok, err)
	}
	if loaded.Name != "Safe version" || loaded.Revision != 1 {
		t.Fatalf("failed update changed durable project: %+v", loaded)
	}

	retried, err := failing.Update(created.ID, created.Revision, "retry version")
	if err != nil {
		t.Fatalf("retry update: %v", err)
	}
	if retried.Name != "retry version" || retried.Revision != 2 {
		t.Fatalf("unexpected retried project: %+v", retried)
	}
	projects, err := failing.List()
	if err != nil {
		t.Fatalf("list after retry: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != created.ID {
		t.Fatalf("retry duplicated project: %+v", projects)
	}
}

func TestProjectValidationAndTraversalAreRejected(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, name := range []string{"", "   ", string(make([]byte, MaxNameLength+1))} {
		if _, err := store.Create(name); !errors.Is(err, ErrInvalid) {
			t.Fatalf("create name %q error = %v, want ErrInvalid", name, err)
		}
	}

	for _, id := range []string{"", "../../outside", `..\outside`, "a/b", "UPPER", "sneaky.."} {
		if _, ok, err := store.Get(id); err != nil || ok {
			t.Fatalf("Get(%q) = ok %v, err %v", id, ok, err)
		}
		if _, err := store.Update(id, 1, "name"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Update(%q) error = %v, want ErrNotFound", id, err)
		}
		if err := store.Delete(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Delete(%q) error = %v, want ErrNotFound", id, err)
		}
	}
}
