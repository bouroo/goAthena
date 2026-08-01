//go:build unit

package config_test

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/config"
)

func TestLoad_ExplicitConfigFileMissingFails(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.yaml")

	t.Setenv("CONFIG_FILE", missing)

	cfg, err := config.Load()
	require.Error(t, err)
	require.Nil(t, cfg)
	require.Contains(t, err.Error(), "read config")
}

func TestLoad_ReadsConfigFile(t *testing.T) {
	// Ambient process env (e.g. an .env loaded by `task`'s `dotenv` directive)
	// can override the YAML values this test asserts on. Override every env
	// key that LeafBindings() exposes to the value the inlined YAML defines
	// (or the value the test would otherwise read). t.Setenv records the prior
	// value and restores it on test cleanup, so the global env is untouched
	// after the test. Keys absent from the YAML fall back to viper's
	// setDefaults(); we still pin them to match the default so an ambient
	// override never leaks into this test.
	neutralizeEnv(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	content := `
app:
  name: test-app
  environment: test
  host: 127.0.0.1
  port: 7000
  shutdown_timeout: 5s
http:
  host: 127.0.0.1
  port: 7001
  read_timeout: 10s
  write_timeout: 10s
  idle_timeout: 30s
  body_limit: 2M
grpc:
  host: 127.0.0.1
  port: 7002
db:
  driver: mariadb
  host: 127.0.0.1
  port: 3306
  name: testdb
  user: testuser
  password: testpass
  ssl_mode: "false"
  max_conns: 5
  max_idle_conns: 1
  max_conn_idle: 10m
  max_conn_life: 20m
  connect_timeout: 3s
valkey:
  host: 127.0.0.1
  port: 6379
  password: ""
  db: 0
nats:
  url: nats://127.0.0.1:4222
  connect_timeout: 3s
log:
  level: debug
  format: console
otel:
  exporter: none
  service_name: test-service
  sampling: 0.5
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0o600))

	t.Setenv("CONFIG_FILE", cfgPath)

	cfg, err := config.Load()
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	require.Equal(t, "test-app", cfg.App.Name)
	require.Equal(t, "test", cfg.App.Environment)
	require.Equal(t, "127.0.0.1", cfg.HTTP.Host)
	require.Equal(t, 7001, cfg.HTTP.Port)
	require.Equal(t, "127.0.0.1:7001", cfg.HTTPAddr())
	require.Equal(t, "mariadb", cfg.DB.Driver)
	require.Equal(t, "testdb", cfg.DB.Name)
	require.Equal(t, int32(5), cfg.DB.MaxConns)
	require.Equal(t, "127.0.0.1:6379", cfg.ValkeyAddr())
	require.Equal(t, "nats://127.0.0.1:4222", cfg.NATS.URL)
}

func TestLoad_OverridesFromEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	content := `
app:
  environment: test
http:
  port: 1111
db:
  host: 127.0.0.1
  port: 3306
  name: db
  user: u
  password: p
valkey:
  host: 127.0.0.1
  port: 6379
log:
  level: info
  format: json
otel:
  exporter: none
  service_name: svc
  sampling: 1.0
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0o600))
	t.Setenv("CONFIG_FILE", cfgPath)

	t.Setenv("APP_NAME", "env-app")
	t.Setenv("HTTP_PORT", "2222")
	t.Setenv("DB_NAME", "envdb")
	t.Setenv("DB_DRIVER", "postgres")
	t.Setenv("NATS_URL", "nats://env-host:4222")
	t.Setenv("OTEL_SERVICE_NAME", "env-service")
	t.Setenv("ZONE_RENEWAL", "true")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	require.Equal(t, "env-app", cfg.App.Name)
	require.Equal(t, 2222, cfg.HTTP.Port)
	require.Equal(t, "envdb", cfg.DB.Name)
	require.Equal(t, "postgres", cfg.DB.Driver)
	require.Equal(t, "nats://env-host:4222", cfg.NATS.URL)
	require.Equal(t, "env-service", cfg.OTel.ServiceName)
	require.True(t, cfg.Zone.Renewal)
	// Default db_path joins the mode subtree: renewal → .../db/re.
	require.Equal(t, "third_party/rathenaThailand/db/re", cfg.Zone.DBRoot())
}

