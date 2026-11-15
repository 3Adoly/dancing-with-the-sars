package main

import (
	"context"
	"fmt"
	"io"

	"github.com/dwts/backend/internal/config"
	"github.com/dwts/backend/internal/stories"
)

// prepareStoriesValidate parses `ingest stories-validate` (it takes no flags
// besides -h). The directory comes from STORIES_DIR. The command never touches
// the database, so it works with DATABASE_URL empty.
func prepareStoriesValidate(args []string, stderr io.Writer) (action, error) {
	return noFlags("stories-validate",
		"validate STORIES_DIR/manifest.json (default ./data/stories) against every story rule.\n"+
			"Prints \"N stories OK\", or each broken rule and exits 1.",
		cmdStoriesValidate(stderr))(args, stderr)
}

// cmdStoriesValidate loads the manifest and validates it, writing the verdict
// to out. A parse error or any validation error makes it return an error
// (exit 1).
func cmdStoriesValidate(out io.Writer) action {
	return func(_ context.Context, cfg config.Config) error {
		m, err := stories.LoadRaw(cfg.StoriesDir)
		if err != nil {
			return err
		}
		if errs := stories.Validate(cfg.StoriesDir, m); len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintln(out, e)
			}
			return fmt.Errorf("%s: %d validation error(s)", cfg.StoriesDir, len(errs))
		}
		noun := "stories"
		if len(m.Stories) == 1 {
			noun = "story"
		}
		fmt.Fprintf(out, "%d %s OK\n", len(m.Stories), noun)
		return nil
	}
}
