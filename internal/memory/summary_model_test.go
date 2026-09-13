package memory

import (
	"context"
	"testing"

	"github.com/TheInternetUse7/yuna/internal/ai"
	"github.com/TheInternetUse7/yuna/internal/config"
	"github.com/TheInternetUse7/yuna/internal/store"
)

// The summariser must ask for the model the caller chose, not the provider's
// default: that is the whole point of separating SummaryModel from the chain.
func TestRefreshSummaryRequestsConfiguredModel(t *testing.T) {
	st := newMemoryTestStore(t)
	fake := &fakeSummarizer{result: &ai.SummaryResult{Summary: "summarised"}}
	m := NewManager(st, fake, Settings{
		SystemPrompt:  "persona",
		HistoryWindow: 15,
		SummaryEvery:  3,
		FactsPerUser:  50,
		FactsInject:   20,
		Enabled:       true,
		SummaryProvider: config.Provider{
			Name: "groq", Driver: "groq",
			Models: []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant"},
		},
		SummaryModel: "llama-3.1-8b-instant",
	}, nil)

	// SummaryEvery is 3, so a refresh only has material once three messages
	// have accumulated.
	for i := 0; i < 3; i++ {
		if _, err := st.InsertMessage(store.StoredMessage{
			MessageID: "m" + string(rune('a'+i)), ChannelID: "chan-1", GuildID: "guild-1",
			UserID: "user-a", Username: "alice", DisplayName: "Alice", Nickname: "ali",
			Role: store.RoleUser, Content: "hello", CreatedAt: int64(1000 + i),
		}); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	if err := m.refreshSummary(context.Background(), "chan-1", "guild-1"); err != nil {
		t.Fatalf("refreshSummary: %v", err)
	}
	if fake.model != "llama-3.1-8b-instant" {
		t.Fatalf("summariser asked for %q, want the configured summary model", fake.model)
	}
}
