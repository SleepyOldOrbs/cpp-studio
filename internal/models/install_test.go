package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testInstaller(t *testing.T, payload []byte) (*Installer, Model, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	sum := sha256.Sum256(payload)
	model := Model{
		ID: "dramabox", Engine: "dramabox", Family: "dramabox", Path: "audio/DramaBox/model.gguf",
		Bytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:]), Source: "https://example.invalid/catalog",
		License: "test-license", PackageID: "dramabox_q8_0", Revision: "fixed-commit", DownloadURL: server.URL + "/model.gguf",
	}
	installer := NewInstaller(Manifest{Models: []Model{model}}, t.TempDir(), []string{model.PackageID})
	installer.Client = server.Client()
	installer.Free = func(string) (uint64, error) { return 100 * 1024 * 1024 * 1024, nil }
	return installer, model, server
}

func TestInstallerPreviewConfirmationAndVerifiedPromotion(t *testing.T) {
	payload := bytes.Repeat([]byte("verified-model"), 100)
	installer, model, _ := testInstaller(t, payload)
	preview, err := installer.Preview(model.ID)
	if err != nil || preview.ConfirmationID == "" || len(preview.Blockers) != 0 {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	if !strings.Contains(strings.Join(preview.Warnings, " | "), "electricity") {
		t.Fatalf("preview omitted local cost warning: %+v", preview.Warnings)
	}
	task, err := installer.Begin(model.ID, preview.ConfirmationID)
	if err != nil {
		t.Fatal(err)
	}
	var phases []string
	status, err := task.Run(func(p InstallProgress) { phases = append(phases, p.Phase) })
	if err != nil || status.State != StateVerified {
		t.Fatalf("run: %+v, %v", status, err)
	}
	if data, err := os.ReadFile(preview.Destination); err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("promoted artifact mismatch: %v", err)
	}
	wantPhases := []string{"preparing", "downloading", "verifying", "promoting", "refreshing_catalog"}
	for _, want := range wantPhases {
		if !contains(phases, want) {
			t.Errorf("missing phase %q in %v", want, phases)
		}
	}
	if _, err := installer.Begin(model.ID, preview.ConfirmationID); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("confirmation replay was accepted: %v", err)
	}
}

func TestInstallerPreviewBlockers(t *testing.T) {
	installer, model, _ := testInstaller(t, []byte("model"))
	model.License = ""
	model.Path = "../escape.gguf"
	installer.Manifest.Models[0] = model
	installer.Free = func(string) (uint64, error) { return 1, nil }
	preview, err := installer.Preview(model.ID)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(preview.Blockers, " | ")
	for _, required := range []string{"immutable", "escapes", "insufficient"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing %q blocker: %s", required, joined)
		}
	}
	if preview.ConfirmationID != "" {
		t.Fatal("blocked preview issued a confirmation")
	}
}

func TestInstallerPreviewBlocksUnknownDiskAndLinkedEscape(t *testing.T) {
	installer, model, _ := testInstaller(t, []byte("model"))
	installer.Free = func(string) (uint64, error) { return 0, errors.New("disk query failed") }
	preview, err := installer.Preview(model.ID)
	if err != nil || !strings.Contains(strings.Join(preview.Blockers, " | "), "cannot be determined") || preview.ConfirmationID != "" {
		t.Fatalf("unknown disk state was not a blocker: %+v, %v", preview, err)
	}

	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	model.Path = filepath.Join("linked", "model.gguf")
	installer = NewInstaller(Manifest{Models: []Model{model}}, root, []string{model.PackageID})
	installer.Free = func(string) (uint64, error) { return 1 << 40, nil }
	preview, err = installer.Preview(model.ID)
	if err != nil || !strings.Contains(strings.Join(preview.Blockers, " | "), "resolves outside") || preview.ConfirmationID != "" {
		t.Fatalf("linked destination escape was not blocked: %+v, %v", preview, err)
	}
}

func TestInstallerExpiryAndDestinationLock(t *testing.T) {
	installer, model, _ := testInstaller(t, []byte("model"))
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	installer.Now = func() time.Time { return now }
	expiring, _ := installer.Preview(model.ID)
	now = now.Add(confirmationLifetime + time.Second)
	if _, err := installer.Begin(model.ID, expiring.ConfirmationID); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("expired confirmation accepted: %v", err)
	}
	first, _ := installer.Preview(model.ID)
	firstTask, err := installer.Begin(model.ID, first.ConfirmationID)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := installer.Preview(model.ID)
	if _, err := installer.Begin(model.ID, second.ConfirmationID); !errors.Is(err, ErrInstallConflict) {
		t.Fatalf("destination race accepted: %v", err)
	}
	if err := firstTask.Cancel(); err != nil {
		t.Fatal(err)
	}
	if _, err := firstTask.Run(nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled install result: %v", err)
	}
}

