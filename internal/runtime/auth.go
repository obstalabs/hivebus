package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

type Role string

const (
	RoleOperator Role = "operator"
	RoleWorker   Role = "worker"
)

type TokenEntry struct {
	ID      string `json:"id"`
	KeyHash string `json:"key_hash"`
	Role    Role   `json:"role"`
}

type KeyStore struct {
	keys map[string]*TokenEntry
}

type authContextKey string

const tokenEntryContextKey authContextKey = "hivebus_token_entry"

func LoadKeyStore(path string) (*KeyStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read token file: %w", err)
	}

	return ParseKeyStore(data)
}

func ParseKeyStore(data []byte) (*KeyStore, error) {
	var entries []TokenEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse token store: %w", err)
	}

	keys := make(map[string]*TokenEntry, len(entries))
	for i := range entries {
		if strings.TrimSpace(entries[i].ID) == "" {
			return nil, fmt.Errorf("token entry %d is missing id", i)
		}
		if strings.TrimSpace(entries[i].KeyHash) == "" {
			return nil, fmt.Errorf("token entry %q is missing key_hash", entries[i].ID)
		}
		if entries[i].Role != RoleOperator && entries[i].Role != RoleWorker {
			return nil, fmt.Errorf("token entry %q has unsupported role %q", entries[i].ID, entries[i].Role)
		}

		keys[entries[i].KeyHash] = &entries[i]
	}

	return &KeyStore{keys: keys}, nil
}

func HashToken(raw string) string {
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func (ks *KeyStore) Lookup(rawToken string) *TokenEntry {
	if ks == nil {
		return nil
	}
	return ks.keys[HashToken(rawToken)]
}

func withAuth(ks *KeyStore, required Role, next http.HandlerFunc) http.HandlerFunc {
	if ks == nil {
		return next
	}

	return func(w http.ResponseWriter, r *http.Request) {
		entry, ok := authenticateRequest(ks, w, r)
		if !ok {
			return
		}
		if !entry.allows(required) {
			writeError(w, http.StatusForbidden, fmt.Errorf("role %q cannot access this endpoint", entry.Role))
			return
		}

		ctx := context.WithValue(r.Context(), tokenEntryContextKey, entry)
		next(w, r.WithContext(ctx))
	}
}

func authenticateRequest(ks *KeyStore, w http.ResponseWriter, r *http.Request) (*TokenEntry, bool) {
	if ks == nil {
		return nil, true
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid Authorization header"))
		return nil, false
	}

	entry := ks.Lookup(strings.TrimPrefix(auth, "Bearer "))
	if entry == nil {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("invalid bearer token"))
		return nil, false
	}

	return entry, true
}

func (entry *TokenEntry) allows(required Role) bool {
	switch required {
	case RoleWorker:
		return entry.Role == RoleWorker || entry.Role == RoleOperator
	case RoleOperator:
		return entry.Role == RoleOperator
	default:
		return false
	}
}