func TestLoad_SliceEnvVariable(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	content := `
app:
  environment: test
  port: 8080
http:
  port: 8080
  read_timeout: 10s
  write_timeout: 10s
  idle_timeout: 30s
  body_limit: 1M
db:
  host: 127.0.0.1
  port: 3306
  name: db
  user: u
  password: p
valkey:
  host: 127.0.0.1
  port: 6379
log:
  level: info
  format: json
otel:
  exporter: none
  service_name: svc
  sampling: 1.0
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0o600))
	t.Setenv("CONFIG_FILE", cfgPath)

	t.Setenv("HTTP_CORS_ALLOW_ORIGINS", "https://example.com,https://app.example.com")
	t.Setenv("HTTP_CORS_ALLOW_METHODS", "GET,POST")
	t.Setenv("HTTP_CORS_ALLOW_HEADERS", "X-Custom")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	require.Equal(t, []string{"https://example.com", "https://app.example.com"}, cfg.HTTP.CORSAllowOrigins)
	require.Equal(t, []string{"GET", "POST"}, cfg.HTTP.CORSAllowMethods)
	require.Equal(t, []string{"X-Custom"}, cfg.HTTP.CORSAllowHeaders)
}

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	content := `
app:
  environment: test
http:
  port: 8080
db:
  host: 127.0.0.1
  port: 3306
  name: db
  user: u
  password: p
valkey:
  host: 127.0.0.1
  port: 6379
log:
  level: info
  format: json
otel:
  exporter: none
  service_name: svc
  sampling: 1.0
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0o600))
	t.Setenv("CONFIG_FILE", cfgPath)

	cfg, err := config.Load()
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	require.Equal(t, "goathena", cfg.App.Name)
	require.Equal(t, "mariadb", cfg.DB.Driver)
	require.Equal(t, 3306, cfg.DB.Port)
	require.Equal(t, "false", cfg.DB.SSLMode)
	require.Equal(t, "nats://localhost:4222", cfg.NATS.URL)
	require.Equal(t, 5*time.Second, cfg.HTTP.HealthProbeTimeout)
	require.Equal(t, "utf-8", cfg.Gateway.TextCodepage)
	// Default renewal is true: the vendored rathenaThailand submodule compiles
	// RENEWAL ON, so the default keeps db/ loads and formulas in parity with it.
	require.True(t, cfg.Zone.Renewal)
	// Default db_path joins the mode subtree: renewal → .../db/re.
	require.Equal(t, "third_party/rathenaThailand/db/re", cfg.Zone.DBRoot())
	// Default map_bind_addr/map_ws_bind_addr are empty, so the listen-address
	// resolvers fall back to the advertised map_addr/map_ws_addr — the
	// historical same-host-only bind-equals-advertise behavior.
	require.Empty(t, cfg.Gateway.MapBindAddr)
	require.Empty(t, cfg.Gateway.MapWSBindAddr)
	require.Equal(t, cfg.Gateway.MapAddr, cfg.Gateway.MapListenAddr())
	require.Equal(t, cfg.Gateway.MapWSAddr, cfg.Gateway.MapWSListenAddr())
}

// TestGatewayConfig_MapListenAddr covers the bind/advertise split: with no
// *BindAddr override the listener binds exactly where the advertised address
// points (historical behavior); with an override set, the listener binds
// there instead while the advertised address is untouched — the case that
// makes NAT/port-forward/Docker deployments (public hostname or remapped
// host port) actually bindable.
func TestGatewayConfig_MapListenAddr(t *testing.T) {
	t.Run("falls back to advertised address when unset", func(t *testing.T) {
		g := config.GatewayConfig{MapAddr: "play.example.com:5121", MapWSAddr: "play.example.com:6902"}
		require.Equal(t, "play.example.com:5121", g.MapListenAddr())
		require.Equal(t, "play.example.com:6902", g.MapWSListenAddr())
	})
	t.Run("uses bind override when set, advertised address unaffected", func(t *testing.T) {
		g := config.GatewayConfig{
			MapAddr:       "play.example.com:5121",
			MapBindAddr:   ":5121",
			MapWSAddr:     "play.example.com:6902",
			MapWSBindAddr: ":6902",
		}
		require.Equal(t, ":5121", g.MapListenAddr())
		require.Equal(t, ":6902", g.MapWSListenAddr())
		require.Equal(t, "play.example.com:5121", g.MapAddr)
		require.Equal(t, "play.example.com:6902", g.MapWSAddr)
	})
}

