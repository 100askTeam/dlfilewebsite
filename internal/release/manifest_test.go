package release

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aead.dev/minisign"
)

func signedReleaseFixture(t *testing.T, version, kind, from string) (string, minisign.PublicKey) {
	t.Helper()
	root := t.TempDir()
	publicKey, privateKey, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("signed updater bytes")
	name := "lynx-update.bin"
	if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
	signingReader := minisign.NewReader(bytes.NewReader(data))
	if _, err := io.Copy(io.Discard, signingReader); err != nil {
		t.Fatal(err)
	}
	signature := base64.StdEncoding.EncodeToString(signingReader.Sign(privateKey))
	digest := sha256.Sum256(data)
	manifest := Manifest{
		SchemaVersion: 1,
		Product:       "lynx",
		Channel:       "stable",
		Version:       version,
		PublishedAt:   time.Now().UTC().Format(time.RFC3339),
		Notes:         "test release",
		Assets: []Asset{{
			Target: "windows-x86_64", Kind: kind, FromVersion: from, File: name,
			Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), Signature: signature,
		}},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestName), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, publicKey
}

func TestLoadAndVerifySignedRelease(t *testing.T) {
	root, publicKey := signedReleaseFixture(t, "1.2.3", "full", "")
	manifest, err := LoadAndVerify(root, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Product != "lynx" || manifest.Version != "1.2.3" || len(manifest.Assets) != 1 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestTamperedAssetAndWrongBaseAreRejected(t *testing.T) {
	root, publicKey := signedReleaseFixture(t, "1.2.3", "full", "")
	if err := os.WriteFile(filepath.Join(root, "lynx-update.bin"), []byte("tampered updater bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAndVerify(root, publicKey); err == nil {
		t.Fatal("tampered release was accepted")
	}
	root, publicKey = signedReleaseFixture(t, "1.2.3", "delta", "1.2.3")
	if _, err := LoadAndVerify(root, publicKey); err == nil {
		t.Fatal("non-forward delta was accepted")
	}
}

func TestUndeclaredFilesAndDirectoriesAreRejected(t *testing.T) {
	root, publicKey := signedReleaseFixture(t, "1.2.3", "full", "")
	if err := os.WriteFile(filepath.Join(root, "unexpected.sh"), []byte("not declared"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAndVerify(root, publicKey); err == nil {
		t.Fatal("undeclared release file was accepted")
	}

	root, publicKey = signedReleaseFixture(t, "1.2.3", "full", "")
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAndVerify(root, publicKey); err == nil {
		t.Fatal("nested release directory was accepted")
	}
}

func TestDeltaCompatibilityUsesMajorBoundaryAndZeroMinorBoundary(t *testing.T) {
	parse := func(value string) Version {
		version, err := ParseVersion(value)
		if err != nil {
			t.Fatal(err)
		}
		return version
	}
	if !DeltaCompatible(parse("1.2.9"), parse("1.4.0")) {
		t.Fatal("same stable major should support a delta")
	}
	if DeltaCompatible(parse("1.9.0"), parse("2.0.0")) {
		t.Fatal("major upgrade must use a full installer")
	}
	if DeltaCompatible(parse("0.9.9"), parse("0.10.0")) {
		t.Fatal("pre-1.0 minor boundary must use a full installer")
	}
}

func TestSchema2AcceptsSignedExternalFullAndLocalDelta(t *testing.T) {
	root := t.TempDir()
	publicKey, privateKey, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(data []byte) string {
		reader := minisign.NewReader(bytes.NewReader(data))
		if _, err := io.Copy(io.Discard, reader); err != nil {
			t.Fatal(err)
		}
		return base64.StdEncoding.EncodeToString(reader.Sign(privateKey))
	}
	full := []byte("full installer stays on GitHub")
	delta := []byte("small exact delta")
	deltaName := "LYNX_0.9.2_from_0.9.1_x64-delta.exe"
	if err := os.WriteFile(filepath.Join(root, deltaName), delta, 0o600); err != nil {
		t.Fatal(err)
	}
	fullHash := sha256.Sum256(full)
	deltaHash := sha256.Sum256(delta)
	manifest := Manifest{
		SchemaVersion: 2, Product: "lynx", Channel: "stable", Version: "0.9.2",
		Assets: []Asset{
			{Target: "windows-x86_64", Kind: "full", File: "LYNX_0.9.2_x64-setup.exe", Storage: "external", Size: int64(len(full)), SHA256: hex.EncodeToString(fullHash[:]), Signature: sign(full), Mirrors: []string{"https://github.com/dshanpi/lynx-releases/releases/download/v0.9.2/LYNX_0.9.2_x64-setup.exe"}},
			{Target: "windows-x86_64", Kind: "delta", FromVersion: "0.9.1", File: deltaName, Storage: "site", Size: int64(len(delta)), SHA256: hex.EncodeToString(deltaHash[:]), Signature: sign(delta)},
		},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestName), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestSignature), []byte(sign(encoded)), 0o600); err != nil {
		t.Fatal(err)
	}
	verified, err := LoadAndVerify(root, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Assets[0].Storage != "external" && verified.Assets[1].Storage != "external" {
		t.Fatal("external full asset was not retained")
	}
	manifest.Notes = "tampered after signing"
	tampered, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, ManifestName), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAndVerify(root, publicKey); err == nil {
		t.Fatal("tampered schema 2 manifest was accepted")
	}
}
