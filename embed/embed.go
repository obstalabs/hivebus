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
// exported surface minimal — LocalConfig, Config, ServeLocal, Serve, and Server
// — so the public API commitment stays small.
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
	"strings"
	"sync"
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

// LocalConfig is the public, external-module embedding path. It keeps internal
// store/runtime concrete types behind this package boundary.
type LocalConfig struct {
	// StorePath is the SQLite database file used by the embedded runtime. Required.
	StorePath string // WO-154: public embed config must not require internal/store imports.
	// ArtifactRoot is the artifact directory used by the embedded runtime. Required.
	ArtifactRoot string // WO-154: public embed config must not require internal/artifact imports.
}

// Server wraps the running *http.Server so the caller can shut it down without
// reaching into net/http details. Per net/http, Shutdown closes the active
// listener; the caller is responsible for removing any socket file after
// Shutdown returns.
type Server struct {
	httpServer *http.Server
	done       chan struct{} // WO-156: terminal signal is observed, never drained.

	mu       sync.Mutex
	err      error // WO-156: Err reports this stored result without consuming done.
	closeFns []func() error

	closeOnce sync.Once
	closeErr  error
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
	if ctx == nil {
		ctx = context.Background()
	}
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

	srv := &Server{httpServer: httpServer, done: make(chan struct{})}
	go func() {
		srv.finish(httpServer.Serve(listener))
	}()
	return srv, nil
}

// ServeLocal opens the runtime dependencies from public paths and serves Hivebus
// over the caller-provided listener. Shutdown closes dependencies opened here.
func ServeLocal(ctx context.Context, listener net.Listener, cfg LocalConfig) (*Server, error) {
	if strings.TrimSpace(cfg.StorePath) == "" {
		return nil, errors.New("embed: LocalConfig.StorePath is required")
	}
	if strings.TrimSpace(cfg.ArtifactRoot) == "" {
		return nil, errors.New("embed: LocalConfig.ArtifactRoot is required")
	}

	st, err := store.Open(cfg.StorePath)
	if err != nil {
		return nil, fmt.Errorf("embed: open store: %w", err)
	}
	artifacts, err := artifact.Open(cfg.ArtifactRoot)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("embed: open artifacts: %w", err)
	}

	srv, err := Serve(ctx, listener, Config{Store: st, Artifacts: artifacts})
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	srv.closeFns = append(srv.closeFns, st.Close)
	return srv, nil
}

// Shutdown gracefully stops serving and waits for in-flight requests to drain or
// the context to expire. It closes the active listener through net/http and
// returns nil on a clean stop (http.ErrServerClosed is treated as success).
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.httpServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("embed: shutdown: %w", err)
	}
	select {
	case <-s.done:
	case <-ctx.Done():
		return fmt.Errorf("embed: shutdown wait: %w", ctx.Err())
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("embed: serve: %w", err)
	}
	if err := s.closeOwned(); err != nil {
		return err
	}
	return nil
}

// Err returns the serve goroutine's terminal error without waiting, or nil if it
// is still running. http.ErrServerClosed is normalized to nil.
func (s *Server) Err() error {
	if s == nil || s.done == nil {
		return nil
	}
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return normalizeServeError(s.err)
	default:
		return nil
	}
}

func (s *Server) finish(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
	close(s.done)
}

func (s *Server) closeOwned() error {
	s.closeOnce.Do(func() {
		for _, closeFn := range s.closeFns {
			if closeFn == nil {
				continue
			}
			if err := closeFn(); err != nil && s.closeErr == nil {
				s.closeErr = fmt.Errorf("embed: close dependency: %w", err)
			}
		}
	})
	return s.closeErr
}

func normalizeServeError(err error) error {
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
