package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDramaBoxExamplesKeepExistingAudioAndUseSafeServerPosture(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	cfg, err := Load(filepath.Join(repoRoot, "config.dramabox-local.example.json"))
	if err != nil {
		t.Fatalf("load DramaBox gateway example: %v", err)
	}
	if _, ok := cfg.Engines["audio"]; !ok {
		t.Fatal("DramaBox example must preserve the existing audio narrator")
	}
	drama, ok := cfg.Engines["dramabox"]
	if !ok || drama.Mode != "server" || drama.HealthURL != "http://127.0.0.1:8740/health" {
		t.Fatalf("unexpected DramaBox gateway engine: %+v", drama)
	}
	if drama.GPU {
		t.Fatal("reference DramaBox engine must not claim the shared GPU slot")
	}
	if drama.DefaultVoiceRef != "" {
		t.Fatal("text-only DramaBox example must not require a default voice reference")
	}

	serverPath := filepath.Join(repoRoot, "dramabox-server.example.json")
	file, err := os.Open(serverPath)
	if err != nil {
		t.Fatalf("open DramaBox server example: %v", err)
	}
	defer file.Close()
	var server struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Backend  string `json:"backend"`
		LazyLoad bool   `json:"lazy_load"`
		Models   []struct {
			ID     string `json:"id"`
			Family string `json:"family"`
			Path   string `json:"path"`
			Task   string `json:"task"`
			Mode   string `json:"mode"`
		} `json:"models"`
	}
	if err := json.NewDecoder(file).Decode(&server); err != nil {
		t.Fatalf("decode DramaBox server example: %v", err)
	}
	if server.Host != "127.0.0.1" || server.Port != 8740 || server.Backend != "cpu" || !server.LazyLoad {
		t.Fatalf("unsafe DramaBox server posture: %+v", server)
	}
	if len(server.Models) != 1 || server.Models[0].ID != "tts" || server.Models[0].Family != "dramabox" || server.Models[0].Task != "tts" || server.Models[0].Mode != "offline" {
		t.Fatalf("unexpected DramaBox server model contract: %+v", server.Models)
	}
	resolvedModel := filepath.Clean(filepath.Join(filepath.Dir(serverPath), filepath.FromSlash(server.Models[0].Path)))
	wantModel := filepath.Clean(filepath.Join(repoRoot, "models", "speech", "synthesis", "DramaBox-GGUF", "dramabox-q8_0.gguf"))
	if resolvedModel != wantModel {
		t.Fatalf("DramaBox server model resolves to %q, want CPP Studio model layout %q", resolvedModel, wantModel)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {
    "llama": {
      "command": "llama-server",
      "healthURL": "http://127.0.0.1:8733/health"
    }
  }
}`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadRejectsInvalidRuntimeValues(t *testing.T) {
	tests := map[string]string{
		"bad-port": `{
  "gateway": {"host": "127.0.0.1", "port": 70000},
  "engines": {"llama": {"command": "llama-server"}}
}`,
		"bad-health-url": `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {"llama": {"command": "llama-server", "healthUrl": "127.0.0.1:8733/health"}}
}`,
		"negative-timeout": `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {"llama": {"command": "llama-server", "startupTimeoutSeconds": -1}}
}`,
		"bad-mode": `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {"llama": {"command": "llama-server", "mode": "daemon"}}
}`,
		"negative-request-timeout": `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {"llama": {"command": "llama-server", "requestTimeoutSeconds": -1}}
}`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, body))
			if err == nil {
				t.Fatalf("expected config validation error")
			}
		})
	}
}

func TestCheckCommandsRejectsMissingCommand(t *testing.T) {
	cfg := Config{
		Gateway: GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: map[string]EngineConfig{
			"missing": {Command: "definitely-not-a-real-cpp-studio-command"},
		},
	}
	if err := cfg.CheckCommands(nil); err == nil {
		t.Fatalf("expected missing command error")
	}
}

func TestCheckCommandsResolvesRelativeToWorkingDir(t *testing.T) {
	var probed []string
	lookPath := func(path string) (string, error) {
		probed = append(probed, path)
		return path, nil
	}
	cfg := Config{
		Engines: map[string]EngineConfig{
			"relative": {Command: `bin\engine.exe`, WorkingDir: `C:\engines`},
			"bare":     {Command: "engine"},
		},
	}
	if err := cfg.CheckCommands(lookPath); err != nil {
		t.Fatalf("check commands: %v", err)
	}
	joined := strings.Join(probed, ";")
	if !strings.Contains(joined, filepath.Join(`C:\engines`, `bin\engine.exe`)) {
		t.Fatalf("expected workingDir-joined probe, got %v", probed)
	}
	if !strings.Contains(joined, "engine") {
		t.Fatalf("expected bare-name probe, got %v", probed)
	}
}

func TestLoadCheckedRejectsMissingCommand(t *testing.T) {
	path := writeConfig(t, `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "engines": {"llama": {"command": "definitely-not-a-real-cpp-studio-command"}}
}`)
	if _, err := LoadChecked(path); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing command error, got %v", err)
	}
}

func TestLoadEngineVariants(t *testing.T) {
	t.Run("valid variants load with vars expanded", func(t *testing.T) {
		path := writeConfig(t, `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "vars": {"models": "C:\\models"},
  "engines": {"whisper": {
    "command": "whisper-server",
    "mode": "server",
    "defaultVariant": "large",
    "variants": {
      "large": {"label": "large-v3 (best)", "args": ["-m", "${models}\\large.bin"]},
      "turbo": {"args": ["-m", "${models}\\turbo.bin"]}
    }
  }}
}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		whisper := cfg.Engines["whisper"]
		if len(whisper.Variants) != 2 || whisper.DefaultVariant != "large" {
			t.Fatalf("unexpected variants %+v", whisper)
		}
		if got := whisper.Variants["large"].Args[1]; got != `C:\models\large.bin` {
			t.Fatalf("expected vars expanded in variant args, got %q", got)
		}
	})

	rejects := []struct {
		name string
		body string
		want string
	}{
		{
			name: "variants on a subprocess engine",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"subprocess","defaultVariant":"a","variants":{"a":{"args":[]}}}}}`,
			want: "server mode",
		},
		{
			name: "args and variants together",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server","args":["-m","m"],"defaultVariant":"a","variants":{"a":{"args":[]}}}}}`,
			want: "single source of truth",
		},
		{
			name: "missing defaultVariant",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server","variants":{"a":{"args":[]}}}}}`,
			want: "no defaultVariant",
		},
		{
			name: "unknown defaultVariant",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server","defaultVariant":"b","variants":{"a":{"args":[]}}}}}`,
			want: "not a declared variant",
		},
		{
			name: "defaultVariant without variants",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server","defaultVariant":"a"}}}`,
			want: "declares no variants",
		},
		{
			name: "unknown variant field",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server","defaultVariant":"a","variants":{"a":{"args":[],"model":"m"}}}}}`,
			want: "unknown field",
		},
	}
	for _, tt := range rejects {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tt.body)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestLoadEngineByom(t *testing.T) {
	path := writeConfig(t, `{
  "gateway": {"host": "127.0.0.1", "port": 8765},
  "vars": {"root": "C:\\studio", "model": "should-not-be-touched"},
  "engines": {"llama": {
    "command": "llama-server",
    "mode": "server",
    "defaultVariant": "base",
    "variants": {"base": {"args": ["-m", "${root}\\base.gguf"]}},
    "byomDir": "${root}\\byom",
    "byomArgs": ["--port", "8733", "-m", "{model}", "-c", "8192"]
  }}
}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	llama := cfg.Engines["llama"]
	if llama.ByomDir != `C:\studio\byom` {
		t.Fatalf("expected vars expanded in byomDir, got %q", llama.ByomDir)
	}
	// {model} must survive expansion even when a "model" var exists — it
	// is the launch-time placeholder, not a config token.
	if got := llama.ByomArgs[3]; got != "{model}" {
		t.Fatalf("expected {model} placeholder untouched, got %q", got)
	}
}

func TestLoadRejectsBadByomConfigs(t *testing.T) {
	variants := `"defaultVariant":"a","variants":{"a":{"args":[]}}`
	rejects := []struct {
		name string
		body string
		want string
	}{
		{
			name: "byomDir without byomArgs",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server",` + variants + `,"byomDir":"d"}}}`,
			want: "byomDir and byomArgs together",
		},
		{
			name: "byomArgs without byomDir",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server",` + variants + `,"byomArgs":["-m","{model}"]}}}`,
			want: "byomDir and byomArgs together",
		},
		{
			name: "byomDir without variants",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server","byomDir":"d","byomArgs":["-m","{model}"]}}}`,
			want: "requires a variants block",
		},
		{
			name: "missing model placeholder",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server",` + variants + `,"byomDir":"d","byomArgs":["-m","x.gguf"]}}}`,
			want: "exactly once, found 0",
		},
		{
			name: "two model placeholders",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server",` + variants + `,"byomDir":"d","byomArgs":["-m","{model}","--lora","{model}"]}}}`,
			want: "exactly once, found 2",
		},
		{
			name: "dollar model placeholder",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server",` + variants + `,"byomDir":"d","byomArgs":["-m","${model}"]}}}`,
			want: "use {model}",
		},
		{
			name: "variant id in the byom namespace",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"server","defaultVariant":"byom:a","variants":{"byom:a":{"args":[]}}}}}`,
			want: "reserved byom: namespace",
		},
		{
			name: "byom on a subprocess engine",
			body: `{"gateway":{},"engines":{"x":{"command":"c","mode":"subprocess","byomDir":"d","byomArgs":["-m","{model}"]}}}`,
			want: "requires a variants block",
		},
	}
	for _, tt := range rejects {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tt.body)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
