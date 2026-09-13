package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadYAMLValidConfigAndDefaults(t *testing.T) {
	path := writeConfig(t, `
discord:
  token: file-token
  guild_id: guild-1
providers:
  - name: gemini
    api_key: gemini-key
    models:
      - id: gemini-2.5-flash
        image_input: true
      - id: gemini-2.5-pro
  - name: my-vllm
    base_url: http://127.0.0.1:8000/v1
    tools: false
    models:
      - id: qwen2.5-14b-instruct
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DiscordToken != "file-token" || cfg.GuildID != "guild-1" {
		t.Fatalf("discord = %q/%q, want file-token/guild-1", cfg.DiscordToken, cfg.GuildID)
	}
	if got := cfg.ProviderNames(); strings.Join(got, ",") != "gemini,my-vllm" {
		t.Fatalf("provider order = %v", got)
	}

	gemini := cfg.Providers[0]
	if gemini.Driver != schemas.Gemini || !gemini.Tools {
		t.Fatalf("gemini driver/tools = %s/%t", gemini.Driver, gemini.Tools)
	}
	if got := strings.Join(gemini.Models, ","); got != "gemini-2.5-flash,gemini-2.5-pro" {
		t.Fatalf("gemini models = %q", got)
	}
	if !gemini.SupportsImages("gemini-2.5-flash") || gemini.SupportsImages("gemini-2.5-pro") {
		t.Fatal("image_input must be a per-model capability")
	}

	custom := cfg.Providers[1]
	if !custom.IsCustom || !custom.KeyLess || custom.Tools {
		t.Fatalf("custom provider flags = custom:%t keyless:%t tools:%t",
			custom.IsCustom, custom.KeyLess, custom.Tools)
	}
	if custom.BaseURL != "http://127.0.0.1:8000" {
		t.Fatalf("custom base URL = %q, want /v1 stripped", custom.BaseURL)
	}

	if cfg.HistoryWindow != 15 || cfg.SummaryEvery != 25 {
		t.Fatalf("history/summary every = %d/%d, want 15/25", cfg.HistoryWindow, cfg.SummaryEvery)
	}
	if cfg.FactsPerUserLimit != 50 || cfg.FactsInjectLimit != 20 {
		t.Fatalf("fact limits = %d/%d, want 50/20", cfg.FactsPerUserLimit, cfg.FactsInjectLimit)
	}
	if !cfg.MemoryEnabled || cfg.MaxRetries != 2 || cfg.RequestTimeoutSeconds != 30 {
		t.Fatalf("memory/retries/timeout = %t/%d/%d", cfg.MemoryEnabled, cfg.MaxRetries, cfg.RequestTimeoutSeconds)
	}
	if cfg.SystemPrompt != DefaultSystemPrompt {
		t.Fatalf("system prompt = %q, want default", cfg.SystemPrompt)
	}
	if cfg.SummaryProvider != "my-vllm" || cfg.SummaryModel != "qwen2.5-14b-instruct" {
		t.Fatalf("summary = %s/%s, want last provider and its default model",
			cfg.SummaryProvider, cfg.SummaryModel)
	}
	if cfg.DBPath != "yuna.db" || cfg.LogFile != "yuna.log" || cfg.Debug {
		t.Fatalf("runtime defaults = %s/%s/%t", cfg.DBPath, cfg.LogFile, cfg.Debug)
	}
}

func TestLoadYAMLIgnoresEnvironmentVariables(t *testing.T) {
	t.Setenv("DISCORD_TOKEN", "environment-token")
	path := writeConfig(t, `
discord:
  token: file-token
providers:
  - name: gemini
    api_key: key
    models:
      - id: gemini-2.5-flash
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DiscordToken != "file-token" {
		t.Fatalf("token = %q, want the YAML value", cfg.DiscordToken)
	}
}

func TestLoadYAMLRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, `
unknown: true
discord:
  token: token
providers:
  - name: gemini
    api_key: key
    models:
      - id: gemini-2.5-flash
`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("err = %v, want an unknown-field error", err)
	}
}

