package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const fixtureModel = "cpp-studio-fixture"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("missing fixture subcommand")
	}

	switch args[0] {
	case "server":
		return runServer(args[1:], stderr)
	case "whisper":
		return runWhisper(args[1:], stdout, stderr)
	case "speech":
		return runSpeech(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown fixture subcommand %q", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "cpp-studio-fixture is a deterministic fixture/test helper, not a native engine.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  cpp-studio-fixture server --host 127.0.0.1 --port 8799")
	fmt.Fprintln(w, "  cpp-studio-fixture whisper -f <wav>")
	fmt.Fprintln(w, "  cpp-studio-fixture speech --text <text> --out <path>")
}

func runServer(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	host := flags.String("host", "127.0.0.1", "fixture/test helper listen host")
	port := flags.Int("port", 8799, "fixture/test helper listen port")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *host == "" {
		return errors.New("server --host is required")
	}
	if *port <= 0 || *port > 65535 {
		return errors.New("server --port must be between 1 and 65535")
	}

	server := &http.Server{
		Addr:              *host + ":" + strconv.Itoa(*port),
		Handler:           newFixtureHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func newFixtureHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/v1/chat/completions", handleChatCompletions)
	return mux
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"fixture": true,
		"model":   fixtureModel,
	})
}

type chatRequest struct {
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "invalid chat completion request",
		})
		return
	}

	lastUser := ""
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			lastUser = msg.Content
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl-fixture",
		"object":  "chat.completion",
		"created": int64(0),
		"model":   fixtureModel,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "fixture assistant reply to: " + lastUser,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func runWhisper(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("whisper", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("f", "", "fixture/test helper WAV input path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("whisper -f <wav> is required")
	}
	if err := validateWAVFile(*path); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "fixture transcript")
	return err
}

func validateWAVFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open wav: %w", err)
	}
	defer f.Close()

	header := make([]byte, 12)
	if _, err := io.ReadFull(f, header); err != nil {
		return fmt.Errorf("invalid wav: missing RIFF/WAVE header")
	}
	if !bytes.Equal(header[0:4], []byte("RIFF")) || !bytes.Equal(header[8:12], []byte("WAVE")) {
		return fmt.Errorf("invalid wav: expected RIFF/WAVE header")
	}
	return nil
}

func runSpeech(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("speech", flag.ContinueOnError)
	flags.SetOutput(stderr)
	text := flags.String("text", "", "fixture/test helper text to synthesize")
	outPath := flags.String("out", "", "fixture/test helper WAV output path")
	requireContains := flags.String("require-contains", "", "fixture/test helper assertion for speech input")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*text) == "" {
		return errors.New("speech --text must be non-empty")
	}
	if *requireContains != "" && !strings.Contains(*text, *requireContains) {
		return fmt.Errorf("speech --text must contain %q", *requireContains)
	}
	if *outPath == "" {
		return errors.New("speech --out <path> is required")
	}
	if err := writeFixtureWAV(*outPath); err != nil {
		return err
	}
	return nil
}

func writeFixtureWAV(path string) error {
	const (
		channels      = 1
		sampleRate    = 16000
		bitsPerSample = 16
		sampleCount   = 160
	)

	var pcm bytes.Buffer
	for i := 0; i < sampleCount; i++ {
		sample := int16(0)
		if i%2 == 0 {
			sample = 1000
		} else {
			sample = -1000
		}
		if err := binary.Write(&pcm, binary.LittleEndian, sample); err != nil {
			return fmt.Errorf("encode pcm: %w", err)
		}
	}

	var wav bytes.Buffer
	dataSize := uint32(pcm.Len())
	byteRate := uint32(sampleRate * channels * bitsPerSample / 8)
	blockAlign := uint16(channels * bitsPerSample / 8)

	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36)+dataSize)
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&wav, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&wav, binary.LittleEndian, byteRate)
	_ = binary.Write(&wav, binary.LittleEndian, blockAlign)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(bitsPerSample))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, dataSize)
	wav.Write(pcm.Bytes())

	if err := os.WriteFile(path, wav.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write wav: %w", err)
	}
	return nil
}
