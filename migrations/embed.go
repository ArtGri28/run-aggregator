// Package migrations embeds the goose SQL migration files so they ship
// inside the single aggregator binary (FND-02). The //go:embed directive
// must live in this directory: embed patterns cannot contain ".." and
// cannot reach outside the directory of the declaring file
// (02-RESEARCH.md Pitfall 1).
package migrations

import "embed"

// FS holds every *.sql migration file embedded at build time.
//
//go:embed *.sql
var FS embed.FS
