// Package wav is the single home for WAV format knowledge in cpp-studio:
// what counts as a valid WAV, and how the deterministic fixture tone used by
// tests and smoke runs is built.
package wav

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"
)

// ErrNotWAV reports data that does not begin with a RIFF/WAVE header.
var ErrNotWAV = errors.New("not a valid WAV: expected RIFF/WAVE header")

// ValidateHeader checks that r begins with a RIFF/WAVE header.
func ValidateHeader(r io.Reader) error {
	header := make([]byte, 12)
	if _, err := io.ReadFull(r, header); err != nil {
		return ErrNotWAV
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return ErrNotWAV
	}
	return nil
}

// ValidateBytes checks that data begins with a RIFF/WAVE header.
func ValidateBytes(data []byte) error {
	return ValidateHeader(bytes.NewReader(data))
}

// ValidateFile checks that the file at path begins with a RIFF/WAVE header.
func ValidateFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open audio file: %v", err)
	}
	defer file.Close()
	return ValidateHeader(file)
}

// Format describes the PCM shape of a WAV clip.
type Format struct {
	Channels      uint16
	SampleRate    uint32
	BitsPerSample uint16
}

// TimelinePlacement is one nondestructive source slice in a mixed WAV.
type TimelinePlacement struct {
	Data       []byte
	StartMS    int64
	SourceInMS int64
	DurationMS int64
}

// TimelineMixFormat is the one delivery format used by deterministic timeline
// renders. Sources are converted to it while they are mixed.
func TimelineMixFormat() Format {
	return Format{Channels: toneChannels, SampleRate: ToneSampleRate, BitsPerSample: toneBitsPerSample}
}

// MixTimeline renders placements onto a silent canvas. PCM sources are
// deterministically resampled and downmixed to TimelineMixFormat; samples are
// summed across overlaps and clamped once at the final output boundary.
func MixTimeline(durationMS int64, placements []TimelinePlacement) ([]byte, error) {
	if durationMS <= 0 {
		return nil, fmt.Errorf("timeline duration must be positive")
	}
	format := TimelineMixFormat()
	type decodedPlacement struct {
		format                  Format
		pcm                     []byte
		startFrame, sourceFrame int64
		frameCount              int64
	}
	decoded := make([]decodedPlacement, 0, len(placements))
	for i, placement := range placements {
		if placement.StartMS < 0 || placement.SourceInMS < 0 || placement.DurationMS <= 0 {
			return nil, fmt.Errorf("placement %d has invalid timing", i+1)
		}
		clipFormat, pcm, err := Decode(placement.Data)
		if err != nil {
			return nil, fmt.Errorf("placement %d: %w", i+1, err)
		}
		if clipFormat.Channels == 0 || clipFormat.SampleRate == 0 ||
			(clipFormat.BitsPerSample != 8 && clipFormat.BitsPerSample != 16 && clipFormat.BitsPerSample != 24 && clipFormat.BitsPerSample != 32) {
			return nil, fmt.Errorf("placement %d has unsupported PCM format %+v", i+1, clipFormat)
		}
		blockAlign := int64(clipFormat.Channels) * int64(clipFormat.BitsPerSample/8)
		if int64(len(pcm))%blockAlign != 0 {
			return nil, fmt.Errorf("placement %d PCM is not frame-aligned", i+1)
		}
		outputRate := int64(format.SampleRate)
		sourceRate := int64(clipFormat.SampleRate)
		if placement.StartMS > math.MaxInt64/outputRate || placement.DurationMS > math.MaxInt64/outputRate ||
			placement.SourceInMS > math.MaxInt64/sourceRate {
			return nil, fmt.Errorf("placement %d timing is too large", i+1)
		}
		startFrame := placement.StartMS * outputRate / 1000
		sourceFrame := placement.SourceInMS * sourceRate / 1000
		frameCount := placement.DurationMS * outputRate / 1000
		if frameCount <= 0 {
			return nil, fmt.Errorf("placement %d trim exceeds its source", i+1)
		}
		lastSourceFrame := sourceFrame + (frameCount-1)*sourceRate/outputRate
		if sourceFrame < 0 || lastSourceFrame >= int64(len(pcm))/blockAlign {
			return nil, fmt.Errorf("placement %d trim exceeds its source", i+1)
		}
		decoded = append(decoded, decodedPlacement{format: clipFormat, pcm: pcm, startFrame: startFrame, sourceFrame: sourceFrame, frameCount: frameCount})
	}

	if durationMS > math.MaxInt64/int64(format.SampleRate) {
		return nil, fmt.Errorf("timeline duration is too large")
	}
	outputFrames := durationMS * int64(format.SampleRate) / 1000
	if outputFrames <= 0 {
		return nil, fmt.Errorf("timeline duration is shorter than one sample")
	}
	const maxTimelineWAVBytes = 256 * 1024 * 1024
	if outputFrames > (maxTimelineWAVBytes-44)/2 || outputFrames > int64(int(^uint(0)>>1))/8 {
		return nil, fmt.Errorf("mixed WAV is too large")
	}
	mix := make([]int64, int(outputFrames))
	for i, placement := range decoded {
		if placement.startFrame > outputFrames-placement.frameCount {
			return nil, fmt.Errorf("placement %d exceeds the timeline", i+1)
		}
		for frame := int64(0); frame < placement.frameCount; frame++ {
			sourceFrame := placement.sourceFrame + frame*int64(placement.format.SampleRate)/int64(format.SampleRate)
			var sum int64
			for channel := int64(0); channel < int64(placement.format.Channels); channel++ {
				sourceSample := sourceFrame*int64(placement.format.Channels) + channel
				sum += pcmSample16(placement.pcm, placement.format.BitsPerSample, sourceSample)
			}
			mix[placement.startFrame+frame] += sum / int64(placement.format.Channels)
		}
	}
	pcm := make([]byte, len(mix)*2)
	for i, sample := range mix {
		if sample > math.MaxInt16 {
			sample = math.MaxInt16
		} else if sample < math.MinInt16 {
			sample = math.MinInt16
		}
		binary.LittleEndian.PutUint16(pcm[i*2:i*2+2], uint16(int16(sample)))
	}
	return Encode(format, pcm), nil
}

