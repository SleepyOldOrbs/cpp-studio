package models

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	confirmationLifetime = 5 * time.Minute
	lowDiskThreshold     = uint64(5 * 1024 * 1024 * 1024)
	installerMarkerName  = ".cpp-studio-installer.json"
)

var (
	ErrInstallNotAllowed = errors.New("model installation is not allowed")
	ErrConfirmation      = errors.New("invalid installation confirmation")
	ErrInstallConflict   = errors.New("model installation already in progress")
)

type FreeSpaceFunc func(string) (uint64, error)

type Installer struct {
	Manifest       Manifest
	Root           string
	Allowed        map[string]bool
	Client         *http.Client
	Now            func() time.Time
	Free           FreeSpaceFunc
	CleanupWarning string

	mu            sync.Mutex
	confirmations map[string]installConfirmation
	locks         map[string]bool
}

type InstallPreview struct {
	ConfirmationID string    `json:"confirmationId,omitempty"`
	ExpiresAt      time.Time `json:"expiresAt,omitempty"`
	ModelID        string    `json:"modelId"`
	Source         string    `json:"source"`
	Revision       string    `json:"revision"`
	Destination    string    `json:"destination"`
	Licence        string    `json:"licence"`
	ExpectedBytes  int64     `json:"expectedBytes"`
	Checksum       string    `json:"checksum"`
	FreeSpace      uint64    `json:"freeSpace"`
	VRAMWarning    string    `json:"vramWarning,omitempty"`
	Warnings       []string  `json:"warnings"`
	Blockers       []string  `json:"blockers"`
}

type InstallProgress struct {
	Phase         string
	Downloaded    int64
	ExpectedBytes int64
}

type installConfirmation struct {
	ID          string
	ExpiresAt   time.Time
	ModelID     string
	Fingerprint string
	Destination string
	Used        bool
}

type installerMarker struct {
	Schema      string `json:"schema"`
	Root        string `json:"root"`
	Destination string `json:"destination"`
	ModelID     string `json:"modelId"`
}

type InstallTask struct {
	installer   *Installer
	model       Model
	destination string

	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	cancellable bool
}

func NewInstaller(manifest Manifest, root string, allowed []string) *Installer {
	allow := make(map[string]bool, len(allowed))
	for _, packageID := range allowed {
		if packageID != "" {
			allow[packageID] = true
		}
	}
	i := &Installer{
		Manifest:      manifest,
		Root:          root,
		Allowed:       allow,
		Client:        http.DefaultClient,
		Now:           func() time.Time { return time.Now().UTC() },
		Free:          diskFreeSpace,
		confirmations: map[string]installConfirmation{},
		locks:         map[string]bool{},
	}
	if err := i.CleanupOrphans(); err != nil {
		i.CleanupWarning = err.Error()
	}
	return i
}

