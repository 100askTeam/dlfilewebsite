package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"aead.dev/minisign"

	releasepkg "dladmin-go/internal/release"
)

func TestTauriEndpointSelectsDeltaAndHonorsFullFallbackHeader(t *testing.T) {
	publicKey, privateKey, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyText, err := publicKey.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DL_RELEASE_PUBLIC_KEY", string(keyText))
	app := newTestApp(t)
	user, err := app.store.UserByUsername(context.Background(), "release-admin")
	if err != nil {
		t.Fatal(err)
	}

	incomingID := "tauri-route-fixture"
	directory := filepath.Join(app.releases.IncomingDir(), incomingID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	assets := []releasepkg.Asset{
		{Target: "windows-x86_64", Kind: "full", File: "LYNX_0.9.1_x64-setup.exe"},
		{Target: "windows-x86_64", Kind: "delta", FromVersion: "0.9.0", File: "LYNX_0.9.1_from_0.9.0_x64-delta.exe"},
	}
	for index := range assets {
		asset := &assets[index]
		data := []byte("signed test bytes for " + asset.File)
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
	manifest := releasepkg.Manifest{
		SchemaVersion: 1,
		Product:       "lynx",
		Channel:       "stable",
		Version:       "0.9.1",
		Notes:         "route selection test",
		Assets:        assets,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, releasepkg.ManifestName), manifestJSON, 0o640); err != nil {
		t.Fatal(err)
	}
	record, err := app.releases.ImportIncoming(context.Background(), incomingID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.releases.Publish(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}

	request := func(header, mode string) map[string]any {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/tauri/lynx/stable/windows-x86_64/0.9.0", nil)
		if mode != "" {
			r.Header.Set(header, mode)
		}
		w := httptest.NewRecorder()
		app.handleTauriUpdateAPI(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("mode=%q status=%d body=%s", mode, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Header().Get("Vary") != "X-Update-Mode, X-Lynx-Update-Mode" {
			t.Fatalf("missing update-mode Vary header")
		}
		return result
	}

	delta := request("", "")
	if delta["strategy"] != "delta" || delta["fallback_available"] != true {
		t.Fatalf("default route must select exact delta: %#v", delta)
	}
	full := request("X-Update-Mode", "full")
	if full["strategy"] != "full" || full["fallback_available"] != false {
		t.Fatalf("full header must select complete installer: %#v", full)
	}
	legacyFull := request("X-Lynx-Update-Mode", "full")
	if legacyFull["strategy"] != "full" {
		t.Fatalf("legacy LYNX header must remain compatible: %#v", legacyFull)
	}
}

func TestReleaseAssetURLPreservesVerifiedHTTPSFallback(t *testing.T) {
	if got := releaseAssetURL("https://dl.100ask.net", "/Tools/lynx/stable/1.0.0/lynx.exe"); got != "https://dl.100ask.net/Tools/lynx/stable/1.0.0/lynx.exe" {
		t.Fatalf("unexpected site URL: %s", got)
	}
	github := "https://github.com/dshanpi/lynx-releases/releases/download/v1.0.1/lynx.exe"
	if got := releaseAssetURL("https://dl.100ask.net", github); got != github {
		t.Fatalf("external fallback was prefixed by the download site: %s", got)
	}
}