func pcmSample16(pcm []byte, bits uint16, sample int64) int64 {
	bytesPerSample := int64(bits / 8)
	offset := sample * bytesPerSample
	switch bits {
	case 8:
		return (int64(pcm[offset]) - 128) << 8
	case 16:
		return int64(int16(binary.LittleEndian.Uint16(pcm[offset : offset+2])))
	case 24:
		value := int32(pcm[offset]) | int32(pcm[offset+1])<<8 | int32(pcm[offset+2])<<16
		if value&0x800000 != 0 {
			value |= ^int32(0xffffff)
		}
		return int64(value >> 8)
	case 32:
		return int64(int32(binary.LittleEndian.Uint32(pcm[offset:offset+4])) >> 16)
	default:
		return 0
	}
}

// Decode splits a WAV into its format and raw PCM data by walking the RIFF
// chunks; chunks other than fmt and data (LIST, cue, ...) are skipped.
func Decode(data []byte) (Format, []byte, error) {
	if err := ValidateBytes(data); err != nil {
		return Format{}, nil, err
	}

	var format Format
	var pcm []byte
	haveFormat := false
	offset := 12
	for offset+8 <= len(data) {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		if size < 0 || body+size > len(data) {
			return Format{}, nil, fmt.Errorf("invalid WAV: chunk %q overruns the file", id)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return Format{}, nil, fmt.Errorf("invalid WAV: fmt chunk is %d bytes", size)
			}
			if audioFormat := binary.LittleEndian.Uint16(data[body : body+2]); audioFormat != 1 {
				return Format{}, nil, fmt.Errorf("invalid WAV: unsupported audio format %d (PCM required)", audioFormat)
			}
			format = Format{
				Channels:      binary.LittleEndian.Uint16(data[body+2 : body+4]),
				SampleRate:    binary.LittleEndian.Uint32(data[body+4 : body+8]),
				BitsPerSample: binary.LittleEndian.Uint16(data[body+14 : body+16]),
			}
			haveFormat = true
		case "data":
			pcm = data[body : body+size]
		}
		offset = body + size
		if size%2 == 1 {
			offset++ // RIFF chunks are word-aligned
		}
	}
	if !haveFormat {
		return Format{}, nil, fmt.Errorf("invalid WAV: no fmt chunk")
	}
	if pcm == nil {
		return Format{}, nil, fmt.Errorf("invalid WAV: no data chunk")
	}
	return format, pcm, nil
}

