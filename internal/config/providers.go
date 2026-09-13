package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// Provider is one entry in the ordered provider chain. The first entry is the
// primary; the rest become Bifrost fallbacks.
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
	// ImageModels lists the subset of Models that accept image input.
	ImageModels []string
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

type fileProvider struct {
	Name    string      `yaml:"name"`
	APIKey  string      `yaml:"api_key"`
	BaseURL string      `yaml:"base_url"`
	Tools   *bool       `yaml:"tools"`
	Models  []fileModel `yaml:"models"`
}

type fileModel struct {
	ID         string `yaml:"id"`
	ImageInput bool   `yaml:"image_input"`
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

// SupportsImages reports whether the named model can consume image input. Like
// HasModel, the comparison is exact because model IDs are vendor identifiers.
func (p Provider) SupportsImages(id string) bool {
	for _, m := range p.ImageModels {
		if m == id {
			return true
		}
	}
	return false
}

func resolveProviders(fileProviders []fileProvider, problems *[]string) []Provider {
	if len(fileProviders) == 0 {
		*problems = append(*problems, "providers requires at least one entry")
		return nil
	}

	seenProviders := make(map[string]bool, len(fileProviders))
	providers := make([]Provider, 0, len(fileProviders))
	for index, fileProvider := range fileProviders {
		name := strings.ToLower(strings.TrimSpace(fileProvider.Name))
		if name == "" {
			*problems = append(*problems, fmt.Sprintf("providers[%d].name is required", index))
			continue
		}
		if seenProviders[name] {
			*problems = append(*problems, fmt.Sprintf("providers lists %q more than once", name))
			continue
		}
		seenProviders[name] = true

		baseURL := normalizeBaseURL(fileProvider.BaseURL)
		apiKey := strings.TrimSpace(fileProvider.APIKey)
		provider := Provider{Name: name, APIKey: apiKey, BaseURL: baseURL}

		entry, isCatalog := providerCatalog[name]
		usable := true
		if isCatalog && apiKey == "" {
			*problems = append(*problems, fmt.Sprintf("%s API key is required for provider %q",
				strings.ToUpper(strings.ReplaceAll(name, "-", "_")), name))
		}
		switch {
		case isCatalog && baseURL == "":
			provider.Driver = entry.driver
			provider.Tools = entry.tools
		case isCatalog && entry.driver == schemas.OpenAI:
			// A gateway or proxy speaking the OpenAI wire format.
			provider.Driver = entry.driver
			provider.Tools = entry.tools
		case isCatalog:
			*problems = append(*problems, fmt.Sprintf(
				"provider %q is built-in and cannot be redirected by base_url; "+
					"remove base_url or use a custom provider name (e.g. %s-proxy)", name, name))
			usable = false
		case baseURL != "":
			// Any other name with a base URL becomes an OpenAI-compatible custom.
			provider.Driver = schemas.ModelProvider(name)
			provider.Tools = true
			provider.IsCustom = true
			provider.KeyLess = apiKey == ""
		default:
			*problems = append(*problems, fmt.Sprintf(
				"unknown provider %q: not one of the built-in providers (%s), and base_url is not set",
				name, strings.Join(CatalogNames(), ", ")))
			usable = false
		}

		if fileProvider.Tools != nil {
			provider.Tools = *fileProvider.Tools
		}

		if len(fileProvider.Models) == 0 {
			*problems = append(*problems, fmt.Sprintf("provider %q requires at least one model", name))
		}
		seenModels := make(map[string]bool, len(fileProvider.Models))
		for _, model := range fileProvider.Models {
			id := strings.TrimSpace(model.ID)
			if id == "" {
				*problems = append(*problems, fmt.Sprintf("provider %q has a model with an empty id", name))
				continue
			}
			if seenModels[id] {
				*problems = append(*problems, fmt.Sprintf(
					"provider %q has duplicate model %q", name, id))
				continue
			}
			seenModels[id] = true
			provider.Models = append(provider.Models, id)
			if model.ImageInput {
				provider.ImageModels = append(provider.ImageModels, id)
			}
		}
		if usable {
			providers = append(providers, provider)
		}
	}

	if len(providers) == 0 {
		*problems = append(*problems, "providers requires at least one usable entry")
	}
	return providers
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
