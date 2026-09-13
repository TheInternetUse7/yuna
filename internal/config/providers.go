package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// Provider is one entry in the ordered provider chain. The first entry in
// YUNA_PROVIDERS is the primary; the rest become Bifrost fallbacks.
type Provider struct {
	// Name is the configuration name, e.g. "gemini" or "my-vllm". It is also
	// the name used to address the provider in Bifrost requests, so a custom
	// provider is never collapsed onto the shared OpenAI driver.
	Name string
	// Driver is the Bifrost provider key, e.g. schemas.Gemini.
	Driver schemas.ModelProvider
	// Models are the bare, provider-native model IDs (no "provider/" prefix),
	// in the order they should be tried. The first is the provider's default;
	// the rest are per-model fallbacks, which is what lets a single 429 or a
	// retired model roll within one vendor instead of leaving it.
	Models []string
	// APIKey is empty for keyless custom endpoints such as local Ollama.
	APIKey string
	// Tools reports whether the remember tool may be offered to this provider.
	Tools bool
	// BaseURL is the OpenAI-compatible host, without /v1. Empty for native providers.
	BaseURL string
	// IsCustom marks a provider that must be configured through
	// CustomProviderConfig rather than a first-class Bifrost driver.
	IsCustom bool
	// KeyLess marks a custom provider that needs no API key.
	KeyLess bool
}

// Default is the model this provider answers with when nothing overrides it.
// resolveProviders guarantees at least one model, so this never panics on a
// provider built by Load.
func (p Provider) Default() string {
	if len(p.Models) == 0 {
		return ""
	}
	return p.Models[0]
}

// HasModel reports whether id appears in this provider's model list. The
// comparison is exact: model IDs are vendor identifiers, and some vendors
// ship names that differ only by case.
func (p Provider) HasModel(id string) bool {
	for _, m := range p.Models {
		if m == id {
			return true
		}
	}
	return false
}

type catalogEntry struct {
	driver schemas.ModelProvider
	tools  bool
}

// providerCatalog lists the first-class Bifrost providers that can be selected
// by name alone. Anything else must supply a base URL and is registered as an
// OpenAI-compatible custom provider.
var providerCatalog = map[string]catalogEntry{
	"gemini":      {schemas.Gemini, true},
	"openai":      {schemas.OpenAI, true},
	"anthropic":   {schemas.Anthropic, true},
	"groq":        {schemas.Groq, true},
	"cerebras":    {schemas.Cerebras, true},
	"openrouter":  {schemas.OpenRouter, true},
	"cohere":      {schemas.Cohere, false},
	"mistral":     {schemas.Mistral, true},
	"deepseek":    {schemas.DeepSeek, true},
	"xai":         {schemas.XAI, true},
	"perplexity":  {schemas.Perplexity, true},
	"nebius":      {schemas.Nebius, true},
	"fireworks":   {schemas.Fireworks, true},
	"huggingface": {schemas.HuggingFace, true},
	"replicate":   {schemas.Replicate, false},
	"sarvam":      {schemas.Sarvam, true},
	"parasail":    {schemas.Parasail, true},
	"wafer":       {schemas.Wafer, true},
}

