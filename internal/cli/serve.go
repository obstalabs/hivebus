package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/ppiankov/hivebus/internal/runtime"
	"github.com/ppiankov/hivebus/internal/store"
	"github.com/spf13/cobra"
)

const shutdownTimeout = 10 * time.Second

func newServeCommand() *cobra.Command {
	var listenAddr string
	var dbPath string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the v0 Hivebus HTTP runtime",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dbPath == "" {
				dbPath = filepath.Join(".", ".hivebus", "events.db")
			}

			return runServe(cmd.Context(), cmd.OutOrStdout(), listenAddr, dbPath)
		},
	}

	cmd.Flags().StringVar(&listenAddr, "listen", "127.0.0.1:7081", "Listen address for the HTTP runtime")
	cmd.Flags().StringVar(&dbPath, "db", "", "Path to the SQLite event log")

	return cmd
}

func runServe(ctx context.Context, out io.Writer, listenAddr string, dbPath string) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = st.Close()
	}()

	handler := runtime.NewHandler(st)

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
	}()

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- server.Serve(listener)
	}()

	if _, err := io.WriteString(out, "hivebus listening on "+listener.Addr().String()+"\n"); err != nil {
		return err
	}

	signalCtx, stop := signalNotifyContext(ctx)
	defer stop()

	select {
	case err := <-serveErrCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signalCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-serveErrCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
