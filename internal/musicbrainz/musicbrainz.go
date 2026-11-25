package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/utils"
)

const DefaultBaseURL = "https://musicbrainz.org/ws/2"

type Config struct {
	BaseURL    string
	UserAgent  string
	MaxRetries int           // default 3
	Timeout    time.Duration // default 15s
	Logger     *utils.Logger // optional; retries log at Warn, success stays silent
}

type Client struct {
	baseURL    string
	userAgent  string
	maxRetries int
	http       *http.Client
	limiter    *rate.Limiter
	log        *utils.Logger
}

// New validates config defaults and returns a rate-limited MusicBrainz client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.UserAgent) == "" {
		return nil, fmt.Errorf("musicbrainz: UserAgent is required (app name/version + contact)")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}

	return &Client{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		userAgent:  cfg.UserAgent,
		maxRetries: cfg.MaxRetries,
		http:       &http.Client{Timeout: cfg.Timeout},
		log:        cfg.Logger,
		// 1.1s instead of 1s: a small margin so clock jitter never pushes
		// us over the limit, which would get the whole IP throttled.
		limiter: rate.NewLimiter(rate.Every(1100*time.Millisecond), 1),
	}, nil
}

// getJSON GETs path with fmt=json, enforcing the 1.1s rate limit and 3
// retries with backoff for 429/503/5xx. 404 maps to ErrNotFound; 4xx fails fast.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	if query == nil {
		query = url.Values{}
	}
	query.Set("fmt", "json")
	fullURL := c.baseURL + path + "?" + query.Encode()

	var lastErr error
	for attempt := range c.maxRetries {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("musicbrainz: rate limiter: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return fmt.Errorf("musicbrainz: build request: %w", err)
		}
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("musicbrainz: request: %w", err)
		} else {
			switch {
			case resp.StatusCode == http.StatusOK:
				decodeErr := json.NewDecoder(resp.Body).Decode(out)
				_ = resp.Body.Close()
				if decodeErr != nil {
					return fmt.Errorf("musicbrainz: decode response: %w", decodeErr)
				}
				return nil
			case resp.StatusCode == http.StatusNotFound:
				_ = resp.Body.Close()
				return apperrors.ErrNotFound
			case resp.StatusCode == http.StatusTooManyRequests ||
				resp.StatusCode == http.StatusServiceUnavailable ||
				resp.StatusCode >= 500:
				lastErr = fmt.Errorf("musicbrainz: transient status %d", resp.StatusCode)
				wait := backoffDelay(attempt, resp.Header.Get("Retry-After"))
				_ = resp.Body.Close()
				c.warnf("musicbrainz transient status %d %s (attempt %d/%d, retry in %s)",
					resp.StatusCode, path, attempt+1, c.maxRetries, wait)
				if !sleepOrDone(ctx, wait) {
					return fmt.Errorf("musicbrainz: %w", ctx.Err())
				}
				continue
			case resp.StatusCode >= 400:
				_ = resp.Body.Close()
				return fmt.Errorf("musicbrainz: status %d for %s", resp.StatusCode, path)
			}
			_ = resp.Body.Close()
		}

		if attempt == c.maxRetries-1 {
			break
		}
		if ctx.Err() != nil {
			return fmt.Errorf("musicbrainz: %w", ctx.Err())
		}
		wait := backoffDelay(attempt, "")
		c.warnf("musicbrainz request %s failed (attempt %d/%d, retry in %s): %v",
			path, attempt+1, c.maxRetries, wait, lastErr)
		if !sleepOrDone(ctx, wait) {
			return fmt.Errorf("musicbrainz: %w", ctx.Err())
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("musicbrainz: request failed after %d attempts", c.maxRetries)
}

func (c *Client) warnf(format string, args ...any) {
	if c.log != nil {
		c.log.Warnf(format, args...)
	}
}

// backoffDelay returns 1s, 2s, 4s, ... capped at 30s, or the Retry-After
// value when it parses.
func backoffDelay(attempt int, retryAfter string) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && s >= 0 {
		if s > 30 {
			return 30 * time.Second
		}
		return time.Duration(s) * time.Second
	}
	if t, err := time.Parse(http.TimeFormat, strings.TrimSpace(retryAfter)); err == nil {
		if d := time.Until(t); d > 0 && d <= 30*time.Second {
			return d
		}
	}
	d := time.Second << attempt
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

// sleepOrDone sleeps d unless ctx is canceled, reporting whether the sleep ran.
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
