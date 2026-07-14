// Package config loads application configuration from config.yaml and
// environment variables. Validation is performed by Config.Validate.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
)

// leafBinding describes a configuration leaf that is explicitly bound to an
// environment variable name.
type leafBinding struct {
	key     string
	envName string
}

// Config is the single source of truth for application configuration.
type Config struct {
	App      AppConfig      `mapstructure:"app" yaml:"app" validate:"required"`
	HTTP     HTTPConfig     `mapstructure:"http" yaml:"http" validate:"required"`
	GRPC     GRPCConfig     `mapstructure:"grpc" yaml:"grpc" validate:"required"`
	DB       DBConfig       `mapstructure:"db" yaml:"db" validate:"required"`
	Valkey   ValkeyConfig   `mapstructure:"valkey" yaml:"valkey" validate:"required"`
	NATS     NATSConfig     `mapstructure:"nats" yaml:"nats"`
	Gateway  GatewayConfig  `mapstructure:"gateway" yaml:"gateway" validate:"required"`
	Identity IdentityConfig `mapstructure:"identity" yaml:"identity" validate:"required"`
	Zone     ZoneConfig     `mapstructure:"zone" yaml:"zone" validate:"required"`
	Assets   AssetsConfig   `mapstructure:"assets" yaml:"assets"`
	OTel     OTelConfig     `mapstructure:"otel" yaml:"otel" validate:"required"`
	Log      LogConfig      `mapstructure:"log" yaml:"log" validate:"required"`
}

// AppConfig holds process-level settings.
type AppConfig struct {
	Name            string        `mapstructure:"name" yaml:"name" env:"APP_NAME" validate:"required"`
	Environment     string        `mapstructure:"environment" yaml:"environment" env:"APP_ENVIRONMENT" validate:"oneof=development staging production test"`
	Host            string        `mapstructure:"host" yaml:"host" env:"APP_HOST" validate:"ip|hostname"`
	Port            int           `mapstructure:"port" yaml:"port" env:"APP_PORT" validate:"required,min=1,max=65535"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout" yaml:"shutdown_timeout" env:"APP_SHUTDOWN_TIMEOUT" validate:"required,min=1s"`
}

// HTTPConfig holds the HTTP server settings and CORS options.
type HTTPConfig struct {
	Host               string        `mapstructure:"host" yaml:"host" env:"HTTP_HOST" validate:"ip|hostname"`
	Port               int           `mapstructure:"port" yaml:"port" env:"HTTP_PORT" validate:"required,min=1,max=65535"`
	ReadTimeout        time.Duration `mapstructure:"read_timeout" yaml:"read_timeout" env:"HTTP_READ_TIMEOUT" validate:"required,min=1s"`
	WriteTimeout       time.Duration `mapstructure:"write_timeout" yaml:"write_timeout" env:"HTTP_WRITE_TIMEOUT" validate:"required,min=1s"`
	IdleTimeout        time.Duration `mapstructure:"idle_timeout" yaml:"idle_timeout" env:"HTTP_IDLE_TIMEOUT" validate:"required,min=1s"`
	BodyLimit          string        `mapstructure:"body_limit" yaml:"body_limit" env:"HTTP_BODY_LIMIT" validate:"required"`
	HealthProbeTimeout time.Duration `mapstructure:"health_probe_timeout" yaml:"health_probe_timeout" env:"HTTP_HEALTH_PROBE_TIMEOUT" validate:"required,min=1s"`
	CORSAllowOrigins   []string      `mapstructure:"cors_allow_origins" yaml:"cors_allow_origins" env:"HTTP_CORS_ALLOW_ORIGINS"`
	CORSAllowMethods   []string      `mapstructure:"cors_allow_methods" yaml:"cors_allow_methods" env:"HTTP_CORS_ALLOW_METHODS"`
	CORSAllowHeaders   []string      `mapstructure:"cors_allow_headers" yaml:"cors_allow_headers" env:"HTTP_CORS_ALLOW_HEADERS"`
}