// TestDBRoot isolates DBRoot() resolution from config loading: it pins the
// mode-subtree selection for a set DBPath and the bare-subtree fallback when
// DBPath is empty. filepath.Join cleans the result, so a "./"-prefixed DBPath
// loses its leading dot — the file still resolves relative to cwd.
func TestDBRoot(t *testing.T) {
	cases := []struct {
		name string
		zone config.ZoneConfig
		want string
	}{
		{"renewal absolute", config.ZoneConfig{DBPath: "/srv/rathena/db", Renewal: true}, "/srv/rathena/db/re"},
		{"pre-re absolute", config.ZoneConfig{DBPath: "/srv/rathena/db", Renewal: false}, "/srv/rathena/db/pre-re"},
		{"renewal relative cleaned", config.ZoneConfig{DBPath: "./db", Renewal: true}, "db/re"},
		{"renewal empty bare", config.ZoneConfig{DBPath: "", Renewal: true}, "re"},
		{"pre-re empty bare", config.ZoneConfig{DBPath: "", Renewal: false}, "pre-re"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.zone.DBRoot())
		})
	}
}

func TestValidate_InvalidEnvironment(t *testing.T) {
	cfg := validConfig()
	cfg.App.Environment = "invalid"

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "config validation failed")
}

func TestValidate_InvalidDBDriver(t *testing.T) {
	cfg := validConfig()
	cfg.DB.Driver = "sqlite"

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "config validation failed")
}

func TestValidate_MaxConnsBelowMaxIdleConns(t *testing.T) {
	cfg := validConfig()
	cfg.DB.MaxConns = 1
	cfg.DB.MaxIdleConns = 5

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "DB_MAX_CONNS must be >= DB_MAX_IDLE_CONNS")
}

func TestValidate_OTLPWithoutEndpoint(t *testing.T) {
	cfg := validConfig()
	cfg.OTel.Exporter = "otlp"
	cfg.OTel.Endpoint = ""

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT is required when OTEL_EXPORTER=otlp")
}

func TestValidate_InvalidOTLPURL(t *testing.T) {
	cfg := validConfig()
	cfg.OTel.Exporter = "otlp"
	cfg.OTel.Endpoint = "://missing-scheme"

	err := cfg.Validate()
	require.Error(t, err)
}

func TestValidate_AcceptsValidConfig(t *testing.T) {
	cfg := validConfig()
	require.NoError(t, cfg.Validate())
}

// TestValidate_GatewayPacketverMinGreaterThanMax verifies the gtefield
// cross-field tag added in response to Gemini PR #88 review comment 1:
// PacketverMax must be >= PacketverMin.
func TestValidate_GatewayPacketverMinGreaterThanMax(t *testing.T) {
	cfg := validConfig()
	cfg.Gateway.PacketverMin = 20200000
	cfg.Gateway.PacketverMax = 20150000 // strictly < min → validator must reject

	err := cfg.Validate()
	require.Error(t, err, "PacketverMax < PacketverMin must fail validation")
	require.Contains(t, err.Error(), "PacketverMax",
		"error should mention the offending field, got: %v", err)
}

// TestValidate_GatewayPacketverMinEqualMax documents that the boundary case
// (Min == Max, single allowed version) is accepted by the gtefield
// comparison.
func TestValidate_GatewayPacketverMinEqualMax(t *testing.T) {
	cfg := validConfig()
	cfg.Gateway.PacketverMin = 20250604
	cfg.Gateway.PacketverMax = 20250604

	require.NoError(t, cfg.Validate())
}

func TestDBConnString_MariaDB(t *testing.T) {
	cfg := validConfig()
	cfg.DB.Driver = "mariadb"
	cfg.DB.Password = "p@ss w#rd"
	cfg.DB.SSLMode = "false"

	dsn := cfg.DBConnString()

	require.True(t, strings.HasPrefix(dsn, "goathena:p@ss w#rd@tcp(127.0.0.1:3306)/app?"),
		"unexpected DSN: %s", dsn)
	require.Contains(t, dsn, "parseTime=true")
	require.Contains(t, dsn, "tls=false")
}

