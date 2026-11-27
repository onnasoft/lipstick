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

// Config is the client configuration. Values come from the YAML file, and
// command-line flags override them when given.
type Config struct {
	APISecret string   `yaml:"api_secret"` // Key of the domain, created with the server admin API
	ServerURL string   `yaml:"server_url"` // Server manager URL. Always https://
	ProxyPass []string `yaml:"proxy_pass"` // Local services to expose, host:port
	CAFile    string   `yaml:"ca_file"`    // PEM file with the server certificate or its CA (needed for self-signed servers)
	// IntranetPort asks the server to reserve this plain-TCP port for the domain,
	// inside the range its policy allows. 0 means no intranet port.
	IntranetPort int `yaml:"intranet_port"`
	// IntranetUDPPort asks the server to reserve this plain-UDP port. Datagrams
	// that arrive on it are sent to UDPTarget, a local UDP service. 0 means none.
	IntranetUDPPort int `yaml:"intranet_udp_port"`
	// UDPTarget is the local UDP service, host:port, for datagrams from the server.
	UDPTarget string `yaml:"udp_target"`
	// UDPTargetTLS makes the connection to udp_target DTLS, verified like an https
	// target: the service must present a certificate its name matches.
	UDPTargetTLS bool `yaml:"udp_target_tls"`
}

const (
	defaultServerURL = "https://localhost:5051"
	defaultProxyPass = "127.0.0.1:8082"
)

// checkOnly is set by -check: validate the configuration, then exit.
var checkOnly bool

// CheckOnly reports whether -check was given.
func CheckOnly() bool { return checkOnly }

var (
	loadOnce sync.Once
	config   *Config
	loadErr  error
)

// GetConfig loads and validates the configuration once. Later calls return the
// same result.
func GetConfig() (*Config, error) {
	loadOnce.Do(func() {
		config, loadErr = loadConfig(os.Args[1:])
	})
	return config, loadErr
}

// loadConfig merges defaults, the YAML file and flags, then validates the
// result. It is a function of its arguments so it can be tested.
func loadConfig(args []string) (*Config, error) {
	fs := flag.NewFlagSet("lipstick-client", flag.ContinueOnError)

	execDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		execDir = "."
	}
	defaultConfigPath := filepath.Join(execDir, "config.client.yml")

	var configPath, serverURL, proxyPass, apiSecret, caFile string
	var intranetPort, intranetUDPPort int
	var udpTarget string
	var check bool
	fs.StringVar(&configPath, "c", defaultConfigPath, "Path to the configuration file")
	fs.StringVar(&serverURL, "s", "", "Server manager URL, https://host:port (default "+defaultServerURL+")")
	fs.StringVar(&proxyPass, "p", "", "Local services to expose, host:port, separated by spaces (default "+defaultProxyPass+")")
	fs.StringVar(&apiSecret, "k", "", "Domain API key")
	fs.StringVar(&caFile, "ca", "", "PEM file with the server certificate or its CA")
	fs.BoolVar(&check, "check", false, "Validate the configuration and exit, without connecting")
	fs.IntVar(&intranetPort, "intranet-port", 0, "Ask the server to reserve this plain-TCP port for the domain (intranet services only)")
	fs.IntVar(&intranetUDPPort, "intranet-udp-port", 0, "Ask the server to reserve this plain-UDP port for the domain (intranet services only)")
	fs.StringVar(&udpTarget, "udp-target", "", "Local UDP service, host:port, for datagrams that arrive on the intranet UDP port")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	checkOnly = check

	explicitConfig := false
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
		if f.Name == "c" {
			explicitConfig = true
		}
	})

	cfg := &Config{
		ServerURL: defaultServerURL,
		ProxyPass: []string{defaultProxyPass},
	}

	content, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		// KnownFields turns misspelled keys into errors.
		decoder := yaml.NewDecoder(bytes.NewReader(content))
		decoder.KnownFields(true)
		if err := decoder.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("invalid configuration file %q: %w", configPath, err)
		}
	case explicitConfig || !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("cannot read configuration file %q: %w", configPath, err)
	}

	// Flags override the file when they are set.
	if serverURL != "" {
		cfg.ServerURL = serverURL
	}
	if proxyPass != "" {
		cfg.ProxyPass = strings.Fields(proxyPass)
	}
	if apiSecret != "" {
		cfg.APISecret = apiSecret
	}
	if caFile != "" {
		cfg.CAFile = caFile
	}
	if set["intranet-port"] {
		cfg.IntranetPort = intranetPort
	}
	if set["intranet-udp-port"] {
		cfg.IntranetUDPPort = intranetUDPPort
	}
	if set["udp-target"] {
		cfg.UDPTarget = udpTarget
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid client configuration:\n%w", err)
	}
	return cfg, nil
}

// Validate reports every problem in the configuration at once.
func (c *Config) Validate() error {
	var errs []error

	if c.IntranetPort < 0 || c.IntranetPort > 65535 {
		errs = append(errs, fmt.Errorf("intranet_port %d is not a port number", c.IntranetPort))
	}
	if c.IntranetUDPPort < 0 || c.IntranetUDPPort > 65535 {
		errs = append(errs, fmt.Errorf("intranet_udp_port %d is not a port number", c.IntranetUDPPort))
	}
	if c.UDPTarget != "" {
		if _, _, err := net.SplitHostPort(c.UDPTarget); err != nil {
			errs = append(errs, fmt.Errorf("udp_target %q must be host:port", c.UDPTarget))
		}
	}
	if c.UDPTargetTLS && c.UDPTarget == "" {
		errs = append(errs, errors.New("udp_target_tls needs udp_target"))
	}
	if c.IntranetUDPPort > 0 && c.UDPTarget == "" {
		errs = append(errs, errors.New("intranet_udp_port needs udp_target: the local UDP service the datagrams go to"))
	}

	if c.APISecret == "" {
		errs = append(errs, errors.New("api_secret is required (-k or api_secret in the YAML file)"))
	}

	if err := validateServerURL(c.ServerURL); err != nil {
		errs = append(errs, fmt.Errorf("server_url: %w", err))
	}

	if len(c.ProxyPass) == 0 {
		errs = append(errs, errors.New("proxy_pass needs at least one local service (-p host:port)"))
	}
	for _, target := range c.ProxyPass {
		if err := validateHostPort(target); err != nil {
			errs = append(errs, fmt.Errorf("proxy_pass %q: %w", target, err))
		}
	}

	return errors.Join(errs...)
}

func validateServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not a valid URL: %w", raw, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%q must start with https:// (TLS is required)", raw)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%q has no host", raw)
	}
	if u.Port() != "" {
		if err := validatePort(u.Port()); err != nil {
			return err
		}
	}
	return nil
}

// validateHostPort accepts host:port, with an optional tcp:// or tls:// prefix
// kept from older configurations.
func validateHostPort(target string) error {
	for _, prefix := range []string{"tcp://", "tls://", "http://", "https://"} {
		target = strings.TrimPrefix(target, prefix)
	}
	_, port, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("must be host:port, for example 127.0.0.1:8082")
	}
	return validatePort(port)
}

func validatePort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port %q is not in range 1-65535", port)
	}
	return nil
}
