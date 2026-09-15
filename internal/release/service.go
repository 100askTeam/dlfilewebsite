package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"aead.dev/minisign"

	"dladmin-go/internal/store"
)

var incomingIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Service struct {
	store       *store.Store
	stateDir    string
	publicDir   string
	incomingDir string
	stagedDir   string
	publicKey   minisign.PublicKey
}

type SelectedUpdate struct {
	Available   bool         `json:"available"`
	Version     string       `json:"version,omitempty"`
	Notes       string       `json:"notes,omitempty"`
	PublishedAt string       `json:"published_at,omitempty"`
	Strategy    string       `json:"strategy,omitempty"`
	Asset       *UpdateAsset `json:"asset,omitempty"`
	Fallback    *UpdateAsset `json:"fallback,omitempty"`
}

type UpdateAsset struct {
	Kind        string   `json:"kind"`
	FromVersion string   `json:"from_version,omitempty"`
	URL         string   `json:"url"`
	Mirrors     []string `json:"mirrors,omitempty"`
	Size        int64    `json:"size"`
	SHA256      string   `json:"sha256"`
	Signature   string   `json:"signature"`
}

type LayoutMigration struct {
	ReleaseID int64  `json:"release_id"`
	From      string `json:"from"`
	To        string `json:"to"`
}

func NewService(storage *store.Store, stateDir, publicDir, publicKeyText string) (*Service, error) {
	if storage == nil {
		return nil, errors.New("release store is required")
	}
	key, err := DecodePublicKey(publicKeyText)
	if err != nil {
		return nil, err
	}
	stateDir, err = filepath.Abs(stateDir)
	if err != nil {
		return nil, err
	}
	publicDir, err = filepath.Abs(publicDir)
	if err != nil {
		return nil, err
	}
	service := &Service{
		store: storage, stateDir: stateDir, publicDir: publicDir, publicKey: key,
		incomingDir: filepath.Join(stateDir, "incoming"),
		stagedDir:   filepath.Join(stateDir, "staged"),
	}
	for _, directory := range []string{service.incomingDir, service.stagedDir} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return nil, fmt.Errorf("create release directory: %w", err)
		}
	}
	if err := ensureDirectoryTree(publicDir, PublicToolsDirectory, 0o755); err != nil {
		return nil, fmt.Errorf("create public Tools directory: %w", err)
	}
	return service, nil
}

func (s *Service) IncomingDir() string { return s.incomingDir }

func (s *Service) ImportIncoming(ctx context.Context, incomingID string, actorID int64) (store.ReleaseRecord, error) {
	if !incomingIDPattern.MatchString(incomingID) || incomingID == "." || incomingID == ".." {
		return store.ReleaseRecord{}, errors.New("invalid incoming release identifier")
	}
	source := filepath.Join(s.incomingDir, incomingID)
	if err := requireRealDirectory(source); err != nil {
		return store.ReleaseRecord{}, err
	}
	manifest, err := LoadAndVerify(source, s.publicKey)
	if err != nil {
		return store.ReleaseRecord{}, err
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return store.ReleaseRecord{}, err
	}
	relativeStaged := filepath.Join("staged", manifest.Product, manifest.Channel, manifest.Version)
	destination := filepath.Join(s.stateDir, relativeStaged)
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return store.ReleaseRecord{}, err
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return store.ReleaseRecord{}, errors.New("release is already staged")
		}
		return store.ReleaseRecord{}, err
	}
	if err := os.Rename(source, destination); err != nil {
		return store.ReleaseRecord{}, fmt.Errorf("stage release (incoming and state directories must share a filesystem): %w", err)
	}
	assets := make([]store.ReleaseAssetInput, 0, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		mirrors, _ := json.Marshal(asset.Mirrors)
		assets = append(assets, store.ReleaseAssetInput{
			Target: asset.Target, Kind: asset.Kind, FromVersion: strings.TrimPrefix(asset.FromVersion, "v"),
			FileName: asset.File, Size: asset.Size, SHA256: strings.ToLower(asset.SHA256),
			Signature: asset.Signature, MirrorsJSON: string(mirrors),
		})
	}
	id, err := s.store.StageRelease(ctx, store.ReleaseRecord{
		Product: manifest.Product, Channel: manifest.Channel, Version: strings.TrimPrefix(manifest.Version, "v"),
		Notes: manifest.Notes, ManifestJSON: string(manifestJSON), StagedPath: relativeStaged, CreatedBy: actorID,
	}, assets)
	if err != nil {
		if rollbackErr := os.Rename(destination, source); rollbackErr != nil {
			return store.ReleaseRecord{}, fmt.Errorf("record staged release: %v; rollback failed: %w", err, rollbackErr)
		}
		return store.ReleaseRecord{}, err
	}
	return s.store.Release(ctx, id)
}

