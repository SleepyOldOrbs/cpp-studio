package gguf

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// headerBuilder assembles a minimal GGUF header byte-for-byte, the same
// way the WAV and PNG tests hand-craft their fixtures.
type headerBuilder struct {
	version uint32
	kvs     bytes.Buffer
	count   uint64
}

func newHeader() *headerBuilder { return &headerBuilder{version: 3} }

func (b *headerBuilder) u32(v uint32) { binary.Write(&b.kvs, binary.LittleEndian, v) }
func (b *headerBuilder) u64(v uint64) { binary.Write(&b.kvs, binary.LittleEndian, v) }
func (b *headerBuilder) str(s string) { b.u64(uint64(len(s))); b.kvs.WriteString(s) }

func (b *headerBuilder) kvString(key, value string) *headerBuilder {
	b.str(key)
	b.u32(typeString)
	b.str(value)
	b.count++
	return b
}

func (b *headerBuilder) kvU32(key string, value uint32) *headerBuilder {
	b.str(key)
	b.u32(typeUint32)
	b.u32(value)
	b.count++
	return b
}

func (b *headerBuilder) kvU64(key string, value uint64) *headerBuilder {
	b.str(key)
	b.u32(typeUint64)
	b.u64(value)
	b.count++
	return b
}

func (b *headerBuilder) kvF32Array(key string, values int) *headerBuilder {
	b.str(key)
	b.u32(typeArray)
	b.u32(typeFloat32)
	b.u64(uint64(values))
	for i := 0; i < values; i++ {
		b.u32(0x3f800000)
	}
	b.count++
	return b
}

func (b *headerBuilder) kvStringArray(key string, values ...string) *headerBuilder {
	b.str(key)
	b.u32(typeArray)
	b.u32(typeString)
	b.u64(uint64(len(values)))
	for _, v := range values {
		b.str(v)
	}
	b.count++
	return b
}

func (b *headerBuilder) bytes() []byte {
	var out bytes.Buffer
	out.WriteString("GGUF")
	binary.Write(&out, binary.LittleEndian, b.version)
	binary.Write(&out, binary.LittleEndian, uint64(0)) // tensor count
	binary.Write(&out, binary.LittleEndian, b.count)
	out.Write(b.kvs.Bytes())
	return out.Bytes()
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadInfoParsesArchitectureAndExpertCount(t *testing.T) {
	h := newHeader().
		kvString("general.architecture", "qwen35moe").
		kvString("general.size_label", "35B-A3B").
		kvU32("qwen35moe.block_count", 40).
		kvU32("qwen35moe.expert_count", 256)
	info, err := ReadInfo(writeTemp(t, h.bytes()))
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if info.Architecture != "qwen35moe" || info.ExpertCount != 256 || info.SizeLabel != "35B-A3B" {
		t.Fatalf("got %+v", info)
	}
}

func TestReadInfoDenseModelReportsZeroExperts(t *testing.T) {
	h := newHeader().
		kvString("general.architecture", "gemma3").
		kvU32("gemma3.block_count", 62).
		kvString("tokenizer.ggml.model", "llama")
	info, err := ReadInfo(writeTemp(t, h.bytes()))
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if info.Architecture != "gemma3" || info.ExpertCount != 0 {
		t.Fatalf("got %+v", info)
	}
}

func TestReadInfoStopsBeforeTokenizerKeys(t *testing.T) {
	// The expert count hides behind the tokenizer boundary; the walk must
	// stop rather than read on, and the caller gets the dense reading.
	h := newHeader().
		kvString("general.architecture", "llama").
		kvStringArray("tokenizer.ggml.tokens", strings.Repeat("x", 64), "y").
		kvU32("llama.expert_count", 8)
	info, err := ReadInfo(writeTemp(t, h.bytes()))
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if info.Architecture != "llama" || info.ExpertCount != 0 {
		t.Fatalf("expected the walk to stop at tokenizer keys, got %+v", info)
	}
}

func TestReadInfoSkipsArraysAndNestedValues(t *testing.T) {
	h := newHeader().
		kvString("general.license", "apache-2.0").
		kvStringArray("general.tags", "chat", "moe").
		kvF32Array("rope.scaling.factors", 32).
		kvU64("llama.context_length", 131072).
		kvString("general.architecture", "llama").
		kvU32("llama.expert_count", 8)
	info, err := ReadInfo(writeTemp(t, h.bytes()))
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if info.Architecture != "llama" || info.ExpertCount != 8 {
		t.Fatalf("got %+v", info)
	}
}

func TestReadInfoAcceptsVersion2(t *testing.T) {
	h := newHeader().kvString("general.architecture", "llama")
	h.version = 2
	info, err := ReadInfo(writeTemp(t, h.bytes()))
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if info.Architecture != "llama" {
		t.Fatalf("got %+v", info)
	}
}

func TestReadInfoRejectsBadMagicTruncationAndAbsurdCounts(t *testing.T) {
	valid := newHeader().kvString("general.architecture", "llama").bytes()

	absurdKVs := newHeader().bytes()
	binary.LittleEndian.PutUint64(absurdKVs[16:], maxKVCount+1)

	hugeString := newHeader().kvString("general.architecture", "llama").bytes()
	// The architecture value's length field sits after key len(8)+key+type(4).
	offset := 4 + 4 + 8 + 8 + 8 + len("general.architecture") + 4
	binary.LittleEndian.PutUint64(hugeString[offset:], maxStringLen+1)

	v1 := newHeader().kvString("general.architecture", "llama").bytes()
	binary.LittleEndian.PutUint32(v1[4:], 1)

	cases := []struct {
		name string
		data []byte
	}{
		{"badMagic", append([]byte("GGML"), valid[4:]...)},
		{"version1", v1},
		{"truncatedHeader", valid[:10]},
		{"truncatedValue", valid[:len(valid)-3]},
		{"absurdKVCount", absurdKVs},
		{"absurdStringLength", hugeString},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ReadInfo(writeTemp(t, tc.data)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
