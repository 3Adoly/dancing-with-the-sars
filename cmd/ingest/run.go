package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dwts/backend/internal/config"
	"github.com/dwts/backend/internal/ingest"
	"github.com/dwts/backend/internal/ingest/fetch"
	"github.com/dwts/backend/internal/ingest/sources"
	"github.com/dwts/backend/internal/platform"
	"github.com/dwts/backend/internal/store/postgres"
)

// sourceDeps is what a registry entry may use to build its adapter.
type sourceDeps struct {
	F   *fetch.Fetcher
	Cfg config.Config
	Log *slog.Logger
	// ISO2 maps countries.iso2 → iso3, loaded from the countries table at
	// the start of `ingest run` (glc resolves its ISO2 country_code with it).
	ISO2 map[string]string
	// Names maps countries.name → iso3, loaded with ISO2 (dfo resolves its
	// country names with it).
	Names map[string]string
}

// registry maps every --source name to its adapter constructor. The key must
// equal the adapter's Name() (a test checks it). Adding a source is one line.
var registry = map[string]func(sourceDeps) ingest.Source{
	"usgs":    func(d sourceDeps) ingest.Source { return sources.NewUSGS(d.F, d.Cfg.USGSMinMag, d.Log) },
	"noaa_eq": func(d sourceDeps) ingest.Source { return sources.NewNOAAEarthquakes(d.F, d.Log) },
	"noaa_ts": func(d sourceDeps) ingest.Source { return sources.NewNOAATsunamis(d.F, d.Log) },
	"noaa_vo": func(d sourceDeps) ingest.Source { return sources.NewNOAAVolcanoes(d.F, d.Log) },
	"eonet":   func(d sourceDeps) ingest.Source { return sources.NewEONET(d.F, d.Log) },
	"ibtracs": func(d sourceDeps) ingest.Source { return sources.NewIBTrACS(d.F, d.Cfg.MinYear, d.Log) },
	"glc":     func(d sourceDeps) ingest.Source { return sources.NewGLC(d.F, d.ISO2, d.Log) },
	"dfo":     func(d sourceDeps) ingest.Source { return sources.NewDFO(d.F, d.Names, d.Log) },
	"gdacs":   func(d sourceDeps) ingest.Source { return sources.NewGDACS(d.F, d.Log) },
	"gvp":     func(d sourceDeps) ingest.Source { return sources.NewGVP(d.F, d.Log) },
}

// registryNames returns the registered source names, sorted: the order --all runs them in.
func registryNames() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// runOpts are the parsed flags of `ingest run`.
type runOpts struct {
	sources          []string // in run order, de-duplicated; every registered name for --all
	all              bool
	fromYear, toYear int           // inclusive UTC years; 0 = unset
	since            time.Duration // > 0: window is [now-since, now]
	refresh          bool
}

const runDesc = `run source adapters sequentially, then run "ingest postprocess" to publish the rows.

The window defaults to Jan 1 of MIN_YEAR until now. With --all, a source that
fails is logged and skipped; the command fails only when every source failed.
With --source, the first failure stops the run.`

// parseRunFlags parses `ingest run` flags. Errors were already printed to
// stderr; it returns flag.ErrHelp for -h or errUsage.
func parseRunFlags(args []string, stderr io.Writer) (runOpts, error) {
	var (
		o       runOpts
		srcList string
		since   string
	)
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&srcList, "source", "", "comma-separated sources to run (registered: "+strings.Join(registryNames(), ", ")+")")
	fs.BoolVar(&o.all, "all", false, "run every registered source")
	fs.IntVar(&o.fromYear, "from", 0, "first year (UTC, inclusive); default MIN_YEAR")
	fs.IntVar(&o.toYear, "to", 0, "last year (UTC, inclusive); default the current year, up to now")
	fs.StringVar(&since, "since", "", "only the recent past: a number of days like 30d, or a Go duration like 36h")
	fs.BoolVar(&o.refresh, "refresh", false, "ignore the download cache and fetch again")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: ingest run --source a,b | --all [--from YEAR] [--to YEAR] [--since 30d] [--refresh]\n\n%s\n\nflags:\n", runDesc)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return runOpts{}, err
	}
	fail := func(format string, a ...any) (runOpts, error) {
		fmt.Fprintf(stderr, "ingest run: "+format+"\n", a...)
		fs.Usage()
		return runOpts{}, errUsage
	}
	if fs.NArg() > 0 {
		return fail("unexpected arguments %q", fs.Args())
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	for _, n := range strings.Split(srcList, ",") {
		n = strings.TrimSpace(n)
		if n == "" || slices.Contains(o.sources, n) {
			continue
		}
		if !isSource(n) {
			return fail("unknown source %q (registered: %s)", n, strings.Join(registryNames(), ", "))
		}
		o.sources = append(o.sources, n)
	}
	if o.all == (len(o.sources) > 0) {
		return fail("give exactly one of --source or --all")
	}
	if o.all {
		o.sources = registryNames()
	}

	for _, y := range []struct {
		name string
		v    int
	}{{"from", o.fromYear}, {"to", o.toYear}} {
		if set[y.name] && (y.v < 1 || y.v > 9999) {
			return fail("--%s %d: out of range 1..9999", y.name, y.v)
		}
	}
	if o.fromYear > 0 && o.toYear > 0 && o.fromYear > o.toYear {
		return fail("--from %d is after --to %d", o.fromYear, o.toYear)
	}
	if set["since"] {
		if set["from"] || set["to"] {
			return fail("--since cannot be combined with --from or --to")
		}
		d, ok := parseSince(since)
		if !ok {
			return fail("--since %q: want a positive number of days like 30d or a Go duration like 36h", since)
		}
		o.since = d
	}
	return o, nil
}