func (s *Service) Publish(ctx context.Context, id int64) (store.ReleaseRecord, error) {
	record, err := s.store.Release(ctx, id)
	if err != nil {
		return store.ReleaseRecord{}, err
	}
	if record.Status != "staged" {
		return store.ReleaseRecord{}, fmt.Errorf("release is %s, expected staged", record.Status)
	}
	nextVersion, err := ParseVersion(record.Version)
	if err != nil {
		return store.ReleaseRecord{}, err
	}
	if head, headErr := s.store.ChannelHead(ctx, record.Product, record.Channel); headErr == nil {
		currentVersion, parseErr := ParseVersion(head.Version)
		if parseErr != nil || nextVersion.Compare(currentVersion) <= 0 {
			return store.ReleaseRecord{}, fmt.Errorf("version %s must be newer than channel head %s", record.Version, head.Version)
		}
	} else if !errors.Is(headErr, store.ErrNotFound) {
		return store.ReleaseRecord{}, headErr
	}

	source, err := containedPath(s.stateDir, record.StagedPath)
	if err != nil {
		return store.ReleaseRecord{}, err
	}
	if err := requireRealDirectory(source); err != nil {
		return store.ReleaseRecord{}, err
	}
	manifest, err := LoadAndVerify(source, s.publicKey)
	if err != nil {
		return store.ReleaseRecord{}, fmt.Errorf("revalidate staged release: %w", err)
	}
	if manifest.Product != record.Product || manifest.Channel != record.Channel || strings.TrimPrefix(manifest.Version, "v") != record.Version {
		return store.ReleaseRecord{}, errors.New("staged manifest identity changed")
	}
	if err := makePublicTree(source); err != nil {
		return store.ReleaseRecord{}, err
	}
	relativePublished := PublishedPath(record.Product, record.Channel, record.Version)
	destination := filepath.Join(s.publicDir, relativePublished)
	if err := ensureDirectoryTree(s.publicDir, filepath.Dir(relativePublished), 0o755); err != nil {
		return store.ReleaseRecord{}, err
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return store.ReleaseRecord{}, errors.New("published release directory already exists")
		}
		return store.ReleaseRecord{}, err
	}
	if err := os.Rename(source, destination); err != nil {
		return store.ReleaseRecord{}, fmt.Errorf("publish release (state and public directories must share a filesystem): %w", err)
	}
	if err := s.store.PublishRelease(ctx, id, filepath.ToSlash(relativePublished)); err != nil {
		if rollbackErr := os.Rename(destination, source); rollbackErr != nil {
			return store.ReleaseRecord{}, fmt.Errorf("record published release: %v; rollback failed: %w", err, rollbackErr)
		}
		return store.ReleaseRecord{}, err
	}
	return s.store.Release(ctx, id)
}