// GRPCConfig holds the gRPC server settings.
type GRPCConfig struct {
	Host string `mapstructure:"host" yaml:"host" env:"GRPC_HOST" validate:"ip|hostname"`
	Port int    `mapstructure:"port" yaml:"port" env:"GRPC_PORT" validate:"required,min=1,max=65535"`
}

// DBConfig holds the connection and pool settings. Driver selects the GORM
// dialector; SSLMode accepts both MariaDB/MySQL values (e.g. "false",
// "skip-verify") and PostgreSQL values, so validation is intentionally
// permissive.
type DBConfig struct {
	Driver         string        `mapstructure:"driver" yaml:"driver" env:"DB_DRIVER" validate:"required,oneof=mariadb postgres"`
	Host           string        `mapstructure:"host" yaml:"host" env:"DB_HOST" validate:"required,hostname|ip"`
	Port           int           `mapstructure:"port" yaml:"port" env:"DB_PORT" validate:"required,min=1,max=65535"`
	Name           string        `mapstructure:"name" yaml:"name" env:"DB_NAME" validate:"required"`
	User           string        `mapstructure:"user" yaml:"user" env:"DB_USER" validate:"required"`
	Password       string        `mapstructure:"password" yaml:"password" env:"DB_PASSWORD" validate:"required"`
	SSLMode        string        `mapstructure:"ssl_mode" yaml:"ssl_mode" env:"DB_SSL_MODE" validate:"required"`
	MaxConns       int32         `mapstructure:"max_conns" yaml:"max_conns" env:"DB_MAX_CONNS" validate:"required,min=1"`
	MaxIdleConns   int32         `mapstructure:"max_idle_conns" yaml:"max_idle_conns" env:"DB_MAX_IDLE_CONNS" validate:"min=0"`
	MaxConnIdle    time.Duration `mapstructure:"max_conn_idle" yaml:"max_conn_idle" env:"DB_MAX_CONN_IDLE" validate:"required,min=1s"`
	MaxConnLife    time.Duration `mapstructure:"max_conn_life" yaml:"max_conn_life" env:"DB_MAX_CONN_LIFE" validate:"required,min=1s"`
	ConnectTimeout time.Duration `mapstructure:"connect_timeout" yaml:"connect_timeout" env:"DB_CONNECT_TIMEOUT" validate:"required,min=1s"`
}

// ValkeyConfig holds the Valkey client settings.
type ValkeyConfig struct {
	Host           string        `mapstructure:"host" yaml:"host" env:"VALKEY_HOST" validate:"required,hostname|ip"`
	Port           int           `mapstructure:"port" yaml:"port" env:"VALKEY_PORT" validate:"required,min=1,max=65535"`
	Password       string        `mapstructure:"password" yaml:"password" env:"VALKEY_PASSWORD"`
	DB             int           `mapstructure:"db" yaml:"db" env:"VALKEY_DB" validate:"min=0"`
	ConnectTimeout time.Duration `mapstructure:"connect_timeout" yaml:"connect_timeout" env:"VALKEY_CONNECT_TIMEOUT" validate:"omitempty,min=1s"`
}

// NATSConfig holds the NATS pub/sub connection settings.
type NATSConfig struct {
	URL            string        `mapstructure:"url" yaml:"url" env:"NATS_URL" validate:"required"`
	ConnectTimeout time.Duration `mapstructure:"connect_timeout" yaml:"connect_timeout" env:"NATS_CONNECT_TIMEOUT" validate:"required,min=1s"`
}

