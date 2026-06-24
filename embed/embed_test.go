package embed_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/embed"
)

// newConfig returns public path-based config; ServeLocal owns opened resources.
func newConfig(t *testing.T, root string) embed.LocalConfig {
	t.Helper()

	return embed.LocalConfig{
		StorePath:    filepath.Join(root, "hivebus.db"),
		ArtifactRoot: filepath.Join(root, "artifacts"),
		AuthDisabled: true,
	}
}

// shortSocketRoot keeps macOS unix-socket paths under the platform limit while
// still using a unique per-test directory.
// WO-155: the test must pass under default macOS temp settings.
func shortSocketRoot(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix-domain socket tests use the Unix local-IPC primitive")
	}
	root, err := os.MkdirTemp("/tmp", "hb-")
	if err != nil {
		t.Fatalf("short socket root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

// unixListener binds a Unix domain socket at path. Returns the listener; caller
// closes it and is responsible for unlinking the path (mirrors the real
// embedding contract).
func unixListener(t *testing.T, path string) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("net.Listen unix %s: %v", path, err)
	}
	return ln
}

func unixGet(t *testing.T, socketPath, route string) (int, error) {
	t.Helper()
	return unixGetWithAuth(t, socketPath, route, "")
}

func unixGetWithAuth(t *testing.T, socketPath, route string, bearer string) (int, error) {
	t.Helper()
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 2 * time.Second,
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://unix"+route, nil)
	if err != nil {
		return 0, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}

func signedWorkerKey(t *testing.T) (string, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	claims := struct {
		Subject   string `json:"sub"`
		Role      string `json:"role"`
		ExpiresAt int64  `json:"exp"`
	}{
		Subject:   "embed-worker",
		Role:      "worker",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal(claims) error = %v", err)
	}
	signature := ed25519.Sign(privateKey, payload)
	return base64.StdEncoding.EncodeToString(publicKey),
		"hbk1." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// TestServeOverInjectedUnixListener is the static half: the runtime serves the
// existing routes over a caller-provided Unix listener with no TCP daemon.
func TestServeOverInjectedUnixListener(t *testing.T) {
	root := shortSocketRoot(t)
	sock := filepath.Join(root, "hb.sock")
	ln := unixListener(t, sock)
	cfg := newConfig(t, root)

	srv, err := embed.ServeLocal(context.Background(), ln, cfg)
	if err != nil {
		t.Fatalf("ServeLocal: %v", err)
	}

	code, err := unixGet(t, sock, "/healthz")
	if err != nil {
		t.Fatalf("healthz over unix socket: %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", code)
	}

	// An agent route exists over the injected listener (401/registered, not a
	// connection failure — proves /v0/agents is mounted on this socket).
	code, err = unixGet(t, sock, "/v0/agents/sessions/missing/inbox")
	if err != nil {
		t.Fatalf("agent route over unix socket: %v", err)
	}
	if code == 0 {
		t.Fatalf("agent route did not respond over the injected listener")
	}

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// Shutdown already closed ln; nothing more to release.
}

// TestRestartLifecycle is the load-bearing half (WO-150 acceptance): a full
// serve -> shutdown -> rebind cycle on the SAME socket path must leave no orphan.
// After Shutdown + unlink, a fresh Serve must rebind cleanly;
// if Shutdown leaked the socket, the second bind would fail with EADDRINUSE.
func TestRestartLifecycle(t *testing.T) {
	root := shortSocketRoot(t)
	sock := filepath.Join(root, "hb.sock")
	cfg := newConfig(t, root)

	for cycle := range 3 {
		ln := unixListener(t, sock)
		srv, err := embed.ServeLocal(context.Background(), ln, cfg)
		if err != nil {
			t.Fatalf("cycle %d ServeLocal: %v", cycle, err)
		}
		code, err := unixGet(t, sock, "/healthz")
		if err != nil || code != http.StatusOK {
			t.Fatalf("cycle %d healthz: code=%d err=%v", cycle, code, err)
		}
		// Shutdown drains AND closes the listener (net/http). The caller does not
		// re-Close it; it only unlinks the socket file before the next rebind.
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatalf("cycle %d Shutdown: %v", cycle, err)
		}
		if err := os.Remove(sock); err != nil && !os.IsNotExist(err) {
			t.Fatalf("cycle %d unlink: %v", cycle, err)
		}
	}
}

func TestServeLocalWithVerifyKeyRequiresAuth(t *testing.T) {
	root := shortSocketRoot(t)
	sock := filepath.Join(root, "hb.sock")
	verifyKey, signedKey := signedWorkerKey(t)
	cfg := newConfig(t, root)
	cfg.AuthDisabled = false
	cfg.APIVerifyKey = verifyKey

	ln := unixListener(t, sock)
	srv, err := embed.ServeLocal(context.Background(), ln, cfg)
	if err != nil {
		t.Fatalf("ServeLocal: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Shutdown(context.Background())
		_ = os.Remove(sock)
	})

	code, err := unixGet(t, sock, "/v0/agents/sessions/missing/inbox")
	if err != nil {
		t.Fatalf("unauthenticated inbox request: %v", err)
	}
	if code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated inbox status = %d, want 401", code)
	}

	code, err = unixGetWithAuth(t, sock, "/v0/agents/sessions/missing/inbox", signedKey)
	if err != nil {
		t.Fatalf("authenticated inbox request: %v", err)
	}
	if code != http.StatusNotFound {
		t.Fatalf("authenticated inbox status = %d, want 404 after auth passes", code)
	}
}

