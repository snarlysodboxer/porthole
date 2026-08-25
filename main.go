// Command porthole is a curated, read-only Kubernetes MCP server for AI
// troubleshooting: a sealed window you can look through but not reach
// through.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/snarlysodboxer/porthole/internal/config"
	"github.com/snarlysodboxer/porthole/internal/kube"
	"github.com/snarlysodboxer/porthole/internal/tools"
)

var version = "dev" // set via -ldflags at build time

func main() {
	// Logs go to stderr: stdout belongs to the protocol in stdio mode.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return err
	}
	clients, err := kube.NewClients(cfg.Kubeconfig)
	if err != nil {
		return err
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "porthole",
		Title:   "Porthole — read-only Kubernetes troubleshooting",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: tools.Instructions(cfg),
		Logger:       logger,
	})
	tools.New(cfg, clients).Register(server)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cfg.Mode {
	case "stdio":
		logger.Info("serving MCP over stdio")
		return server.Run(ctx, &mcp.StdioTransport{})
	case "http":
		return serveHTTP(ctx, logger, cfg, server)
	default:
		return fmt.Errorf("unknown mode %q", cfg.Mode)
	}
}

func serveHTTP(ctx context.Context, logger *slog.Logger, cfg *config.Config, server *mcp.Server) error {
	handler := http.Handler(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		// Stateless is required for the 2026-07-28 spec over streamable
		// HTTP; every porthole tool call is an independent read.
		&mcp.StreamableHTTPOptions{Stateless: true},
	))

	if cfg.AuthTokenFile != "" {
		token, err := os.ReadFile(cfg.AuthTokenFile)
		if err != nil {
			return fmt.Errorf("reading auth token file: %w", err)
		}
		handler = bearerAuth(strings.TrimSpace(string(token)), handler)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/", handler)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("serving MCP over streamable HTTP", "addr", cfg.ListenAddr)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return nil
	}
}

// bearerAuth requires "Authorization: Bearer <token>" on every request.
// Defense in depth behind network policy — not a substitute for it.
func bearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