// OTelConfig holds OpenTelemetry exporter settings.
type OTelConfig struct {
	Exporter    string  `mapstructure:"exporter" yaml:"exporter" env:"OTEL_EXPORTER" validate:"oneof=otlp none"`
	Endpoint    string  `mapstructure:"endpoint" yaml:"endpoint" env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	ServiceName string  `mapstructure:"service_name" yaml:"service_name" env:"OTEL_SERVICE_NAME" validate:"required"`
	Sampling    float64 `mapstructure:"sampling" yaml:"sampling" env:"OTEL_TRACES_SAMPLER_ARG" validate:"min=0,max=1"`
}

// GatewayConfig configures the ingress gateway service (DEL-01). IdentityAddr
// is the gRPC target for the identity service (e.g. "identity:50051"); the
// gateway forwards every decoded CA_LOGIN there.
//
// MapAddr is the "host:port" address of the zone service that the gateway
// advertises to the client in HC_NOTIFY_ZONESVR (cmd 0x0ac5). It is not the
// gateway's own listening address — it is the destination the client opens
// a new TCP connection to after CH_SELECT_CHAR. Defaults to "localhost:5121"
// (the Thai Classic map port).
type GatewayConfig struct {
	TCP          TCPConfig `mapstructure:"tcp" yaml:"tcp" validate:"required"`
	WS           WSConfig  `mapstructure:"ws" yaml:"ws" validate:"required"`
	Packetver    int       `mapstructure:"packetver" yaml:"packetver" env:"GATEWAY_PACKETVER" validate:"min=20000000,max=20260000"`
	IdentityAddr string    `mapstructure:"identity_addr" yaml:"identity_addr" env:"GATEWAY_IDENTITY_ADDR" validate:"required"`
	// ZoneAddr is the gRPC endpoint of the zone service (DEL-03). The
	// gateway forwards decoded map-server packets (CZ_ENTER,
	// CZ_REQUEST_MOVE) here.
	ZoneAddr string `mapstructure:"zone_addr" yaml:"zone_addr" env:"GATEWAY_ZONE_ADDR" validate:"required"`
	// MapAddr is the "host:port" address of the zone service that the gateway
	// advertises to the client in HC_NOTIFY_ZONESVR (cmd 0x0ac5). It is not the
	// gateway's own listening address — it is the destination the client opens
	// a new TCP connection to after CH_SELECT_CHAR. Defaults to "localhost:5121"
	// (the Thai Classic map port).
	MapAddr string `mapstructure:"map_addr" yaml:"map_addr" env:"GATEWAY_MAP_ADDR" validate:"required"`
}

// TCPConfig holds the gnet TCP listener settings for the kRO ingress port.
type TCPConfig struct {
	Addr string `mapstructure:"addr" yaml:"addr" env:"GATEWAY_TCP_ADDR" validate:"required"`
}

// WSConfig holds the HTTP/WebSocket listener settings for the roBrowser
// ingress port. Path is the URL path on which the upgrade handler is
// mounted (e.g. "/ws/"); the HTTP server only accepts WebSocket upgrades
// at that path and returns 404 for everything else.
//
// AllowedOrigins is the CSWSH (Cross-Site WebSocket Hijacking) origin
// allowlist passed to websocket.AcceptOptions.OriginPatterns. Entries
// follow coder/websocket glob semantics (path.Match, case-insensitive);
// scheme-prefixed patterns (e.g. "https://example.com") match the full
// "scheme://host"; bare host patterns (e.g. "*.example.com") match the
// origin host. When empty, origin verification is disabled and a warning
// is logged per connection — preserve backward-compatible dev behavior
// while making production misconfiguration visible.
type WSConfig struct {
	Addr           string   `mapstructure:"addr" yaml:"addr" env:"GATEWAY_WS_ADDR" validate:"required"`
	Path           string   `mapstructure:"path" yaml:"path" env:"GATEWAY_WS_PATH" validate:"required"`
	AllowedOrigins []string `mapstructure:"allowed_origins" yaml:"allowed_origins" env:"GATEWAY_WS_ALLOWED_ORIGINS"`
}

// IdentityConfig configures the identity service (DEL-02). UseMD5Passwords
// is the deployment-wide `use_md5_passwds` bit (loginclif.cpp:279-281) and
// must match the encoding declared on every LoginRequest.Method; the
// service rejects mismatches with AuthRejected (login.cpp:233).
// MaxChars caps the character roster (effective = max(account.character_slots,
// MIN_CHARS)); the default of 15 matches PACKETVER >= 20100413. ItemDBPath is
// the optional rAthena item_db YAML file used to resolve inventory weights.
type IdentityConfig struct {
	UseMD5Passwords bool   `mapstructure:"use_md5_passwords" yaml:"use_md5_passwords" env:"IDENTITY_USE_MD5_PASSWORDS"`
	MaxChars        int    `mapstructure:"max_chars" yaml:"max_chars" env:"IDENTITY_MAX_CHARS" validate:"min=0,max=15"`
	ItemDBPath      string `mapstructure:"item_db_path" yaml:"item_db_path" env:"IDENTITY_ITEM_DB_PATH" validate:"omitempty"`
}

// LogConfig holds the zerolog settings.
type LogConfig struct {
	Level  string `mapstructure:"level" yaml:"level" env:"LOG_LEVEL" validate:"oneof=trace debug info warn error fatal panic"`
	Format string `mapstructure:"format" yaml:"format" env:"LOG_FORMAT" validate:"oneof=json console"`
}

// ZoneConfig configures the zone service (DEL-03). TickRate drives the
// physics loop (50 ms = 20 Hz in production; ≤10 ms upper bound enforced).
// MapDir is the on-disk root of the .gat/.rsw map files; DefaultMap is the
// initial map loaded at startup when no zone is provided. MoveSpeed is the
// baseline ms-per-cell used when an entity has no status data. ShutdownGrace
// is the cooldown before Agones Shutdown after the last player leaves.
// MobDBPath is the rAthena mob_db.yml (version 5) used to resolve mob stats;
// MobSpawnsPath is the per-map spawn-group YAML applied at startup. Both are
// optional: a missing or unreadable file logs a warning and disables mob
// spawning rather than failing zone boot. ScriptDir is the on-disk root of
// the NPC .txt script corpus loaded once at zone startup and compiled into
// the in-memory script engine. ScriptReloadInterval drives hot reload of
// that corpus; 0 disables scheduled reloads. Like MobDBPath, ScriptDir is
// optional: an empty or unreadable directory logs a warning and leaves the
// engine holding an empty compiled set rather than failing zone boot.
type ZoneConfig struct {
	TickRate             time.Duration `mapstructure:"tick_rate" yaml:"tick_rate" env:"ZONE_TICK_RATE" validate:"required,min=10ms"`
	MapDir               string        `mapstructure:"map_dir" yaml:"map_dir" env:"ZONE_MAP_DIR" validate:"required"`
	DefaultMap           string        `mapstructure:"default_map" yaml:"default_map" env:"ZONE_DEFAULT_MAP"`
	MoveSpeed            int           `mapstructure:"move_speed" yaml:"move_speed" env:"ZONE_MOVE_SPEED" validate:"min=50,max=1000"`
	ShutdownGrace        time.Duration `mapstructure:"shutdown_grace" yaml:"shutdown_grace" env:"ZONE_SHUTDOWN_GRACE" validate:"min=0"`
	MobDBPath            string        `mapstructure:"mob_db_path" yaml:"mob_db_path" env:"ZONE_MOB_DB_PATH" validate:"omitempty"`
	MobSpawnsPath        string        `mapstructure:"mob_spawns_path" yaml:"mob_spawns_path" env:"ZONE_MOB_SPAWNS_PATH" validate:"omitempty"`
	ScriptDir            string        `mapstructure:"script_dir" yaml:"script_dir" env:"ZONE_SCRIPT_DIR" validate:"omitempty"`
	ScriptReloadInterval time.Duration `mapstructure:"script_reload_interval" yaml:"script_reload_interval" env:"ZONE_SCRIPT_RELOAD_INTERVAL" validate:"omitempty,min=0"`
}

// AssetsConfig configures the GRF-backed HTTP asset server that serves
// game files (sprites, textures, maps, Lua scripts) to roBrowser.
// When Enabled is false, the asset server is not mounted.
type AssetsConfig struct {
	Enabled    bool   `mapstructure:"enabled" yaml:"enabled" env:"ASSETS_ENABLED"`
	GRFDir     string `mapstructure:"grf_dir" yaml:"grf_dir" env:"ASSETS_GRF_DIR" validate:"required_with=Enabled"`
	MaxCacheMB int64  `mapstructure:"max_cache_mb" yaml:"max_cache_mb" env:"ASSETS_MAX_CACHE_MB" validate:"min=0"`
}

// validate is the package-level validator instance.
var validate = validator.New()

// Load reads config.yaml (or CONFIG_FILE) and environment variables and returns
// a typed configuration. Environment variables are unprefixed and use
// SCREAMING_SNAKE names matching the nested config keys (e.g. app.name ->
// APP_NAME, http.port -> HTTP_PORT).
func Load() (*Config, error) {
	v := viper.NewWithOptions(viper.ExperimentalBindStruct())

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	configFileExplicit := false
	if configFile, ok := os.LookupEnv("CONFIG_FILE"); ok && configFile != "" {
		configFileExplicit = true
		absPath, err := filepath.Abs(configFile)
		if err != nil {
			return nil, fmt.Errorf("resolve CONFIG_FILE path %q: %w", configFile, err)
		}
		v.SetConfigFile(absPath)
	}

	setDefaults(v)

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	for _, binding := range leafBindings() {
		if err := v.BindEnv(binding.key, binding.envName); err != nil {
			return nil, fmt.Errorf("bind env %s to key %s: %w", binding.envName, binding.key, err)
		}
	}

	if err := v.ReadInConfig(); err != nil {
		if configFileExplicit || !errorsIsConfigNotFound(err) {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	return &cfg, nil
}

// Validate runs go-playground/validator and cross-section checks.
func (c *Config) Validate() error {
	if err := validate.Struct(c); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	if c.OTel.Exporter == "otlp" && c.OTel.Endpoint == "" {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT is required when OTEL_EXPORTER=otlp")
	}

	if c.OTel.Exporter == "otlp" {
		if _, err := url.Parse(c.OTel.Endpoint); err != nil {
			return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT is invalid: %w", err)
		}
	}

	if c.DB.MaxConns < c.DB.MaxIdleConns {
		return fmt.Errorf("DB_MAX_CONNS must be >= DB_MAX_IDLE_CONNS")
	}

	return nil
}

// HTTPAddr returns the HTTP listen address.
func (c *Config) HTTPAddr() string {
	return net.JoinHostPort(c.HTTP.Host, strconv.Itoa(c.HTTP.Port))
}

// GRPCAddr returns the gRPC listen address.
func (c *Config) GRPCAddr() string {
	return net.JoinHostPort(c.GRPC.Host, strconv.Itoa(c.GRPC.Port))
}

// DBConnString returns a driver-appropriate DSN. For MariaDB the MySQL DSN
// format "user:pass@tcp(host:port)/db?tls=..." is used; for PostgreSQL a
// postgres:// URL is returned. golang-migrate expects the same formats.
func (c *Config) DBConnString() string {
	switch c.DB.Driver {
	case "postgres":
		u := url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(c.DB.User, c.DB.Password),
			Host:   net.JoinHostPort(c.DB.Host, strconv.Itoa(c.DB.Port)),
			Path:   "/" + c.DB.Name,
		}
		q := u.Query()
		q.Set("sslmode", c.DB.SSLMode)
		u.RawQuery = q.Encode()
		return u.String()
	default: // "mariadb"
		return fmt.Sprintf(
			"%s:%s@tcp(%s:%d)/%s?parseTime=true&tls=%s",
			c.DB.User, c.DB.Password,
			c.DB.Host, c.DB.Port,
			c.DB.Name, c.DB.SSLMode,
		)
	}
}

// ValkeyAddr returns the Valkey server address.
func (c *Config) ValkeyAddr() string {
	return net.JoinHostPort(c.Valkey.Host, strconv.Itoa(c.Valkey.Port))
}

const defaultHost = "0.0.0.0"

// setDefaults registers default values used when a key is missing from both
// config file and environment.
func setDefaults(v *viper.Viper) {
	defaults := map[string]any{
		"app.name":             "goathena",
		"app.environment":      "development",
		"app.host":             defaultHost,
		"app.port":             8080,
		"app.shutdown_timeout": 15 * time.Second,

		"http.host":                 defaultHost,
		"http.port":                 8080,
		"http.read_timeout":         15 * time.Second,
		"http.write_timeout":        15 * time.Second,
		"http.idle_timeout":         60 * time.Second,
		"http.body_limit":           "1M",
		"http.health_probe_timeout": 5 * time.Second,
		"http.cors_allow_origins":   []string{},
		"http.cors_allow_methods":   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		"http.cors_allow_headers":   []string{"Authorization", "Content-Type", "X-Request-ID"},

		"grpc.host": defaultHost,
		"grpc.port": 50051,

		"db.driver":          "mariadb",
		"db.port":            3306,
		"db.ssl_mode":        "false",
		"db.max_conns":       10,
		"db.max_idle_conns":  2,
		"db.max_conn_idle":   30 * time.Minute,
		"db.max_conn_life":   1 * time.Hour,
		"db.connect_timeout": 5 * time.Second,

		"valkey.db":              0,
		"valkey.connect_timeout": 5 * time.Second,

		"nats.url":             "nats://localhost:4222",
		"nats.connect_timeout": 5 * time.Second,

		"gateway.tcp.addr":           ":6900",
		"gateway.ws.addr":            ":6901",
		"gateway.ws.path":            "/ws/",
		"gateway.ws.allowed_origins": []string{},
		"gateway.packetver":          20250604,
		"gateway.identity_addr":      "localhost:50051",
		"gateway.zone_addr":          "localhost:50052",
		"gateway.map_addr":           "localhost:5121",

		"identity.use_md5_passwords": false,
		"identity.max_chars":         15,
		"identity.item_db_path":      "",

		"zone.tick_rate":      50 * time.Millisecond,
		"zone.map_dir":        "./data/maps",
		"zone.default_map":    "prontera",
		"zone.move_speed":     150,
		"zone.shutdown_grace": 30 * time.Second,

		"assets.enabled":      false,
		"assets.grf_dir":      "./data/grf",
		"assets.max_cache_mb": 256,

		"otel.exporter":     "none",
		"otel.service_name": "goathena",
		"otel.sampling":     1.0,

		"log.level":  "info",
		"log.format": "json",
	}

	for key, value := range defaults {
		v.SetDefault(key, value)
	}
}

// leafBindings returns the explicit env-var-to-config-key bindings used by
// Load.
func leafBindings() []leafBinding {
	return []leafBinding{
		{"app.name", "APP_NAME"},
		{"app.environment", "APP_ENVIRONMENT"},
		{"app.host", "APP_HOST"},
		{"app.port", "APP_PORT"},
		{"app.shutdown_timeout", "APP_SHUTDOWN_TIMEOUT"},

		{"http.host", "HTTP_HOST"},
		{"http.port", "HTTP_PORT"},
		{"http.read_timeout", "HTTP_READ_TIMEOUT"},
		{"http.write_timeout", "HTTP_WRITE_TIMEOUT"},
		{"http.idle_timeout", "HTTP_IDLE_TIMEOUT"},
		{"http.body_limit", "HTTP_BODY_LIMIT"},
		{"http.health_probe_timeout", "HTTP_HEALTH_PROBE_TIMEOUT"},
		{"http.cors_allow_origins", "HTTP_CORS_ALLOW_ORIGINS"},
		{"http.cors_allow_methods", "HTTP_CORS_ALLOW_METHODS"},
		{"http.cors_allow_headers", "HTTP_CORS_ALLOW_HEADERS"},

		{"grpc.host", "GRPC_HOST"},
		{"grpc.port", "GRPC_PORT"},

		{"db.driver", "DB_DRIVER"},
		{"db.host", "DB_HOST"},
		{"db.port", "DB_PORT"},
		{"db.name", "DB_NAME"},
		{"db.user", "DB_USER"},
		{"db.password", "DB_PASSWORD"},
		{"db.ssl_mode", "DB_SSL_MODE"},
		{"db.max_conns", "DB_MAX_CONNS"},
		{"db.max_idle_conns", "DB_MAX_IDLE_CONNS"},
		{"db.max_conn_idle", "DB_MAX_CONN_IDLE"},
		{"db.max_conn_life", "DB_MAX_CONN_LIFE"},
		{"db.connect_timeout", "DB_CONNECT_TIMEOUT"},

		{"valkey.host", "VALKEY_HOST"},
		{"valkey.port", "VALKEY_PORT"},
		{"valkey.password", "VALKEY_PASSWORD"},
		{"valkey.db", "VALKEY_DB"},
		{"valkey.connect_timeout", "VALKEY_CONNECT_TIMEOUT"},

		{"nats.url", "NATS_URL"},
		{"nats.connect_timeout", "NATS_CONNECT_TIMEOUT"},

		{"gateway.tcp.addr", "GATEWAY_TCP_ADDR"},
		{"gateway.ws.addr", "GATEWAY_WS_ADDR"},
		{"gateway.ws.path", "GATEWAY_WS_PATH"},
		{"gateway.ws.allowed_origins", "GATEWAY_WS_ALLOWED_ORIGINS"},
		{"gateway.packetver", "GATEWAY_PACKETVER"},
		{"gateway.identity_addr", "GATEWAY_IDENTITY_ADDR"},
		{"gateway.zone_addr", "GATEWAY_ZONE_ADDR"},
		{"gateway.map_addr", "GATEWAY_MAP_ADDR"},

		{"identity.use_md5_passwords", "IDENTITY_USE_MD5_PASSWORDS"},
		{"identity.max_chars", "IDENTITY_MAX_CHARS"},
		{"identity.item_db_path", "IDENTITY_ITEM_DB_PATH"},

		{"zone.tick_rate", "ZONE_TICK_RATE"},
		{"zone.map_dir", "ZONE_MAP_DIR"},
		{"zone.default_map", "ZONE_DEFAULT_MAP"},
		{"zone.move_speed", "ZONE_MOVE_SPEED"},
		{"zone.shutdown_grace", "ZONE_SHUTDOWN_GRACE"},
		{"zone.mob_db_path", "ZONE_MOB_DB_PATH"},
		{"zone.mob_spawns_path", "ZONE_MOB_SPAWNS_PATH"},

		{"assets.enabled", "ASSETS_ENABLED"},
		{"assets.grf_dir", "ASSETS_GRF_DIR"},
		{"assets.max_cache_mb", "ASSETS_MAX_CACHE_MB"},

		{"log.level", "LOG_LEVEL"},
		{"log.format", "LOG_FORMAT"},

		{"otel.exporter", "OTEL_EXPORTER"},
		{"otel.endpoint", "OTEL_EXPORTER_OTLP_ENDPOINT"},
		{"otel.service_name", "OTEL_SERVICE_NAME"},
		{"otel.sampling", "OTEL_TRACES_SAMPLER_ARG"},
	}
}

// errorsIsConfigNotFound reports whether err is a viper config file not found
// error. It uses errors.As to avoid string comparison.
func errorsIsConfigNotFound(err error) bool {
	var configFileNotFoundError viper.ConfigFileNotFoundError
	return err != nil && errors.As(err, &configFileNotFoundError)
}
