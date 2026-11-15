// Package httpapi is the HTTP layer: chi router, middleware, handlers, DTOs.
// Rule R4: it never imports internal/store or internal/store/postgres; it only calls services.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Pinger is satisfied by *pgxpool.Pool. Nil means "no database configured".
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the dependencies of the router. Services are added as tasks A3–A6 land.
type Deps struct {
	Log            *slog.Logger
	DB             Pinger // nil = DATABASE_URL not set
	CORSOrigins    []string
	RequestTimeout time.Duration
	StartedAt      time.Time
	Version        string
	ChatEnabled    bool       // ADDITIONAL — NOT REQUIRED (A12)
	Stats          StatsAPI   // nil = no stats service (E2/E4/E5 → 503, E3 static)
	SAR            SARAPI     // nil = E10/E11 → 503
	Stories        StoriesAPI // nil = no stories (E12 empty list, E13/E14 404)
}

// NewRouter builds the full HTTP handler.
func NewRouter(d Deps) http.Handler {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.RequestTimeout <= 0 {
		d.RequestTimeout = 10 * time.Second
	}
	if d.StartedAt.IsZero() {
		d.StartedAt = time.Now()
	}

	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(middleware.RealIP)
	r.Use(Logger(d.Log))
	r.Use(Recover)
	r.Use(CORS(d.CORSOrigins))
	r.Use(middleware.GetHead) // HEAD on every GET route
	r.Use(HeadNoBody)
	r.Use(QueryGuard)
	r.Use(Gzip(GzipMinBytes))
	r.Use(Timeout(d.RequestTimeout))
	r.NotFound(handleNotFound)
	r.MethodNotAllowed(handleMethodNotAllowed)

	h := &handlers{d: d}

	// E1
	r.Get("/healthz", h.healthz)

	r.Route("/api/v1", func(r chi.Router) {
		// Registered with 501 until their task lands, so the frontend sees the final error shape.
		r.Get("/meta", h.meta)                                 // E2
		r.Get("/incident-types", h.incidentTypes)              // E3
		r.Get("/stats/choropleth", h.choropleth)               // E4
		r.Get("/stats/timeseries", h.timeseries)               // E5
		r.Get("/stats/grid", notImplemented("A8"))             // E6
		r.Get("/incidents", notImplemented("A4"))              // E7
		r.Get("/incidents/{id}", notImplemented("A4"))         // E8
		r.Get("/search", notImplemented("A4"))                 // E9
		r.Get("/incidents/{id}/sar", h.incidentSAR)            // E10
		r.Get("/sar/coverage-timeline", h.sarCoverageTimeline) // E11
		r.Get("/stories", h.stories)                           // E12
		r.Get("/stories/{id}", h.story)                        // E13
		r.Post("/chat", h.chat)                                // E15 (ADDITIONAL — NOT REQUIRED)
	})
	r.Get("/media/stories/{id}/{file}", h.storyMedia) // E14

	return r
}

type handlers struct{ d Deps }

// chat (E15) answers 503 chat_disabled until A12 is built and enabled.
func (h *handlers) chat(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, &APIError{Status: http.StatusServiceUnavailable, Code: CodeChatDisabled,
		Message: "AI chat is disabled on this server (optional feature)"})
}
