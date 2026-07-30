package main

import (
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

	"cpp-studio/internal/wav"
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
	case "design":
		return runDesign(args[1:], stdout, stderr)
	case "image":
		return runImage(args[1:], stdout, stderr)
	case "import":
		return runImport(args[1:], stdout, stderr)
	case "ffmpeg":
		return runFFmpeg(args[1:], stdout, stderr)
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
	fmt.Fprintln(w, "  cpp-studio-fixture speech --text <text> --out <path> [--voice-ref <wav> --reference-text <text>]")
	fmt.Fprintln(w, "  cpp-studio-fixture design --instruct <description> --text <text> --out <path>")
	fmt.Fprintln(w, "  cpp-studio-fixture image --prompt <prompt> --output <path> [--width <px> --height <px>]")
	fmt.Fprintln(w, "  cpp-studio-fixture import --no-simulate --print <template> -o <path> <url>")
	fmt.Fprintln(w, "  cpp-studio-fixture ffmpeg -encoders")
	fmt.Fprintln(w, "  cpp-studio-fixture ffmpeg -i <wav> -c:a <encoder> -b:a <rate> <out>")
}

func runServer(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	flags.SetOutput(stderr)
	host := flags.String("host", "127.0.0.1", "fixture/test helper listen host")
	port := flags.Int("port", 8799, "fixture/test helper listen port")
	// Accepted and ignored: byom variants restart the engine with a model
	// path, and this FlagSet would otherwise reject the unknown flag.
	flags.String("m", "", "fixture/test helper model path (ignored)")
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

// chatMessage accepts both content shapes llama-server does: a plain string,
// or an array of typed parts (text plus image_url for vision requests).
type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type chatContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// flattenContent extracts the text of a message and whether it carried an
// image part.
func flattenContent(raw json.RawMessage) (string, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, false
	}
	var parts []chatContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", false
	}
	hasImage := false
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			texts = append(texts, part.Text)
		case "image_url":
			hasImage = true
		}
	}
	return strings.Join(texts, " "), hasImage
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
	lastUserHasImage := false
	isNormalizer := false
	isStoryScript := false
	isSketchScript := false
	systemText := ""
	for _, msg := range req.Messages {
		text, hasImage := flattenContent(msg.Content)
		if msg.Role == "system" {
			systemText = text
			if strings.Contains(text, "voice-design normalizer") {
				isNormalizer = true
			}
			if strings.Contains(text, "audio stories as dialogue scripts") {
				isStoryScript = true
			}
			if strings.Contains(text, "comedy sketch scripts") {
				isSketchScript = true
			}
		}
		if msg.Role == "user" {
			lastUser, lastUserHasImage = text, hasImage
		}
	}
	reply := "fixture assistant reply to: " + lastUser
	if lastUserHasImage {
		reply = "fixture image description"
	}
	if isNormalizer {
		reply = `{"prose": "A deep, gravelly middle-aged cowboy voice with a slow drawl.", "attributes": "male, middle-aged, low pitch, american accent"}`
	}
	if isStoryScript {
		reply = fixtureStoryScriptJSON
	}
	if isSketchScript {
		reply = fixtureSketchScriptJSON(castIDsFromPrompt(systemText))
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
					"content": reply,
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

// fixtureStoryScriptJSON is the canned reply to story-script prompts. It
// cites only fact-1..fact-8, which every story request guarantees exists
// (fact card generation always produces at least eight cards).
const fixtureStoryScriptJSON = `{"title": "Fixture Story", "script": [
{"speaker_id": "narrator", "text": "Our story opens inside the sources.", "fact_ids": ["fact-1"]},
{"speaker_id": "nova", "text": "Where does it begin?", "fact_ids": ["fact-2"]},
{"speaker_id": "dr-lumen", "text": "It begins with the first fact.", "fact_ids": ["fact-1", "fact-3"]},
{"speaker_id": "narrator", "text": "One idea leads to the next.", "fact_ids": ["fact-4"]},
{"speaker_id": "nova", "text": "And then what happens?", "fact_ids": ["fact-5"]},
{"speaker_id": "dr-lumen", "text": "The sources explain it step by step.", "fact_ids": ["fact-6"]},
{"speaker_id": "narrator", "text": "Each fact builds on the last.", "fact_ids": ["fact-7"]},
{"speaker_id": "dr-lumen", "text": "Until the picture is complete.", "fact_ids": ["fact-8"]},
{"speaker_id": "narrator", "text": "And that is the story the sources tell.", "fact_ids": ["fact-1", "fact-8"]}
]}`

// castIDsFromPrompt reads the speaker ids back out of the gateway's cast
// rule line ("- speaker_id must be exactly one of: a, b, c."), so a fixture
// sketch is performable by whatever cast the request named — including a
// cast cloned out of the Extractor.
func castIDsFromPrompt(systemText string) []string {
	const marker = "speaker_id must be exactly one of:"
	start := strings.Index(systemText, marker)
	if start < 0 {
		return nil
	}
	rest := systemText[start+len(marker):]
	if end := strings.IndexAny(rest, ".\n"); end >= 0 {
		rest = rest[:end]
	}
	var ids []string
	for _, part := range strings.Split(rest, ",") {
		if id := strings.TrimSpace(part); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// fixtureSketchScriptJSON is the canned reply to sketch prompts: no fact
// ids, lines dealt round the cast in order.
func fixtureSketchScriptJSON(castIDs []string) string {
	if len(castIDs) == 0 {
		castIDs = []string{"narrator", "nova", "dr-lumen"}
	}
	texts := []string{
		"I have been standing in this queue since Tuesday.",
		"That is not a queue. That is a bus stop.",
		"Then why is everyone holding a ticket?",
		"Habit, mostly. This town does love a ticket.",
		"Well I am not moving until somebody serves me.",
		"Nobody is going to serve you at a bus stop.",
		"Then I shall wait. I have brought sandwiches.",
		"He has brought sandwiches. That settles it, we are all staying.",
	}
	lines := make([]string, 0, len(texts))
	for i, text := range texts {
		lines = append(lines, fmt.Sprintf(`{"speaker_id": %q, "text": %q}`, castIDs[i%len(castIDs)], text))
	}
	return `{"title": "Fixture Sketch", "script": [` + strings.Join(lines, ",\n") + `]}`
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

// runImport stands in for yt-dlp: it accepts the flags the gateway's import
// spec sends, prints a title on stdout the way --print does, and writes
// fixture audio to the -o path. Real yt-dlp would refuse to overwrite the
// temp file the spec runner already created, which is why the gateway passes
// --force-overwrites — the fixture asserts it is there so that contract
// cannot quietly rot.
func runImport(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outPath := flags.String("o", "", "fixture/test helper output path")
	printTemplate := flags.String("print", "", "fixture/test helper output template to print")
	noSimulate := flags.Bool("no-simulate", false, "fixture/test helper download despite --print")
	forceOverwrites := flags.Bool("force-overwrites", false, "fixture/test helper overwrite existing output")
	flags.Bool("no-playlist", false, "fixture/test helper single-item download")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *outPath == "" {
		return errors.New("import -o <path> is required")
	}
	if *printTemplate != "" && !*noSimulate {
		return errors.New("import --print without --no-simulate would only simulate")
	}
	if !*forceOverwrites {
		return errors.New("import needs --force-overwrites to replace the created output file")
	}
	rest := flags.Args()
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return errors.New("import needs exactly one URL argument")
	}
	if !strings.HasPrefix(rest[0], "http://") && !strings.HasPrefix(rest[0], "https://") {
		return fmt.Errorf("import URL must be http or https, got %q", rest[0])
	}
	if *printTemplate != "" {
		if _, err := fmt.Fprintln(stdout, "Fixture Import"); err != nil {
			return err
		}
	}
	return writeFixtureWAV(*outPath)
}

// runFFmpeg stands in for the operator's ffmpeg. It answers the encoder
// probe and performs a transcode that is real enough to test the contract:
// it reads the named input, refuses an encoder it does not claim to have,
// and writes a plausibly-shaped file to the named output.
func runFFmpeg(args []string, stdout, stderr io.Writer) error {
	for _, arg := range args {
		if arg == "-encoders" {
			_, err := fmt.Fprint(stdout, fixtureEncoderList)
			return err
		}
	}

	var inPath, outPath, codec string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-i":
			if i+1 < len(args) {
				inPath = args[i+1]
			}
		case "-c:a":
			if i+1 < len(args) {
				codec = args[i+1]
			}
		}
	}
	// ffmpeg's output path is the last positional argument.
	if len(args) > 0 {
		outPath = args[len(args)-1]
	}
	if inPath == "" || outPath == "" || strings.HasPrefix(outPath, "-") {
		return errors.New("ffmpeg needs -i <input> and an output path")
	}
	if codec == "" {
		return errors.New("ffmpeg needs -c:a <encoder>")
	}
	if !strings.Contains(fixtureEncoderList, codec) {
		return fmt.Errorf("ffmpeg has no encoder %q", codec)
	}
	if _, err := os.Stat(inPath); err != nil {
		return fmt.Errorf("ffmpeg could not open %s: %w", inPath, err)
	}
	// pcm_s16le is a decode rather than a compression: whatever went in,
	// a WAV comes out. That is the Extractor's escape hatch for files the
	// browser cannot read, so the fixture must produce a real WAV.
	if codec == "pcm_s16le" {
		return writeFixtureWAV(outPath)
	}
	source, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("ffmpeg could not read %s: %w", inPath, err)
	}
	if err := validateWAVFile(inPath); err != nil {
		return fmt.Errorf("ffmpeg input: %w", err)
	}
	// Stand in for compression: a header the sniffer recognises plus a
	// fraction of the source, so callers see a smaller, non-empty file.
	encoded := append([]byte("ID3\x03\x00\x00\x00"), source[:len(source)/8]...)
	return os.WriteFile(outPath, encoded, 0o600)
}

