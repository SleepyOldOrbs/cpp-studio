package storybuilder

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactPublicationFailurePathsPreservePriorState(t *testing.T) {
	for _, failurePoint := range []string{"stage", "validate", "record"} {
		t.Run(failurePoint, func(t *testing.T) {
			directory := t.TempDir()
			finalPath := filepath.Join(directory, "artifact.wav")
			before := []byte("prior valid artifact")
			if err := os.WriteFile(finalPath, before, 0o644); err != nil {
				t.Fatal(err)
			}
			recorded := false
			published, err := publishArtifact(artifactPublication{
				FinalPath: finalPath,
				Replace:   true,
				Stage: func(path string) error {
					if failurePoint == "stage" {
						return errors.New("injected stage failure")
					}
					return os.WriteFile(path, []byte("replacement artifact"), 0o644)
				},
				Validate: func(string) error {
					if failurePoint == "validate" {
						return errors.New("injected validation failure")
					}
					return nil
				},
				Record: func() error {
					if failurePoint == "record" {
						return errors.New("injected manifest failure")
					}
					recorded = true
					return nil
				},
			})
			if err == nil || published || recorded {
				t.Fatalf("failure %q: published=%v recorded=%v err=%v", failurePoint, published, recorded, err)
			}
			after, readErr := os.ReadFile(finalPath)
			if readErr != nil || !bytes.Equal(after, before) {
				t.Fatalf("failure %q changed prior artifact: bytes=%q err=%v", failurePoint, after, readErr)
			}
			assertNoPublicationFiles(t, directory)
		})
	}
}

func TestArtifactPublicationCommitsArtifactBeforeManifest(t *testing.T) {
	directory := t.TempDir()
	finalPath := filepath.Join(directory, "artifact.wav")
	want := []byte("new artifact")
	published, err := publishArtifact(artifactPublication{
		FinalPath: finalPath,
		Stage: func(path string) error {
			return os.WriteFile(path, want, 0o644)
		},
		Validate: func(path string) error {
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, want) {
				t.Fatalf("validator saw bytes=%q err=%v", data, err)
			}
			return nil
		},
		Record: func() error {
			data, err := os.ReadFile(finalPath)
			if err != nil || !bytes.Equal(data, want) {
				t.Fatalf("manifest callback ran before promotion: bytes=%q err=%v", data, err)
			}
			return nil
		},
	})
	if err != nil || !published {
		t.Fatalf("publish: published=%v err=%v", published, err)
	}
	assertNoPublicationFiles(t, directory)
}

func assertNoPublicationFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name()[0] == '.' {
			t.Fatalf("publication staging file leaked: %s", entry.Name())
		}
	}
}
