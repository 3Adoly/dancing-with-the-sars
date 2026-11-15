package domain

// YearRange is an inclusive range of years.
type YearRange struct{ From, To int }

// BBox in EPSG:4326. MinLon > MaxLon means the box crosses the antimeridian.
type BBox struct{ MinLon, MinLat, MaxLon, MaxLat float64 }

// IncidentFilter drives GET /incidents (E7).
type IncidentFilter struct {
	Types        []IncidentType
	Years        YearRange
	BBox         *BBox
	ISO3         string
	MinMagnitude *float32
	MinSeverity  Severity
	Sort         string // "severity" (default) | "time"
	Limit        int    // 1..5000, default 500
	Cursor       string // opaque keyset cursor (see cursor.go)
}

// SearchQuery drives GET /search (E9). Limit ≤ 50.
type SearchQuery struct {
	Q     string
	Types []IncidentType
	Years *YearRange
	Limit int
}

// ChoroplethQuery drives GET /stats/choropleth (E4).
type ChoroplethQuery struct {
	Types []IncidentType
	Years YearRange
}

// TimeseriesQuery drives GET /stats/timeseries (E5). ISO3 "" = world.
type TimeseriesQuery struct {
	Types []IncidentType
	Years YearRange
	ISO3  string
}

// GridQuery drives GET /stats/grid (E6). CellDeg ∈ {0.25, 0.5, 1, 2}.
type GridQuery struct {
	Types   []IncidentType
	Years   YearRange
	BBox    BBox
	CellDeg float64
}
