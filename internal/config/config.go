// Package config loads and validates all of Yuna's runtime settings from YAML.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultConfigPath is used when the operator does not pass --config.
const DefaultConfigPath = "config.yaml"

// DefaultSystemPrompt is Yuna's persona when persona.system_prompt is unset.
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

	HistoryWindow   int
	SummaryEvery    int
	SummaryProvider string
	// SummaryModel is the model the summariser uses, always one of the summary
	// provider's configured models. It exists so memory can run on something
	// cheaper than whatever the chat chain is using.
	SummaryModel      string
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

type fileConfig struct {
	Discord   fileDiscord    `yaml:"discord"`
	Providers []fileProvider `yaml:"providers"`
	Memory    fileMemory     `yaml:"memory"`
	Requests  fileRequests   `yaml:"requests"`
	Persona   filePersona    `yaml:"persona"`
	Runtime   fileRuntime    `yaml:"runtime"`
}

type fileDiscord struct {
	Token   string `yaml:"token"`
	GuildID string `yaml:"guild_id"`
}

type fileMemory struct {
	Enabled           *bool  `yaml:"enabled"`
	HistoryWindow     *int   `yaml:"history_window"`
	SummaryEvery      *int   `yaml:"summary_every"`
	SummaryProvider   string `yaml:"summary_provider"`
	SummaryModel      string `yaml:"summary_model"`
	FactsPerUserLimit *int   `yaml:"facts_per_user_limit"`
	FactsInjectLimit  *int   `yaml:"facts_inject_limit"`
}

type fileRequests struct {
	MaxRetries     *int `yaml:"max_retries"`
	TimeoutSeconds *int `yaml:"timeout_seconds"`
}

type filePersona struct {
	SystemPrompt string `yaml:"system_prompt"`
}

type fileRuntime struct {
	DBPath  string `yaml:"db_path"`
	LogFile string `yaml:"log_file"`
	Debug   *bool  `yaml:"debug"`
}

// Load reads and validates one YAML configuration file. Unknown fields are
// rejected so a renamed setting fails at boot instead of being silently
// ignored. Every semantic problem is reported in a single error.
func Load(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	var fileCfg fileConfig
	if err := decoder.Decode(&fileCfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	var problems []string
	cfg := &Config{}

	cfg.DiscordToken = strings.TrimSpace(fileCfg.Discord.Token)
	if cfg.DiscordToken == "" {
		problems = append(problems, "discord.token is required")
	}
	cfg.GuildID = strings.TrimSpace(fileCfg.Discord.GuildID)

	cfg.Providers = resolveProviders(fileCfg.Providers, &problems)

	memory := fileCfg.Memory
	cfg.MemoryEnabled = boolSetting(memory.Enabled, true)
	cfg.HistoryWindow = intSetting("memory.history_window", memory.HistoryWindow, 15, 1, &problems)
	cfg.SummaryEvery = intSetting("memory.summary_every", memory.SummaryEvery, 25, 1, &problems)
	cfg.FactsPerUserLimit = intSetting(
		"memory.facts_per_user_limit", memory.FactsPerUserLimit, 50, 1, &problems)
	cfg.FactsInjectLimit = intSetting(
		"memory.facts_inject_limit", memory.FactsInjectLimit, 20, 1, &problems)

	requests := fileCfg.Requests
	cfg.MaxRetries = intSetting("requests.max_retries", requests.MaxRetries, 2, 0, &problems)
	cfg.RequestTimeoutSeconds = intSetting(
		"requests.timeout_seconds", requests.TimeoutSeconds, 30, 1, &problems)

	cfg.SystemPrompt = strings.TrimSpace(fileCfg.Persona.SystemPrompt)
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}

	runtime := fileCfg.Runtime
	cfg.DBPath = stringDefault(runtime.DBPath, "yuna.db")
	cfg.LogFile = stringDefault(runtime.LogFile, "yuna.log")
	cfg.Debug = boolSetting(runtime.Debug, false)

	cfg.SummaryProvider = strings.ToLower(strings.TrimSpace(memory.SummaryProvider))
	if cfg.SummaryProvider == "" && len(cfg.Providers) > 0 {
		cfg.SummaryProvider = cfg.Providers[len(cfg.Providers)-1].Name
	} else if _, ok := cfg.Provider(cfg.SummaryProvider); !ok {
		problems = append(problems, fmt.Sprintf(
			"memory.summary_provider %q is not present in providers", cfg.SummaryProvider))
	}

	// The summariser's model must belong to its provider: a typo here fails at
	// the first summary refresh, long after boot, so catch it up front.
	cfg.SummaryModel = strings.TrimSpace(memory.SummaryModel)
	if provider, ok := cfg.Provider(cfg.SummaryProvider); ok {
		switch {
		case cfg.SummaryModel == "":
			cfg.SummaryModel = provider.Default()
		case !provider.HasModel(cfg.SummaryModel):
			problems = append(problems, fmt.Sprintf(
				"memory.summary_model %q is not a model of provider %q (%s)",
				cfg.SummaryModel, provider.Name, strings.Join(provider.Models, ", ")))
		}
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("configuration problems:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return cfg, nil
}

// Provider finds a configured provider by name, case-insensitively.
func (c *Config) Provider(name string) (Provider, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// Primary is the provider that answers unless a stored model preference moves
// another provider to the front for a particular scope.
func (c *Config) Primary() Provider { return c.Providers[0] }

// ProviderNames returns the configured chain in order.
func (c *Config) ProviderNames() []string {
	names := make([]string, 0, len(c.Providers))
	for _, p := range c.Providers {
		names = append(names, p.Name)
	}
	return names
}

func stringDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func boolSetting(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func intSetting(name string, value *int, fallback, minimum int, problems *[]string) int {
	if value == nil {
		return fallback
	}
	if *value < minimum {
		*problems = append(*problems, fmt.Sprintf("%s must be at least %d, got %d",
			name, minimum, *value))
		return fallback
	}
	return *value
}
