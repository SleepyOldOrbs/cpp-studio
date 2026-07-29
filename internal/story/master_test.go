package story

import (
	"context"
	"math"
	"testing"

	"cpp-studio/internal/wav"
)

// fakeMeasure models a measuring engine: loudness is read from a table keyed
// by the clip's length, and every gain applied since is tracked so the tests
// can reason about what mastering actually did.
type fakeMeasure struct {
	// byteLoudness maps a clip length to the loudness it should report.
	byteLoudness map[int]Loudness
	fallback     Loudness
	calls        int
}

func (f *fakeMeasure) measure(ctx context.Context, audio []byte) (Loudness, error) {
	f.calls++
	if loudness, ok := f.byteLoudness[len(audio)]; ok {
		return loudness, nil
	}
	return f.fallback, nil
}

func TestMasterRenderReachesTheTarget(t *testing.T) {
	stitched := wav.SyntheticTone(wav.ToneSampleRate)
	// Quiet input with plenty of peak headroom: the full gain fits.
	fake := &fakeMeasure{fallback: Loudness{IntegratedLUFS: -24, TruePeakDBTP: -12}}
	manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Measure: fake.measure, Now: fixedNow})

	// The second measurement is of the mastered audio, so report the value
	// the applied gain should have produced.
	fake.byteLoudness = map[int]Loudness{}
	mastered, report, err := manager.masterRender(context.Background(), stitched, nil)
	if err != nil {
		t.Fatalf("masterRender returned error: %v", err)
	}
	if report.GainDB != 8 {
		t.Fatalf("expected +8 dB to lift -24 LUFS to -16, got %+v", report.GainDB)
	}
	if len(mastered) != len(stitched) {
		t.Fatalf("gain changed the clip length: %d -> %d", len(stitched), len(mastered))
	}
	if report.Before.IntegratedLUFS != -24 {
		t.Fatalf("unexpected before measurement %+v", report.Before)
	}
	if fake.calls != 2 {
		t.Fatalf("expected a measurement before and after, got %d calls", fake.calls)
	}
}

// The honest case: linear gain cannot always hit both the loudness target
// and the peak ceiling. When it cannot, the render stays quieter and says
// so — it does not get compressed, and it does not claim the target.
func TestMasterRenderStopsAtThePeakCeiling(t *testing.T) {
	stitched := wav.SyntheticTone(wav.ToneSampleRate)
	// Wants +10 dB of loudness but only has 2 dB of peak headroom.
	fake := &fakeMeasure{fallback: Loudness{IntegratedLUFS: -26, TruePeakDBTP: -3.5}}
	manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Measure: fake.measure, Now: fixedNow})

	_, report, err := manager.masterRender(context.Background(), stitched, nil)
	if err != nil {
		t.Fatalf("masterRender returned error: %v", err)
	}
	if report.GainDB != 2 {
		t.Fatalf("expected the gain clamped to the 2 dB of headroom, got %.1f", report.GainDB)
	}
	if report.TargetMet {
		t.Fatalf("the target was not met, so the report must not say it was: %+v", report)
	}
	if report.Note == "" {
		t.Fatalf("an unmet target needs an explanation")
	}
}

func TestLevelSpeakersUsesAggregateMaterial(t *testing.T) {
	loud := wav.SyntheticTone(wav.ToneSampleRate / 2)
	quiet := wav.SyntheticTone(wav.ToneSampleRate / 3)
	// Two performers: one at -14, one at -24. Lengths differ so the fake can
	// tell whose material it is being handed.
	fake := &fakeMeasure{
		byteLoudness: map[int]Loudness{
			len(loud)*2 - 44:  {IntegratedLUFS: -14, TruePeakDBTP: -2},
			len(quiet)*2 - 44: {IntegratedLUFS: -24, TruePeakDBTP: -8},
		},
		fallback: Loudness{IntegratedLUFS: -19, TruePeakDBTP: -5},
	}
	manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Measure: fake.measure, Now: fixedNow})

	script := []ScriptLine{
		{ID: "line-001", SpeakerID: "loud", Text: "one"},
		{ID: "line-002", SpeakerID: "quiet", Text: "two"},
		{ID: "line-003", SpeakerID: "loud", Text: "three"},
		{ID: "line-004", SpeakerID: "quiet", Text: "four"},
	}
	has, load := clipLoaders([][]byte{loud, quiet, loud, quiet})

	gains, err := manager.levelSpeakers(context.Background(), script, has, load)
	if err != nil {
		t.Fatalf("levelSpeakers returned error: %v", err)
	}
	// Two speakers, so the median sits between them: each moves 5 dB toward
	// the other rather than one being dragged all the way.
	if gains["loud"] >= 0 || gains["quiet"] <= 0 {
		t.Fatalf("expected the loud speaker down and the quiet one up, got %+v", gains)
	}
	if math.Abs(gains["loud"]+gains["quiet"]) > 0.2 {
		t.Fatalf("expected symmetric correction toward the median, got %+v", gains)
	}
	// Each speaker is measured once on their combined material, not once per
	// line: measuring per line is what flattens a performance.
	if fake.calls != 2 {
		t.Fatalf("expected one measurement per speaker, got %d", fake.calls)
	}
}

