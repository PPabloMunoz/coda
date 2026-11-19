// Package web embeds the Astro production build (web/dist) so the Go
// binary serves the UI without extra processes.
package web

import "embed"

// Dist holds the contents of web/dist (built by `just web-build`).
//
//go:embed all:dist
var Dist embed.FS