// parseSince accepts "<n>d" (days) or a time.ParseDuration string; the
// result must be positive.
func parseSince(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n > 1e5 {
			return 0, false
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, false
		}
	}
	return d, d > 0
}

// runWindow turns the flags into the fetch window: [now-since, now], or
// Jan 1 of max(--from, minYear) until Jan 1 after --to, never past now.
func runWindow(o runOpts, minYear int, now time.Time) ingest.Window {
	now = now.UTC()
	if o.since > 0 {
		return ingest.Window{From: now.Add(-o.since), To: now}
	}
	from := max(o.fromYear, minYear)
	w := ingest.Window{From: time.Date(from, 1, 1, 0, 0, 0, 0, time.UTC), To: now}
	if o.toYear > 0 {
		if end := time.Date(o.toYear+1, 1, 1, 0, 0, 0, 0, time.UTC); end.Before(now) {
			w.To = end
		}
	}
	return w
}

// sourceRunner is the part of ingest.Runner runSources needs.
type sourceRunner interface {
	Run(ctx context.Context, s ingest.Source, w ingest.Window) (int, error)
}

// runSources runs names in order. For --all (all=true) a failed source is
// logged and skipped and the result is an error only when every source
// failed; otherwise the first failure is returned. A cancelled ctx stops
// either way.
func runSources(ctx context.Context, names []string, all bool, build func(string) ingest.Source,
	r sourceRunner, w ingest.Window, log *slog.Logger) error {
	var failed []string
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := time.Now()
		rows, err := r.Run(ctx, build(name), w)
		if err != nil {
			if !all || ctx.Err() != nil {
				return err
			}
			log.Error("source failed, skipping it", "source", name, "err", err)
			failed = append(failed, name)
			continue
		}
		log.Info("source done", "source", name, "rows", rows, "dur", time.Since(start).Round(time.Second))
	}
	if len(failed) > 0 && len(failed) == len(names) {
		return fmt.Errorf("all %d sources failed: %s", len(names), strings.Join(failed, ", "))
	}
	if len(failed) > 0 {
		log.Warn("some sources failed", "failed", strings.Join(failed, ","), "ok", len(names)-len(failed))
	}
	return nil
}

// prepareRun parses `ingest run`.
func prepareRun(args []string, stderr io.Writer) (action, error) {
	o, err := parseRunFlags(args, stderr)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, cfg config.Config) error { return cmdRun(ctx, cfg, o) }, nil
}

// cmdRun wires the real fetcher, database and runner and runs the sources.
func cmdRun(ctx context.Context, cfg config.Config, o runOpts) error {
	if cfg.DatabaseURL == "" {
		return errNoDatabase
	}
	log := slog.Default()
	pg, err := postgres.New(ctx, cfg.DatabaseURL, cfg.DBMaxConns, log)
	if err != nil {
		return err
	}
	defer pg.Close()

	f := fetch.New(platform.NewHTTPClient(5*time.Minute), cfg.RawDir, log)
	f.Refresh = o.refresh
	if cfg.FIRMSMapKey != "" {
		f.Secrets = []string{cfg.FIRMSMapKey}
	}
	iso2, err := pg.CountryISO2Map(ctx)
	if err != nil {
		return err
	}
	names, err := pg.CountryNameMap(ctx)
	if err != nil {
		return err
	}
	deps := sourceDeps{F: f, Cfg: cfg, Log: log, ISO2: iso2, Names: names}
	build := func(name string) ingest.Source { return buildSource(name, deps) }
	runner := dispatchRunner{&ingest.Runner{W: pg, Log: log, MinYear: cfg.MinYear}}

	w := runWindow(o, cfg.MinYear, time.Now())
	if !w.From.Before(w.To) {
		return errors.New("ingest run: the window is empty (check --from/--to against MIN_YEAR and today)")
	}
	log.Info("ingest run", "sources", strings.Join(o.sources, ","), "from", w.From, "to", w.To, "refresh", o.refresh, "iso2_codes", len(iso2), "country_names", len(names))
	return runSources(ctx, o.sources, o.all, build, runner, w, log)
}
