package bot

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/TheInternetUse7/yuna/internal/applog"
	"github.com/TheInternetUse7/yuna/internal/config"
	"github.com/TheInternetUse7/yuna/internal/store"
)

func preferenceTestChain() []config.Provider {
	return []config.Provider{
		{Name: "gemini", Driver: schemas.Gemini, Models: []string{"gemini-2.5-flash", "gemini-2.5-pro"}, APIKey: "k", Tools: true},
		{Name: "groq", Driver: schemas.Groq, Models: []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant"}, APIKey: "k", Tools: true},
		{Name: "cohere", Driver: schemas.Cohere, Models: []string{"command-a-03-2025"}, APIKey: "k"},
	}
}

func newPreferenceTestBot(t *testing.T, chain []config.Provider) *Bot {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	log, err := applog.New("", false)
	if err != nil {
		t.Fatalf("applog.New: %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })

	return &Bot{cfg: &config.Config{Providers: chain}, store: st, log: log}
}

func adminInteraction(guildID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			GuildID: guildID,
			Member: &discordgo.Member{
				Permissions: int64(discordgo.PermissionAdministrator),
				User:        &discordgo.User{ID: "admin-1"},
			},
		},
	}
}

func dmInteraction(userID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			GuildID: "",
			User:    &discordgo.User{ID: userID},
		},
	}
}

// An administrator in a server changes the server's model; outside one, the
// same command changes only the caller's own DM choice.
func TestPreferenceScopeFollowsContextAndPermissions(t *testing.T) {
	b := newPreferenceTestBot(t, preferenceTestChain())

	scopeType, scopeID, ok := b.preferenceScope(adminInteraction("guild-1"))
	if !ok || scopeType != store.PreferenceScopeGuild || scopeID != "guild-1" {
		t.Fatalf("guild scope = %q/%q/%v, want guild/guild-1/true", scopeType, scopeID, ok)
	}

	scopeType, scopeID, ok = b.preferenceScope(dmInteraction("user-9"))
	if !ok || scopeType != store.PreferenceScopeDM || scopeID != "user-9" {
		t.Fatalf("dm scope = %q/%q/%v, want dm/user-9/true", scopeType, scopeID, ok)
	}

	// A non-administrator cannot change a server's model.
	plain := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			GuildID: "guild-1",
			Member:  &discordgo.Member{User: &discordgo.User{ID: "member-1"}},
		},
	}
	if _, _, ok := b.preferenceScope(plain); ok {
		t.Fatal("a non-administrator was allowed to change the guild model")
	}
}

// With no stored choice the chain is exactly the configured order.
func TestChainWithoutPreference(t *testing.T) {
	b := newPreferenceTestBot(t, preferenceTestChain())

	chain := b.chain("guild-1", "user-1")
	if got := providerNames(chain); got != "gemini,groq,cohere" {
		t.Fatalf("chain = %s, want the configured order", got)
	}
	if chain[0].Default() != "gemini-2.5-flash" {
		t.Fatalf("primary model = %q", chain[0].Default())
	}
}

// A stored choice moves its provider to the front and puts the chosen model
// first, so that model is attempted before its siblings and before other
// providers.
func TestChainAppliesStoredPreference(t *testing.T) {
	b := newPreferenceTestBot(t, preferenceTestChain())

	if err := b.store.SetModelPreference(store.ModelPreference{
		ScopeType: store.PreferenceScopeGuild, ScopeID: "guild-1",
		ProviderName: "groq", ModelName: "llama-3.1-8b-instant", SetByUserID: "admin-1",
	}); err != nil {
		t.Fatalf("SetModelPreference: %v", err)
	}

	chain := b.chain("guild-1", "user-1")
	if got := providerNames(chain); got != "groq,gemini,cohere" {
		t.Fatalf("chain = %s, want the chosen provider first", got)
	}
	if got := strings.Join(chain[0].Models, ","); got != "llama-3.1-8b-instant,llama-3.3-70b-versatile" {
		t.Fatalf("models = %s, want the chosen model first", got)
	}

	// The configured chain must not have been mutated in place.
	if b.cfg.Providers[0].Name != "gemini" || b.cfg.Providers[1].Default() != "llama-3.3-70b-versatile" {
		t.Fatalf("the configured chain was mutated: %+v", b.cfg.Providers)
	}
}

// A guild preference must not leak into DMs, and a DM preference must not
// affect the guild.
func TestChainKeepsGuildAndDMPreferencesApart(t *testing.T) {
	b := newPreferenceTestBot(t, preferenceTestChain())

	if err := b.store.SetModelPreference(store.ModelPreference{
		ScopeType: store.PreferenceScopeDM, ScopeID: "user-1",
		ProviderName: "cohere", ModelName: "command-a-03-2025", SetByUserID: "user-1",
	}); err != nil {
		t.Fatalf("SetModelPreference: %v", err)
	}

	if got := providerNames(b.chain("guild-1", "user-1")); got != "gemini,groq,cohere" {
		t.Fatalf("guild chain = %s, want the configured order", got)
	}
	if got := providerNames(b.chain("", "user-1")); got != "cohere,gemini,groq" {
		t.Fatalf("dm chain = %s, want cohere first", got)
	}
	if got := providerNames(b.chain("", "user-2")); got != "gemini,groq,cohere" {
		t.Fatalf("another user's dm chain = %s, want the configured order", got)
	}
}

