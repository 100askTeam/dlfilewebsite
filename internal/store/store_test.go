package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "state", "dladmin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestUserAndHashedSessionRoundTrip(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	userID, err := store.CreateUser(ctx, "release-admin", "$2a$hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.UserByUsername(ctx, "release-admin")
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != userID || user.PasswordHash != "$2a$hash" || user.Role != "admin" {
		t.Fatalf("unexpected user: %#v", user)
	}
	tokenHash := bytes.Repeat([]byte{1}, 32)
	csrfHash := bytes.Repeat([]byte{2}, 32)
	if err := store.CreateSession(ctx, tokenHash, csrfHash, userID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	session, err := store.Session(ctx, tokenHash, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if session.Username != user.Username || !bytes.Equal(session.CSRFHash, csrfHash) {
		t.Fatalf("unexpected session: %#v", session)
	}
	if err := store.DeleteSession(ctx, tokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session(ctx, tokenHash, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestExpiredSessionAndAuditAreBounded(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	userID, err := store.CreateUser(ctx, "auditor", "hash", "auditor")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, []byte("old"), []byte("csrf"), userID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session(ctx, []byte("old"), time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session should be absent: %v", err)
	}
	if err := store.PurgeExpiredSessions(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAudit(ctx, &userID, "auditor", "list", "audit", "", "127.0.0.1", "test", "success", "{}"); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Actor != "auditor" || events[0].Details != "{}" {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestReleaseLifecycleAndChannelHead(t *testing.T) {
	storage := openTestStore(t)
	ctx := context.Background()
	userID, err := storage.CreateUser(ctx, "publisher", "hash", "release_manager")
	if err != nil {
		t.Fatal(err)
	}

	stage := func(version string) int64 {
		t.Helper()
		id, err := storage.StageRelease(ctx, ReleaseRecord{
			Product:      "lynx",
			Channel:      "stable",
			Version:      version,
			Notes:        "release " + version,
			ManifestJSON: `{"schema_version":1}`,
			StagedPath:   "staged/lynx/stable/" + version,
			CreatedBy:    userID,
		}, []ReleaseAssetInput{{
			Target: "windows-x86_64", Kind: "full", FileName: "lynx.exe",
			Size: 42, SHA256: "abcd", Signature: "signature", MirrorsJSON: `[]`,
		}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	firstID := stage("0.9.0")
	first, err := storage.Release(ctx, firstID)
	if err != nil || first.Status != "staged" || first.PublishedAt != "" {
		t.Fatalf("unexpected staged release: %#v, %v", first, err)
	}
	assets, err := storage.ReleaseAssets(ctx, firstID)
	if err != nil || len(assets) != 1 || assets[0].FileName != "lynx.exe" || assets[0].Storage != "site" {
		t.Fatalf("unexpected assets: %#v, %v", assets, err)
	}
	if err := storage.PublishRelease(ctx, firstID, "releases/lynx/stable/0.9.0"); err != nil {
		t.Fatal(err)
	}
	head, err := storage.ChannelHead(ctx, "lynx", "stable")
	if err != nil || head.ID != firstID || head.Status != "published" {
		t.Fatalf("unexpected channel head: %#v, %v", head, err)
	}
	if err := storage.PublishRelease(ctx, firstID, "again"); err == nil {
		t.Fatal("publishing the same release twice must fail")
	}

	secondID := stage("0.9.1")
	if err := storage.PublishRelease(ctx, secondID, "releases/lynx/stable/0.9.1"); err != nil {
		t.Fatal(err)
	}
	first, err = storage.Release(ctx, firstID)
	if err != nil || first.Status != "superseded" {
		t.Fatalf("previous release was not superseded: %#v, %v", first, err)
	}
	head, err = storage.ChannelHead(ctx, "lynx", "stable")
	if err != nil || head.ID != secondID {
		t.Fatalf("unexpected updated head: %#v, %v", head, err)
	}
	releases, err := storage.ListReleases(ctx, 20)
	if err != nil || len(releases) != 2 || releases[0].ID != secondID {
		t.Fatalf("unexpected release list: %#v, %v", releases, err)
	}
}