func (i *Installer) Preview(modelID string) (InstallPreview, error) {
	model, ok := i.model(modelID)
	if !ok {
		return InstallPreview{}, fmt.Errorf("unknown model id %q", modelID)
	}
	destination, pathErr := secureDestination(i.Root, model.Path)
	preview := InstallPreview{
		ModelID: model.ID, Source: model.DownloadURL, Revision: model.Revision,
		Destination: destination, Licence: model.License, ExpectedBytes: model.Bytes,
		Checksum: model.SHA256,
		Warnings: []string{"Local installation and generation consume storage, compute, and electricity even when there is no hosted usage charge."},
		Blockers: []string{},
	}
	if i.CleanupWarning != "" {
		preview.Warnings = append(preview.Warnings, "installer orphan cleanup needs attention: "+i.CleanupWarning)
	}
	if !i.Allowed[model.PackageID] {
		preview.Blockers = append(preview.Blockers, "package is not allowlisted by this server")
	}
	if !model.HasImmutableInstallMetadata() {
		preview.Blockers = append(preview.Blockers, "catalog entry lacks immutable source, revision, size, checksum, or licence metadata")
	}
	if pathErr != nil {
		preview.Blockers = append(preview.Blockers, pathErr.Error())
	}
	if destination != "" {
		if _, err := os.Stat(destination); err == nil {
			preview.Blockers = append(preview.Blockers, "destination already exists; installation never overwrites")
		} else if !errors.Is(err, os.ErrNotExist) {
			preview.Blockers = append(preview.Blockers, "destination cannot be inspected: "+err.Error())
		}
	}
	if i.Free != nil && i.Root != "" {
		free, err := i.Free(i.Root)
		if err != nil {
			preview.Blockers = append(preview.Blockers, "free disk space cannot be determined: "+err.Error())
		} else {
			preview.FreeSpace = free
			if model.Bytes > 0 && free < uint64(model.Bytes) {
				preview.Blockers = append(preview.Blockers, "insufficient free disk space")
			} else if model.Bytes > 0 && free-uint64(model.Bytes) < lowDiskThreshold {
				preview.Warnings = append(preview.Warnings, "less than 5 GiB would remain after installation")
			}
		}
	}
	if model.Bytes >= 16*1024*1024*1024 {
		preview.VRAMWarning = "This model is larger than 16 GiB; GPU fit depends on runtime overhead, and CPU or memory-saving operation may be required."
	}
	if len(preview.Blockers) > 0 {
		return preview, nil
	}
	now := i.Now()
	id, err := randomID(24)
	if err != nil {
		return InstallPreview{}, err
	}
	confirmation := installConfirmation{
		ID: id, ExpiresAt: now.Add(confirmationLifetime), ModelID: model.ID,
		Fingerprint: installFingerprint(model, i.Root, destination), Destination: destination,
	}
	i.mu.Lock()
	i.pruneConfirmationsLocked(now)
	i.confirmations[id] = confirmation
	i.mu.Unlock()
	preview.ConfirmationID = id
	preview.ExpiresAt = confirmation.ExpiresAt
	return preview, nil
}

func (i *Installer) Begin(modelID, confirmationID string) (*InstallTask, error) {
	model, ok := i.model(modelID)
	if !ok {
		return nil, fmt.Errorf("unknown model id %q", modelID)
	}
	destination, err := secureDestination(i.Root, model.Path)
	if err != nil {
		return nil, err
	}
	now := i.Now()
	i.mu.Lock()
	defer i.mu.Unlock()
	i.pruneConfirmationsLocked(now)
	confirmation, ok := i.confirmations[confirmationID]
	if !ok || confirmation.Used || confirmation.ModelID != modelID || !confirmation.ExpiresAt.After(now) {
		return nil, ErrConfirmation
	}
	confirmation.Used = true
	i.confirmations[confirmationID] = confirmation
	if !i.Allowed[model.PackageID] || !model.HasImmutableInstallMetadata() ||
		confirmation.Destination != destination || confirmation.Fingerprint != installFingerprint(model, i.Root, destination) {
		return nil, fmt.Errorf("%w: catalog or server configuration changed", ErrConfirmation)
	}
	if _, err := os.Stat(destination); err == nil {
		return nil, fmt.Errorf("%w: destination already exists", ErrInstallNotAllowed)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect destination: %w", err)
	}
	if i.locks[destination] {
		return nil, ErrInstallConflict
	}
	i.locks[destination] = true
	ctx, cancel := context.WithCancel(context.Background())
	return &InstallTask{installer: i, model: model, destination: destination, ctx: ctx, cancel: cancel, cancellable: true}, nil
}

func (t *InstallTask) Cancel() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.cancellable {
		return errors.New("installation can no longer be cancelled because promotion has begun")
	}
	t.cancel()
	return nil
}

