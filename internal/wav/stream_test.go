package wav

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func constantPCM(format Format, frames int, sample int16) []byte {
	pcm := make([]byte, frames*int(format.Channels)*2)
	for offset := 0; offset < len(pcm); offset += 2 {
		binary.LittleEndian.PutUint16(pcm[offset:], uint16(sample))
	}
	return Encode(format, pcm)
}

func TestAssembleFilesStreamsEqualPowerCrossfade(t *testing.T) {
	root := t.TempDir()
	format := Format{Channels: 1, SampleRate: 1000, BitsPerSample: 16}
	first := filepath.Join(root, "first.wav")
	second := filepath.Join(root, "second.wav")
	if err := os.WriteFile(first, constantPCM(format, 1000, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, constantPCM(format, 1000, -1000), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "book.wav")
	if err := AssembleFiles(output, []string{first, second}, 50*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond); err != nil {
		t.Fatalf("assemble: %v", err)
	}
	duration, err := DurationFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if want := 2550 * time.Millisecond; duration != want {
		t.Fatalf("duration=%s want=%s", duration, want)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	_, pcm, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	// Crossfade starts after 300 ms lead plus 950 ms of the first section.
	startSample := 1250
	firstMixed := int16(binary.LittleEndian.Uint16(pcm[startSample*2:]))
	lastMixed := int16(binary.LittleEndian.Uint16(pcm[(startSample+49)*2:]))
	if firstMixed != 1000 || lastMixed != -1000 {
		t.Fatalf("crossfade endpoints=%d,%d want=1000,-1000", firstMixed, lastMixed)
	}
	middle := int16(binary.LittleEndian.Uint16(pcm[(startSample+25)*2:]))
	if math.Abs(float64(middle)) > 80 {
		t.Fatalf("opposing equal-power midpoint should nearly cancel, got %d", middle)
	}
	if info, err := os.Stat(output); err != nil || info.Size() != int64(44+2550*2) {
		t.Fatalf("patched WAV size: info=%v err=%v", info, err)
	}
}

func TestAssembleFilesRejectsFormatMismatchBeforeOutput(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.wav")
	second := filepath.Join(root, "second.wav")
	_ = os.WriteFile(first, SyntheticTone(160), 0o600)
	_ = os.WriteFile(second, constantPCM(Format{Channels: 1, SampleRate: 24000, BitsPerSample: 16}, 160, 1), 0o600)
	output := filepath.Join(root, "book.wav")
	if err := AssembleFiles(output, []string{first, second}, 50*time.Millisecond, 0, 0); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected format mismatch, got %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed preflight published output: %v", err)
	}
}

func TestCheckRIFFDataSizeBoundary(t *testing.T) {
	if err := CheckRIFFDataSize(MaxRIFFDataBytes); err != nil {
		t.Fatalf("exact RIFF boundary rejected: %v", err)
	}
	if err := CheckRIFFDataSize(MaxRIFFDataBytes + 1); err == nil || !strings.Contains(err.Error(), "narrate in parts") {
		t.Fatalf("overflow did not give actionable refusal: %v", err)
	}
}
