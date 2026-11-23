// Package web embeds the vanilla static UI (web/static) so the Go
// binary serves it without extra processes.
package web

import "embed"

// Static holds the contents of web/static (committed plain HTML/CSS/JS).
//
//go:embed all:static
var Static embed.FS
