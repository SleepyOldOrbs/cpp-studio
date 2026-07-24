package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Config struct {
	Gateway GatewayConfig           `json:"gateway"`
	Engines map[string]EngineConfig `json:"engines"`
	// Vars are user-defined substitutions expanded as ${name} across engine
	// command/args/workingDir/healthUrl/defaultVoiceRef and the models block.
	// The built-in ${configDir} (absolute directory of the config file) is
	// always available, so a portable config can set e.g. root ${configDir}/..
	// and reference ${root} everywhere else. Unset names fall back to the
	// process environment. Absolute paths without ${} are unaffected.
	Vars map[string]string `json:"vars,omitempty"`
	// Models points at the tracked model manifest and the root its relative
	// paths resolve against, powering the catalog/downloads surface.
	Models *ModelsConfig `json:"models,omitempty"`
}

// ModelsConfig locates the model manifest and the root that its relative model
// paths resolve against.
type ModelsConfig struct {
	Manifest string `json:"manifest"`
	Root     string `json:"root"`
}

type GatewayConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type EngineConfig struct {
	Command                string   `json:"command"`
	Args                   []string `json:"args"`
	Mode                   string   `json:"mode,omitempty"`
	WorkingDir             string   `json:"workingDir,omitempty"`
	HealthURL              string   `json:"healthUrl,omitempty"`
	StartupTimeoutSeconds  int      `json:"startupTimeoutSeconds,omitempty"`
	ShutdownTimeoutSeconds int      `json:"shutdownTimeoutSeconds,omitempty"`
	RequestTimeoutSeconds  int      `json:"requestTimeoutSeconds,omitempty"`
	// GPU marks a subprocess engine as a heavy GPU user; runs of GPU-marked
	// engines are serialized across engines so they never race for VRAM.
	GPU bool `json:"gpu,omitempty"`
	// DefaultVoiceRef / DefaultVoiceText carry the default cloning
	// reference for a server-mode audio engine, where per-request flags
	// cannot ride in args. Subprocess audio engines keep the reference in
	// args instead.
	DefaultVoiceRef  string `json:"defaultVoiceRef,omitempty"`
	DefaultVoiceText string `json:"defaultVoiceText,omitempty"`
}

// Load reads a config file and validates everything that can be checked
// portably: shape, unknown keys, defaults, ranges, modes, and URLs.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if err := rejectUnknownKeys(data); err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&cfg); err != nil {
		return Config{}, err
	}

	configDir := ""
	if abs, err := filepath.Abs(path); err == nil {
		configDir = filepath.Dir(abs)
	}
	cfg.expandVars(configDir)

	if cfg.Gateway.Host == "" {
		cfg.Gateway.Host = "127.0.0.1"
	}
	if cfg.Gateway.Port == 0 {
		cfg.Gateway.Port = 8765
	}
	if cfg.Gateway.Port < 1 || cfg.Gateway.Port > 65535 {
		return Config{}, fmt.Errorf("gateway port must be between 1 and 65535")
	}
	if len(cfg.Engines) == 0 {
		return Config{}, fmt.Errorf("config must declare at least one engine")
	}
	for name, engine := range cfg.Engines {
		if name == "" {
			return Config{}, fmt.Errorf("engine name cannot be empty")
		}
		if engine.Command == "" {
			return Config{}, fmt.Errorf("engine %q command is required", name)
		}
		switch engine.Mode {
		case "", "server", "subprocess":
		default:
			return Config{}, fmt.Errorf("engine %q mode must be server or subprocess", name)
		}
		if engine.StartupTimeoutSeconds < 0 {
			return Config{}, fmt.Errorf("engine %q startup timeout cannot be negative", name)
		}
		if engine.ShutdownTimeoutSeconds < 0 {
			return Config{}, fmt.Errorf("engine %q shutdown timeout cannot be negative", name)
		}
		if engine.RequestTimeoutSeconds < 0 {
			return Config{}, fmt.Errorf("engine %q request timeout cannot be negative", name)
		}
		if engine.HealthURL != "" {
			parsed, err := url.Parse(engine.HealthURL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return Config{}, fmt.Errorf("engine %q healthUrl must be an absolute HTTP(S) URL", name)
			}
		}
	}

	return cfg, nil
}