// fixtureEncoderList mimics the shape of `ffmpeg -encoders` output, which
// the gateway parses to decide which delivery formats it can offer.
const fixtureEncoderList = `Encoders:
 V..... = Video
 A..... = Audio
 ------
 A....D aac                  AAC (Advanced Audio Coding)
 A....D libmp3lame           libmp3lame MP3 (MPEG audio layer 3)
 A....D libopus              libopus Opus
 A....D pcm_s16le            PCM signed 16-bit little-endian
`

func validateWAVFile(path string) error {
	if err := wav.ValidateFile(path); err != nil {
		return fmt.Errorf("invalid wav: %v", err)
	}
	return nil
}

func runSpeech(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("speech", flag.ContinueOnError)
	flags.SetOutput(stderr)
	text := flags.String("text", "", "fixture/test helper text to synthesize")
	outPath := flags.String("out", "", "fixture/test helper WAV output path")
	requireContains := flags.String("require-contains", "", "fixture/test helper assertion for speech input")
	voiceRef := flags.String("voice-ref", "", "fixture/test helper cloned voice reference WAV")
	referenceText := flags.String("reference-text", "", "fixture/test helper cloned voice transcript")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*text) == "" {
		return errors.New("speech --text must be non-empty")
	}
	if *requireContains != "" && !strings.Contains(*text, *requireContains) {
		return fmt.Errorf("speech --text must contain %q", *requireContains)
	}
	if *voiceRef != "" {
		if err := validateWAVFile(*voiceRef); err != nil {
			return fmt.Errorf("speech --voice-ref: %w", err)
		}
		if strings.TrimSpace(*referenceText) == "" {
			return errors.New("speech --voice-ref requires --reference-text")
		}
	}
	if *outPath == "" {
		return errors.New("speech --out <path> is required")
	}
	if err := writeFixtureWAV(*outPath); err != nil {
		return err
	}
	return nil
}

