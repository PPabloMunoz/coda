package server

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	webui "github.com/ppablomunoz/coda/web"
)

// mountWeb serves the embedded Astro build (web/dist) on GET/HEAD /.
// API routes are registered before this catch-all, and Go's ServeMux
// prefers the more specific /api/* patterns, so this never shadows them.
func mountWeb(r *gin.Engine) {
	sub, err := fs.Sub(webui.Dist, "dist")
	if err != nil {
		r.GET("/", func(c *gin.Context) {
			c.JSON(http.StatusNotFound, gin.H{"error": "web build missing"})
		})
		return
	}

	r.StaticFS("/", http.FS(sub))

	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		// StaticFS's /*filepath already matched non-API misses and 404'd
		// inside the file server; this only fires if StaticFS is replaced
		// by explicit file routes. Keep for clean API JSON 404s.
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})
}
