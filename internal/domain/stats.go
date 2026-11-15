package domain

import "time"

// CountRow is one row of mv_counts. ISO3 "" = ocean.
type CountRow struct {
	Type IncidentType
	ISO3 string
	Year int16
	N    int32
}

// TotalRow is one row of mv_totals (DISTINCT incidents per type/year).
type TotalRow struct {
	Type IncidentType
	Year int16
	N    int32
}

// Coverage says which years a type is really covered, and by which sources.
type Coverage struct {
	Type     IncidentType `json:"type"`
	FromYear int          `json:"fromYear"`
	ToYear   int          `json:"toYear"`
	Sources  []string     `json:"sources"`
	Note     string       `json:"note"`
}

// Choropleth is the E4 response. Total = DISTINCT incidents; Σcounts+ocean may exceed it.
type Choropleth struct {
	Types          []IncidentType   `json:"types"`
	From           int              `json:"from"`
	To             int              `json:"to"`
	Level          string           `json:"level"`
	Unit           string           `json:"unit"`
	Counts         map[string]int64 `json:"counts"`
	Ocean          int64            `json:"ocean"`
	Total          int64            `json:"total"`
	Max            int64            `json:"max"`
	Breaks         []int64          `json:"breaks"`
	Coverage       []Coverage       `json:"coverage"`
	DatasetVersion int64            `json:"datasetVersion"`
}

// YearCount is one timeseries point.
type YearCount struct {
	Year int   `json:"year"`
	N    int64 `json:"n"`
}

// Timeseries is the E5 response.
type Timeseries struct {
	Types          []IncidentType `json:"types"`
	ISO3           string         `json:"iso3"`
	Unit           string         `json:"unit"`
	Points         []YearCount    `json:"points"`
	DatasetVersion int64          `json:"datasetVersion"`
}

// GridCell is one density cell (center coordinates).
type GridCell struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
	N   int64   `json:"n"`
}

// Meta is the E2 response.
type Meta struct {
	DatasetVersion int64           `json:"datasetVersion"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	MinYear        int             `json:"minYear"`
	MaxYear        int             `json:"maxYear"`
	Features       map[string]bool `json:"features"` // chat, stories, grid, sar
	Coverage       []Coverage      `json:"coverage"`
}
