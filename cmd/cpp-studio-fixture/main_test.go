package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerHealthAndChat(t *testing.T) {
	server := httptest.NewServer(newFixtureHandler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var health map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if health["status"] != "ok" || health["fixture"] != true {
		t.Fatalf("unexpected health body: %#v", health)
	}

	body := strings.NewReader(`{"model":"ignored-by-fixture","messages":[{"role":"system","content":"ignored"},{"role":"user","content":"first"},{"role":"assistant","content":"ignored"},{"role":"user","content":"last user message"}]}`)
	resp, err = http.Post(server.URL+"/v1/chat/completions", "application/json", body)
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var chat struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chat); err != nil {
		t.Fatalf("decode chat: %v", err)
	}
	if chat.ID == "" || chat.Object != "chat.completion" || chat.Model != fixtureModel {
		t.Fatalf("unexpected chat envelope: %#v", chat)
	}
	if len(chat.Choices) != 1 {
		t.Fatalf("len(choices) = %d, want 1", len(chat.Choices))
	}
	got := chat.Choices[0].Message.Content
	if chat.Choices[0].Message.Role != "assistant" || !strings.Contains(got, "last user message") {
		t.Fatalf("unexpected assistant reply: role=%q content=%q", chat.Choices[0].Message.Role, got)
	}
	if chat.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop", chat.Choices[0].FinishReason)
	}
}

func TestWhisperRejectsNonWAV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte("not a wav"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := run([]string{"whisper", "-f", path}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run whisper succeeded for non-WAV input")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(err.Error(), "invalid wav") {
		t.Fatalf("error = %q, want invalid wav", err.Error())
	}
}

func TestWhisperAcceptsWAV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.wav")
	writeMinimalWAV(t, path)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"whisper", "-f", path}, &stdout, &stderr); err != nil {
		t.Fatalf("run whisper: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "fixture transcript" {
		t.Fatalf("stdout = %q, want fixture transcript", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestSpeechWritesValidWAV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested-name.wav")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"speech", "--text", "hello", "--out", path}, &stdout, &stderr); err != nil {
		t.Fatalf("run speech: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("unexpected output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	assertFixtureWAV(t, data)
}

func TestSpeechRequiresText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.wav")

	var stdout, stderr bytes.Buffer
	err := run([]string{"speech", "--text", " ", "--out", path}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run speech succeeded with empty text")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("error = %q, want non-empty", err.Error())
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("output file exists after rejected speech input: %v", statErr)
	}
}

func TestImageWritesValidPNG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"image", "--prompt", "a fixture image", "--output", path, "--width", "512", "--height", "512"}, &stdout, &stderr); err != nil {
		t.Fatalf("run image: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("unexpected output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read png: %v", err)
	}
	assertFixturePNG(t, data)
}

func TestImageSupportsSDCLIShortAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"image", "-p", "a fixture image", "-o", path, "-W", "256", "-H", "256"}, &stdout, &stderr); err != nil {
		t.Fatalf("run image with aliases: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("unexpected output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read png: %v", err)
	}
	assertFixturePNG(t, data)
}

func TestImageRequiresPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")

	var stdout, stderr bytes.Buffer
	err := run([]string{"image", "--prompt", " ", "--output", path}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run image succeeded with empty prompt")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("error = %q, want non-empty", err.Error())
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("output file exists after rejected image input: %v", statErr)
	}
}

func TestImageRequireSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"image", "--prompt", "a fixture image", "--output", path, "--width", "64", "--height", "64", "--require-size", "64x64"}, &stdout, &stderr); err != nil {
		t.Fatalf("run image: %v", err)
	}

	badPath := filepath.Join(t.TempDir(), "bad-image.png")
	stdout.Reset()
	stderr.Reset()
	err := run([]string{"image", "--prompt", "a fixture image", "--output", badPath, "--width", "32", "--height", "64", "--require-size", "64x64"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run image succeeded with mismatched required size")
	}
	if !strings.Contains(err.Error(), "required size") {
		t.Fatalf("error = %q, want required size", err.Error())
	}
	if _, statErr := os.Stat(badPath); !os.IsNotExist(statErr) {
		t.Fatalf("output file exists after rejected image size: %v", statErr)
	}
}

func TestSpeechRequireContains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.wav")

	var stdout, stderr bytes.Buffer
	err := run([]string{"speech", "--text", "hello", "--require-contains", "fixture transcript", "--out", path}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run speech succeeded without required text")
	}
	if !strings.Contains(err.Error(), "fixture transcript") {
		t.Fatalf("error = %q, want required substring", err.Error())
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("output file exists after rejected speech input: %v", statErr)
	}

	if err := run([]string{"speech", "--text", "fixture transcript", "--require-contains", "fixture transcript", "--out", path}, &stdout, &stderr); err != nil {
		t.Fatalf("run speech with required text: %v", err)
	}
}

func assertFixturePNG(t *testing.T, data []byte) {
	t.Helper()
	signature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if len(data) < len(signature) {
		t.Fatalf("png length = %d, want at least %d", len(data), len(signature))
	}
	if !bytes.Equal(data[:len(signature)], signature) {
		t.Fatalf("missing PNG signature")
	}
}

func writeMinimalWAV(t *testing.T, path string) {
	t.Helper()
	if err := writeFixtureWAV(path); err != nil {
		t.Fatalf("write fixture wav: %v", err)
	}
}

func assertFixtureWAV(t *testing.T, data []byte) {
	t.Helper()
	if len(data) < 44 {
		t.Fatalf("wav length = %d, want at least 44", len(data))
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("missing RIFF/WAVE header")
	}
	if string(data[12:16]) != "fmt " {
		t.Fatalf("missing fmt chunk")
	}
	if got := binary.LittleEndian.Uint16(data[20:22]); got != 1 {
		t.Fatalf("audio format = %d, want PCM format 1", got)
	}
	if got := binary.LittleEndian.Uint16(data[22:24]); got != 1 {
		t.Fatalf("channels = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint32(data[24:28]); got != 16000 {
		t.Fatalf("sample rate = %d, want 16000", got)
	}
	if got := binary.LittleEndian.Uint16(data[34:36]); got != 16 {
		t.Fatalf("bits per sample = %d, want 16", got)
	}
	if string(data[36:40]) != "data" {
		t.Fatalf("missing data chunk")
	}
	if dataSize := int(binary.LittleEndian.Uint32(data[40:44])); dataSize != len(data)-44 {
		t.Fatalf("data chunk size = %d, want %d", dataSize, len(data)-44)
	}
}
