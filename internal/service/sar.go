package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/external/asf"
	"github.com/dwts/backend/internal/store"
)

// SARSearcher is implemented by *asf.Client (and by stubs in tests).
type SARSearcher interface {
	Search(ctx context.Context, q asf.Query) ([]asf.Scene, error)
}

// StoryLinker returns the story id linked to an incident. Implemented by StoryService (A6); may be nil.
type StoryLinker interface {
	ForIncident(id int64) (storyID string, ok bool)
}

// SARConfig holds SARService tuning (from config).
type SARConfig struct {
	WindowDays int           // default ±days around the event (45)
	MemTTL     time.Duration // memory LRU TTL (24h)
	DBFresh    time.Duration // sar_cache rows younger than this are fresh (168h)
	ASFTimeout time.Duration // overall ASF fetch timeout (8s)
	MemEntries int           // LRU size (5000)
	MaxPerSide int           // scenes kept before/after (10)
	Log        *slog.Logger
}

type sarKey struct {
	id     int64
	window int
}

// SARService implements E10/E11 (Plan_Detailed §9).
type SARService struct {
	inc      store.IncidentReader
	cache    store.SARCache // may be nil (memory only)
	asf      SARSearcher
	stories  StoryLinker
	missions []domain.SARMission
	cfg      SARConfig
	mem      *ttlCache[sarKey, domain.SARResult]
	sf       singleflight.Group
	now      func() time.Time
	bg       sync.WaitGroup // background refreshes (tests wait on it)
}

// NewSARService builds the service. inc may be nil (then ForIncident answers ErrNotReady).
func NewSARService(inc store.IncidentReader, cache store.SARCache, a SARSearcher, missions []domain.SARMission, stories StoryLinker, cfg SARConfig) *SARService {
	if cfg.WindowDays <= 0 {
		cfg.WindowDays = 45
	}
	if cfg.MemTTL <= 0 {
		cfg.MemTTL = 24 * time.Hour
	}
	if cfg.DBFresh <= 0 {
		cfg.DBFresh = 7 * 24 * time.Hour
	}
	if cfg.ASFTimeout <= 0 {
		cfg.ASFTimeout = 8 * time.Second
	}
	if cfg.MemEntries <= 0 {
		cfg.MemEntries = 5000
	}
	if cfg.MaxPerSide <= 0 {
		cfg.MaxPerSide = 10
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &SARService{inc: inc, cache: cache, asf: a, stories: stories, missions: missions, cfg: cfg,
		mem: newTTLCache[sarKey, domain.SARResult](cfg.MemEntries, cfg.MemTTL), now: time.Now}
}

// DefaultWindowDays is the window used when the client does not pass windowDays.
func (s *SARService) DefaultWindowDays() int { return s.cfg.WindowDays }

// ForIncident is ForIncidentWindow with the default window.
func (s *SARService) ForIncident(ctx context.Context, id int64, refresh bool) (domain.SARResult, error) {
	return s.ForIncidentWindow(ctx, id, s.cfg.WindowDays, refresh)
}

// ForIncidentWindow returns SAR scenes before/after incident id within ±windowDays.
// Order: era rule (no ASF call) → memory LRU → sar_cache (fresh, or stale + background refresh) →
// ASF (singleflight) → on ASF error, any cached value with stale=true, else ErrUpstream/ErrUpstreamTimeout.
// Only default-window results are persisted to sar_cache (its key is the incident id).
func (s *SARService) ForIncidentWindow(ctx context.Context, id int64, windowDays int, refresh bool) (domain.SARResult, error) {
	if s.inc == nil {
		return domain.SARResult{}, fmt.Errorf("sar: no incident source: %w", domain.ErrNotReady)
	}
	if windowDays < 1 || windowDays > 180 {
		return domain.SARResult{}, fmt.Errorf("windowDays must be 1..180: %w", domain.ErrInvalid)
	}
	inc, err := s.inc.GetIncident(ctx, id)
	if err != nil {
		return domain.SARResult{}, err
	}
	w := time.Duration(windowDays) * 24 * time.Hour
	inWindow := s.missionsInWindow(inc.StartTime.Add(-w), inc.StartTime.Add(w))
	if len(inWindow) == 0 {
		return s.noMissionResult(inc, windowDays), nil
	}

	key := sarKey{id, windowDays}
	persist := windowDays == s.cfg.WindowDays && s.cache != nil
	var stale *domain.SARResult
	if !refresh {
		if r, ok := s.mem.Get(key); ok {
			return r, nil
		}
		if persist {
			r, err := s.cache.GetSAR(ctx, id)
			switch {
			case err == nil && s.now().Sub(r.FetchedAt) < s.cfg.DBFresh:
				r.Stale = false
				s.mem.Put(key, r)
				return r, nil
			case err == nil: // stale: serve it now, refresh in the background
				r.Stale = true
				s.refreshInBackground(inc, windowDays, inWindow)
				return r, nil
			case !errors.Is(err, domain.ErrNotFound):
				s.cfg.Log.Warn("sar_cache read failed", "incident", id, "err", err)
			}
		}
	} else if persist {
		if r, err := s.cache.GetSAR(ctx, id); err == nil {
			stale = &r // fallback if ASF fails
		}
	}
	if r, ok := s.mem.Get(key); ok && stale == nil {
		stale = &r
	}

	res, err := s.fetchShared(ctx, inc, windowDays, inWindow)
	if err != nil {
		if stale != nil {
			r := *stale
			r.Stale, r.Reason = true, "upstream_error"
			s.cfg.Log.Warn("ASF failed; serving cached SAR result", "incident", id, "err", err)
			return r, nil
		}
		return domain.SARResult{}, err
	}
	return res, nil
}

// fetchShared deduplicates concurrent fetches for the same key (singleflight) and stores the result.
// The fetch runs on a context detached from the caller so one canceled client does not fail others.
func (s *SARService) fetchShared(ctx context.Context, inc domain.Incident, windowDays int, missions []domain.SARMission) (domain.SARResult, error) {
	k := strconv.FormatInt(inc.ID, 10) + "/" + strconv.Itoa(windowDays)
	ch := s.sf.DoChan(k, func() (any, error) {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ASFTimeout)
		defer cancel()
		res, err := s.fetch(fctx, inc, windowDays, missions)
		if err != nil {
			return domain.SARResult{}, err
		}
		s.mem.Put(sarKey{inc.ID, windowDays}, res)
		if windowDays == s.cfg.WindowDays && s.cache != nil {
			if err := s.cache.PutSAR(fctx, res); err != nil {
				s.cfg.Log.Warn("sar_cache write failed", "incident", inc.ID, "err", err)
			}
		}
		return res, nil
	})
	select {
	case r := <-ch:
		if r.Err != nil {
			return domain.SARResult{}, r.Err
		}
		return r.Val.(domain.SARResult), nil
	case <-ctx.Done():
		return domain.SARResult{}, ctx.Err()
	}
}

