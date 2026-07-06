package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

type Config struct {
	Gateway GatewayConfig           `json:"gateway"`
	Engines map[string]EngineConfig `json:"engines"`
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
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if err := rejectUnknownKeys(data); err != nil {
		return Config{}, err
	}

	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}

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

func rejectUnknownKeys(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	for key := range root {
		if key != "gateway" && key != "engines" {
			return fmt.Errorf("unknown top-level field %q", key)
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
				case "command", "args", "mode", "workingDir", "healthUrl", "startupTimeoutSeconds", "shutdownTimeoutSeconds", "requestTimeoutSeconds":
				default:
					return fmt.Errorf("unknown engine %q field %q", name, key)
				}
			}
		}
	}
	return nil
}
