// Package config loads and validates all of Yuna's runtime settings from the
// environment (optionally seeded from a .env file in the working directory).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// DefaultSystemPrompt is Yuna's persona when YUNA_SYSTEM_PROMPT is unset.
const DefaultSystemPrompt = "You are Yuna, a helpful AI assistant on Discord. " +
	"Be conversational and friendly."

// Config is the fully validated runtime configuration.
type Config struct {
	DiscordToken string
	// GuildID, when set, registers slash commands on that guild only. An empty
	// value registers them globally, which can take up to an hour to appear.
	GuildID string

	// Providers is the ordered chain: Providers[0] is the primary.
	Providers []Provider

	HistoryWindow     int
	SummaryEvery      int
	SummaryProvider   string
	FactsPerUserLimit int
	FactsInjectLimit  int
	MemoryEnabled     bool

	MaxRetries            int
	RequestTimeoutSeconds int

	SystemPrompt string

	DBPath  string
	LogFile string
	Debug   bool
}

// Load reads .env if present, then the environment, and validates everything.
// Every problem is reported at once rather than one per run.
func Load() (*Config, error) {
	var problems []string
	cfg := &Config{}

	// A missing .env is normal (Docker injects the environment directly), but a
	// .env that exists and cannot be parsed must be reported: silently ignoring
	// it turns every variable into a confusing "is required" error.
	if err := loadDotEnv(".env"); err != nil && !os.IsNotExist(err) {
		problems = append(problems, fmt.Sprintf(".env could not be loaded: %v", err))
	}

	cfg.DiscordToken = strings.TrimSpace(os.Getenv("DISCORD_TOKEN"))
	if cfg.DiscordToken == "" {
		problems = append(problems, "DISCORD_TOKEN is required")
	}
	cfg.GuildID = strings.TrimSpace(os.Getenv("DISCORD_GUILD_ID"))

	rawProviders := strings.TrimSpace(os.Getenv("YUNA_PROVIDERS"))
	if rawProviders == "" {
		problems = append(problems, "YUNA_PROVIDERS is required "+
			"(comma-separated provider names, first entry is the primary)")
	} else {
		cfg.Providers = resolveProviders(rawProviders, &problems)
	}

	cfg.HistoryWindow = intEnv("YUNA_HISTORY_WINDOW", 15, 1, &problems)
	cfg.SummaryEvery = intEnv("YUNA_SUMMARY_EVERY", 25, 1, &problems)
	cfg.FactsPerUserLimit = intEnv("YUNA_FACTS_PER_USER_LIMIT", 50, 1, &problems)
	cfg.FactsInjectLimit = intEnv("YUNA_FACTS_INJECT_LIMIT", 20, 1, &problems)
	cfg.MaxRetries = intEnv("YUNA_MAX_RETRIES", 2, 0, &problems)
	cfg.RequestTimeoutSeconds = intEnv("YUNA_REQUEST_TIMEOUT_SECONDS", 30, 1, &problems)
	cfg.MemoryEnabled = boolEnv("YUNA_MEMORY_ENABLED", true, &problems)

	cfg.SystemPrompt = strings.TrimSpace(os.Getenv("YUNA_SYSTEM_PROMPT"))
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}

	cfg.DBPath = strEnv("YUNA_DB_PATH", "yuna.db")
	cfg.LogFile = strEnv("YUNA_LOG_FILE", "yuna.log")
	cfg.Debug = boolEnv("YUNA_DEBUG", false, &problems)

	cfg.SummaryProvider = strings.ToLower(strings.TrimSpace(os.Getenv("YUNA_SUMMARY_PROVIDER")))
	if len(cfg.Providers) > 0 {
		switch {
		case cfg.SummaryProvider == "":
			cfg.SummaryProvider = cfg.Providers[len(cfg.Providers)-1].Name
		default:
			if _, ok := cfg.Provider(cfg.SummaryProvider); !ok {
				problems = append(problems, fmt.Sprintf(
					"YUNA_SUMMARY_PROVIDER %q is not present in YUNA_PROVIDERS", cfg.SummaryProvider))
			}
		}
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("configuration problems:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return cfg, nil
}

// loadDotEnv seeds the process environment from a .env file without
// overwriting variables that are already set, matching godotenv.Load.
//
// It does not call godotenv.Load directly because that parser treats the \r of
// a CRLF line ending as part of the value, which swallows every following line
// into the first variable. Files edited on Windows are CRLF by default, so the
// endings are normalised before parsing.
func loadDotEnv(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	normalised := strings.ReplaceAll(string(raw), "\r\n", "\n")
	normalised = strings.ReplaceAll(normalised, "\r", "\n")

	values, err := godotenv.Unmarshal(normalised)
	if err != nil {
		return err
	}
	for key, value := range values {
		if _, alreadySet := os.LookupEnv(key); alreadySet {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}

// Provider looks up a configured provider by its configuration name.
func (c *Config) Provider(name string) (Provider, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// Primary is the head of the chain.
func (c *Config) Primary() Provider { return c.Providers[0] }

// ProviderNames lists the chain names in order.
func (c *Config) ProviderNames() []string {
	names := make([]string, 0, len(c.Providers))
	for _, p := range c.Providers {
		names = append(names, p.Name)
	}
	return names
}

func strEnv(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func intEnv(name string, def, min int, problems *[]string) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s must be a whole number, got %q", name, raw))
		return def
	}
	if v < min {
		*problems = append(*problems, fmt.Sprintf("%s must be at least %d, got %d", name, min, v))
		return def
	}
	return v
}

func boolEnv(name string, def bool, problems *[]string) bool {
	raw, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(raw) == "" {
		return def
	}
	v, err := parseBool(raw)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s must be true or false, got %q", name, raw))
		return def
	}
	return v
}