func (s *SARService) refreshInBackground(inc domain.Incident, windowDays int, missions []domain.SARMission) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		if _, err := s.fetchShared(context.Background(), inc, windowDays, missions); err != nil {
			s.cfg.Log.Warn("background SAR refresh failed", "incident", inc.ID, "err", err)
		}
	}()
}

// fetch queries ASF once per mission in parallel. A mission that fails is logged and skipped;
// only when every mission fails is an error returned (mapped to ErrUpstream / ErrUpstreamTimeout).
func (s *SARService) fetch(ctx context.Context, inc domain.Incident, windowDays int, missions []domain.SARMission) (domain.SARResult, error) {
	w := time.Duration(windowDays) * 24 * time.Hour
	wkt := searchWKT(inc)
	results := make([][]domain.SARScene, len(missions))
	errs := make([]error, len(missions))
	var g errgroup.Group
	for i, m := range missions {
		g.Go(func() error {
			scenes, err := s.asf.Search(ctx, asf.Query{Params: m.ASFParams, WKT: wkt,
				Start: inc.StartTime.Add(-w), End: inc.StartTime.Add(w), MaxResults: 250})
			if err != nil {
				errs[i] = err
				return nil
			}
			for _, sc := range scenes {
				results[i] = append(results[i], toDomainScene(m.ID, sc))
			}
			return nil
		})
	}
	_ = g.Wait()

	var all []domain.SARScene
	failed := 0
	for i := range missions {
		if errs[i] != nil {
			failed++
			s.cfg.Log.Warn("ASF mission query failed", "mission", missions[i].ID, "incident", inc.ID, "err", errs[i])
			continue
		}
		all = append(all, results[i]...)
	}
	if failed == len(missions) {
		for _, e := range errs {
			if errors.Is(e, asf.ErrTimeout) || errors.Is(e, context.DeadlineExceeded) {
				return domain.SARResult{}, fmt.Errorf("asf: %v: %w", e, domain.ErrUpstreamTimeout)
			}
		}
		return domain.SARResult{}, fmt.Errorf("asf: %v: %w", errs[0], domain.ErrUpstream)
	}

	ids := make([]string, len(missions))
	for i, m := range missions {
		ids[i] = m.ID
	}
	res := BuildSARResult(inc, windowDays, ids, all, s.cfg.MaxPerSide)
	res.FetchedAt = s.now().UTC()
	res.StoryID = s.storyFor(inc.ID)
	return res, nil
}