// expandVars substitutes ${name} tokens across the config's path-like string
// fields. Resolution order: the built-in ${configDir}, then user vars, then the
// process environment; an unresolved token is left literal so a misconfigured
// path surfaces plainly rather than silently emptying.
func (c *Config) expandVars(configDir string) {
	lookup := func(name string) (string, bool) {
		if name == "configDir" {
			return configDir, true
		}
		if v, ok := c.Vars[name]; ok {
			return v, true
		}
		return os.LookupEnv(name)
	}
	expand := func(s string) string {
		return os.Expand(s, func(name string) string {
			if v, ok := lookup(name); ok {
				return v
			}
			return "${" + name + "}"
		})
	}
	// Vars may reference ${configDir} (and, in declaration order, each other via
	// the environment); expand their values first so downstream fields see them.
	for name, value := range c.Vars {
		c.Vars[name] = expand(value)
	}
	for name, engine := range c.Engines {
		engine.Command = expand(engine.Command)
		engine.WorkingDir = expand(engine.WorkingDir)
		engine.HealthURL = expand(engine.HealthURL)
		engine.DefaultVoiceRef = expand(engine.DefaultVoiceRef)
		for i, arg := range engine.Args {
			engine.Args[i] = expand(arg)
		}
		c.Engines[name] = engine
	}
	if c.Models != nil {
		c.Models.Manifest = expand(c.Models.Manifest)
		c.Models.Root = expand(c.Models.Root)
	}
}

// LoadChecked is Load plus the machine-local check that every engine command
// resolves to an executable on this machine. One call fully answers "is this
// config acceptable here"; CI-style portable validation uses Load alone.
func LoadChecked(path string) (Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.CheckCommands(nil); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LookPath resolves a command name to an executable path. It exists so tests
// can substitute the machine-local probe; nil means exec.LookPath.
type LookPath func(string) (string, error)

// CheckCommands verifies that each engine command resolves to an executable:
// path-like commands relative to the engine workingDir, bare names on PATH.
func (c Config) CheckCommands(lookPath LookPath) error {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	for name, engine := range c.Engines {
		if strings.ContainsAny(engine.Command, `/\`) {
			abs := engine.Command
			if !filepath.IsAbs(abs) && engine.WorkingDir != "" {
				abs = filepath.Join(engine.WorkingDir, engine.Command)
			}
			if _, err := lookPath(abs); err != nil {
				return fmt.Errorf("engine %q command not found: %w", name, err)
			}
			continue
		}
		if _, err := lookPath(engine.Command); err != nil {
			return fmt.Errorf("engine %q command not found on PATH: %w", name, err)
		}
	}
	return nil
}

func rejectUnknownKeys(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	for key := range root {
		switch key {
		case "gateway", "engines", "vars", "models":
		default:
			return fmt.Errorf("unknown top-level field %q", key)
		}
	}

	if raw, ok := root["models"]; ok {
		var models map[string]json.RawMessage
		if err := json.Unmarshal(raw, &models); err != nil {
			return fmt.Errorf("models must be an object: %w", err)
		}
		for key := range models {
			if key != "manifest" && key != "root" {
				return fmt.Errorf("unknown models field %q", key)
			}
		}
	}

	if raw, ok := root["gateway"]; ok {
		var gateway map[string]json.RawMessage
		if err := json.Unmarshal(raw, &gateway); err != nil {
			return fmt.Errorf("gateway must be an object: %w", err)
		}
		for key := range gateway {
			if key != "host" && key != "port" {
				return fmt.Errorf("unknown gateway field %q", key)
			}
		}
	}

	if raw, ok := root["engines"]; ok {
		var engines map[string]json.RawMessage
		if err := json.Unmarshal(raw, &engines); err != nil {
			return fmt.Errorf("engines must be an object: %w", err)
		}
		for name, rawEngine := range engines {
			var engine map[string]json.RawMessage
			if err := json.Unmarshal(rawEngine, &engine); err != nil {
				return fmt.Errorf("engine %q must be an object: %w", name, err)
			}
			for key := range engine {
				switch key {
				case "command", "args", "mode", "workingDir", "healthUrl", "startupTimeoutSeconds", "shutdownTimeoutSeconds", "requestTimeoutSeconds", "gpu", "defaultVoiceRef", "defaultVoiceText":
				default:
					return fmt.Errorf("unknown engine %q field %q", name, key)
				}
			}
		}
	}
	return nil
}
