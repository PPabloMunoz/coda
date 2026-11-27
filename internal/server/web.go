package server

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	webui "github.com/ppablomunoz/coda/web"
)

// pageRoutes maps UI paths to their committed static files. Query strings
// (e.g. /artist?id=…) don't affect file serving — only the path matters.
var pageRoutes = map[string]string{
	"/":              "index.html",
	"/artists":       "artists.html",
	"/artist":        "artist.html",
	"/release-group": "release-group.html",
	"/link":          "link.html",
	"/files":         "files.html",
	"/file":          "file.html",
	"/removed":       "removed.html",
}

// mountWeb serves the embedded vanilla UI (web/static).
// API routes are registered before this and take precedence; unknown
// /api/* paths get a JSON 404 from NoRoute below, never UI content.
func mountWeb(r *gin.Engine) {
	sub, err := fs.Sub(webui.Static, "static")
	if err != nil {
		r.GET("/", func(c *gin.Context) {
			c.JSON(http.StatusNotFound, gin.H{"error": "web build missing"})
		})
		r.NoRoute(func(c *gin.Context) {
			c.JSON(http.StatusNotFound, gin.H{"error": "web build missing"})
		})
		return
	}
	// Exact file, no wildcard — never conflicts with the radix tree.
	// NOTE: gin's c.FileFromFS must not be used with "index.html": it
	// delegates to http.FileServer, which 301-redirects any */index.html
	// path to its directory — a self-redirect loop on "/".
	r.GET("/", func(c *gin.Context) { serveFSFile(c, sub, "index.html") })
	r.HEAD("/", func(c *gin.Context) { serveFSFile(c, sub, "index.html") })

	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		clean := path.Clean("/" + p)
		// UI page (Clean already strips the trailing slash: /artist/ → artist.html).
		if file, ok := pageRoutes[clean]; ok {
			serveFSFile(c, sub, file)
			return
		}
		// Real file (/css/…, /js/…, /logo.png, …)? Serve it.
		if trimmed := strings.TrimPrefix(clean, "/"); trimmed != "" {
			if f, statErr := fs.Stat(sub, trimmed); statErr == nil && !f.IsDir() {
				serveFSFile(c, sub, trimmed)
				return
			}
		}
		// Unknown UI path → home. (There are no client-side routes; every
		// page above is a real file, so this only fires on stale links.)
		serveFSFile(c, sub, "index.html")
	})
}

// serveFSFile writes an embedded file with a sensible content type.
// It reads bytes directly instead of gin's FileFromFS so FileServer's
// /index.html → ./ canonical redirect can never fire.
func serveFSFile(c *gin.Context, sub fs.FS, name string) {
	data, err := fs.ReadFile(sub, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	} else if strings.HasPrefix(ctype, "text/") && !strings.Contains(ctype, "charset") {
		ctype += "; charset=utf-8"
	}
	// ES modules need an explicit JS content type on some platforms.
	if strings.HasSuffix(name, ".js") && !strings.Contains(ctype, "javascript") {
		ctype = "text/javascript; charset=utf-8"
	}
	c.Data(http.StatusOK, ctype, data)
}
