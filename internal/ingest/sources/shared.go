package sources

import (
	"net/http"
	"strings"
	"time"

	"github.com/dwts/backend/internal/platform"
)

// atLeastTimeout returns an HTTP client whose timeout (which covers the
// whole response body) is at least d: a new client when hc is nil, a copy
// of hc sharing its transport when hc's timeout is shorter, else hc itself
// (a zero timeout means none). hc is never changed.
func atLeastTimeout(hc *http.Client, d time.Duration) *http.Client {
	switch {
	case hc == nil:
		return platform.NewHTTPClient(d)
	case hc.Timeout > 0 && hc.Timeout < d:
		c := *hc
		c.Timeout = d
		return &c
	}
	return hc
}

// joinNonEmpty joins the non-empty parts with sep.
func joinNonEmpty(sep string, parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// validCoordPair reports whether lon is within ±180 and lat within ±90
// (NaN fails both).
func validCoordPair(lon, lat float64) bool {
	return lon >= -180 && lon <= 180 && lat >= -90 && lat <= 90
}

// collapseSpaces trims s and turns every run of whitespace into one space.
func collapseSpaces(s string) string { return strings.Join(strings.Fields(s), " ") }
