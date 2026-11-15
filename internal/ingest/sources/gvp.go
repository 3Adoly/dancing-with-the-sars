package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
)

const (
	// GVPURL is the Smithsonian GVP Holocene eruptions WFS request (Guide M16).
	GVPURL  = "https://webservices.volcano.si.edu/geoserver/GVP-VOTW/ows?service=WFS&version=2.0.0&request=GetFeature&typeName=GVP-VOTW:Smithsonian_VOTW_Holocene_Eruptions&outputFormat=application/json"
	gvpPace = 500 * time.Millisecond
)

// GVP is the Smithsonian Global Volcanism Program adapter: ~11k Holocene
// eruptions in one WFS response. The file is small and changes slowly, so it
// is cached once (only the global --refresh downloads it again).
type GVP struct {
	F   *fetch.Fetcher
	Log *slog.Logger
	URL string // "" = GVPURL; tests use an httptest server
}

// NewGVP returns the adapter with its own copy of f (paced; f is unchanged).
func NewGVP(f *fetch.Fetcher, log *slog.Logger) *GVP {
	fc := *f
	fc.Limiter = rate.NewLimiter(rate.Every(gvpPace), 1)
	if log == nil {
		log = slog.Default()
	}
	return &GVP{F: &fc, Log: log}
}

func (s *GVP) Name() string { return "gvp" }

// Fetch downloads the single response and emits every eruption that starts
// in [w.From, w.To). The service has no date filter in the Guide's URL.
func (s *GVP) Fetch(ctx context.Context, w ingest.Window, emit func(domain.Incident) error) error {
	if !w.From.Before(w.To) {
		return nil
	}
	url := s.URL
	if url == "" {
		url = GVPURL
	}
	rc, err := s.F.Get(ctx, s.Name(), url)
	if err != nil {
		return err
	}
	defer rc.Close()
	n, err := parseGVP(ctx, rc, w, emit)
	if err != nil {
		return fmt.Errorf("gvp %s: %w", url, err)
	}
	s.Log.Info("gvp done", "emitted", n)
	return nil
}

type gvpFeature struct {
	Geometry struct {
		Coordinates []float64 `json:"coordinates"`
	} `json:"geometry"`
	Properties struct {
		VolcanoNumber *int64   `json:"Volcano_Number"`
		VolcanoName   string   `json:"Volcano_Name"`
		EruptionNum   *int64   `json:"Eruption_Number"`
		ActivityType  string   `json:"Activity_Type"`
		VEI           *float64 `json:"ExplosivityIndexMax"`
		VEIModifier   string   `json:"ExplosivityIndexModifier"`
		Year          *int     `json:"StartDateYear"`
		Month         *int     `json:"StartDateMonth"`
		Day           *int     `json:"StartDateDay"`
		EndYear       *int     `json:"EndDateYear"`
		Area          string   `json:"ActivityArea"`
	} `json:"properties"`
}

// parseGVP streams the FeatureCollection, one feature at a time.
func parseGVP(ctx context.Context, r io.Reader, w ingest.Window, emit func(domain.Incident) error) (int, error) {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{', "gvp"); err != nil {
		return 0, err
	}
	n, seen := 0, false
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return n, err
		}
		if k, _ := kt.(string); k != "features" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return n, err
			}
			continue
		}
		seen = true
		if err := expectDelim(dec, '[', "gvp"); err != nil {
			return n, err
		}
		for dec.More() {
			if err := ctx.Err(); err != nil {
				return n, err
			}
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return n, err
			}
			inc, ok, err := gvpIncident(raw, w)
			if err != nil {
				return n, err
			}
			if !ok {
				continue
			}
			if err := emit(inc); err != nil {
				return n, err
			}
			n++
		}
		if err := expectDelim(dec, ']', "gvp"); err != nil {
			return n, err
		}
	}
	if !seen {
		return n, fmt.Errorf("no features array in response")
	}
	return n, nil
}

// gvpIncident maps one eruption (Guide M16). Missing month → Jan, precision
// "year"; missing day → the 1st, "month"; else "day". GVP uses 0 for a
// missing month/day. Severity follows the VEI rule of noaa_vo (>=4 → 3,
// 3 → 2, else 1). Uncertain eruptions are kept (props.activityType says so).
func gvpIncident(raw json.RawMessage, w ingest.Window) (inc domain.Incident, ok bool, err error) {
	var f gvpFeature
	if err := json.Unmarshal(raw, &f); err != nil {
		return inc, false, err
	}
	p := f.Properties
	if p.EruptionNum == nil || p.Year == nil || len(f.Geometry.Coordinates) < 2 {
		return inc, false, nil
	}
	lon, lat := f.Geometry.Coordinates[0], f.Geometry.Coordinates[1]
	if !validCoordPair(lon, lat) {
		return inc, false, nil
	}
	y := *p.Year
	start, prec := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC), domain.Precision("year")
	if p.Month != nil && *p.Month >= 1 && *p.Month <= 12 {
		m := time.Month(*p.Month)
		start, prec = time.Date(y, m, 1, 0, 0, 0, 0, time.UTC), "month"
		if p.Day != nil && *p.Day >= 1 && *p.Day <= daysIn(y, m) {
			start, prec = time.Date(y, m, *p.Day, 0, 0, 0, 0, time.UTC), "day"
		}
	}
	if start.Before(w.From) || !start.Before(w.To) {
		return inc, false, nil
	}
	name := collapseSpaces(p.VolcanoName)
	id := strconv.FormatInt(*p.EruptionNum, 10)
	title := fmt.Sprintf("%s eruption (%d)", name, y)
	if name == "" {
		title = fmt.Sprintf("Eruption (%d)", y)
	}
	sev := domain.Severity(1)
	if p.VEI != nil {
		switch {
		case *p.VEI >= 4:
			sev = 3
		case *p.VEI == 3:
			sev = 2
		}
	}
	props := map[string]any{}
	if p.ActivityType != "" {
		props["activityType"] = p.ActivityType
	}
	if p.VolcanoNumber != nil {
		props["volcanoNumber"] = *p.VolcanoNumber
	}
	if p.VEI != nil {
		props["vei"] = *p.VEI
	}
	if p.VEIModifier != "" {
		props["veiModifier"] = p.VEIModifier
	}
	if a := collapseSpaces(p.Area); a != "" {
		props["activityArea"] = a
	}
	if p.EndYear != nil {
		props["endYear"] = *p.EndYear
	}
	inc = domain.Incident{
		Source: "gvp", SourceID: id, Type: domain.Volcano, Title: title, Name: name,
		StartTime: start, TimePrecision: prec, Lon: lon, Lat: lat,
		Magnitude: f32ptr(p.VEI), Severity: sev, Props: props,
		URL:       "https://volcano.si.edu/volcano.cfm?vn=" + strconv.FormatInt(derefInt64(p.VolcanoNumber), 10),
		PlaceText: strings.TrimSpace(name),
		Raw:       raw,
	}
	if p.VEI != nil {
		inc.MagnitudeUnit = "VEI"
	}
	if p.VolcanoNumber == nil {
		inc.URL = ""
	}
	return inc, true, nil
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
