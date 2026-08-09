// Package config loads goAthena's runtime configuration from a YAML file with
// environment-variable overrides, applies sensible defaults, and validates the
// result before any service starts.
//
// The loader is intentionally dependency-light: YAML decode (gopkg.in/yaml.v3)
// plus a small reflection-based env overlay. Container deployments set env
// vars; local dev edits config.yaml. Env always wins over file, which wins over
// the zero value after defaults are applied.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

// ErrConfigFatal marks a configuration error that no amount of retrying will
// fix: malformed YAML, an unsupported enum value, or a missing required field.
// Callers must treat it as a hard exit, not a transient outage the readiness
// loop could recover from. Infrastructure reachability failures (DB/Valkey
// unreachable, including auth errors) are NEVER wrapped with this sentinel —
// those stay best-effort so the process keeps probing and reporting /readyz.
var ErrConfigFatal = errors.New("config fatal")

// IsFatal reports whether err (or an error it wraps) is a non-recoverable
// configuration error. main uses this to exit immediately on a bad config
// instead of entering the retry loop reserved for transient outages. Pure so
// the classification is unit-testable without a live process.
func IsFatal(err error) bool { return errors.Is(err, ErrConfigFatal) }

// fatal wraps err so callers can detect it via IsFatal. The sentinel is the
// leading wrapped value so errors.Is matches before the detail.
func fatal(err error) error { return fmt.Errorf("%w: %w", ErrConfigFatal, err) }

// validate is the process-wide, concurrency-safe validator instance. Built once
// with a custom rule so time.Duration fields can enforce a minimum like 1s.
var validate = sync.OnceValue(func() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	_ = v.RegisterValidation("min_duration", minDuration) // 1s minimum, ns precise
	return v
})

// minDuration validates a time.Duration field is >= 1 second.
func minDuration(fl validator.FieldLevel) bool {
	d, ok := fl.Field().Interface().(time.Duration)
	return ok && d >= time.Second
}

// Config is the root runtime configuration for the goathena binary.
type Config struct {
	App      AppConfig      `yaml:"app"`
	HTTP     HTTPConfig     `yaml:"http"`
	DB       DBConfig       `yaml:"db"`
	Valkey   ValkeyConfig   `yaml:"valkey"`
	NATS     NATSConfig     `yaml:"nats"`
	Gateway  GatewayConfig  `yaml:"gateway"`
	Identity IdentityConfig `yaml:"identity"`
	Zone     ZoneConfig     `yaml:"zone"`
	Log      LogConfig      `yaml:"log"`
	OTel     OTelConfig     `yaml:"otel"`
}

// AppConfig holds process-level identity and lifecycle knobs.
type AppConfig struct {
	Name            string        `yaml:"name"             env:"APP_NAME"`
	Environment     string        `yaml:"environment"      env:"APP_ENVIRONMENT" validate:"oneof=development staging production test"`
	Host            string        `yaml:"host"             env:"APP_HOST"`
	Port            int           `yaml:"port"             env:"APP_PORT" validate:"min=1,max=65535"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" env:"APP_SHUTDOWN_TIMEOUT" validate:"min_duration"`
}

// HTTPConfig is the control-plane HTTP server (health/metrics/ops).
type HTTPConfig struct {
	Host               string        `yaml:"host"                 env:"HTTP_HOST"`
	Port               int           `yaml:"port"                 env:"HTTP_PORT" validate:"min=1,max=65535"`
	ReadTimeout        time.Duration `yaml:"read_timeout"         env:"HTTP_READ_TIMEOUT"`
	WriteTimeout       time.Duration `yaml:"write_timeout"        env:"HTTP_WRITE_TIMEOUT"`
	IdleTimeout        time.Duration `yaml:"idle_timeout"         env:"HTTP_IDLE_TIMEOUT"`
	HealthProbeTimeout time.Duration `yaml:"health_probe_timeout" env:"HTTP_HEALTH_PROBE_TIMEOUT"`
}

// DBConfig selects the persistence engine. mysql:// → MariaDB, postgres:// →
// PostgreSQL. Both stay read/write compatible with the rAthena schema.
type DBConfig struct {
	Driver   string `yaml:"driver"   env:"DB_DRIVER" validate:"oneof=mariadb mysql postgres postgresql"`
	Host     string `yaml:"host"     env:"DB_HOST"`
	Port     int    `yaml:"port"     env:"DB_PORT" validate:"min=1,max=65535"`
	Name     string `yaml:"name"     env:"DB_NAME"`
	User     string `yaml:"user"     env:"DB_USER"`
	Password string `yaml:"password" env:"DB_PASSWORD"`
	SSLMode  string `yaml:"ssl_mode" env:"DB_SSL_MODE"`
}

