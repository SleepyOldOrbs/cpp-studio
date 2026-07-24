package library

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"cpp-studio/internal/wav"
)

func validPNG() []byte {
	// 1x1 PNG (signature check only cares about the header).
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 13}
}

func TestSaveListGetDelete(t *testing.T) {
	s := NewStore(t.TempDir())

	audio, err := s.Save("audio", "My take", wav.SyntheticTone(1600), map[string]string{"voice": "cox"})
	if err != nil {
		t.Fatalf("save audio: %v", err)
	}
	image, err := s.Save("image", "A cabin", validPNG(), nil)
	if err != nil {
		t.Fatalf("save image: %v", err)
	}

	items, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	got, ok, err := s.Get(audio.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Name != "My take" || got.Kind != "audio" || got.Meta["voice"] != "cox" {
		t.Fatalf("unexpected item: %+v", got)
	}

	path, filename, err := s.ArtifactPath(audio.ID)
	if err != nil {
		t.Fatalf("artifact path: %v", err)
	}
	if filename != "audio.wav" {
		t.Fatalf("unexpected filename %q", filename)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, wav.SyntheticTone(1600)) {
		t.Fatalf("artifact roundtrip failed: %v", err)
	}

	if err := s.Delete(image.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := s.Get(image.ID); ok {
		t.Fatal("deleted item still present")
	}
	if err := s.Delete(image.ID); err == nil {
		t.Fatal("expected error deleting missing item")
	}
}

func TestSaveValidation(t *testing.T) {
	s := NewStore(t.TempDir())
	cases := []struct {
		name string
		kind string
		item string
		data []byte
	}{
		{"unknown kind", "video", "x", validPNG()},
		{"empty name", "image", "  ", validPNG()},
		{"long name", "image", strings.Repeat("x", MaxNameLength+1), validPNG()},
		{"empty data", "image", "x", nil},
		{"wav bytes as image", "image", "x", wav.SyntheticTone(160)},
		{"png bytes as audio", "audio", "x", validPNG()},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.Save(tt.kind, tt.item, tt.data, nil); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}

func TestPathTraversalRejected(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, id := range []string{"../../etc", `..\..\x`, "a/b", "", "UPPER", "sneaky.."} {
		if _, ok, _ := s.Get(id); ok {
			t.Fatalf("traversal id %q resolved", id)
		}
		if err := s.Delete(id); err == nil {
			t.Fatalf("traversal delete %q succeeded", id)
		}
	}
}
