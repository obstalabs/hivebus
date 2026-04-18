package runtime

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

type Role string

const (
	RoleOperator Role = "operator"
	RoleWorker   Role = "worker"
)

const signedTokenPrefix = "hbk1."

// BuiltInAPIVerifyKey is set at build time via -ldflags for release builds.
var BuiltInAPIVerifyKey string

type TokenEntry struct {
	ID      string `json:"id"`
	KeyHash string `json:"key_hash,omitempty"`
	Role    Role   `json:"role"`
}

type KeyStore struct {
	keys      map[string]*TokenEntry
	publicKey ed25519.PublicKey
}

type authContextKey string

const tokenEntryContextKey authContextKey = "hivebus_token_entry"

type SignedTokenClaims struct {
	Subject   string `json:"sub"`
	Role      Role   `json:"role"`
	ExpiresAt int64  `json:"exp"`
	NotBefore int64  `json:"nbf,omitempty"`
	IssuedAt  int64  `json:"iat,omitempty"`
	KeyID     string `json:"kid,omitempty"`
}

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

func NewSignedKeyStore(encodedPublicKey string) (*KeyStore, error) {
	publicKey, err := parseVerifyKey(encodedPublicKey)
	if err != nil {
		return nil, err
	}

	return &KeyStore{publicKey: publicKey}, nil
}

func HashToken(raw string) string {
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func (ks *KeyStore) Lookup(rawToken string) *TokenEntry {
	return ks.LookupAt(rawToken, time.Now().UTC())
}

func (ks *KeyStore) LookupAt(rawToken string, now time.Time) *TokenEntry {
	if ks == nil {
		return nil
	}

	if strings.HasPrefix(rawToken, signedTokenPrefix) {
		entry, err := ks.lookupSignedToken(rawToken, now)
		if err != nil {
			return nil
		}
		return entry
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

func (ks *KeyStore) lookupSignedToken(rawToken string, now time.Time) (*TokenEntry, error) {
	if ks == nil || len(ks.publicKey) == 0 {
		return nil, fmt.Errorf("signed api key verification is not configured")
	}

	rest := strings.TrimPrefix(rawToken, signedTokenPrefix)
	parts := strings.Split(rest, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("signed api key must contain payload and signature")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode signed api key payload: %w", err)
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode signed api key signature: %w", err)
	}

	if !ed25519.Verify(ks.publicKey, payloadBytes, signature) {
		return nil, fmt.Errorf("signed api key signature verification failed")
	}

	var claims SignedTokenClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("parse signed api key claims: %w", err)
	}
	if err := claims.Validate(now); err != nil {
		return nil, err
	}

	return &TokenEntry{
		ID:   claims.Subject,
		Role: claims.Role,
	}, nil
}

func (claims SignedTokenClaims) Validate(now time.Time) error {
	switch {
	case strings.TrimSpace(claims.Subject) == "":
		return fmt.Errorf("signed api key is missing subject")
	case !slices.Contains([]Role{RoleOperator, RoleWorker}, claims.Role):
		return fmt.Errorf("signed api key has unsupported role %q", claims.Role)
	case claims.ExpiresAt <= 0:
		return fmt.Errorf("signed api key is missing expiry")
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	nowUnix := now.Unix()
	if claims.NotBefore != 0 && nowUnix < claims.NotBefore {
		return fmt.Errorf("signed api key is not valid yet")
	}
	if nowUnix >= claims.ExpiresAt {
		return fmt.Errorf("signed api key is expired")
	}

	return nil
}

func parseVerifyKey(value string) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("api verify key is required")
	}

	for _, encoding := range []*base64.Encoding{
		base64.RawStdEncoding,
		base64.StdEncoding,
		base64.RawURLEncoding,
		base64.URLEncoding,
	} {
		publicKey, err := encoding.DecodeString(trimmed)
		if err != nil {
			continue
		}
		if len(publicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("api verify key must decode to %d bytes", ed25519.PublicKeySize)
		}
		return ed25519.PublicKey(publicKey), nil
	}

	return nil, fmt.Errorf("api verify key must be base64 encoded")
}
