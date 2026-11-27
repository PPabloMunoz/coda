package server

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// apiMaxBodyBytes caps JSON API request bodies at 1 MiB. Cover uploads
// (PUT /releases/:id/cover, PUT /artists/:id/cover) are exempt and keep
// their own 15 MiB limit in readCoverUpload.
const apiMaxBodyBytes = 1 << 20

// contentSecurityPolicy locks the embedded UI to same-origin resources.
// Scripts and styles are external files under /js and /css (no inline
// code), images are same-origin or data: URLs (CSS masks), and API calls
// are same-origin fetches.
const contentSecurityPolicy = "default-src 'none'; base-uri 'none'; " +
	"form-action 'self'; frame-ancestors 'none'; object-src 'none'; " +
	"script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; font-src 'self'"

// Options carries hardening configuration into NewHandler.
type Options struct {
	// AllowedHosts lists extra bare hostnames/IPs accepted by the Host
	// allowlist (normalized defensively here; validated in config).
	AllowedHosts []string
	// ListenHost is the concrete configured listen host (may be empty).
	ListenHost string
}

// securityHeaders sets cheap insurance headers on every response,
// including aborts from downstream middleware.
func (h *Handler) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.Writer.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		c.Next()
	}
}

// hostAllowlist rejects requests whose Host is not localhost, an IP
// literal, the configured listen host, or an explicitly allowed name.
// This mitigates DNS rebinding against the no-auth design: an attacker's
// hostname can never be allowlisted by accident, while direct-IP access
// (Docker healthchecks, LAN IPs) keeps working. Rejections answer 421
// with the standard error envelope.
func (h *Handler) hostAllowlist() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.hostAllowed(c.Request.Host) {
			c.Next()
			return
		}
		h.log.Warnf("%s %s -> INVALID_HOST: host=%q",
			c.Request.Method, c.FullPath(), c.Request.Host)
		c.AbortWithStatusJSON(http.StatusMisdirectedRequest,
			gin.H{"error": apperrors.ErrInvalidHost.Message, "code": apperrors.ErrInvalidHost.Code})
	}
}

// apiBodyLimit caps API request bodies without buffering the whole body:
// a declared Content-Length over the limit is rejected before reading,
// and chunked bodies are cut off by MaxBytesReader during the read.
// Cover upload PUTs are exempt (they enforce their own 15 MiB limit).
func (h *Handler) apiBodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if !strings.HasPrefix(p, "/api/") {
			c.Next()
			return
		}
		if c.Request.Method == http.MethodPut && isCoverUploadPath(p) {
			c.Next()
			return
		}
		if c.Request.ContentLength > apiMaxBodyBytes {
			h.writeAppError(c, apperrors.ErrPayloadTooLarge)
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, apiMaxBodyBytes)
		}
		c.Next()
	}
}

// isCoverUploadPath reports the two raw-image upload routes, which enforce
// their own 15 MiB limit in readCoverUpload.
func isCoverUploadPath(p string) bool {
	if strings.HasPrefix(p, "/api/v1/releases/") && strings.HasSuffix(p, "/cover") {
		return true
	}
	if strings.HasPrefix(p, "/api/v1/artists/") && strings.HasSuffix(p, "/cover") {
		return true
	}
	return false
}

// hostAllowed checks a Host header value (with or without port) against
// localhost, IP literals, the listen host, and the configured allowlist.
func (h *Handler) hostAllowed(host string) bool {
	cand := normalizeRequestHost(host)
	if cand == "" {
		// No Host (HTTP/1.0): nothing to validate.
		return true
	}
	if cand == "localhost" {
		return true
	}
	// IP literals (v4/v6, optional zone) cannot be rebound via DNS.
	ipStr := cand
	if i := strings.LastIndex(ipStr, "%"); i != -1 {
		ipStr = ipStr[:i]
	}
	if net.ParseIP(ipStr) != nil {
		return true
	}
	if h.listenHost != "" && cand == h.listenHost {
		return true
	}
	_, ok := h.allowedHosts[cand]
	return ok
}

// normalizeRequestHost strips any port, lowercases, and drops a trailing
// dot so "Example.COM.:8687" and "example.com" compare equal.
func normalizeRequestHost(host string) string {
	v := strings.TrimSpace(host)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "[") {
		if i := strings.Index(v, "]"); i != -1 {
			v = v[1:i]
		}
	} else if h, _, err := net.SplitHostPort(v); err == nil {
		v = h
	}
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimSuffix(v, ".")
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") && len(v) >= 2 {
		v = strings.TrimSpace(v[1 : len(v)-1])
	}
	return v
}

// normalizeAllowlistEntry applies the same canonical form to configured
// names so file, env, and request values compare equal.
func normalizeAllowlistEntry(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	v = strings.TrimSuffix(v, ".")
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") && len(v) >= 2 {
		v = strings.TrimSpace(v[1 : len(v)-1])
		v = strings.ToLower(v)
		v = strings.TrimSuffix(v, ".")
	}
	return v
}
