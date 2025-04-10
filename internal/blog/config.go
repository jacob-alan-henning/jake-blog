package blog

import (
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
)

type ConfigOption func(*Config) error

type Config struct {
	ServerPort  string // ServerPort is the port number the HTTP server will listen on (e.g. "8080")
	RepoURL     string // RepoURL is the Git repository URL containing blog content
	ContentDir  string // ContentDir is the local directory where blog content will be stored
	RepoKeyPriv string // RepoKeyPriv is the private key content for Git repository access
	KeyPrivPath string // KeyPrivPath is the file path where the private key will be stored
	RepoPass    string // RepoPass is the optional password for the private key
	LocalOnly   bool   // LocalyOnly = true specifies that the server won't clone a git repo but rely on md files in ContentDir
	HTTPSOn     bool   // HTTPSON = true specifies that https is enabled for the web server. http will be redirected
	HTTPSCRT    string // HTTPSCRT is the location of the https certificate
	HTTPSKey    string // HTTPSKEY is the location of the key associated with your certifacte
	CPUProf     bool   // CPUPProf is whether cpu profiling is enabled
}

func DefaultConfig() *Config {
	return &Config{
		ServerPort:  "8080",
		ContentDir:  "content",
		KeyPrivPath: filepath.Join(os.TempDir(), "blog-repo-key"),
		LocalOnly:   false,
		HTTPSOn:     false,
		CPUProf:     false,
	}
}

// I need a a function which returns a bool (true,false)

func NewConfig(opts ...ConfigOption) (*Config, error) {
	cfg := DefaultConfig()

	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, fmt.Errorf("failed to apply config option: %w", err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	required := map[string]string{
		"ServerPort": c.ServerPort,
		"RepoURL":    c.RepoURL,
		"ContentDir": c.ContentDir,
	}

	for field, value := range required {
		if value == "" {
			return fmt.Errorf("missing required configuration: %s", field)
		}
	}

	// validation logic below this point
	if c.RepoKeyPriv != "" && c.KeyPrivPath == "" {
		return fmt.Errorf("KeyPrivPath must be set when RepoKeyPriv is provided")
	}

	if c.HTTPSOn {
		if c.HTTPSCRT == "" {
			return fmt.Errorf("https cert must be specified when https is enabled")
		}
		if c.HTTPSKey == "" {
			return fmt.Errorf("https key must be specified when https is enabled")
		}
		// check that tls cert/key pair are valid
		_, err := tls.LoadX509KeyPair(c.HTTPSCRT, c.HTTPSKey)
		if err != nil {
			return fmt.Errorf("failed to load tls certificate: %w", err)
		}
	}

	return nil
}

// load config from env var
func WithEnvironment(prefix string) ConfigOption {
	return func(c *Config) error {
		envVars := map[string]*string{
			"SERVER_PORT":        &c.ServerPort,
			"REPO_URL":           &c.RepoURL,
			"CONTENT_DIR":        &c.ContentDir,
			"REPO_PRIV_KEY":      &c.RepoKeyPriv,
			"REPO_PRIV_KEY_PATH": &c.KeyPrivPath,
			"REPO_PASS":          &c.RepoPass,
			"HTTPSCRT":           &c.HTTPSCRT,
			"HTTPSKEY":           &c.HTTPSKey,
		}
		envFlags := map[string]*bool{
			"LOCAL_ONLY": &c.LocalOnly,
			"HTTPS_ON":   &c.HTTPSOn,
			"CPUProf":    &c.CPUProf,
		}
		for env, ptr := range envVars {
			if value := os.Getenv(prefix + env); value != "" {
				*ptr = value
			}
		}

		for env, ptr := range envFlags {
			if value := os.Getenv(prefix + env); value != "" {
				if value == "true" {
					*ptr = true
				} else {
					*ptr = false
				}
			}
		}

		return nil
	}
}

// Write private key to disk if provided
func (c *Config) InitializePrivateKey() error {
	if c.RepoKeyPriv == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(c.KeyPrivPath), 0700); err != nil {
		return fmt.Errorf("failed to create private key directory: %w", err)
	}
	if err := os.WriteFile(c.KeyPrivPath, []byte(c.RepoKeyPriv), 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	return nil
}

// profiling configuration
// need this because profiling configuration should be seperate from
// the BlogServer struct configuration. Because it directly effects the runtime.
// config struct == the configuration of the server
// anonymousEnvironmental flag is for configuring go runtime and server specific optimizations
// reuturn bool
func CheckAnonEnvironmentalFlag(flag string) bool {
	// return value true
	//"true"
	//
	value := os.Getenv(flag)
	if value == "" || value == "false" {
		return false
	}

	return true
}

func CheckAnonEnvironmental(flag string) string {
	value := os.Getenv(flag)
	return value
}
