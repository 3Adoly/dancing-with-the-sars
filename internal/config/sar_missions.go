package config

import (
	"time"

	"github.com/dwts/backend/internal/domain"
)

// SARMissions returns the SAR mission era table (Plan_Detailed §9.1), sorted by start date.
// Searchable missions are queried live via ASF; the others only appear on the coverage timeline.
// Dates of non-searchable missions are approximate.
func SARMissions() []domain.SARMission {
	return []domain.SARMission{
		{ID: "seasat", Name: "Seasat", Agency: "NASA", Band: "L", From: "1978-06-28", To: "1978-10-10", Searchable: true,
			ASFParams: map[string]string{"platform": "SEASAT"}},
		{ID: "ers", Name: "ERS-1/2", Agency: "ESA", Band: "C", From: "1991-07-17", To: "2011-07-04",
			Note: "Not returned by our ASF query"},
		{ID: "jers1", Name: "JERS-1", Agency: "JAXA", Band: "L", From: "1992-02-11", To: "1998-10-12",
			Note: "Not returned by our ASF query"},
		{ID: "radarsat1", Name: "RADARSAT-1", Agency: "CSA", Band: "C", From: "1995-11-04", To: "2013-03-29",
			Note: "CSA open data portal (link only)"},
		{ID: "envisat", Name: "Envisat ASAR", Agency: "ESA", Band: "C", From: "2002-03-01", To: "2012-04-08",
			Note: "Not in our ASF query"},
		{ID: "alos", Name: "ALOS PALSAR", Agency: "JAXA", Band: "L", From: "2006-05-16", To: "2011-04-21", Searchable: true,
			ASFParams: map[string]string{"platform": "ALOS", "processingLevel": "L1.5"}, // verify level on day 1
		},
		{ID: "sentinel1", Name: "Sentinel-1", Agency: "ESA/Copernicus", Band: "C", From: "2014-04-03", Searchable: true,
			ASFParams: map[string]string{"platform": "SENTINEL-1", "processingLevel": "GRD_HD"}},
		{ID: "saocom", Name: "SAOCOM-1A/1B", Agency: "CONAE", Band: "L", From: "2018-10-07",
			Note: "Partner agency: no public search API; used only in a precomputed story (Argentina)"},
		{ID: "rcm", Name: "RADARSAT Constellation", Agency: "CSA", Band: "C", From: "2019-06-12",
			Note: "Partner agency (link only)"},
		{ID: "nisar", Name: "NISAR", Agency: "NASA/ISRO", Band: "L+S", From: "2025-10-28", Searchable: true,
			ASFParams: map[string]string{"dataset": "NISAR", "processingLevel": "GCOV"}},
	}
}

// MissionRange returns the parsed [from, to] of a mission; an empty To yields `now`.
func MissionRange(m domain.SARMission, now time.Time) (from, to time.Time, err error) {
	from, err = time.Parse(time.DateOnly, m.From)
	if err != nil {
		return
	}
	if m.To == "" {
		return from, now, nil
	}
	to, err = time.Parse(time.DateOnly, m.To)
	return
}
