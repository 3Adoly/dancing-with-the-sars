// Command ingest loads, normalizes and post-processes hazard data into Postgres.
//
// Usage: ingest <migrate|boundaries|run|postprocess|stories-validate|schedule> [flags]
//
// Each subcommand parses its own flags with the stdlib flag package before
// anything reads the config or touches the database. Subcommands that are not
// implemented yet print "not implemented" and exit 2.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/dwts/backend/internal/config"
	"github.com/dwts/backend/internal/platform"
	"github.com/dwts/backend/internal/store/postgres"
	"github.com/dwts/backend/migrations"
)

const usage = `usage: ingest <command> [flags]

commands:
  migrate           apply the embedded SQL migrations (idempotent)
  boundaries        load country boundaries
  run               run source adapters
  postprocess       assign countries, dedup, refresh aggregates
  stories-validate  validate data/stories
  schedule          run adapters on a schedule

Run "ingest <command> -h" for a command's flags.
`

// action is the work of a subcommand once its flags are parsed and the
// config is loaded.
type action func(ctx context.Context, cfg config.Config) error

// command is one ingest subcommand. prepare gets the arguments after the
// subcommand name and parses them with a flag.FlagSet. It runs before the
// config is loaded or the database is touched, so bad flags never reach the
// DB. It returns the action to run, or an error (flag.ErrHelp for -h/--help).
// A nil prepare means "not implemented yet".
type command struct {
	name    string
	prepare func(args []string, stderr io.Writer) (action, error)
}

// errUsage marks a usage error whose message prepare already printed.
var errUsage = errors.New("usage error")

// commands is the dispatch table, in help order. Later milestones fill in schedule.
var commands = []command{
	{name: "migrate", prepare: prepareMigrate},
	{name: "boundaries", prepare: prepareBoundaries},
	{name: "run", prepare: prepareRun},
	{name: "postprocess", prepare: preparePostprocess},
	{name: "stories-validate", prepare: prepareStoriesValidate},
	{name: "schedule"},
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stderr, config.Load)
	stop()
	os.Exit(code)
}

// run dispatches args to a subcommand and returns the process exit code:
// 0 ok (also for help: "ingest help" and "ingest <command> -h"), 1 runtime
// error, 2 usage error (unknown command, bad flag or extra argument) or
// not-implemented subcommand.
func run(ctx context.Context, args []string, stderr io.Writer, load func() (config.Config, error)) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	name := args[0]
	if name == "-h" || name == "--help" || name == "help" {
		fmt.Fprint(stderr, usage)
		return 0
	}
	var cmd *command
	for i := range commands {
		if commands[i].name == name {
			cmd = &commands[i]
			break
		}
	}
	if cmd == nil {
		fmt.Fprintf(stderr, "ingest: unknown command %q\n\n%s", name, usage)
		return 2
	}
	if cmd.prepare == nil {
		fmt.Fprintf(stderr, "ingest %s: not implemented\n", name)
		return 2
	}
	act, err := cmd.prepare(args[1:], stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0 // the flag package already printed the usage
		}
		return 2 // flag errors and usage were already printed
	}

	cfg, err := load()
	if err != nil {
		fmt.Fprintln(stderr, "ingest:", err)
		return 1
	}
	log := platform.NewLoggerTo(stderr, cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(log)
	if err := act(ctx, cfg); err != nil {
		log.Error("ingest failed", "command", name, "err", err)
		return 1
	}
	return 0
}

// errNoDatabase is returned by subcommands that need Postgres when
// DATABASE_URL is empty (config.Validate allows that on purpose).
var errNoDatabase = errors.New("DATABASE_URL is not set (copy .env.example to .env, or export it)")

// noFlags builds the prepare function of a subcommand that takes no flags
// besides -h: name is the subcommand, desc the help text under its usage line.
func noFlags(name, desc string, act action) func([]string, io.Writer) (action, error) {
	return func(args []string, stderr io.Writer) (action, error) {
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		fs.SetOutput(stderr)
		fs.Usage = func() {
			fmt.Fprintf(stderr, "usage: ingest %s\n\n%s\n", name, desc)
		}
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() > 0 {
			fmt.Fprintf(stderr, "ingest %s: unexpected arguments %q\n", name, fs.Args())
			fs.Usage()
			return nil, errUsage
		}
		return act, nil
	}
}

// prepareMigrate parses `ingest migrate` (it takes no flags besides -h).
func prepareMigrate(args []string, stderr io.Writer) (action, error) {
	return noFlags("migrate", "apply every pending embedded SQL migration; a second run is a no-op.", cmdMigrate)(args, stderr)
}

// cmdMigrate applies every pending goose migration from migrations.FS.
// Running it again with nothing pending is a no-op.
func cmdMigrate(ctx context.Context, cfg config.Config) error {
	if cfg.DatabaseURL == "" {
		return errNoDatabase
	}
	pg, err := postgres.New(ctx, cfg.DatabaseURL, cfg.DBMaxConns, slog.Default())
	if err != nil {
		return err
	}
	defer pg.Close()

	goose.SetLogger(gooseLogger{pg.Log})
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	db := stdlib.OpenDBFromPool(pg.Pool)
	defer db.Close()
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// gooseLogger routes goose's output through slog.
type gooseLogger struct{ log *slog.Logger }

func (g gooseLogger) Printf(format string, v ...any) {
	g.log.Info(strings.TrimRight(fmt.Sprintf(format, v...), "\n"))
}

func (g gooseLogger) Fatalf(format string, v ...any) {
	g.log.Error(strings.TrimRight(fmt.Sprintf(format, v...), "\n"))
	os.Exit(1)
}

// preparePostprocess parses `ingest postprocess` (it takes no flags besides -h).
func preparePostprocess(args []string, stderr io.Writer) (action, error) {
	return noFlags("postprocess",
		"assign countries, rebuild incident_countries, refresh the aggregates and bump the dataset version.\n"+
			"Every step is idempotent; run it after any load.",
		cmdPostprocess)(args, stderr)
}

// cmdPostprocess runs every post-processing step (Plan_Detailed_Level.md §6).
func cmdPostprocess(ctx context.Context, cfg config.Config) error {
	if cfg.DatabaseURL == "" {
		return errNoDatabase
	}
	pg, err := postgres.New(ctx, cfg.DatabaseURL, cfg.DBMaxConns, slog.Default())
	if err != nil {
		return err
	}
	defer pg.Close()
	return pg.Postprocess(ctx)
}
