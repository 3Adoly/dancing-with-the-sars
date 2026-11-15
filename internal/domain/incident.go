// Package domain holds the pure data types shared by every other package.
// CONTRACT FILE (Plan_Detailed_Level.md §3): change only via a contract/* PR.
// Rule R1: this package imports nothing from internal/ (stdlib only).
package domain

import (
	"encoding/json"
	"strings"
	"time"
)

// IncidentType is the hazard category of an incident.
type IncidentType string

const (
	Earthquake   IncidentType = "earthquake"
	Volcano      IncidentType = "volcano"
	Tsunami      IncidentType = "tsunami"
	Wildfire     IncidentType = "wildfire"      // events (EONET)
	FireActivity IncidentType = "fire_activity" // FIRMS detections, aggregate-only, not clickable
	Flood        IncidentType = "flood"
	Landslide    IncidentType = "landslide"
	Storm        IncidentType = "storm"
	Drought      IncidentType = "drought"
	Other        IncidentType = "other"
)

// AllTypes is the cube axis order. APPEND ONLY: never reorder or remove.
var AllTypes = []IncidentType{Earthquake, Volcano, Tsunami, Wildfire, FireActivity, Flood, Landslide, Storm, Drought, Other}

// ParseType parses a type id (case-insensitive, surrounding spaces ignored).
func ParseType(s string) (IncidentType, bool) {
	t := IncidentType(strings.ToLower(strings.TrimSpace(s)))
	for _, k := range AllTypes {
		if k == t {
			return t, true
		}
	}
	return "", false
}

// Valid reports whether t is a known type.
func (t IncidentType) Valid() bool { _, ok := ParseType(string(t)); return ok }

// Unit is the counting unit of a type: "detections" for fire_activity, "incidents" otherwise.
func (t IncidentType) Unit() string {
	if t == FireActivity {
		return "detections"
	}
	return "incidents"
}

// TypeInfo is the display metadata of a type (E3 /incident-types).
type TypeInfo struct {
	ID        IncidentType `json:"id"`
	Label     string       `json:"label"`
	Unit      string       `json:"unit"`
	Color     string       `json:"color"`
	Icon      string       `json:"icon"`
	FromYear  int          `json:"fromYear"`
	ToYear    int          `json:"toYear"`
	Clickable bool         `json:"clickable"`
}

// TypeCatalog returns the static display metadata in AllTypes order.
// FromYear defaults reflect source coverage; ToYear is filled by the caller (current year).
func TypeCatalog() []TypeInfo {
	return []TypeInfo{
		{ID: Earthquake, Label: "Earthquakes", Unit: "incidents", Color: "#d7301f", Icon: "earthquake", FromYear: 1900, Clickable: true},
		{ID: Volcano, Label: "Volcanoes", Unit: "incidents", Color: "#7a0177", Icon: "volcano", FromYear: 1900, Clickable: true},
		{ID: Tsunami, Label: "Tsunamis", Unit: "incidents", Color: "#0570b0", Icon: "tsunami", FromYear: 1900, Clickable: true},
		{ID: Wildfire, Label: "Wildfires (events)", Unit: "incidents", Color: "#e31a1c", Icon: "wildfire", FromYear: 2000, Clickable: true},
		{ID: FireActivity, Label: "Fire activity (satellite)", Unit: "detections", Color: "#fd8d3c", Icon: "fire", FromYear: 2000, Clickable: false},
		{ID: Flood, Label: "Floods", Unit: "incidents", Color: "#2171b5", Icon: "flood", FromYear: 1985, Clickable: true},
		{ID: Landslide, Label: "Landslides", Unit: "incidents", Color: "#8c510a", Icon: "landslide", FromYear: 1988, Clickable: true},
		{ID: Storm, Label: "Storms / cyclones", Unit: "incidents", Color: "#6a51a3", Icon: "storm", FromYear: 1900, Clickable: true},
		{ID: Drought, Label: "Droughts", Unit: "incidents", Color: "#bf812d", Icon: "drought", FromYear: 2000, Clickable: true},
		{ID: Other, Label: "Other", Unit: "incidents", Color: "#737373", Icon: "other", FromYear: 2000, Clickable: true},
	}
}

// Severity is normalized 0 unknown · 1 low/green · 2 medium/orange · 3 high/red.
type Severity int16

// Precision of StartTime: "second" | "day" | "month" | "year".
type Precision string

const (
	PrecisionSecond Precision = "second"
	PrecisionDay    Precision = "day"
	PrecisionMonth  Precision = "month"
	PrecisionYear   Precision = "year"
)

// Incident is one normalized hazard event.
type Incident struct {
	ID              int64           `json:"id"`
	Source          string          `json:"source"` // usgs|noaa_eq|noaa_ts|noaa_vo|gvp|eonet|gdacs|ibtracs|glc|dfo
	SourceID        string          `json:"sourceId"`
	Type            IncidentType    `json:"type"`
	Title           string          `json:"title"`
	Name            string          `json:"name,omitempty"` // storm / volcano name
	Description     string          `json:"description,omitempty"`
	StartTime       time.Time       `json:"startTime"`
	EndTime         *time.Time      `json:"endTime,omitempty"`
	TimePrecision   Precision       `json:"timePrecision"`
	Year            int16           `json:"year"`
	Lon             float64         `json:"lon"`
	Lat             float64         `json:"lat"`
	Magnitude       *float32        `json:"magnitude,omitempty"`
	MagnitudeUnit   string          `json:"magnitudeUnit,omitempty"` // Mw, VEI, kt, km2
	Severity        Severity        `json:"severity"`
	Deaths          *int32          `json:"deaths,omitempty"`
	PrimaryISO3     string          `json:"primaryIso3,omitempty"` // ADM0_A3; "" = ocean
	Countries       []string        `json:"countries"`             // from incident_countries
	CountryHints    []string        `json:"-"`                     // ingest only
	URL             string          `json:"url,omitempty"`
	Props           map[string]any  `json:"props,omitempty"`
	Track           json.RawMessage `json:"track,omitempty"`           // GeoJSON LineString (storms; detail only)
	TrackWKT        string          `json:"-"`                         // ingest only
	Raw             json.RawMessage `json:"-"`                         // ingest only
	SARAvailability string          `json:"sarAvailability,omitempty"` // detail only: searchable | pre_sar_era | gap
	StoryID         string          `json:"storyId,omitempty"`         // detail only
}

// IncidentSummary is the small list/map/search payload.
type IncidentSummary struct {
	ID          int64        `json:"id"`
	Type        IncidentType `json:"type"`
	Title       string       `json:"title"`
	StartTime   time.Time    `json:"startTime"`
	Lon         float64      `json:"lon"`
	Lat         float64      `json:"lat"`
	Magnitude   *float32     `json:"magnitude,omitempty"`
	Severity    Severity     `json:"severity"`
	PrimaryISO3 string       `json:"primaryIso3,omitempty"`
	Score       float32      `json:"score,omitempty"` // search only
}

// FireCell is one aggregated FIRMS cell (0.25°, month). Ingest only.
type FireCell struct {
	Year, Month    int16
	LatIdx, LonIdx int32
	ISO3           string
	Detections     int32
	FRPSum         float32
	Product        string
}