func TestDBConnString_Postgres(t *testing.T) {
	cfg := validConfig()
	cfg.DB.Driver = "postgres"
	cfg.DB.Password = "p@ss w#rd"
	cfg.DB.SSLMode = "disable"

	dsn := cfg.DBConnString()

	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "goathena", parsed.User.Username())
	password, hasPassword := parsed.User.Password()
	require.True(t, hasPassword)
	require.Equal(t, "p@ss w#rd", password)
	require.Equal(t, "disable", parsed.Query().Get("sslmode"))
}

func TestGRPCAddr(t *testing.T) {
	cfg := validConfig()
	cfg.GRPC.Host = "127.0.0.1"
	cfg.GRPC.Port = 50051
	require.Equal(t, "127.0.0.1:50051", cfg.GRPCAddr())
}

func validConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{
			Name:            "test",
			Environment:     "test",
			Host:            "127.0.0.1",
			Port:            8080,
			ShutdownTimeout: 15 * time.Second,
		},
		HTTP: config.HTTPConfig{
			Host:               "127.0.0.1",
			Port:               8080,
			ReadTimeout:        15 * time.Second,
			WriteTimeout:       15 * time.Second,
			IdleTimeout:        60 * time.Second,
			BodyLimit:          "1M",
			HealthProbeTimeout: 5 * time.Second,
		},
		GRPC: config.GRPCConfig{
			Host: "127.0.0.1",
			Port: 50051,
		},
		DB: config.DBConfig{
			Driver:         "mariadb",
			Host:           "127.0.0.1",
			Port:           3306,
			Name:           "app",
			User:           "goathena",
			Password:       "goathena",
			SSLMode:        "false",
			MaxConns:       10,
			MaxIdleConns:   2,
			MaxConnIdle:    30 * time.Minute,
			MaxConnLife:    1 * time.Hour,
			ConnectTimeout: 5 * time.Second,
		},
		Valkey: config.ValkeyConfig{
			Host: "127.0.0.1",
			Port: 6379,
			DB:   0,
		},
		NATS: config.NATSConfig{
			URL:            "nats://127.0.0.1:4222",
			ConnectTimeout: 5 * time.Second,
		},
		Zone: config.ZoneConfig{
			TickRate:      50 * time.Millisecond,
			MapDir:        "./data/maps",
			DefaultMap:    "prontera",
			MoveSpeed:     150,
			ShutdownGrace: 30 * time.Second,
		},
		Gateway: config.GatewayConfig{
			TCP: config.TCPConfig{
				Addr: ":6900",
			},
			WS: config.WSConfig{
				Addr: ":6901",
				Path: "/ws/",
			},
			Packetver:    20250604,
			IdentityAddr: "localhost:50051",
			ZoneAddr:     "localhost:50052",
			MapAddr:      "localhost:5121",
			MapWSAddr:    "localhost:6902",
		},
		Assets: config.AssetsConfig{
			Enabled:    false,
			GRFDir:     "./data/grf",
			MaxCacheMB: 256,
		},
		OTel: config.OTelConfig{
			Exporter:    "none",
			Endpoint:    "http://localhost:4318",
			ServiceName: "test-service",
			Sampling:    1.0,
		},
		Log: config.LogConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// neutralizeEnv pins every env key exposed via LeafBindings() to the value
