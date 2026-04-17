package artifact

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStorePutDeduplicatesByDigest(t *testing.T) {
	t.Helper()

	store, err := Open(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	first, err := store.Put(context.Background(), PutInput{
		ContentType: "text/plain",
		Body:        []byte("smoke log"),
	})
	if err != nil {
		t.Fatalf("Put(first) error = %v", err)
	}
	second, err := store.Put(context.Background(), PutInput{
		ContentType: "text/plain",
		Body:        []byte("smoke log"),
	})
	if err != nil {
		t.Fatalf("Put(second) error = %v", err)
	}

	if first.SHA256 != second.SHA256 {
		t.Fatalf("expected same digest, got %q and %q", first.SHA256, second.SHA256)
	}
}

func TestStoreGetReturnsManifestAndBytes(t *testing.T) {
	t.Helper()

	store, err := Open(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	manifest, err := store.Put(context.Background(), PutInput{
		ContentType: "text/plain",
		Body:        []byte("smoke log"),
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	gotManifest, body, err := store.Get(context.Background(), manifest.SHA256)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if gotManifest.ContentType != "text/plain" {
		t.Fatalf("expected content type text/plain, got %q", gotManifest.ContentType)
	}
	if string(body) != "smoke log" {
		t.Fatalf("expected body smoke log, got %q", string(body))
	}
}
