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
	"os"
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
