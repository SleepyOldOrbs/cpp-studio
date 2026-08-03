package models

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type discoveryCall struct {
	command string
	args    []string
}

type fixtureDiscoveryRunner struct {
	calls     []discoveryCall
	bad       string
	emptyInfo bool
}

func (r *fixtureDiscoveryRunner) Run(_ context.Context, command string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, discoveryCall{command, append([]string(nil), args...)})
	joined := strings.Join(args, " ")
	if r.bad != "" && strings.Contains(joined, r.bad) {
		return []byte("not-json"), nil
	}
	switch {
	case strings.Contains(joined, " list --json"):
		return []byte(`{"packages":[{"id":"dramabox-package"}]}`), nil
	case strings.Contains(joined, " info dramabox-package --json"):
		if r.emptyInfo {
			return []byte(`{}`), nil
		}
		return []byte(`{"id":"dramabox-package","revision":"fixed"}`), nil
	case joined == "--list-loaders --json":
		return []byte(`{"loaders":[{"family":"dramabox"}]}`), nil
	default:
		return nil, fmt.Errorf("unexpected command")
	}
}

func TestDiscoveryRejectsInfoThatDoesNotIdentifyAllowlistedPackage(t *testing.T) {
	runner := &fixtureDiscoveryRunner{emptyInfo: true}
	manifest := Manifest{Models: []Model{{ID: "dramabox", PackageID: "dramabox-package"}}}
	result := Discover(context.Background(), DiscoveryConfig{PythonCommand: "python", ManagerScript: "manager.py", AllowedPackages: []string{"dramabox-package"}}, manifest, runner)
	if result.PackageInfo["dramabox-package"] || !strings.Contains(result.DiscoveryError, "did not identify") {
		t.Fatalf("unidentified info response was trusted: %+v", result)
	}
}

func TestDiscoveryUsesOnlyFixedAllowlistedCatalogCommands(t *testing.T) {
	runner := &fixtureDiscoveryRunner{}
	manifest := Manifest{Models: []Model{{ID: "dramabox", PackageID: "dramabox-package"}, {ID: "other", PackageID: "not-allowed"}}}
	result := Discover(context.Background(), DiscoveryConfig{
		PythonCommand: "python", ManagerScript: "manager.py", AudioCLI: "audiocpp_cli",
		AllowedPackages: []string{"dramabox-package", "browser-injected", "dramabox-package"},
	}, manifest, runner)
	if result.DiscoveryError != "" || !reflect.DeepEqual(result.PackageIDs, []string{"dramabox-package"}) || !result.PackageInfo["dramabox-package"] || !reflect.DeepEqual(result.LoaderFamilies, []string{"dramabox"}) {
		t.Fatalf("unexpected discovery: %+v", result)
	}
	want := []discoveryCall{
		{command: "python", args: []string{"manager.py", "list", "--json"}},
		{command: "python", args: []string{"manager.py", "info", "dramabox-package", "--json"}},
		{command: "audiocpp_cli", args: []string{"--list-loaders", "--json"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("commands escaped the fixed contract: %+v", runner.calls)
	}
}

func TestDiscoveryMalformedOutputDegradesWithoutHidingCatalog(t *testing.T) {
	runner := &fixtureDiscoveryRunner{bad: "list --json"}
	manifest := Manifest{Models: []Model{{ID: "dramabox", PackageID: "dramabox-package"}}}
	result := Discover(context.Background(), DiscoveryConfig{PythonCommand: "python", ManagerScript: "manager.py", AllowedPackages: []string{"dramabox-package"}}, manifest, runner)
	if !strings.Contains(result.DiscoveryError, "malformed JSON") || len(manifest.Statuses(t.TempDir())) != 1 {
		t.Fatalf("malformed discovery broke catalog fallback: %+v", result)
	}
}

func TestDiscoveryOutputBufferIsBounded(t *testing.T) {
	var buffer limitedBuffer
	payload := make([]byte, maxDiscoveryOutputBytes+1)
	if n, err := buffer.Write(payload); err != nil || n != len(payload) || !buffer.overflow || buffer.Len() != maxDiscoveryOutputBytes {
		t.Fatalf("output bound failed: n=%d len=%d overflow=%v err=%v", n, buffer.Len(), buffer.overflow, err)
	}
}

func TestHuggingFaceInstallMetadataRequiresPinnedRevisionURL(t *testing.T) {
	model := Model{
		PackageID: "dramabox_q8_0", Revision: "fixed-commit", License: "test", Bytes: 1,
		SHA256: strings.Repeat("0", 64), DownloadURL: "https://huggingface.co/owner/repo/resolve/main/model.gguf",
	}
	if model.HasImmutableInstallMetadata() {
		t.Fatal("moving Hugging Face URL was treated as immutable")
	}
	model.DownloadURL = "https://huggingface.co/owner/repo/resolve/fixed-commit/model.gguf"
	if !model.HasImmutableInstallMetadata() {
		t.Fatal("revision-pinned Hugging Face URL was rejected")
	}
}