// A preference left over from an older configuration must not break replies:
// the chain falls back to the configured order.
func TestChainIgnoresStalePreference(t *testing.T) {
	b := newPreferenceTestBot(t, preferenceTestChain())

	for _, stale := range []store.ModelPreference{
		{ScopeType: store.PreferenceScopeGuild, ScopeID: "guild-1",
			ProviderName: "retired-vendor", ModelName: "whatever"},
		{ScopeType: store.PreferenceScopeGuild, ScopeID: "guild-1",
			ProviderName: "gemini", ModelName: "gemini-1.0-ultra"},
	} {
		if err := b.store.SetModelPreference(stale); err != nil {
			t.Fatalf("SetModelPreference: %v", err)
		}
		if got := providerNames(b.chain("guild-1", "user-1")); got != "gemini,groq,cohere" {
			t.Fatalf("chain = %s, want the configured order for %+v", got, stale)
		}
	}
}

// findModel prefers the provider that would answer now and reports when the ID
// is offered by more than one.
func TestFindModelPrefersEffectiveProvider(t *testing.T) {
	chain := []config.Provider{
		{Name: "groq", Models: []string{"openai/gpt-oss-120b"}},
		{Name: "openrouter", Models: []string{"openai/gpt-oss-120b", "meta/llama-3.3-70b"}},
	}

	got, ok, ambiguous := findModel(chain, "openai/gpt-oss-120b")
	if !ok || !ambiguous || got.Name != "groq" {
		t.Fatalf("findModel = %q/%v/%v, want groq/true/true", got.Name, ok, ambiguous)
	}

	got, ok, ambiguous = findModel(chain, "meta/llama-3.3-70b")
	if !ok || ambiguous || got.Name != "openrouter" {
		t.Fatalf("findModel = %q/%v/%v, want openrouter/true/false", got.Name, ok, ambiguous)
	}

	if _, ok, _ := findModel(chain, "not-a-model"); ok {
		t.Fatal("an unconfigured model resolved")
	}
}

// The typeahead lists every configured model as "provider model", in chain
// order, and filters case-insensitively on either part.
func TestModelAutocompleteCandidatesAndFiltering(t *testing.T) {
	chain := preferenceTestChain()
	candidates := autocompleteCandidates(chain)
	want := []string{
		"gemini · gemini-2.5-flash",
		"gemini · gemini-2.5-pro",
		"groq · llama-3.3-70b-versatile",
		"groq · llama-3.1-8b-instant",
		"cohere · command-a-03-2025",
	}
	labels := make([]string, 0, len(candidates))
	for _, c := range candidates {
		labels = append(labels, c.label())
	}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Fatalf("candidate labels = %v, want %v", labels, want)
	}

	for _, tc := range []struct{ typed, wantContains string }{
		{"", "gemini · gemini-2.5-flash"},
		{"GROQ", "groq · llama-3.1-8b-instant"},
		{"2.5-pro", "gemini · gemini-2.5-pro"},
		{"command", "cohere · command-a-03-2025"},
		{"groq · llama", "groq · llama-3.3-70b-versatile"},
	} {
		choices := matchChoices(candidates, tc.typed)
		if len(choices) == 0 {
			t.Fatalf("filtering by %q produced no choices", tc.typed)
		}
		found := false
		for _, c := range choices {
			if c.Name == tc.wantContains {
				found = true
			}
			// Regression: the value used to be the label, so every pick came
			// back as "provider · model" and findModel rejected it as unknown.
			value, ok := c.Value.(string)
			if !ok {
				t.Fatalf("choice %q has a non-string value %#v", c.Name, c.Value)
			}
			if _, resolved, _ := findModel(chain, value); !resolved {
				t.Fatalf("choice %q carries value %q, which findModel cannot resolve", c.Name, value)
			}
		}
		if !found {
			t.Fatalf("filtering by %q did not offer %q", tc.typed, tc.wantContains)
		}
	}

	// The label is display-only; the pick must round-trip the raw ID.
	choice := matchChoices(candidates, "2.5-pro")[0]
	if choice.Value != "gemini-2.5-pro" {
		t.Fatalf("choice value = %#v, want the raw model ID gemini-2.5-pro", choice.Value)
	}

	if got := matchChoices(candidates, "zzz-no-match"); len(got) != 0 {
		t.Fatalf("a non-matching query returned %d choices", len(got))
	}
}

// Discord rejects more than 25 choices, so the list is capped.
func TestModelAutocompleteIsCappedAtDiscordLimit(t *testing.T) {
	models := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		models = append(models, "model-"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+strings.Repeat("", i%7))
	}
	candidates := autocompleteCandidates([]config.Provider{{Name: "big", Models: models}})

	choices := matchChoices(candidates, "")
	if len(choices) != autocompleteMax {
		t.Fatalf("got %d choices, want the cap of %d", len(choices), autocompleteMax)
	}
	if autocompleteMax != 25 {
		t.Fatalf("autocompleteMax = %d, but Discord allows 25", autocompleteMax)
	}
}

func providerNames(chain []config.Provider) string {
	names := make([]string, 0, len(chain))
	for _, p := range chain {
		names = append(names, p.Name)
	}
	return strings.Join(names, ",")
}
