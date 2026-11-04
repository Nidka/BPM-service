package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestLoadFromDevelopment(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL": "postgresql://postgres:secret@localhost:5432/bpm",
	}

	cfg, err := loadFrom(func(key string) string { return values[key] })
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.HTTP.Port != 8080 {
		t.Fatalf("expected default port 8080, got %d", cfg.HTTP.Port)
	}
	if cfg.Database.MaxConnections != 10 {
		t.Fatalf("expected default pool max 10, got %d", cfg.Database.MaxConnections)
	}
	if cfg.Firebase.Enabled || cfg.R2.Enabled {
		t.Fatal("external integrations must be opt-in during development")
	}
}

func TestSessionTimeoutConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, absolute, idle string
		valid                bool
	}{
		{"defaults", "", "", true}, {"shorter limits", "4h", "10m", true},
		{"disabled absolute", "0s", "15m", false}, {"disabled idle", "8h", "0s", false},
		{"exceeds requirement", "9h", "15m", false}, {"idle exceeds absolute", "10m", "15m", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{"DATABASE_URL": "postgres://test:test@localhost/bpm_test", "AUTH_SESSION_ABSOLUTE_TIMEOUT": tc.absolute, "AUTH_SESSION_IDLE_TIMEOUT": tc.idle}
			cfg, err := loadFrom(func(key string) string { return values[key] })
			if (err == nil) != tc.valid {
				t.Fatal("unexpected validation", err)
			}
			if tc.name == "defaults" && (cfg.Session.AbsoluteTimeout != 8*time.Hour || cfg.Session.IdleTimeout != 15*time.Minute) {
				t.Fatal("incorrect defaults")
			}
		})
	}
}

func TestRailwayHSTSDoesNotDependOnForwardedHeaders(t *testing.T) {
	values := map[string]string{"DATABASE_URL": "postgres://test:test@localhost/bpm_test", "RAILWAY_ENVIRONMENT_NAME": "production"}
	cfg, err := loadFrom(func(key string) string { return values[key] })
	if err != nil || !cfg.HTTP.HSTSEnabled {
		t.Fatal("Railway must enable HSTS", err)
	}
	delete(values, "RAILWAY_ENVIRONMENT_NAME")
	cfg, err = loadFrom(func(key string) string { return values[key] })
	if err != nil || cfg.HTTP.HSTSEnabled {
		t.Fatal("local HTTP should not acquire HSTS", err)
	}
}

func TestProductionRequiresSecurityIntegrations(t *testing.T) {
	values := map[string]string{
		"APP_ENV":              Production,
		"DATABASE_URL":         "postgresql://postgres:secret@localhost:5432/bpm",
		"CORS_ALLOWED_ORIGINS": "https://app.example.com",
	}

	_, err := loadFrom(func(key string) string { return values[key] })
	if err == nil {
		t.Fatal("expected production validation error")
	}
	if !strings.Contains(err.Error(), "FIREBASE_ENABLED") || !strings.Contains(err.Error(), "R2_ENABLED") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProductionConfiguration(t *testing.T) {
	credentials := base64.StdEncoding.EncodeToString([]byte(`{"type":"service_account"}`))
	values := map[string]string{
		"APP_ENV":                              Production,
		"DATABASE_URL":                         "postgresql://postgres:secret@postgres.railway.internal:5432/bpm",
		"CORS_ALLOWED_ORIGINS":                 "https://app.example.com",
		"FIREBASE_ENABLED":                     "true",
		"FIREBASE_PROJECT_ID":                  "bpm-production",
		"FIREBASE_SERVICE_ACCOUNT_JSON_BASE64": credentials,
		"R2_ENABLED":                           "true",
		"R2_ACCOUNT_ID":                        "account",
		"R2_ACCESS_KEY_ID":                     "key",
		"R2_SECRET_ACCESS_KEY":                 "secret",
		"R2_BUCKET_NAME":                       "bpm-private",
		"R2_ENDPOINT":                          "https://account.r2.cloudflarestorage.com",
	}

	if _, err := loadFrom(func(key string) string { return values[key] }); err != nil {
		t.Fatalf("expected valid production config: %v", err)
	}
}

func TestProductionRejectsWildcardCORS(t *testing.T) {
	values := map[string]string{
		"APP_ENV":              Production,
		"DATABASE_URL":         "postgresql://postgres:secret@localhost:5432/bpm",
		"CORS_ALLOWED_ORIGINS": "*",
		"FIREBASE_ENABLED":     "false",
		"R2_ENABLED":           "false",
	}

	_, err := loadFrom(func(key string) string { return values[key] })
	if err == nil || !strings.Contains(err.Error(), "CORS wildcard") {
		t.Fatalf("expected wildcard CORS error, got %v", err)
	}
}

func TestWorkerConfigurationKeepsProvidersExplicit(t *testing.T) {
	values := map[string]string{"DATABASE_URL": "postgres://localhost/bpm_test"}
	cfg, e := loadFrom(func(k string) string { return values[k] })
	if e != nil || cfg.Worker.Interval != time.Minute || cfg.Worker.FCMEnabled {
		t.Fatal("worker defaults", cfg.Worker, e)
	}
	for _, pair := range []struct{ k, v string }{{"ALERT_WORKER_INTERVAL", "0s"}, {"ALERT_WORKER_INTERVAL", "3h"}, {"FCM_ENABLED", "true"}, {"RESEND_FROM_EMAIL", "bad\naddress"}} {
		values[pair.k] = pair.v
		if _, e = loadFrom(func(k string) string { return values[k] }); e == nil {
			t.Fatal("accepted invalid worker configuration", pair.k)
		}
		delete(values, pair.k)
	}
}