// runDesign mimics the Qwen3-TTS VoiceDesign task: an instruction plus text
// in, a deterministic WAV out.
func runDesign(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("design", flag.ContinueOnError)
	flags.SetOutput(stderr)
	instruct := flags.String("instruct", "", "fixture/test helper voice design instruction")
	text := flags.String("text", "", "fixture/test helper text to synthesize")
	outPath := flags.String("out", "", "fixture/test helper WAV output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*instruct) == "" {
		return errors.New("design --instruct must be non-empty")
	}
	if strings.TrimSpace(*text) == "" {
		return errors.New("design --text must be non-empty")
	}
	if *outPath == "" {
		return errors.New("design --out <path> is required")
	}
	return writeFixtureWAV(*outPath)
}

func writeFixtureWAV(path string) error {
	const sampleCount = 160
	if err := os.WriteFile(path, wav.SyntheticTone(sampleCount), 0o644); err != nil {
		return fmt.Errorf("write wav: %w", err)
	}
	return nil
}

func runImage(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("image", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var prompt string
	var outPath string
	var width int
	var height int
	var requireSize string
	flags.StringVar(&prompt, "prompt", "", "fixture/test helper prompt")
	flags.StringVar(&prompt, "p", "", "fixture/test helper prompt")
	flags.StringVar(&outPath, "output", "", "fixture/test helper PNG output path")
	flags.StringVar(&outPath, "o", "", "fixture/test helper PNG output path")
	flags.IntVar(&width, "width", 0, "fixture/test helper image width")
	flags.IntVar(&width, "W", 0, "fixture/test helper image width")
	flags.IntVar(&height, "height", 0, "fixture/test helper image height")
	flags.IntVar(&height, "H", 0, "fixture/test helper image height")
	var seed int64
	flags.Int64Var(&seed, "seed", 0, "fixture/test helper seed (accepted for arg parity with sd-cli)")
	flags.StringVar(&requireSize, "require-size", "", "fail unless width and height match WIDTHxHEIGHT")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(prompt) == "" {
		return errors.New("image --prompt must be non-empty")
	}
	if outPath == "" {
		return errors.New("image --output <path> is required")
	}
	if width < 0 || height < 0 {
		return errors.New("image width and height must be non-negative")
	}
	if requireSize != "" && fmt.Sprintf("%dx%d", width, height) != requireSize {
		return fmt.Errorf("image size %dx%d did not match required size %s", width, height, requireSize)
	}
	return writeFixturePNG(outPath)
}

func writeFixturePNG(path string) error {
	data := []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00,
		0x90, 0x77, 0x53, 0xde,
		0x00, 0x00, 0x00, 0x10, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x62, 0xfa, 0xff, 0xff, 0x3f, 0x20,
		0x00, 0x00, 0xff, 0xff, 0x06, 0x06, 0x03, 0x00,
		0xb7, 0x66, 0x11, 0x21,
		0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D',
		0xae, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write png: %w", err)
	}
	return nil
}
