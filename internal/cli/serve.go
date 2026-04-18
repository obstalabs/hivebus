package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ppiankov/hivebus/internal/artifact"
	"github.com/ppiankov/hivebus/internal/runtime"
	"github.com/ppiankov/hivebus/internal/store"
	"github.com/spf13/cobra"
)

const shutdownTimeout = 10 * time.Second

func newServeCommand() *cobra.Command {
	var listenAddr string
	var dbPath string
	var artifactsDir string
	var tokensFile string
	var authDisabled bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the v0 Hivebus HTTP runtime",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dbPath == "" {
				dbPath = filepath.Join(".", ".hivebus", "events.db")
			}
			if artifactsDir == "" {
				artifactsDir = filepath.Join(".", ".hivebus", "artifacts")
			}

			return runServe(
				cmd.Context(),
				cmd.OutOrStdout(),
				listenAddr,
				dbPath,
				artifactsDir,
				tokensFile,
				authDisabled,
			)
		},
	}

	cmd.Flags().StringVar(&listenAddr, "listen", "127.0.0.1:7081", "Listen address for the HTTP runtime")
	cmd.Flags().StringVar(&dbPath, "db", "", "Path to the SQLite event log")
	cmd.Flags().StringVar(&artifactsDir, "artifacts-dir", "", "Path to the local artifact store")
	cmd.Flags().StringVar(&tokensFile, "tokens-file", "", "Path to a JSON file containing hashed operator and worker tokens for local auth fallback")
	cmd.Flags().BoolVar(&authDisabled, "auth-disabled", false, "Disable runtime auth explicitly for local development")

	return cmd
}

func runServe(
	ctx context.Context,
	out io.Writer,
	listenAddr string,
	dbPath string,
	artifactsDir string,
	tokensFile string,
	authDisabled bool,
) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = st.Close()
	}()

	artifacts, err := artifact.Open(artifactsDir)
	if err != nil {
		return err
	}

	keys, err := loadKeyStore(tokensFile, authDisabled)
	if err != nil {
		return err
	}

	workOrders, err := loadWorkOrderBridgeFromEnv()
	if err != nil {
		return err
	}

	handler := runtime.NewHandlerWithOptions(
		st,
		artifacts,
		keys,
		runtime.HandlerOptions{
			WorkOrders: workOrders,
		},
	)

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

func loadWorkOrderBridgeFromEnv() (runtime.WorkOrderBridge, error) {
	apiKey := strings.TrimSpace(os.Getenv("WORKLEDGER_API_KEY"))
	if apiKey == "" {
		return nil, nil
	}

	baseURL := strings.TrimSpace(os.Getenv("WORKLEDGER_URL"))
	if baseURL == "" {
		host := strings.TrimSpace(os.Getenv("WORKLEDGER_HOST"))
		if host == "" {
			return nil, errors.New("WORKLEDGER_API_KEY requires WORKLEDGER_URL or WORKLEDGER_HOST")
		}
		baseURL = "https://" + host + "/api/v1"
	}

	return runtime.NewWorkledgerHTTPBridge(baseURL, apiKey)
}

func loadKeyStore(tokensFile string, authDisabled bool) (*runtime.KeyStore, error) {
	envTokensJSON := os.Getenv("HIVEBUS_TOKENS_JSON")
	envTokensFile := os.Getenv("HIVEBUS_TOKENS_FILE")
	envVerifyKey := os.Getenv("HIVEBUS_API_VERIFY_KEY")

	var configuredSources int
	if tokensFile != "" {
		configuredSources++
	}
	if envTokensJSON != "" {
		configuredSources++
	}
	if envTokensFile != "" {
		configuredSources++
	}
	if strings.TrimSpace(envVerifyKey) != "" {
		configuredSources++
	}
	if configuredSources > 1 {
		return nil, errors.New("configure auth from exactly one source: --tokens-file, HIVEBUS_TOKENS_FILE, HIVEBUS_TOKENS_JSON, or HIVEBUS_API_VERIFY_KEY")
	}
	if authDisabled {
		if configuredSources > 0 {
			return nil, errors.New("cannot combine --auth-disabled with token configuration")
		}
		return nil, nil
	}

	if tokensFile == "" {
		tokensFile = envTokensFile
	}
	switch {
	case tokensFile != "":
		return runtime.LoadKeyStore(tokensFile)
	case envTokensJSON != "":
		return runtime.ParseKeyStore([]byte(envTokensJSON))
	case strings.TrimSpace(envVerifyKey) != "":
		return runtime.NewSignedKeyStore(envVerifyKey)
	case strings.TrimSpace(runtime.BuiltInAPIVerifyKey) != "":
		return runtime.NewSignedKeyStore(runtime.BuiltInAPIVerifyKey)
	default:
		return nil, errors.New("auth requires HIVEBUS_API_VERIFY_KEY, a build-time verify key, or token configuration unless --auth-disabled is set")
	}
}
