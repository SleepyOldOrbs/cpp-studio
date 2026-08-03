package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const maxDiscoveryOutputBytes = 1024 * 1024

type DiscoveryConfig struct {
	PythonCommand   string
	ManagerScript   string
	AudioCLI        string
	WorkingDir      string
	AllowedPackages []string
	Timeout         time.Duration
}

type DiscoveryResult struct {
	RuntimeIdentity string          `json:"runtimeIdentity,omitempty"`
	PackageIDs      []string        `json:"packageIds,omitempty"`
	PackageInfo     map[string]bool `json:"packageInfo,omitempty"`
	LoaderFamilies  []string        `json:"loaderFamilies,omitempty"`
	DiscoveryError  string          `json:"discoveryError,omitempty"`
	DiscoveredAt    time.Time       `json:"discoveredAt"`
}

func (r DiscoveryResult) HasPackage(id string) bool {
	if r.PackageInfo[id] {
		return true
	}
	for _, known := range r.PackageIDs {
		if known == id {
			return true
		}
	}
	return false
}

func (r DiscoveryResult) HasLoader(family string) bool {
	for _, known := range r.LoaderFamilies {
		if known == family {
			return true
		}
	}
	return false
}

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecCommandRunner struct{ WorkingDir string }

func (r ExecCommandRunner) Run(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = r.WorkingDir
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		return nil, fmt.Errorf("discovery command output exceeded %d bytes", maxDiscoveryOutputBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", filepathBase(command), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	overflow bool
}

func (w *limitedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := maxDiscoveryOutputBytes - w.Len()
	if remaining <= 0 {
		w.overflow = true
		return original, nil
	}
	if len(data) > remaining {
		w.overflow = true
		data = data[:remaining]
	}
	_, _ = w.Buffer.Write(data)
	return original, nil
}

func filepathBase(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[index+1:]
	}
	return path
}

func Discover(ctx context.Context, cfg DiscoveryConfig, manifest Manifest, runner CommandRunner) DiscoveryResult {
	result := DiscoveryResult{PackageInfo: map[string]bool{}, DiscoveredAt: time.Now().UTC()}
	if runner == nil {
		runner = ExecCommandRunner{WorkingDir: cfg.WorkingDir}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	result.RuntimeIdentity = DiscoveryRuntimeIdentity(cfg)
	allowed := allowedCatalogPackages(cfg.AllowedPackages, manifest)
	errorsFound := []string{}
	if cfg.PythonCommand != "" && cfg.ManagerScript != "" {
		commandCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		output, err := runner.Run(commandCtx, cfg.PythonCommand, cfg.ManagerScript, "list", "--json")
		cancel()
		if err != nil {
			errorsFound = append(errorsFound, "model manager list: "+err.Error())
		} else if ids, parseErr := discoveryValues(output, "id", "package_id"); parseErr != nil {
			errorsFound = append(errorsFound, "model manager list: "+parseErr.Error())
		} else {
			result.PackageIDs = ids
		}
		for _, packageID := range allowed {
			commandCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
			output, err = runner.Run(commandCtx, cfg.PythonCommand, cfg.ManagerScript, "info", packageID, "--json")
			cancel()
			if err != nil {
				errorsFound = append(errorsFound, "model manager info "+packageID+": "+err.Error())
				continue
			}
			var decoded any
			if err := json.Unmarshal(output, &decoded); err != nil {
				errorsFound = append(errorsFound, "model manager info "+packageID+": malformed JSON")
				continue
			}
			ids, parseErr := discoveryValues(output, "id", "package_id")
			if parseErr != nil || !containsString(ids, packageID) {
				errorsFound = append(errorsFound, "model manager info "+packageID+": response did not identify the requested package")
				continue
			}
			result.PackageInfo[packageID] = true
		}
	}
	if cfg.AudioCLI != "" {
		commandCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		output, err := runner.Run(commandCtx, cfg.AudioCLI, "--list-loaders", "--json")
		cancel()
		if err != nil {
			errorsFound = append(errorsFound, "audio loader discovery: "+err.Error())
		} else if families, parseErr := discoveryLoaderFamilies(output); parseErr != nil {
			errorsFound = append(errorsFound, "audio loader discovery: "+parseErr.Error())
		} else {
			result.LoaderFamilies = families
		}
	}
	result.DiscoveryError = strings.Join(errorsFound, "; ")
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func allowedCatalogPackages(configured []string, manifest Manifest) []string {
	cataloged := map[string]bool{}
	for _, model := range manifest.Models {
		if model.PackageID != "" {
			cataloged[model.PackageID] = true
		}
	}
	seen := map[string]bool{}
	result := []string{}
	for _, id := range configured {
		if id != "" && cataloged[id] && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func discoveryValues(data []byte, keys ...string) ([]string, error) {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("malformed JSON")
	}
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	seen := map[string]bool{}
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			for key, item := range typed {
				if allowed[key] {
					if text, ok := item.(string); ok && text != "" {
						seen[text] = true
					}
				}
				visit(item)
			}
		}
	}
	visit(decoded)
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return values, nil
}

func discoveryLoaderFamilies(data []byte) ([]string, error) {
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("malformed JSON")
	}
	raw, ok := decoded["loaders"]
	if !ok {
		return discoveryValues(data, "family", "id")
	}
	var loaderMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &loaderMap); err != nil {
		return discoveryValues(data, "family", "id")
	}
	values := make([]string, 0, len(loaderMap))
	for family := range loaderMap {
		if family != "" {
			values = append(values, family)
		}
	}
	sort.Strings(values)
	return values, nil
}

func DiscoveryRuntimeIdentity(cfg DiscoveryConfig) string {
	h := sha256.New()
	for _, value := range append([]string{cfg.PythonCommand, cfg.ManagerScript, cfg.AudioCLI, cfg.WorkingDir}, cfg.AllowedPackages...) {
		_, _ = h.Write([]byte(value + "\x00"))
		if info, err := os.Stat(value); err == nil {
			_, _ = h.Write([]byte(fmt.Sprintf("%d\x00%d\x00", info.Size(), info.ModTime().UnixNano())))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (m Model) HasImmutableInstallMetadata() bool {
	checksum, checksumErr := hex.DecodeString(m.SHA256)
	download, downloadErr := url.Parse(m.DownloadURL)
	valid := m.PackageID != "" && m.Revision != "" && m.License != "" && m.Bytes > 0 &&
		checksumErr == nil && len(checksum) == sha256.Size && downloadErr == nil &&
		download.Scheme == "https" && download.Host != ""
	if !valid {
		return false
	}
	if strings.EqualFold(download.Hostname(), "huggingface.co") {
		return strings.Contains(download.EscapedPath(), "/resolve/"+url.PathEscape(m.Revision)+"/")
	}
	return true
}