// the inlined YAML in TestLoad_ReadsConfigFile provides (or, when the YAML
// does not define the key, to the viper default). t.Setenv restores all
// prior values on test cleanup, so the host process is unaffected.
//
// Centralized here so adding a new config key only requires updating the
// `bindings` table — no scattered env neutering inside the test body.
func neutralizeEnv(t *testing.T) {
	t.Helper()
	// Each entry pairs the env var name bound in config.LeafBindings with the
	// value the YAML under TestLoad_ReadsConfigFile provides (or the viper
	// default when the key is absent). Values must match the YAML byte-for-byte
	// so the YAML file is authoritative regardless of ambient overrides.
	bindings := map[string]string{
		"APP_NAME":             "test-app",
		"APP_ENVIRONMENT":      "test",
		"APP_HOST":             "127.0.0.1",
		"APP_PORT":             "7000",
		"APP_SHUTDOWN_TIMEOUT": "5s",

		"HTTP_HOST":                 "127.0.0.1",
		"HTTP_PORT":                 "7001",
		"HTTP_READ_TIMEOUT":         "10s",
		"HTTP_WRITE_TIMEOUT":        "10s",
		"HTTP_IDLE_TIMEOUT":         "30s",
		"HTTP_BODY_LIMIT":           "2M",
		"HTTP_HEALTH_PROBE_TIMEOUT": "5s",
		"HTTP_CORS_ALLOW_ORIGINS":   "",
		"HTTP_CORS_ALLOW_METHODS":   "",
		"HTTP_CORS_ALLOW_HEADERS":   "",

		"GRPC_HOST": "127.0.0.1",
		"GRPC_PORT": "7002",

		"DB_DRIVER":          "mariadb",
		"DB_HOST":            "127.0.0.1",
		"DB_PORT":            "3306",
		"DB_NAME":            "testdb",
		"DB_USER":            "testuser",
		"DB_PASSWORD":        "testpass",
		"DB_SSL_MODE":        "false",
		"DB_MAX_CONNS":       "5",
		"DB_MAX_IDLE_CONNS":  "1",
		"DB_MAX_CONN_IDLE":   "10m",
		"DB_MAX_CONN_LIFE":   "20m",
		"DB_CONNECT_TIMEOUT": "3s",

		"VALKEY_HOST":            "127.0.0.1",
		"VALKEY_PORT":            "6379",
		"VALKEY_PASSWORD":        "",
		"VALKEY_DB":              "0",
		"VALKEY_CONNECT_TIMEOUT": "5s",

		"NATS_URL":             "nats://127.0.0.1:4222",
		"NATS_CONNECT_TIMEOUT": "3s",

		"GATEWAY_TCP_ADDR":           ":6900",
		"GATEWAY_WS_ADDR":            ":6901",
		"GATEWAY_WS_PATH":            "/ws/",
		"GATEWAY_WS_ALLOWED_ORIGINS": "",
		"GATEWAY_PACKETVER":          "20250604",
		"GATEWAY_PACKETVER_MIN":      "20000000",
		"GATEWAY_PACKETVER_MAX":      "20260000",
		"GATEWAY_IDENTITY_ADDR":      "localhost:50051",
		"GATEWAY_ZONE_ADDR":          "localhost:50052",
		"GATEWAY_MAP_ADDR":           "localhost:5121",
		"GATEWAY_MAP_BIND_ADDR":      "",
		"GATEWAY_MAP_WS_ADDR":        "localhost:6902",
		"GATEWAY_MAP_WS_BIND_ADDR":   "",
		"GATEWAY_TEXT_CODEPAGE":      "utf-8",

		"IDENTITY_USE_MD5_PASSWORDS": "false",
		"IDENTITY_MAX_CHARS":         "15",
		"IDENTITY_ITEM_DB_PATH":      "",

		// Zone defaults from viper.setDefaults (the YAML omits zone).
		"ZONE_TICK_RATE":              "50ms",
		"ZONE_RENEWAL":                "false",
		"ZONE_DB_PATH":                "./third_party/rathenaThailand/db",
		"ZONE_MAP_DIR":                "./data/maps",
		"ZONE_DEFAULT_MAP":            "prontera",
		"ZONE_MOVE_SPEED":             "150",
		"ZONE_SHUTDOWN_GRACE":         "30s",
		"ZONE_MOB_DB_PATH":            "",
		"ZONE_SKILL_DB_PATH":          "",
		"ZONE_JOB_EXP_DB_PATH":        "",
		"ZONE_ITEM_DB_PATH":           "",
		"ZONE_MOB_SPAWNS_PATH":        "",
		"ZONE_SCRIPT_DIR":             "",
		"ZONE_SCRIPT_RELOAD_INTERVAL": "0s",

		"ASSETS_ENABLED":      "false",
		"ASSETS_GRF_DIR":      "./data/grf",
		"ASSETS_MAX_CACHE_MB": "256",

		"LOG_LEVEL":  "debug",
		"LOG_FORMAT": "console",

		"OTEL_EXPORTER":               "none",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "",
		"OTEL_SERVICE_NAME":           "test-service",
		"OTEL_TRACES_SAMPLER_ARG":     "0.5",
	}
	for key, value := range bindings {
		t.Setenv(key, value)
	}
}
