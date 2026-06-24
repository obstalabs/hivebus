package embed_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/embed"
	"github.com/obstalabs/hivebus/internal/artifact"
	"github.com/obstalabs/hivebus/internal/store"
)

// newConfig opens fresh store + artifact dirs under root. The host (test) owns
// them across restart cycles — Serve never closes them.
func newConfig(t *testing.T, root string) embed.Config {
	t.Helper()
	st, err := store.Open(filepath.Join(root, "hivebus.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	arts, err := artifact.Open(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatalf("artifact.Open: %v", err)
	}
	// Keys nil => auth disabled; the unix socket is the trust boundary here.
	return embed.Config{Store: st, Artifacts: arts}
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
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get("http://unix" + route)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}

// TestServeOverInjectedUnixListener is the static half: the runtime serves the
// existing routes over a caller-provided Unix listener with no TCP daemon.
func TestServeOverInjectedUnixListener(t *testing.T) {
	root := t.TempDir()
	sock := filepath.Join(root, "hb.sock")
	ln := unixListener(t, sock)
	cfg := newConfig(t, root)

	srv, err := embed.Serve(context.Background(), ln, cfg)
	if err != nil {
		t.Fatalf("Serve: %v", err)
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
// After Shutdown + listener.Close + unlink, a fresh Serve must rebind cleanly;
// if Shutdown leaked the socket, the second bind would fail with EADDRINUSE.
func TestRestartLifecycle(t *testing.T) {
	root := t.TempDir()
	sock := filepath.Join(root, "hb.sock")
	cfg := newConfig(t, root)

	for cycle := range 3 {
		ln := unixListener(t, sock)
		srv, err := embed.Serve(context.Background(), ln, cfg)
		if err != nil {
			t.Fatalf("cycle %d Serve: %v", cycle, err)
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

// TestNoSplitBrainSecondBindFails proves the single-leader guard: while one
// embedded server holds the socket, a second bind on the same path FAILS rather
// than silently producing two leaders / two rosters. The embed package does not
// arbitrate leadership; the bound socket is the structural guard, and this test
// pins that the guard actually holds.
func TestNoSplitBrainSecondBindFails(t *testing.T) {
	root := t.TempDir()
	sock := filepath.Join(root, "hb.sock")
	cfg := newConfig(t, root)

	ln := unixListener(t, sock)
	srv, err := embed.Serve(context.Background(), ln, cfg)
	if err != nil {
		t.Fatalf("Serve: %v", err)
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
	root := t.TempDir()
	ln := unixListener(t, filepath.Join(root, "hb.sock"))
	t.Cleanup(func() { _ = ln.Close() })

	cases := map[string]embed.Config{
		"no store":     {Artifacts: mustArtifacts(t, root)},
		"no artifacts": {Store: mustStore(t, root)},
	}
	for name, cfg := range cases {
		if _, err := embed.Serve(context.Background(), ln, cfg); err == nil {
			t.Fatalf("%s: Serve accepted an invalid config", name)
		}
	}
	if _, err := embed.Serve(context.Background(), nil, newConfig(t, root)); err == nil {
		t.Fatalf("nil listener: Serve accepted a nil listener")
	}
}

func mustStore(t *testing.T, root string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(root, fmt.Sprintf("s-%d.db", time.Now().UnixNano())))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mustArtifacts(t *testing.T, root string) *artifact.Store {
	t.Helper()
	arts, err := artifact.Open(filepath.Join(root, fmt.Sprintf("a-%d", time.Now().UnixNano())))
	if err != nil {
		t.Fatalf("artifact.Open: %v", err)
	}
	return arts
}
