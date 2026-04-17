package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ppiankov/hivebus/internal/model"
)

type Store struct {
	root string
}

type Manifest struct {
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentType string `json:"content_type"`
}

type PutInput struct {
	ContentType string
	Body        []byte
}

func Open(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("artifact root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact root: %w", err)
	}

	return &Store{root: root}, nil
}

func (s *Store) Put(ctx context.Context, input PutInput) (Manifest, error) {
	if s == nil {
		return Manifest{}, errors.New("artifact store is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	digest := sha256.Sum256(input.Body)
	sha := hex.EncodeToString(digest[:])
	manifest := Manifest{
		SHA256:      sha,
		SizeBytes:   int64(len(input.Body)),
		ContentType: strings.TrimSpace(input.ContentType),
	}
	if manifest.ContentType == "" {
		manifest.ContentType = model.DefaultContentType(input.Body)
	}

	dir := s.digestDir(sha)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("create digest dir: %w", err)
	}

	if err := writeIfMissing(ctx, s.dataPath(sha), input.Body, 0o644); err != nil {
		return Manifest{}, err
	}

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return Manifest{}, fmt.Errorf("marshal manifest: %w", err)
	}
	if err := writeIfMissing(ctx, s.manifestPath(sha), manifestBytes, 0o644); err != nil {
		return Manifest{}, err
	}

	return manifest, nil
}

func (s *Store) Get(ctx context.Context, digest string) (Manifest, []byte, error) {
	if s == nil {
		return Manifest{}, nil, errors.New("artifact store is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, nil, err
	}
	if !model.IsSHA256Hex(digest) {
		return Manifest{}, nil, errors.New("artifact digest must be a 64-character lowercase hex digest")
	}

	manifestBytes, err := os.ReadFile(s.manifestPath(digest))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, nil, os.ErrNotExist
		}
		return Manifest{}, nil, fmt.Errorf("read manifest: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Manifest{}, nil, fmt.Errorf("parse manifest: %w", err)
	}

	body, err := os.ReadFile(s.dataPath(digest))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, nil, os.ErrNotExist
		}
		return Manifest{}, nil, fmt.Errorf("read artifact body: %w", err)
	}

	return manifest, body, nil
}

func URIForDigest(digest string) string {
	return "artifact://sha256/" + digest
}

func (s *Store) digestDir(digest string) string {
	return filepath.Join(s.root, digest[:2], digest)
}

func (s *Store) dataPath(digest string) string {
	return filepath.Join(s.digestDir(digest), "data")
}

func (s *Store) manifestPath(digest string) string {
	return filepath.Join(s.digestDir(digest), "manifest.json")
}

func writeIfMissing(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat artifact path: %w", err)
	}

	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, mode); err != nil {
		return fmt.Errorf("write temp artifact file: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			_ = os.Remove(temp)
			return nil
		}
		if removeErr := os.Remove(temp); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("rename artifact file: %w (cleanup: %v)", err, removeErr)
		}
		return fmt.Errorf("rename artifact file: %w", err)
	}

	return nil
}
