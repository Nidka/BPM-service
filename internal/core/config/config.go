// Package config define y valida el contrato de configuración de la aplicación.
package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	Development = "development"
	Test        = "test"
	Staging     = "staging"
	Production  = "production"
)

type Config struct {
	App           AppConfig
	HTTP          HTTPConfig
	Session       SessionConfig
	Database      DatabaseConfig
	Firebase      FirebaseConfig
	R2            R2Config
	Email         EmailConfig
	Observability ObservabilityConfig
	Worker        WorkerConfig
}

type WorkerConfig struct {
	Interval   time.Duration
	FCMEnabled bool
}

type SessionConfig struct{ AbsoluteTimeout, IdleTimeout time.Duration }

type AppConfig struct {
	Name        string
	Environment string
	Version     string
	LogLevel    string
}

func (c AppConfig) IsProductionLike() bool {
	return c.Environment == Staging || c.Environment == Production
}

type HTTPConfig struct {
	Host              string
	Port              int
	APIBasePath       string
	AllowedOrigins    []string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	RequestTimeout    time.Duration
	MaxJSONBodyBytes  int64
	RateLimitRPS      float64
	RateLimitBurst    int
	TrustProxyHeaders bool
	HSTSMaxAgeSeconds int
	HSTSEnabled       bool
}

