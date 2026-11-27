package config

import (
	"testing"
	"time"
)

func TestScanTimeoutDefaultsAndValidation(t *testing.T) {
	// Zero defaults to DefaultScanTimeout.
	conf := Config{Port: 8687, Roots: []string{}}
	if err := validateConfig(&conf); err != nil {
		t.Fatalf("validateConfig zero timeout = %v, want default", err)
	}
	if conf.ScanTimeout != DefaultScanTimeout {
		t.Errorf("ScanTimeout = %s, want %s (default)", conf.ScanTimeout, DefaultScanTimeout)
	}

	// Negative is rejected.
	conf = Config{Port: 8687, Roots: []string{}, ScanTimeout: -time.Minute}
	if err := validateConfig(&conf); err == nil {
		t.Error("negative scan_timeout = nil, want error")
	}

	// Positive is kept.
	conf = Config{Port: 8687, Roots: []string{}, ScanTimeout: 15 * time.Minute}
	if err := validateConfig(&conf); err != nil {
		t.Fatalf("validateConfig 15m = %v, want nil", err)
	}
	if conf.ScanTimeout != 15*time.Minute {
		t.Errorf("ScanTimeout = %s, want 15m", conf.ScanTimeout)
	}
}

func TestScanTimeoutEnvOverride(t *testing.T) {
	t.Setenv("CODA_SCAN_TIMEOUT", "15m")
	conf := Config{Port: 8687}
	if err := applyEnvOverrides(&conf); err != nil {
		t.Fatalf("applyEnvOverrides = %v", err)
	}
	if conf.ScanTimeout != 15*time.Minute {
		t.Errorf("ScanTimeout = %s, want 15m from env", conf.ScanTimeout)
	}

	t.Setenv("CODA_SCAN_TIMEOUT", "bogus")
	conf = Config{Port: 8687}
	if err := applyEnvOverrides(&conf); err == nil {
		t.Error("bogus CODA_SCAN_TIMEOUT = nil, want error")
	}

	// Empty env keeps file value, missing file value defaults.
	t.Setenv("CODA_SCAN_TIMEOUT", "")
	conf = Config{Port: 8687, ScanTimeout: 20 * time.Minute}
	if err := applyEnvOverrides(&conf); err != nil {
		t.Fatalf("applyEnvOverrides empty = %v", err)
	}
	if conf.ScanTimeout != 20*time.Minute {
		t.Errorf("ScanTimeout = %s, want file value 20m", conf.ScanTimeout)
	}
	conf = Config{Port: 8687}
	if err := applyEnvOverrides(&conf); err != nil {
		t.Fatalf("applyEnvOverrides missing = %v", err)
	}
	if conf.ScanTimeout != DefaultScanTimeout {
		t.Errorf("ScanTimeout = %s, want default %s", conf.ScanTimeout, DefaultScanTimeout)
	}
}
