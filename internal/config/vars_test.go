package config

import (
	"path/filepath"
	"testing"
)

func TestVarsExpandAcrossFields(t *testing.T) {
	path := writeConfig(t, `{
      "gateway": {"host":"127.0.0.1","port":8765},
      "vars": {"root":"${configDir}/..", "port":"8733"},
      "models": {"manifest":"${configDir}/models.json", "root":"${root}"},
      "engines": {
        "llama": {
          "command":"${root}/engines/llama.cpp/llama-server.exe",
          "args":["--port","${port}","-m","${root}/engines/models/x.gguf"],
          "mode":"server",
          "healthUrl":"http://127.0.0.1:${port}/health"
        }
      }
    }`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	configDir := filepath.Dir(path)
	wantRoot := configDir + "/.."

	e := cfg.Engines["llama"]
	if want := wantRoot + "/engines/llama.cpp/llama-server.exe"; e.Command != want {
		t.Errorf("command: got %q want %q", e.Command, want)
	}
	if e.Args[1] != "8733" {
		t.Errorf("port arg not expanded: %q", e.Args[1])
	}
	if e.Args[3] != wantRoot+"/engines/models/x.gguf" {
		t.Errorf("model arg: got %q", e.Args[3])
	}
	if e.HealthURL != "http://127.0.0.1:8733/health" {
		t.Errorf("healthUrl: got %q", e.HealthURL)
	}
	if cfg.Models == nil || cfg.Models.Root != wantRoot {
		t.Errorf("models.root: got %+v", cfg.Models)
	}
	if cfg.Models.Manifest != configDir+"/models.json" {
		t.Errorf("models.manifest: got %q", cfg.Models.Manifest)
	}
}

func TestVarsFallBackToEnvAndAbsolutePathsUnaffected(t *testing.T) {
	t.Setenv("CPP_STUDIO_TEST_ROOT", "/opt/models")
	path := writeConfig(t, `{
      "gateway": {"host":"127.0.0.1","port":8765},
      "engines": {
        "llama": {"command":"${CPP_STUDIO_TEST_ROOT}/llama-server", "healthUrl":"http://127.0.0.1:8733/health", "mode":"server"},
        "whisper": {"command":"C:\\abs\\whisper.exe"}
      }
    }`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Engines["llama"].Command; got != "/opt/models/llama-server" {
		t.Errorf("env var not expanded: %q", got)
	}
	if got := cfg.Engines["whisper"].Command; got != `C:\abs\whisper.exe` {
		t.Errorf("absolute path altered: %q", got)
	}
}

func TestUnknownModelsFieldRejected(t *testing.T) {
	path := writeConfig(t, `{
      "gateway":{"port":8765},
      "models":{"manifest":"m.json","nope":1},
      "engines":{"llama":{"command":"x"}}
    }`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown models field to be rejected")
	}
}
