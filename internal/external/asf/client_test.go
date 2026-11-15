package asf

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseRealSentinel1(t *testing.T) {
	scenes, err := Parse(strings.NewReader(string(fixture(t, "asf_sample.json"))))
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 20 {
		t.Fatalf("got %d scenes, want 20", len(scenes))
	}
	s := scenes[0]
	if !strings.HasPrefix(s.GranuleID, "S1") || !strings.HasPrefix(s.Platform, "Sentinel-1") || s.BeamMode != "IW" ||
		s.Polarization == "" || s.Path == 0 || s.Frame == 0 || s.ProcessingLevel != "GRD_HD" ||
		!strings.HasSuffix(s.BrowseURL, ".jpg") || !strings.HasSuffix(s.DownloadURL, ".zip") || s.SizeBytes < 1e8 ||
		s.StartTime.IsZero() || !strings.Contains(string(s.Footprint), "Polygon") {
		t.Errorf("bad first scene: %+v", s)
	}
}

func TestParseRealNISARAndSeasat(t *testing.T) {
	scenes, err := Parse(strings.NewReader(string(fixture(t, "asf_nisar_sample.json"))))
	if err != nil || len(scenes) == 0 {
		t.Fatalf("nisar: %v %d", err, len(scenes))
	}
	n := scenes[0]
	if n.Platform != "NISAR" || n.ProcessingLevel != "GCOV" || n.Polarization != "HH+HV" || n.BeamMode != "" ||
		n.SizeBytes < 1e9 || strings.Contains(n.BrowseURL, "thumbnail") || !strings.HasSuffix(n.BrowseURL, ".png") {
		t.Errorf("bad NISAR scene: %+v", n)
	}
	ss, err := Parse(strings.NewReader(string(fixture(t, "asf_seasat_sample.json"))))
	if err != nil || len(ss) != 2 {
		t.Fatalf("seasat: %v %d", err, len(ss))
	}
	if ss[0].ProcessingLevel != "" || ss[0].SizeBytes == 0 || ss[0].BrowseURL == "" || strings.Contains(ss[0].BrowseURL, "THUMBNAIL") {
		t.Errorf("bad Seasat scene: %+v", ss[0])
	}
}

func TestParseSkipsBadFeaturesAndRejectsGarbage(t *testing.T) {
	scenes, err := Parse(strings.NewReader(`{"features":[{"properties":{"sceneName":"","startTime":"x"}},{"properties":{"sceneName":"A","startTime":"2024-01-01T00:00:00Z","bytes":null}}]}`))
	if err != nil || len(scenes) != 1 || scenes[0].GranuleID != "A" {
		t.Errorf("got %+v %v", scenes, err)
	}
	if _, err := Parse(strings.NewReader("<html>")); !errors.Is(err, ErrBadResponse) {
		t.Errorf("garbage: %v", err)
	}
}

func TestSearchBuildsQueryAndParses(t *testing.T) {
	var got url.Values
	body := fixture(t, "asf_sample.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		if ua := r.Header.Get("User-Agent"); ua != "dwts-hackathon/1.0" {
			t.Errorf("user agent %q", ua)
		}
		w.Header().Set("Content-Type", "application/geo+json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := New(srv.URL, 2*time.Second, nil)
	start := time.Date(2022, 12, 23, 1, 17, 34, 0, time.UTC)
	scenes, err := c.Search(context.Background(), Query{
		Params: map[string]string{"platform": "SENTINEL-1", "processingLevel": "GRD_HD"},
		WKT:    "POINT(37.0143 37.2256)", Start: start, End: start.AddDate(0, 0, 90),
	})
	if err != nil || len(scenes) != 20 {
		t.Fatalf("search: %v %d", err, len(scenes))
	}
	want := map[string]string{"platform": "SENTINEL-1", "processingLevel": "GRD_HD", "intersectsWith": "POINT(37.0143 37.2256)",
		"start": "2022-12-23T01:17:34Z", "end": "2023-03-23T01:17:34Z", "output": "geojson", "maxResults": "250"}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("param %s=%q want %q", k, got.Get(k), v)
		}
	}
}

func TestSearchErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		delay  time.Duration
		want   error
	}{
		{"429", 429, 0, ErrRateLimited},
		{"503", 503, 0, ErrUpstream},
		{"400", 400, 0, ErrUpstream},
		{"timeout", 200, 300 * time.Millisecond, ErrTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.delay > 0 {
					select {
					case <-time.After(c.delay):
					case <-r.Context().Done():
						return
					}
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(`{"error":"x"}`))
			}))
			defer srv.Close()
			_, err := New(srv.URL, 100*time.Millisecond, nil).Search(context.Background(), Query{WKT: "POINT(0 0)"})
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
			var he *HTTPError
			if c.status != 200 && (!errors.As(err, &he) || he.Status != c.status) {
				t.Errorf("want HTTPError %d, got %v", c.status, err)
			}
		})
	}
}

// TestLiveASF hits the real API. Run with: ASF_LIVE=1 go test ./internal/external/asf -run Live -v
func TestLiveASF(t *testing.T) {
	if os.Getenv("ASF_LIVE") != "1" {
		t.Skip("set ASF_LIVE=1 to call the real ASF API")
	}
	c := New("", 15*time.Second, nil)
	ctx := context.Background()
	cases := []struct {
		name   string
		params map[string]string
		wkt    string
		t      time.Time
	}{
		{"S1 Turkiye 2023", map[string]string{"platform": "SENTINEL-1", "processingLevel": "GRD_HD"}, "POINT(37.0143 37.2256)", time.Date(2023, 2, 6, 1, 17, 34, 0, time.UTC)},
		{"NISAR Aomori 2025", map[string]string{"dataset": "NISAR", "processingLevel": "GCOV"}, "POINT(142.1836 40.9984)", time.Date(2025, 12, 8, 14, 15, 9, 0, time.UTC)},
		{"ALOS L1.5 Haiti 2010", map[string]string{"platform": "ALOS", "processingLevel": "L1.5"}, "POINT(-72.571 18.443)", time.Date(2010, 1, 12, 21, 53, 10, 0, time.UTC)},
	}
	for _, cs := range cases {
		scenes, err := c.Search(ctx, Query{Params: cs.params, WKT: cs.wkt, Start: cs.t.AddDate(0, 0, -45), End: cs.t.AddDate(0, 0, 45)})
		if err != nil {
			t.Errorf("%s: %v", cs.name, err)
			continue
		}
		t.Logf("%s: %d scenes", cs.name, len(scenes))
		if len(scenes) == 0 {
			t.Errorf("%s: expected scenes", cs.name)
		}
	}
}
