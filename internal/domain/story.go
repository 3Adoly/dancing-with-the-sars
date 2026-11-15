package domain

// Story layer kinds.
const (
	LayerBefore        = "before"
	LayerAfter         = "after"
	LayerChange        = "change"
	LayerInterferogram = "interferogram"
	LayerFrame         = "frame"
)

// StoryLayer is one image overlay of a story. The JSON tags are the manifest schema (Plan_Detailed §10).
type StoryLayer struct {
	Kind   string     `json:"kind"`           // before | after | change | interferogram | frame
	URL    string     `json:"url"`            // manifest: file name in the story dir; API: /media/stories/<id>/<file>
	Bounds [4]float64 `json:"bounds"`         // [west, south, east, north] EPSG:4326
	Date   string     `json:"date,omitempty"` // YYYY-MM-DD
	Label  string     `json:"label,omitempty"`
	Legend *Legend    `json:"legend,omitempty"`
	// Optional per-layer sensor info for stories that mix sensors (e.g. SAOCOM + NISAR).
	// Empty = inherit Story.Platform / Story.Polarization.
	Platform     string `json:"platform,omitempty"`     // e.g. "SAOCOM-1A", "NISAR"
	Polarization string `json:"polarization,omitempty"` // e.g. "HV"
}

// Legend describes how a layer's colors map to values.
type Legend struct {
	Type      string   `json:"type"` // diverging | sequential | cyclic
	Min       float64  `json:"min"`
	Max       float64  `json:"max"`
	Unit      string   `json:"unit"` // dB, cm, rad
	LowLabel  string   `json:"lowLabel"`
	HighLabel string   `json:"highLabel"`
	Colors    []string `json:"colors,omitempty"`
}

// Explanation is the plain-language science text of a story.
type Explanation struct {
	Driver           string `json:"driver"`
	PolarizationNote string `json:"polarizationNote"`
	HowToRead        string `json:"howToRead"`
	Caveats          string `json:"caveats,omitempty"`
}

// Story is a curated change-detection showcase (E13).
type Story struct {
	ID               string       `json:"id"`
	Title            string       `json:"title"`
	Summary          string       `json:"summary"`
	IncidentID       *int64       `json:"incidentId,omitempty"`
	IncidentSource   string       `json:"incidentSource,omitempty"`
	IncidentSourceID string       `json:"incidentSourceId,omitempty"`
	Type             IncidentType `json:"type"`
	Place            string       `json:"place"`
	ISO3             string       `json:"iso3,omitempty"`
	BBox             [4]float64   `json:"bbox"`
	Center           [2]float64   `json:"center"`
	Zoom             float64      `json:"zoom"`
	EventDate        string       `json:"eventDate"`
	BeforeDate       string       `json:"beforeDate"`
	AfterDate        string       `json:"afterDate"`
	Platform         string       `json:"platform"`
	Band             string       `json:"band"`
	Polarization     string       `json:"polarization"`
	Product          string       `json:"product"`
	Thumbnail        string       `json:"thumbnail"`
	Layers           []StoryLayer `json:"layers"`
	Explanation      Explanation  `json:"explanation"`
	Credits          []string     `json:"credits"`
	Order            int          `json:"order"`
}

// StorySummary is one entry of E12 /stories.
type StorySummary struct {
	ID         string       `json:"id"`
	Title      string       `json:"title"`
	Summary    string       `json:"summary"`
	Type       IncidentType `json:"type"`
	Place      string       `json:"place"`
	EventDate  string       `json:"eventDate"`
	Platform   string       `json:"platform"`
	Thumbnail  string       `json:"thumbnail"`
	IncidentID *int64       `json:"incidentId,omitempty"`
	Order      int          `json:"order"`
}

// ToSummary builds the E12 list entry of a story.
func (s Story) ToSummary() StorySummary {
	return StorySummary{ID: s.ID, Title: s.Title, Summary: s.Summary, Type: s.Type, Place: s.Place,
		EventDate: s.EventDate, Platform: s.Platform, Thumbnail: s.Thumbnail, IncidentID: s.IncidentID, Order: s.Order}
}
