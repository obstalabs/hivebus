package embed

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestShutdownClosesOwnedDependenciesAfterServeError pins WO-158: owned
// ServeLocal dependencies close even when the serve loop reports failure.
func TestShutdownClosesOwnedDependenciesAfterServeError(t *testing.T) {
	closed := false
	srv := &Server{
		httpServer: &http.Server{},
		done:       make(chan struct{}),
		err:        errors.New("serve failed"),
		closeFns: []func() error{
			func() error {
				closed = true
				return nil
			},
		},
	}
	close(srv.done)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := srv.Shutdown(ctx)
	if err == nil {
		t.Fatal("Shutdown returned nil; want serve error")
	}
	if !strings.Contains(err.Error(), "serve failed") {
		t.Fatalf("Shutdown error = %v, want serve failure", err)
	}
	if !closed {
		t.Fatal("Shutdown did not close owned dependencies after serve error")
	}
}
