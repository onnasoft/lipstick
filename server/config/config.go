package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type ProxyConfig struct {
	Address string `yaml:"address"`
}

type ManagerConfig struct {
	Address string `yaml:"address"`
}

type AdminConfig struct {
	Address string `yaml:"address"`
}

type NatsConfig struct {
	URL string `yaml:"url"`
}

type TLSConfig struct {
	CertificatePath string `yaml:"certificate_path"`
	KeyPath         string `yaml:"key_path"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	SSLMode  string `yaml:"ssl_mode"`
}

type RedisConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	Password     string `yaml:"password"`
	Database     int    `yaml:"database"`
	PoolSize     int    `yaml:"pool_size"`
	MinIdleConns int    `yaml:"min_idle_conns"`
	PoolTimeout  int    `yaml:"pool_timeout"`
}

type AppConfig struct {
	AdminSecretKey string         `yaml:"admin_secret_key"`
	Proxy          ProxyConfig    `yaml:"proxy"`
	Manager        ManagerConfig  `yaml:"manager"`
	Admin          AdminConfig    `yaml:"admin"`
	TLS            TLSConfig      `yaml:"tls"`
	Database       DatabaseConfig `yaml:"database"`
	Redis          RedisConfig    `yaml:"redis"`
	Nats           NatsConfig     `yaml:"nats"`
}

const minAdminSecretKeyLength = 16

// placeholderPrefix marks the value used in config.yml.example. Refusing it
// stops a copied example file from starting with a public key.
const placeholderPrefix = "change-me"

var validSSLModes = map[string]bool{
	"disable":     true,
	"allow":       true,
	"prefer":      true,
	"require":     true,
	"verify-ca":   true,
	"verify-full": true,
}

// Validate checks the configuration and returns every problem found, so the
// operator can fix them all in one pass.
func (c AppConfig) Validate() error {
	var errs []error

	if len(c.AdminSecretKey) < minAdminSecretKeyLength {
		errs = append(errs, fmt.Errorf("admin_secret_key is required and must be at least %d characters", minAdminSecretKeyLength))
	} else if strings.HasPrefix(c.AdminSecretKey, placeholderPrefix) {
		errs = append(errs, errors.New("admin_secret_key still has the example value; generate one with: openssl rand -hex 24"))
	}

	listeners := []struct{ name, addr string }{
		{"proxy.address", c.Proxy.Address},
		{"manager.address", c.Manager.Address},
		{"admin.address", c.Admin.Address},
	}
	seen := map[string]string{}
	for _, l := range listeners {
		if err := validateListenAddress(l.addr); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", l.name, err))
			continue
		}
		if other, ok := seen[l.addr]; ok {
			errs = append(errs, fmt.Errorf("%s and %s both use %q", other, l.name, l.addr))
		}
		seen[l.addr] = l.name
	}

	if c.Database.Host == "" {
		errs = append(errs, errors.New("database.host is required"))
	}
	if c.Database.User == "" {
		errs = append(errs, errors.New("database.user is required"))
	}
	if c.Database.Database == "" {
		errs = append(errs, errors.New("database.database is required"))
	}
	if c.Database.Port < 1 || c.Database.Port > 65535 {
		errs = append(errs, fmt.Errorf("database.port %d is out of range (1-65535)", c.Database.Port))
	}
	if !validSSLModes[c.Database.SSLMode] {
		errs = append(errs, fmt.Errorf("database.ssl_mode %q is not valid (use disable, allow, prefer, require, verify-ca or verify-full)", c.Database.SSLMode))
	}

	if err := validateNatsURL(c.Nats.URL); err != nil {
		errs = append(errs, fmt.Errorf("nats.url: %w", err))
	}

	// TLS is mandatory: the tunnel never carries plain-text traffic. If the
	// files do not exist yet, they are generated on startup (see Prepare).
	if c.TLS.CertificatePath == "" {
		errs = append(errs, errors.New("tls.certificate_path is required"))
	}
	if c.TLS.KeyPath == "" {
		errs = append(errs, errors.New("tls.key_path is required"))
	}

	return errors.Join(errs...)
}

func validateListenAddress(addr string) error {
	if addr == "" {
		return errors.New("is empty")
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q is not a host:port address (use \":5050\" to listen on all interfaces)", addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port %q is not in range 1-65535", port)
	}
	return nil
}

func validateNatsURL(raw string) error {
	if raw == "" {
		return errors.New("is required because clients are dispatched through NATS")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not a valid URL: %w", raw, err)
	}
	if u.Scheme != "nats" && u.Scheme != "tls" {
		return fmt.Errorf("%q must start with nats:// or tls://", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("%q has no host", raw)
	}
	return nil
}

var (
	loadOnce  sync.Once
	appConfig AppConfig
	loadErr   error
)

// GetConfig loads and validates the configuration once. Later calls return the
// same result, so callers can check the error instead of the process exiting.
func GetConfig() (AppConfig, error) {
	loadOnce.Do(func() {
		appConfig, loadErr = loadConfig()
	})
	return appConfig, loadErr
}

func loadConfig() (AppConfig, error) {
	var configPath string
	var adminAddress, managerAddress, proxyAddress, adminSecretKey, tlsCert, tlsKey string
	var redisHost, redisPassword, rabbitMQHost, rabbitMQUser, rabbitMQPassword, rabbitMQVHost string
	var redisPort, redisDatabase, redisPoolSize, redisMinIdleConns, redisPoolTimeout, rabbitMQPort int
	var dbHost, dbUser, dbPassword, dbName, dbSSLMode string
	var dbPort int
	var natsURL string

	defaultConfig := AppConfig{
		Admin: AdminConfig{
			// Loopback only: the admin API should not be reachable from the network.
			Address: "127.0.0.1:5052",
		},
		Manager: ManagerConfig{
			Address: ":5051",
		},
		Proxy: ProxyConfig{
			Address: ":5050",
		},
		Redis: RedisConfig{
			Host:         "localhost",
			Port:         6379,
			Database:     0,
			PoolSize:     10,
			MinIdleConns: 3,
			PoolTimeout:  30,
		},
		Database: DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "",
			Database: "app_db",
			SSLMode:  "disable",
		},
		Nats: NatsConfig{
			URL: "nats://localhost:4222",
		},
		TLS: TLSConfig{
			// Relative paths are resolved against the directory of the
			// configuration file.
			CertificatePath: "certs/server.crt",
			KeyPath:         "certs/server.key",
		},
	}

	flag.StringVar(&configPath, "c", "/etc/lipstick/config.yml", "Path to the configuration file")
	flag.StringVar(&adminAddress, "admin-addr", "", "Address for the admin API")
	flag.StringVar(&managerAddress, "manager-addr", "", "Address for WebSocket manager connections")
	flag.StringVar(&proxyAddress, "proxy-addr", "", "Address for the proxy")
	flag.StringVar(&adminSecretKey, "admin-secret", "", "Secret key for admin API authorization")
	flag.StringVar(&tlsCert, "tls-cert", "", "Path to the TLS certificate")
	flag.StringVar(&tlsKey, "tls-key", "", "Path to the TLS key")
	flag.StringVar(&redisHost, "redis-host", "", "Redis host")
	flag.IntVar(&redisPort, "redis-port", 0, "Redis port")
	flag.StringVar(&redisPassword, "redis-password", "", "Redis password")
	flag.IntVar(&redisDatabase, "redis-db", 0, "Redis database index")
	flag.IntVar(&redisPoolSize, "redis-pool-size", 0, "Redis pool size")
	flag.IntVar(&redisMinIdleConns, "redis-min-idle-conns", 0, "Redis minimum idle connections")
	flag.IntVar(&redisPoolTimeout, "redis-pool-timeout", 0, "Redis pool timeout in seconds")
	flag.StringVar(&rabbitMQHost, "rabbitmq-host", "", "RabbitMQ host")
	flag.IntVar(&rabbitMQPort, "rabbitmq-port", 0, "RabbitMQ port")
	flag.StringVar(&rabbitMQUser, "rabbitmq-user", "", "RabbitMQ user")
	flag.StringVar(&rabbitMQPassword, "rabbitmq-password", "", "RabbitMQ password")
	flag.StringVar(&rabbitMQVHost, "rabbitmq-vhost", "", "RabbitMQ virtual host")
	flag.StringVar(&dbHost, "db-host", "", "Database host")
	flag.IntVar(&dbPort, "db-port", 0, "Database port")
	flag.StringVar(&dbUser, "db-user", "", "Database user")
	flag.StringVar(&dbPassword, "db-password", "", "Database password")
	flag.StringVar(&dbName, "db-name", "", "Database name")
	flag.StringVar(&dbSSLMode, "db-ssl-mode", "", "Database SSL mode")
	flag.StringVar(&natsURL, "nats-url", "", "NATS URL")

	flag.Parse()

	return loadConfigFrom(configPath, defaultConfig)
}

// loadConfigFrom reads the YAML file over the given defaults, then validates
// the result.
func loadConfigFrom(configPath string, defaultConfig AppConfig) (AppConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return AppConfig{}, fmt.Errorf("cannot read configuration file %q: %w (use -c to point to another file)", configPath, err)
	}

	// KnownFields makes misspelled or unsupported keys an error instead of
	// being silently ignored.
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&defaultConfig); err != nil && !errors.Is(err, io.EOF) {
		return AppConfig{}, fmt.Errorf("invalid configuration file %q: %w", configPath, err)
	}

	// Relative TLS paths are resolved against the configuration file's
	// directory, so the server behaves the same regardless of the working dir.
	baseDir := filepath.Dir(configPath)
	defaultConfig.TLS.CertificatePath = resolvePath(baseDir, defaultConfig.TLS.CertificatePath)
	defaultConfig.TLS.KeyPath = resolvePath(baseDir, defaultConfig.TLS.KeyPath)

	if err := defaultConfig.Validate(); err != nil {
		return AppConfig{}, fmt.Errorf("invalid configuration file %q:\n%w", configPath, err)
	}

	return defaultConfig, nil
}

func resolvePath(baseDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(baseDir, p)
}