// DSN renders the connection string for the selected driver.
func (d DBConfig) DSN() (string, error) {
	switch strings.ToLower(d.Driver) {
	case "mariadb", "mysql":
		return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&tls=%s",
			d.User, d.Password, d.Host, d.Port, d.Name, d.SSLMode), nil
	case "postgres", "postgresql":
		return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
			d.User, d.Password, d.Host, d.Port, d.Name, d.SSLMode), nil
	default:
		return "", fmt.Errorf("unsupported db driver %q: want mariadb|postgres", d.Driver)
	}
}

// ValkeyConfig is the cache/session store (Redis-compatible).
type ValkeyConfig struct {
	Host     string `yaml:"host"     env:"VALKEY_HOST"`
	Port     int    `yaml:"port"     env:"VALKEY_PORT"`
	Password string `yaml:"password" env:"VALKEY_PASSWORD"`
	DB       int    `yaml:"db"       env:"VALKEY_DB"`
}

// NATSConfig is the inter-service event bus (the scale-out seam).
type NATSConfig struct {
	URL string `yaml:"url" env:"NATS_URL"`
}

// GatewayConfig holds the game-protocol listeners. The client only knows the
// login port statically; char/map ports are advertised during the handoff.
type GatewayConfig struct {
	LoginHost string `yaml:"login_host" env:"GATEWAY_LOGIN_HOST"` // bind host for all listeners
	LoginPort int    `yaml:"login_port" env:"GATEWAY_LOGIN_PORT" validate:"min=1,max=65535"`
	CharHost  string `yaml:"char_host"  env:"GATEWAY_CHAR_HOST"` // advertised char-server host (client-facing)
	CharPort  int    `yaml:"char_port"  env:"GATEWAY_CHAR_PORT" validate:"min=1,max=65535"`
	MapHost   string `yaml:"map_host"   env:"GATEWAY_MAP_HOST"` // advertised map-server host (client-facing)
	MapPort   int    `yaml:"map_port"   env:"GATEWAY_MAP_PORT" validate:"min=1,max=65535"`
}

// IdentityConfig holds the login/char-server identity knobs rAthena .conf files
// populate. Mirrors pkg/ro/athenaconf.Identity so the kernel applier can feed
// these without importing this package.
type IdentityConfig struct {
	UseMD5Passwords bool `yaml:"use_md5_passwords" env:"IDENTITY_USE_MD5_PASSWORDS"`
	MaxChars        int  `yaml:"max_chars"         env:"IDENTITY_MAX_CHARS"`
}

// ZoneConfig holds the game-world loop parameters.
type ZoneConfig struct {
	TickRateHz     int    `yaml:"tick_rate_hz"     env:"ZONE_TICK_RATE_HZ" validate:"min=1,max=200"`
	ViewRangeCells int    `yaml:"view_range_cells" env:"ZONE_VIEW_RANGE_CELLS" validate:"min=1"`
	DBPath         string `yaml:"db_path"          env:"ZONE_DB_PATH"`
}

// LogConfig selects the structured logger (log/slog).
type LogConfig struct {
	Level  string `yaml:"level"  env:"LOG_LEVEL"`
	Format string `yaml:"format" env:"LOG_FORMAT"`
}

