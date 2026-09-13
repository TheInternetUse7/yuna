package config

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

// envVars is every variable Load reads, so one test cannot leak settings into
// the next.
var envVars = []string{
	"DISCORD_TOKEN", "DISCORD_GUILD_ID", "YUNA_PROVIDERS",
	"YUNA_HISTORY_WINDOW", "YUNA_SUMMARY_EVERY", "YUNA_SUMMARY_PROVIDER",
	"YUNA_FACTS_PER_USER_LIMIT", "YUNA_FACTS_INJECT_LIMIT", "YUNA_MEMORY_ENABLED",
	"YUNA_MAX_RETRIES", "YUNA_REQUEST_TIMEOUT_SECONDS", "YUNA_SYSTEM_PROMPT",
	"YUNA_DB_PATH", "YUNA_LOG_FILE", "YUNA_DEBUG",
	"GEMINI_MODEL", "GEMINI_API_KEY", "GEMINI_TOOLS", "GEMINI_BASE_URL",
	"GROQ_MODEL", "GROQ_API_KEY",
	"OPENAI_MODEL", "OPENAI_API_KEY", "OPENAI_BASE_URL",
	"MY_VLLM_MODEL", "MY_VLLM_BASE_URL", "MY_VLLM_API_KEY", "MY_VLLM_TOOLS",
}

func withEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for _, name := range envVars {
		t.Setenv(name, "")
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
}

func validEnv() map[string]string {
	return map[string]string{
		"DISCORD_TOKEN":  "token",
		"YUNA_PROVIDERS": "gemini,groq",
		"GEMINI_MODEL":   "gemini-2.5-flash",
		"GEMINI_API_KEY": "gemini-key",
		"GROQ_MODEL":     "llama-3.3-70b-versatile",
		"GROQ_API_KEY":   "groq-key",
	}
}

func TestLoadValidConfig(t *testing.T) {
	withEnv(t, validEnv())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ProviderNames(); strings.Join(got, ",") != "gemini,groq" {
		t.Fatalf("provider order = %v, want gemini,groq", got)
	}
	if cfg.Providers[0].Driver != schemas.Gemini {
		t.Fatalf("first driver = %q, want %q", cfg.Providers[0].Driver, schemas.Gemini)
	}
	if cfg.Providers[0].Model != "gemini-2.5-flash" {
		t.Fatalf("first model = %q", cfg.Providers[0].Model)
	}
	if !cfg.Providers[0].Tools {
		t.Fatal("gemini should support tools by default")
	}
	if cfg.Primary().Name != "gemini" {
		t.Fatalf("primary = %q, want gemini", cfg.Primary().Name)
	}

	// Defaults.
	if cfg.HistoryWindow != 15 || cfg.SummaryEvery != 25 {
		t.Fatalf("window/every = %d/%d, want 15/25", cfg.HistoryWindow, cfg.SummaryEvery)
	}
	if cfg.FactsPerUserLimit != 50 || cfg.FactsInjectLimit != 20 {
		t.Fatalf("fact limits = %d/%d, want 50/20", cfg.FactsPerUserLimit, cfg.FactsInjectLimit)
	}
	if !cfg.MemoryEnabled {
		t.Fatal("memory should be enabled by default")
	}
	if cfg.MaxRetries != 2 || cfg.RequestTimeoutSeconds != 30 {
		t.Fatalf("retries/timeout = %d/%d, want 2/30", cfg.MaxRetries, cfg.RequestTimeoutSeconds)
	}
	if cfg.SystemPrompt != DefaultSystemPrompt {
		t.Fatalf("system prompt = %q, want the default persona", cfg.SystemPrompt)
	}
	// The summary provider defaults to the last entry in the chain.
	if cfg.SummaryProvider != "groq" {
		t.Fatalf("summary provider = %q, want groq", cfg.SummaryProvider)
	}
	if _, ok := cfg.Provider("GEMINI"); !ok {
		t.Fatal("Provider lookup should be case-insensitive")
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	withEnv(t, map[string]string{
		"YUNA_PROVIDERS":      "gemini,mystery",
		"GROQ_MODEL":          "unused",
		"YUNA_HISTORY_WINDOW": "-3",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail")
	}
	message := err.Error()
	for _, want := range []string{
		"DISCORD_TOKEN",       // missing token
		"GEMINI_MODEL",        // missing model
		"GEMINI_API_KEY",      // missing key for a built-in
		"mystery",             // unknown provider with no base URL
		"YUNA_HISTORY_WINDOW", // negative number
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("error does not mention %s:\n%s", want, message)
		}
	}
}

func TestLoadRejectsEmptyProviderList(t *testing.T) {
	env := validEnv()
	env["YUNA_PROVIDERS"] = "  ,  "
	withEnv(t, env)

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "YUNA_PROVIDERS") {
		t.Fatalf("err = %v, want a YUNA_PROVIDERS complaint", err)
	}
}

func TestLoadRejectsDuplicateProvider(t *testing.T) {
	env := validEnv()
	env["YUNA_PROVIDERS"] = "gemini,GEMINI"
	withEnv(t, env)

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("err = %v, want a duplicate complaint", err)
	}
}

func TestLoadRejectsUnknownProviderWithoutBaseURL(t *testing.T) {
	env := validEnv()
	env["YUNA_PROVIDERS"] = "mystery"
	withEnv(t, env)

	err := mustFail(t, env)
	if !strings.Contains(err, "MYSTERY_BASE_URL") {
		t.Fatalf("error should name the missing variable:\n%s", err)
	}
}

