// Package web embeds the Mini App static files (Phase 15, D-08) so they ship
// inside the single aggregator binary and are served by the webapp
// subcommand. The //go:embed directive must live in this directory: embed
// patterns cannot contain ".." and cannot reach outside the directory of the
// declaring file (same rule as migrations/embed.go).
package web

import "embed"

// FS holds the static/ tree (index.html, app.css, ES modules) embedded at
// build time; internal/webapp serves its "static" subdirectory at "/".
//
//go:embed static
var FS embed.FS