// OTelConfig carries the OpenTelemetry export settings. When Exporter is "otlp"
// and Endpoint is set, app.initOTel initializes a trace TracerProvider + OTLP
// gRPC exporter at boot (see internal/app/otel.go). Exporter "none" (default)
// leaves tracing disabled. Metrics (meter provider) are not yet exported.
type OTelConfig struct {
	Exporter    string  `yaml:"exporter"     env:"OTEL_EXPORTER" validate:"oneof=none otlp"`
	Endpoint    string  `yaml:"endpoint"     env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	ServiceName string  `yaml:"service_name" env:"OTEL_SERVICE_NAME"`
	Sampling    float64 `yaml:"sampling"     env:"OTEL_TRACES_SAMPLER_ARG" validate:"min=0,max=1"`
}

// Load reads the YAML file at path, applies defaults, overlays environment
// variables, and validates the result.
//
// A missing file is NOT fatal: the process runs on defaults + env overlay
// (12-factor/container mode). This lets an image ship no config file at all
// and be configured purely through environment variables, so no secrets are
// baked into the image. A genuinely misconfigured deployment still fails
// fast — Validate rejects missing required fields with a fatal error.
//
// Malformed input (unparseable YAML, a bad env value, or a violated required
// field) returns an error wrapping ErrConfigFatal; callers use IsFatal to
// distinguish those from a transient file-system error and exit immediately.
func Load(path string) (*Config, error) {
	cfg := defaults()

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, fatal(err))
		}
	case errors.Is(err, fs.ErrNotExist):
		// No file: fall through to defaults + env overlay.
	default:
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	if err := applyEnv(cfg); err != nil {
		return nil, fatal(fmt.Errorf("apply env: %w", err))
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

// defaults returns a Config with production-safe defaults for every field that
// is not derivable from its zero value.
func defaults() *Config {
	return &Config{
		App: AppConfig{
			Name:            "goathena",
			Environment:     "development",
			Host:            "0.0.0.0",
			Port:            8080,
			ShutdownTimeout: 15 * time.Second,
		},
		HTTP: HTTPConfig{
			Host:               "0.0.0.0",
			Port:               8080,
			ReadTimeout:        10 * time.Second,
			WriteTimeout:       10 * time.Second,
			IdleTimeout:        120 * time.Second,
			HealthProbeTimeout: 2 * time.Second,
		},
		DB:     DBConfig{Driver: "mariadb", Port: 3306, SSLMode: "disable"},
		Valkey: ValkeyConfig{Port: 6379},
		NATS:   NATSConfig{URL: "nats://127.0.0.1:4222"},
		Gateway: GatewayConfig{
			LoginHost: "0.0.0.0", LoginPort: 6900,
			CharHost: "127.0.0.1", CharPort: 6121,
			MapHost: "127.0.0.1", MapPort: 5121,
		},
		Identity: IdentityConfig{UseMD5Passwords: true, MaxChars: 9},
		Zone:     ZoneConfig{TickRateHz: 50, ViewRangeCells: 20, DBPath: "db"},
		Log:      LogConfig{Level: "info", Format: "json"},
		OTel:     OTelConfig{Exporter: "none", ServiceName: "goathena", Sampling: 1.0},
	}
}

// Validate enforces the struct `validate:` tags plus required-field checks the
// tag system cannot express (non-empty connection/address fields). Every
// violation is a fatal config error: violations are collected and returned as a
// single error wrapping ErrConfigFatal so callers can detect it via IsFatal.
func (c *Config) Validate() error {
	var errs []error

	if err := validate().Struct(c); err != nil {
		errs = append(errs, err)
	}

	// Required fields a usable config cannot run without: the DB connection
	// target and the game-protocol listen/advertise hosts. Password is optional
	// (some engines allow passwordless local auth), so it is not checked here.
	require := func(field, val string) {
		if strings.TrimSpace(val) == "" {
			errs = append(errs, fmt.Errorf("%s is required", field))
		}
	}
	require("db.host", c.DB.Host)
	require("db.name", c.DB.Name)
	require("db.user", c.DB.User)
	require("gateway.login_host", c.Gateway.LoginHost)
	require("gateway.char_host", c.Gateway.CharHost)
	require("gateway.map_host", c.Gateway.MapHost)

	if len(errs) == 0 {
		return nil
	}
	return fatal(fmt.Errorf("config: %w", errors.Join(errs...)))
}

// applyEnv overlays environment variables onto any struct field carrying an
// `env:"NAME"` tag. Unset vars are ignored. Supported kinds: string, bool, int
// (and int-derived widths), float64, time.Duration, and []string (comma-split).
func applyEnv(cfg any) error {
	v := reflect.ValueOf(cfg)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return errors.New("applyEnv: need non-nil pointer")
	}
	return walkEnv(v.Elem())
}

func walkEnv(v reflect.Value) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := v.Field(i)
		if !field.CanSet() {
			continue
		}
		// Recurse into nested structs so env tags work at any depth.
		if field.Kind() == reflect.Struct {
			if err := walkEnv(field); err != nil {
				return err
			}
			continue
		}
		name, ok := t.Field(i).Tag.Lookup("env")
		if !ok {
			continue
		}
		raw, set := os.LookupEnv(name)
		if !set {
			continue
		}
		if err := setField(field, raw, name); err != nil {
			return err
		}
	}
	return nil
}

func setField(field reflect.Value, raw, name string) error {
	switch field.Kind() { //nolint:exhaustive // only these kinds carry env tags
	case reflect.String:
		field.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("env %s=%q: %w", name, raw, err)
		}
		field.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// time.Duration is an int64 but parsed from its string form ("30s").
		if field.Type() == reflect.TypeOf(time.Duration(0)) {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("env %s=%q: %w", name, raw, err)
			}
			field.SetInt(int64(d))
			return nil
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("env %s=%q: %w", name, raw, err)
		}
		field.SetInt(n)
	case reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("env %s=%q: %w", name, raw, err)
		}
		field.SetFloat(f)
	case reflect.Slice:
		if field.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("env %s: only []string slices supported", name)
		}
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		field.Set(reflect.ValueOf(out))
	default:
		return fmt.Errorf("env %s: unsupported kind %s", name, field.Kind())
	}
	return nil
}
