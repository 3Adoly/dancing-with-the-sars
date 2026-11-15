package domain

import (
	"encoding/json"
	"time"
)

// SARMission is one row of the SAR mission era table (Plan_Detailed §9.1).
type SARMission struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Agency     string            `json:"agency"`
	Band       string            `json:"band"`
	From       string            `json:"from"`         // YYYY-MM-DD
	To         string            `json:"to,omitempty"` // empty = still active
	Searchable bool              `json:"searchable"`   // queried live by /sar
	ASFParams  map[string]string `json:"-"`            // e.g. {"dataset":"NISAR","processingLevel":"GCOV"}
	Note       string            `json:"note,omitempty"`
}

// SARScene is one ASF granule.
type SARScene struct {
	GranuleID       string          `json:"granuleId"`
	Mission         string          `json:"mission"`
	Platform        string          `json:"platform"`
	BeamMode        string          `json:"beamMode,omitempty"`
	Polarization    string          `json:"polarization,omitempty"`
	FlightDirection string          `json:"flightDirection,omitempty"`
	Path            int             `json:"path,omitempty"`
	Frame           int             `json:"frame,omitempty"`
	StartTime       time.Time       `json:"startTime"`
	ProcessingLevel string          `json:"processingLevel"`
	BrowseURL       string          `json:"browseUrl,omitempty"`
	DownloadURL     string          `json:"downloadUrl,omitempty"`
	SizeMB          float64         `json:"sizeMB,omitempty"`
	Footprint       json.RawMessage `json:"footprint,omitempty"`
}

// SARPair names the best before/after granules.
type SARPair struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// SAR coverage values.
const (
	SARCoverageOK         = "ok"
	SARCoverageBeforeOnly = "before_only"
	SARCoverageAfterOnly  = "after_only"
	SARCoverageNone       = "none"
	SARCoveragePreSAREra  = "pre_sar_era"
)

// SARResult is the E10 response.
type SARResult struct {
	IncidentID       int64      `json:"incidentId"`
	Coverage         string     `json:"coverage"`         // ok | before_only | after_only | none | pre_sar_era
	Reason           string     `json:"reason,omitempty"` // no_scenes_found | no_searchable_mission_in_window | upstream_error
	WindowDays       int        `json:"windowDays"`
	MissionsInWindow []string   `json:"missionsInWindow"`
	Before           []SARScene `json:"before"`
	After            []SARScene `json:"after"`
	BestPair         *SARPair   `json:"bestPair,omitempty"`
	StoryID          string     `json:"storyId,omitempty"`
	FetchedAt        time.Time  `json:"fetchedAt"`
	Stale            bool       `json:"stale"`
	ASFSearchURL     string     `json:"asfSearchUrl"`
}

// YearMissions lists the missions active in one year.
type YearMissions struct {
	Year     int      `json:"year"`
	Missions []string `json:"missions"`
}

// SARCoverageTimeline is the E11 response.
type SARCoverageTimeline struct {
	Missions []SARMission   `json:"missions"`
	ByYear   []YearMissions `json:"byYear"`
}
