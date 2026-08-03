package wav

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
)

const MaxRIFFDataBytes = uint64(math.MaxUint32 - 36)

type filePCM struct {
	format Format
	offset int64
	size   uint64
}

// CheckRIFFDataSize refuses canonical WAV output that cannot be represented
// by RIFF's 32-bit size fields. RF64 is intentionally outside cpp-studio.
func CheckRIFFDataSize(size uint64) error {
	if size > MaxRIFFDataBytes {
		return fmt.Errorf("audiobook WAV would exceed the 32-bit RIFF limit; narrate in parts")
	}
	return nil
}

// AssembleFiles writes one canonical PCM WAV without retaining whole-book PCM
// in memory. At most one section and a boundary window are held at a time.
func AssembleFiles(outputPath string, sectionPaths []string, crossfade, lead, trail time.Duration) error {
	if len(sectionPaths) == 0 {
		return fmt.Errorf("no WAV sections to assemble")
	}
	sections := make([]filePCM, len(sectionPaths))
	var format Format
	for i, path := range sectionPaths {
		section, err := inspectPCMFile(path)
		if err != nil {
			return fmt.Errorf("section %d: %w", i+1, err)
		}
		if i == 0 {
			format = section.format
			if format.BitsPerSample != 16 {
				return fmt.Errorf("section crossfade needs 16-bit PCM, got %d-bit", format.BitsPerSample)
			}
		} else if section.format != format {
			return fmt.Errorf("section %d format %+v does not match first section %+v", i+1, section.format, format)
		}
		sections[i] = section
	}
	blockAlign := uint64(format.Channels) * uint64(format.BitsPerSample) / 8
	if blockAlign == 0 {
		return fmt.Errorf("invalid WAV: zero block align")
	}
	fadeBytes := durationBytes(format, crossfade)
	leadBytes := durationBytes(format, lead)
	trailBytes := durationBytes(format, trail)
	outputBytes := leadBytes + trailBytes
	previousTail := uint64(0)
	for i, section := range sections {
		remaining := section.size
		if i > 0 {
			overlap := minUint64(previousTail, fadeBytes, remaining)
			outputBytes -= overlap
			remaining -= overlap
		}
		outputBytes += section.size
		previousTail = minUint64(fadeBytes, remaining)
	}
	if err := CheckRIFFDataSize(outputBytes); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(outputPath), "."+filepath.Base(outputPath)+".assembling-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := writeCanonicalHeader(tmp, format, 0); err != nil {
		return err
	}
	if err := writeSilence(tmp, leadBytes); err != nil {
		return err
	}
	var tail []byte
	for i, section := range sections {
		pcm, err := readPCM(sectionPaths[i], section)
		if err != nil {
			return fmt.Errorf("read section %d: %w", i+1, err)
		}
		start := 0
		if i > 0 {
			overlap := int(minUint64(uint64(len(tail)), fadeBytes, uint64(len(pcm))))
			if _, err := tmp.Write(tail[:len(tail)-overlap]); err != nil {
				return err
			}
			mixed := equalPowerCrossfade(tail[len(tail)-overlap:], pcm[:overlap], int(blockAlign))
			if _, err := tmp.Write(mixed); err != nil {
				return err
			}
			start = overlap
		}
		remaining := pcm[start:]
		tailSize := int(minUint64(fadeBytes, uint64(len(remaining))))
		if _, err := tmp.Write(remaining[:len(remaining)-tailSize]); err != nil {
			return err
		}
		tail = append(tail[:0], remaining[len(remaining)-tailSize:]...)
	}
	if _, err := tmp.Write(tail); err != nil {
		return err
	}
	if err := writeSilence(tmp, trailBytes); err != nil {
		return err
	}
	if err := patchCanonicalSizes(tmp, outputBytes); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	assembled, err := inspectPCMFile(tmpPath)
	if err != nil || assembled.size != outputBytes || assembled.format != format {
		return fmt.Errorf("validate assembled WAV: format=%+v bytes=%d err=%v", assembled.format, assembled.size, err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return err
	}
	committed = true
	return nil
}

