package wav

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

const lowEnergyThreshold = 0.02

// PCMAnalysis describes measurable properties of a 16-bit PCM reference.
// Ratios are in [0,1], and amplitudes are normalized against full scale.
type PCMAnalysis struct {
	Duration               time.Duration
	Format                 Format
	PeakAmplitude          float64
	ClippedSampleRatio     float64
	RMS                    float64
	LeadingLowEnergyRatio  float64
	TrailingLowEnergyRatio float64
	TotalLowEnergyRatio    float64
	UsableSpeechDuration   time.Duration
}

// AnalyzePCM16 measures amplitude and low-energy regions without claiming
// speech detection. Optional VAD can replace the usable-duration estimate at
// a higher layer while retaining these format and signal facts.
func AnalyzePCM16(data []byte) (PCMAnalysis, error) {
	format, pcm, err := Decode(data)
	if err != nil {
		return PCMAnalysis{}, err
	}
	if format.Channels == 0 || format.SampleRate == 0 {
		return PCMAnalysis{}, fmt.Errorf("invalid WAV: zero channels or sample rate")
	}
	if format.BitsPerSample != 16 {
		return PCMAnalysis{}, fmt.Errorf("PCM analysis requires 16-bit audio, got %d-bit", format.BitsPerSample)
	}
	frameBytes := int(format.Channels) * 2
	if frameBytes <= 0 || len(pcm)%frameBytes != 0 {
		return PCMAnalysis{}, fmt.Errorf("invalid WAV: PCM data is not frame-aligned")
	}
	samples := len(pcm) / 2
	if samples == 0 {
		return PCMAnalysis{}, fmt.Errorf("invalid WAV: empty PCM data")
	}
	var sumSquares float64
	peak := 0.0
	clipped := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		value := float64(int16(binary.LittleEndian.Uint16(pcm[i:i+2]))) / 32768.0
		absolute := math.Abs(value)
		if absolute > peak {
			peak = absolute
		}
		if absolute >= 32767.0/32768.0 {
			clipped++
		}
		sumSquares += value * value
	}

	windowFrames := int(format.SampleRate) / 50 // 20 ms windows.
	if windowFrames < 1 {
		windowFrames = 1
	}
	windowSamples := windowFrames * int(format.Channels)
	low := make([]bool, 0, (samples+windowSamples-1)/windowSamples)
	for start := 0; start < samples; start += windowSamples {
		end := start + windowSamples
		if end > samples {
			end = samples
		}
		var windowSquares float64
		for sample := start; sample < end; sample++ {
			offset := sample * 2
			value := float64(int16(binary.LittleEndian.Uint16(pcm[offset:offset+2]))) / 32768.0
			windowSquares += value * value
		}
		low = append(low, math.Sqrt(windowSquares/float64(end-start)) < lowEnergyThreshold)
	}
	lowCount, leading, trailing := 0, 0, 0
	for _, isLow := range low {
		if isLow {
			lowCount++
		}
	}
	for leading < len(low) && low[leading] {
		leading++
	}
	for trailing < len(low) && low[len(low)-1-trailing] {
		trailing++
	}
	duration := time.Duration(float64(len(pcm)) / float64(int(format.SampleRate)*frameBytes) * float64(time.Second))
	totalRatio := float64(lowCount) / float64(len(low))
	return PCMAnalysis{
		Duration: duration, Format: format, PeakAmplitude: peak,
		ClippedSampleRatio:     float64(clipped) / float64(samples),
		RMS:                    math.Sqrt(sumSquares / float64(samples)),
		LeadingLowEnergyRatio:  float64(leading) / float64(len(low)),
		TrailingLowEnergyRatio: float64(trailing) / float64(len(low)),
		TotalLowEnergyRatio:    totalRatio,
		UsableSpeechDuration:   time.Duration(float64(duration) * (1 - totalRatio)),
	}, nil
}