// Encode wraps raw PCM data in a canonical 44-byte WAV header.
func Encode(format Format, pcm []byte) []byte {
	var out bytes.Buffer
	dataSize := uint32(len(pcm))
	byteRate := format.SampleRate * uint32(format.Channels) * uint32(format.BitsPerSample) / 8
	blockAlign := format.Channels * format.BitsPerSample / 8

	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(36)+dataSize)
	out.WriteString("WAVE")
	out.WriteString("fmt ")
	_ = binary.Write(&out, binary.LittleEndian, uint32(16))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))
	_ = binary.Write(&out, binary.LittleEndian, format.Channels)
	_ = binary.Write(&out, binary.LittleEndian, format.SampleRate)
	_ = binary.Write(&out, binary.LittleEndian, byteRate)
	_ = binary.Write(&out, binary.LittleEndian, blockAlign)
	_ = binary.Write(&out, binary.LittleEndian, format.BitsPerSample)
	out.WriteString("data")
	_ = binary.Write(&out, binary.LittleEndian, dataSize)
	out.Write(pcm)
	return out.Bytes()
}

// Duration reports the play time of a WAV clip.
func Duration(data []byte) (time.Duration, error) {
	format, pcm, err := Decode(data)
	if err != nil {
		return 0, err
	}
	bytesPerSecond := int(format.SampleRate) * int(format.Channels) * int(format.BitsPerSample) / 8
	if bytesPerSecond <= 0 {
		return 0, fmt.Errorf("invalid WAV: zero byte rate")
	}
	return time.Duration(float64(len(pcm)) / float64(bytesPerSecond) * float64(time.Second)), nil
}

// Concatenate joins clips into one WAV, inserting gap of silence between
// consecutive clips. Every clip must share the same PCM format.
func Concatenate(clips [][]byte, gap time.Duration) ([]byte, error) {
	if len(clips) == 0 {
		return nil, fmt.Errorf("no clips to concatenate")
	}
	gaps := make([]time.Duration, len(clips))
	for i := range gaps {
		gaps[i] = gap
	}
	return ConcatenateGaps(clips, gaps)
}

// ConcatenateGaps is Concatenate with the silence chosen per clip: gaps[i]
// is the silence placed *before* clips[i]. gaps[0] is ignored — nothing
// precedes the first clip — which lets callers index gaps and clips the
// same way rather than keeping an off-by-one in their heads.
func ConcatenateGaps(clips [][]byte, gaps []time.Duration) ([]byte, error) {
	return ConcatenateGapsFrom(len(clips), gaps, func(i int) ([]byte, error) {
		return clips[i], nil
	})
}

// ConcatenateGapsFrom is ConcatenateGaps with clips pulled one at a time:
// load(i) supplies clip i, and it is released before the next is loaded.
// Peak memory is the joined PCM plus a single clip, which is what makes an
// episode-length stitch feasible — holding three hundred clips at once is
// exactly the thing this variant exists to avoid.
func ConcatenateGapsFrom(count int, gaps []time.Duration, load func(i int) ([]byte, error)) ([]byte, error) {
	if count == 0 {
		return nil, fmt.Errorf("no clips to concatenate")
	}
	if len(gaps) != count {
		return nil, fmt.Errorf("have %d gaps for %d clips", len(gaps), count)
	}

	var format Format
	var pcm bytes.Buffer
	for i := 0; i < count; i++ {
		clip, err := load(i)
		if err != nil {
			return nil, fmt.Errorf("clip %d: %w", i+1, err)
		}
		clipFormat, clipPCM, err := Decode(clip)
		if err != nil {
			return nil, fmt.Errorf("clip %d: %v", i+1, err)
		}
		if i == 0 {
			format = clipFormat
		} else if clipFormat != format {
			return nil, fmt.Errorf("clip %d format %+v does not match first clip %+v", i+1, clipFormat, format)
		}
		if i > 0 && gaps[i] > 0 {
			gapBytes := int(float64(format.SampleRate)*gaps[i].Seconds()) * int(format.Channels) * int(format.BitsPerSample) / 8
			// Keep silence aligned to whole frames.
			blockAlign := int(format.Channels) * int(format.BitsPerSample) / 8
			if blockAlign > 0 {
				gapBytes -= gapBytes % blockAlign
			}
			pcm.Write(make([]byte, gapBytes))
		}
		pcm.Write(clipPCM)
	}
	return Encode(format, pcm.Bytes()), nil
}

