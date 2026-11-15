package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dwts/backend/internal/config"
	"github.com/dwts/backend/internal/httpapi"
	"github.com/dwts/backend/internal/service"
	"github.com/dwts/backend/internal/store"
	"github.com/dwts/backend/internal/store/fixture"
	"github.com/dwts/backend/internal/store/memory"
)

// buildDeps wires stores → services → httpapi.Deps. It is shared by `serve` and the router-level
// contract test, so the test exercises the production wiring. pool may be nil (no database);
// searcher is the ASF client (a stub in tests).
func buildDeps(ctx context.Context, cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, searcher service.SARSearcher) (httpapi.Deps, error) {
	deps := httpapi.Deps{
		Log:            log,
		CORSOrigins:    cfg.CORSOrigins,
		RequestTimeout: cfg.RequestTimeout,
		StartedAt:      time.Now(),
		Version:        version,
		ChatEnabled:    cfg.ChatConfigured(),
	}

	if pool != nil {
		deps.DB = pool // assign only a non-nil pool (avoid a typed-nil interface)
	}

	// Stats (A3). Data source: STATS_FIXTURE (dev-only JSON) until the Postgres reader lands in A2.
	var statsReader store.StatsReader
	if cfg.StatsFixture != "" {
		fr, err := fixture.Load(cfg.StatsFixture)
		if err != nil {
			return deps, err
		}
		log.Warn("DEV: serving stats from a fixture file, not the database", "path", cfg.StatsFixture)
		statsReader = fr
	}
	// TODO(A2): else if deps.DB != nil { statsReader = postgres.NewReader(pool) }
	// SAR (A5). Incidents: INCIDENTS_FIXTURE (dev-only) until the Postgres reader lands in A2.
	// Cache: in-memory until store/postgres sarcache.go (A2).
	var incidents store.IncidentReader
	if cfg.IncidentsFixture != "" {
		fi, err := fixture.LoadIncidents(cfg.IncidentsFixture)
		if err != nil {
			return deps, err
		}
		log.Warn("DEV: serving incidents from a fixture file, not the database", "path", cfg.IncidentsFixture, "incidents", fi.Len())
		incidents = fi
	}
	// TODO(A2): else if deps.DB != nil { incidents = postgres reader; sarCache = postgres sar_cache }
	var sarCache store.SARCache = memory.NewSARCache()
	// Stories (A6). TEMPORARY loader until M14 (stories.Load) — see stories_temp.go.
	storyLoader := tempStoryLoader(cfg.StoriesDir)
	storySvc, err := service.NewStoryService(ctx, storyLoader, cfg.StoriesDir, incidents, log)
	if err != nil {
		log.Warn("stories: some or all stories not loaded", "dir", cfg.StoriesDir, "err", err)
	}
	log.Info("stories loaded", "dir", cfg.StoriesDir, "count", storySvc.Len())
	deps.Stories = storySvc

	sar := service.NewSARService(incidents, sarCache, searcher, cfg.SARMissionTable(), storySvc, service.SARConfig{
		WindowDays: cfg.SARWindowDays, MemTTL: cfg.SARCacheTTL, DBFresh: cfg.SARDBFresh, ASFTimeout: cfg.ASFTimeout, Log: log,
	})
	deps.SAR = sar

	features := map[string]bool{"chat": deps.ChatEnabled, "stories": storySvc.Len() > 0, "grid": false, "sar": incidents != nil}
	stats := service.NewStatsService(statsReader, nil, cfg.MinYear, features, log)
	if err := stats.Start(ctx, cfg.CubeReloadInterval); err != nil {
		log.Warn("stats not ready yet; E2/E4/E5 answer 503 not_ready until data loads", "err", err)
	}
	deps.Stats = stats
	return deps, nil
}