// MigrateLegacyLayout moves previously published root-level releases into the
// canonical /Tools/<product>/<channel>/<version> tree and updates each database record.
// Run this with the web service stopped so file and metadata changes are not
// observed halfway through the migration.
func (s *Service) MigrateLegacyLayout(ctx context.Context, dryRun bool) ([]LayoutMigration, error) {
	records, err := s.store.LegacyPublishedReleases(ctx)
	if err != nil {
		return nil, err
	}
	migrations := make([]LayoutMigration, 0, len(records))
	for _, record := range records {
		from := legacyPublishedPath(record.Product, record.Channel, record.Version)
		if filepath.ToSlash(record.PublishedPath) != from {
			return nil, fmt.Errorf("release %d has unexpected legacy path %q", record.ID, record.PublishedPath)
		}
		to := PublishedPath(record.Product, record.Channel, record.Version)
		source, err := containedPath(s.publicDir, from)
		if err != nil {
			return nil, err
		}
		destination, err := containedPath(s.publicDir, to)
		if err != nil {
			return nil, err
		}
		sourceInfo, sourceErr := os.Lstat(source)
		destinationInfo, destinationErr := os.Lstat(destination)
		sourceExists := sourceErr == nil
		destinationExists := destinationErr == nil
		if sourceErr != nil && !errors.Is(sourceErr, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect legacy release %d: %w", record.ID, sourceErr)
		}
		if destinationErr != nil && !errors.Is(destinationErr, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect canonical release %d: %w", record.ID, destinationErr)
		}
		if sourceExists && (!sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0) {
			return nil, fmt.Errorf("legacy release %d is not a real directory", record.ID)
		}
		if sourceExists {
			if err := requireSafeExistingDirectory(s.publicDir, from); err != nil {
				return nil, fmt.Errorf("inspect legacy release %d path: %w", record.ID, err)
			}
		}
		if destinationExists && (!destinationInfo.IsDir() || destinationInfo.Mode()&os.ModeSymlink != 0) {
			return nil, fmt.Errorf("canonical release %d is not a real directory", record.ID)
		}
		if destinationExists {
			if err := requireSafeExistingDirectory(s.publicDir, to); err != nil {
				return nil, fmt.Errorf("inspect canonical release %d path: %w", record.ID, err)
			}
		}
		if sourceExists && destinationExists {
			return nil, fmt.Errorf("release %d exists in both legacy and canonical layouts", record.ID)
		}
		if !sourceExists && !destinationExists {
			return nil, fmt.Errorf("release %d has no files in either public layout", record.ID)
		}
		verifyDirectory := destination
		if sourceExists {
			verifyDirectory = source
		}
		if _, err := LoadAndVerify(verifyDirectory, s.publicKey); err != nil {
			return nil, fmt.Errorf("verify release %d before migration: %w", record.ID, err)
		}
		migration := LayoutMigration{ReleaseID: record.ID, From: from, To: to}
		if dryRun {
			migrations = append(migrations, migration)
			continue
		}

		moved := false
		if sourceExists {
			if err := ensureDirectoryTree(s.publicDir, filepath.Dir(to), 0o755); err != nil {
				return nil, fmt.Errorf("create canonical release parent: %w", err)
			}
			if err := os.Rename(source, destination); err != nil {
				return nil, fmt.Errorf("move release %d to Tools layout: %w", record.ID, err)
			}
			moved = true
		}
		if err := s.store.UpdatePublishedPath(ctx, record.ID, from, to); err != nil {
			if moved {
				if rollbackErr := os.Rename(destination, source); rollbackErr != nil {
					return nil, fmt.Errorf("update release %d path: %v; rollback failed: %w", record.ID, err, rollbackErr)
				}
			}
			return nil, fmt.Errorf("update release %d path: %w", record.ID, err)
		}
		migrations = append(migrations, migration)
		removeEmptyLegacyParents(filepath.Dir(source), filepath.Join(s.publicDir, LegacyReleaseDirectoryName))
	}
	return migrations, nil
}

func removeEmptyLegacyParents(current, stop string) {
	stop = filepath.Clean(stop)
	for current = filepath.Clean(current); strings.HasPrefix(current, stop); current = filepath.Dir(current) {
		if err := os.Remove(current); err != nil || current == stop {
			return
		}
	}
}

func makePublicTree(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read staged release permissions: %w", err)
	}
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse unsafe public release entry: %s", entry.Name())
		}
		if err := os.Chmod(path, 0o644); err != nil {
			return fmt.Errorf("make release asset public-readable: %w", err)
		}
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		return fmt.Errorf("make release directory traversable: %w", err)
	}
	return nil
}

