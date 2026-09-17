package release

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aead.dev/minisign"

	"dladmin-go/internal/store"
)

func newReleaseServiceFixture(t *testing.T) (*Service, *store.Store, minisign.PrivateKey, int64, string) {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	publicDir := filepath.Join(root, "public")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	storage, err := store.Open(filepath.Join(stateDir, "dladmin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	userID, err := storage.CreateUser(context.Background(), "publisher", "hash", "release_manager")
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyText, err := publicKey.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := NewPublicKeyring(map[string]string{
		"lynx":       string(keyText),
		"usbtoolbox": string(keyText),
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewServiceWithKeyring(storage, stateDir, publicDir, keyring)
	if err != nil {
		t.Fatal(err)
	}
	return service, storage, privateKey, userID, publicDir
}

func putIncomingRelease(t *testing.T, service *Service, privateKey minisign.PrivateKey, incomingID, version string, assets []Asset) {
	putIncomingProductRelease(t, service, privateKey, incomingID, "lynx", version, assets)
}

func putIncomingProductRelease(t *testing.T, service *Service, privateKey minisign.PrivateKey, incomingID, product, version string, assets []Asset) {
	t.Helper()
	directory := filepath.Join(service.IncomingDir(), incomingID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	for index := range assets {
		asset := &assets[index]
		data := []byte("signed bytes for " + asset.File)
		if err := os.WriteFile(filepath.Join(directory, asset.File), data, 0o640); err != nil {
			t.Fatal(err)
		}
		reader := minisign.NewReader(bytes.NewReader(data))
		if _, err := io.Copy(io.Discard, reader); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		asset.Size = int64(len(data))
		asset.SHA256 = hex.EncodeToString(sum[:])
		asset.Signature = base64.StdEncoding.EncodeToString(reader.Sign(privateKey))
	}
	manifest := Manifest{
		SchemaVersion: 1, Product: product, Channel: "stable", Version: version,
		Notes: "release " + version, Assets: assets,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ManifestName), data, 0o640); err != nil {
		t.Fatal(err)
	}
}

func putIncomingV2Release(t *testing.T, service *Service, privateKey minisign.PrivateKey, incomingID, version string, assets []Asset) {
	t.Helper()
	directory := filepath.Join(service.IncomingDir(), incomingID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	for index := range assets {
		asset := &assets[index]
		data := []byte("signed bytes for " + asset.File)
		if asset.Storage == "site" {
			if err := os.WriteFile(filepath.Join(directory, asset.File), data, 0o640); err != nil {
				t.Fatal(err)
			}
		}
		reader := minisign.NewReader(bytes.NewReader(data))
		if _, err := io.Copy(io.Discard, reader); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		asset.Size = int64(len(data))
		asset.SHA256 = hex.EncodeToString(sum[:])
		asset.Signature = base64.StdEncoding.EncodeToString(reader.Sign(privateKey))
	}
	manifest := Manifest{SchemaVersion: 2, Product: "lynx", Channel: "stable", Version: version, Notes: "release " + version, Assets: assets}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ManifestName), data, 0o640); err != nil {
		t.Fatal(err)
	}
	reader := minisign.NewReader(bytes.NewReader(data))
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ManifestSignature), []byte(base64.StdEncoding.EncodeToString(reader.Sign(privateKey))), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestPublishUsesSameToolsContractForUSBToolBox(t *testing.T) {
	service, _, privateKey, userID, publicDir := newReleaseServiceFixture(t)
	putIncomingProductRelease(t, service, privateKey, "usbtoolbox-job", "usbtoolbox", "1.0.1", []Asset{{
		Target: "windows-x86_64", Kind: "full", File: "USBToolBox_1.0.1_x64-setup.exe",
	}})
	staged, err := service.ImportIncoming(context.Background(), "usbtoolbox-job", userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), staged.ID); err != nil {
		t.Fatal(err)
	}
	assetPath := filepath.Join(publicDir, "Tools", "usbtoolbox", "stable", "1.0.1", "USBToolBox_1.0.1_x64-setup.exe")
	if _, err := os.Stat(assetPath); err != nil {
		t.Fatal(err)
	}
	update, err := service.SelectUpdate(context.Background(), "usbtoolbox", "stable", "windows-x86_64", "1.0.0")
	if err != nil || update.Asset == nil || update.Asset.URL != "/Tools/usbtoolbox/stable/1.0.1/USBToolBox_1.0.1_x64-setup.exe" {
		t.Fatalf("unexpected USBToolBox update: %#v, %v", update, err)
	}
}

func TestImportPublishAndSelectExactDelta(t *testing.T) {
	service, storage, privateKey, userID, publicDir := newReleaseServiceFixture(t)
	putIncomingRelease(t, service, privateKey, "job-123", "0.9.1", []Asset{
		{Target: "windows-x86_64", Kind: "full", File: "lynx-full.exe", Mirrors: []string{"https://github.com/100ask/lynx/releases/full.exe"}},
		{Target: "windows-x86_64", Kind: "delta", FromVersion: "0.9.0", File: "lynx-0.9.0-0.9.1.patch"},
	})

	staged, err := service.ImportIncoming(context.Background(), "job-123", userID)
	if err != nil || staged.Status != "staged" {
		t.Fatalf("unexpected staged release: %#v, %v", staged, err)
	}
	published, err := service.Publish(context.Background(), staged.ID)
	if err != nil || published.Status != "published" {
		t.Fatalf("unexpected published release: %#v, %v", published, err)
	}
	if _, err := os.Stat(filepath.Join(publicDir, "Tools", "lynx", "stable", "0.9.1", ManifestName)); err != nil {
		t.Fatal(err)
	}
	assetInfo, err := os.Stat(filepath.Join(publicDir, "Tools", "lynx", "stable", "0.9.1", "lynx-full.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if assetInfo.Mode().Perm() != 0o644 {
		t.Fatalf("published asset must be Nginx-readable: mode=%v", assetInfo.Mode().Perm())
	}
	head, err := storage.ChannelHead(context.Background(), "lynx", "stable")
	if err != nil || head.ID != staged.ID {
		t.Fatalf("unexpected head: %#v, %v", head, err)
	}

	update, err := service.SelectUpdate(context.Background(), "lynx", "stable", "windows-x86_64", "0.9.0")
	if err != nil || !update.Available || update.Strategy != "delta" || update.Fallback == nil {
		t.Fatalf("unexpected delta selection: %#v, %v", update, err)
	}
	if update.Asset.URL != "/Tools/lynx/stable/0.9.1/lynx-0.9.0-0.9.1.patch" {
		t.Fatalf("unexpected asset URL: %s", update.Asset.URL)
	}
	update, err = service.SelectUpdate(context.Background(), "lynx", "stable", "windows-x86_64", "0.8.9")
	if err != nil || update.Strategy != "full" || update.Fallback != nil {
		t.Fatalf("incompatible pre-1.0 version must get full installer: %#v, %v", update, err)
	}
	update, err = service.SelectUpdate(context.Background(), "lynx", "stable", "windows-x86_64", "0.9.1")
	if err != nil || update.Available {
		t.Fatalf("current version must not get an update: %#v, %v", update, err)
	}
	full, err := service.SelectFullUpdate(context.Background(), "lynx", "stable", "windows-x86_64", "0.9.0")
	if err != nil || full.Strategy != "full" || full.Asset == nil || full.Asset.Kind != "full" || full.Fallback != nil {
		t.Fatalf("Tauri-compatible selection must force the full fallback: %#v, %v", full, err)
	}
}

func TestSchema2PublishesOnlyDeltaAndSelectsGitHubFullFallback(t *testing.T) {
	service, _, privateKey, userID, publicDir := newReleaseServiceFixture(t)
	putIncomingV2Release(t, service, privateKey, "delta-only-site", "0.9.2", []Asset{
		{Target: "windows-x86_64", Kind: "full", File: "LYNX_0.9.2_x64-setup.exe", Storage: "external", Mirrors: []string{"https://github.com/dshanpi/lynx-releases/releases/download/v0.9.2/LYNX_0.9.2_x64-setup.exe"}},
		{Target: "windows-x86_64", Kind: "delta", FromVersion: "0.9.1", File: "LYNX_0.9.2_from_0.9.1_x64-delta.exe", Storage: "site"},
	})
	staged, err := service.ImportIncoming(context.Background(), "delta-only-site", userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), staged.ID); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(publicDir, "Tools", "lynx", "stable", "0.9.2")
	if _, err := os.Stat(filepath.Join(directory, "LYNX_0.9.2_x64-setup.exe")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external full installer must not exist on the download site: %v", err)
	}
	for _, name := range []string{ManifestName, ManifestSignature, "LYNX_0.9.2_from_0.9.1_x64-delta.exe"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("published site asset %s: %v", name, err)
		}
	}
	update, err := service.SelectUpdate(context.Background(), "lynx", "stable", "windows-x86_64", "0.9.1")
	if err != nil || update.Strategy != "delta" || update.Asset == nil || update.Fallback == nil {
		t.Fatalf("unexpected exact delta selection: %#v, %v", update, err)
	}
	if update.Asset.URL != "/Tools/lynx/stable/0.9.2/LYNX_0.9.2_from_0.9.1_x64-delta.exe" {
		t.Fatalf("delta must use the download site: %s", update.Asset.URL)
	}
	if update.Fallback.URL != "https://github.com/dshanpi/lynx-releases/releases/download/v0.9.2/LYNX_0.9.2_x64-setup.exe" {
		t.Fatalf("full fallback must use GitHub: %s", update.Fallback.URL)
	}
	full, err := service.SelectFullUpdate(context.Background(), "lynx", "stable", "windows-x86_64", "0.9.1")
	if err != nil || full.Asset == nil || full.Asset.URL != update.Fallback.URL {
		t.Fatalf("forced full must select GitHub: %#v, %v", full, err)
	}
}

func TestMigrateLegacyLayoutMovesFilesAndMetadata(t *testing.T) {
	service, storage, privateKey, userID, publicDir := newReleaseServiceFixture(t)
	putIncomingRelease(t, service, privateKey, "legacy-job", "0.9.0", []Asset{{
		Target: "windows-x86_64", Kind: "full", File: "lynx-full.exe",
	}})
	staged, err := service.ImportIncoming(context.Background(), "legacy-job", userID)
	if err != nil {
		t.Fatal(err)
	}
	published, err := service.Publish(context.Background(), staged.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonical := PublishedPath("lynx", "stable", "0.9.0")
	legacy := legacyPublishedPath("lynx", "stable", "0.9.0")
	canonicalDirectory := filepath.Join(publicDir, filepath.FromSlash(canonical))
	legacyDirectory := filepath.Join(publicDir, filepath.FromSlash(legacy))
	if err := os.MkdirAll(filepath.Dir(legacyDirectory), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(canonicalDirectory, legacyDirectory); err != nil {
		t.Fatal(err)
	}
	if err := storage.UpdatePublishedPath(context.Background(), published.ID, canonical, legacy); err != nil {
		t.Fatal(err)
	}

	migrations, err := service.MigrateLegacyLayout(context.Background(), true)
	if err != nil || len(migrations) != 1 {
		t.Fatalf("unexpected dry-run plan: %#v, %v", migrations, err)
	}
	if _, err := os.Stat(filepath.Join(legacyDirectory, ManifestName)); err != nil {
		t.Fatalf("dry run changed legacy files: %v", err)
	}

	migrations, err = service.MigrateLegacyLayout(context.Background(), false)
	if err != nil || len(migrations) != 1 || migrations[0].To != canonical {
		t.Fatalf("unexpected migration: %#v, %v", migrations, err)
	}
	if _, err := os.Stat(filepath.Join(canonicalDirectory, ManifestName)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(publicDir, LegacyReleaseDirectoryName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy root should be removed when empty: %v", err)
	}
	head, err := storage.ChannelHead(context.Background(), "lynx", "stable")
	if err != nil || head.PublishedPath != canonical {
		t.Fatalf("unexpected migrated head: %#v, %v", head, err)
	}
	migrations, err = service.MigrateLegacyLayout(context.Background(), false)
	if err != nil || len(migrations) != 0 {
		t.Fatalf("migration must be idempotent: %#v, %v", migrations, err)
	}
}

func TestRestorePublishedRecreatesMissingCanonicalRelease(t *testing.T) {
	service, storage, privateKey, userID, publicDir := newReleaseServiceFixture(t)
	putIncomingRelease(t, service, privateKey, "original-job", "0.9.0", []Asset{{
		Target: "windows-x86_64", Kind: "full", File: "lynx-full.exe",
	}})
	staged, err := service.ImportIncoming(context.Background(), "original-job", userID)
	if err != nil {
		t.Fatal(err)
	}
	published, err := service.Publish(context.Background(), staged.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonical := PublishedPath("lynx", "stable", "0.9.0")
	legacy := legacyPublishedPath("lynx", "stable", "0.9.0")
	canonicalDirectory := filepath.Join(publicDir, filepath.FromSlash(canonical))
	restoreDirectory := filepath.Join(service.IncomingDir(), "restore-job")
	if err := os.Rename(canonicalDirectory, restoreDirectory); err != nil {
		t.Fatal(err)
	}
	if err := storage.UpdatePublishedPath(context.Background(), published.ID, canonical, legacy); err != nil {
		t.Fatal(err)
	}

	restored, err := service.RestorePublished(context.Background(), "restore-job")
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID != published.ID || restored.PublishedPath != canonical {
		t.Fatalf("restore changed release identity or path: %#v", restored)
	}
	assetInfo, err := os.Stat(filepath.Join(canonicalDirectory, "lynx-full.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if assetInfo.Mode().Perm() != 0o644 {
		t.Fatalf("restored asset must be public-readable: mode=%v", assetInfo.Mode().Perm())
	}
	if _, err := os.Stat(restoreDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incoming directory must be consumed: %v", err)
	}
}

func TestRestorePublishedKeepsCanonicalMetadata(t *testing.T) {
	service, _, privateKey, userID, publicDir := newReleaseServiceFixture(t)
	putIncomingRelease(t, service, privateKey, "original-job", "0.9.0", []Asset{{
		Target: "linux-x86_64", Kind: "full", File: "lynx.AppImage",
	}})
	staged, err := service.ImportIncoming(context.Background(), "original-job", userID)
	if err != nil {
		t.Fatal(err)
	}
	published, err := service.Publish(context.Background(), staged.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonical := PublishedPath("lynx", "stable", "0.9.0")
	if err := os.Rename(filepath.Join(publicDir, filepath.FromSlash(canonical)), filepath.Join(service.IncomingDir(), "restore-job")); err != nil {
		t.Fatal(err)
	}
	restored, err := service.RestorePublished(context.Background(), "restore-job")
	if err != nil || restored.ID != published.ID || restored.PublishedPath != canonical {
		t.Fatalf("unexpected canonical restore: %#v, %v", restored, err)
	}
}

func TestRestorePublishedRejectsExistingPublicFiles(t *testing.T) {
	service, _, privateKey, userID, _ := newReleaseServiceFixture(t)
	assets := []Asset{{Target: "windows-x86_64", Kind: "full", File: "lynx-full.exe"}}
	putIncomingRelease(t, service, privateKey, "original-job", "0.9.0", assets)
	staged, err := service.ImportIncoming(context.Background(), "original-job", userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), staged.ID); err != nil {
		t.Fatal(err)
	}
	putIncomingRelease(t, service, privateKey, "restore-job", "0.9.0", assets)
	if _, err := service.RestorePublished(context.Background(), "restore-job"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("restore must not overwrite public files: %v", err)
	}
}

func TestRestorePublishedRejectsChangedSignedManifest(t *testing.T) {
	service, _, privateKey, userID, publicDir := newReleaseServiceFixture(t)
	putIncomingRelease(t, service, privateKey, "original-job", "0.9.0", []Asset{{
		Target: "windows-x86_64", Kind: "full", File: "lynx-full.exe",
	}})
	staged, err := service.ImportIncoming(context.Background(), "original-job", userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), staged.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(publicDir, filepath.FromSlash(PublishedPath("lynx", "stable", "0.9.0")))); err != nil {
		t.Fatal(err)
	}
	putIncomingRelease(t, service, privateKey, "restore-job", "0.9.0", []Asset{{
		Target: "windows-x86_64", Kind: "full", File: "different.exe",
	}})
	if _, err := service.RestorePublished(context.Background(), "restore-job"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("restore accepted a different signed manifest: %v", err)
	}
}

func TestPublishRevalidatesStagedBytes(t *testing.T) {
	service, _, privateKey, userID, _ := newReleaseServiceFixture(t)
	putIncomingRelease(t, service, privateKey, "tamper-job", "1.0.0", []Asset{{
		Target: "linux-x86-64", Kind: "full", File: "lynx.AppImage",
	}})
	staged, err := service.ImportIncoming(context.Background(), "tamper-job", userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(service.stateDir, staged.StagedPath, "lynx.AppImage"), []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), staged.ID); err == nil {
		t.Fatal("tampered staged release was published")
	}
}

func TestPublishRejectsSymlinkInCanonicalToolsPath(t *testing.T) {
	service, _, privateKey, userID, publicDir := newReleaseServiceFixture(t)
	putIncomingRelease(t, service, privateKey, "symlink-job", "1.0.0", []Asset{{
		Target: "linux-x86_64", Kind: "full", File: "lynx.AppImage",
	}})
	staged, err := service.ImportIncoming(context.Background(), "symlink-job", userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(publicDir, PublicToolsDirectory, "lynx")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(context.Background(), staged.ID); err == nil || !strings.Contains(err.Error(), "unsafe component") {
		t.Fatalf("publish followed a symlink in Tools path: %v", err)
	}
}