// CatalogNames returns the built-in provider names in stable order, for error
// messages and documentation.
func CatalogNames() []string {
	names := make([]string, 0, len(providerCatalog))
	for name := range providerCatalog {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// EnvPrefix turns a provider name into its environment variable prefix:
// lowercase with dashes replaced by underscores, uppercased.
func EnvPrefix(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// resolveProviders parses the ordered YUNA_PROVIDERS list and fills in each
// provider from its environment variables. Problems are appended, not fatal,
// so the caller can report every misconfiguration at once.
func resolveProviders(raw string, problems *[]string) []Provider {
	seen := make(map[string]bool)
	var providers []Provider

	for _, part := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if seen[name] {
			*problems = append(*problems, fmt.Sprintf("YUNA_PROVIDERS lists %q more than once", name))
			continue
		}
		seen[name] = true

		prefix := EnvPrefix(name)
		baseURL := normalizeBaseURL(os.Getenv(prefix + "_BASE_URL"))
		apiKey := strings.TrimSpace(os.Getenv(prefix + "_API_KEY"))

		p := Provider{Name: name, APIKey: apiKey}

		entry, isCatalog := providerCatalog[name]
		switch {
		case isCatalog && baseURL == "":
			p.Driver = entry.driver
			p.Tools = entry.tools

		case isCatalog && entry.driver == schemas.OpenAI:
			// A gateway or proxy speaking the OpenAI wire format.
			p.Driver = entry.driver
			p.Tools = entry.tools
			p.BaseURL = baseURL

		case isCatalog:
			*problems = append(*problems, fmt.Sprintf(
				"%s_BASE_URL is set but %q is a built-in provider that cannot be redirected; "+
					"remove it or use a custom provider name (e.g. %s-proxy)", prefix, name, name))
			continue

		case baseURL != "":
			// Any other name with a base URL becomes an OpenAI-compatible custom.
			p.Driver = schemas.ModelProvider(name)
			p.Tools = true
			p.BaseURL = baseURL
			p.IsCustom = true
			p.KeyLess = apiKey == ""

		default:
			*problems = append(*problems, fmt.Sprintf(
				"unknown provider %q: not one of the built-in providers (%s) and %s_BASE_URL is not set",
				name, strings.Join(CatalogNames(), ", "), prefix))
			continue
		}

		models := modelsFromEnv(os.Getenv(prefix+"_MODELS"), name, problems)
		if len(models) == 0 {
			*problems = append(*problems, fmt.Sprintf("%s_MODELS is required for provider %q", prefix, name))
		}
		p.Models = models
		// Custom providers may legitimately be keyless (local Ollama, vLLM).
		// Built-ins always need a key.
		if isCatalog && apiKey == "" {
			*problems = append(*problems, fmt.Sprintf("%s_API_KEY is required for provider %q", prefix, name))
		}
		if raw, ok := os.LookupEnv(prefix + "_TOOLS"); ok && strings.TrimSpace(raw) != "" {
			if v, err := parseBool(raw); err == nil {
				p.Tools = v
			} else {
				*problems = append(*problems, fmt.Sprintf("%s_TOOLS must be true or false, got %q", prefix, raw))
			}
		}

		providers = append(providers, p)
	}

	if len(providers) == 0 {
		*problems = append(*problems, "YUNA_PROVIDERS did not contain any usable provider names")
	}
	return providers
}

func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean %q", raw)
}

// modelsFromEnv parses a provider's <PREFIX>_MODELS list: comma-separated
// model IDs in the order they should be tried. A repeated ID is reported and
// dropped, because trying the same model twice only burns a fallback slot and
// doubles the latency of a failing request. Order is preserved, since the
// operator's ordering is the priority.
func modelsFromEnv(raw, provider string, problems *[]string) []string {
	var models []string
	seen := make(map[string]bool)

	for _, part := range strings.Split(raw, ",") {
		model := strings.TrimSpace(part)
		if model == "" {
			continue
		}
		if seen[model] {
			*problems = append(*problems, fmt.Sprintf(
				"%s_MODELS lists model %q more than once for provider %q",
				EnvPrefix(provider), model, provider))
			continue
		}
		seen[model] = true
		models = append(models, model)
	}
	return models
}

// normalizeBaseURL trims whitespace and a trailing slash or /v1 from a
// configured base URL. Bifrost appends the provider's API path to this value,
// so the version segment belongs to Bifrost, not to the operator: the OpenAI
// driver requests BaseURL+"/v1/chat/completions". Accepting both spellings
// means a base URL copied from a vendor's OpenAI-compatible quickstart, which
// almost always ends in /v1, still works.
func normalizeBaseURL(raw string) string {
	out := strings.TrimSpace(raw)
	for {
		trimmed := strings.TrimSuffix(strings.TrimRight(out, "/"), "/v1")
		if trimmed == out {
			return out
		}
		out = trimmed
	}
}
