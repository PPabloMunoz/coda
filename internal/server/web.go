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

// mountWeb serves the embedded Astro build (web/dist).
// API routes are registered before this and take precedence; unknown
// /api/* paths get a JSON 404 from NoRoute below, never UI content.
func mountWeb(r *gin.Engine) {
	sub, err := fs.Sub(webui.Dist, "dist")
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
		// Real file (/_astro/…, /favicon.ico, …)? Serve it.
		if trimmed := strings.TrimPrefix(path.Clean("/"+p), "/"); trimmed != "" {
			if f, statErr := fs.Stat(sub, trimmed); statErr == nil {
				if !f.IsDir() {
					serveFSFile(c, sub, trimmed)
					return
				}
				// Prerendered Astro dir (e.g. /artists/) → its index.
				if _, indexErr := fs.Stat(sub, trimmed+"/index.html"); indexErr == nil {
					serveFSFile(c, sub, trimmed+"/index.html")
					return
				}
			}
		}
		// SPA fallback: unknown UI path → index.html.
		// (If Astro has no client-side routes, swap this for a JSON 404.)
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
	c.Data(http.StatusOK, ctype, data)
}