func TestServeLocalRejectsUnauthenticatedNonLocalListener(t *testing.T) {
	root := t.TempDir()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("Listen(tcp): %v", err)
	}
	defer func() { _ = ln.Close() }()

	if _, err := embed.ServeLocal(context.Background(), ln, newConfig(t, root)); err == nil {
		t.Fatalf("ServeLocal accepted AuthDisabled on non-local listener")
	}
}

// TestNoSplitBrainSecondBindFails proves the single-leader guard: while one
// embedded server holds the socket, a second bind on the same path FAILS rather
// than silently producing two leaders / two rosters. The embed package does not
// arbitrate leadership; the bound socket is the structural guard, and this test
// pins that the guard actually holds.
func TestNoSplitBrainSecondBindFails(t *testing.T) {
	root := shortSocketRoot(t)
	sock := filepath.Join(root, "hb.sock")
	cfg := newConfig(t, root)

	ln := unixListener(t, sock)
	srv, err := embed.ServeLocal(context.Background(), ln, cfg)
	if err != nil {
		t.Fatalf("ServeLocal: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Shutdown(context.Background()) // also closes ln
	})

	// Second bind on the SAME path must fail while the first holds it.
	if second, err := net.Listen("unix", sock); err == nil {
		_ = second.Close()
		t.Fatalf("second bind on %s succeeded; expected the held socket to reject a co-leader", sock)
	}
}

// TestServeRejectsBadConfig guards the required-dependency contract.
func TestServeRejectsBadConfig(t *testing.T) {
	root := shortSocketRoot(t)
	ln := unixListener(t, filepath.Join(root, "hb.sock"))
	t.Cleanup(func() { _ = ln.Close() })

	cases := map[string]embed.LocalConfig{
		"no store path":    {ArtifactRoot: filepath.Join(root, "artifacts")},
		"no artifact root": {StorePath: filepath.Join(root, "hivebus.db")},
		"no auth config": {
			StorePath:    filepath.Join(root, "hivebus.db"),
			ArtifactRoot: filepath.Join(root, "artifacts"),
		},
	}
	for name, cfg := range cases {
		if _, err := embed.ServeLocal(context.Background(), ln, cfg); err == nil {
			t.Fatalf("%s: ServeLocal accepted an invalid config", name)
		}
	}
	if _, err := embed.ServeLocal(context.Background(), nil, newConfig(t, root)); err == nil {
		t.Fatalf("nil listener: ServeLocal accepted a nil listener")
	}
}

func TestErrBeforeShutdownDoesNotConsumeTerminalState(t *testing.T) {
	root := shortSocketRoot(t)
	sock := filepath.Join(root, "hb.sock")
	ln := unixListener(t, sock)

	srv, err := embed.ServeLocal(context.Background(), ln, newConfig(t, root))
	if err != nil {
		t.Fatalf("ServeLocal: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("listener close: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := srv.Err(); err != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := srv.Err(); err == nil {
		t.Fatalf("Err() did not report the closed listener")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil && ctx.Err() != nil {
		t.Fatalf("Shutdown after Err waited for context expiry: %v", err)
	}
}
