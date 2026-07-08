package wav

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateBytes(t *testing.T) {
	if err := ValidateBytes([]byte("RIFFtestWAVE")); err != nil {
		t.Fatalf("expected minimal RIFF/WAVE header to validate, got %v", err)
	}
	for name, data := range map[string][]byte{
		"empty":       nil,
		"short":       []byte("RIFF"),
		"wrong magic": []byte("RIFFtestXXXX"),
		"not riff":    []byte("XXXXtestWAVE"),
		"plain text":  []byte("not a wav at all"),
	} {
		if err := ValidateBytes(data); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

func TestValidateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := os.WriteFile(path, SyntheticTone(160), 0o600); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	if err := ValidateFile(path); err != nil {
		t.Fatalf("expected synthetic tone to validate, got %v", err)
	}
	if err := ValidateFile(filepath.Join(t.TempDir(), "missing.wav")); err == nil {
		t.Fatalf("expected missing file error")
	}
}

func TestSyntheticTone(t *testing.T) {
	tone := SyntheticTone(160)
	if err := ValidateBytes(tone); err != nil {
		t.Fatalf("synthetic tone failed validation: %v", err)
	}
	wantLen := 44 + 160*2
	if len(tone) != wantLen {
		t.Fatalf("expected %d bytes, got %d", wantLen, len(tone))
	}
	if got := SyntheticTone(0); len(got) != 44+2 {
		t.Fatalf("expected non-positive sample count to clamp to one sample, got %d bytes", len(got))
	}
}