// BuildSARResult deduplicates, splits at the event time, keeps maxPerSide per side, sets coverage and bestPair.
func BuildSARResult(inc domain.Incident, windowDays int, missionIDs []string, scenes []domain.SARScene, maxPerSide int) domain.SARResult {
	seen := map[string]bool{}
	var before, after []domain.SARScene
	for _, sc := range scenes {
		if seen[sc.GranuleID] {
			continue
		}
		seen[sc.GranuleID] = true
		if sc.StartTime.Before(inc.StartTime) {
			before = append(before, sc)
		} else {
			after = append(after, sc)
		}
	}
	slices.SortFunc(before, func(a, b domain.SARScene) int { return b.StartTime.Compare(a.StartTime) }) // newest first
	slices.SortFunc(after, func(a, b domain.SARScene) int { return a.StartTime.Compare(b.StartTime) })  // oldest first
	before, after = before[:min(len(before), maxPerSide)], after[:min(len(after), maxPerSide)]

	res := domain.SARResult{
		IncidentID: inc.ID, WindowDays: windowDays, MissionsInWindow: missionIDs,
		Before: nonNil(before), After: nonNil(after), ASFSearchURL: VertexURL(inc, windowDays),
	}
	switch {
	case len(before) > 0 && len(after) > 0:
		res.Coverage = domain.SARCoverageOK
	case len(before) > 0:
		res.Coverage = domain.SARCoverageBeforeOnly
	case len(after) > 0:
		res.Coverage = domain.SARCoverageAfterOnly
	default:
		res.Coverage, res.Reason = domain.SARCoverageNone, "no_scenes_found"
	}
	res.BestPair = bestPair(inc.StartTime, before, after)
	return res
}

// bestPair picks the before/after pair on the same mission, path, frame and flight direction.
// Ranking (challenge-driven): 1) NISAR pairs beat every other mission (the challenge is about NISAR),
// 2) pairs with browse images on both sides, 3) the smallest total time gap around the event.
func bestPair(t time.Time, before, after []domain.SARScene) *domain.SARPair {
	var best *domain.SARPair
	bestRank, bestBrowse, bestGap := 1<<30, false, time.Duration(1<<62)
	for _, b := range before {
		for _, a := range after {
			if a.Mission != b.Mission || a.Path != b.Path || a.Frame != b.Frame || a.FlightDirection != b.FlightDirection {
				continue
			}
			rank := missionRank(a.Mission)
			browse := a.BrowseURL != "" && b.BrowseURL != ""
			gap := t.Sub(b.StartTime) + a.StartTime.Sub(t)
			better := rank < bestRank ||
				(rank == bestRank && browse && !bestBrowse) ||
				(rank == bestRank && browse == bestBrowse && gap < bestGap)
			if better {
				best = &domain.SARPair{Before: b.GranuleID, After: a.GranuleID}
				bestRank, bestBrowse, bestGap = rank, browse, gap
			}
		}
	}
	return best
}

// missionRank orders missions for bestPair: NISAR first, everything else equal.
func missionRank(mission string) int {
	if strings.EqualFold(mission, "nisar") {
		return 0
	}
	return 1
}

func (s *SARService) noMissionResult(inc domain.Incident, windowDays int) domain.SARResult {
	cov := domain.SARCoverageNone
	if inc.StartTime.Before(s.modernSAREraStart()) {
		cov = domain.SARCoveragePreSAREra
	}
	return domain.SARResult{
		IncidentID: inc.ID, Coverage: cov, Reason: "no_searchable_mission_in_window", WindowDays: windowDays,
		MissionsInWindow: []string{}, Before: []domain.SARScene{}, After: []domain.SARScene{},
		StoryID: s.storyFor(inc.ID), FetchedAt: s.now().UTC(), ASFSearchURL: VertexURL(inc, windowDays),
	}
}

// Availability classifies an incident time for E8's sarAvailability: searchable | pre_sar_era | gap.
func (s *SARService) Availability(t time.Time) string {
	w := time.Duration(s.cfg.WindowDays) * 24 * time.Hour
	switch {
	case len(s.missionsInWindow(t.Add(-w), t.Add(w))) > 0:
		return "searchable"
	case t.Before(s.modernSAREraStart()):
		return "pre_sar_era"
	default:
		return "gap"
	}
}