func TestLevelSpeakersLeavesASingleVoiceAlone(t *testing.T) {
	clip := wav.SyntheticTone(wav.ToneSampleRate / 4)
	fake := &fakeMeasure{fallback: Loudness{IntegratedLUFS: -30, TruePeakDBTP: -10}}
	manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Measure: fake.measure, Now: fixedNow})

	script := []ScriptLine{
		{ID: "line-001", SpeakerID: "solo", Text: "one"},
		{ID: "line-002", SpeakerID: "solo", Text: "two"},
	}
	has, load := clipLoaders([][]byte{clip, clip})
	gains, err := manager.levelSpeakers(context.Background(), script, has, load)
	if err != nil {
		t.Fatalf("levelSpeakers returned error: %v", err)
	}
	if len(gains) != 0 {
		t.Fatalf("one voice cannot be out of balance with itself: %+v", gains)
	}
	if fake.calls != 0 {
		t.Fatalf("a single speaker needs no levelling measurement, got %d calls", fake.calls)
	}
}

func TestLevelSpeakersIgnoresMutedLines(t *testing.T) {
	clip := wav.SyntheticTone(wav.ToneSampleRate / 4)
	fake := &fakeMeasure{fallback: Loudness{IntegratedLUFS: -20, TruePeakDBTP: -6}}
	manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Measure: fake.measure, Now: fixedNow})

	// The only line by the second speaker is muted, so only one performer is
	// actually in the render and there is nothing to balance.
	script := []ScriptLine{
		{ID: "line-001", SpeakerID: "a", Text: "one"},
		{ID: "line-002", SpeakerID: "b", Text: "two", Muted: true},
	}
	has, load := clipLoaders([][]byte{clip, clip})
	gains, err := manager.levelSpeakers(context.Background(), script, has, load)
	if err != nil {
		t.Fatalf("levelSpeakers returned error: %v", err)
	}
	if len(gains) != 0 {
		t.Fatalf("a muted speaker is not in the render: %+v", gains)
	}
}

// clipLoaders adapts in-memory clips to the pull shape levelSpeakers uses,
// so these tests keep stating their material as plain slices.
func clipLoaders(clips [][]byte) (func(i int) bool, func(i int) ([]byte, error)) {
	return func(i int) bool { return len(clips[i]) > 0 },
		func(i int) ([]byte, error) { return clips[i], nil }
}

func TestApplyGainIsLinearAndClamps(t *testing.T) {
	clip := wav.SyntheticTone(wav.ToneSampleRate / 8)

	t.Run("zero is a no-op", func(t *testing.T) {
		out, clipped, err := wav.ApplyGain(clip, 0)
		if err != nil || clipped != 0 || len(out) != len(clip) {
			t.Fatalf("unexpected no-op result: %d bytes, %d clipped, %v", len(out), clipped, err)
		}
	})

	t.Run("attenuation never clips", func(t *testing.T) {
		_, clipped, err := wav.ApplyGain(clip, -6)
		if err != nil {
			t.Fatalf("ApplyGain returned error: %v", err)
		}
		if clipped != 0 {
			t.Fatalf("turning something down clipped %d samples", clipped)
		}
	})

	t.Run("absurd gain clamps and reports it", func(t *testing.T) {
		_, clipped, err := wav.ApplyGain(clip, 60)
		if err != nil {
			t.Fatalf("ApplyGain returned error: %v", err)
		}
		if clipped == 0 {
			t.Fatalf("+60 dB on a full-scale tone should have clipped something")
		}
	})
}
