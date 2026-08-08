//go:build unit

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	c := defaults()
	if c.App.Environment != "development" {
		t.Errorf("environment = %q, want development", c.App.Environment)
	}
	if c.HTTP.Port != 8080 {
		t.Errorf("http port = %d, want 8080", c.HTTP.Port)
	}
	if c.DB.Driver != "mariadb" {
		t.Errorf("db driver = %q, want mariadb", c.DB.Driver)
	}
	if c.Gateway.LoginPort != 6900 || c.Gateway.CharPort != 6121 || c.Gateway.MapPort != 5121 {
		t.Errorf("gateway ports = %+v, want login6900/char6121/map5121", c.Gateway)
	}
	if c.Zone.TickRateHz != 50 {
		t.Errorf("tick rate = %d, want 50", c.Zone.TickRateHz)
	}
}

func TestLoad_FromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte(`
app:
  name: goathena-test
  environment: production
  port: 9090
http:
  port: 9091
db:
  driver: postgres
  host: db.local
  port: 5432
  name: ro
  user: ro
identity:
  use_md5_passwords: false
  max_chars: 12
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.App.Environment != "production" {
		t.Errorf("environment = %q", c.App.Environment)
	}
	if c.App.Port != 9090 {
		t.Errorf("app port = %d", c.App.Port)
	}
	if c.DB.Driver != "postgres" || c.DB.Port != 5432 {
		t.Errorf("db = %+v", c.DB)
	}
	if c.Identity.UseMD5Passwords {
		t.Error("use_md5_passwords should be false")
	}
	if c.Identity.MaxChars != 12 {
		t.Errorf("max_chars = %d, want 12", c.Identity.MaxChars)
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	t.Setenv("APP_PORT", "7070")
	t.Setenv("HTTP_PORT", "7071")
	t.Setenv("DB_DRIVER", "postgres")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("app:\n  environment: production\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.App.Port != 7070 {
		t.Errorf("app port = %d, env should win", c.App.Port)
	}
	if c.HTTP.Port != 7071 {
		t.Errorf("http port = %d, env should win", c.HTTP.Port)
	}
	if c.DB.Driver != "postgres" {
		t.Errorf("db driver = %q, env should win", c.DB.Driver)
	}
}

func TestLoad_EnvParsesDuration(t *testing.T) {
	t.Setenv("APP_SHUTDOWN_TIMEOUT", "45s")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("app:\n  environment: development\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.App.ShutdownTimeout != 45*time.Second {
		t.Errorf("shutdown timeout = %v, want 45s", c.App.ShutdownTimeout)
	}
}

func TestValidate_BadDriver(t *testing.T) {
	c := defaults()
	c.DB.Driver = "oracle"
	if err := c.Validate(); err == nil {
		t.Error("Validate should reject driver oracle")
	}
}

func TestValidate_BadEnvironment(t *testing.T) {
	c := defaults()
	c.App.Environment = "moon"
	if err := c.Validate(); err == nil {
		t.Error("Validate should reject environment moon")
	}
}

func TestDSN(t *testing.T) {
	cases := []struct {
		name    string
		db      DBConfig
		wantSub string
		wantErr bool
	}{
		{"mariadb", DBConfig{Driver: "mariadb", User: "u", Password: "p", Host: "h", Port: 3306, Name: "n", SSLMode: "disable"}, "u:p@tcp(h:3306)/n", false},
		{"postgres", DBConfig{Driver: "postgres", User: "u", Password: "p", Host: "h", Port: 5432, Name: "n", SSLMode: "disable"}, "postgres://u:p@h:5432/n", false},
		{"unknown", DBConfig{Driver: "oracle"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dsn, err := tc.db.DSN()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("DSN: %v", err)
			}
			if !contains(dsn, tc.wantSub) {
				t.Errorf("dsn = %q, want it to contain %q", dsn, tc.wantSub)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
