package httpapi

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/dwts/backend/internal/domain"
)

// parseTypes parses the CSV `types` parameter (dedup, order kept). Unknown ids → 400.
func parseTypes(q url.Values, required bool) ([]domain.IncidentType, error) {
	raw := strings.TrimSpace(q.Get("types"))
	if raw == "" {
		if required {
			return nil, BadParam("types", "types is required (comma-separated, e.g. earthquake,flood)")
		}
		return nil, nil
	}
	seen := map[domain.IncidentType]bool{}
	var out []domain.IncidentType
	for _, s := range strings.Split(raw, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		t, ok := domain.ParseType(s)
		if !ok {
			return nil, BadParam("types", fmt.Sprintf("unknown incident type %q", strings.TrimSpace(s)))
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 && required {
		return nil, BadParam("types", "types is required")
	}
	return out, nil
}

// ensureSingleUnit rejects mixing fire_activity (detections) with incident types (stats endpoints).
func ensureSingleUnit(types []domain.IncidentType) error {
	if len(types) > 1 {
		for _, t := range types {
			if t == domain.FireActivity {
				return BadParam("types", "fire_activity must be requested alone (its unit is detections)")
			}
		}
	}
	return nil
}

// parseYearRange reads `from`/`to` (inclusive). Missing → [minYear, maxYear]; values are clamped
// to that range; from > to → 400.
func parseYearRange(q url.Values, minYear, maxYear int) (domain.YearRange, error) {
	yr := domain.YearRange{From: minYear, To: maxYear}
	for _, p := range []struct {
		name string
		dst  *int
	}{{"from", &yr.From}, {"to", &yr.To}} {
		v := strings.TrimSpace(q.Get(p.name))
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return yr, BadParam(p.name, p.name+" must be a year (integer)")
		}
		*p.dst = max(minYear, min(maxYear, n))
	}
	if yr.From > yr.To {
		return yr, BadParam("from", "from must be <= to")
	}
	return yr, nil
}

// parseBBox reads `bbox=minLon,minLat,maxLon,maxLat`. minLon > maxLon is allowed (antimeridian).
func parseBBox(q url.Values, required bool) (*domain.BBox, error) {
	raw := strings.TrimSpace(q.Get("bbox"))
	if raw == "" {
		if required {
			return nil, BadParam("bbox", "bbox is required (minLon,minLat,maxLon,maxLat)")
		}
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return nil, BadParam("bbox", "bbox must have 4 numbers: minLon,minLat,maxLon,maxLat")
	}
	var v [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, BadParam("bbox", "bbox values must be numbers")
		}
		v[i] = f
	}
	b := domain.BBox{MinLon: v[0], MinLat: v[1], MaxLon: v[2], MaxLat: v[3]}
	if b.MinLon < -180 || b.MinLon > 180 || b.MaxLon < -180 || b.MaxLon > 180 {
		return nil, BadParam("bbox", "longitudes must be within [-180, 180]")
	}
	if b.MinLat < -90 || b.MinLat > 90 || b.MaxLat < -90 || b.MaxLat > 90 {
		return nil, BadParam("bbox", "latitudes must be within [-90, 90]")
	}
	if b.MinLat >= b.MaxLat {
		return nil, BadParam("bbox", "minLat must be < maxLat")
	}
	if b.MinLon == b.MaxLon {
		return nil, BadParam("bbox", "minLon must differ from maxLon")
	}
	return &b, nil
}

// parseLimit reads `limit` in [1, maxLimit]; missing → def.
func parseLimit(q url.Values, def, maxLimit int) (int, error) {
	v := strings.TrimSpace(q.Get("limit"))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxLimit {
		return 0, BadParam("limit", fmt.Sprintf("limit must be an integer between 1 and %d", maxLimit))
	}
	return n, nil
}

// parseCursor validates the opaque `cursor` parameter and returns it unchanged ("" if absent).
func parseCursor(q url.Values) (string, error) {
	c := strings.TrimSpace(q.Get("cursor"))
	if c == "" {
		return "", nil
	}
	if _, err := domain.DecodeCursor(c); err != nil {
		return "", BadParam("cursor", "cursor is invalid; use nextCursor from the previous page")
	}
	return c, nil
}

// parseID parses a positive int64 path id.
func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, BadParam("id", "id must be a positive integer")
	}
	return id, nil
}

// parseBool reads an optional boolean parameter.
func parseBool(q url.Values, name string, def bool) (bool, error) {
	v := strings.TrimSpace(q.Get(name))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def, BadParam(name, name+" must be true or false")
	}
	return b, nil
}

// parseIntRange reads an optional integer parameter in [lo, hi].
func parseIntRange(q url.Values, name string, def, lo, hi int) (int, error) {
	v := strings.TrimSpace(q.Get(name))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return def, BadParam(name, fmt.Sprintf("%s must be an integer between %d and %d", name, lo, hi))
	}
	return n, nil
}

// parseCell reads `cell` for the grid endpoint: 0.25 | 0.5 | 1 | 2 (default 1).
func parseCell(q url.Values) (float64, error) {
	v := strings.TrimSpace(q.Get("cell"))
	if v == "" {
		return 1, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err == nil {
		switch f {
		case 0.25, 0.5, 1, 2:
			return f, nil
		}
	}
	return 0, BadParam("cell", "cell must be one of 0.25, 0.5, 1, 2")
}

// parseSort reads `sort`: severity (default) | time.
func parseSort(q url.Values) (string, error) {
	switch v := strings.TrimSpace(q.Get("sort")); v {
	case "", "severity":
		return "severity", nil
	case "time":
		return "time", nil
	default:
		return "", BadParam("sort", "sort must be severity or time")
	}
}
