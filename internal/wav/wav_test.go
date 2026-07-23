package wav

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestDecodeRoundTripsSyntheticTone(t *testing.T) {
	format, pcm, err := Decode(SyntheticTone(160))
	if err != nil {
		t.Fatalf("decode synthetic tone: %v", err)
	}
	if format.Channels != 1 || format.SampleRate != ToneSampleRate || format.BitsPerSample != 16 {
		t.Fatalf("unexpected format %+v", format)
	}
	if len(pcm) != 160*2 {
		t.Fatalf("expected %d PCM bytes, got %d", 160*2, len(pcm))
	}
	if encoded := Encode(format, pcm); string(encoded) != string(SyntheticTone(160)) {
		t.Fatalf("encode did not round-trip the tone")
	}
}

func TestDecodeSkipsExtraChunks(t *testing.T) {
	tone := SyntheticTone(16)
	// Rebuild the file with a LIST chunk between fmt and data.
	list := append([]byte("LIST"), 0x04, 0x00, 0x00, 0x00, 'I', 'N', 'F', 'O')
	rebuilt := append(append([]byte{}, tone[:36]...), append(list, tone[36:]...)...)
	// Fix the RIFF size for the inserted chunk.
	rebuilt[4] = byte(len(rebuilt) - 8)

	format, pcm, err := Decode(rebuilt)
	if err != nil {
		t.Fatalf("decode with LIST chunk: %v", err)
	}
	if format.SampleRate != ToneSampleRate || len(pcm) != 16*2 {
		t.Fatalf("unexpected decode result %+v with %d PCM bytes", format, len(pcm))
	}
}

func TestDecodeRejectsTruncatedChunks(t *testing.T) {
	tone := SyntheticTone(16)
	if _, _, err := Decode(tone[:50]); err == nil {
		t.Fatalf("expected truncated data chunk to fail decoding")
	}
	if _, _, err := Decode([]byte("RIFF\x04\x00\x00\x00WAVE")); err == nil {
		t.Fatalf("expected WAV without fmt/data chunks to fail decoding")
	}
}

func TestConcatenateInsertsGaps(t *testing.T) {
	clip := SyntheticTone(ToneSampleRate) // one second
	joined, err := Concatenate([][]byte{clip, clip}, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("concatenate: %v", err)
	}
	duration, err := Duration(joined)
	if err != nil {
		t.Fatalf("duration: %v", err)
	}
	want := 2500 * time.Millisecond
	if diff := duration - want; diff < -10*time.Millisecond || diff > 10*time.Millisecond {
		t.Fatalf("expected ~%s of audio, got %s", want, duration)
	}
}

func TestConcatenateRejectsMismatchedFormats(t *testing.T) {
	clip := SyntheticTone(160)
	format, pcm, err := Decode(clip)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	format.SampleRate = 24000
	other := Encode(format, pcm)

	if _, err := Concatenate([][]byte{clip, other}, 0); err == nil {
		t.Fatalf("expected mismatched formats to fail")
	}
	if _, err := Concatenate(nil, 0); err == nil {
		t.Fatalf("expected empty clip list to fail")
	}
}

func TestPadSilenceExtendsClip(t *testing.T) {
	clip := SyntheticTone(ToneSampleRate) // 1 second

	padded, err := PadSilence(clip, 250*time.Millisecond, 250*time.Millisecond)
	if err != nil {
		t.Fatalf("pad silence: %v", err)
	}
	duration, err := Duration(padded)
	if err != nil {
		t.Fatalf("duration: %v", err)
	}
	want := 1500 * time.Millisecond
	if diff := duration - want; diff < -10*time.Millisecond || diff > 10*time.Millisecond {
		t.Fatalf("expected ~%s of audio, got %s", want, duration)
	}

	format, pcm, err := Decode(padded)
	if err != nil {
		t.Fatalf("decode padded: %v", err)
	}
	_, originalPCM, err := Decode(clip)
	if err != nil {
		t.Fatalf("decode original: %v", err)
	}
	leadBytes := int(float64(format.SampleRate)*0.25) * int(format.Channels) * int(format.BitsPerSample) / 8
	for i := 0; i < leadBytes; i++ {
		if pcm[i] != 0 {
			t.Fatalf("expected silence at lead byte %d", i)
		}
	}
	if !bytes.Equal(pcm[leadBytes:leadBytes+len(originalPCM)], originalPCM) {
		t.Fatalf("expected original PCM preserved after lead silence")
	}
	for i := leadBytes + len(originalPCM); i < len(pcm); i++ {
		if pcm[i] != 0 {
			t.Fatalf("expected silence at trail byte %d", i)
		}
	}
}

func TestPadSilenceZeroDurationsKeepPCM(t *testing.T) {
	clip := SyntheticTone(160)
	padded, err := PadSilence(clip, 0, 0)
	if err != nil {
		t.Fatalf("pad silence: %v", err)
	}
	_, pcm, err := Decode(padded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	_, originalPCM, err := Decode(clip)
	if err != nil {
		t.Fatalf("decode original: %v", err)
	}
	if !bytes.Equal(pcm, originalPCM) {
		t.Fatalf("expected unchanged PCM with zero padding")
	}
}

func TestPadSilenceRejectsNonWAV(t *testing.T) {
	if _, err := PadSilence([]byte("RIFFtestWAVE"), time.Second, time.Second); err == nil {
		t.Fatalf("expected header-only bytes to fail decode")
	}
}