func (t *InstallTask) Run(progress func(InstallProgress)) (Status, error) {
	i := t.installer
	defer func() {
		i.mu.Lock()
		delete(i.locks, t.destination)
		i.mu.Unlock()
	}()
	report := func(phase string, downloaded int64) {
		if progress != nil {
			progress(InstallProgress{Phase: phase, Downloaded: downloaded, ExpectedBytes: t.model.Bytes})
		}
	}
	report("preparing", 0)
	if err := t.ctx.Err(); err != nil {
		return Status{}, err
	}
	root, err := filepath.Abs(i.Root)
	if err != nil {
		return Status{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Status{}, fmt.Errorf("create models root: %w", err)
	}
	staging, err := os.MkdirTemp(root, ".cpp-studio-install-")
	if err != nil {
		return Status{}, fmt.Errorf("create staging: %w", err)
	}
	marker := installerMarker{Schema: "cpp-studio-model-install.v1", Root: root, Destination: t.destination, ModelID: t.model.ID}
	markerBytes, _ := json.Marshal(marker)
	if err := os.WriteFile(filepath.Join(staging, installerMarkerName), markerBytes, 0o600); err != nil {
		_ = os.RemoveAll(staging)
		return Status{}, fmt.Errorf("mark staging: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = i.removeValidatedStaging(staging)
		}
	}()
	part := filepath.Join(staging, "artifact.part")
	if err := t.download(part, report); err != nil {
		return Status{}, err
	}
	report("verifying", t.model.Bytes)
	if err := t.ctx.Err(); err != nil {
		return Status{}, err
	}
	info, err := os.Stat(part)
	if err != nil {
		return Status{}, err
	}
	if info.Size() != t.model.Bytes {
		return Status{}, fmt.Errorf("downloaded size mismatch: got %d, expected %d", info.Size(), t.model.Bytes)
	}
	sum, err := sha256File(t.ctx, part)
	if err != nil {
		return Status{}, err
	}
	if !strings.EqualFold(sum, t.model.SHA256) {
		return Status{}, fmt.Errorf("downloaded checksum mismatch")
	}
	checkedDestination, err := secureDestination(i.Root, t.model.Path)
	if err != nil {
		return Status{}, fmt.Errorf("destination safety changed during installation: %w", err)
	}
	if checkedDestination != t.destination {
		return Status{}, errors.New("destination safety changed during installation")
	}
	if err := os.MkdirAll(filepath.Dir(t.destination), 0o755); err != nil {
		return Status{}, fmt.Errorf("create destination directory: %w", err)
	}
	checkedDestination, err = secureDestination(i.Root, t.model.Path)
	if err != nil {
		return Status{}, fmt.Errorf("destination safety changed during installation: %w", err)
	}
	if checkedDestination != t.destination {
		return Status{}, errors.New("destination safety changed during installation")
	}
	if _, err := os.Stat(t.destination); err == nil {
		return Status{}, fmt.Errorf("destination appeared during installation; refusing to overwrite")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	t.mu.Lock()
	if err := t.ctx.Err(); err != nil {
		t.mu.Unlock()
		return Status{}, err
	}
	t.cancellable = false
	t.mu.Unlock()
	report("promoting", t.model.Bytes)
	if err := promoteNoReplace(part, t.destination); err != nil {
		return Status{}, fmt.Errorf("promote verified artifact: %w", err)
	}
	report("refreshing_catalog", t.model.Bytes)
	cleanup = false
	_ = i.removeValidatedStaging(staging)
	status, err := i.Manifest.Verify(context.Background(), t.model.ID, i.Root)
	if err != nil {
		return status, fmt.Errorf("artifact promoted but catalog verification failed: %w", err)
	}
	if status.State != StateVerified {
		return status, fmt.Errorf("artifact promoted but is not ready: %s", status.State)
	}
	return status, nil
}

func (t *InstallTask) download(path string, progress func(string, int64)) error {
	parsed, err := url.Parse(t.model.DownloadURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("tracked download URL must be absolute HTTPS")
	}
	request, err := http.NewRequestWithContext(t.ctx, http.MethodGet, t.model.DownloadURL, nil)
	if err != nil {
		return err
	}
	if token := strings.TrimSpace(os.Getenv("HF_TOKEN")); token != "" && strings.EqualFold(parsed.Hostname(), "huggingface.co") {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := t.installer.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := make([]byte, 1024*1024)
	var downloaded int64
	for {
		if err := t.ctx.Err(); err != nil {
			return err
		}
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			downloaded += int64(n)
			if downloaded > t.model.Bytes {
				return errors.New("download exceeded tracked expected size")
			}
			if _, err := file.Write(buffer[:n]); err != nil {
				return err
			}
			progress("downloading", downloaded)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return file.Sync()
}

func (i *Installer) CleanupOrphans() error {
	root, err := filepath.Abs(i.Root)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var cleanupErr error
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".cpp-studio-install-") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if err := i.removeValidatedStaging(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func (i *Installer) removeValidatedStaging(staging string) error {
	root, err := filepath.Abs(i.Root)
	if err != nil {
		return err
	}
	stage, err := filepath.Abs(staging)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, stage)
	if err != nil || outsideRelativePath(rel) {
		return errors.New("refusing to clean staging outside models root")
	}
	if !strings.HasPrefix(filepath.Base(stage), ".cpp-studio-install-") {
		return errors.New("refusing to clean directory without installer staging name")
	}
	data, err := os.ReadFile(filepath.Join(stage, installerMarkerName))
	if err != nil {
		return err
	}
	var marker installerMarker
	if json.Unmarshal(data, &marker) != nil || marker.Schema != "cpp-studio-model-install.v1" || marker.Root != root {
		return errors.New("refusing to clean unowned staging directory")
	}
	if !filepath.IsAbs(marker.Destination) {
		return errors.New("invalid installer marker destination")
	}
	destination, err := filepath.Abs(marker.Destination)
	if err != nil {
		return err
	}
	destinationRel, err := filepath.Rel(root, destination)
	if err != nil || outsideRelativePath(destinationRel) {
		return errors.New("refusing to clean marker with destination outside models root")
	}
	return os.RemoveAll(stage)
}

func (i *Installer) model(id string) (Model, bool) {
	for _, model := range i.Manifest.Models {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

func (i *Installer) pruneConfirmationsLocked(now time.Time) {
	for id, confirmation := range i.confirmations {
		if confirmation.Used || !confirmation.ExpiresAt.After(now) {
			delete(i.confirmations, id)
		}
	}
}

func secureDestination(root, relative string) (string, error) {
	if root == "" {
		return "", errors.New("models root is not configured")
	}
	if relative == "" || filepath.IsAbs(relative) {
		return "", errors.New("catalog destination must be relative to the models root")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	destination, err := filepath.Abs(filepath.Join(absRoot, relative))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, destination)
	if err != nil || outsideRelativePath(rel) {
		return "", errors.New("catalog destination escapes the models root")
	}
	resolvedRoot, err := resolveExistingPath(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve models root: %w", err)
	}
	resolvedDestination, err := resolveExistingPath(destination)
	if err != nil {
		return "", fmt.Errorf("resolve catalog destination: %w", err)
	}
	resolvedRel, err := filepath.Rel(resolvedRoot, resolvedDestination)
	if err != nil || outsideRelativePath(resolvedRel) {
		return "", errors.New("catalog destination resolves outside the models root")
	}
	return destination, nil
}

func outsideRelativePath(relative string) bool {
	return relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative)
}

// resolveExistingPath resolves links/junctions in the longest existing
// prefix, then appends any not-yet-created suffix. This keeps a catalog path
// from escaping through an existing link while still allowing a new root.
func resolveExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := abs
	suffix := []string{}
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return filepath.Abs(resolved)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func installFingerprint(model Model, root, destination string) string {
	data, _ := json.Marshal(struct {
		Model       Model
		Root        string
		Destination string
	}{model, root, destination})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func randomID(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
