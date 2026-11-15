package httpapi

import (
	"context"
	"net/http"
	"time"
)

// healthPingTimeout bounds the DB ping inside /healthz.
const healthPingTimeout = time.Second

// healthz (E1) never fails because of the database: without DATABASE_URL it reports
// db:"not_configured", with an unreachable DB db:"unavailable"; both give status:"degraded" (HTTP 200).
// It returns 503 not_ready only when the cube is required and not loaded (from A3 on).
func (h *handlers) healthz(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:    "ok",
		DB:        "ok",
		UptimeSec: int64(time.Since(h.d.StartedAt).Seconds()),
		Version:   h.d.Version,
	}
	if h.d.DB == nil {
		resp.DB, resp.Status = "not_configured", "degraded"
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), healthPingTimeout)
		defer cancel()
		if err := h.d.DB.Ping(ctx); err != nil {
			loggerFrom(r.Context()).Warn("healthz: db ping failed", "err", err)
			resp.DB, resp.Status = "unavailable", "degraded"
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}
