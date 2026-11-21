package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateConfigPort(t *testing.T) {
	dir := t.TempDir()
	for _, port := range []int{0, -1, 65536} {
		conf := Config{Port: port, Roots: []string{}}
		if err := validateConfig(&conf); err == nil {
			t.Errorf("validateConfig(port=%d) = nil, want error", port)
		}
	}
	conf := Config{Port: 8687, Roots: []string{}}
	if err := validateConfig(&conf); err != nil {
		t.Errorf("validateConfig(valid) = %v, want nil", err)
	}
	// Empty roots are allowed (fresh start).
	conf = Config{Port: 1, Roots: []string{}}
	if err := validateConfig(&conf); err != nil {
		t.Errorf("validateConfig(port=1, no roots) = %v, want nil", err)
	}
	_ = dir
}

func TestValidateConfigRoots(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("empty root rejected", func(t *testing.T) {
		conf := Config{Port: 8687, Roots: []string{"   "}}
		if err := validateConfig(&conf); err == nil {
			t.Error("expected error for empty root")
		}
	})

	t.Run("relative root rejected", func(t *testing.T) {
		conf := Config{Port: 8687, Roots: []string{"relative/path"}}
		if err := validateConfig(&conf); err == nil {
			t.Error("expected error for relative root")
		}
	})

	t.Run("duplicate roots rejected", func(t *testing.T) {
		conf := Config{Port: 8687, Roots: []string{root, root}}
		if err := validateConfig(&conf); err == nil {
			t.Error("expected error for duplicate roots")
		}
	})

	t.Run("nested roots rejected", func(t *testing.T) {
		conf := Config{Port: 8687, Roots: []string{root, sub}}
		if err := validateConfig(&conf); err == nil {
			t.Error("expected error for nested roots")
		}
		conf = Config{Port: 8687, Roots: []string{sub, root}}
		if err := validateConfig(&conf); err == nil {
			t.Error("expected error for nested roots (reversed)")
		}
	})

	t.Run("sibling prefix not nested", func(t *testing.T) {
		a := filepath.Join(root, "ab")
		b := filepath.Join(root, "abc")
		if err := os.Mkdir(a, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(b, 0o755); err != nil {
			t.Fatal(err)
		}
		conf := Config{Port: 8687, Roots: []string{a, b}}
		if err := validateConfig(&conf); err != nil {
			t.Errorf("sibling prefix roots should pass, got %v", err)
		}
	})

	t.Run("file not directory rejected", func(t *testing.T) {
		conf := Config{Port: 8687, Roots: []string{file}}
		if err := validateConfig(&conf); err == nil {
			t.Error("expected error for file root")
		}
	})

	t.Run("missing path rejected", func(t *testing.T) {
		conf := Config{Port: 8687, Roots: []string{filepath.Join(root, "nope")}}
		if err := validateConfig(&conf); err == nil {
			t.Error("expected error for missing root")
		}
	})

	t.Run("valid single root", func(t *testing.T) {
		conf := Config{Port: 8687, Roots: []string{root}}
		if err := validateConfig(&conf); err != nil {
			t.Errorf("validateConfig = %v, want nil", err)
		}
	})
}

func TestGetDBPathEnvOverride(t *testing.T) {
	t.Setenv("CODA_DB_PATH", "/tmp/custom.db")
	got, err := GetDBPath()
	if err != nil {
		t.Fatalf("GetDBPath = %v, want nil", err)
	}
	if !strings.HasPrefix(got, "/tmp/custom.db") {
		t.Errorf("GetDBPath = %q, want prefix /tmp/custom.db", got)
	}
	if !strings.Contains(got, "journal_mode(WAL)") {
		t.Errorf("GetDBPath = %q, want pragmas appended", got)
	}
}

func TestGetConfigPathEnvOverride(t *testing.T) {
	t.Setenv("CODA_CONFIG", "/tmp/coda-test.yaml")
	got, err := getConfigPath()
	if err != nil {
		t.Fatalf("getConfigPath = %v, want nil", err)
	}
	if got != "/tmp/coda-test.yaml" {
		t.Errorf("getConfigPath = %q, want /tmp/coda-test.yaml", got)
	}
}
