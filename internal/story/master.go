package story

import (
	"context"
	"fmt"
	"math"
	"sort"

	"cpp-studio/internal/wav"
)

// Mastering. A render used to be raw concatenation, so each cloned voice sat
// wherever its reference sat and the finished piece sat at no particular
// level at all. Two problems, and they want different answers.
//
// Between speakers, the fix is levelling: measure each performer across all
// of their material and bring them into line with each other, so a two-hander
// stops having a loud half. Measuring per *line* would be the wrong unit — it
// would drag every whisper up and every shout down and flatten the
// performance into a monotone.
//
// For the finished piece, the fix is a single gain toward the delivery
// target. Both are linear gain: no compression, no limiting, nothing that
// changes the shape of what was performed.

const (
	// TargetLUFS is the podcast delivery convention, and it is a delivery
	// target rather than "EBU R128" — R128's programme target is -23 LUFS.
	// -16 is what listeners' players and every podcast host expect.
	TargetLUFS = -16.0
	// TargetTruePeakDBTP leaves headroom for the inter-sample peaks a lossy
	// encoder reconstructs, which is why it sits below 0.
	TargetTruePeakDBTP = -1.5
	// MaxLevellingGainDB bounds per-speaker correction. A voice that needs
	// more than this is not badly levelled, it is a bad reference, and
	// hauling it up would bring its noise floor with it.
	MaxLevellingGainDB = 12.0
)

// MeasureFunc reports the BS.1770 loudness of a WAV. Injected so this
// package never learns what ffmpeg is.
type MeasureFunc func(ctx context.Context, audio []byte) (Loudness, error)

// Loudness is one measurement, in the units the standard uses.
type Loudness struct {
	IntegratedLUFS float64 `json:"integrated_lufs"`
	TruePeakDBTP   float64 `json:"true_peak_dbtp"`
	RangeLU        float64 `json:"range_lu,omitempty"`
}

// Master is the record of what mastering did to a render, kept so the claim
// is checkable rather than asserted. Every field is a measurement or an
// applied number, and TargetMet says plainly whether the delivery target was
// reached — because it cannot always be.
type Master struct {
	TargetLUFS     float64            `json:"target_lufs"`
	TargetPeakDBTP float64            `json:"target_true_peak_dbtp"`
	Before         Loudness           `json:"before"`
	After          Loudness           `json:"after"`
	GainDB         float64            `json:"gain_db"`
	SpeakerGainsDB map[string]float64 `json:"speaker_gains_db,omitempty"`
	// TargetMet is false when the true-peak ceiling was reached before the
	// loudness target was. The honest outcome then is a quieter render, not
	// a compressor.
	TargetMet bool `json:"target_met"`
	// Note explains a TargetMet of false in the same words a person would.
	Note string `json:"note,omitempty"`
}

