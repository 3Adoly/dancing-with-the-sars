// Package asf is a small client for the ASF DAAC Search API
// (https://api.daac.asf.alaska.edu/services/search/param). Searching needs no login.
// Rule R5: leaf package with its own DTOs; it never imports domain or service.
package asf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/dwts/backend/internal/platform"
)

// DefaultBaseURL is the ASF search endpoint (verified 2026-11-15).
const DefaultBaseURL = "https://api.daac.asf.alaska.edu/services/search/param"

// maxBody caps the response size we read (250 GeoJSON features are well under 2 MB).
const maxBody = 20 << 20

// Typed errors. Use errors.Is.
var (
	ErrRateLimited = errors.New("asf: rate limited (HTTP 429)")
	ErrUpstream    = errors.New("asf: upstream error")
	ErrTimeout     = errors.New("asf: timeout")
	ErrBadResponse = errors.New("asf: unparseable response")
)

// HTTPError carries the status code of a non-200 answer. It wraps ErrRateLimited or ErrUpstream.
type HTTPError struct {
	Status int
	Body   string // first 300 bytes, for logs
	kind   error
}

func (e *HTTPError) Error() string { return fmt.Sprintf("asf: HTTP %d: %s", e.Status, e.Body) }
func (e *HTTPError) Unwrap() error { return e.kind }

// Query is one search. Params carries the mission-specific keywords, e.g.
// {"dataset":"NISAR","processingLevel":"GCOV"} or {"platform":"SENTINEL-1","processingLevel":"GRD_HD"}.
type Query struct {
	Params     map[string]string
	WKT        string // intersectsWith, e.g. "POINT(37.0143 37.2256)"
	Start, End time.Time
	MaxResults int // default 250
}

// Scene is one granule as returned by ASF (our own DTO).
type Scene struct {
	GranuleID       string
	Platform        string
	BeamMode        string
	Polarization    string
	FlightDirection string
	Path            int
	Frame           int
	StartTime       time.Time
	ProcessingLevel string
	BrowseURL       string
	DownloadURL     string
	SizeBytes       int64
	Footprint       json.RawMessage // GeoJSON geometry
}

// Client calls the ASF Search API.
type Client struct {
	base    string
	timeout time.Duration
	hc      *http.Client
}

// New creates a client. An empty baseURL uses DefaultBaseURL; a nil hc uses platform.NewHTTPClient.
func New(baseURL string, timeout time.Duration, hc *http.Client) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	if hc == nil {
		hc = platform.NewHTTPClient(timeout + 2*time.Second)
	}
	return &Client{base: baseURL, timeout: timeout, hc: hc}
}

// URL builds the search URL for q (also handy for logs and debugging).
func (c *Client) URL(q Query) string {
	v := url.Values{}
	for k, val := range q.Params {
		v.Set(k, val)
	}
	if q.WKT != "" {
		v.Set("intersectsWith", q.WKT)
	}
	if !q.Start.IsZero() {
		v.Set("start", q.Start.UTC().Format(time.RFC3339))
	}
	if !q.End.IsZero() {
		v.Set("end", q.End.UTC().Format(time.RFC3339))
	}
	n := q.MaxResults
	if n <= 0 {
		n = 250
	}
	v.Set("maxResults", fmt.Sprint(n))
	v.Set("output", "geojson")
	return c.base + "?" + v.Encode()
}

// Search runs q and returns the parsed scenes (in ASF order).
func (c *Client) Search(ctx context.Context, q Query) ([]Scene, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL(q), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/geo+json, application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil || isTimeout(err) {
			return nil, fmt.Errorf("%w: %v", ErrTimeout, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, maxBody)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(body, 300))
		kind := ErrUpstream
		if resp.StatusCode == http.StatusTooManyRequests {
			kind = ErrRateLimited
		}
		return nil, &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(b)), kind: kind}
	}
	scenes, err := Parse(body)
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	return scenes, err
}

func isTimeout(err error) bool {
	var te interface{ Timeout() bool }
	return errors.As(err, &te) && te.Timeout()
}

// --- GeoJSON parsing (property names verified against live responses 2026-11-15) ---

type featureCollection struct {
	Features []struct {
		Geometry   json.RawMessage `json:"geometry"`
		Properties properties      `json:"properties"`
	} `json:"features"`
}

type properties struct {
	SceneName            string          `json:"sceneName"`
	FileID               string          `json:"fileID"`
	Platform             string          `json:"platform"`
	BeamModeType         *string         `json:"beamModeType"`
	Polarization         *string         `json:"polarization"`         // null for NISAR
	MainBandPolarization []string        `json:"mainBandPolarization"` // NISAR, e.g. ["HH","HV"]
	FlightDirection      string          `json:"flightDirection"`
	PathNumber           *int            `json:"pathNumber"`
	FrameNumber          *int            `json:"frameNumber"`
	ProcessingLevel      *string         `json:"processingLevel"` // null for SEASAT
	StartTime            string          `json:"startTime"`
	URL                  string          `json:"url"`
	Browse               []string        `json:"browse"`
	Bytes                json.RawMessage `json:"bytes"` // number (S1, ALOS) or {file: {bytes, format}} (NISAR, SEASAT)
}

// Parse decodes an ASF GeoJSON FeatureCollection.
func Parse(r io.Reader) ([]Scene, error) {
	var fc featureCollection
	if err := json.NewDecoder(r).Decode(&fc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadResponse, err)
	}
	out := make([]Scene, 0, len(fc.Features))
	for _, f := range fc.Features {
		p := f.Properties
		id := p.SceneName
		if id == "" {
			id = p.FileID
		}
		t, err := time.Parse(time.RFC3339, p.StartTime)
		if id == "" || err != nil {
			continue // unusable feature; skip rather than fail the whole search
		}
		out = append(out, Scene{
			GranuleID:       id,
			Platform:        p.Platform,
			BeamMode:        deref(p.BeamModeType),
			Polarization:    polarization(p),
			FlightDirection: p.FlightDirection,
			Path:            derefInt(p.PathNumber),
			Frame:           derefInt(p.FrameNumber),
			StartTime:       t.UTC(),
			ProcessingLevel: deref(p.ProcessingLevel),
			BrowseURL:       pickBrowse(p.Browse),
			DownloadURL:     p.URL,
			SizeBytes:       sizeBytes(p.Bytes, p.URL),
			Footprint:       f.Geometry,
		})
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

func polarization(p properties) string {
	if s := deref(p.Polarization); s != "" {
		return s
	}
	return strings.Join(p.MainBandPolarization, "+")
}

// pickBrowse prefers the full browse image over a "_thumbnail"/"_THUMBNAIL" variant.
func pickBrowse(urls []string) string {
	for _, u := range urls {
		if !strings.Contains(strings.ToLower(u), "thumbnail") {
			return u
		}
	}
	if len(urls) > 0 {
		return urls[0]
	}
	return ""
}

// sizeBytes reads "bytes" as a number, or as a per-file object (then the entry matching the
// download URL's file name, else the largest entry).
func sizeBytes(raw json.RawMessage, downloadURL string) int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return int64(n)
	}
	var files map[string]struct {
		Bytes int64 `json:"bytes"`
	}
	if json.Unmarshal(raw, &files) != nil {
		return 0
	}
	if f, ok := files[path.Base(downloadURL)]; ok && downloadURL != "" {
		return f.Bytes
	}
	var maxB int64
	for _, f := range files {
		maxB = max(maxB, f.Bytes)
	}
	return maxB
}
