package release

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aead.dev/minisign"
)

func publicKeyText(t *testing.T) string {
	t.Helper()
	publicKey, _, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	text, err := publicKey.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func rewriteFixtureProduct(t *testing.T, root, product string) {
	t.Helper()
	path := filepath.Join(root, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Product = product
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProductKeyringUsesOnlyTheNamedProductKey(t *testing.T) {
	root, lynxKey := signedReleaseFixture(t, "1.2.3", "full", "")
	lynxText, err := lynxKey.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	usbText := publicKeyText(t)
	keyring, err := NewPublicKeyring(map[string]string{
		"lynx":       string(lynxText),
		"usbtoolbox": usbText,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAndVerifyWithKeyring(root, keyring); err != nil {
		t.Fatal(err)
	}

	rewriteFixtureProduct(t, root, "usbtoolbox")
	if _, err := LoadAndVerifyWithKeyring(root, keyring); err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Fatalf("LYNX-signed USBToolBox release was not rejected: %v", err)
	}

	lynxOnly, err := NewPublicKeyring(map[string]string{"lynx": string(lynxText)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAndVerifyWithKeyring(root, lynxOnly); err == nil || !strings.Contains(err.Error(), "no configured public key") {
		t.Fatalf("unknown product was not rejected: %v", err)
	}
}

func TestLoadPublicKeyringDirectoryRejectsUnsafeEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "lynx.pub"), []byte(publicKeyText(t)), 0o640); err != nil {
		t.Fatal(err)
	}
	keyring, err := LoadPublicKeyringDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.KeyFor("lynx"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "README.txt"), []byte("not a key"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublicKeyringDirectory(root); err == nil {
		t.Fatal("unexpected keyring entry was accepted")
	}
	if err := os.Remove(filepath.Join(root, "README.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "lynx.pub"), filepath.Join(root, "usbtoolbox.pub")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublicKeyringDirectory(root); err == nil {
		t.Fatal("symlinked public key was accepted")
	}
}

func TestLegacyConfiguredKeyIsScopedToLynx(t *testing.T) {
	t.Setenv("DL_RELEASE_PUBLIC_KEYS_DIR", "")
	t.Setenv("DL_RELEASE_PUBLIC_KEY_FILE", "")
	t.Setenv("DL_RELEASE_PUBLIC_KEY", publicKeyText(t))
	keyring, err := LoadConfiguredPublicKeyring()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.KeyFor("lynx"); err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.KeyFor("usbtoolbox"); err == nil {
		t.Fatal("legacy LYNX key became a wildcard")
	}
}
