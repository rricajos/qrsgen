package config

import (
	"os"
	"testing"
)

// osUnsetenv is a thin alias so the test file's intent is obvious at the call
// site (forcing an env var into the "missing" state for env.Parse).
var osUnsetenv = os.Unsetenv

func setenv(t *testing.T, k, v string) {
	t.Helper()
	t.Setenv(k, v)
}

// minimalEnv populates the env vars marked `required` in Config with usable
// defaults so other fields can be exercised in isolation.
func minimalEnv(t *testing.T) {
	t.Helper()
	setenv(t, "POSTGRES_HOST", "pg")
	setenv(t, "POSTGRES_DB", "bridge")
	setenv(t, "POSTGRES_USER", "postgres")
	setenv(t, "POSTGRES_PASSWORD", "secret")
	setenv(t, "DOWNSTREAM_BASE_URL", "https://example.test")
	setenv(t, "DOWNSTREAM_API_TOKEN", "tok")
	setenv(t, "DOWNSTREAM_INBOX_ID", "1")
	setenv(t, "INSTANCE_NAME", "DEFAULT")
}

func TestLoad_DefaultsApplied(t *testing.T) {
	minimalEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 3100 {
		t.Errorf("Port = %d, want default 3100", cfg.Port)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
	if cfg.PostgresPort != 5432 {
		t.Errorf("PostgresPort = %d, want default 5432", cfg.PostgresPort)
	}
	if cfg.DedupWindowMs != 10000 {
		t.Errorf("DedupWindowMs = %d, want default 10000", cfg.DedupWindowMs)
	}
	if !cfg.DedupEnabled {
		t.Error("DedupEnabled default should be true")
	}
}

func TestLoad_MissingRequired_ReturnsError(t *testing.T) {
	// Force unset of POSTGRES_HOST so env.Parse hits the missing-required path.
	// t.Setenv to "" doesn't trigger required because env/v11 treats "" as set;
	// using os.Unsetenv + manual restore via t.Cleanup does.
	t.Setenv("POSTGRES_DB", "bridge")
	t.Setenv("POSTGRES_USER", "postgres")
	t.Setenv("POSTGRES_PASSWORD", "secret")
	t.Setenv("DOWNSTREAM_BASE_URL", "https://example.test")
	t.Setenv("DOWNSTREAM_API_TOKEN", "tok")
	t.Setenv("DOWNSTREAM_INBOX_ID", "1")
	t.Setenv("INSTANCE_NAME", "DEFAULT")
	// Tee t.Setenv so the cleanup re-establishes whatever was there before.
	t.Setenv("POSTGRES_HOST", "placeholder")
	if err := osUnsetenv("POSTGRES_HOST"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	if _, err := Load(); err == nil {
		t.Error("expected error when POSTGRES_HOST is unset")
	}
}

func TestLoad_OptionalFieldsParsed(t *testing.T) {
	minimalEnv(t)
	setenv(t, "PORT", "9090")
	setenv(t, "QRSGEN_API_TOKEN", "bearer-tok")
	setenv(t, "WEBHOOK_HMAC_SECRET", "hmac-key")
	setenv(t, "DEDUP_ENABLED", "false")
	setenv(t, "DEDUP_WINDOW_MS", "5000")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.APIToken != "bearer-tok" {
		t.Errorf("APIToken = %q", cfg.APIToken)
	}
	if cfg.WebhookHMACSecret != "hmac-key" {
		t.Errorf("WebhookHMACSecret = %q", cfg.WebhookHMACSecret)
	}
	if cfg.DedupEnabled {
		t.Error("DedupEnabled should be false when env says so")
	}
	if cfg.DedupWindowMs != 5000 {
		t.Errorf("DedupWindowMs = %d", cfg.DedupWindowMs)
	}
}

func TestPostgresDSN_Format(t *testing.T) {
	cfg := Config{
		PostgresUser:     "u",
		PostgresPassword: "p",
		PostgresHost:     "h",
		PostgresPort:     6543,
		PostgresDB:       "db",
	}
	got := cfg.PostgresDSN()
	want := "postgres://u:p@h:6543/db?sslmode=disable"
	if got != want {
		t.Errorf("DSN = %q, want %q", got, want)
	}
}
