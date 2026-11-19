package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/ppablomunoz/coda/internal/utils"
)

type Config struct {
	Port  int      `yaml:"port"`
	Roots []string `yaml:"roots"`
}

const (
	ConfigName = "config.yaml"
)

var DefaultConfig = Config{Port: 8687, Roots: []string{}}

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

	if err := validateConfig(&conf); err != nil {
		return nil, fmt.Errorf("config: config file invalid: %w", err)
	}

	return &conf, nil
}

func validateConfig(conf *Config) error {
	if conf.Port < 1 || conf.Port > 65535 {
		return fmt.Errorf("port must be between 1-65535")
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

func getConfigPath() (string, error) {
	if p := os.Getenv("CODA_CONFIG"); p != "" {
		return p, nil
	}
	return defaultConfigPath()
}

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
