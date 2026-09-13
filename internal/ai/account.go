package ai

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/config"
)

// Account implements schemas.Account for the configured provider chain.
type Account struct {
	byDriver   map[schemas.ModelProvider]config.Provider
	drivers    []schemas.ModelProvider
	maxRetries int
	timeout    int
}

// NewAccount indexes the provider chain for lookup by Bifrost provider key.
func NewAccount(providers []config.Provider, maxRetries, timeoutSeconds int) *Account {
	a := &Account{
		byDriver:   make(map[schemas.ModelProvider]config.Provider, len(providers)),
		maxRetries: maxRetries,
		timeout:    timeoutSeconds,
	}
	for _, p := range providers {
		a.byDriver[p.Driver] = p
		a.drivers = append(a.drivers, p.Driver)
	}
	return a
}

// GetConfiguredProviders lists every provider Bifrost may route to.
func (a *Account) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return a.drivers, nil
}

// GetKeysForProvider returns the single key for a provider, or an empty slice
// for keyless custom endpoints such as a local Ollama or vLLM server.
func (a *Account) GetKeysForProvider(_ context.Context, providerKey schemas.ModelProvider) ([]schemas.Key, error) {
	p, ok := a.byDriver[providerKey]
	if !ok {
		return nil, fmt.Errorf("provider %q is not configured", providerKey)
	}
	if p.APIKey == "" {
		return []schemas.Key{}, nil
	}
	return []schemas.Key{{
		ID:     p.Name,
		Name:   p.Name,
		Value:  schemas.SecretVar{Val: p.APIKey},
		Models: schemas.WhiteList{"*"},
		Weight: 1.0,
	}}, nil
}

// GetConfigForProvider starts from Bifrost's defaults so retry backoff and
// buffer sizes are inherited, then applies Yuna's timeout, retry count and base
// URL.
func (a *Account) GetConfigForProvider(providerKey schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	p, ok := a.byDriver[providerKey]
	if !ok {
		return nil, fmt.Errorf("provider %q is not configured", providerKey)
	}

	nc := schemas.DefaultNetworkConfig
	nc.DefaultRequestTimeoutInSeconds = a.timeout
	nc.MaxRetries = a.maxRetries
	if p.BaseURL != "" {
		nc.BaseURL = p.BaseURL
		// Bifrost refuses RFC 1918 and loopback destinations unless asked.
		// A self-hosted model server is the whole reason to set a base URL, so
		// allow the private network only when the URL actually points there.
		if isPrivateHost(p.BaseURL) {
			nc.AllowPrivateNetwork = true
		}
	}

	cfg := &schemas.ProviderConfig{
		NetworkConfig:            nc,
		ConcurrencyAndBufferSize: schemas.DefaultConcurrencyAndBufferSize,
	}
	if p.IsCustom {
		cfg.CustomProviderConfig = &schemas.CustomProviderConfig{
			BaseProviderType: schemas.OpenAI,
			IsKeyLess:        p.KeyLess,
		}
	}
	return cfg, nil
}

// isPrivateHost reports whether a base URL points at a loopback, private or
// otherwise non-public host.
func isPrivateHost(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" || strings.EqualFold(host, "localhost") {
		return host == "localhost"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false // a DNS name; let Bifrost resolve and check it
	}
	return ip.IsLoopback() || ip.IsPrivate()
}
