package audiobook

import (
	"fmt"
	"time"

	"cpp-studio/internal/wav"
)

// chapterTimings mirrors the existing joins using actual PCM frame counts.
// The first chapter owns leading padding; the last owns trailing padding.
func chapterTimings(plan *DocumentPlan, units []narrationUnit, paths []string, crossfade, gap time.Duration) (uint32, []ChapterBoundary, error) {
	if plan == nil {
		return 0, nil, nil
	}
	if len(units) == 0 || len(units) != len(paths) {
		return 0, nil, fmt.Errorf("missing chapter audio")
	}
	var format wav.Format
	var position, previousTail, fadeFrames, gapFrames, padFrames int64
	var boundaries []ChapterBoundary
	for index, path := range paths {
		current, frames, err := wav.PCMFramesFile(path)
		if err != nil {
			return 0, nil, err
		}
		if frames <= 0 {
			return 0, nil, fmt.Errorf("chapter audio is empty")
		}
		if index == 0 {
			format = current
			padFrames = int64(float64(format.SampleRate) * artifactPad.Seconds())
			position = padFrames
			fadeFrames = int64(float64(format.SampleRate) * crossfade.Seconds())
			gapFrames = int64(float64(format.SampleRate) * gap.Seconds())
		} else if format != current {
			return 0, nil, fmt.Errorf("chapter audio formats do not match")
		}
		start, overlap := position, int64(0)
		if index > 0 {
			if crossfade > 0 {
				overlap = min(previousTail, fadeFrames, frames)
				start -= overlap
			} else {
				start += gapFrames
			}
		}
		chapterID := units[index].chapterID
		if len(boundaries) == 0 || boundaries[len(boundaries)-1].ChapterID != chapterID {
			chapterIndex := len(boundaries)
			if chapterIndex >= len(plan.Chapters) || chapterID != plan.Chapters[chapterIndex].ID {
				return 0, nil, fmt.Errorf("rendered audio does not follow its chapter plan")
			}
			marker := start
			if chapterIndex == 0 {
				marker = 0
			} else {
				if marker <= boundaries[chapterIndex-1].StartSample {
					return 0, nil, fmt.Errorf("chapter starts overlap completely")
				}
				boundaries[chapterIndex-1].EndSample = marker
			}
			boundaries = append(boundaries, ChapterBoundary{ChapterID: chapterID, StartSample: marker})
		}
		position = start + frames
		previousTail = min(fadeFrames, frames-overlap)
	}
	if len(boundaries) != len(plan.Chapters) {
		return 0, nil, fmt.Errorf("a chapter has no rendered audio")
	}
	boundaries[len(boundaries)-1].EndSample = position + padFrames
	return format.SampleRate, boundaries, nil
}