func inspectPCMFile(path string) (filePCM, error) {
	file, err := os.Open(path)
	if err != nil {
		return filePCM{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return filePCM{}, err
	}
	header := make([]byte, 12)
	if _, err := io.ReadFull(file, header); err != nil || string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return filePCM{}, ErrNotWAV
	}
	var result filePCM
	haveFormat := false
	for offset := int64(12); offset+8 <= info.Size(); {
		chunkHeader := make([]byte, 8)
		if _, err := file.ReadAt(chunkHeader, offset); err != nil {
			return filePCM{}, err
		}
		size := uint64(binary.LittleEndian.Uint32(chunkHeader[4:]))
		body := offset + 8
		if size > uint64(info.Size()-body) {
			return filePCM{}, fmt.Errorf("invalid WAV: chunk %q overruns the file", chunkHeader[:4])
		}
		switch string(chunkHeader[:4]) {
		case "fmt ":
			if size < 16 {
				return filePCM{}, fmt.Errorf("invalid WAV: short fmt chunk")
			}
			bodyBytes := make([]byte, 16)
			if _, err := file.ReadAt(bodyBytes, body); err != nil {
				return filePCM{}, err
			}
			if binary.LittleEndian.Uint16(bodyBytes[:2]) != 1 {
				return filePCM{}, fmt.Errorf("unsupported WAV: only integer PCM is accepted")
			}
			result.format = Format{
				Channels: binary.LittleEndian.Uint16(bodyBytes[2:4]), SampleRate: binary.LittleEndian.Uint32(bodyBytes[4:8]),
				BitsPerSample: binary.LittleEndian.Uint16(bodyBytes[14:16]),
			}
			haveFormat = true
		case "data":
			result.offset, result.size = body, size
		}
		offset = body + int64(size)
		if size%2 == 1 {
			offset++
		}
	}
	if !haveFormat || result.offset == 0 {
		return filePCM{}, fmt.Errorf("invalid WAV: missing fmt or data chunk")
	}
	blockAlign := uint64(result.format.Channels) * uint64(result.format.BitsPerSample) / 8
	if blockAlign == 0 || result.size%blockAlign != 0 {
		return filePCM{}, fmt.Errorf("invalid WAV: PCM data is not frame-aligned")
	}
	return result, nil
}

func readPCM(path string, section filePCM) ([]byte, error) {
	if section.size > uint64(maxInt()) {
		return nil, fmt.Errorf("section is too large for this process")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data := make([]byte, int(section.size))
	_, err = file.ReadAt(data, section.offset)
	return data, err
}

func equalPowerCrossfade(left, right []byte, blockAlign int) []byte {
	if len(left) != len(right) || len(left) == 0 {
		return nil
	}
	frames := len(left) / blockAlign
	out := make([]byte, len(left))
	for frame := 0; frame < frames; frame++ {
		t := 0.5
		if frames > 1 {
			t = float64(frame) / float64(frames-1)
		}
		leftGain, rightGain := math.Cos(t*math.Pi/2), math.Sin(t*math.Pi/2)
		for offset := frame * blockAlign; offset < (frame+1)*blockAlign; offset += 2 {
			mixed := math.Round(float64(int16(binary.LittleEndian.Uint16(left[offset:]))) * leftGain)
			mixed += math.Round(float64(int16(binary.LittleEndian.Uint16(right[offset:]))) * rightGain)
			if mixed > math.MaxInt16 {
				mixed = math.MaxInt16
			} else if mixed < math.MinInt16 {
				mixed = math.MinInt16
			}
			binary.LittleEndian.PutUint16(out[offset:], uint16(int16(mixed)))
		}
	}
	return out
}

func durationBytes(format Format, duration time.Duration) uint64 {
	if duration <= 0 {
		return 0
	}
	frames := uint64(float64(format.SampleRate) * duration.Seconds())
	return frames * uint64(format.Channels) * uint64(format.BitsPerSample) / 8
}

func minUint64(values ...uint64) uint64 {
	minimum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}

func maxInt() int { return int(^uint(0) >> 1) }

func writeCanonicalHeader(writer io.Writer, format Format, dataSize uint32) error {
	byteRate := format.SampleRate * uint32(format.Channels) * uint32(format.BitsPerSample) / 8
	blockAlign := format.Channels * format.BitsPerSample / 8
	values := []any{
		[]byte("RIFF"), uint32(36) + dataSize, []byte("WAVEfmt "), uint32(16), uint16(1), format.Channels,
		format.SampleRate, byteRate, blockAlign, format.BitsPerSample, []byte("data"), dataSize,
	}
	for _, value := range values {
		if err := binary.Write(writer, binary.LittleEndian, value); err != nil {
			return err
		}
	}
	return nil
}

func patchCanonicalSizes(file *os.File, dataSize uint64) error {
	if err := CheckRIFFDataSize(dataSize); err != nil {
		return err
	}
	if _, err := file.Seek(4, io.SeekStart); err != nil {
		return err
	}
	if err := binary.Write(file, binary.LittleEndian, uint32(36+dataSize)); err != nil {
		return err
	}
	if _, err := file.Seek(40, io.SeekStart); err != nil {
		return err
	}
	return binary.Write(file, binary.LittleEndian, uint32(dataSize))
}

func writeSilence(writer io.Writer, size uint64) error {
	zeroes := make([]byte, 64*1024)
	for size > 0 {
		chunk := uint64(len(zeroes))
		if size < chunk {
			chunk = size
		}
		if _, err := writer.Write(zeroes[:int(chunk)]); err != nil {
			return err
		}
		size -= chunk
	}
	return nil
}

func DurationFile(path string) (time.Duration, error) {
	section, err := inspectPCMFile(path)
	if err != nil {
		return 0, err
	}
	bytesPerSecond := uint64(section.format.SampleRate) * uint64(section.format.Channels) * uint64(section.format.BitsPerSample) / 8
	if bytesPerSecond == 0 {
		return 0, fmt.Errorf("invalid WAV: zero byte rate")
	}
	return time.Duration(float64(section.size) / float64(bytesPerSecond) * float64(time.Second)), nil
}
