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

func TestValidateConfigHost(t *testing.T) {
	for _, tc := range []struct {
		name    string
		host    string
		want    string
		wantErr bool
	}{
		{"empty keeps all interfaces", "", "", false},
		{"loopback", "127.0.0.1", "127.0.0.1", false},
		{"hostname", "localhost", "localhost", false},
		{"ipv6", "::1", "::1", false},
		{"bracketed ipv6 unwrapped", "[::1]", "::1", false},
		{"whitespace trimmed", "  127.0.0.1  ", "127.0.0.1", false},
		{"host:port rejected", "127.0.0.1:8687", "", true},
		{"url rejected", "http://localhost", "", true},
		{"slash rejected", "foo/bar", "", true},
		{"space rejected", "foo bar", "", true},
		{"empty brackets rejected", "[]", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conf := Config{Host: tc.host, Port: 8687, Roots: []string{}}
			err := validateConfig(&conf)
			if tc.wantErr {
				if err == nil {
					t.Errorf("validateConfig(host=%q) = nil, want error", tc.host)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateConfig(host=%q) = %v, want nil", tc.host, err)
			}
			if conf.Host != tc.want {
				t.Errorf("host = %q, want %q", conf.Host, tc.want)
			}
		})
	}
}

func TestApplyEnvOverridesHost(t *testing.T) {
	t.Run("env wins over file", func(t *testing.T) {
		t.Setenv("CODA_HOST", "127.0.0.1")
		conf := Config{Host: "", Port: 8687}
		if err := applyEnvOverrides(&conf); err != nil {
			t.Fatal(err)
		}
		if conf.Host != "127.0.0.1" {
			t.Errorf("Host = %q, want 127.0.0.1", conf.Host)
		}
	})

	t.Run("empty env keeps file value", func(t *testing.T) {
		t.Setenv("CODA_HOST", "")
		conf := Config{Host: "0.0.0.0", Port: 8687}
		if err := applyEnvOverrides(&conf); err != nil {
			t.Fatal(err)
		}
		if conf.Host != "0.0.0.0" {
			t.Errorf("Host = %q, want 0.0.0.0", conf.Host)
		}
	})
}

func TestListenAddr(t *testing.T) {
	for _, tc := range []struct {
		name string
		conf Config
		want string
	}{
		{"empty host binds all interfaces", Config{Port: 8687}, ":8687"},
		{"loopback", Config{Host: "127.0.0.1", Port: 8687}, "127.0.0.1:8687"},
		{"ipv6 is bracketed", Config{Host: "::1", Port: 8687}, "[::1]:8687"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.conf.ListenAddr(); got != tc.want {
				t.Errorf("ListenAddr() = %q, want %q", got, tc.want)
			}
		})
	}
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

func TestValidateAllowedHosts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hosts   []string
		want    []string
		wantErr bool
	}{
		{"empty ok", nil, []string{}, false},
		{"hostname lowercased", []string{"Coda.LAN"}, []string{"coda.lan"}, false},
		{"trailing dot stripped", []string{"nas.local."}, []string{"nas.local"}, false},
		{"bracketed ipv6 unwrapped", []string{"[::1]"}, []string{"::1"}, false},
		{"ip ok", []string{"192.168.1.10"}, []string{"192.168.1.10"}, false},
		{"duplicates collapse", []string{"a.lan", "A.LAN", "a.lan."}, []string{"a.lan"}, false},
		{"empty entry rejected", []string{""}, nil, true},
		{"blank entry rejected", []string{"   "}, nil, true},
		{"wildcard rejected", []string{"*"}, nil, true},
		{"url rejected", []string{"http://coda.lan"}, nil, true},
		{"path rejected", []string{"coda/lan"}, nil, true},
		{"space rejected", []string{"coda lan"}, nil, true},
		{"host:port rejected", []string{"coda.lan:8687"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conf := Config{Port: 8687, AllowedHosts: tc.hosts}
			err := validateConfig(&conf)
			if tc.wantErr {
				if err == nil {
					t.Errorf("validateConfig(hosts=%q) = nil, want error", tc.hosts)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateConfig(hosts=%q) = %v, want nil", tc.hosts, err)
			}
			if len(conf.AllowedHosts) != len(tc.want) {
				t.Fatalf("hosts = %q, want %q", conf.AllowedHosts, tc.want)
			}
			for i := range tc.want {
				if conf.AllowedHosts[i] != tc.want[i] {
					t.Errorf("hosts[%d] = %q, want %q", i, conf.AllowedHosts[i], tc.want[i])
				}
			}
		})
	}
}

func TestApplyEnvOverridesAllowedHosts(t *testing.T) {
	t.Run("env replaces file", func(t *testing.T) {
		t.Setenv("CODA_ALLOWED_HOSTS", "coda.lan, NAS.LOCAL.")
		conf := Config{Port: 8687, AllowedHosts: []string{"old.lan"}}
		if err := applyEnvOverrides(&conf); err != nil {
			t.Fatal(err)
		}
		if err := validateConfig(&conf); err != nil {
			t.Fatal(err)
		}
		if len(conf.AllowedHosts) != 2 || conf.AllowedHosts[0] != "coda.lan" || conf.AllowedHosts[1] != "nas.local" {
			t.Errorf("AllowedHosts = %q, want [coda.lan nas.local]", conf.AllowedHosts)
		}
	})

	t.Run("empty env keeps file", func(t *testing.T) {
		t.Setenv("CODA_ALLOWED_HOSTS", "")
		conf := Config{Port: 8687, AllowedHosts: []string{"keep.lan"}}
		if err := applyEnvOverrides(&conf); err != nil {
			t.Fatal(err)
		}
		if len(conf.AllowedHosts) != 1 || conf.AllowedHosts[0] != "keep.lan" {
			t.Errorf("AllowedHosts = %q, want [keep.lan]", conf.AllowedHosts)
		}
	})
}
