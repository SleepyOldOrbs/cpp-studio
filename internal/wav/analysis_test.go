package wav

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func TestAnalyzePCM16MeasuresSignalAndSilence(t *testing.T) {
	format := Format{Channels: 1, SampleRate: 1000, BitsPerSample: 16}
	pcm := make([]byte, 1000*2)
	for i := 200; i < 800; i++ {
		binary.LittleEndian.PutUint16(pcm[i*2:i*2+2], uint16(int16(16384)))
	}
	analysis, err := AnalyzePCM16(Encode(format, pcm))
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Duration != time.Second || analysis.Format != format {
		t.Fatalf("format facts = %+v", analysis)
	}
	if math.Abs(analysis.PeakAmplitude-0.5) > 0.001 || math.Abs(analysis.RMS-math.Sqrt(0.15)) > 0.001 {
		t.Fatalf("amplitude facts = %+v", analysis)
	}
	if math.Abs(analysis.LeadingLowEnergyRatio-0.2) > 0.001 || math.Abs(analysis.TrailingLowEnergyRatio-0.2) > 0.001 || math.Abs(analysis.TotalLowEnergyRatio-0.4) > 0.001 {
		t.Fatalf("silence facts = %+v", analysis)
	}
	if analysis.UsableSpeechDuration != 600*time.Millisecond || analysis.ClippedSampleRatio != 0 {
		t.Fatalf("usable/clipping facts = %+v", analysis)
	}
}

func TestAnalyzePCM16RejectsUnsupportedBitDepth(t *testing.T) {
	_, err := AnalyzePCM16(Encode(Format{Channels: 1, SampleRate: 16000, BitsPerSample: 8}, []byte{1, 2, 3}))
	if err == nil {
		t.Fatal("8-bit reference was analyzed as 16-bit PCM")
	}
}
