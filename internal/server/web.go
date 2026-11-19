package server

import (
	"io/fs"
	"net/http"

	webui "github.com/ppablomunoz/coda/web"
)

// mountWeb serves the embedded Astro build (web/dist) on GET/HEAD /.
// API routes are registered before this catch-all, and Go's ServeMux
// prefers the more specific /api/* patterns, so this never shadows them.
func mountWeb(mux *http.ServeMux) {
	sub, err := fs.Sub(webui.Dist, "dist")
	if err != nil {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "web build missing")
		})
		return
	}

	files := http.FileServer(http.FS(sub))
	// Methodless "/" is the fallback: the more specific "GET /api/*"
	// patterns above still win. (Separate "GET /" + "HEAD /" entries
	// conflict with the GET-only API routes at registration time.)
	mux.Handle("/", files)
}
