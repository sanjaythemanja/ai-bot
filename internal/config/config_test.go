package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequireApprovalPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// Missing file → default true.
	fc, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := boolSetting("TEST_REQUIRE_APPROVAL", fc.RequireApproval, true); !v {
		t.Fatal("default should be true")
	}

	// File says false.
	if err := os.WriteFile(path, []byte("require_approval: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fc, err = loadFile(path); err != nil {
		t.Fatal(err)
	}
	if v, _ := boolSetting("TEST_REQUIRE_APPROVAL", fc.RequireApproval, true); v {
		t.Fatal("file value false should win over default")
	}

	// Env overrides file; garbage is an error.
	t.Setenv("TEST_REQUIRE_APPROVAL", "true")
	if v, _ := boolSetting("TEST_REQUIRE_APPROVAL", fc.RequireApproval, true); !v {
		t.Fatal("env should override file")
	}
	t.Setenv("TEST_REQUIRE_APPROVAL", "maybe")
	if _, err := boolSetting("TEST_REQUIRE_APPROVAL", fc.RequireApproval, true); err == nil {
		t.Fatal("expected error for non-boolean value")
	}

	// Broken YAML is reported, not silently ignored.
	_ = os.WriteFile(path, []byte("require_approval: [oops"), 0o600)
	if _, err := loadFile(path); err == nil {
		t.Fatal("expected YAML parse error")
	}
}

func TestProductionValidation(t *testing.T) {
	good := Config{
		Env: "production", DatabaseURL: "postgres://x", BaseURL: "https://app.example.com",
		AppSecret: "0123456789abcdef0123456789abcdef", GoogleClientID: "id", GoogleClientSecret: "secret",
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"no database":  func(c *Config) { c.DatabaseURL = "" },
		"demo mode":    func(c *Config) { c.DemoMode = true },
		"plain http":   func(c *Config) { c.BaseURL = "http://app.example.com" },
		"short secret": func(c *Config) { c.AppSecret = "short" },
		"no oauth":     func(c *Config) { c.GoogleClientSecret = "" },
	} {
		c := good
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected production config to be rejected", name)
		}
	}
	if err := (Config{Env: "local"}).Validate(); err != nil {
		t.Errorf("local config should always be accepted: %v", err)
	}
	if err := (Config{Env: "staging"}).Validate(); err == nil {
		t.Error("unknown APP_ENV should be rejected")
	}
}
