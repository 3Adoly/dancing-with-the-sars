// Package migrations embeds the goose SQL migrations (Detailed §5).
package migrations

import "embed"

// FS holds 0001_*.sql ... 0006_*.sql at its root; pass "." as the goose directory.
//
//go:embed *.sql
var FS embed.FS