func TestLoadRejectsBaseURLOnUnsupportedBuiltin(t *testing.T) {
	env := validEnv()
	env["GEMINI_BASE_URL"] = "http://example.test/v1"
	withEnv(t, env)

	err := mustFail(t, env)
	if !strings.Contains(err, "GEMINI_BASE_URL") {
		t.Fatalf("error should name GEMINI_BASE_URL:\n%s", err)
	}
}

func TestLoadAcceptsBaseURLForOpenAI(t *testing.T) {
	env := validEnv()
	env["YUNA_PROVIDERS"] = "openai"
	env["OPENAI_MODEL"] = "gpt-4o-mini"
	env["OPENAI_API_KEY"] = "key"
	env["OPENAI_BASE_URL"] = "https://gateway.example/v1"
	withEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The /v1 is stripped on load: Bifrost appends the API path itself, so
	// keeping it would produce /v1/v1/chat/completions.
	if cfg.Providers[0].BaseURL != "https://gateway.example" {
		t.Fatalf("base URL = %q", cfg.Providers[0].BaseURL)
	}
	if cfg.Providers[0].IsCustom {
		t.Fatal("the openai driver stays a first-class provider")
	}
}

func TestLoadResolvesCustomProviderAsOpenAICompatible(t *testing.T) {
	withEnv(t, map[string]string{
		"DISCORD_TOKEN":    "token",
		"YUNA_PROVIDERS":   "my-vllm",
		"MY_VLLM_MODEL":    "qwen2.5-14b-instruct",
		"MY_VLLM_BASE_URL": "http://127.0.0.1:8000/v1",
		"MY_VLLM_TOOLS":    "true",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Providers[0]
	if !p.IsCustom {
		t.Fatal("a provider with only a base URL must be resolved as custom")
	}
	if p.Driver != schemas.ModelProvider("my-vllm") {
		t.Fatalf("driver = %q, want the custom name so two endpoints can coexist", p.Driver)
	}
	if !p.KeyLess {
		t.Fatal("a custom provider with no API key must be keyless")
	}
	// A trailing /v1 is accepted but stripped, because Bifrost appends
	// /v1/chat/completions to whatever base URL it is given.
	if p.BaseURL != "http://127.0.0.1:8000" {
		t.Fatalf("base URL = %q", p.BaseURL)
	}
	if !p.Tools {
		t.Fatal("custom providers default to supporting tools")
	}
}

func TestLoadHonoursOverrides(t *testing.T) {
	env := validEnv()
	env["YUNA_HISTORY_WINDOW"] = "5"
	env["YUNA_MEMORY_ENABLED"] = "false"
	env["YUNA_SUMMARY_PROVIDER"] = "gemini"
	env["YUNA_SYSTEM_PROMPT"] = "You are a test."
	env["YUNA_DB_PATH"] = "/data/yuna.db"
	env["GEMINI_TOOLS"] = "false"
	env["YUNA_DEBUG"] = "1"
	withEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HistoryWindow != 5 {
		t.Fatalf("history window = %d, want 5", cfg.HistoryWindow)
	}
	if cfg.MemoryEnabled {
		t.Fatal("memory should be disabled")
	}
	if cfg.SummaryProvider != "gemini" {
		t.Fatalf("summary provider = %q", cfg.SummaryProvider)
	}
	if cfg.SystemPrompt != "You are a test." {
		t.Fatalf("system prompt = %q", cfg.SystemPrompt)
	}
	if cfg.DBPath != "/data/yuna.db" {
		t.Fatalf("db path = %q", cfg.DBPath)
	}
	if cfg.Providers[0].Tools {
		t.Fatal("GEMINI_TOOLS=false should disable tools for that provider")
	}
	if !cfg.Debug {
		t.Fatal("YUNA_DEBUG=1 should enable debug logging")
	}
}

func TestLoadRejectsSummaryProviderOutsideChain(t *testing.T) {
	env := validEnv()
	env["YUNA_SUMMARY_PROVIDER"] = "cohere"
	withEnv(t, env)

	err := mustFail(t, env)
	if !strings.Contains(err, "YUNA_SUMMARY_PROVIDER") {
		t.Fatalf("error should mention YUNA_SUMMARY_PROVIDER:\n%s", err)
	}
}

func TestLoadRejectsNonNumericSetting(t *testing.T) {
	env := validEnv()
	env["YUNA_MAX_RETRIES"] = "lots"
	withEnv(t, env)

	err := mustFail(t, env)
	if !strings.Contains(err, "YUNA_MAX_RETRIES") {
		t.Fatalf("error should mention YUNA_MAX_RETRIES:\n%s", err)
	}
}

func TestCatalogNames(t *testing.T) {
	names := CatalogNames()
	if len(names) != 18 {
		t.Fatalf("catalog has %d entries, want 18: %v", len(names), names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("catalog is not sorted: %v", names)
		}
	}
}

func TestEnvPrefix(t *testing.T) {
	cases := map[string]string{
		"gemini":  "GEMINI",
		"my-vllm": "MY_VLLM",
		"openai":  "OPENAI",
	}
	for name, want := range cases {
		if got := EnvPrefix(name); got != want {
			t.Fatalf("EnvPrefix(%q) = %q, want %q", name, got, want)
		}
	}
}

// mustFail applies env, expects Load to fail, and returns the message.
func mustFail(t *testing.T, env map[string]string) string {
	t.Helper()
	withEnv(t, env)
	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail")
	}
	return err.Error()
}
