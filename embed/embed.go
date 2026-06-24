// Package embed exposes a public, listener-injection entrypoint for serving the
// Hivebus v0 agent runtime in-process, without owning process lifecycle.
//
// WO-150: a caller (for example a host process that wants Hivebus over local IPC)
// provides its own net.Listener — a Unix socket, an abstract socket, or a
// loopback TCP listener — and Serve runs the existing /v0 routes over it. The
// caller owns the listener and the returned *http.Server's shutdown; Hivebus does
// not start a TCP daemon, install signal handlers, or block.
//
// This package is the public seam on purpose: Go's internal/ rule makes
// internal/runtime unimportable across modules, so an external embedder (a
// separate module) must consume this package, not internal/runtime. Keep the
// exported surface minimal — Config, Serve, and Server — so the public API
// commitment stays small.
//
// Boundary: this package imports only the open-core runtime. It pulls in no
// private governance, no live-session concepts, and nothing vendor-specific.
package embed

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/obstalabs/hivebus/internal/artifact"
	"github.com/obstalabs/hivebus/internal/runtime"
	"github.com/obstalabs/hivebus/internal/store"
)

// readHeaderTimeout matches the daemon path (internal/cli/serve.go) so embedded
// and standalone serving share the same slow-loris protection.
const readHeaderTimeout = 5 * time.Second

// Config is the dependency set Serve needs. The caller opens and owns the store
// and artifact directories; Serve neither creates nor closes them, so a host
// process can keep them across a serve/restart cycle.
type Config struct {
	// Store is the opened append-only event/agent store. Required.
	Store *store.Store
	// Artifacts is the opened artifact store. Required.
	Artifacts *artifact.Store
	// Keys authenticates requests. Nil means auth is disabled (local-only /
	// caller-trusted transport, e.g. a 0700 Unix socket).
	Keys *runtime.KeyStore
	// Options forwards optional bridges (work-order, execution-sync hooks).
	Options runtime.HandlerOptions
}

// Server wraps the running *http.Server so the caller can shut it down without
// reaching into net/http details. The caller still owns the net.Listener and is
// responsible for removing any socket file after Shutdown returns.
type Server struct {
	httpServer *http.Server
	errCh      chan error
}

// Serve builds the Hivebus v0 handler and serves it over the caller-provided
// listener in a background goroutine. It returns immediately with a *Server the
// caller drives; it never blocks and never installs signal handlers.
//
// Lifecycle contract (WO-150):
//   - Shutdown drains in-flight requests and stops serving. Per net/http,
//     http.Server.Shutdown closes the active listener for you, so after Shutdown
//     returns the socket is released and a fresh Serve can rebind the same path —
//     no orphaned listener. The caller still owns the socket *file*: for a Unix
//     listener it must unlink the path (os.Remove) before rebinding, since the
//     filesystem entry outlives the closed listener. The caller must not Close
//     the listener again after Shutdown (it is already closed).
//   - Serve does not arbitrate leadership. Calling Serve twice against the same
//     store on the same machine is the caller's responsibility to prevent; the
//     single bound socket (a second bind fails) and single-writer SQLite are the
//     structural guards against split-brain, not logic in this package.
func Serve(ctx context.Context, listener net.Listener, cfg Config) (*Server, error) {
	if listener == nil {
		return nil, errors.New("embed: listener is required")
	}
	if cfg.Store == nil {
		return nil, errors.New("embed: Config.Store is required")
	}
	if cfg.Artifacts == nil {
		return nil, errors.New("embed: Config.Artifacts is required")
	}

	handler := runtime.NewHandlerWithOptions(cfg.Store, cfg.Artifacts, cfg.Keys, cfg.Options)

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	srv := &Server{httpServer: httpServer, errCh: make(chan error, 1)}
	go func() {
		srv.errCh <- httpServer.Serve(listener)
	}()
	return srv, nil
}

// Shutdown gracefully stops serving and waits for in-flight requests to drain or
// the context to expire. It does NOT close the listener — the caller owns that.
// Returns nil on a clean stop (http.ErrServerClosed is treated as success).
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("embed: shutdown: %w", err)
	}
	if err := <-s.errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("embed: serve: %w", err)
	}
	return nil
}

// Err returns the serve goroutine's terminal error without waiting, or nil if it
// is still running. http.ErrServerClosed is normalized to nil.
func (s *Server) Err() error {
	if s == nil {
		return nil
	}
	select {
	case err := <-s.errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	default:
		return nil
	}
}
