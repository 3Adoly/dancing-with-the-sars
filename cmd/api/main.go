// Command api is the HTTP server (Ahmed).
//
//	api [serve]                 start the HTTP server (default)
//	api prewarm-sar --top 200   fill the SAR cache for top incidents (A9, not implemented yet)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dwts/backend/internal/config"
	"github.com/dwts/backend/internal/httpapi"
	"github.com/dwts/backend/internal/platform"
)

// version is set at build time: go build -ldflags "-X main.version=demo-v1".
var version = "dev"

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "prewarm-sar":
		err = runPrewarmSAR(args)
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "api:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: api [serve] | api prewarm-sar --top N")
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := platform.NewLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	deps := httpapi.Deps{
		Log:            log,
		CORSOrigins:    cfg.CORSOrigins,
		RequestTimeout: cfg.RequestTimeout,
		StartedAt:      time.Now(),
		Version:        version,
		ChatEnabled:    cfg.ChatConfigured(),
	}

	if pool := openPool(ctx, cfg, log); pool != nil {
		defer pool.Close()
		deps.DB = pool // assign only a non-nil pool (avoid a typed-nil interface)
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewRouter(deps),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", cfg.HTTPAddr, "version", version, "db", deps.DB != nil, "chat", deps.ChatEnabled)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shCtx)
}

// openPool connects to Postgres only when DATABASE_URL is set. A failed ping is logged but not
// fatal: pgxpool reconnects lazily, and /healthz reports db:"unavailable" until the DB is up.
func openPool(ctx context.Context, cfg config.Config, log *slog.Logger) *pgxpool.Pool {
	if cfg.DatabaseURL == "" {
		log.Warn("DATABASE_URL not set: running without a database (healthz will report degraded)")
		return nil
	}
	pc, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Error("invalid DATABASE_URL; running without a database", "err", err) // err never contains the password
		return nil
	}
	pc.MaxConns = cfg.DBMaxConns
	pc.ConnConfig.ConnectTimeout = 3 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		log.Error("cannot create db pool; running without a database", "err", err)
		return nil
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		log.Warn("database not reachable yet (will retry lazily)", "err", err)
	} else {
		log.Info("database connected")
	}
	return pool
}

// runPrewarmSAR is task A9 (Phase 2). It lives in cmd/api because it needs service.SARService (rule R6).
func runPrewarmSAR(args []string) error {
	fs := flag.NewFlagSet("prewarm-sar", flag.ContinueOnError)
	top := fs.Int("top", 200, "number of top-severity incidents to prewarm")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return fmt.Errorf("prewarm-sar --top %d: not implemented yet (task A9)", *top)
}