func TestInstallerConcurrentConfirmationsHaveOneDestinationWinner(t *testing.T) {
	installer, model, _ := testInstaller(t, []byte("model"))
	first, _ := installer.Preview(model.ID)
	second, _ := installer.Preview(model.ID)
	start := make(chan struct{})
	type outcome struct {
		task *InstallTask
		err  error
	}
	results := make(chan outcome, 2)
	for _, confirmationID := range []string{first.ConfirmationID, second.ConfirmationID} {
		confirmationID := confirmationID
		go func() {
			<-start
			task, err := installer.Begin(model.ID, confirmationID)
			results <- outcome{task: task, err: err}
		}()
	}
	close(start)
	winners := []*InstallTask{}
	conflicts := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			winners = append(winners, result.task)
		} else if errors.Is(result.err, ErrInstallConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent result: %v", result.err)
		}
	}
	if len(winners) != 1 || conflicts != 1 {
		t.Fatalf("destination lock winners=%d conflicts=%d", len(winners), conflicts)
	}
	_ = winners[0].Cancel()
	if _, err := winners[0].Run(nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("release winning lock: %v", err)
	}
}

func TestInstallerChecksumFailureCleansOnlyOwnedStaging(t *testing.T) {
	installer, model, _ := testInstaller(t, []byte("wrong-payload"))
	model.SHA256 = strings.Repeat("0", 64)
	installer.Manifest.Models[0] = model
	preview, _ := installer.Preview(model.ID)
	task, err := installer.Begin(model.ID, preview.ConfirmationID)
	if err != nil {
		t.Fatal(err)
	}
	unowned := filepath.Join(installer.Root, ".cpp-studio-install-unowned")
	if err := os.MkdirAll(unowned, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Run(nil); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("wanted checksum failure, got %v", err)
	}
	if _, err := os.Stat(unowned); err != nil {
		t.Fatalf("unowned directory was removed: %v", err)
	}
	if _, err := os.Stat(preview.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed artifact was promoted: %v", err)
	}
	entries, _ := os.ReadDir(installer.Root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".cpp-studio-install-") && entry.Name() != filepath.Base(unowned) {
			t.Fatalf("owned staging survived failure: %s", entry.Name())
		}
	}
}

func TestInstallerStartupCleanupRequiresValidatedMarker(t *testing.T) {
	root := t.TempDir()
	owned := filepath.Join(root, ".cpp-studio-install-owned")
	invalid := filepath.Join(root, ".cpp-studio-install-a-invalid")
	unowned := filepath.Join(root, ".cpp-studio-install-unowned")
	ordinary := filepath.Join(root, "ordinary-model-directory")
	for _, path := range []string{owned, invalid, unowned, ordinary} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	absRoot, _ := filepath.Abs(root)
	marker, _ := json.Marshal(installerMarker{Schema: "cpp-studio-model-install.v1", Root: absRoot, Destination: filepath.Join(absRoot, "models", "x.gguf"), ModelID: "x"})
	if err := os.WriteFile(filepath.Join(owned, installerMarkerName), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalid, installerMarkerName), []byte(`{"schema":"unowned"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = NewInstaller(Manifest{}, root, nil)
	if _, err := os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned orphan survived: %v", err)
	}
	if _, err := os.Stat(unowned); err != nil {
		t.Fatalf("unowned directory was removed: %v", err)
	}
	if _, err := os.Stat(invalid); err != nil {
		t.Fatalf("invalid-marker directory was removed: %v", err)
	}
	if _, err := os.Stat(ordinary); err != nil {
		t.Fatalf("ordinary model directory was removed: %v", err)
	}
}

func TestInstallFingerprintChangesWithImmutableInputs(t *testing.T) {
	model := Model{ID: "x", Revision: "one"}
	base := installFingerprint(model, "root", "dest")
	model.Revision = "two"
	if reflect.DeepEqual(base, installFingerprint(model, "root", "dest")) {
		t.Fatal("revision change did not stale confirmation")
	}
}

func TestInstallerRejectsStaleConfirmationAndPartialDownload(t *testing.T) {
	installer, model, _ := testInstaller(t, []byte("partial"))
	preview, _ := installer.Preview(model.ID)
	installer.Manifest.Models[0].Revision = "changed-after-preview"
	if _, err := installer.Begin(model.ID, preview.ConfirmationID); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("changed catalog accepted stale confirmation: %v", err)
	}

	installer.Manifest.Models[0] = model
	installer.Manifest.Models[0].Bytes++
	preview, _ = installer.Preview(model.ID)
	task, err := installer.Begin(model.ID, preview.ConfirmationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := task.Run(nil); err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("partial download was accepted: %v", err)
	}
	if _, err := os.Stat(preview.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial artifact was promoted: %v", err)
	}
}

func TestInstallerCancellationBecomesUnavailableAtPromotion(t *testing.T) {
	installer, model, _ := testInstaller(t, []byte("verified"))
	preview, _ := installer.Preview(model.ID)
	task, err := installer.Begin(model.ID, preview.ConfirmationID)
	if err != nil {
		t.Fatal(err)
	}
	var cancelErr error
	status, err := task.Run(func(progress InstallProgress) {
		if progress.Phase == "promoting" {
			cancelErr = task.Cancel()
		}
	})
	if err != nil || status.State != StateVerified {
		t.Fatalf("promotion failed: %+v %v", status, err)
	}
	if cancelErr == nil || !strings.Contains(cancelErr.Error(), "promotion") {
		t.Fatalf("late cancellation was accepted: %v", cancelErr)
	}
}

func TestPromotionNeverOverwritesAnExistingDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "staged.part")
	destination := filepath.Join(root, "model.gguf")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := promoteNoReplace(source, destination); err == nil {
		t.Fatal("promotion overwrote an existing destination")
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "existing" {
		t.Fatalf("existing destination changed: %q, %v", data, err)
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