// ApplyGain scales a clip by db decibels. Gain is the whole of what
// "levelling" and "mastering" mean here: no compression, no limiting, just
// the same multiplication applied to every sample, which is what keeps a
// performance sounding like the performance.
//
// Samples that would leave the 16-bit range are clamped rather than allowed
// to wrap, but a caller that has done its arithmetic against a true-peak
// measurement should never reach that clamp; ClippedSamples reports whether
// it happened so the caller can stop claiming the gain was transparent.
func ApplyGain(data []byte, db float64) ([]byte, int, error) {
	format, pcm, err := Decode(data)
	if err != nil {
		return nil, 0, err
	}
	if format.BitsPerSample != 16 {
		return nil, 0, fmt.Errorf("gain needs 16-bit PCM, got %d-bit", format.BitsPerSample)
	}
	if db == 0 {
		return data, 0, nil
	}
	scale := math.Pow(10, db/20)

	out := make([]byte, len(pcm))
	clipped := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := float64(int16(binary.LittleEndian.Uint16(pcm[i : i+2])))
		scaled := math.Round(sample * scale)
		switch {
		case scaled > math.MaxInt16:
			scaled = math.MaxInt16
			clipped++
		case scaled < math.MinInt16:
			scaled = math.MinInt16
			clipped++
		}
		binary.LittleEndian.PutUint16(out[i:i+2], uint16(int16(scaled)))
	}
	return Encode(format, out), clipped, nil
}

// PadSilence returns the clip with lead and trail silence spliced around its
// PCM data. Playback commonly swallows the first fraction of a second of a
// clip (Bluetooth wake-up, autoplay ramp-in), which listeners hear as speech
// clipped at the start or end; the padding absorbs that without touching the
// speech itself.
func PadSilence(data []byte, lead, trail time.Duration) ([]byte, error) {
	format, pcm, err := Decode(data)
	if err != nil {
		return nil, err
	}
	blockAlign := int(format.Channels) * int(format.BitsPerSample) / 8
	if blockAlign <= 0 {
		return nil, fmt.Errorf("invalid WAV: zero block align")
	}
	silence := func(d time.Duration) []byte {
		if d <= 0 {
			return nil
		}
		n := int(float64(format.SampleRate)*d.Seconds()) * blockAlign
		n -= n % blockAlign
		return make([]byte, n)
	}

	var out bytes.Buffer
	out.Write(silence(lead))
	out.Write(pcm)
	out.Write(silence(trail))
	return Encode(format, out.Bytes()), nil
}

// ToneSampleRate is the sample rate of SyntheticTone output.
const ToneSampleRate = 16000

const (
	toneChannels      = 1
	toneBitsPerSample = 16
)

// SyntheticTone returns a mono 16-bit 16 kHz square-wave WAV with the given
// number of samples. It is the shared deterministic fixture audio.
func SyntheticTone(sampleCount int) []byte {
	if sampleCount <= 0 {
		sampleCount = 1
	}

	var pcm bytes.Buffer
	for i := 0; i < sampleCount; i++ {
		sample := int16(1000)
		if i%2 == 1 {
			sample = -1000
		}
		_ = binary.Write(&pcm, binary.LittleEndian, sample)
	}

	var out bytes.Buffer
	dataSize := uint32(pcm.Len())
	byteRate := uint32(ToneSampleRate * toneChannels * toneBitsPerSample / 8)
	blockAlign := uint16(toneChannels * toneBitsPerSample / 8)

	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(36)+dataSize)
	out.WriteString("WAVE")
	out.WriteString("fmt ")
	_ = binary.Write(&out, binary.LittleEndian, uint32(16))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))
	_ = binary.Write(&out, binary.LittleEndian, uint16(toneChannels))
	_ = binary.Write(&out, binary.LittleEndian, uint32(ToneSampleRate))
	_ = binary.Write(&out, binary.LittleEndian, byteRate)
	_ = binary.Write(&out, binary.LittleEndian, blockAlign)
	_ = binary.Write(&out, binary.LittleEndian, uint16(toneBitsPerSample))
	out.WriteString("data")
	_ = binary.Write(&out, binary.LittleEndian, dataSize)
	out.Write(pcm.Bytes())
	return out.Bytes()
}
