package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/bellek-si/bellek/api"
)

type Config struct {
	ServerURL     string
	Token         string
	LogLevel      string
	Capture       []string
	CaptureIgnore []string
	MemoryDirs    []string
	Recall        []string
	BriefTokens   int
}

const (
	DefaultServerURL     = "https://bellek.si"
	DefaultCapture       = "prompts,files,progress,memory"
	DefaultCaptureIgnore = ".env*,*.pem,*.key,*lock*,id_rsa*,id_dsa*,id_ecdsa*,id_ed25519*,.netrc,.npmrc,.pypirc,credentials,*credentials.json,.git-credentials"
	DefaultRecall        = "briefing,prompt,files"
	DefaultBriefTokens   = 800
)

// Load reads the environment only. The hooks, bellek mcp and the CLI run
// inside whatever repository Claude Code has open, and the key there decides
// the workspace, so no file may pick it for them.
func Load() (*Config, error) {
	cfg := &Config{
		ServerURL:     strings.TrimRight(value("BELLEK_URL", DefaultServerURL), "/"),
		Token:         os.Getenv("BELLEK_TOKEN"),
		LogLevel:      value("LOG_LEVEL", "info"),
		Capture:       list("BELLEK_CAPTURE", DefaultCapture),
		CaptureIgnore: list("BELLEK_CAPTURE_IGNORE", DefaultCaptureIgnore),
		MemoryDirs:    list("BELLEK_MEMORY_DIRS", ""),
		Recall:        list("BELLEK_RECALL", DefaultRecall),
		BriefTokens:   DefaultBriefTokens,
	}
	if raw := os.Getenv("BELLEK_BRIEF_TOKENS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return nil, errors.New("BELLEK_BRIEF_TOKENS must be a positive number")
		}
		cfg.BriefTokens = n
	}
	if cfg.Token == "" {
		return nil, errors.New("BELLEK_TOKEN is required")
	}
	if !strings.HasPrefix(cfg.Token, api.KeyPrefix) {
		return nil, fmt.Errorf("BELLEK_TOKEN must be an API key, which starts with %s; create one on the settings page", api.KeyPrefix)
	}
	return cfg, nil
}

func value(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// list gives an empty list for a variable that is set but empty, so an operator
// can turn a default list off.
func list(name, fallback string) []string {
	raw, ok := os.LookupEnv(name)
	if !ok {
		raw = fallback
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (c *Config) Captures(stream string) bool {
	return slices.Contains(c.Capture, stream)
}

func (c *Config) Recalls(injection string) bool {
	return slices.Contains(c.Recall, injection)
}

func (c *Config) ClearText() bool {
	u, err := url.Parse(c.ServerURL)
	if err != nil || u.Scheme != "http" || u.Hostname() == "localhost" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip == nil || !ip.IsLoopback()
}