func TestLoadYAMLReportsEveryProblemAtOnce(t *testing.T) {
	path := writeConfig(t, `
providers:
  - name: gemini
    base_url: https://gateway.example
    models:
      - id: gemini-2.5-flash
      - id: gemini-2.5-flash
  - name: mystery
    models:
      - id: missing-base-url
memory:
  history_window: -1
  summary_provider: absent
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected configuration problems")
	}
	message := err.Error()
	for _, want := range []string{
		"discord.token is required",
		"providers requires at least one usable entry",
		"cannot be redirected by base_url",
		"GEMINI API key is required",
		"duplicate model",
		"base_url is not set",
		"memory.history_window",
		"memory.summary_provider",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("error does not mention %q:\n%s", want, message)
		}
	}
}

func TestLoadYAMLHonoursOverrides(t *testing.T) {
	path := writeConfig(t, `
discord:
  token: token
providers:
  - name: gemini
    api_key: key
    tools: false
    models:
      - id: gemini-2.5-flash
      - id: gemini-2.5-pro
memory:
  enabled: false
  history_window: 5
  summary_every: 10
  summary_provider: gemini
  summary_model: gemini-2.5-pro
  facts_per_user_limit: 7
  facts_inject_limit: 3
requests:
  max_retries: 4
  timeout_seconds: 60
persona:
  system_prompt: You are a test.
runtime:
  db_path: /data/yuna.db
  log_file: /data/yuna.log
  debug: true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.MemoryEnabled == false || cfg.HistoryWindow != 5 || cfg.SummaryEvery != 10 {
		t.Fatalf("memory = %t/%d/%d", cfg.MemoryEnabled, cfg.HistoryWindow, cfg.SummaryEvery)
	}
	if cfg.SummaryProvider != "gemini" || cfg.SummaryModel != "gemini-2.5-pro" {
		t.Fatalf("summary = %s/%s", cfg.SummaryProvider, cfg.SummaryModel)
	}
	if cfg.FactsPerUserLimit != 7 || cfg.FactsInjectLimit != 3 {
		t.Fatalf("fact limits = %d/%d", cfg.FactsPerUserLimit, cfg.FactsInjectLimit)
	}
	if cfg.MaxRetries != 4 || cfg.RequestTimeoutSeconds != 60 {
		t.Fatalf("requests = %d/%d", cfg.MaxRetries, cfg.RequestTimeoutSeconds)
	}
	if cfg.SystemPrompt != "You are a test." {
		t.Fatalf("system prompt = %q", cfg.SystemPrompt)
	}
	if cfg.DBPath != "/data/yuna.db" || cfg.LogFile != "/data/yuna.log" || !cfg.Debug {
		t.Fatalf("runtime = %s/%s/%t", cfg.DBPath, cfg.LogFile, cfg.Debug)
	}
	if cfg.Providers[0].Tools {
		t.Fatal("provider tools override was not applied")
	}
}

func TestLoadYAMLRejectsSummaryModelOutsideProvider(t *testing.T) {
	path := writeConfig(t, `
discord:
  token: token
providers:
  - name: gemini
    api_key: key
    models:
      - id: gemini-2.5-flash
memory:
  summary_provider: gemini
  summary_model: gpt-5.5
`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "memory.summary_model") {
		t.Fatalf("err = %v, want a summary-model error", err)
	}
}

func TestLoadYAMLAcceptsOpenAICompatibleBaseURL(t *testing.T) {
	path := writeConfig(t, `
discord:
  token: token
providers:
  - name: openai
    api_key: key
    base_url: https://gateway.example/v1
    models:
      - id: gpt-4o-mini
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	provider := cfg.Providers[0]
	if provider.IsCustom || provider.BaseURL != "https://gateway.example" {
		t.Fatalf("provider = custom:%t base_url:%q", provider.IsCustom, provider.BaseURL)
	}
}

func TestLoadYAMLRejectsDuplicateProviders(t *testing.T) {
	path := writeConfig(t, `
discord:
  token: token
providers:
  - name: gemini
    api_key: key
    models:
      - id: gemini-2.5-flash
  - name: GEMINI
    api_key: key
    models:
      - id: gemini-2.5-pro
`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("err = %v, want a duplicate-provider error", err)
	}
}

func TestLoadYAMLRejectsInvalidYAML(t *testing.T) {
	path := writeConfig(t, "discord: [unclosed\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("err = %v, want a parse error", err)
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	filled := strings.NewReplacer(
		`token: ""`, `token: test-token`,
		`api_key: ""`, `api_key: test-key`,
	).Replace(string(raw))
	path := writeConfig(t, filled)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load example config: %v", err)
	}
	if cfg.DiscordToken != "test-token" || len(cfg.Providers) != 3 {
		t.Fatalf("example loaded as token=%q providers=%d", cfg.DiscordToken, len(cfg.Providers))
	}
}
