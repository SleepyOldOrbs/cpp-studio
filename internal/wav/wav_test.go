package wav

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
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

func TestMixTimelinePlacesTrimsAndOverlapsSources(t *testing.T) {
	tone := steppedTestWAV()
	mixed, err := MixTimeline(3000, []TimelinePlacement{
		{Data: tone, StartMS: 500, SourceInMS: 1000, DurationMS: 1000},
		{Data: tone, StartMS: 1000, DurationMS: 1000},
	})
	if err != nil {
		t.Fatalf("mix timeline: %v", err)
	}
	if duration, err := Duration(mixed); err != nil || duration != 3*time.Second {
		t.Fatalf("mixed duration = %s, err=%v", duration, err)
	}
	_, pcm, err := Decode(mixed)
	if err != nil {
		t.Fatalf("decode mix: %v", err)
	}
	sampleAt := func(ms int) int16 {
		offset := ms * ToneSampleRate / 1000 * 2
		return int16(binary.LittleEndian.Uint16(pcm[offset : offset+2]))
	}
	for _, check := range []struct {
		ms   int
		want int16
	}{{0, 0}, {500, 3000}, {1000, 4000}, {1500, 1000}, {2000, 0}} {
		if got := sampleAt(check.ms); got != check.want {
			t.Fatalf("sample at %d ms = %d, want %d", check.ms, got, check.want)
		}
	}
}

func steppedTestWAV() []byte {
	format := Format{Channels: 1, SampleRate: ToneSampleRate, BitsPerSample: 16}
	pcm := make([]byte, 2*ToneSampleRate*2)
	for sample := 0; sample < 2*ToneSampleRate; sample++ {
		value := int16(1000)
		if sample >= ToneSampleRate {
			value = 3000
		}
		binary.LittleEndian.PutUint16(pcm[sample*2:sample*2+2], uint16(value))
	}
	return Encode(format, pcm)
}

func TestMixTimelineRejectsInvalidSourcesAndTiming(t *testing.T) {
	tone := SyntheticTone(ToneSampleRate)
	for name, placements := range map[string][]TimelinePlacement{
		"trim exceeds source":        {{Data: tone, SourceInMS: 500, DurationMS: 1000}},
		"placement exceeds timeline": {{Data: tone, StartMS: 500, DurationMS: 1000}},
	} {
		durationMS := int64(1000)
		if name == "placement exceeds timeline" {
			durationMS = 1000
		}
		if _, err := MixTimeline(durationMS, placements); err == nil {
			t.Fatalf("%s succeeded", name)
		}
	}
}

func TestMixTimelineNormalizesSourceRatesAndChannels(t *testing.T) {
	format := Format{Channels: 2, SampleRate: 24000, BitsPerSample: 16}
	pcm := make([]byte, int(format.SampleRate)*int(format.Channels)*2)
	for frame := 0; frame < int(format.SampleRate); frame++ {
		binary.LittleEndian.PutUint16(pcm[frame*4:frame*4+2], uint16(int16(1000)))
		binary.LittleEndian.PutUint16(pcm[frame*4+2:frame*4+4], uint16(int16(3000)))
	}
	mixed, err := MixTimeline(1000, []TimelinePlacement{
		{Data: Encode(format, pcm), DurationMS: 1000},
		{Data: SyntheticTone(ToneSampleRate), DurationMS: 1000},
	})
	if err != nil {
		t.Fatalf("mix different formats: %v", err)
	}
	gotFormat, gotPCM, err := Decode(mixed)
	if err != nil || gotFormat != TimelineMixFormat() {
		t.Fatalf("mixed format = %+v, err=%v", gotFormat, err)
	}
	if got := int16(binary.LittleEndian.Uint16(gotPCM[:2])); got != 3000 {
		t.Fatalf("normalized stereo + mono sample = %d, want 3000", got)
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

func TestDecodeRejectsNonPCMFormat(t *testing.T) {
	data := SyntheticTone(ToneSampleRate)
	data[20] = 3 // IEEE float format tag; the payload is deliberately irrelevant.
	if _, _, err := Decode(data); err == nil || !strings.Contains(err.Error(), "PCM required") {
		t.Fatalf("Decode non-PCM error = %v, want PCM-required error", err)
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
