package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"

	"github.com/ppablomunoz/coda/internal/utils"
)

type Config struct {
	// Host is the interface to listen on. Empty means all interfaces
	// (previous behavior); "127.0.0.1" restricts to loopback.
	Host  string   `yaml:"host"`
	Port  int      `yaml:"port"`
	Roots []string `yaml:"roots"`
	// AllowedHosts lists extra bare hostnames/IPs accepted by the HTTP
	// Host allowlist. Localhost and any IP literal are always allowed,
	// as is the concrete Host above; anything else must be listed here
	// (reverse proxies, mDNS names, Docker hostnames).
	AllowedHosts []string `yaml:"allowed_hosts"`
	// ScanTimeout bounds the whole library scan (walk + sync + auto-link
	// + prune). Zero means the default (10m); it is defaulted on load.
	ScanTimeout time.Duration `yaml:"scan_timeout"`
}

const (
	ConfigName = "config.yaml"
)

// DefaultScanTimeout is the scan bound when scan_timeout is omitted.
const DefaultScanTimeout = 10 * time.Minute

var DefaultConfig = Config{Host: "", Port: 8687, Roots: []string{}, ScanTimeout: DefaultScanTimeout}

// ListenAddr returns the TCP address for http.Server.Addr. An empty Host
// keeps the previous all-interfaces behavior (":port").
func (c Config) ListenAddr() string {
	return net.JoinHostPort(strings.TrimSpace(c.Host), strconv.Itoa(c.Port))
}

// GetConfig loads config.yaml (creating it with defaults on first run),
// applies CODA_HOST/CODA_PORT/CODA_ROOTS env overrides, validates host,
// roots and port, and returns the effective config.
func GetConfig(log *utils.Logger) (*Config, error) {
	var conf Config

	path, err := getConfigPath()
	if err != nil {
		return nil, fmt.Errorf("config: cannot get config path: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if _, err := os.Stat(path); err != nil &&
			errors.Is(err, os.ErrNotExist) {

			// Does not exist -> Create config file
			log.Info("config file not found, creating default")
			log.Debugf("config path: %s", path)

			dir := filepath.Dir(path)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("config: create dir %q: %w", path, err)
			}

			f, err := os.Create(path)
			if err != nil {
				return nil, fmt.Errorf("config: create config file %q: %w", path, err)
			}
			defer func() { _ = f.Close() }()

			data, err = yaml.Marshal(DefaultConfig)
			if err != nil {
				return nil, fmt.Errorf("config: marshal default config: %w", err)
			}

			if _, err := f.Write(data); err != nil {
				return nil, fmt.Errorf("config: write default config: %w", err)
			}

		} else {
			// Config file exists but cannot read it
			return nil, fmt.Errorf("config: read config file: %w", err)
		}
	}

	if err := yaml.Unmarshal(data, &conf); err != nil {
		return nil, fmt.Errorf("config: parse config file: %w", err)
	}

	if err := applyEnvOverrides(&conf); err != nil {
		return nil, err
	}

	if err := validateConfig(&conf); err != nil {
		return nil, fmt.Errorf("config: config file invalid: %w", err)
	}

	return &conf, nil
}

// applyEnvOverrides lets CODA_HOST, CODA_PORT, CODA_ROOTS,
// CODA_ALLOWED_HOSTS and CODA_SCAN_TIMEOUT override the file.
// CODA_ROOTS uses the OS path list (filepath.SplitList): colon-separated
// on macOS/Linux, semicolon-separated on Windows. Empty entries are dropped.
// CODA_ALLOWED_HOSTS is comma-separated and replaces the file's
// allowed_hosts. An empty CODA_HOST leaves the file's host untouched.
// CODA_SCAN_TIMEOUT is a Go duration (e.g. "15m"); empty leaves the file's
// scan_timeout untouched.
func applyEnvOverrides(conf *Config) error {
	if h := strings.TrimSpace(os.Getenv("CODA_HOST")); h != "" {
		conf.Host = h
	}
	if p := strings.TrimSpace(os.Getenv("CODA_PORT")); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("config: invalid CODA_PORT %q: %w", p, err)
		}
		conf.Port = port
	}
	if r := strings.TrimSpace(os.Getenv("CODA_ROOTS")); r != "" {
		var roots []string
		for _, part := range filepath.SplitList(r) {
			if part = strings.TrimSpace(part); part != "" {
				roots = append(roots, part)
			}
		}
		conf.Roots = roots
	}
	if raw, ok := os.LookupEnv("CODA_ALLOWED_HOSTS"); ok && strings.TrimSpace(raw) != "" {
		var hosts []string
		for part := range strings.SplitSeq(raw, ",") {
			if part = strings.TrimSpace(part); part != "" {
				hosts = append(hosts, part)
			}
		}
		conf.AllowedHosts = hosts
	}
	if raw := strings.TrimSpace(os.Getenv("CODA_SCAN_TIMEOUT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("config: invalid CODA_SCAN_TIMEOUT %q: %w", raw, err)
		}
		conf.ScanTimeout = d
	}
	if conf.ScanTimeout == 0 {
		conf.ScanTimeout = DefaultScanTimeout
	}
	return nil
}

