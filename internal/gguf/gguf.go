// Package gguf reads just enough of a GGUF model file's header to answer
// the console's preflight questions: what architecture is this, and is it
// a mixture-of-experts model. It is not a general GGUF library — tensor
// data is never touched, and the walk stops before the tokenizer section,
// which holds the vocabulary arrays that make up most of the metadata.
package gguf

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

// Info is what the header walk resolves. A dense model reports
// ExpertCount 0 — the key is simply absent from its header.
type Info struct {
	// Version is the GGUF container version (currently 2 or 3).
	Version uint32
	// Architecture is general.architecture, e.g. "llama", "qwen35moe".
	Architecture string
	// ExpertCount is {arch}.expert_count; 0 means dense.
	ExpertCount uint32
	// SizeLabel is general.size_label when present, e.g. "35B-A3B".
	SizeLabel string
}

// GGUF value type ids, from the format spec.
const (
	typeUint8   = 0
	typeInt8    = 1
	typeUint16  = 2
	typeInt16   = 3
	typeUint32  = 4
	typeInt32   = 5
	typeFloat32 = 6
	typeBool    = 7
	typeString  = 8
	typeArray   = 9
	typeUint64  = 10
	typeInt64   = 11
	typeFloat64 = 12
)

// Sanity bounds: a header that claims more than these is malformed (or
// hostile), and the caller should fall back to size-only judgement.
const (
	maxKVCount    = 4096
	maxStringLen  = 16 << 20 // 16 MiB
	maxArrayCount = 1 << 20
)

// ReadInfo opens the file and walks header key-values until it has what
// it needs or reaches the tokenizer section. Any structural problem is an
// error; callers are expected to degrade to file-size-only judgement.
func ReadInfo(path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	return readInfo(bufio.NewReaderSize(f, 1<<16))
}

func readInfo(r *bufio.Reader) (Info, error) {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return Info{}, fmt.Errorf("read magic: %w", err)
	}
	if string(magic[:]) != "GGUF" {
		return Info{}, fmt.Errorf("not a GGUF file (magic %q)", magic[:])
	}
	version, err := readU32(r)
	if err != nil {
		return Info{}, fmt.Errorf("read version: %w", err)
	}
	if version != 2 && version != 3 {
		return Info{}, fmt.Errorf("unsupported GGUF version %d", version)
	}
	info := Info{Version: version}
	if _, err := readU64(r); err != nil { // tensor count, unused
		return Info{}, fmt.Errorf("read tensor count: %w", err)
	}
	kvCount, err := readU64(r)
	if err != nil {
		return Info{}, fmt.Errorf("read kv count: %w", err)
	}
	if kvCount > maxKVCount {
		return Info{}, fmt.Errorf("implausible kv count %d", kvCount)
	}

	var expertSeen bool
	for i := uint64(0); i < kvCount; i++ {
		key, err := readString(r)
		if err != nil {
			return Info{}, fmt.Errorf("read key %d: %w", i, err)
		}
		// The tokenizer section trails the model keys in every
		// llama.cpp-converted file and holds the huge vocab arrays;
		// everything this package answers is resolved before it.
		if strings.HasPrefix(key, "tokenizer.") {
			return info, nil
		}
		valueType, err := readU32(r)
		if err != nil {
			return Info{}, fmt.Errorf("read value type of %q: %w", key, err)
		}
		switch {
		case key == "general.architecture" && valueType == typeString:
			if info.Architecture, err = readString(r); err != nil {
				return Info{}, fmt.Errorf("read architecture: %w", err)
			}
		case key == "general.size_label" && valueType == typeString:
			if info.SizeLabel, err = readString(r); err != nil {
				return Info{}, fmt.Errorf("read size label: %w", err)
			}
		case strings.HasSuffix(key, ".expert_count"):
			n, err := readUint(r, valueType)
			if err != nil {
				return Info{}, fmt.Errorf("read %q: %w", key, err)
			}
			info.ExpertCount = uint32(n)
			expertSeen = true
		default:
			if err := skipValue(r, valueType); err != nil {
				return Info{}, fmt.Errorf("skip %q: %w", key, err)
			}
		}
		if info.Architecture != "" && info.SizeLabel != "" && expertSeen {
			return info, nil
		}
	}
	return info, nil
}

func readU32(r io.Reader) (uint32, error) {
	var v uint32
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

func readU64(r io.Reader) (uint64, error) {
	var v uint64
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

func readString(r *bufio.Reader) (string, error) {
	n, err := readU64(r)
	if err != nil {
		return "", err
	}
	if n > maxStringLen {
		return "", fmt.Errorf("implausible string length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// readUint reads any integer-typed value; expert_count is uint32 in most
// files but converters have written other widths.
func readUint(r *bufio.Reader, valueType uint32) (uint64, error) {
	switch valueType {
	case typeUint8, typeInt8, typeBool:
		b, err := r.ReadByte()
		return uint64(b), err
	case typeUint16, typeInt16:
		var v uint16
		err := binary.Read(r, binary.LittleEndian, &v)
		return uint64(v), err
	case typeUint32, typeInt32:
		v, err := readU32(r)
		return uint64(v), err
	case typeUint64, typeInt64:
		return readU64(r)
	default:
		return 0, fmt.Errorf("not an integer type (%d)", valueType)
	}
}

func skipValue(r *bufio.Reader, valueType uint32) error {
	if size, ok := scalarSize(valueType); ok {
		_, err := io.CopyN(io.Discard, r, size)
		return err
	}
	switch valueType {
	case typeString:
		_, err := readString(r)
		return err
	case typeArray:
		elemType, err := readU32(r)
		if err != nil {
			return err
		}
		count, err := readU64(r)
		if err != nil {
			return err
		}
		if count > maxArrayCount {
			return fmt.Errorf("implausible array count %d", count)
		}
		if size, ok := scalarSize(elemType); ok {
			_, err := io.CopyN(io.Discard, r, int64(count)*size)
			return err
		}
		for i := uint64(0); i < count; i++ {
			if err := skipValue(r, elemType); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown value type %d", valueType)
	}
}

func scalarSize(valueType uint32) (int64, bool) {
	switch valueType {
	case typeUint8, typeInt8, typeBool:
		return 1, true
	case typeUint16, typeInt16:
		return 2, true
	case typeUint32, typeInt32, typeFloat32:
		return 4, true
	case typeUint64, typeInt64, typeFloat64:
		return 8, true
	default:
		return 0, false
	}
}