func (s *Service) SelectUpdate(ctx context.Context, product, channel, target, current string) (SelectedUpdate, error) {
	currentVersion, err := ParseVersion(current)
	if err != nil {
		return SelectedUpdate{}, err
	}
	head, err := s.store.ChannelHead(ctx, product, channel)
	if errors.Is(err, store.ErrNotFound) {
		return SelectedUpdate{Available: false}, nil
	}
	if err != nil {
		return SelectedUpdate{}, err
	}
	headVersion, err := ParseVersion(head.Version)
	if err != nil {
		return SelectedUpdate{}, err
	}
	if currentVersion.Compare(headVersion) >= 0 {
		return SelectedUpdate{Available: false}, nil
	}
	assets, err := s.store.ReleaseAssets(ctx, head.ID)
	if err != nil {
		return SelectedUpdate{}, err
	}
	var full, delta *store.ReleaseAssetInput
	for i := range assets {
		asset := assets[i]
		if asset.Target != target {
			continue
		}
		if asset.Kind == "full" {
			copy := asset
			full = &copy
		}
		if asset.Kind == "delta" && asset.FromVersion == strings.TrimPrefix(current, "v") && DeltaCompatible(currentVersion, headVersion) {
			copy := asset
			delta = &copy
		}
	}
	if full == nil {
		return SelectedUpdate{}, fmt.Errorf("release has no full asset for target %s", target)
	}
	selected, strategy := full, "full"
	if delta != nil {
		selected, strategy = delta, "delta"
	}
	basePath := "/" + strings.TrimPrefix(filepath.ToSlash(head.PublishedPath), "/") + "/"
	toUpdateAsset := func(asset *store.ReleaseAssetInput) *UpdateAsset {
		if asset == nil {
			return nil
		}
		var mirrors []string
		_ = json.Unmarshal([]byte(asset.MirrorsJSON), &mirrors)
		return &UpdateAsset{
			Kind: asset.Kind, FromVersion: asset.FromVersion, URL: basePath + asset.FileName,
			Mirrors: mirrors, Size: asset.Size, SHA256: asset.SHA256, Signature: asset.Signature,
		}
	}
	var fallback *UpdateAsset
	if strategy == "delta" {
		fallback = toUpdateAsset(full)
	}
	return SelectedUpdate{
		Available: true, Version: head.Version, Notes: head.Notes, PublishedAt: head.PublishedAt,
		Strategy: strategy, Asset: toUpdateAsset(selected), Fallback: fallback,
	}, nil
}

func (s *Service) SelectFullUpdate(ctx context.Context, product, channel, target, current string) (SelectedUpdate, error) {
	update, err := s.SelectUpdate(ctx, product, channel, target, current)
	if err != nil || !update.Available || update.Strategy == "full" {
		return update, err
	}
	if update.Fallback == nil {
		return SelectedUpdate{}, errors.New("delta update has no full fallback")
	}
	update.Strategy = "full"
	update.Asset = update.Fallback
	update.Fallback = nil
	return update, nil
}

func containedPath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("stored path must be relative")
	}
	root = filepath.Clean(root)
	resolved := filepath.Clean(filepath.Join(root, relative))
	if resolved == root || !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", errors.New("stored path escapes configured root")
	}
	return resolved, nil
}

func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("read release directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("release source must be a real directory")
	}
	return nil
}

func ensureDirectoryTree(root, relative string, mode os.FileMode) error {
	if filepath.IsAbs(relative) {
		return errors.New("directory path must be relative")
	}
	root = filepath.Clean(root)
	current := root
	for _, component := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		if component == ".." {
			return errors.New("directory path escapes configured root")
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, mode); err != nil {
				return fmt.Errorf("create directory %s: %w", component, err)
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("directory path contains unsafe component %s", component)
		}
	}
	return nil
}

func requireSafeExistingDirectory(root, relative string) error {
	if err := ensureDirectoryTree(root, filepath.Dir(relative), 0o755); err != nil {
		return err
	}
	directory, err := containedPath(root, relative)
	if err != nil {
		return err
	}
	return requireRealDirectory(directory)
}
