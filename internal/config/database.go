package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	DBName  = "coda.db"
	Pragmas = "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
)

func GetDBPath() (string, error) {
	if p := os.Getenv("CODA_DB_PATH"); p != "" {
		return p + Pragmas, nil
	}
	return defaultDBPath()
}

func defaultDBPath() (string, error) {
	switch runtime.GOOS {
	case "windows":
		// DB is machine-local state, so prefer Local over Roaming AppData.
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "Coda", DBName+Pragmas), nil
		}
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			return filepath.Join(dir, "Coda", DBName+Pragmas), nil
		}
	case "darwin":
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			return filepath.Join(dir, "Coda", DBName+Pragmas), nil
		}
	default:
		// Linux and other Unix: data dir, not ~/.config (that's for config).
		if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
			return filepath.Join(dir, "coda", DBName+Pragmas), nil
		}
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
		return filepath.Join(home, "AppData", "Local", "Coda", DBName+Pragmas), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Coda", DBName+Pragmas), nil
	default:
		return filepath.Join(home, ".local", "share", "coda", DBName+Pragmas), nil
	}
}