// levelSpeakers brings each speaker's material into line with the others.
// It measures a speaker's takes together rather than one at a time, so the
// correction reflects how loud that performer is overall and not how loud
// any single line happened to be. Audio is pulled through load one speaker
// at a time — the peak cost is one performer's material, not the episode's.
func (m *Manager) levelSpeakers(ctx context.Context, script []ScriptLine, has func(i int) bool, load func(i int) ([]byte, error)) (map[string]float64, error) {
	bySpeaker := make(map[string][]int)
	for i := range script {
		if script[i].Muted || !has(i) {
			continue
		}
		bySpeaker[script[i].SpeakerID] = append(bySpeaker[script[i].SpeakerID], i)
	}
	if len(bySpeaker) < 2 {
		// One voice cannot be out of balance with itself; the render-level
		// pass will place it against the target.
		return nil, nil
	}

	measured := make(map[string]float64, len(bySpeaker))
	speakers := make([]string, 0, len(bySpeaker))
	for speaker, lines := range bySpeaker {
		takes := make([][]byte, 0, len(lines))
		for _, i := range lines {
			audio, err := load(i)
			if err != nil {
				return nil, fmt.Errorf("load %s's takes to measure them: %w", speaker, err)
			}
			takes = append(takes, audio)
		}
		joined, err := wav.Concatenate(takes, 0)
		if err != nil {
			return nil, fmt.Errorf("join %s's takes to measure them: %w", speaker, err)
		}
		takes = nil
		loudness, err := m.measure(ctx, joined)
		if err != nil {
			return nil, fmt.Errorf("measure %s: %w", speaker, err)
		}
		measured[speaker] = loudness.IntegratedLUFS
		speakers = append(speakers, speaker)
	}
	sort.Strings(speakers)

	// Level toward the median performer rather than the loudest or the
	// quietest: it is the choice that moves the fewest voices the furthest.
	reference := medianOf(measured, speakers)
	gains := make(map[string]float64, len(speakers))
	for _, speaker := range speakers {
		gain := reference - measured[speaker]
		if gain > MaxLevellingGainDB {
			gain = MaxLevellingGainDB
		}
		if gain < -MaxLevellingGainDB {
			gain = -MaxLevellingGainDB
		}
		// Sub-decibel differences are not audible and not worth recording.
		if math.Abs(gain) < 0.5 {
			continue
		}
		gains[speaker] = round1(gain)
	}
	if len(gains) == 0 {
		return nil, nil
	}
	return gains, nil
}

// masterRender places a finished stitch at the delivery target with one
// linear gain, refusing to breach the true-peak ceiling to get there.
func (m *Manager) masterRender(ctx context.Context, stitched []byte, speakerGains map[string]float64) ([]byte, *Master, error) {
	before, err := m.measure(ctx, stitched)
	if err != nil {
		return nil, nil, err
	}

	wanted := TargetLUFS - before.IntegratedLUFS
	// The most this can be raised before the loudest inter-sample peak
	// crosses the ceiling. Below the target is a quieter render; above it
	// would be distortion.
	headroom := TargetTruePeakDBTP - before.TruePeakDBTP
	gain := wanted
	note := ""
	if gain > headroom {
		gain = headroom
		note = fmt.Sprintf("raised as far as the %.1f dBTP peak ceiling allows; reaching %.0f LUFS would have needed compression", TargetTruePeakDBTP, TargetLUFS)
	}
	gain = round1(gain)

	mastered := stitched
	if gain != 0 {
		adjusted, clipped, err := wav.ApplyGain(stitched, gain)
		if err != nil {
			return nil, nil, fmt.Errorf("apply master gain: %w", err)
		}
		if clipped > 0 {
			// The peak arithmetic should make this unreachable; if it ever
			// happens, say so rather than shipping clipped audio quietly.
			note = fmt.Sprintf("%d samples clipped applying %.1f dB; the render was left unmastered", clipped, gain)
			after, _ := m.measure(ctx, stitched)
			return stitched, &Master{
				TargetLUFS: TargetLUFS, TargetPeakDBTP: TargetTruePeakDBTP,
				Before: before, After: after, GainDB: 0,
				SpeakerGainsDB: speakerGains, TargetMet: false, Note: note,
			}, nil
		}
		mastered = adjusted
	}

	// Measure the result rather than predicting it: a recorded number that
	// was never observed is the thing this whole feature exists to avoid.
	after, err := m.measure(ctx, mastered)
	if err != nil {
		return nil, nil, err
	}
	return mastered, &Master{
		TargetLUFS:     TargetLUFS,
		TargetPeakDBTP: TargetTruePeakDBTP,
		Before:         before,
		After:          after,
		GainDB:         gain,
		SpeakerGainsDB: speakerGains,
		TargetMet:      math.Abs(after.IntegratedLUFS-TargetLUFS) <= 1.0,
		Note:           note,
	}, nil
}

func medianOf(values map[string]float64, keys []string) float64 {
	ordered := make([]float64, 0, len(keys))
	for _, key := range keys {
		ordered = append(ordered, values[key])
	}
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}

func round1(value float64) float64 {
	return math.Round(value*10) / 10
}