// missionsInWindow returns the searchable missions whose [From, To|now] overlaps [a, b].
func (s *SARService) missionsInWindow(a, b time.Time) []domain.SARMission {
	now := s.now()
	var out []domain.SARMission
	for _, m := range s.missions {
		if !m.Searchable {
			continue
		}
		from, to, err := missionRange(m, now)
		if err == nil && !from.After(b) && !to.Before(a) {
			out = append(out, m)
		}
	}
	return out
}

// modernSAREraStart is the start of the earliest searchable mission other than the 1978 Seasat
// one-off (ALOS, 2006-05-16). Events before it with no mission in the window are "pre_sar_era".
func (s *SARService) modernSAREraStart() time.Time {
	var first time.Time
	for _, m := range s.missions {
		if !m.Searchable || m.ID == "seasat" {
			continue
		}
		if from, _, err := missionRange(m, s.now()); err == nil && (first.IsZero() || from.Before(first)) {
			first = from
		}
	}
	return first
}

func missionRange(m domain.SARMission, now time.Time) (time.Time, time.Time, error) {
	from, err := time.Parse(time.DateOnly, m.From)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if m.To == "" {
		return from, now, nil
	}
	to, err := time.Parse(time.DateOnly, m.To)
	return from, to.Add(24*time.Hour - time.Nanosecond), err // To is inclusive (whole day)
}

// CoverageTimeline builds E11 from the mission table: missions + active missions per year (1978→now).
func (s *SARService) CoverageTimeline() domain.SARCoverageTimeline {
	now := s.now()
	out := domain.SARCoverageTimeline{Missions: slices.Clone(s.missions), ByYear: []domain.YearMissions{}}
	for y := 1978; y <= now.UTC().Year(); y++ {
		ys := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		ye := time.Date(y, 12, 31, 23, 59, 59, 0, time.UTC)
		ym := domain.YearMissions{Year: y, Missions: []string{}}
		for _, m := range s.missions {
			if from, to, err := missionRange(m, now); err == nil && !from.After(ye) && !to.Before(ys) {
				ym.Missions = append(ym.Missions, m.ID)
			}
		}
		out.ByYear = append(out.ByYear, ym)
	}
	return out
}

func (s *SARService) storyFor(id int64) string {
	if s.stories == nil {
		return ""
	}
	if sid, ok := s.stories.ForIncident(id); ok {
		return sid
	}
	return ""
}

// searchWKT: a point for localized hazards; a 0.25° box for floods and storms (area events).
func searchWKT(inc domain.Incident) string {
	if inc.Type == domain.Flood || inc.Type == domain.Storm {
		const h = 0.125
		w, e := max(inc.Lon-h, -180), min(inc.Lon+h, 180)
		so, n := max(inc.Lat-h, -90), min(inc.Lat+h, 90)
		return fmt.Sprintf("POLYGON((%.4f %.4f,%.4f %.4f,%.4f %.4f,%.4f %.4f,%.4f %.4f))", w, so, e, so, e, n, w, n, w, so)
	}
	return fmt.Sprintf("POINT(%.4f %.4f)", inc.Lon, inc.Lat)
}

// VertexURL is a best-effort deep link into ASF Vertex (centered on the event, same date window).
func VertexURL(inc domain.Incident, windowDays int) string {
	w := time.Duration(windowDays) * 24 * time.Hour
	v := url.Values{}
	v.Set("zoom", "7")
	v.Set("center", fmt.Sprintf("%.4f,%.4f", inc.Lon, inc.Lat))
	v.Set("polygon", searchWKT(inc))
	v.Set("start", inc.StartTime.Add(-w).UTC().Format(time.RFC3339))
	v.Set("end", inc.StartTime.Add(w).UTC().Format(time.RFC3339))
	return "https://search.asf.alaska.edu/#/?" + v.Encode()
}

func toDomainScene(mission string, sc asf.Scene) domain.SARScene {
	return domain.SARScene{
		GranuleID: sc.GranuleID, Mission: mission, Platform: sc.Platform, BeamMode: sc.BeamMode,
		Polarization: sc.Polarization, FlightDirection: sc.FlightDirection, Path: sc.Path, Frame: sc.Frame,
		StartTime: sc.StartTime, ProcessingLevel: sc.ProcessingLevel, BrowseURL: sc.BrowseURL,
		DownloadURL: sc.DownloadURL, SizeMB: math.Round(float64(sc.SizeBytes)/1e5) / 10, Footprint: sc.Footprint, // decimal MB, 1 decimal
	}
}

func nonNil(s []domain.SARScene) []domain.SARScene {
	if s == nil {
		return []domain.SARScene{}
	}
	return s
}
