package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/dwts/backend/internal/config"
	"github.com/dwts/backend/internal/platform"
	"github.com/dwts/backend/internal/store/postgres"
)

// boundariesURL is the Natural Earth 50m admin-0 countries GeoJSON (Guide M2).
const boundariesURL = "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/master/geojson/ne_50m_admin_0_countries.geojson"

// prepareBoundaries parses `ingest boundaries` (it takes no flags besides -h).
func prepareBoundaries(args []string, stderr io.Writer) (action, error) {
	return noFlags("boundaries",
		"load the Natural Earth 50m admin-0 countries into the countries table.\n"+
			"The file is downloaded to BOUNDARIES_PATH first when it is missing.",
		cmdBoundaries)(args, stderr)
}

// cmdBoundaries makes sure the GeoJSON exists at cfg.BoundariesPath
// (downloading it if not) and loads it into the countries table.
func cmdBoundaries(ctx context.Context, cfg config.Config) error {
	if cfg.DatabaseURL == "" {
		return errNoDatabase
	}
	if cfg.BoundariesPath == "" {
		return errors.New("BOUNDARIES_PATH is empty")
	}
	client := platform.NewHTTPClient(5 * time.Minute)
	if err := ensureBoundaries(ctx, client, boundariesURL, cfg.BoundariesPath, slog.Default()); err != nil {
		return err
	}
	pg, err := postgres.New(ctx, cfg.DatabaseURL, cfg.DBMaxConns, slog.Default())
	if err != nil {
		return err
	}
	defer pg.Close()
	n, err := pg.LoadCountries(ctx, cfg.BoundariesPath)
	if err != nil {
		return err
	}
	slog.Info("boundaries loaded", "countries", n, "file", cfg.BoundariesPath)
	return nil
}

// ensureBoundaries downloads url to path unless a non-empty file is already
// there. The body goes to a temporary file in the same directory that is
// renamed into place only after a complete download, so an interrupted run
// never leaves a truncated file behind.
func ensureBoundaries(ctx context.Context, client *http.Client, url, path string, log *slog.Logger) error {
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		log.Info("boundaries file present, not downloading", "file", path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("download boundaries: %w", err)
	}
	log.Info("downloading boundaries", "url", url, "file", path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("download boundaries: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download boundaries: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download boundaries: %s returned %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".part-*")
	if err != nil {
		return fmt.Errorf("download boundaries: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	n, err := io.Copy(tmp, resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download boundaries: %w", err)
	}
	if n == 0 {
		return errors.New("download boundaries: empty response")
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("download boundaries: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("download boundaries: %w", err)
	}
	log.Info("boundaries downloaded", "bytes", n, "file", path)
	return nil
}