// normalizeHost trims whitespace, unwraps bracketed IPv6 ("[::1]" -> "::1"),
// and rejects values that are not a bare hostname or IP. Empty stays empty
// (listen on all interfaces). A "host:port" value is rejected — the port
// belongs in `port` / CODA_PORT.
func normalizeHost(conf *Config) error {
	h := strings.TrimSpace(conf.Host)
	if h == "" {
		conf.Host = ""
		return nil
	}
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = strings.TrimSpace(h[1 : len(h)-1])
		if h == "" {
			return fmt.Errorf("host must not be empty")
		}
	}
	if strings.ContainsAny(h, " \t\r\n/") {
		return fmt.Errorf("host %q is invalid: must be a bare hostname or IP", conf.Host)
	}
	if strings.Contains(h, ":") && net.ParseIP(h) == nil {
		return fmt.Errorf("host %q is invalid: must be a bare hostname or IP, not host:port", conf.Host)
	}
	conf.Host = h
	return nil
}

// validateConfig cleans root paths, resolves symlinks, and rejects empty,
// relative, duplicate, or nested roots plus invalid hosts/ports,
// non-directories, and non-positive scan timeouts. A zero scan timeout
// means "omitted" and defaults to DefaultScanTimeout (old configs and
// direct validateConfig callers without the field).
func validateConfig(conf *Config) error {
	if conf.Port < 1 || conf.Port > 65535 {
		return fmt.Errorf("port must be between 1-65535")
	}
	if conf.ScanTimeout == 0 {
		conf.ScanTimeout = DefaultScanTimeout
	} else if conf.ScanTimeout < 0 {
		return fmt.Errorf("scan_timeout must be positive")
	}

	if err := normalizeHost(conf); err != nil {
		return err
	}

	if err := normalizeAllowedHosts(conf); err != nil {
		return err
	}

	for i, r := range conf.Roots {
		r = strings.TrimSpace(r)
		if r == "" {
			return fmt.Errorf("root #%d is invalid: cannot be empty", i)
		}

		if !filepath.IsAbs(r) {
			return fmt.Errorf("root #%d is invalid: needs to be an absolute path", i)
		}

		r = filepath.Clean(r)

		var err error
		r, err = filepath.EvalSymlinks(r)
		if err != nil {
			return fmt.Errorf("root #%d is invalid: %w", i, err)
		}

		conf.Roots[i] = r
	}

	// Reject nesting
	pathCount := len(conf.Roots)

	for i := range pathCount {
		for j := i + 1; j < pathCount; j++ {
			a := conf.Roots[i]
			b := conf.Roots[j]

			if a == b {
				return fmt.Errorf(
					"root #%d %q duplicates root #%d",
					j,
					conf.Roots[j],
					i,
				)
			}

			if strings.HasPrefix(b, a+string(os.PathSeparator)) {
				return fmt.Errorf(
					"root #%d %q is inside root #%d %q",
					j,
					conf.Roots[j],
					i,
					conf.Roots[i],
				)
			}

			if strings.HasPrefix(a, b+string(os.PathSeparator)) {
				return fmt.Errorf(
					"root #%d %q is inside root #%d %q",
					i,
					conf.Roots[i],
					j,
					conf.Roots[j],
				)
			}
		}

		// Check if dir
		f, err := os.Stat(conf.Roots[i])
		if err != nil {
			return fmt.Errorf("cannot get stat of root #%d %q: %w", i, conf.Roots[i], err)
		}

		if !f.IsDir() {
			return fmt.Errorf("root #%d %q is not a directory", i, conf.Roots[i])
		}
	}

	return nil
}

// normalizeAllowedHosts trims, lowercases, unwraps bracketed IPv6,
// drops a trailing dot, and rejects empty, wildcard, URL/path, or
// host:port values. Duplicates collapse silently. IP literals need not be
// listed (the server always allows them) but are accepted when present.
func normalizeAllowedHosts(conf *Config) error {
	seen := make(map[string]struct{}, len(conf.AllowedHosts))
	out := make([]string, 0, len(conf.AllowedHosts))
	for i, raw := range conf.AllowedHosts {
		h := strings.TrimSpace(raw)
		if h == "" {
			return fmt.Errorf("allowed_hosts #%d is invalid: cannot be empty", i)
		}
		if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
			h = strings.TrimSpace(h[1 : len(h)-1])
			if h == "" {
				return fmt.Errorf("allowed_hosts #%d is invalid: must not be empty", i)
			}
		}
		if strings.ContainsAny(h, " \t\r\n/") || strings.Contains(h, "\\") {
			return fmt.Errorf("allowed_hosts #%d %q is invalid: must be a bare hostname or IP", i, raw)
		}
		h = strings.ToLower(h)
		h = strings.TrimSuffix(h, ".")
		if h == "" {
			return fmt.Errorf("allowed_hosts #%d is invalid: must not be empty", i)
		}
		if h == "*" {
			return fmt.Errorf("allowed_hosts #%d is invalid: %q is not allowed", i, raw)
		}
		if strings.Contains(h, ":") {
			ip := h
			if j := strings.LastIndex(ip, "%"); j != -1 {
				ip = ip[:j]
			}
			if net.ParseIP(ip) == nil {
				return fmt.Errorf("allowed_hosts #%d %q is invalid: must be a bare hostname or IP, not host:port", i, raw)
			}
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	conf.AllowedHosts = out
	return nil
}

// getConfigPath returns the config path, honoring CODA_CONFIG when set.
func getConfigPath() (string, error) {
	if p := os.Getenv("CODA_CONFIG"); p != "" {
		return p, nil
	}
	return defaultConfigPath()
}

// defaultConfigPath returns the per-OS default config.yaml location.
func defaultConfigPath() (string, error) {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "coda", ConfigName), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf(
			"config: cannot determine home directory for config path: %w",
			err,
		)
	}
	if home == "" {
		return "", fmt.Errorf("config: empty home directory")
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Coda", ConfigName), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Coda", ConfigName), nil
	default:
		return filepath.Join(home, ".config", "coda", ConfigName), nil
	}
}