func (c HTTPConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

type DatabaseConfig struct {
	URL               string
	MinConnections    int32
	MaxConnections    int32
	MaxConnectionIdle time.Duration
	MaxConnectionLife time.Duration
	ConnectTimeout    time.Duration
	StatementTimeout  time.Duration
	LockTimeout       time.Duration
}

type FirebaseConfig struct {
	Enabled                  bool
	ProjectID                string
	ServiceAccountJSONBase64 string
}

type R2Config struct {
	Enabled          bool
	AccountID        string
	AccessKeyID      string
	SecretKey        string
	Bucket           string
	Endpoint         string
	SignedURLTTL     time.Duration
	MaxUploadBytes   int64
	AllowedMIMETypes map[string]struct{}
}

type EmailConfig struct {
	ResendAPIKey string
	From         string
}

type ObservabilityConfig struct {
	SentryDSN string
}

func Load() (Config, error) {
	// Railway inyecta las variables de entorno. El archivo .env facilita únicamente el desarrollo local.
	_ = godotenv.Load()
	return loadFrom(os.Getenv)
}

func loadFrom(getenv func(string) string) (Config, error) {
	var parseErrs []error

	integer := func(key string, fallback int) int {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			return fallback
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("%s must be an integer", key))
			return fallback
		}
		return parsed
	}

	integer64 := func(key string, fallback int64) int64 {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			return fallback
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("%s must be an integer", key))
			return fallback
		}
		return parsed
	}

	duration := func(key string, fallback time.Duration) time.Duration {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			return fallback
		}
		parsed, err := time.ParseDuration(value)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("%s must be a duration such as 5s or 1m", key))
			return fallback
		}
		return parsed
	}

	boolean := func(key string, fallback bool) bool {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			return fallback
		}
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("%s must be true or false", key))
			return fallback
		}
		return parsed
	}

	floating := func(key string, fallback float64) float64 {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			return fallback
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("%s must be numeric", key))
			return fallback
		}
		return parsed
	}

	environment := strings.ToLower(valueOrDefault(getenv("APP_ENV"), Development))
	allowedOrigins := splitCSV(getenv("CORS_ALLOWED_ORIGINS"))
	allowedMIMEs := make(map[string]struct{})
	for _, mime := range splitCSV(valueOrDefault(getenv("MEDIA_ALLOWED_MIME_TYPES"), "image/jpeg,image/png,image/webp,application/pdf")) {
		allowedMIMEs[strings.ToLower(mime)] = struct{}{}
	}

	cfg := Config{
		App: AppConfig{
			Name:        valueOrDefault(getenv("APP_NAME"), "bpm-api"),
			Environment: environment,
			Version:     valueOrDefault(getenv("APP_VERSION"), valueOrDefault(getenv("RAILWAY_GIT_COMMIT_SHA"), "dev")),
			LogLevel:    strings.ToUpper(valueOrDefault(getenv("LOG_LEVEL"), "INFO")),
		},
		HTTP: HTTPConfig{
			Host:              valueOrDefault(getenv("APP_HOST"), "0.0.0.0"),
			Port:              integer("PORT", 8080),
			APIBasePath:       valueOrDefault(getenv("API_BASE_PATH"), "/api/v1"),
			AllowedOrigins:    allowedOrigins,
			ReadHeaderTimeout: duration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
			ReadTimeout:       duration("HTTP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:      duration("HTTP_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:       duration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout:   duration("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
			RequestTimeout:    duration("HTTP_REQUEST_TIMEOUT", 20*time.Second),
			MaxJSONBodyBytes:  integer64("HTTP_MAX_JSON_BODY_BYTES", 1<<20),
			RateLimitRPS:      floating("HTTP_RATE_LIMIT_RPS", 50),
			RateLimitBurst:    integer("HTTP_RATE_LIMIT_BURST", 100),
			TrustProxyHeaders: boolean("HTTP_TRUST_PROXY_HEADERS", false),
			HSTSMaxAgeSeconds: integer("HTTP_HSTS_MAX_AGE_SECONDS", 31536000),
			HSTSEnabled:       boolean("HTTP_HSTS_ENABLED", getenv("APP_ENV") == Production || getenv("APP_ENV") == Staging || getenv("RAILWAY_ENVIRONMENT_NAME") != ""),
		},
		Worker:  WorkerConfig{Interval: duration("ALERT_WORKER_INTERVAL", time.Minute), FCMEnabled: boolean("FCM_ENABLED", false)},
		Session: SessionConfig{AbsoluteTimeout: duration("AUTH_SESSION_ABSOLUTE_TIMEOUT", 8*time.Hour), IdleTimeout: duration("AUTH_SESSION_IDLE_TIMEOUT", 15*time.Minute)},
		Database: DatabaseConfig{
			URL:               strings.TrimSpace(getenv("DATABASE_URL")),
			MinConnections:    int32(integer("DB_POOL_MIN_SIZE", 2)),
			MaxConnections:    int32(integer("DB_POOL_MAX_SIZE", 10)),
			MaxConnectionIdle: duration("DB_MAX_CONNECTION_IDLE", 5*time.Minute),
			MaxConnectionLife: duration("DB_MAX_CONNECTION_LIFETIME", 30*time.Minute),
			ConnectTimeout:    duration("DB_CONNECTION_TIMEOUT", 5*time.Second),
			StatementTimeout:  duration("DB_STATEMENT_TIMEOUT", 15*time.Second),
			LockTimeout:       duration("DB_LOCK_TIMEOUT", 5*time.Second),
		},
		Firebase: FirebaseConfig{
			Enabled:                  boolean("FIREBASE_ENABLED", false),
			ProjectID:                strings.TrimSpace(getenv("FIREBASE_PROJECT_ID")),
			ServiceAccountJSONBase64: strings.TrimSpace(getenv("FIREBASE_SERVICE_ACCOUNT_JSON_BASE64")),
		},
		R2: R2Config{
			Enabled:          boolean("R2_ENABLED", false),
			AccountID:        strings.TrimSpace(getenv("R2_ACCOUNT_ID")),
			AccessKeyID:      strings.TrimSpace(getenv("R2_ACCESS_KEY_ID")),
			SecretKey:        strings.TrimSpace(getenv("R2_SECRET_ACCESS_KEY")),
			Bucket:           strings.TrimSpace(getenv("R2_BUCKET_NAME")),
			Endpoint:         strings.TrimSpace(getenv("R2_ENDPOINT")),
			SignedURLTTL:     duration("R2_SIGNED_URL_TTL", 5*time.Minute),
			MaxUploadBytes:   integer64("MEDIA_MAX_UPLOAD_BYTES", 10<<20),
			AllowedMIMETypes: allowedMIMEs,
		},
		Email: EmailConfig{
			ResendAPIKey: strings.TrimSpace(getenv("RESEND_API_KEY")),
			From:         strings.TrimSpace(getenv("RESEND_FROM_EMAIL")),
		},
		Observability: ObservabilityConfig{
			SentryDSN: strings.TrimSpace(getenv("SENTRY_DSN")),
		},
	}

	if len(parseErrs) > 0 {
		return Config{}, errors.Join(parseErrs...)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var errs []error
	if c.Worker.Interval < time.Second || c.Worker.Interval > time.Hour {
		errs = append(errs, errors.New("ALERT_WORKER_INTERVAL must be between 1s and 1h"))
	}
	if c.Worker.FCMEnabled && !c.Firebase.Enabled {
		errs = append(errs, errors.New("FCM_ENABLED requires FIREBASE_ENABLED"))
	}
	if c.Email.ResendAPIKey != "" && c.Email.From == "" {
		errs = append(errs, errors.New("RESEND_FROM_EMAIL is required for Resend"))
	}
	if c.Email.From != "" {
		if _, e := mail.ParseAddress(c.Email.From); e != nil || strings.ContainsAny(c.Email.From, "\r\n") {
			errs = append(errs, errors.New("RESEND_FROM_EMAIL is invalid"))
		}
	}

	switch c.App.Environment {
	case Development, Test, Staging, Production:
	default:
		errs = append(errs, fmt.Errorf("APP_ENV must be one of development, test, staging or production"))
	}

	if c.HTTP.Port < 1 || c.HTTP.Port > 65535 {
		errs = append(errs, fmt.Errorf("PORT must be between 1 and 65535"))
	}
	if !strings.HasPrefix(c.HTTP.APIBasePath, "/") {
		errs = append(errs, fmt.Errorf("API_BASE_PATH must start with /"))
	}
	if c.HTTP.MaxJSONBodyBytes < 1024 || c.HTTP.MaxJSONBodyBytes > 5<<20 {
		errs = append(errs, fmt.Errorf("HTTP_MAX_JSON_BODY_BYTES must be between 1 KiB and 5 MiB"))
	}
	if c.HTTP.RateLimitRPS <= 0 || c.HTTP.RateLimitBurst < 1 {
		errs = append(errs, fmt.Errorf("HTTP rate limit values must be positive"))
	}
	if c.HTTP.HSTSMaxAgeSeconds < 1 || c.HTTP.HSTSMaxAgeSeconds > 31536000 {
		errs = append(errs, errors.New("HTTP_HSTS_MAX_AGE_SECONDS must be between 1 and 31536000"))
	}
	if c.Session.AbsoluteTimeout <= 0 || c.Session.AbsoluteTimeout > 8*time.Hour || c.Session.IdleTimeout <= 0 || c.Session.IdleTimeout > c.Session.AbsoluteTimeout {
		errs = append(errs, errors.New("session timeouts must be positive, idle <= absolute <= 8h"))
	}
	for _, origin := range c.HTTP.AllowedOrigins {
		if origin == "*" && c.App.IsProductionLike() {
			errs = append(errs, fmt.Errorf("CORS wildcard is forbidden outside development/test"))
			continue
		}
		parsed, err := url.ParseRequestURI(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			errs = append(errs, fmt.Errorf("invalid CORS origin: %s", origin))
			continue
		}
		if c.App.IsProductionLike() && parsed.Scheme != "https" {
			errs = append(errs, fmt.Errorf("CORS origins must use HTTPS outside development/test"))
		}
	}

	if c.Database.URL == "" {
		errs = append(errs, fmt.Errorf("DATABASE_URL is required"))
	} else if parsed, err := url.Parse(c.Database.URL); err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		errs = append(errs, fmt.Errorf("DATABASE_URL must be a PostgreSQL URL"))
	}
	if c.Database.MinConnections < 0 || c.Database.MaxConnections < 1 || c.Database.MinConnections > c.Database.MaxConnections {
		errs = append(errs, fmt.Errorf("database pool sizes are invalid"))
	}

	if c.App.IsProductionLike() && !c.Firebase.Enabled {
		errs = append(errs, fmt.Errorf("FIREBASE_ENABLED must be true in staging and production"))
	}
	if c.Firebase.Enabled {
		if c.Firebase.ProjectID == "" || c.Firebase.ServiceAccountJSONBase64 == "" {
			errs = append(errs, fmt.Errorf("Firebase project and service account are required when Firebase is enabled"))
		} else if raw, err := base64.StdEncoding.DecodeString(c.Firebase.ServiceAccountJSONBase64); err != nil || !json.Valid(raw) {
			errs = append(errs, fmt.Errorf("FIREBASE_SERVICE_ACCOUNT_JSON_BASE64 must contain valid base64-encoded JSON"))
		}
	}

	if c.App.IsProductionLike() && !c.R2.Enabled {
		errs = append(errs, fmt.Errorf("R2_ENABLED must be true in staging and production"))
	}
	if c.R2.Enabled {
		if c.R2.AccountID == "" || c.R2.AccessKeyID == "" || c.R2.SecretKey == "" || c.R2.Bucket == "" || c.R2.Endpoint == "" {
			errs = append(errs, fmt.Errorf("all R2 connection values are required when R2 is enabled"))
		}
		if endpoint, err := url.Parse(c.R2.Endpoint); err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
			errs = append(errs, fmt.Errorf("R2_ENDPOINT must be a valid HTTPS URL"))
		}
		if c.R2.SignedURLTTL < time.Minute || c.R2.SignedURLTTL > 15*time.Minute {
			errs = append(errs, fmt.Errorf("R2_SIGNED_URL_TTL must be between 1m and 15m"))
		}
		if c.R2.MaxUploadBytes < 1<<20 || c.R2.MaxUploadBytes > 25<<20 {
			errs = append(errs, fmt.Errorf("MEDIA_MAX_UPLOAD_BYTES must be between 1 MiB and 25 MiB"))
		}
	}

	return errors.Join(errs...)
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
